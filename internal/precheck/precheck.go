package precheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wangdayong228/ydyl-deploy-client/internal/constants/enums"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
	"gopkg.in/yaml.v3"
)

const DefaultTimeout = 10 * time.Second

type Params struct {
	ConfigPath           string
	OPParamsTemplatePath string
	Timeout              time.Duration
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int    `json:"id"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type opParamsFile struct {
	OptimismPackage struct {
		OpContractDeployerParams struct {
			L1ArtifactsLocator string `yaml:"l1_artifacts_locator"`
			L2ArtifactsLocator string `yaml:"l2_artifacts_locator"`
		} `yaml:"op_contract_deployer_params"`
	} `yaml:"optimism_package"`
}

func Run(ctx context.Context, p Params) error {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cfg := deploy.LoadConfigFromFile(p.ConfigPath)
	client := &http.Client{Timeout: timeout}

	if hasOPService(cfg) {
		if err := checkOPArtifacts(ctx, client, p.OPParamsTemplatePath); err != nil {
			return err
		}
	}

	l1HTTP := strings.TrimSpace(cfg.L1RpcUrl)
	if l1HTTP == "" {
		return fmt.Errorf("配置项 l1RpcUrl 不能为空")
	}
	l1ChainHex, err := callHTTPJSONRPC(ctx, client, l1HTTP, "eth_chainId")
	if err != nil {
		return fmt.Errorf("l1RpcUrl 不通 (%s): %w", l1HTTP, err)
	}
	if err := matchConfiguredChainID("l1RpcUrl", l1HTTP, l1ChainHex, cfg.L1ChainId); err != nil {
		return err
	}
	globalChainID, err := parseChainID(l1ChainHex)
	if err != nil {
		return fmt.Errorf("l1RpcUrl (%s) 返回的 chainId 无法解析 %q: %w", l1HTTP, l1ChainHex, err)
	}

	if ws := strings.TrimSpace(cfg.L1RpcUrlWs); ws != "" {
		wsChainHex, err := callWSJSONRPC(ctx, timeout, ws, "eth_chainId")
		if err != nil {
			return fmt.Errorf("l1RpcUrlWs 不通 (%s): %w", ws, err)
		}
		if err := matchConfiguredChainID("l1RpcUrlWs", ws, wsChainHex, cfg.L1ChainId); err != nil {
			return err
		}
	}

	for _, svc := range cfg.Services {
		if svc.Type != enums.ServiceTypeOP || svc.Count == 0 {
			continue
		}
		opURL := strings.TrimSpace(svc.L1RpcUrl)
		if opURL == "" {
			log.Printf("警告：services.op.l1RpcUrl 为空，OP 将直连全局 l1RpcUrl（Confura），没有 jsonrpc-proxy")
			continue
		}
		if err := ensureDifferentRPCURL(opURL, l1HTTP); err != nil {
			return err
		}
		if err := checkOPProxy(ctx, client, opURL, cfg.L1ChainId, globalChainID); err != nil {
			return fmt.Errorf("jsonrpc-proxy-op 公网入口不通 (%s): %w；可能原因：YAML 仍是旧 Confura IP、proxy 未启动、或 Confura 本机 127.0.0.1:28545 未起来。请运行 setup-cfxnode.sh 的第 1 步（ONLY_UPDATE_CONFURA_IP=true 即可只重启 jsonrpc-proxy-op）", opURL, err)
		}
	}
	return nil
}

func hasOPService(cfg *deploy.DeployConfig) bool {
	for _, s := range cfg.Services {
		if s.Type == enums.ServiceTypeOP && s.Count > 0 {
			return true
		}
	}
	return false
}

func checkOPArtifacts(ctx context.Context, client *http.Client, templatePath string) error {
	if strings.TrimSpace(templatePath) == "" {
		var err error
		templatePath, err = defaultOPParamsTemplatePath()
		if err != nil {
			return err
		}
	}
	data, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("读取 OP params 模板失败 (%s): %w", templatePath, err)
	}
	var parsed opParamsFile
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("解析 OP params 模板失败 (%s): %w", templatePath, err)
	}
	l1 := strings.TrimSpace(parsed.OptimismPackage.OpContractDeployerParams.L1ArtifactsLocator)
	l2 := strings.TrimSpace(parsed.OptimismPackage.OpContractDeployerParams.L2ArtifactsLocator)
	if l1 == "" {
		return fmt.Errorf("OP params 模板缺少 l1_artifacts_locator (%s)", templatePath)
	}
	seen := map[string]struct{}{}
	for _, loc := range []string{l1, l2} {
		if loc == "" {
			continue
		}
		if _, ok := seen[loc]; ok {
			continue
		}
		seen[loc] = struct{}{}
		if err := checkHTTPAccessible(ctx, client, loc); err != nil {
			return err
		}
	}
	return nil
}

func defaultOPParamsTemplatePath() (string, error) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("获取当前文件路径失败")
	}
	p := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "op-work", "scripts", "params.template.yml")
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("解析 params.template.yml 路径失败: %w", err)
	}
	return filepath.Clean(abs), nil
}

func checkHTTPAccessible(ctx context.Context, client *http.Client, locator string) error {
	headReq, err := http.NewRequestWithContext(ctx, http.MethodHead, locator, nil)
	if err != nil {
		return fmt.Errorf("artifacts 请求构造失败 (%s): %w", locator, err)
	}
	headResp, headErr := client.Do(headReq)
	if headErr == nil {
		_ = headResp.Body.Close()
		if headResp.StatusCode >= 200 && headResp.StatusCode < 300 {
			return nil
		}
	}

	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, locator, nil)
	if err != nil {
		return fmt.Errorf("artifacts 请求构造失败 (%s): %w", locator, err)
	}
	getResp, getErr := client.Do(getReq)
	if getErr != nil {
		if headErr != nil {
			return fmt.Errorf("artifacts 无法访问 %s: %w", locator, getErr)
		}
		return fmt.Errorf("artifacts 无法访问 %s: HTTP %d", locator, headResp.StatusCode)
	}
	defer getResp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(getResp.Body, 64))
	if getResp.StatusCode >= 200 && getResp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("artifacts 无法访问 %s: HTTP %d", locator, getResp.StatusCode)
}

func checkOPProxy(ctx context.Context, client *http.Client, opURL, configuredChainID string, globalChainID *big.Int) error {
	chainHex, err := callHTTPJSONRPC(ctx, client, opURL, "eth_chainId")
	if err != nil {
		return fmt.Errorf("eth_chainId: %w", err)
	}
	if err := matchConfiguredChainID("services.op.l1RpcUrl", opURL, chainHex, configuredChainID); err != nil {
		return err
	}
	got, err := parseChainID(chainHex)
	if err != nil {
		return fmt.Errorf("eth_chainId 无法解析 %q: %w", chainHex, err)
	}
	if globalChainID != nil && got.Cmp(globalChainID) != 0 {
		return fmt.Errorf("chainId 与全局 l1RpcUrl 不一致: proxy=%s global=%s", got.String(), globalChainID.String())
	}
	if _, err := callHTTPJSONRPC(ctx, client, opURL, "eth_blockNumber"); err != nil {
		return fmt.Errorf("eth_blockNumber: %w", err)
	}
	return nil
}

func ensureDifferentRPCURL(opURL, globalURL string) error {
	a, err := canonicalRPCURL(opURL)
	if err != nil {
		return fmt.Errorf("services.op.l1RpcUrl 无效 (%s): %w", opURL, err)
	}
	b, err := canonicalRPCURL(globalURL)
	if err != nil {
		return fmt.Errorf("l1RpcUrl 无效 (%s): %w", globalURL, err)
	}
	if a == b {
		return fmt.Errorf("services.op.l1RpcUrl (%s) 与全局 l1RpcUrl 规范化后相同；OP 需要 jsonrpc-proxy（通常 :3031）而不是 Confura /espace", opURL)
	}
	return nil
}

func canonicalRPCURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("缺少 scheme 或 host")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + u.Path, nil
}

func matchConfiguredChainID(label, endpoint, gotHex, configured string) error {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return nil
	}
	got, err := parseChainID(gotHex)
	if err != nil {
		return fmt.Errorf("%s (%s) 返回的 chainId 无法解析 %q: %w", label, endpoint, gotHex, err)
	}
	want, err := parseChainID(configured)
	if err != nil {
		return fmt.Errorf("配置项 l1ChainId 无法解析 %q: %w", configured, err)
	}
	if got.Cmp(want) != 0 {
		return fmt.Errorf("%s (%s) chainId 与配置不一致: rpc=%s config=%s", label, endpoint, got.String(), want.String())
	}
	return nil
}

func parseChainID(s string) (*big.Int, error) {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	if s == "" {
		return nil, fmt.Errorf("空 chainId")
	}
	n := new(big.Int)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		if _, ok := n.SetString(s[2:], 16); !ok {
			return nil, fmt.Errorf("无效 hex chainId %q", s)
		}
		return n, nil
	}
	if _, ok := n.SetString(s, 10); !ok {
		return nil, fmt.Errorf("无效 chainId %q", s)
	}
	return n, nil
}

func callHTTPJSONRPC(ctx context.Context, client *http.Client, endpoint, method string) (string, error) {
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: []any{}, ID: 1})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseRPCResult(body)
}

func callWSJSONRPC(ctx context.Context, timeout time.Duration, endpoint, method string) (string, error) {
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: []any{}, ID: 1})
	if err != nil {
		return "", err
	}
	dialer := websocket.Dialer{HandshakeTimeout: timeout}
	conn, _, err := dialer.DialContext(ctx, endpoint, nil)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return "", err
	}
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return "", err
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return "", err
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		return "", err
	}
	return parseRPCResult(msg)
}

func parseRPCResult(body []byte) (string, error) {
	var rpc rpcResponse
	if err := json.Unmarshal(body, &rpc); err != nil {
		return "", fmt.Errorf("解析 JSON-RPC 响应失败: %w", err)
	}
	if rpc.Error != nil {
		return "", fmt.Errorf("JSON-RPC error %d: %s", rpc.Error.Code, rpc.Error.Message)
	}
	if len(rpc.Result) == 0 || string(rpc.Result) == "null" {
		return "", fmt.Errorf("JSON-RPC 无 result")
	}
	var asString string
	if err := json.Unmarshal(rpc.Result, &asString); err == nil {
		return asString, nil
	}
	return strings.TrimSpace(string(rpc.Result)), nil
}

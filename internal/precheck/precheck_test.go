package precheck

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/websocket"
)

func TestRun_SucceedsWhenAllEndpointsOK(t *testing.T) {
	artifactSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		t.Errorf("HEAD 已成功时不应 GET，method=%s", r.Method)
	}))
	t.Cleanup(artifactSrv.Close)

	rpcSrv := newJSONRPCServer(t, "0x1de7", "0x10")
	t.Cleanup(rpcSrv.Close)

	wsSrv := newWSJSONRPCServer(t, "0x1de7")
	t.Cleanup(wsSrv.Close)

	proxySrv := newJSONRPCServer(t, "0x1de7", "0x10")
	t.Cleanup(proxySrv.Close)

	cfgPath, tmplPath := writeOPConfig(t, rpcSrv.URL+"/espace", wsURL(wsSrv.URL), proxySrv.URL, artifactSrv.URL, artifactSrv.URL)

	if err := Run(t.Context(), Params{
		ConfigPath:           cfgPath,
		OPParamsTemplatePath: tmplPath,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRun_SkipsArtifactsWhenNoOP(t *testing.T) {
	rpcSrv := newJSONRPCServer(t, "0x1de7", "0x10")
	t.Cleanup(rpcSrv.Close)
	wsSrv := newWSJSONRPCServer(t, "0x1de7")
	t.Cleanup(wsSrv.Close)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	mustWrite(t, cfgPath, testConfigYAML(rpcSrv.URL, wsURL(wsSrv.URL), "", 0, "7655"))

	missingTmpl := filepath.Join(dir, "missing-params.template.yml")
	if err := Run(t.Context(), Params{
		ConfigPath:           cfgPath,
		OPParamsTemplatePath: missingTmpl,
	}); err != nil {
		t.Fatalf("无 OP 时不应读 artifacts 模板: %v", err)
	}
}

func TestRun_FailsWhenArtifactsNotFound(t *testing.T) {
	artifactSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(artifactSrv.Close)

	rpcSrv := newJSONRPCServer(t, "0x1de7", "0x10")
	t.Cleanup(rpcSrv.Close)
	wsSrv := newWSJSONRPCServer(t, "0x1de7")
	t.Cleanup(wsSrv.Close)
	proxySrv := newJSONRPCServer(t, "0x1de7", "0x10")
	t.Cleanup(proxySrv.Close)

	cfgPath, tmplPath := writeOPConfig(t, rpcSrv.URL+"/espace", wsURL(wsSrv.URL), proxySrv.URL, artifactSrv.URL, artifactSrv.URL)

	err := Run(t.Context(), Params{
		ConfigPath:           cfgPath,
		OPParamsTemplatePath: tmplPath,
	})
	if err == nil {
		t.Fatal("期望 artifacts 失败")
	}
	if !strings.Contains(err.Error(), artifactSrv.URL) {
		t.Fatalf("错误应包含 artifacts URL，got=%v", err)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("错误应包含 HTTP status，got=%v", err)
	}
}

func TestRun_ArtifactsHEAD405ThenGETSucceeds(t *testing.T) {
	var gotGET atomic.Bool
	artifactSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Method == http.MethodGet {
			gotGET.Store(true)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	t.Cleanup(artifactSrv.Close)

	rpcSrv := newJSONRPCServer(t, "0x1de7", "0x10")
	t.Cleanup(rpcSrv.Close)
	wsSrv := newWSJSONRPCServer(t, "0x1de7")
	t.Cleanup(wsSrv.Close)
	proxySrv := newJSONRPCServer(t, "0x1de7", "0x10")
	t.Cleanup(proxySrv.Close)

	cfgPath, tmplPath := writeOPConfig(t, rpcSrv.URL+"/espace", wsURL(wsSrv.URL), proxySrv.URL, artifactSrv.URL, artifactSrv.URL)

	if err := Run(t.Context(), Params{
		ConfigPath:           cfgPath,
		OPParamsTemplatePath: tmplPath,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !gotGET.Load() {
		t.Fatal("HEAD 非 2xx 时应 fallback GET")
	}
}

func TestRun_FailsWhenL1HTTPChainIDMismatch(t *testing.T) {
	rpcSrv := newJSONRPCServer(t, "0x1", "0x10")
	t.Cleanup(rpcSrv.Close)
	wsSrv := newWSJSONRPCServer(t, "0x1de7")
	t.Cleanup(wsSrv.Close)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	mustWrite(t, cfgPath, testConfigYAML(rpcSrv.URL, wsURL(wsSrv.URL), "", 0, "7655"))

	err := Run(t.Context(), Params{ConfigPath: cfgPath})
	if err == nil {
		t.Fatal("期望 chainId 不一致失败")
	}
	if !strings.Contains(err.Error(), "l1RpcUrl") {
		t.Fatalf("错误应提到 l1RpcUrl，got=%v", err)
	}
}

func TestRun_SkipsWSWhenEmpty(t *testing.T) {
	rpcSrv := newJSONRPCServer(t, "0x1de7", "0x10")
	t.Cleanup(rpcSrv.Close)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	mustWrite(t, cfgPath, testConfigYAML(rpcSrv.URL, "", "", 0, "7655"))

	if err := Run(t.Context(), Params{ConfigPath: cfgPath}); err != nil {
		t.Fatalf("空 WS 应跳过: %v", err)
	}
}

func TestRun_FailsWhenOPProxySameAsGlobal(t *testing.T) {
	rpcSrv := newJSONRPCServer(t, "0x1de7", "0x10")
	t.Cleanup(rpcSrv.Close)
	wsSrv := newWSJSONRPCServer(t, "0x1de7")
	t.Cleanup(wsSrv.Close)
	artifactSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(artifactSrv.Close)

	same := rpcSrv.URL + "/espace"
	cfgPath, tmplPath := writeOPConfig(t, same, wsURL(wsSrv.URL), same+"/", artifactSrv.URL, artifactSrv.URL)

	err := Run(t.Context(), Params{
		ConfigPath:           cfgPath,
		OPParamsTemplatePath: tmplPath,
	})
	if err == nil {
		t.Fatal("期望 op l1RpcUrl 与全局相同失败")
	}
	if !strings.Contains(err.Error(), "jsonrpc-proxy") && !strings.Contains(err.Error(), "l1RpcUrl") {
		t.Fatalf("错误应指出 URL 冲突，got=%v", err)
	}
}

func TestRun_FailsWhenOPProxyDown_MentionsSetupCfxnodeStep1(t *testing.T) {
	rpcSrv := newJSONRPCServer(t, "0x1de7", "0x10")
	t.Cleanup(rpcSrv.Close)
	wsSrv := newWSJSONRPCServer(t, "0x1de7")
	t.Cleanup(wsSrv.Close)
	artifactSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(artifactSrv.Close)

	deadProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	deadURL := deadProxy.URL
	deadProxy.Close()

	cfgPath, tmplPath := writeOPConfig(t, rpcSrv.URL+"/espace", wsURL(wsSrv.URL), deadURL, artifactSrv.URL, artifactSrv.URL)

	err := Run(t.Context(), Params{
		ConfigPath:           cfgPath,
		OPParamsTemplatePath: tmplPath,
	})
	if err == nil {
		t.Fatal("期望 proxy 不通失败")
	}
	msg := err.Error()
	if !strings.Contains(msg, "jsonrpc-proxy-op") {
		t.Fatalf("错误应写明 jsonrpc-proxy-op，got=%v", err)
	}
	if !strings.Contains(msg, "setup-cfxnode.sh") || !strings.Contains(msg, "第 1 步") {
		t.Fatalf("错误应提示 setup-cfxnode.sh 第 1 步，got=%v", err)
	}
	if !strings.Contains(msg, "ONLY_UPDATE_CONFURA_IP=true") {
		t.Fatalf("错误应提示 ONLY_UPDATE_CONFURA_IP=true，got=%v", err)
	}
}

func TestRun_ChecksDistinctL2ArtifactsLocator(t *testing.T) {
	var l1Hits, l2Hits atomic.Int32
	l1Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l1Hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(l1Srv.Close)
	l2Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l2Hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(l2Srv.Close)

	rpcSrv := newJSONRPCServer(t, "0x1de7", "0x10")
	t.Cleanup(rpcSrv.Close)
	wsSrv := newWSJSONRPCServer(t, "0x1de7")
	t.Cleanup(wsSrv.Close)
	proxySrv := newJSONRPCServer(t, "0x1de7", "0x10")
	t.Cleanup(proxySrv.Close)

	cfgPath, tmplPath := writeOPConfig(t, rpcSrv.URL+"/espace", wsURL(wsSrv.URL), proxySrv.URL, l1Srv.URL+"/a.tar.gz", l2Srv.URL+"/b.tar.gz")

	if err := Run(t.Context(), Params{
		ConfigPath:           cfgPath,
		OPParamsTemplatePath: tmplPath,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if l1Hits.Load() == 0 || l2Hits.Load() == 0 {
		t.Fatalf("不同 locator 应各查一次，l1=%d l2=%d", l1Hits.Load(), l2Hits.Load())
	}
}

func TestRun_EmptyOPL1RpcUrlDoesNotFail(t *testing.T) {
	rpcSrv := newJSONRPCServer(t, "0x1de7", "0x10")
	t.Cleanup(rpcSrv.Close)
	wsSrv := newWSJSONRPCServer(t, "0x1de7")
	t.Cleanup(wsSrv.Close)
	artifactSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(artifactSrv.Close)

	cfgPath, tmplPath := writeOPConfig(t, rpcSrv.URL+"/espace", wsURL(wsSrv.URL), "", artifactSrv.URL, artifactSrv.URL)

	if err := Run(t.Context(), Params{
		ConfigPath:           cfgPath,
		OPParamsTemplatePath: tmplPath,
	}); err != nil {
		t.Fatalf("空 op.l1RpcUrl 应警告但不失败: %v", err)
	}
}

func writeOPConfig(t *testing.T, l1HTTP, l1WS, opL1, l1Art, l2Art string) (cfgPath, tmplPath string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath = filepath.Join(dir, "config.yaml")
	tmplPath = filepath.Join(dir, "params.template.yml")
	mustWrite(t, cfgPath, testConfigYAML(l1HTTP, l1WS, opL1, 1, "7655"))
	mustWrite(t, tmplPath, fmt.Sprintf(`
optimism_package:
  op_contract_deployer_params:
    l1_artifacts_locator: %q
    l2_artifacts_locator: %q
`, l1Art, l2Art))
	return cfgPath, tmplPath
}

func testConfigYAML(l1HTTP, l1WS, opL1 string, opCount uint, chainID string) string {
	opURLLine := ""
	if opL1 != "" {
		opURLLine = fmt.Sprintf("    l1RpcUrl: %q", opL1)
	} else {
		opURLLine = `    l1RpcUrl: ""`
	}
	return fmt.Sprintf(`
region: us-west-2
securityGroupId: sg-test
diskSizeGiB: 100
runDuration: 1h
sshUser: ubuntu
sshKeyDir: ""
sshMaxConcurrency: 4
sshReadyRetryCount: 3
sshReadyRetryInterval: 1s
keyName: test-key
logDir: logs
outputDir: output
benchClientIP: ""
l1ChainId: %q
l1RpcUrl: %q
l1RpcUrlWs: %q
l1VaultMnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
l1BridgeHubContract: "0x00000000000000000000000000000000000000ff"
l1RegisterBridgePrivateKey: "0x1111111111111111111111111111111111111111111111111111111111111111"
dryRun: true
forceDeployL2Chain: false
enableGenAccounts: false
enableBridge: true
cdkUseRealProver: false
faultGameMaxClockDuration: "600"
l1FundVaultEth: 5000
l1FundClaimServiceEth: 1000
l1FundRegisterBridgeEth: 1000
services:
  - type: op
    count: %d
    ami: ami-test
    instanceType: [c6a.xlarge]
    tagPrefix: t
    remoteCmd: ""
%s
    l1VaultFundAmount: 1000
`, chainID, l1HTTP, l1WS, opCount, opURLLine)
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func newJSONRPCServer(t *testing.T, chainID, blockNumber string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result := chainID
		if req.Method == "eth_blockNumber" {
			result = blockNumber
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(req.ID),
			"result":  result,
		})
	}))
}

func newWSJSONRPCServer(t *testing.T, chainID string) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			var req struct {
				Method string          `json:"method"`
				ID     json.RawMessage `json:"id"`
			}
			if err := json.Unmarshal(msg, &req); err != nil {
				return
			}
			resp, _ := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      json.RawMessage(req.ID),
				"result":  chainID,
			})
			if err := c.WriteMessage(websocket.TextMessage, resp); err != nil {
				return
			}
		}
	}))
}

package chainhealth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openweb3/web3go"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
	ydylconsolesdk "github.com/wangdayong228/ydyl-deploy-client/pkg/ydyl-console-service-sdk"
)

const (
	DefaultStaleAfter   = 2 * time.Minute
	DefaultConsolePort  = 8080
	DefaultXJSTRPCPort  = 30010
	DefaultRPCTimeout   = 10 * time.Second
	summaryFetchTimeout = 10 * time.Second
)

// Status 表示链端点的活跃状态。
type Status string

const (
	StatusOK   Status = "OK"
	StatusSlow Status = "SLOW"
	StatusDown Status = "DOWN"
)

// Emoji 返回对应状态的可视化标识。
func (s Status) Emoji() string {
	switch s {
	case StatusOK:
		return "✅"
	case StatusSlow:
		return "⚠️"
	default:
		return "❌"
	}
}

// EndpointHealth 保存单个 RPC 端点的健康检查结果。
type EndpointHealth struct {
	RPCURL      string
	BlockNumber uint64
	BlockTime   time.Time
	Age         time.Duration
	Status      Status
	Error       string
}

// NodeHealth 保存一个已部署节点的 L2 健康状态。
type NodeHealth struct {
	Name        string
	IP          string
	ServiceType string
	L2          EndpointHealth
}

// Overall 返回当前节点的总体状态（仅 L2）。
func (n NodeHealth) Overall() Status {
	return n.L2.Status
}

// Params 控制 Check 函数的行为。
type Params struct {
	ServersPath string
	// StaleAfter 超出该时长判定为出块缓慢，默认 2 分钟。
	StaleAfter time.Duration
	// ConsolePort 是 ydyl-console-service 的监听端口，默认 8080。
	ConsolePort int
	// XJSTRPCPort 是 XJST RPC 监听端口，默认 30010。
	XJSTRPCPort int
	// RPCTimeout 是单次 RPC 调用的超时时间，默认 10s。
	RPCTimeout time.Duration
}

// Check 加载 servers.json，筛选可检查的目标并并发探测每条链 L2 最新区块。
// 规则：
// - op/cdk: 全部检查，RPC 地址来自 console-service 的 summary.L2_RPC_URL
// - xjst: 仅检查 name 末段为 "-1" 的主节点，RPC 固定使用 http://<ip>:30010
func Check(ctx context.Context, p Params) ([]NodeHealth, error) {
	if p.StaleAfter <= 0 {
		p.StaleAfter = DefaultStaleAfter
	}
	if p.ConsolePort <= 0 {
		p.ConsolePort = DefaultConsolePort
	}
	if p.RPCTimeout <= 0 {
		p.RPCTimeout = DefaultRPCTimeout
	}
	if p.XJSTRPCPort <= 0 {
		p.XJSTRPCPort = DefaultXJSTRPCPort
	}

	servers, err := loadServers(p.ServersPath)
	if err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("servers.json 为空或不含任何节点")
	}

	targets := pickStatusTargets(servers)
	if len(targets) == 0 {
		return nil, fmt.Errorf("servers.json 中没有可检查的目标（仅支持 op/cdk 与 xjst 主节点）")
	}

	results := make([]NodeHealth, len(targets))
	var wg sync.WaitGroup
	for i, s := range targets {
		wg.Add(1)
		go func(idx int, server deploy.ServerInfo) {
			defer wg.Done()
			results[idx] = checkNode(ctx, server, p)
		}(i, s)
	}
	wg.Wait()
	return results, nil
}

func checkNode(ctx context.Context, server deploy.ServerInfo, p Params) NodeHealth {
	h := NodeHealth{
		Name:        strings.TrimSpace(server.Name),
		IP:          strings.TrimSpace(server.IP),
		ServiceType: strings.ToLower(strings.TrimSpace(server.ServiceType)),
	}

	switch h.ServiceType {
	case "xjst":
		l2URL := fmt.Sprintf("http://%s:%d", h.IP, p.XJSTRPCPort)
		h.L2 = probeXjstWithCfxRPC(ctx, l2URL, p.RPCTimeout, p.StaleAfter)
		h.L2.RPCURL = l2URL
	case "op", "cdk":
		l2URL, err := resolveL2RPCURL(ctx, h.IP, p.ConsolePort)
		if err != nil {
			h.L2 = EndpointHealth{
				Status: StatusDown,
				Error:  fmt.Sprintf("获取 L2 RPC 失败: %v", err),
			}
			return h
		}
		h.L2 = probeOpOrCdkWithWeb3go(ctx, l2URL, p.RPCTimeout, p.StaleAfter)
		h.L2.RPCURL = l2URL
	default:
		h.L2 = EndpointHealth{
			Status: StatusDown,
			Error:  fmt.Sprintf("不支持的 serviceType=%q", h.ServiceType),
		}
	}
	return h
}

func resolveL2RPCURL(ctx context.Context, serverIP string, consolePort int) (string, error) {
	baseURL := fmt.Sprintf("http://%s:%d", serverIP, consolePort)
	sdk := ydylconsolesdk.New(baseURL)

	summaryCtx, cancel := context.WithTimeout(ctx, summaryFetchTimeout)
	defer cancel()

	summary, err := sdk.Result.GetDeploySummary(summaryCtx)
	if err != nil {
		return "", err
	}
	l2URL := replaceLocalhostWithIP(strings.TrimSpace(summary.L2_RPC_URL), serverIP)
	if l2URL == "" {
		return "", fmt.Errorf("summary.L2_RPC_URL 为空")
	}
	return l2URL, nil
}

func probeOpOrCdkWithWeb3go(ctx context.Context, rpcURL string, rpcTimeout, staleAfter time.Duration) EndpointHealth {
	probeCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := web3go.NewClient(rpcURL)
	if err != nil {
		return EndpointHealth{Status: StatusDown, Error: fmt.Sprintf("创建 web3go client 失败: %v", err)}
	}

	var blockNumHex string
	if err := client.Provider().CallContext(probeCtx, &blockNumHex, "eth_blockNumber"); err != nil {
		return EndpointHealth{Status: StatusDown, Error: fmt.Sprintf("调用 eth_blockNumber 失败: %v", err)}
	}
	blockNum, err := parseUintString(blockNumHex)
	if err != nil {
		return EndpointHealth{Status: StatusDown, Error: fmt.Sprintf("解析 eth_blockNumber 返回值失败: %v", err)}
	}

	var block struct {
		Number    string `json:"number"`
		Timestamp string `json:"timestamp"`
	}
	if err := client.Provider().CallContext(probeCtx, &block, "eth_getBlockByNumber", blockNumHex, false); err != nil {
		return EndpointHealth{Status: StatusDown, Error: fmt.Sprintf("调用 eth_getBlockByNumber 失败: %v", err)}
	}
	blockTime, err := parseTimestamp(block.Timestamp)
	if err != nil {
		return EndpointHealth{Status: StatusDown, Error: fmt.Sprintf("解析区块时间失败: %v", err)}
	}

	if parsedNumber, parseErr := parseUintString(block.Number); parseErr == nil {
		blockNum = parsedNumber
	}
	age := time.Since(blockTime)
	return EndpointHealth{
		BlockNumber: blockNum,
		BlockTime:   blockTime,
		Age:         age,
		Status:      statusByAge(age, staleAfter),
	}
}

func probeXjstWithCfxRPC(ctx context.Context, rpcURL string, rpcTimeout, staleAfter time.Duration) EndpointHealth {
	probeCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	client, err := web3go.NewClient(rpcURL)
	if err != nil {
		return EndpointHealth{Status: StatusDown, Error: fmt.Sprintf("创建 web3go client 失败: %v", err)}
	}

	var epochHex string
	if err := client.Provider().CallContext(probeCtx, &epochHex, "cfx_epochNumber"); err != nil {
		return EndpointHealth{Status: StatusDown, Error: fmt.Sprintf("调用 cfx_epochNumber 失败: %v", err)}
	}
	blockNum, err := parseUintString(epochHex)
	if err != nil {
		return EndpointHealth{Status: StatusDown, Error: fmt.Sprintf("解析 cfx_epochNumber 返回值失败: %v", err)}
	}

	var block struct {
		Timestamp   string `json:"timestamp"`
		EpochNumber string `json:"epochNumber"`
	}
	if err := client.Provider().CallContext(probeCtx, &block, "cfx_getBlockByEpochNumber", epochHex, false); err != nil {
		return EndpointHealth{Status: StatusDown, Error: fmt.Sprintf("调用 cfx_getBlockByEpochNumber 失败: %v", err)}
	}
	blockTime, err := parseTimestamp(block.Timestamp)
	if err != nil {
		return EndpointHealth{Status: StatusDown, Error: fmt.Sprintf("解析区块时间失败: %v", err)}
	}
	if parsedEpoch, parseErr := parseUintString(block.EpochNumber); parseErr == nil {
		blockNum = parsedEpoch
	}

	age := time.Since(blockTime)
	return EndpointHealth{
		BlockNumber: blockNum,
		BlockTime:   blockTime,
		Age:         age,
		Status:      statusByAge(age, staleAfter),
	}
}

func statusByAge(age, staleAfter time.Duration) Status {
	if age > staleAfter {
		return StatusSlow
	}
	return StatusOK
}

func parseTimestamp(v string) (time.Time, error) {
	sec, err := parseUintString(v)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(int64(sec), 0), nil
}

func parseUintString(v string) (uint64, error) {
	raw := strings.TrimSpace(v)
	if raw == "" {
		return 0, fmt.Errorf("空字符串")
	}
	n, err := strconv.ParseUint(raw, 0, 64)
	if err == nil {
		return n, nil
	}
	if strings.HasPrefix(raw, "0x") || strings.HasPrefix(raw, "0X") {
		return 0, err
	}
	hexNum, hexErr := strconv.ParseUint(raw, 16, 64)
	if hexErr != nil {
		return 0, err
	}
	return hexNum, nil
}

func pickStatusTargets(servers []deploy.ServerInfo) []deploy.ServerInfo {
	result := make([]deploy.ServerInfo, 0, len(servers))
	for _, s := range servers {
		t := strings.ToLower(strings.TrimSpace(s.ServiceType))
		ip := strings.TrimSpace(s.IP)
		name := strings.TrimSpace(s.Name)
		if ip == "" {
			continue
		}

		switch t {
		case "op", "cdk":
			result = append(result, deploy.ServerInfo{
				IP:          ip,
				ServiceType: t,
				Name:        name,
			})
		case "xjst":
			if !isXjstPrimaryNode(name) {
				continue
			}
			result = append(result, deploy.ServerInfo{
				IP:          ip,
				ServiceType: t,
				Name:        name,
			})
		}
	}
	return result
}

func isXjstPrimaryNode(name string) bool {
	parts := strings.Split(strings.TrimSpace(name), "-")
	if len(parts) < 4 {
		return false
	}
	if strings.ToLower(parts[len(parts)-3]) != "xjst" {
		return false
	}
	return parts[len(parts)-1] == "1"
}

func loadServers(path string) ([]deploy.ServerInfo, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 servers 文件失败: %w", err)
	}
	var servers []deploy.ServerInfo
	if err := json.Unmarshal(b, &servers); err != nil {
		return nil, fmt.Errorf("解析 servers 文件失败: %w", err)
	}
	return servers, nil
}

// replaceLocalhostWithIP 将 RPC URL 中的 localhost / 127.0.0.1 / 私有 IP 替换为 serverIP。
func replaceLocalhostWithIP(rawURL, serverIP string) string {
	if rawURL == "" || serverIP == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		r := strings.NewReplacer("127.0.0.1", serverIP, "localhost", serverIP)
		return r.Replace(rawURL)
	}
	if !shouldReplaceHost(u.Hostname()) {
		return rawURL
	}
	port := u.Port()
	if port == "" {
		u.Host = serverIP
	} else {
		u.Host = net.JoinHostPort(serverIP, port)
	}
	return u.String()
}

func shouldReplaceHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4[0] == 10 ||
		(v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31) ||
		(v4[0] == 192 && v4[1] == 168)
}

package crosstxconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tyler-smith/go-bip39"

	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
	ydylconsolesdk "github.com/wangdayong228/ydyl-deploy-client/pkg/ydyl-console-service-sdk"
)

// Job 对齐 zk-claim-service/scripts/7s_jobs.json 的结构。
type Job struct {
	TargetL1Bridge                  string `json:"target_l1_bridge"`
	SourceL2Bridge                  string `json:"source_l2_bridge"`
	TargetL2Contract                string `json:"target_l2_contract"`
	L1BridgeReceiver                string `json:"l1_bridge_receiver"`
	SourceL2RPC                     string `json:"source_l2_rpc"`
	Mnemonic                        string `json:"mnemonic"`
	TxAmount                        int    `json:"tx_amount"`
	SourceL2ChainType               string `json:"source_l2_chain_type"`
	TargetL2ChainType               string `json:"target_l2_chain_type"`
	SourceL2BalanceSenderPrivatekey string `json:"source_l2_balance_sender_privatekey,omitempty"`

	TargetL2RPC    string `json:"target_l2_rpc"`
	TargetL2Bridge string `json:"target_l2_bridge"`
	BlockRange     int64  `json:"block_range"`
}

type ChainInfo struct {
	Type string
	IP   string

	Summary   *ydylconsolesdk.SummaryResultResponse
	Contracts *ydylconsolesdk.NodeDeploymentContractsResponse
}

type Fetcher interface {
	Fetch(ctx context.Context, chainType, ip string) (*ChainInfo, error)
}

// SDKFetcher 通过固定的 http://<ip>:8080 访问 ydyl-console-service。
type SDKFetcher struct{}

func (f SDKFetcher) Fetch(ctx context.Context, chainType, ip string) (*ChainInfo, error) {
	baseURL := fmt.Sprintf("http://%s:8080", strings.TrimSpace(ip))
	sdk := ydylconsolesdk.New(baseURL)

	summary, err := sdk.Result.GetDeploySummary(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取 summary 失败: chainType=%s ip=%s baseURL=%s: %w", chainType, ip, baseURL, err)
	}
	contracts, err := sdk.Result.GetNodeDeploymentContracts(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取 node-deployment-contracts 失败: chainType=%s ip=%s baseURL=%s: %w", chainType, ip, baseURL, err)
	}

	return &ChainInfo{
		Type:      chainType,
		IP:        ip,
		Summary:   summary,
		Contracts: contracts,
	}, nil
}

type GenerateParams struct {
	ServersPath string
	OutPath     string
	TxAmount    int
	BlockRange  int64

	// Mnemonic 可选：为空则随机生成一次（12 words），所有 jobs 复用同一个。
	Mnemonic string
}

type GenerateResult struct {
	OutPath   string
	Mnemonic  string
	Chains    []string
	JobsCount int
}

func Generate(ctx context.Context, p GenerateParams) (*GenerateResult, error) {
	return GenerateWithFetcher(ctx, p, SDKFetcher{})
}

func GenerateWithFetcher(ctx context.Context, p GenerateParams, fetcher Fetcher) (*GenerateResult, error) {
	if p.ServersPath == "" {
		return nil, fmt.Errorf("serversPath 不能为空")
	}
	if p.OutPath == "" {
		return nil, fmt.Errorf("outPath 不能为空")
	}
	if p.TxAmount <= 0 {
		return nil, fmt.Errorf("txAmount 必须 > 0")
	}
	if p.BlockRange <= 0 {
		return nil, fmt.Errorf("blockRange 必须 > 0")
	}

	servers, err := LoadServers(p.ServersPath)
	if err != nil {
		return nil, err
	}

	chainTypeToIP, err := PickChainEntryIPs(servers)
	if err != nil {
		return nil, err
	}
	if len(chainTypeToIP) < 2 {
		return nil, fmt.Errorf("可用链数量不足（需要至少 2 条链），当前=%d", len(chainTypeToIP))
	}

	chainTypes := make([]string, 0, len(chainTypeToIP))
	for t := range chainTypeToIP {
		chainTypes = append(chainTypes, t)
	}
	sort.Strings(chainTypes)

	infos := make(map[string]*ChainInfo, len(chainTypes))
	for _, t := range chainTypes {
		ip := chainTypeToIP[t]
		info, err := fetcher.Fetch(ctx, t, ip)
		if err != nil {
			return nil, err
		}
		infos[t] = info
	}

	mnemonic := strings.TrimSpace(p.Mnemonic)
	if mnemonic == "" {
		mn, err := GenerateMnemonic12()
		if err != nil {
			return nil, err
		}
		mnemonic = mn
	}

	jobs := GenerateJobs(chainTypes, infos, mnemonic, p.TxAmount, p.BlockRange)
	if err := WriteJSONFile(p.OutPath, jobs); err != nil {
		return nil, err
	}

	return &GenerateResult{
		OutPath:   p.OutPath,
		Mnemonic:  mnemonic,
		Chains:    chainTypes,
		JobsCount: len(jobs),
	}, nil
}

func LoadServers(path string) ([]deploy.ServerInfo, error) {
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

// PickChainEntryIPs 从 servers 列表中为每种链类型挑选一个入口 IP。
// 仅支持 op/cdk/xjst；遇到其它类型直接返回错误（避免静默生成不完整配置）。
func PickChainEntryIPs(servers []deploy.ServerInfo) (map[string]string, error) {
	chainTypeToIP := make(map[string]string)
	for _, s := range servers {
		t := strings.ToLower(strings.TrimSpace(s.ServiceType))
		if t == "" {
			continue
		}
		switch t {
		case "op", "cdk", "xjst":
			if _, exists := chainTypeToIP[t]; exists {
				continue
			}
			ip := strings.TrimSpace(s.IP)
			if ip == "" {
				return nil, fmt.Errorf("servers.json 存在空 ip: serviceType=%s", t)
			}
			chainTypeToIP[t] = ip
		default:
			return nil, fmt.Errorf("不支持的 serviceType=%q（仅支持 op/cdk/xjst）", s.ServiceType)
		}
	}
	return chainTypeToIP, nil
}

// GenerateJobs 生成所有有向链对 a->b（a!=b）的 jobs。
// 注意：该函数仅做组合与字段映射；不做网络/文件 IO，便于测试。
func GenerateJobs(chainTypes []string, infos map[string]*ChainInfo, mnemonic string, txAmount int, blockRange int64) []Job {
	jobs := make([]Job, 0, len(chainTypes)*len(chainTypes))
	for _, srcType := range chainTypes {
		for _, dstType := range chainTypes {
			if srcType == dstType {
				continue
			}

			source := infos[srcType]
			target := infos[dstType]

			jobs = append(jobs, Job{
				TargetL1Bridge:                  target.Contracts.L1Bridge.Hex(),
				SourceL2Bridge:                  source.Contracts.L2Bridge.Hex(),
				TargetL2Contract:                target.Summary.L2_COUNTER_CONTRACT.Hex(),
				L1BridgeReceiver:                source.Summary.L1_BRIDGE_HUB_CONTRACT.Hex(),
				SourceL2RPC:                     source.Summary.L2_RPC_URL,
				Mnemonic:                        mnemonic,
				TxAmount:                        txAmount,
				SourceL2ChainType:               source.Type,
				TargetL2ChainType:               target.Type,
				SourceL2BalanceSenderPrivatekey: source.Summary.L2_PRIVATE_KEY.Hex(),

				TargetL2RPC:    target.Summary.L2_RPC_URL,
				TargetL2Bridge: target.Contracts.L2Bridge.Hex(),
				BlockRange:     blockRange,
			})
		}
	}
	return jobs
}

func GenerateMnemonic12() (string, error) {
	entropy, err := bip39.NewEntropy(128)
	if err != nil {
		return "", fmt.Errorf("生成 entropy 失败: %w", err)
	}
	m, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return "", fmt.Errorf("生成 mnemonic 失败: %w", err)
	}
	return m, nil
}

func WriteJSONFile(path string, v any) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建输出目录失败: %w", err)
		}
	}

	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化输出 JSON 失败: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("写出文件失败: %w", err)
	}
	return nil
}

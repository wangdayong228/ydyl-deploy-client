package crosstxconfig

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
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

	chainEntries, err := PickChainEntries(servers)
	if err != nil {
		return nil, err
	}
	if len(chainEntries) < 2 {
		return nil, fmt.Errorf("可用链数量不足（需要至少 2 条链），当前=%d", len(chainEntries))
	}

	chainKeys := make([]string, 0, len(chainEntries))
	for name := range chainEntries {
		chainKeys = append(chainKeys, name)
	}
	sort.Strings(chainKeys)

	infos := make(map[string]*ChainInfo, len(chainKeys))
	for _, key := range chainKeys {
		entry := chainEntries[key]
		info, err := fetcher.Fetch(ctx, entry.ServiceType, entry.IP)
		if err != nil {
			return nil, err
		}
		infos[key] = info
	}

	mnemonic := strings.TrimSpace(p.Mnemonic)
	if mnemonic == "" {
		mn, err := GenerateMnemonic12()
		if err != nil {
			return nil, err
		}
		mnemonic = mn
	}

	jobs := GenerateJobs(chainKeys, infos, mnemonic, p.TxAmount, p.BlockRange)
	if err := WriteJSONFile(p.OutPath, jobs); err != nil {
		return nil, err
	}

	return &GenerateResult{
		OutPath:   p.OutPath,
		Mnemonic:  mnemonic,
		Chains:    chainKeys,
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

// PickChainEntries 从 servers 列表中挑选可参与跨链配置生成的入口节点。
// 返回值以 name 为唯一键，确保同类型多链（如多个 op/cdk）都可参与。
func PickChainEntries(servers []deploy.ServerInfo) (map[string]deploy.ServerInfo, error) {
	entries := make(map[string]deploy.ServerInfo)
	for _, s := range servers {
		t := strings.ToLower(strings.TrimSpace(s.ServiceType))
		if t == "" {
			continue
		}
		switch t {
		case "op", "cdk", "xjst":
			name := strings.TrimSpace(s.Name)
			if name == "" {
				return nil, fmt.Errorf("servers.json 存在空 name: serviceType=%s ip=%s", t, strings.TrimSpace(s.IP))
			}
			index, err := parseServerNameIndex(name, t)
			if err != nil {
				return nil, fmt.Errorf("解析 name 失败: serviceType=%s name=%q: %w", t, s.Name, err)
			}
			// 仅 xjst 需要过滤 index=1，op/cdk 全部参与。
			if t == "xjst" && index != 1 {
				continue
			}

			ip := strings.TrimSpace(s.IP)
			if ip == "" {
				return nil, fmt.Errorf("servers.json 存在空 ip: serviceType=%s", t)
			}
			if _, exists := entries[name]; exists {
				return nil, fmt.Errorf("servers.json 存在重复 name=%q", name)
			}
			entries[name] = deploy.ServerInfo{
				IP:          ip,
				ServiceType: t,
				Name:        name,
			}
		default:
			return nil, fmt.Errorf("不支持的 serviceType=%q（仅支持 op/cdk/xjst）", s.ServiceType)
		}
	}
	return entries, nil
}

func parseServerNameIndex(name, serviceType string) (int, error) {
	parts := strings.Split(strings.TrimSpace(name), "-")
	switch serviceType {
	case "op", "cdk":
		// tagPrefix-serviceType-ordinal
		if len(parts) < 3 {
			return 0, fmt.Errorf("name 格式不合法，期望 tagPrefix-%s-ordinal", serviceType)
		}
		prefix := strings.Join(parts[:len(parts)-2], "-")
		if strings.TrimSpace(prefix) == "" {
			return 0, fmt.Errorf("name 格式不合法，tagPrefix 不能为空")
		}
		if strings.ToLower(parts[len(parts)-2]) != serviceType {
			return 0, fmt.Errorf("name 与 serviceType 不匹配")
		}
		ordinal, err := strconv.Atoi(parts[len(parts)-1])
		if err != nil || ordinal <= 0 {
			return 0, fmt.Errorf("ordinal 必须是正整数")
		}
		return ordinal, nil
	case "xjst":
		// tagPrefix-serviceType-groupId-index
		if len(parts) < 4 {
			return 0, fmt.Errorf("name 格式不合法，期望 tagPrefix-xjst-groupId-index")
		}
		prefix := strings.Join(parts[:len(parts)-3], "-")
		if strings.TrimSpace(prefix) == "" {
			return 0, fmt.Errorf("name 格式不合法，tagPrefix 不能为空")
		}
		if strings.ToLower(parts[len(parts)-3]) != serviceType {
			return 0, fmt.Errorf("name 与 serviceType 不匹配")
		}
		groupID, err := strconv.Atoi(parts[len(parts)-2])
		if err != nil || groupID <= 0 {
			return 0, fmt.Errorf("groupId 必须是正整数")
		}
		index, err := strconv.Atoi(parts[len(parts)-1])
		if err != nil || index < 1 || index > 4 {
			return 0, fmt.Errorf("index 必须在 1~4")
		}
		return index, nil
	default:
		return 0, fmt.Errorf("不支持的 serviceType=%q", serviceType)
	}
}

// GenerateJobs 生成 jobs：源链遍历所有链，目标链为随机选取且不为自身。
// 注意：该函数仅做组合与字段映射；不做网络/文件 IO，便于测试。
func GenerateJobs(chainKeys []string, infos map[string]*ChainInfo, mnemonic string, txAmount int, blockRange int64) []Job {
	jobs := make([]Job, 0, len(chainKeys))
	for _, srcKey := range chainKeys {
		targetCandidates := make([]string, 0, len(chainKeys)-1)
		for _, candidate := range chainKeys {
			if candidate == srcKey {
				continue
			}
			targetCandidates = append(targetCandidates, candidate)
		}
		if len(targetCandidates) == 0 {
			continue
		}

		dstType, err := pickRandomTarget(targetCandidates)
		if err != nil {
			// 随机数获取失败时回退到第一个候选目标，避免中断配置生成。
			dstType = targetCandidates[0]
		}

		source := infos[srcKey]
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
	return jobs
}

func pickRandomTarget(candidates []string) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("候选目标链为空")
	}

	nBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(candidates))))
	if err != nil {
		return "", err
	}
	return candidates[int(nBig.Int64())], nil
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

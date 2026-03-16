package crosstxconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"

	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
	ydylconsolesdk "github.com/wangdayong228/ydyl-deploy-client/pkg/ydyl-console-service-sdk"
)

type fakeFetchPlan struct {
	failTimes int
	info      *ChainInfo
	delay     time.Duration
}

type fakeRetryFetcher struct {
	mu        sync.Mutex
	plans     map[string]fakeFetchPlan
	calls     map[string]int
	active    int
	maxActive int
}

func newFakeRetryFetcher(plans map[string]fakeFetchPlan) *fakeRetryFetcher {
	return &fakeRetryFetcher{
		plans: plans,
		calls: make(map[string]int, len(plans)),
	}
}

func (f *fakeRetryFetcher) Fetch(ctx context.Context, chainType, ip string) (*ChainInfo, error) {
	key := fmt.Sprintf("%s@%s", chainType, ip)

	f.mu.Lock()
	plan, ok := f.plans[key]
	if !ok {
		f.mu.Unlock()
		return nil, fmt.Errorf("unexpected fetch key: %s", key)
	}
	f.calls[key]++
	currentCall := f.calls[key]
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()

	if plan.delay > 0 {
		select {
		case <-time.After(plan.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	if currentCall <= plan.failTimes {
		return nil, fmt.Errorf("transient error for %s at call=%d", key, currentCall)
	}
	return plan.info, nil
}

func (f *fakeRetryFetcher) Calls(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func (f *fakeRetryFetcher) MaxActive() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxActive
}

func writeTestServersAndConfigFiles(t *testing.T) (string, string, string) {
	t.Helper()

	tmpDir := t.TempDir()
	serversPath := filepath.Join(tmpDir, "servers.json")
	outPath := filepath.Join(tmpDir, "jobs.json")
	configPath := filepath.Join(tmpDir, "config.deploy.yaml")

	serversJSON := `[
  {"ip":"1.1.1.1","serviceType":"op","name":"ydyl-op-1"},
  {"ip":"2.2.2.2","serviceType":"cdk","name":"ydyl-cdk-1"}
]`
	require.NoError(t, os.WriteFile(serversPath, []byte(serversJSON), 0o644))

	configYAML := "l1BridgeHubContract: \"0x00000000000000000000000000000000000000ff\"\n"
	require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0o644))

	return serversPath, configPath, outPath
}

func newTestChainInfo(chainType, ip, suffix string) *ChainInfo {
	return &ChainInfo{
		Type: chainType,
		IP:   ip,
		Summary: &ydylconsolesdk.SummaryResultResponse{
			L2_RPC_URL:             "http://localhost:8545",
			L2_PRIVATE_KEY:         common.HexToHash("0x" + strings.Repeat(suffix, 64)),
			L2_COUNTER_CONTRACT:    common.HexToAddress("0x" + strings.Repeat(suffix, 40)),
			L1_BRIDGE_HUB_CONTRACT: common.HexToAddress("0x00000000000000000000000000000000000000ff"),
		},
		Contracts: &ydylconsolesdk.NodeDeploymentContractsResponse{
			L1BridgeSendContract:    common.HexToAddress("0x00000000000000000000000000000000000000e" + suffix),
			L2BridgeSendContract:    common.HexToAddress("0x00000000000000000000000000000000000000c" + suffix),
			L2BridgeReceiveContract: common.HexToAddress("0x00000000000000000000000000000000000000c" + suffix),
			L1BridgeReceiveContract: common.HexToAddress("0x00000000000000000000000000000000000000d" + suffix),
		},
	}
}

func TestGenerateJobs_ThreeChains_Success(t *testing.T) {
	// 只测成功路径：3 条链 (op/cdk/xjst) => 每个源链随机选 1 个目标链，共 3 个 jobs。
	chainTypes := []string{"cdk", "op", "xjst"}

	infos := map[string]*ChainInfo{
		"op": {
			Type: "op",
			IP:   "1.1.1.1",
			Summary: &ydylconsolesdk.SummaryResultResponse{
				L2_RPC_URL:             "https://op.l2/rpc",
				L2_PRIVATE_KEY:         common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111"),
				L2_COUNTER_CONTRACT:    common.HexToAddress("0x00000000000000000000000000000000000000a1"),
				L1_BRIDGE_HUB_CONTRACT: common.HexToAddress("0x00000000000000000000000000000000000000b1"),
			},
			Contracts: &ydylconsolesdk.NodeDeploymentContractsResponse{
				L1BridgeSendContract:    common.HexToAddress("0x00000000000000000000000000000000000000e1"),
				L2BridgeSendContract:    common.HexToAddress("0x00000000000000000000000000000000000000c1"),
				L2BridgeReceiveContract: common.HexToAddress("0x00000000000000000000000000000000000000c1"),
				L1BridgeReceiveContract: common.HexToAddress("0x00000000000000000000000000000000000000d1"),
			},
		},
		"cdk": {
			Type: "cdk",
			IP:   "2.2.2.2",
			Summary: &ydylconsolesdk.SummaryResultResponse{
				L2_RPC_URL:             "https://cdk.l2/rpc",
				L2_PRIVATE_KEY:         common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"),
				L2_COUNTER_CONTRACT:    common.HexToAddress("0x00000000000000000000000000000000000000a2"),
				L1_BRIDGE_HUB_CONTRACT: common.HexToAddress("0x00000000000000000000000000000000000000b2"),
			},
			Contracts: &ydylconsolesdk.NodeDeploymentContractsResponse{
				L1BridgeSendContract:    common.HexToAddress("0x00000000000000000000000000000000000000e2"),
				L2BridgeSendContract:    common.HexToAddress("0x00000000000000000000000000000000000000c2"),
				L2BridgeReceiveContract: common.HexToAddress("0x00000000000000000000000000000000000000c2"),
				L1BridgeReceiveContract: common.HexToAddress("0x00000000000000000000000000000000000000d2"),
			},
		},
		"xjst": {
			Type: "xjst",
			IP:   "3.3.3.3",
			Summary: &ydylconsolesdk.SummaryResultResponse{
				L2_RPC_URL:             "http://xjst.l2/rpc",
				L2_PRIVATE_KEY:         common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333"),
				L2_COUNTER_CONTRACT:    common.HexToAddress("0x00000000000000000000000000000000000000a3"),
				L1_BRIDGE_HUB_CONTRACT: common.HexToAddress("0x00000000000000000000000000000000000000b3"),
			},
			Contracts: &ydylconsolesdk.NodeDeploymentContractsResponse{
				L1BridgeSendContract:    common.HexToAddress("0x00000000000000000000000000000000000000e3"),
				L2BridgeSendContract:    common.HexToAddress("0x00000000000000000000000000000000000000c3"),
				L2BridgeReceiveContract: common.HexToAddress("0x00000000000000000000000000000000000000c3"),
				L1BridgeReceiveContract: common.HexToAddress("0x00000000000000000000000000000000000000d3"),
			},
		},
	}

	l1BridgeReceiver := "0x00000000000000000000000000000000000000ff"
	jobs, err := GenerateJobs(chainTypes, infos, 1000, 10, 100000, l1BridgeReceiver)
	require.NoError(t, err)

	require.Len(t, jobs, 3)
	var firstMnemonic string
	seenSources := make(map[string]struct{}, len(chainTypes))
	for _, j := range jobs {
		require.NotEmpty(t, j.SourceL2ChainType)
		require.NotEmpty(t, j.TargetL2ChainType)
		require.NotEqual(t, j.SourceL2ChainType, j.TargetL2ChainType)
		require.NotEmpty(t, j.Mnemonic)
		if firstMnemonic == "" {
			firstMnemonic = j.Mnemonic
		}
		require.Equal(t, firstMnemonic, j.Mnemonic, "所有 job 应复用同一助记词")
		require.Equal(t, 1000, j.TxAmountPerWallet)
		require.Equal(t, 10, j.WalletAmount)
		require.Equal(t, int64(100000), j.BlockRange)
		_, exists := seenSources[j.SourceL2ChainType]
		require.False(t, exists, "每个源链只能出现一次: %s", j.SourceL2ChainType)
		seenSources[j.SourceL2ChainType] = struct{}{}

		// 动态校验 source/target 映射字段，不依赖具体随机结果。
		source := infos[j.SourceL2ChainType]
		target := infos[j.TargetL2ChainType]
		require.NotNil(t, source)
		require.NotNil(t, target)
		expectedTargetL1Bridge := target.Contracts.L1BridgeReceiveContract.Hex()
		if target.Type == "xjst" {
			expectedTargetL1Bridge = target.Contracts.L1BridgeSendContract.Hex()
		}
		require.Equal(t, expectedTargetL1Bridge, j.TargetL1Bridge)
		require.Equal(t, source.Contracts.L2BridgeSendContract.Hex(), j.SourceL2Bridge)
		require.Equal(t, target.Summary.L2_COUNTER_CONTRACT.Hex(), j.TargetL2Contract)
		require.Equal(t, l1BridgeReceiver, j.L1BridgeReceiver)
		require.Equal(t, replaceLocalhostWithIP(source.Summary.L2_RPC_URL, source.IP), j.SourceL2RPC)
		require.Equal(t, replaceLocalhostWithIP(target.Summary.L2_RPC_URL, target.IP), j.TargetL2RPC)
		require.Equal(t, target.Contracts.L2BridgeReceiveContract.Hex(), j.TargetL2Bridge)
		require.Equal(t, source.Summary.L2_PRIVATE_KEY.Hex(), j.SourceL2BalanceSenderPrivatekey)
	}
	require.Len(t, seenSources, len(chainTypes))
}

func TestReplaceLocalhostWithIP_RewriteRules(t *testing.T) {
	tests := []struct {
		name   string
		rawURL string
		ip     string
		want   string
	}{
		{
			name:   "replace localhost host",
			rawURL: "http://localhost:30010/l2rpc",
			ip:     "35.92.243.192",
			want:   "http://35.92.243.192:30010/l2rpc",
		},
		{
			name:   "replace loopback host",
			rawURL: "http://127.0.0.1:30010/l2rpc?a=1",
			ip:     "35.92.243.192",
			want:   "http://35.92.243.192:30010/l2rpc?a=1",
		},
		{
			name:   "replace private host",
			rawURL: "http://172.31.34.58:30010",
			ip:     "44.249.174.85",
			want:   "http://44.249.174.85:30010",
		},
		{
			name:   "keep public domain",
			rawURL: "https://op.yidaiyilu0.site/l2rpc",
			ip:     "35.92.243.192",
			want:   "https://op.yidaiyilu0.site/l2rpc",
		},
		{
			name:   "keep public ip",
			rawURL: "http://8.8.8.8:8545",
			ip:     "35.92.243.192",
			want:   "http://8.8.8.8:8545",
		},
		{
			name:   "fallback for non standard url",
			rawURL: "127.0.0.1:30010",
			ip:     "35.92.243.192",
			want:   "35.92.243.192:30010",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replaceLocalhostWithIP(tt.rawURL, tt.ip)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestPickChainEntries_FilterRules(t *testing.T) {
	servers := []deploy.ServerInfo{
		{IP: "1.1.1.2", ServiceType: "op", Name: "ydyl-op-2"},
		{IP: "1.1.1.1", ServiceType: "op", Name: "ydyl-op-1"},
		{IP: "2.2.2.3", ServiceType: "cdk", Name: "my-tag-cdk-3"},
		{IP: "2.2.2.1", ServiceType: "cdk", Name: "my-tag-cdk-1"},
		{IP: "3.3.3.2", ServiceType: "xjst", Name: "prefix-xjst-1-2"},
		{IP: "3.3.3.5", ServiceType: "xjst", Name: "prefix-xjst-2-1"},
	}

	got, err := PickChainEntries(servers)
	require.NoError(t, err)
	require.Equal(t, map[string]deploy.ServerInfo{
		"ydyl-op-2":       {IP: "1.1.1.2", ServiceType: "op", Name: "ydyl-op-2"},
		"ydyl-op-1":       {IP: "1.1.1.1", ServiceType: "op", Name: "ydyl-op-1"},
		"my-tag-cdk-3":    {IP: "2.2.2.3", ServiceType: "cdk", Name: "my-tag-cdk-3"},
		"my-tag-cdk-1":    {IP: "2.2.2.1", ServiceType: "cdk", Name: "my-tag-cdk-1"},
		"prefix-xjst-2-1": {IP: "3.3.3.5", ServiceType: "xjst", Name: "prefix-xjst-2-1"},
	}, got)
}

func TestPickChainEntries_InvalidName_FailFast(t *testing.T) {
	tests := []struct {
		name    string
		servers []deploy.ServerInfo
	}{
		{
			name: "empty name",
			servers: []deploy.ServerInfo{
				{IP: "1.1.1.1", ServiceType: "op", Name: ""},
			},
		},
		{
			name: "invalid op format",
			servers: []deploy.ServerInfo{
				{IP: "1.1.1.1", ServiceType: "op", Name: "bad-op"},
			},
		},
		{
			name: "invalid xjst format",
			servers: []deploy.ServerInfo{
				{IP: "3.3.3.1", ServiceType: "xjst", Name: "prefix-xjst-1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := PickChainEntries(tt.servers)
			require.Error(t, err)
		})
	}
}

func TestGenerateWithFetcher_ConcurrentAndRetrySuccess(t *testing.T) {
	serversPath, configPath, outPath := writeTestServersAndConfigFiles(t)

	opKey := "op@1.1.1.1"
	cdkKey := "cdk@2.2.2.2"
	fetcher := newFakeRetryFetcher(map[string]fakeFetchPlan{
		opKey: {
			failTimes: 2,
			delay:     40 * time.Millisecond,
			info:      newTestChainInfo("op", "1.1.1.1", "1"),
		},
		cdkKey: {
			failTimes: 0,
			delay:     40 * time.Millisecond,
			info:      newTestChainInfo("cdk", "2.2.2.2", "2"),
		},
	})

	res, err := GenerateWithFetcher(context.Background(), GenerateParams{
		ServersPath:       serversPath,
		ConfigPath:        configPath,
		OutPath:           outPath,
		TxAmountPerWallet: 1000,
		WalletAmount:      10,
		BlockRange:        100000,
	}, fetcher)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, 2, res.JobsCount)
	require.Equal(t, 3, fetcher.Calls(opKey), "op 应重试到第 3 次成功")
	require.Equal(t, 1, fetcher.Calls(cdkKey), "cdk 应只调用 1 次")
	require.Greater(t, fetcher.MaxActive(), 1, "应存在并发执行")
}

func TestGenerateWithFetcher_RetryExhaustedFail(t *testing.T) {
	serversPath, configPath, outPath := writeTestServersAndConfigFiles(t)

	fetcher := newFakeRetryFetcher(map[string]fakeFetchPlan{
		"op@1.1.1.1": {
			failTimes: 5,
			info:      newTestChainInfo("op", "1.1.1.1", "1"),
		},
		"cdk@2.2.2.2": {
			failTimes: 0,
			info:      newTestChainInfo("cdk", "2.2.2.2", "2"),
		},
	})

	_, err := GenerateWithFetcher(context.Background(), GenerateParams{
		ServersPath:       serversPath,
		ConfigPath:        configPath,
		OutPath:           outPath,
		TxAmountPerWallet: 1000,
		WalletAmount:      10,
		BlockRange:        100000,
	}, fetcher)
	require.Error(t, err)
	require.Contains(t, err.Error(), "attempts=3")
	require.Equal(t, 3, fetcher.Calls("op@1.1.1.1"), "失败链应重试 3 次")
}

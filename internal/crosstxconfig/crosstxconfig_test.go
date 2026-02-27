package crosstxconfig

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"

	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
	ydylconsolesdk "github.com/wangdayong228/ydyl-deploy-client/pkg/ydyl-console-service-sdk"
)

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
				L2BridgeSendContract:    common.HexToAddress("0x00000000000000000000000000000000000000c3"),
				L2BridgeReceiveContract: common.HexToAddress("0x00000000000000000000000000000000000000c3"),
				L1BridgeReceiveContract: common.HexToAddress("0x00000000000000000000000000000000000000d3"),
			},
		},
	}

	mnemonic := "test test test test test test test test test test test junk"
	l1BridgeReceiver := "0x00000000000000000000000000000000000000ff"
	jobs := GenerateJobs(chainTypes, infos, mnemonic, 1000, 10, 100000, l1BridgeReceiver)

	require.Len(t, jobs, 3)
	seenSources := make(map[string]struct{}, len(chainTypes))
	for _, j := range jobs {
		require.NotEmpty(t, j.SourceL2ChainType)
		require.NotEmpty(t, j.TargetL2ChainType)
		require.NotEqual(t, j.SourceL2ChainType, j.TargetL2ChainType)
		require.Equal(t, mnemonic, j.Mnemonic)
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
		require.Equal(t, target.Contracts.L1BridgeReceiveContract.Hex(), j.TargetL1Bridge)
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

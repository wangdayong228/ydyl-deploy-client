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
				L2Bridge: common.HexToAddress("0x00000000000000000000000000000000000000c1"),
				L1Bridge: common.HexToAddress("0x00000000000000000000000000000000000000d1"),
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
				L2Bridge: common.HexToAddress("0x00000000000000000000000000000000000000c2"),
				L1Bridge: common.HexToAddress("0x00000000000000000000000000000000000000d2"),
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
				L2Bridge: common.HexToAddress("0x00000000000000000000000000000000000000c3"),
				L1Bridge: common.HexToAddress("0x00000000000000000000000000000000000000d3"),
			},
		},
	}

	mnemonic := "test test test test test test test test test test test junk"
	l1BridgeReceiver := "0x00000000000000000000000000000000000000ff"
	jobs := GenerateJobs(chainTypes, infos, mnemonic, 1000, 100000, l1BridgeReceiver)

	require.Len(t, jobs, 3)
	seenSources := make(map[string]struct{}, len(chainTypes))
	for _, j := range jobs {
		require.NotEmpty(t, j.SourceL2ChainType)
		require.NotEmpty(t, j.TargetL2ChainType)
		require.NotEqual(t, j.SourceL2ChainType, j.TargetL2ChainType)
		require.Equal(t, mnemonic, j.Mnemonic)
		require.Equal(t, 1000, j.TxAmount)
		require.Equal(t, int64(100000), j.BlockRange)
		_, exists := seenSources[j.SourceL2ChainType]
		require.False(t, exists, "每个源链只能出现一次: %s", j.SourceL2ChainType)
		seenSources[j.SourceL2ChainType] = struct{}{}

		// 动态校验 source/target 映射字段，不依赖具体随机结果。
		source := infos[j.SourceL2ChainType]
		target := infos[j.TargetL2ChainType]
		require.NotNil(t, source)
		require.NotNil(t, target)
		require.Equal(t, target.Contracts.L1Bridge.Hex(), j.TargetL1Bridge)
		require.Equal(t, source.Contracts.L2Bridge.Hex(), j.SourceL2Bridge)
		require.Equal(t, target.Summary.L2_COUNTER_CONTRACT.Hex(), j.TargetL2Contract)
		require.Equal(t, l1BridgeReceiver, j.L1BridgeReceiver)
		require.Equal(t, source.Summary.L2_RPC_URL, j.SourceL2RPC)
		require.Equal(t, target.Summary.L2_RPC_URL, j.TargetL2RPC)
		require.Equal(t, target.Contracts.L2Bridge.Hex(), j.TargetL2Bridge)
		require.Equal(t, source.Summary.L2_PRIVATE_KEY.Hex(), j.SourceL2BalanceSenderPrivatekey)
	}
	require.Len(t, seenSources, len(chainTypes))
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

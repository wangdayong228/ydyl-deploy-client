package crosstxconfig

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"

	ydylconsolesdk "github.com/wangdayong228/ydyl-deploy-client/pkg/ydyl-console-service-sdk"
)

func TestGenerateJobs_ThreeChains_Success(t *testing.T) {
	// 只测成功路径：3 条链 (op/cdk/xjst) => 6 个有向组合 a->b。
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
	jobs := GenerateJobs(chainTypes, infos, mnemonic, 1000, 100000)

	require.Len(t, jobs, 6)
	for _, j := range jobs {
		require.NotEmpty(t, j.SourceL2ChainType)
		require.NotEmpty(t, j.TargetL2ChainType)
		require.NotEqual(t, j.SourceL2ChainType, j.TargetL2ChainType)
		require.Equal(t, mnemonic, j.Mnemonic)
		require.Equal(t, 1000, j.TxAmount)
		require.Equal(t, int64(100000), j.BlockRange)
	}

	// 抽样校验 1 组映射：op -> cdk
	var sample *Job
	for i := range jobs {
		if jobs[i].SourceL2ChainType == "op" && jobs[i].TargetL2ChainType == "cdk" {
			sample = &jobs[i]
			break
		}
	}
	require.NotNil(t, sample)
	require.Equal(t, infos["cdk"].Contracts.L1Bridge.Hex(), sample.TargetL1Bridge)
	require.Equal(t, infos["op"].Contracts.L2Bridge.Hex(), sample.SourceL2Bridge)
	require.Equal(t, infos["cdk"].Summary.L2_COUNTER_CONTRACT.Hex(), sample.TargetL2Contract)
	require.Equal(t, infos["op"].Summary.L1_BRIDGE_HUB_CONTRACT.Hex(), sample.L1BridgeReceiver)
	require.Equal(t, infos["op"].Summary.L2_RPC_URL, sample.SourceL2RPC)
	require.Equal(t, infos["cdk"].Summary.L2_RPC_URL, sample.TargetL2RPC)
	require.Equal(t, infos["cdk"].Contracts.L2Bridge.Hex(), sample.TargetL2Bridge)
	require.Equal(t, infos["op"].Summary.L2_PRIVATE_KEY.Hex(), sample.SourceL2BalanceSenderPrivatekey)
}

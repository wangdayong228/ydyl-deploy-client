package ydylconsolesdk

import "github.com/ethereum/go-ethereum/common"

// 说明：
// - 地址/私钥等字段统一使用 go-ethereum 的 common.Address/common.Hash（JSON 通常是 "0x..." 字符串）。
//
// 注意：`ydyl-console-service` 的 DTO 多数未显式指定 json 字段名（例如 `json:",omitempty"` 或无 tag），
// 实际 JSON key 更倾向于 Go 字段名原样（如 `L2_RPC_URL`、`L2Bridge`、`LAST_DONE_STEP`）。
// 因此这里优先对齐服务端真实输出，避免 SDK 反序列化失败。

type L2Type int

const (
	L2TypeCdk  L2Type = 0
	L2TypeOp   L2Type = 1
	L2TypeXjst L2Type = 2
)

type PipelineStatus string

const (
	PipelineStatusRunning PipelineStatus = "running"
	PipelineStatusSuccess PipelineStatus = "success"
	PipelineStatusFailed  PipelineStatus = "failed"
)

type SummaryResultResponse struct {
	L2Type L2Type `json:",omitempty"`

	L2_RPC_URL string `json:",omitempty"`
	L1_RPC_URL string `json:",omitempty"`

	L1_CHAIN_ID string `json:",omitempty"`
	L2_CHAIN_ID string `json:",omitempty"`

	L1_VAULT_PRIVATE_KEY common.Hash `json:",omitempty"`
	L2_VAULT_PRIVATE_KEY common.Hash `json:",omitempty"`
	L2_PRIVATE_KEY       common.Hash `json:",omitempty"`

	KURTOSIS_L1_PREALLOCATED_MNEMONIC string      `json:",omitempty"`
	CLAIM_SERVICE_PRIVATE_KEY         common.Hash `json:",omitempty"`

	L2_COUNTER_CONTRACT    common.Address `json:",omitempty"`
	L1_BRIDGE_HUB_CONTRACT common.Address `json:",omitempty"`
}

type PipeProgressResponse struct {
	LAST_DONE_STEP  int
	PIPELINE_STATUS PipelineStatus
}

type NodeDeploymentContractsResponse struct {
	L2Bridge common.Address
	L1Bridge common.Address
}

type OpNodeDeploymentContracts struct {
	L2CrossDomainMessenger       common.Address
	L1CrossDomainMessengerProxy  common.Address
	L1StandardBridgeProxyAddress common.Address
	OptimismPortalProxy          common.Address
	DisputeGameFactoryProxy      common.Address
}

type CdkNodeDeploymentContracts struct {
	PolygonZkEVML2BridgeAddress common.Address
	PolygonZkEVMBridgeAddress   common.Address
}

type XjstNodeDeploymentContracts struct {
	L1SimpleCalculator common.Address
	L1StateSender      common.Address
	L1UnifiedBridge    common.Address
	L2StateSender      common.Address
	L2UnifiedBridge    common.Address
}

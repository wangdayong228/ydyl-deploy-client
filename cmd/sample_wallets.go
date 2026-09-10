package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/samplewallets"
)

var (
	sampleWalletsServersPath string
	sampleWalletsL2Type      int
	sampleWalletsMaxIndex    uint64
	sampleWalletsRPCURL      string
	sampleWalletsChainID     uint64
)

func init() {
	cmd := &cobra.Command{
		Use:   "sample-wallets",
		Short: "随机抽查确定性账户的地址和 L2 余额",
		Long: `读取 servers.json，按 --l2type 过滤入口链后随机选 1 条，从 console-service 获取 L2 RPC。
可用 --rpc-url 覆盖查余额 RPC（原样使用，不改写 localhost）。省略或空白时仍改写 summary.L2_RPC_URL。
在 [0, max-index) 抽取 10 个不重复 index，按 ydyl-gen-accounts 确定性规则生成私钥和地址，并查询余额。

l2type: 0=cdk, 1=op, 2=xjst, 3=core。
EVM 使用 summary.L2_CHAIN_ID 派生私钥；xjst 使用机器名中的 groupID。
xjst 仅选择组内 node-1（与 PickChainEntries 一致）。
l2type=3 必填 --rpc-url 与 --chainID，跳过 servers.json 与 console-service，地址输出 CIP-37。`,
		RunE: runSampleWallets,
	}

	cmd.Flags().StringVar(&sampleWalletsServersPath, "servers", samplewallets.DefaultServersPath, "servers.json 路径")
	cmd.Flags().IntVar(&sampleWalletsL2Type, "l2type", 0, "链类型：0=cdk, 1=op, 2=xjst, 3=core")
	cmd.Flags().Uint64Var(&sampleWalletsMaxIndex, "max-index", samplewallets.DefaultMaxIndex, "随机 index 上限（不含），区间为 [0, max-index)")
	cmd.Flags().StringVar(&sampleWalletsRPCURL, "rpc-url", "", "L2/Core RPC URL；l2type=0/1/2 省略或空白时从 console-service 的 L2_RPC_URL 改写；l2type=3 必填")
	cmd.Flags().Uint64Var(&sampleWalletsChainID, "chainID", 0, "Core chain_id；仅 l2type=3 使用，同时作为 CIP-37 networkId")
	_ = cmd.MarkFlagRequired("l2type")

	rootCmd.AddCommand(cmd)
}

func runSampleWallets(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	res, err := samplewallets.Run(ctx, samplewallets.Params{
		ServersPath: sampleWalletsServersPath,
		L2Type:      sampleWalletsL2Type,
		MaxIndex:    sampleWalletsMaxIndex,
		RPCURL:      sampleWalletsRPCURL,
		ChainID:     sampleWalletsChainID,
	}, samplewallets.ConsoleSummaryFetcher{}, samplewallets.JSONRPCBalanceClient{})
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), samplewallets.FormatResult(res))
	return err
}

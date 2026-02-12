package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/crosstxconfig"
)

var (
	genCrossTxServersPath string
	genCrossTxConfigPath  string
	genCrossTxOutPath     string
	genCrossTxTxAmount    int
	genCrossTxBlockRange  int64
)

func init() {
	cmd := &cobra.Command{
		Use:   "gen-cross-tx-config",
		Short: "生成跨链脚本 jobs 配置文件",
		Long:  "读取 servers.json，调用各链节点上的 ydyl-console-service API 获取 rpc/合约信息，生成跨链脚本(zk-claim-service/scripts/7s_jobs.json)所需的 jobs 配置（源链遍历所有链，目标链随机选取且不为自身）。",
		RunE:  runGenCrossTxConfig,
	}

	cmd.Flags().StringVar(&genCrossTxServersPath, "servers", "", "servers.json 路径（参考 ydyl-deploy-client/output/servers.json）")
	_ = cmd.MarkFlagRequired("servers")
	cmd.Flags().StringVar(&genCrossTxConfigPath, "config", "./config.deploy.yaml", "deploy 配置文件路径（用于读取 l1BridgeHubContract）")

	cmd.Flags().StringVar(&genCrossTxOutPath, "out", "./7s_jobs.gen.json", "输出 jobs 配置文件路径（JSON array）")
	cmd.Flags().IntVar(&genCrossTxTxAmount, "tx-amount", 1000, "tx_amount：每个 job 发送交易数量")
	cmd.Flags().Int64Var(&genCrossTxBlockRange, "block-range", 100000, "block_range：查询区块范围")

	rootCmd.AddCommand(cmd)
}

func runGenCrossTxConfig(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := crosstxconfig.Generate(ctx, crosstxconfig.GenerateParams{
		ServersPath: genCrossTxServersPath,
		ConfigPath:  genCrossTxConfigPath,
		OutPath:     genCrossTxOutPath,
		TxAmount:    genCrossTxTxAmount,
		BlockRange:  genCrossTxBlockRange,
	})
	if err != nil {
		return err
	}

	fmt.Printf("已生成 %d 条 job（链=%v），输出到 %s\n", res.JobsCount, res.Chains, res.OutPath)
	return nil
}

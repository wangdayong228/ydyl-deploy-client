package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/crosstxconfig"
)

var (
	genCrossTxServersPath       string
	genCrossTxConfigPath        string
	genCrossTxOutPath           string
	genCrossTxPartNumber        int
	genCrossTxTxAmountPerWallet int
	genCrossTxBlockRange        int64
	genCrossTxWalletAmount      int
)

const generatedJobsConfigFilename = "7s_jobs.gen.json"

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

	cmd.Flags().StringVar(&genCrossTxOutPath, "out", "", "输出根目录（将生成到 <out>/jobs/all.json 与 <out>/jobs/1..N.json；不传则默认使用 servers 所在目录）")
	cmd.Flags().IntVar(&genCrossTxPartNumber, "part-number", 8, "jobs 拆分份数（将生成 jobs/1.json ~ jobs/N.json）")
	cmd.Flags().IntVar(&genCrossTxTxAmountPerWallet, "tx-amount-per-wallet", 500, "tx_amount_per_wallet：每个 wallet 发送交易数量")
	cmd.Flags().IntVar(&genCrossTxWalletAmount, "wallet-amount", 100, "wallet_amount：每个 job 发送的 wallet 数量")
	cmd.Flags().Int64Var(&genCrossTxBlockRange, "block-range", 300, "block_range：查询区块范围")

	rootCmd.AddCommand(cmd)
}

func runGenCrossTxConfig(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	baseOutputDir := strings.TrimSpace(genCrossTxOutPath)
	if baseOutputDir == "" {
		baseOutputDir = filepath.Dir(genCrossTxServersPath)
	}

	res, err := crosstxconfig.Generate(ctx, crosstxconfig.GenerateParams{
		ServersPath:       genCrossTxServersPath,
		ConfigPath:        genCrossTxConfigPath,
		OutPath:           baseOutputDir,
		PartNumber:        genCrossTxPartNumber,
		TxAmountPerWallet: genCrossTxTxAmountPerWallet,
		WalletAmount:      genCrossTxWalletAmount,
		BlockRange:        genCrossTxBlockRange,
	})
	if err != nil {
		return err
	}

	copiedPath, err := copyGeneratedJobsConfig(res.OutPath)
	if err != nil {
		return err
	}

	fmt.Printf("已生成 %d 条 job（链=%v），输出到 %s，并复制到 %s\n", res.JobsCount, res.Chains, res.OutPath, copiedPath)
	return nil
}

func copyGeneratedJobsConfig(sourcePath string) (string, error) {
	b, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", fmt.Errorf("读取 all.json 失败: %w", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("获取当前目录失败: %w", err)
	}
	destPath := filepath.Join(wd, generatedJobsConfigFilename)
	if err := os.WriteFile(destPath, b, 0o644); err != nil {
		return "", fmt.Errorf("写出 %s 失败: %w", generatedJobsConfigFilename, err)
	}
	return destPath, nil
}

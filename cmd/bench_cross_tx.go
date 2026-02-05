package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/crosstxbench"
)

var (
	benchCrossTxConfigPath  string
	benchCrossTxConcurrency int
)

func init() {
	cmd := &cobra.Command{
		Use:   "bench-cross-tx",
		Short: "执行跨链压测交易脚本（7s_multijob.js）",
		Long:  "读取 gen-cross-tx-config 生成的 jobs 配置文件，进入 zk-claim-service 目录执行 node scripts/7s_multijob.js <config> 发送跨链交易。",
		RunE:  runBenchCrossTx,
	}

	cmd.Flags().StringVar(&benchCrossTxConfigPath, "config", "", "jobs 配置文件路径（gen-cross-tx-config 的输出 JSON）")
	_ = cmd.MarkFlagRequired("config")

	cmd.Flags().IntVar(&benchCrossTxConcurrency, "concurrency", 50, "并发数（透传到环境变量 CONCURRENCY）")

	rootCmd.AddCommand(cmd)
}

func runBenchCrossTx(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := crosstxbench.DefaultBench().Run(ctx, crosstxbench.Params{
		ConfigPath:  benchCrossTxConfigPath,
		Concurrency: benchCrossTxConcurrency,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "bench-cross-tx 失败：", err)
		return err
	}

	return nil
}

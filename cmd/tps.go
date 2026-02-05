package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/crosstxtps"
)

var (
	tpsConfigPath string
)

func init() {
	cmd := &cobra.Command{
		Use:   "tps",
		Short: "监控跨链交易 TPS（h_TPSjob.js）",
		Long:  "读取 gen-cross-tx-config 生成的 jobs 配置文件，进入 zk-claim-service 目录执行 node scripts/h_TPSjob.js <config>，等待 hash.json 文件并持续输出 TPS。",
		RunE:  runTPS,
	}

	cmd.Flags().StringVar(&tpsConfigPath, "config", "", "jobs 配置文件路径（gen-cross-tx-config 的输出 JSON，必填）")
	_ = cmd.MarkFlagRequired("config")

	rootCmd.AddCommand(cmd)
}

func runTPS(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := crosstxtps.DefaultTPS().Run(ctx, crosstxtps.Params{
		ConfigPath: tpsConfigPath,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "tps 失败：", err)
		return err
	}
	return nil
}

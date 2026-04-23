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
		Short: "启动跨链交易 TPS Docker Compose service",
		Long:  "进入 ../ydyl-bench-docker 执行 docker compose up --build tps；如传入 --config，则先校验其与 ydyl-deploy-client/output/jobs/all.json JSON 内容一致。",
		RunE:  runTPS,
	}

	cmd.Flags().StringVar(&tpsConfigPath, "config", "", "jobs 配置文件路径（可选；传入时会与 output/jobs/all.json 做 JSON 内容校验）")

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

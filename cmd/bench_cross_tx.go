package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/crosstxbench"
)

var (
	benchCrossTxConfigPath string
)

func init() {
	cmd := &cobra.Command{
		Use:   "bench-cross-tx",
		Short: "启动跨链压测 Docker Compose services",
		Long:  "进入 ../ydyl-bench-docker 执行 docker compose up --build multijob-1..8；如传入 --config，则先校验其与 ydyl-deploy-client/output/jobs/all.json JSON 内容一致。",
		RunE:  runBenchCrossTx,
	}

	cmd.Flags().StringVar(&benchCrossTxConfigPath, "config", "", "jobs 配置文件路径（可选；传入时会与 output/jobs/all.json 做 JSON 内容校验）")

	rootCmd.AddCommand(cmd)
}

func runBenchCrossTx(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := crosstxbench.DefaultBench().Run(ctx, crosstxbench.Params{
		ConfigPath: benchCrossTxConfigPath,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "bench-cross-tx 失败：", err)
		return err
	}

	return nil
}

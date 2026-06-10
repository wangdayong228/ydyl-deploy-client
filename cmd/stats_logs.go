package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
)

var (
	statsLogsOutputDir string
)

func init() {
	cmd := &cobra.Command{
		Use:   "stats-logs",
		Short: "统计日志行数与大小并导出 CSV",
		Long:  "扫描本地客户端日志、pipe 同步日志及 collected 压缩包，统计行数与文件大小，输出 output/log_stats.csv。",
		RunE:  runStatsLogs,
	}
	cmd.Flags().StringVarP(&configPath, "config", "f", "./config.deploy.yaml", "部署配置文件路径（YAML）")
	cmd.Flags().StringVar(&statsLogsOutputDir, "output-dir", "", "覆盖 output 目录（默认使用 config 的 outputDir）")
	rootCmd.AddCommand(cmd)
}

func runStatsLogs(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := deploy.LoadConfigFromFile(configPath)
	outPath, err := deploy.StatsLogs(ctx, cfg.CommonConfig, deploy.StatsLogsOptions{
		OutputDir: statsLogsOutputDir,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "stats-logs 失败：", err)
		return err
	}
	fmt.Printf("stats-logs 完成：CSV 输出 %s\n", outPath)
	return nil
}

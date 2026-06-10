package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
)

var (
	collectLogsOutputDir string
)

func init() {
	cmd := &cobra.Command{
		Use:   "collect-logs",
		Short: "收集远端部署/运行日志并压缩拉回本地",
		Long:  "读取 output/script_status.json，按 service 类型收集 pipe、kurtosis deploy、runtime 日志：先统计行数，再远端 gzip 压缩，并通过 rsync 拉回 logs/collected，同时写入 manifest.json。",
		RunE:  runCollectLogs,
	}
	cmd.Flags().StringVarP(&configPath, "config", "f", "./config.deploy.yaml", "部署配置文件路径（YAML）")
	cmd.Flags().StringVar(&collectLogsOutputDir, "output-dir", "", "覆盖 output 目录（默认使用 config 的 outputDir）")
	rootCmd.AddCommand(cmd)
}

func runCollectLogs(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := deploy.LoadConfigFromFile(configPath)
	if err := deploy.CollectLogs(ctx, cfg.CommonConfig, deploy.CollectLogsOptions{
		OutputDir: collectLogsOutputDir,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "collect-logs 失败：", err)
		return err
	}
	fmt.Println("collect-logs 完成：已写入 logs/collected/manifest.json")
	return nil
}

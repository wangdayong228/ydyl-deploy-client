package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/genaccmonitor"
)

var (
	monitorGenAccServersPath string
	monitorGenAccOutPath     string
	monitorGenAccInterval    time.Duration
)

func init() {
	cmd := &cobra.Command{
		Use:   "monitor-gen-accounts",
		Short: "监控各链生成账户汇总信息",
		Long:  "读取 servers.json，轮询各链 ydyl-console-service 的 /v1/result/gen-acc/summary，输出分链与总汇总到 JSON 文件，并实时打印 totalAccountGenerated。",
		RunE:  runMonitorGenAccounts,
	}

	cmd.Flags().StringVar(&monitorGenAccServersPath, "servers", "./output/servers.json", "servers.json 路径（默认 ./output/servers.json）")
	cmd.Flags().StringVar(&monitorGenAccOutPath, "out", "./output/summary-gen-accounts.json", "汇总输出文件路径")
	cmd.Flags().DurationVar(&monitorGenAccInterval, "interval", 2*time.Second, "轮询间隔（例如 2s）")

	rootCmd.AddCommand(cmd)
}

func runMonitorGenAccounts(cmd *cobra.Command, args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := genaccmonitor.Run(ctx, genaccmonitor.Params{
		ServersPath: monitorGenAccServersPath,
		OutPath:     monitorGenAccOutPath,
		Interval:    monitorGenAccInterval,
	}); err != nil {
		return fmt.Errorf("monitor-gen-accounts 失败: %w", err)
	}
	return nil
}

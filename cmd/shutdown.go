package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
)

var (
	shutdownServersPath string
	shutdownConfigPath  string
)

func init() {
	cmd := &cobra.Command{
		Use:   "shutdown",
		Short: "根据 servers.json 批量关机服务器",
		Long:  "读取 servers.json 中的服务器列表，复用 deploy 配置文件中的 SSH 参数，在远端执行关机命令。",
		RunE:  runShutdown,
	}

	cmd.Flags().StringVar(&shutdownServersPath, "servers", "", "servers.json 路径（参考 ydyl-deploy-client/output/servers.json）")
	_ = cmd.MarkFlagRequired("servers")
	cmd.Flags().StringVar(&shutdownConfigPath, "config", "./config.deploy.yaml", "deploy 配置文件路径（用于读取 SSH 配置）")

	rootCmd.AddCommand(cmd)
}

func runShutdown(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := deploy.LoadConfigFromFile(shutdownConfigPath)

	if err := deploy.Shutdown(ctx, cfg.CommonConfig, shutdownServersPath); err != nil {
		fmt.Fprintln(os.Stderr, "shutdown 失败：", err)
		return err
	}

	return nil
}

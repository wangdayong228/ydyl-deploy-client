package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
)

var (
	deployRestoreIPs []string
)

func init() {
	cmd := &cobra.Command{
		Use:   "deploy-restore",
		Short: "基于已有 output/script_status.json 的任务状态重新执行部署命令",
		Long:  "deploy-restore 命令不会重新创建 EC2 实例，而是从 output/script_status.json 恢复任务：默认仅重跑 failed/pending；running/unknown 仅做 sync（unknown 会在 sync 中基于 PID 与日志判定终态）；指定 --ips 时对命中的 IP 强制重跑（需 command 非空）。",
		RunE:  runDeployRestore,
	}

	cmd.Flags().StringVarP(&configPath, "config", "f", "./config.deploy.yaml", "部署配置文件路径（YAML），用于读取通用配置与 ServiceConfig 列表")
	cmd.Flags().StringSliceVar(&deployRestoreIPs, "ips", nil, "仅恢复指定 IP 列表（逗号分隔或重复传参），未设置时恢复全部")

	rootCmd.AddCommand(cmd)
}

func runDeployRestore(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := deploy.LoadConfigFromFile(configPath)

	if err := deploy.Restore(ctx, cfg.CommonConfig, deployRestoreIPs); err != nil {
		fmt.Fprintln(os.Stderr, "deploy-restore 失败：", err)
		return err
	}

	return nil
}

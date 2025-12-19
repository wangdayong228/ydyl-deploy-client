package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
)

func init() {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "根据已有的 servers.json / script_status.json 同步远程日志和脚本运行状态",
		Long:  "在已经执行过 deploy 并生成 servers.json / script_status.json 的前提下，sync 命令会在不重新创建实例和执行脚本的情况下，重新启动日志与脚本运行状态的同步（可用于进程退出后的恢复）。",
		RunE:  runSync,
	}

	// 与 deploy 复用同一份配置文件参数，主要用于确定 logDir/outputDir/SSH 配置等。
	cmd.Flags().StringVarP(&configPath, "config", "f", "./config.deploy.yaml", "部署配置文件路径（YAML），用于读取通用配置(logDir/outputDir/SSH 等)")

	rootCmd.AddCommand(cmd)
}

func runSync(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := deploy.LoadConfigFromFile(configPath)

	if err := deploy.ResumeSync(ctx, *cfg); err != nil {
		fmt.Fprintln(os.Stderr, "sync 失败：", err)
		return err
	}

	return nil
}



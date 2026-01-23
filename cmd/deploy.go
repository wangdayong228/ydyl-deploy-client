package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
)

var (
	configPath string
)

func init() {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "根据配置文件批量启动 AWS EC2 并在其上执行远程命令",
		Long:  "参考 bash 版本 aws-batch.sh 的流程，实现的 Go 版本批量部署命令：从 YAML 配置文件读取多个 service 的部署参数，启动 EC2、等待 SSH 就绪、按策略执行远程命令。",
		RunE:  runDeploy,
	}

	cmd.Flags().StringVarP(&configPath, "config", "f", "./config.deploy.yaml", "部署配置文件路径（YAML）")
	rootCmd.AddCommand(cmd)
}

func runDeploy(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := deploy.LoadConfigFromFile(configPath)

	if err := deploy.Run(ctx, *cfg); err != nil {
		fmt.Fprintln(os.Stderr, "deploy 失败：", err)
		return err
	}

	return nil
}

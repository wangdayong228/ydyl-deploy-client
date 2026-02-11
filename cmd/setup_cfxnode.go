package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/setupcfxnode"
)

var (
	setupCfxnodeConfigPath string
)

func init() {
	cmd := &cobra.Command{
		Use:   "setup-cfxnode",
		Short: "读取配置并执行 setup-cfxnode.sh",
		Long:  "从配置文件读取 l1RpcUrl 和 l1VaultMnemonic，派生 /m/44/60/0/0/0 私钥后执行 ../setup-cfxnode.sh。",
		RunE:  runSetupCfxnode,
	}

	cmd.Flags().StringVarP(&setupCfxnodeConfigPath, "config", "f", "./config.deploy.yaml", "部署配置文件路径（YAML）")
	rootCmd.AddCommand(cmd)
}

func runSetupCfxnode(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := setupcfxnode.DefaultSetup().Run(ctx, setupcfxnode.Params{
		ConfigPath: setupCfxnodeConfigPath,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "setup-cfxnode 失败：", err)
		return err
	}

	return nil
}

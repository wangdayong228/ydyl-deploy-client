package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
	"github.com/wangdayong228/ydyl-deploy-client/internal/genaccounts"
)

var (
	genAccountsServersPath string
	genAccountsConfigPath  string
)

func init() {
	cmd := &cobra.Command{
		Use:   "gen-accounts",
		Short: "在远程服务器上 start/stop/resume ydyl-gen-accounts",
		Long:  "读取 servers.json，对 OP/CDK 全部节点以及 XJST 组内 node-1 并发 SSH 执行 npm run start|stop|resume。默认 servers 为 ./output/servers.json。",
	}
	cmd.PersistentFlags().StringVar(&genAccountsServersPath, "servers", "./output/servers.json", "servers.json 路径")
	cmd.PersistentFlags().StringVarP(&genAccountsConfigPath, "config", "f", "./config.deploy.yaml", "部署配置文件路径（YAML），用于读取 SSH 参数")

	addGenAccountsAction(cmd, genaccounts.ActionStart, "远程执行 npm run start")
	addGenAccountsAction(cmd, genaccounts.ActionStop, "远程执行 npm run stop")
	addGenAccountsAction(cmd, genaccounts.ActionResume, "远程执行 npm run resume")

	rootCmd.AddCommand(cmd)
}

func addGenAccountsAction(parent *cobra.Command, action genaccounts.Action, short string) {
	sub := &cobra.Command{
		Use:   string(action),
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGenAccounts(cmd.Context(), action)
		},
	}
	parent.AddCommand(sub)
}

func runGenAccounts(ctx context.Context, action genaccounts.Action) error {
	if ctx == nil {
		ctx = context.Background()
	}

	cfg := deploy.LoadConfigFromFile(genAccountsConfigPath)
	sshUser := strings.TrimSpace(cfg.CommonConfig.SSHUser)
	if sshUser == "" {
		return fmt.Errorf("配置项 sshUser 不能为空")
	}
	if strings.TrimSpace(cfg.CommonConfig.KeyName) == "" {
		return fmt.Errorf("配置项 keyName 不能为空")
	}

	if err := genaccounts.Run(ctx, genaccounts.Params{
		ServersPath:    genAccountsServersPath,
		Action:         action,
		SSHUser:        sshUser,
		SSHKeyPath:     deploy.SSHKeyPath(cfg.CommonConfig),
		MaxConcurrency: deploy.SSHMaxConcurrency(cfg.CommonConfig),
	}); err != nil {
		fmt.Fprintln(os.Stderr, "gen-accounts 失败：", err)
		return err
	}
	return nil
}

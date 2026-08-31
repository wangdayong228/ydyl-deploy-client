package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/precheck"
)

var (
	checkConfigPath string
)

func init() {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "部署前预检：artifacts、L1 RPC/WS、jsonrpc-proxy-op",
		Long:  "读取配置文件，检查 OP artifacts 是否可下载、l1RpcUrl/l1RpcUrlWs 是否返回匹配的 eth_chainId，以及 services.op.l1RpcUrl（jsonrpc-proxy-op 公网入口）是否可转发 eth_chainId/eth_blockNumber。失败则非零退出。",
		RunE:  runCheck,
	}

	cmd.Flags().StringVarP(&checkConfigPath, "config", "f", "./config.deploy.yaml", "部署配置文件路径（YAML）")
	rootCmd.AddCommand(cmd)
}

func runCheck(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := precheck.Run(ctx, precheck.Params{ConfigPath: checkConfigPath}); err != nil {
		fmt.Fprintln(os.Stderr, "check 失败：", err)
		return err
	}
	fmt.Println("check 通过")
	return nil
}

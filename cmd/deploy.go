package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
)

var (
	configPath        string
	serversCreatePath string
)

func init() {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "根据配置文件批量启动 AWS EC2 并在其上执行远程命令",
		Long:  "参考 bash 版本 aws-batch.sh 的流程，实现的 Go 版本批量部署命令：从 YAML 配置文件读取多个 service 的部署参数，启动 EC2、等待 SSH 就绪、按策略执行远程命令。",
		RunE:  runDeploy,
	}

	cmd.Flags().StringVarP(&configPath, "config", "f", "./config.deploy.yaml", "部署配置文件路径（YAML）")
	cmd.Flags().StringVar(&serversCreatePath, "servers-create", "", "已有 servers_create.json 路径（会先复制到临时文件再部署；不传则按配置新建 EC2）")
	rootCmd.AddCommand(cmd)
}

func runDeploy(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := deploy.LoadConfigFromFile(configPath)

	opts := deploy.RunOptions{}
	if serversCreatePath != "" {
		origAbs, err := filepath.Abs(serversCreatePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "deploy 失败：解析 --servers-create 路径：", err)
			return err
		}
		tmpAbs, cleanup, err := deploy.CopyServersCreateSnapshotToTemp(serversCreatePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "deploy 失败：", err)
			return err
		}
		defer cleanup()
		log.Printf("servers_create 快照复制: %s -> %s\n", origAbs, tmpAbs)
		opts.ServersCreateJSONPath = tmpAbs
	}

	if err := deploy.RunWithRestoreRetryWithOptions(ctx, *cfg, opts); err != nil {
		fmt.Fprintln(os.Stderr, "deploy 失败：", err)
		return err
	}

	return nil
}

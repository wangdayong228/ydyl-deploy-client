package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ydyl-deploy-client",
	Short: "一站式批量部署脚本的命令行客户端",
	Long:  "ydyl-deploy-client 是一款用 Go 编写的 CLI 工具，用于批量启动 AWS EC2 实例并在其上执行部署脚本。",
}

// Execute 入口
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}



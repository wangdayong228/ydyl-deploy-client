package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/utils/sshutil"
)

var (
	waitSSHIP      string
	waitSSHUser    string
	waitSSHKeyPath string
)

func init() {
	cmd := &cobra.Command{
		Use:   "waitssh",
		Short: "测试到指定主机的 SSH 连通性",
		Long:  "使用本机 ssh 命令测试到指定 IP 的 SSH 连通性，可用于在真正部署前验证网络与密钥配置是否正确。",
		RunE:  runWaitSSH,
	}

	cmd.Flags().StringVarP(&waitSSHIP, "ip", "i", "", "待测试的目标 IP（必填）")
	cmd.Flags().StringVarP(&waitSSHUser, "user", "u", "ubuntu", "SSH 登录用户名")
	cmd.Flags().StringVarP(&waitSSHKeyPath, "key", "k", "", "SSH 私钥路径（例如 ~/.ssh/id_rsa，对应 -i 参数）")

	_ = cmd.MarkFlagRequired("ip")
	_ = cmd.MarkFlagRequired("key")

	rootCmd.AddCommand(cmd)
}

func runWaitSSH(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	keyPath := waitSSHKeyPath
	if len(keyPath) > 0 && keyPath[0] == '~' {
		home, err := os.UserHomeDir()
		if err == nil {
			rest := strings.TrimPrefix(keyPath[1:], string(filepath.Separator))
			keyPath = filepath.Join(home, rest)
		}
	}

	if err := sshutil.WaitSSH(ctx, waitSSHIP, waitSSHUser, keyPath); err != nil {
		fmt.Fprintln(os.Stderr, "SSH 连通性检测失败：", err)
		return err
	}

	fmt.Println("SSH 连通性正常")
	return nil
}

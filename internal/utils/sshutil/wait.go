package sshutil

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WaitSSH 使用本地 ssh 命令探测到指定 IP 的 SSH 连通性。
// 成功返回 nil，超时或多次失败返回错误。
func WaitSSH(ctx context.Context, ip, sshUser, sshKeyPath string) error {
	sshKeyPath = strings.TrimSpace(sshKeyPath)
	if strings.HasPrefix(sshKeyPath, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			rest := strings.TrimPrefix(sshKeyPath, "~")
			rest = strings.TrimPrefix(rest, string(filepath.Separator))
			sshKeyPath = filepath.Join(home, rest)
		}
	}

	fmt.Printf("WaitSSH: ip=%s, sshUser=%s, sshKeyPath=%s ", ip, sshUser, sshKeyPath)
	defer fmt.Println()

	const (
		maxRetry        = 60
		retryInterval   = 3 * time.Second
		singleTimeout   = 10 * time.Second
		sshBinary       = "ssh"
		hostKeyChecking = "no"
	)

	if _, err := os.Stat(sshKeyPath); err != nil {
		return fmt.Errorf("SSH 私钥文件不可用: %s: %w", sshKeyPath, err)
	}

	for i := 0; i < maxRetry; i++ {
		fmt.Print(".")

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		sshCtx, cancel := context.WithTimeout(ctx, singleTimeout)
		cmd := exec.CommandContext(sshCtx, sshBinary,
			"-o", "StrictHostKeyChecking="+hostKeyChecking,
			"-o", "BatchMode=yes",
			"-o", "NumberOfPasswordPrompts=0",
			"-o", "IdentitiesOnly=yes",
			"-o", fmt.Sprintf("ConnectTimeout=%d", int(singleTimeout.Seconds())),
			"-o", "UserKnownHostsFile=/dev/null",
			"-i", sshKeyPath,
			fmt.Sprintf("%s@%s", sshUser, ip),
			"true",
		)

		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf

		if err := cmd.Run(); err == nil {
			cancel()
			return nil
		}

		if out := strings.TrimSpace(buf.String()); out != "" {
			fmt.Printf("\n[%s] ssh 输出: %s\n", ip, out)
		}

		cancel()
		time.Sleep(retryInterval)
	}

	return fmt.Errorf("[%s] SSH 一直未就绪", ip)
}


package sshutil

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// WaitSSH 使用本地 ssh 命令探测到指定 IP 的 SSH 连通性。
// 成功返回 nil，超时或多次失败返回错误。
func WaitSSH(ctx context.Context, ip, sshUser, sshKeyPath string) error {
	fmt.Printf("WaitSSH: ip=%s, sshUser=%s, sshKeyPath=%s ", ip, sshUser, sshKeyPath)
	defer fmt.Println()

	const (
		maxRetry        = 60
		retryInterval   = 3 * time.Second
		singleTimeout   = 3 * time.Second
		sshBinary       = "ssh"
		hostKeyChecking = "no"
	)

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
			"-o", "IdentitiesOnly=yes",
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=3",
			"-i", sshKeyPath,
			fmt.Sprintf("%s@%s", sshUser, ip),
			"true",
		)

		if err := cmd.Run(); err == nil {
			cancel()
			return nil
		}

		cancel()
		time.Sleep(retryInterval)
	}

	return fmt.Errorf("[%s] SSH 一直未就绪", ip)
}

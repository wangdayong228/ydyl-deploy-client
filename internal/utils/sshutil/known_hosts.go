package sshutil

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnsureKnownHosts 将尚未出现在本机 known_hosts 中的 IP 通过 ssh-keyscan 写入。
// 单个 IP 失败不会中断其余 IP；返回聚合错误供调用方记录。
func EnsureKnownHosts(ctx context.Context, ips []string) error {
	knownHosts, err := defaultKnownHostsPath()
	if err != nil {
		return err
	}
	sshDir := filepath.Dir(knownHosts)
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return fmt.Errorf("创建 .ssh 目录失败: %w", err)
	}

	var errs []error
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		if err := ensureKnownHost(ctx, knownHosts, ip); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func ensureKnownHost(ctx context.Context, knownHostsPath, ip string) error {
	exists, err := hostInKnownHosts(knownHostsPath, ip)
	if err != nil {
		return fmt.Errorf("[%s] 检查 known_hosts 失败: %w", ip, err)
	}
	if exists {
		log.Printf("ℹ️ [known_hosts] %s 已存在，跳过\n", ip)
		return nil
	}

	if err := appendHostKeyscan(ctx, knownHostsPath, ip); err != nil {
		return fmt.Errorf("[%s] 写入 known_hosts 失败: %w", ip, err)
	}
	log.Printf("✅ [known_hosts] 已添加 %s\n", ip)
	return nil
}

func defaultKnownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户主目录失败: %w", err)
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

func hostInKnownHosts(knownHostsPath, ip string) (bool, error) {
	if _, err := os.Stat(knownHostsPath); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}

	cmd := exec.Command("ssh-keygen", "-F", ip, "-f", knownHostsPath)
	runErr := cmd.Run()
	if runErr == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, runErr
}

func appendHostKeyscan(ctx context.Context, knownHostsPath, ip string) error {
	cmd := exec.CommandContext(ctx, "ssh-keyscan", "-H", "-T", "10", ip)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("ssh-keyscan 失败: %w", err)
	}

	lines := filterKeyscanLines(string(out))
	if len(lines) == 0 {
		return fmt.Errorf("ssh-keyscan 未返回有效 host key")
	}

	f, err := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("打开 known_hosts 失败: %w", err)
	}
	defer f.Close()

	for _, line := range lines {
		if _, err := fmt.Fprintln(f, line); err != nil {
			return fmt.Errorf("写入 known_hosts 失败: %w", err)
		}
	}
	return nil
}

func filterKeyscanLines(out string) []string {
	lines := strings.Split(out, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		filtered = append(filtered, line)
	}
	return filtered
}

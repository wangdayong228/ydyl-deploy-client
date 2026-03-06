package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var runShutdownSSHCommandFunc = runShutdownSSHCommand

// Shutdown 从指定 servers.json 读取服务器列表，并通过 SSH 下发关机命令。
func Shutdown(ctx context.Context, commonCfg CommonConfig, serversPath string) error {
	serversPath = strings.TrimSpace(serversPath)
	if serversPath == "" {
		return fmt.Errorf("servers 路径不能为空")
	}

	servers, err := loadServersFromFile(serversPath)
	if err != nil {
		return err
	}
	if len(servers) == 0 {
		return fmt.Errorf("servers.json 为空: %s", serversPath)
	}

	ips := collectUniqueServerIPs(servers)
	if len(ips) == 0 {
		return fmt.Errorf("servers.json 中未找到有效 IP")
	}

	sshUser := strings.TrimSpace(commonCfg.SSHUser)
	if sshUser == "" {
		return fmt.Errorf("sshUser 不能为空")
	}
	sshKeyPath := buildSSHKeyPath(commonCfg)

	var (
		mu   sync.Mutex
		errs []error
	)

	addErr := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, err)
	}

	log.Printf("👉 [shutdown] 开始关机，共 %d 台服务器\n", len(ips))
	runWithBatchLimit("shutdown-servers", len(ips), resolveSSHMaxConcurrency(commonCfg), func(i int) {
		ip := ips[i]
		output, runErr := runShutdownSSHCommandFunc(ctx, sshUser, sshKeyPath, ip)
		if runErr != nil {
			msg := strings.TrimSpace(output)
			if msg != "" {
				addErr(fmt.Errorf("[%s] 关机命令执行失败: %w，输出: %s", ip, runErr, msg))
				return
			}
			addErr(fmt.Errorf("[%s] 关机命令执行失败: %w", ip, runErr))
			return
		}
		log.Printf("[shutdown][%s] 关机命令已下发\n", ip)
	})

	if len(errs) > 0 {
		return deployMultiError{errs: errs}
	}
	log.Printf("✅ [shutdown] 关机命令已全部下发，共 %d 台\n", len(ips))
	return nil
}

func loadServersFromFile(path string) ([]ServerInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 servers.json 失败: %w", err)
	}

	var servers []ServerInfo
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil, fmt.Errorf("解析 servers.json 失败: %w", err)
	}
	return servers, nil
}

func collectUniqueServerIPs(servers []ServerInfo) []string {
	seen := make(map[string]struct{}, len(servers))
	ips := make([]string, 0, len(servers))
	for _, s := range servers {
		ip := strings.TrimSpace(s.IP)
		if ip == "" {
			continue
		}
		if _, ok := seen[ip]; ok {
			continue
		}
		seen[ip] = struct{}{}
		ips = append(ips, ip)
	}
	return ips
}

func runShutdownSSHCommand(ctx context.Context, sshUser, sshKeyPath, ip string) (string, error) {
	sshCmd := exec.CommandContext(ctx, "ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "IdentitiesOnly=yes",
		"-i", sshKeyPath,
		fmt.Sprintf("%s@%s", sshUser, ip),
		"sudo -n shutdown -h now",
	)

	var stdoutBuf bytes.Buffer
	sshCmd.Stdout = &stdoutBuf
	sshCmd.Stderr = &stdoutBuf

	err := sshCmd.Run()
	return stdoutBuf.String(), err
}

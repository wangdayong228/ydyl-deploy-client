package deploy

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Restorer 负责基于 script_status.json 的信息，重新在已有服务器上执行部署脚本。
type Restorer struct {
	cfg       CommonConfig
	outputMgr *OutputManager
}

func NewRestorer(cfg CommonConfig, mgr *OutputManager) *Restorer {
	if mgr == nil {
		return nil
	}
	return &Restorer{
		cfg:       cfg,
		outputMgr: mgr,
	}
}

// Restore 基于已有的 output/script_status.json 中的服务器列表与命令，
// 重新在这些机器上执行部署脚本。不会重新创建 EC2 实例，只依赖 CommonConfig 与脚本状态文件。
func Restore(ctx context.Context, commonCfg CommonConfig, targetIPs []string) error {
	log.Printf("👉 开始恢复，配置: %+v\n", commonCfg)

	if err := os.MkdirAll(commonCfg.LogDir, 0o755); err != nil {
		return fmt.Errorf("创建日志目录失败: %w", err)
	}

	if commonCfg.OutputDir == "" {
		commonCfg.OutputDir = filepath.Join(commonCfg.LogDir, "output")
	}

	outputMgr, err := LoadOutputManager(commonCfg.OutputDir)
	if err != nil {
		return fmt.Errorf("加载输出状态失败: %w", err)
	}

	// 优先从 script_status.json 中恢复服务器列表与命令（它记录了真实运行过的任务）。
	statuses := outputMgr.SnapshotStatuses()
	if len(statuses) == 0 {
		return fmt.Errorf("在输出目录 %s 中未找到任何脚本状态信息（script_status.json 为空或不存在）", commonCfg.OutputDir)
	}

	filteredStatuses, err := filterStatusesByIPs(statuses, targetIPs)
	if err != nil {
		return err
	}

	restorer := NewRestorer(commonCfg, outputMgr)
	return restorer.Run(ctx, filteredStatuses)
}

func sanitizeTargetIPs(targetIPs []string) []string {
	if len(targetIPs) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(targetIPs))
	cleaned := make([]string, 0, len(targetIPs))
	for _, ip := range targetIPs {
		trimmed := strings.TrimSpace(ip)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		cleaned = append(cleaned, trimmed)
	}

	return cleaned
}

func filterStatusesByIPs(statuses []*ScriptStatus, targetIPs []string) ([]*ScriptStatus, error) {
	cleanedIPs := sanitizeTargetIPs(targetIPs)
	if len(cleanedIPs) == 0 {
		return filterNonSuccessStatuses(statuses), nil
	}

	existingIPs := make(map[string]struct{}, len(statuses))
	for _, st := range statuses {
		if st == nil || st.IP == "" {
			continue
		}
		existingIPs[st.IP] = struct{}{}
	}

	missing := make([]string, 0)
	for _, ip := range cleanedIPs {
		if _, ok := existingIPs[ip]; !ok {
			missing = append(missing, ip)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("指定的 IP 在 script_status.json 中不存在: %s", strings.Join(missing, ", "))
	}

	allowed := make(map[string]struct{}, len(cleanedIPs))
	for _, ip := range cleanedIPs {
		allowed[ip] = struct{}{}
	}

	filtered := make([]*ScriptStatus, 0, len(statuses))
	for _, st := range statuses {
		if st == nil {
			continue
		}
		if _, ok := allowed[st.IP]; ok {
			filtered = append(filtered, st)
		}
	}

	return filtered, nil
}

func filterNonSuccessStatuses(statuses []*ScriptStatus) []*ScriptStatus {
	filtered := make([]*ScriptStatus, 0, len(statuses))
	for _, st := range statuses {
		if st == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(st.Status), "success") {
			continue
		}
		filtered = append(filtered, st)
	}
	return filtered
}

// Run 启动恢复流程：基于 script_status.json 中的状态，重新在对应机器上执行脚本，并重新开始同步日志与脚本状态。
func (r *Restorer) Run(ctx context.Context, statuses []*ScriptStatus) error {
	if r == nil || r.outputMgr == nil {
		return nil
	}

	keyPath := buildSSHKeyPath(r.cfg)

	var (
		mu   sync.Mutex
		errs []error
	)

	eligible := make([]ScriptStatus, 0, len(statuses))
	for _, st := range statuses {
		if st == nil || st.IP == "" || st.Command == "" {
			continue
		}
		eligible = append(eligible, *st)
	}

	addErr := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, err)
	}

	runWithBatchLimit("restore-run-remote-command", len(eligible), resolveSSHMaxConcurrency(r.cfg), func(i int) {
		st := eligible[i]
		name := st.Name
		if name == "" {
			name = fmt.Sprintf("%s-%s", st.ServiceType, st.IP)
		}

		attempts, readyErr := waitSSHReadyWithRetry(
			ctx,
			st.IP,
			r.cfg.SSHUser,
			keyPath,
			resolveSSHReadyRetryCount(r.cfg),
			resolveSSHReadyRetryInterval(r.cfg),
		)
		now := time.Now().Unix()
		if readyErr != nil {
			addErr(fmt.Errorf("[restore][%s][%s] %w", st.IP, name, readyErr))
			if persistErr := r.outputMgr.UpdateSSHScriptStatus(st.IP, st.ServiceType, name, "fail", attempts, readyErr.Error(), now); persistErr != nil {
				addErr(fmt.Errorf("[restore][%s][%s] 写入 ssh_scripts.json 失败: %w", st.IP, name, persistErr))
			}
			return
		}
		if persistErr := r.outputMgr.UpdateSSHScriptStatus(st.IP, st.ServiceType, name, "success", attempts, "", now); persistErr != nil {
			addErr(fmt.Errorf("[restore][%s][%s] 写入 ssh_scripts.json 失败: %w", st.IP, name, persistErr))
			return
		}

		if err := r.runForStatus(ctx, st, keyPath); err != nil {
			addErr(err)
		}
	})

	if len(errs) > 0 {
		return deployMultiError{errs: errs}
	}

	log.Println("👉 [restore] 所有远程命令已启动，开始同步日志与脚本状态...")

	s := NewSync(r.cfg, r.outputMgr)
	if err := s.Run(ctx); err != nil {
		return err
	}

	log.Println("✅ deploy-restore 执行完成！")
	return nil
}

// runForStatus 在单个 ScriptStatus 对应的服务器上重新执行脚本，并更新该条状态。
func (r *Restorer) runForStatus(ctx context.Context, st ScriptStatus, keyPath string) error {
	name := st.Name
	if name == "" {
		name = fmt.Sprintf("%s-%s", st.ServiceType, st.IP)
	}

	remoteLogFile, remoteLogDir := buildRemoteLogPath(st.LogPath, name)
	fullCmd := buildBackgroundCommand(r.cfg.RunDuration, st.Command, remoteLogDir, remoteLogFile)

	log.Printf("[restore][%s] run (background): %s\n", st.IP, fullCmd)

	localLogPath := st.LocalLog
	if localLogPath == "" {
		localLogPath = buildLocalLogPath(r.cfg.LogDir, st.IP, name)
	}

	sshCmd := exec.CommandContext(ctx, "ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "IdentitiesOnly=yes",
		"-i", keyPath,
		fmt.Sprintf("%s@%s", r.cfg.SSHUser, st.IP),
		fullCmd,
	)

	var stdoutBuf bytes.Buffer
	sshCmd.Stdout = &stdoutBuf
	sshCmd.Stderr = &stdoutBuf

	if err := sshCmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			log.Printf("[restore][%s] ssh 命令执行失败，exitCode=%d，cmd=%q\n", st.IP, exitErr.ExitCode(), fullCmd)
		} else {
			log.Printf("[restore][%s] ssh 命令执行失败（非 ExitError），cmd=%q，err=%v\n", st.IP, fullCmd, err)
		}
		return fmt.Errorf("[restore][%s] 远程命令执行失败: %w", st.IP, err)
	}

	pid, parseErr := parseRemotePID(stdoutBuf.String())
	if parseErr != nil {
		log.Printf("[restore][%s] 解析远端 PID 失败: %v，输出: %q\n", st.IP, parseErr, stdoutBuf.String())
	}

	now := time.Now().Unix()
	_ = r.outputMgr.UpdateStatus(
		st.IP,
		st.ServiceType,
		func(s *ScriptStatus) {
			s.Name = name
			s.Command = st.Command
			s.PID = pid
			s.Status = "running"
			s.LogPath = remoteLogFile
			s.LocalLog = localLogPath
			s.UpdatedAt = now
			s.LogSize = 0
		},
	)
	return nil
}

// 其余辅助方法见 exec_helpers.go

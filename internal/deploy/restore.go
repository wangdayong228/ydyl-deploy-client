package deploy

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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
func Restore(ctx context.Context, commonCfg CommonConfig) error {
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

	restorer := NewRestorer(commonCfg, outputMgr)
	return restorer.Run(ctx, statuses)
}

// Run 启动恢复流程：基于 script_status.json 中的状态，重新在对应机器上执行脚本，并重新开始同步日志与脚本状态。
func (r *Restorer) Run(ctx context.Context, statuses []*ScriptStatus) error {
	if r == nil || r.outputMgr == nil {
		return nil
	}

	keyPath := buildSSHKeyPath(r.cfg)

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
	)

	for _, st := range statuses {
		// 仅对有 IP 且有 Command 的记录进行恢复；其他记录跳过
		if st == nil || st.IP == "" || st.Command == "" {
			continue
		}

		// 避免 goroutine 闭包捕获共享指针，复制一份
		stCopy := *st

		wg.Add(1)
		go func(st ScriptStatus) {
			defer wg.Done()
			r.runForStatus(ctx, st, keyPath, &mu, &first)
		}(stCopy)
	}

	wg.Wait()
	if first != nil {
		return first
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
func (r *Restorer) runForStatus(ctx context.Context, st ScriptStatus, keyPath string, mu *sync.Mutex, first *error) {
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
		mu.Lock()
		if *first == nil {
			*first = fmt.Errorf("[restore][%s] 远程命令执行失败: %w", st.IP, err)
		}
		mu.Unlock()
		return
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
}

// 其余辅助方法见 exec_helpers.go

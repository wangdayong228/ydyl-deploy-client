package deploy

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sync 负责监控所有实例上脚本的运行状态，并同步远端日志到本地。
type Sync struct {
	cfg       CommonConfig
	outputMgr *OutputManager
}

func NewSync(cfg CommonConfig, mgr *OutputManager) *Sync {
	if mgr == nil {
		return nil
	}
	return &Sync{
		cfg:       cfg,
		outputMgr: mgr,
	}
}

// ResumeSync 基于已有的 script_status.json 重新同步日志与脚本状态。
// 适用于部署进程意外退出或者终端关闭后，在不重新创建实例和执行脚本的前提下恢复监控。
func ResumeSync(ctx context.Context, commonCfg CommonConfig) error {
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

	log.Println("👉 载入已有 script_status.json，开始重新同步日志与脚本状态...")

	s := NewSync(commonCfg, outputMgr)
	if err := s.Run(ctx); err != nil {
		return err
	}

	log.Println("✅ 日志与脚本状态同步完成！")
	return nil
}

// Run 启动同步协程，定期同步远端日志到本地，并根据进程/日志更新脚本运行状态。
func (m *Sync) Run(ctx context.Context) error {
	if m == nil || m.outputMgr == nil {
		return nil
	}

	keyPath := buildSSHKeyPath(m.cfg)
	statuses := m.outputMgr.SnapshotStatuses()
	sshSem := make(chan struct{}, resolveSSHMaxConcurrency(m.cfg))

	var (
		wg    sync.WaitGroup
		muErr sync.Mutex
		first error
	)

	for _, st := range statuses {
		if st == nil || st.Status != "running" || st.PID <= 0 || st.LogPath == "" {
			continue
		}

		wg.Add(1)
		go func(st *ScriptStatus) {
			defer wg.Done()

			localLogPath := st.LocalLog
			if localLogPath == "" {
				localLogPath = buildLocalLogPath(m.cfg.LogDir, st.IP, st.Name)
			}

			// 记录已同步的远端日志大小，用于增量拉取
			var offset int64 = st.LogSize
			// 连续拉取远端日志失败次数；达到阈值才判定失败，避免短暂网络抖动导致误判。
			consecutiveLogFetchFailures := 0

			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				// 1. 从远端增量获取日志并追加到本地（受 SSH 并发限制）
				var (
					logData []byte
					newSize int64
					err     error
				)
				err = withSSHToken(ctx, sshSem, st.IP, "fetch-log-delta", func() error {
					logData, newSize, err = m.fetchRemoteLogDelta(ctx, m.cfg.SSHUser, keyPath, st.IP, st.LogPath, offset)
					return err
				})
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					consecutiveLogFetchFailures++
					log.Printf("[%s] 获取远端日志失败（%s），连续失败 %d/10: %v\n", st.IP, st.LogPath, consecutiveLogFetchFailures, err)
					log.Printf("[%s] 本轮日志同步完成，result=failed，连续失败=%d/10\n", st.IP, consecutiveLogFetchFailures)
					if consecutiveLogFetchFailures >= 10 {
						now := time.Now().Unix()
						reason := fmt.Sprintf("连续 10 次获取远端日志失败: %v", err)
						_ = m.outputMgr.UpdateStatus(st.IP, st.ServiceType, func(s *ScriptStatus) {
							s.Status = "failed"
							s.Reason = reason
							s.UpdatedAt = now
							if s.LocalLog == "" {
								s.LocalLog = localLogPath
							}
							s.LogSize = offset
						})

						muErr.Lock()
						if first == nil {
							first = fmt.Errorf("远端日志同步失败: ip=%s serviceType=%s: %s", st.IP, st.ServiceType, reason)
						}
						muErr.Unlock()
						return
					}
					time.Sleep(5 * time.Second)
					continue
				} else {
					consecutiveLogFetchFailures = 0
					if len(logData) > 0 {
						if writeErr := appendToFile(localLogPath, logData); writeErr != nil {
							log.Printf("[%s] 写入本地日志失败 %s: %v\n", st.IP, localLogPath, writeErr)
						}
						offset = newSize
					}
				}

				// 2. 检查远端进程是否仍在运行（受 SSH 并发限制）
				var (
					running  bool
					checkErr error
				)
				checkErr = withSSHToken(ctx, sshSem, st.IP, "check-remote-process", func() error {
					running, checkErr = checkRemoteProcess(ctx, m.cfg.SSHUser, keyPath, st.IP, st.PID)
					return checkErr
				})
				now := time.Now().Unix()

				if checkErr != nil {
					if ctx.Err() != nil {
						return
					}
					log.Printf("[%s] 检查远端进程状态失败(pid=%d): %v\n", st.IP, st.PID, checkErr)
				}

				if running {
					// 仍在运行，更新更新时间即可
					_ = m.outputMgr.UpdateStatus(st.IP, st.ServiceType, func(s *ScriptStatus) {
						s.Status = "running"
						s.UpdatedAt = now
						if s.LocalLog == "" {
							s.LocalLog = localLogPath
						}
						s.LogSize = offset
					})
				} else {
					// 已结束，为了确保本地日志完整，再做一次兜底的增量拉取（受 SSH 并发限制）
					var (
						finalData []byte
						finalSize int64
						finalErr  error
					)
					finalErr = withSSHToken(ctx, sshSem, st.IP, "fetch-log-final", func() error {
						finalData, finalSize, finalErr = m.fetchRemoteLogDelta(ctx, m.cfg.SSHUser, keyPath, st.IP, st.LogPath, offset)
						return finalErr
					})
					if finalErr != nil {
						if ctx.Err() != nil {
							return
						}
						log.Printf("[%s] 结束前最后一次获取远端日志失败（%s）: %v\n", st.IP, st.LogPath, finalErr)
					} else if len(finalData) > 0 {
						if writeErr := appendToFile(localLogPath, finalData); writeErr != nil {
							log.Printf("[%s] 写入本地日志失败(最终同步) %s: %v\n", st.IP, localLogPath, writeErr)
						}
						offset = finalSize
						// 将 logData 与最后一段拼在一起用于状态判断，避免刚好把错误信息分在最后一次里却没参与判断
						logData = append(logData, finalData...)
					}

					// 根据最新的日志内容尽量推断成功 / 失败
					status, reason := deriveStatusFromLog(logData, st)
					_ = m.outputMgr.UpdateStatus(st.IP, st.ServiceType, func(s *ScriptStatus) {
						s.Status = status
						s.Reason = reason
						s.UpdatedAt = now
						if s.LocalLog == "" {
							s.LocalLog = localLogPath
						}
						s.LogSize = offset
					})

					if status == "failed" {
						muErr.Lock()
						if first == nil {
							first = fmt.Errorf("远程脚本执行失败: ip=%s serviceType=%s: %s", st.IP, st.ServiceType, reason)
						}
						muErr.Unlock()
					}

					return
				}

				time.Sleep(5 * time.Second)
			}
		}(st)
	}

	wg.Wait()
	return first
}

func withSSHToken(ctx context.Context, sem chan struct{}, ip, action string, fn func() error) error {
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		log.Printf("[%s] 等待 SSH 并发令牌取消(action=%s): %v\n", ip, action, ctx.Err())
		return ctx.Err()
	}
	defer func() {
		<-sem
	}()

	return fn()
}

// fetchRemoteLogDelta 通过 ssh 从远端增量拉取日志内容。
// lastSize 为上次已同步的字节数，返回本次新增的日志内容以及最新的总大小。
func (m *Sync) fetchRemoteLogDelta(ctx context.Context, user, keyPath, ip, remotePath string, lastSize int64) ([]byte, int64, error) {
	if remotePath == "" {
		return nil, lastSize, fmt.Errorf("远端日志路径为空")
	}

	// 先获取远端文件大小（字节数）
	sizeCmd := exec.CommandContext(ctx, "ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "IdentitiesOnly=yes",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-i", keyPath,
		fmt.Sprintf("%s@%s", user, ip),
		fmt.Sprintf(`wc -c < %s 2>/dev/null || echo 0`, remotePath),
	)

	var sizeOut bytes.Buffer
	var sizeErr bytes.Buffer
	sizeCmd.Stdout = &sizeOut
	sizeCmd.Stderr = &sizeErr

	if err := sizeCmd.Run(); err != nil {
		return nil, lastSize, err
	}

	newSize, err := parseLastInt64Line(sizeOut.String())
	if err != nil {
		return nil, lastSize, fmt.Errorf("解析远端日志大小失败: stdout=%q, stderr=%q, err=%v", sizeOut.String(), sizeErr.String(), err)
	}

	if newSize <= lastSize {
		// 没有新增内容
		return nil, newSize, nil
	}

	// 只拉取新增加的部分
	start := lastSize + 1
	logCmd := exec.CommandContext(ctx, "ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "IdentitiesOnly=yes",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-i", keyPath,
		fmt.Sprintf("%s@%s", user, ip),
		fmt.Sprintf("tail -c +%d %s 2>/dev/null || true", start, remotePath),
	)

	var stdout bytes.Buffer
	logCmd.Stdout = &stdout
	logCmd.Stderr = &stdout

	if err := logCmd.Run(); err != nil {
		return nil, lastSize, err
	}

	return stdout.Bytes(), newSize, nil
}

// appendToFile 将数据追加写入到指定文件（必要时创建）。
func appendToFile(path string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// checkRemoteProcess 检查远端某个 PID 是否仍在运行。
func checkRemoteProcess(ctx context.Context, user, keyPath, ip string, pid int) (bool, error) {
	sshCmd := exec.CommandContext(ctx, "ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "IdentitiesOnly=yes",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-i", keyPath,
		fmt.Sprintf("%s@%s", user, ip),
		fmt.Sprintf("ps -p %d -o pid=", pid),
	)

	if err := sshCmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 0 {
			// 进程不存在
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// deriveStatusFromLog 尝试根据日志内容推断脚本执行结果。
// 这里 data 一般是日志的最新一段（不是全量），对于 cdk 脚本足够判断成功/失败。
func deriveStatusFromLog(data []byte, st *ScriptStatus) (status string, reason string) {
	// 去掉 set -x 打印出来的命令行（通常以 "+" 开头），避免命令回显干扰状态判断。
	s := stripXTraceLines(string(data))

	// 对 CDK service 做更精确的判断：依赖 cdk_pipe.sh 的固定输出
	if st != nil && strings.Contains(strings.ToLower(st.ServiceType), "cdk") {
		// cdk_pipe.sh 失败时 trap 会输出 “cdk_pipe.sh 执行失败”
		if strings.Contains(s, "cdk_pipe.sh 执行失败") {
			return "failed", "cdk_pipe.sh 日志中包含失败信息，请查看详细日志"
		}
		// 成功路径的最后会打印 “所有步骤完成”
		if strings.Contains(s, "所有步骤完成") {
			return "success", ""
		}
		// 未命中特征时，标记为 unknown，交由人工查看日志确认
		return "unknown", "无法从日志中判断脚本状态，请查看详细日志"
	}

	if containsAny(s, []string{"所有步骤完成"}) {
		return "success", ""
	}

	return "failed", "日志中包含错误关键词，请查看详细日志"
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func parseLastInt64Line(output string) (int64, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return 0, fmt.Errorf("输出为空")
	}

	lines := strings.Split(trimmed, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		val, err := strconv.ParseInt(line, 10, 64)
		if err == nil {
			return val, nil
		}
	}

	return 0, fmt.Errorf("未找到可解析的整数行: %q", output)
}

// stripXTraceLines 过滤掉 bash set -x 打印出来的命令行（通常以 "+" 开头），只保留真实业务输出。
func stripXTraceLines(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return s
	}

	var b strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "+") {
			// 认为是 set -x 的命令展示，忽略
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String()
}

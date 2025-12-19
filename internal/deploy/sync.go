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

// Run 启动同步协程，定期同步远端日志到本地，并根据进程/日志更新脚本运行状态。
func (m *Sync) Run(ctx context.Context) error {
	if m == nil || m.outputMgr == nil {
		return nil
	}

	keyPath := buildSSHKeyPath(m.cfg)
	statuses := m.outputMgr.SnapshotStatuses()

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
				localLogPath = filepath.Join(m.cfg.LogDir, fmt.Sprintf("%s-%s.log", st.IP, st.ServiceType))
			}

			// 记录已同步的远端日志大小，用于增量拉取
			var offset int64 = st.LogSize

			for {
				select {
				case <-ctx.Done():
					return
				default:
				}

				// 1. 从远端增量获取日志并追加到本地
				logData, newSize, err := m.fetchRemoteLogDelta(ctx, m.cfg.SSHUser, keyPath, st.IP, st.LogPath, offset)
				if err != nil {
					log.Printf("[%s] 获取远端日志失败（%s）: %v\n", st.IP, st.LogPath, err)
				} else {
					if len(logData) > 0 {
						if writeErr := appendToFile(localLogPath, logData); writeErr != nil {
							log.Printf("[%s] 写入本地日志失败 %s: %v\n", st.IP, localLogPath, writeErr)
						}
						offset = newSize
					}
				}

				// 2. 检查远端进程是否仍在运行
				running, checkErr := checkRemoteProcess(ctx, m.cfg.SSHUser, keyPath, st.IP, st.PID)
				now := time.Now().Unix()

				if checkErr != nil {
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
					// 已结束，为了确保本地日志完整，再做一次兜底的增量拉取
					finalData, finalSize, finalErr := m.fetchRemoteLogDelta(ctx, m.cfg.SSHUser, keyPath, st.IP, st.LogPath, offset)
					if finalErr != nil {
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
		"-i", keyPath,
		fmt.Sprintf("%s@%s", user, ip),
		fmt.Sprintf(`wc -c < %s 2>/dev/null || echo 0`, remotePath),
	)

	var sizeOut bytes.Buffer
	sizeCmd.Stdout = &sizeOut
	sizeCmd.Stderr = &sizeOut

	if err := sizeCmd.Run(); err != nil {
		return nil, lastSize, err
	}

	var newSize int64
	if _, err := fmt.Sscan(sizeOut.String(), &newSize); err != nil {
		return nil, lastSize, fmt.Errorf("解析远端日志大小失败: %q, err=%v", sizeOut.String(), err)
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
	if len(data) == 0 {
		return "success", ""
	}

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

	// 非严格判断：如果包含明显的 ERROR/FAIL 关键字，则认为失败。
	if containsAny(s, []string{"ERROR", "Error", "Failed", "FAIL"}) {
		// 简单返回第一条匹配到的关键字作为 reason
		return "failed", "日志中包含错误关键词，请查看详细日志"
	}
	return "success", ""
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
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

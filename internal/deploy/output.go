package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ServerInfo 描述一台服务器在本次部署中的基础信息。
type ServerInfo struct {
	IP          string `json:"ip"`
	ServiceType string `json:"serviceType"`
	Name        string `json:"name,omitempty"`
}

// ScriptStatus 描述某台服务器上远程脚本的运行状态。
type ScriptStatus struct {
	IP          string `json:"ip"`
	ServiceType string `json:"serviceType"`

	// Name 为本次部署中给这台服务器生成的逻辑名称（通常与 EC2 Name tag 保持一致）
	Name string `json:"name,omitempty"`
	// Command 为在这台机器上实际执行的部署命令（不含外层 shutdown/nohup 包装）
	Command string `json:"command,omitempty"`

	PID       int    `json:"pid"`
	Status    string `json:"status"`              // running / success / failed / unknown
	Reason    string `json:"reason,omitempty"`    // 失败或未知时的原因描述
	LogPath   string `json:"logPath,omitempty"`   // 远端日志路径
	LocalLog  string `json:"localLog,omitempty"`  // 本地同步日志路径
	UpdatedAt int64  `json:"updatedAt,omitempty"` // 状态最近更新时间（Unix 秒）
	// LogSize 记录已经同步的远端日志字节数，用于增量拉取
	LogSize int64 `json:"logSize,omitempty"`
}

// SSHScriptStatus 描述 SSH 就绪探测结果。
type SSHScriptStatus struct {
	IP          string `json:"ip"`
	ServiceType string `json:"serviceType"`
	Name        string `json:"name,omitempty"`
	Status      string `json:"status"`              // success / fail
	Attempts    uint   `json:"attempts,omitempty"`  // 实际尝试次数（含首次）
	Reason      string `json:"reason,omitempty"`    // 失败原因
	UpdatedAt   int64  `json:"updatedAt,omitempty"` // 状态最近更新时间（Unix 秒）
}

// OutputManager 负责维护 servers.json 和 script_status.json 两个输出文件。
type OutputManager struct {
	outputDir string

	mu         sync.Mutex
	servers    []ServerInfo
	allIPs     []string
	allIPSet   map[string]struct{}
	statuses   map[string]*ScriptStatus
	sshScripts map[string]*SSHScriptStatus
}

func NewOutputManager(outputDir string) *OutputManager {
	return &OutputManager{
		outputDir:  outputDir,
		allIPSet:   make(map[string]struct{}),
		statuses:   make(map[string]*ScriptStatus),
		sshScripts: make(map[string]*SSHScriptStatus),
	}
}

// LoadOutputManager 从指定目录下已有的 JSON 文件（servers.json / script_status.json）恢复 OutputManager。
// 主要用于进程重启后，基于已有状态重新进行日志与脚本状态同步。
func LoadOutputManager(outputDir string) (*OutputManager, error) {
	m := &OutputManager{
		outputDir:  outputDir,
		allIPSet:   make(map[string]struct{}),
		statuses:   make(map[string]*ScriptStatus),
		sshScripts: make(map[string]*SSHScriptStatus),
	}

	// 尝试加载 servers.json（如果不存在则忽略）
	if outputDir != "" {
		if data, err := os.ReadFile(filepath.Join(outputDir, "servers.json")); err == nil {
			var servers []ServerInfo
			if uErr := json.Unmarshal(data, &servers); uErr != nil {
				return nil, fmt.Errorf("解析 servers.json 失败: %w", uErr)
			}
			m.servers = servers
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("读取 servers.json 失败: %w", err)
		}

		// 尝试加载 script_status.json
		if data, err := os.ReadFile(filepath.Join(outputDir, "script_status.json")); err == nil {
			var list []*ScriptStatus
			if uErr := json.Unmarshal(data, &list); uErr != nil {
				return nil, fmt.Errorf("解析 script_status.json 失败: %w", uErr)
			}
			for _, st := range list {
				if st == nil {
					continue
				}
				key := compositeKey(st.IP, st.ServiceType)
				m.statuses[key] = st
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("读取 script_status.json 失败: %w", err)
		}

		// 尝试加载 ssh_scripts.json
		if data, err := os.ReadFile(filepath.Join(outputDir, "ssh_scripts.json")); err == nil {
			var list []*SSHScriptStatus
			if uErr := json.Unmarshal(data, &list); uErr != nil {
				return nil, fmt.Errorf("解析 ssh_scripts.json 失败: %w", uErr)
			}
			for _, st := range list {
				if st == nil {
					continue
				}
				key := compositeKey(st.IP, st.ServiceType)
				m.sshScripts[key] = st
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("读取 ssh_scripts.json 失败: %w", err)
		}

		// 尝试加载 all_ips.json
		if data, err := os.ReadFile(filepath.Join(outputDir, "all_ips.json")); err == nil {
			var list []string
			if uErr := json.Unmarshal(data, &list); uErr != nil {
				return nil, fmt.Errorf("解析 all_ips.json 失败: %w", uErr)
			}
			for _, ip := range list {
				trimmed := strings.TrimSpace(ip)
				if trimmed == "" {
					continue
				}
				if _, exists := m.allIPSet[trimmed]; exists {
					continue
				}
				m.allIPSet[trimmed] = struct{}{}
				m.allIPs = append(m.allIPs, trimmed)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("读取 all_ips.json 失败: %w", err)
		}
	}

	return m, nil
}

// AddAllIPs 记录本次启动到的所有实例公网 IP 并写入 all_ips.json。
// 该文件用于保留“启动成功但 SSH 不可达”机器的 IP，便于排查网络与安全组问题。
func (m *OutputManager) AddAllIPs(ips []string) error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ip := range ips {
		trimmed := strings.TrimSpace(ip)
		if trimmed == "" {
			continue
		}
		if _, exists := m.allIPSet[trimmed]; exists {
			continue
		}
		m.allIPSet[trimmed] = struct{}{}
		m.allIPs = append(m.allIPs, trimmed)
	}

	return m.saveAllIPsLocked()
}

// UpdateSSHScriptStatus 更新某台服务器 SSH 就绪探测状态。
func (m *OutputManager) UpdateSSHScriptStatus(ip, serviceType, name, status string, attempts uint, reason string, updatedAt int64) error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := compositeKey(ip, serviceType)
	m.sshScripts[key] = &SSHScriptStatus{
		IP:          ip,
		ServiceType: serviceType,
		Name:        name,
		Status:      status,
		Attempts:    attempts,
		Reason:      reason,
		UpdatedAt:   updatedAt,
	}

	return m.saveSSHScriptsLocked()
}

func compositeKey(ip, serviceType string) string {
	return fmt.Sprintf("%s|%s", ip, serviceType)
}

// SnapshotServers 返回当前记录的服务器列表副本，用于基于已有 output 做二次操作（如 deploy-restore）。
func (m *OutputManager) SnapshotServers() []ServerInfo {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]ServerInfo, len(m.servers))
	copy(out, m.servers)
	return out
}

// AddServers 将一批服务器信息追加到列表并写入 servers.json。
func (m *OutputManager) AddServers(servers []ServerInfo) error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.servers = append(m.servers, servers...)

	return m.saveServersLocked()
}

// InitStatus 初始化某台服务器的脚本运行状态（通常在脚本后台启动成功后调用）。
// name:  逻辑名称（例如 tagPrefix-type-index）
// cmd:   实际执行的部署命令（不含 shutdown/nohup 等包装）
func (m *OutputManager) InitStatus(ip, serviceType, name, cmd string, pid int, logPath, localLog string, updatedAt int64) error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := compositeKey(ip, serviceType)
	m.statuses[key] = &ScriptStatus{
		IP:          ip,
		ServiceType: serviceType,
		Name:        name,
		Command:     cmd,
		PID:         pid,
		Status:      "running",
		LogPath:     logPath,
		LocalLog:    localLog,
		UpdatedAt:   updatedAt,
		LogSize:     0,
	}

	return m.saveStatusesLocked()
}

// UpsertPlannedStatus 预写入一条“待执行”的脚本状态。
// 用于在真正 SSH 启动远端命令之前落盘 command，避免后续恢复时遗漏未启动任务。
func (m *OutputManager) UpsertPlannedStatus(ip, serviceType, name, cmd, logPath, localLog string, updatedAt int64) error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := compositeKey(ip, serviceType)
	st, ok := m.statuses[key]
	if !ok || st == nil {
		st = &ScriptStatus{
			IP:          ip,
			ServiceType: serviceType,
		}
		m.statuses[key] = st
	}

	st.Name = name
	st.Command = cmd
	st.PID = 0
	st.Status = "pending"
	st.Reason = ""
	st.LogPath = logPath
	st.LocalLog = localLog
	st.UpdatedAt = updatedAt
	st.LogSize = 0

	return m.saveStatusesLocked()
}

// UpdateStatus 更新某台服务器脚本的状态信息。
func (m *OutputManager) UpdateStatus(ip, serviceType string, updateFn func(*ScriptStatus)) error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := compositeKey(ip, serviceType)
	st, ok := m.statuses[key]
	if !ok {
		st = &ScriptStatus{
			IP:          ip,
			ServiceType: serviceType,
		}
		m.statuses[key] = st
	}

	updateFn(st)

	return m.saveStatusesLocked()
}

// SnapshotStatuses 生成当前状态的浅拷贝，用于监控协程遍历。
func (m *OutputManager) SnapshotStatuses() []*ScriptStatus {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]*ScriptStatus, 0, len(m.statuses))
	for _, st := range m.statuses {
		if st == nil {
			continue
		}
		copied := *st
		result = append(result, &copied)
	}
	return result
}

func (m *OutputManager) saveServersLocked() error {
	if m.outputDir == "" {
		return nil
	}
	if err := os.MkdirAll(m.outputDir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(m.servers, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(m.outputDir, "servers.json")
	return os.WriteFile(path, data, 0o644)
}

func (m *OutputManager) saveStatusesLocked() error {
	if m.outputDir == "" {
		return nil
	}
	if err := os.MkdirAll(m.outputDir, 0o755); err != nil {
		return err
	}

	// 将 map 转为 slice 输出，保证输出结构稳定。
	list := make([]*ScriptStatus, 0, len(m.statuses))
	for _, st := range m.statuses {
		if st == nil {
			continue
		}
		list = append(list, st)
	}

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(m.outputDir, "script_status.json")
	return os.WriteFile(path, data, 0o644)
}

func (m *OutputManager) saveSSHScriptsLocked() error {
	if m.outputDir == "" {
		return nil
	}
	if err := os.MkdirAll(m.outputDir, 0o755); err != nil {
		return err
	}

	list := make([]*SSHScriptStatus, 0, len(m.sshScripts))
	for _, st := range m.sshScripts {
		if st == nil {
			continue
		}
		list = append(list, st)
	}

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(m.outputDir, "ssh_scripts.json")
	return os.WriteFile(path, data, 0o644)
}

func (m *OutputManager) saveAllIPsLocked() error {
	if m.outputDir == "" {
		return nil
	}
	if err := os.MkdirAll(m.outputDir, 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(m.allIPs, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(m.outputDir, "all_ips.json")
	return os.WriteFile(path, data, 0o644)
}

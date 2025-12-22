package deploy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ServerInfo 描述一台服务器在本次部署中的基础信息。
type ServerInfo struct {
	IP          string `json:"ip"`
	ServiceType string `json:"serviceType"`
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

// OutputManager 负责维护 servers.json 和 script_status.json 两个输出文件。
type OutputManager struct {
	outputDir string

	mu       sync.Mutex
	servers  []ServerInfo
	statuses map[string]*ScriptStatus
}

func NewOutputManager(outputDir string) *OutputManager {
	return &OutputManager{
		outputDir: outputDir,
		statuses:  make(map[string]*ScriptStatus),
	}
}

// LoadOutputManager 从指定目录下已有的 JSON 文件（servers.json / script_status.json）恢复 OutputManager。
// 主要用于进程重启后，基于已有状态重新进行日志与脚本状态同步。
func LoadOutputManager(outputDir string) (*OutputManager, error) {
	m := &OutputManager{
		outputDir: outputDir,
		statuses:  make(map[string]*ScriptStatus),
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
	}

	return m, nil
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
func (m *OutputManager) AddServers(ips []string, serviceType string) error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ip := range ips {
		m.servers = append(m.servers, ServerInfo{
			IP:          ip,
			ServiceType: serviceType,
		})
	}

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

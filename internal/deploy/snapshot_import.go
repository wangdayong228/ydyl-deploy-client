package deploy

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// RunOptions 为 deploy 运行时的可选行为；零值为默认（创建新 EC2）。
type RunOptions struct {
	// ServersCreateJSONPath 为 servers_create.json 格式快照的绝对路径（通常由 CLI 先复制到临时文件后再传入）。
	ServersCreateJSONPath string
}

// CopyServersCreateSnapshotToTemp 将任意路径下的 servers_create 快照复制到系统临时目录，返回临时文件绝对路径与 cleanup。
// 调用方应在 deploy 结束前执行 cleanup（例如 defer cleanup()），避免残留临时文件。
func CopyServersCreateSnapshotToTemp(src string) (dstAbs string, cleanup func(), err error) {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return "", nil, fmt.Errorf("解析 servers-create 路径失败: %w", err)
	}
	in, err := os.Open(srcAbs)
	if err != nil {
		return "", nil, fmt.Errorf("打开 servers-create 文件失败: %w", err)
	}
	defer in.Close()

	out, err := os.CreateTemp("", "ydyl-servers-create-*.json")
	if err != nil {
		return "", nil, fmt.Errorf("创建临时 servers-create 副本失败: %w", err)
	}
	dstPath := out.Name()

	cleanupFn := func() {
		_ = os.Remove(dstPath)
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		cleanupFn()
		return "", nil, fmt.Errorf("复制 servers-create 失败: %w", err)
	}
	if err := out.Close(); err != nil {
		cleanupFn()
		return "", nil, fmt.Errorf("关闭临时 servers-create 失败: %w", err)
	}

	absDst, err := filepath.Abs(dstPath)
	if err != nil {
		cleanupFn()
		return "", nil, fmt.Errorf("解析临时文件路径失败: %w", err)
	}
	return absDst, cleanupFn, nil
}

// LoadCreatedServersFromFile 从 JSON 文件加载创建阶段机器列表，并做与 OutputManager 一致的规范化与无效项过滤。
func LoadCreatedServersFromFile(path string) ([]CreatedServerInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	var raw []CreatedServerInfo
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("解析 JSON 失败: %w", err)
	}

	out := make([]CreatedServerInfo, 0, len(raw))
	for _, item := range raw {
		n := normalizeCreatedServerInfo(item)
		if n.IP == "" || n.ServiceType == "" {
			continue
		}
		out = append(out, n)
	}
	return out, nil
}

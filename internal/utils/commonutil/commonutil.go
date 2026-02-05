package commonutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvOverride 在 base 环境变量列表上覆盖指定 key=value，并保证结果中该 key 只出现一次。
func EnvOverride(base []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		if strings.HasPrefix(kv, prefix) {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, prefix+value)
	return out
}

// ResolveZkClaimDir 基于当前工作目录按固定相对位置拼接，找到 zk-claim-service 目录：
// - 在仓库根目录执行：./zk-claim-service
// - 在 ydyl-deploy-client 目录执行：../zk-claim-service
func ResolveZkClaimDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("获取当前目录失败: %w", err)
	}

	candidates := []string{
		filepath.Join(wd, "zk-claim-service"),
		filepath.Join(wd, "..", "zk-claim-service"),
	}
	for _, c := range candidates {
		if ok := hasMultijobScript(c); ok {
			return c, nil
		}
	}
	return "", fmt.Errorf("未找到 zk-claim-service/scripts/7s_multijob.js（请在仓库根目录或 ydyl-deploy-client 目录执行）")
}

func hasMultijobScript(zkClaimDir string) bool {
	p := filepath.Join(zkClaimDir, "scripts", "7s_multijob.js")
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

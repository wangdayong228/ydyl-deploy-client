package crosstxtps

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wangdayong228/ydyl-deploy-client/internal/infra/oscmdexec"
	"github.com/wangdayong228/ydyl-deploy-client/internal/utils/commonutil"
)

type Params struct {
	// ConfigPath 为 gen-cross-tx-config 的输出文件路径（jobs JSON array）。
	// 该路径会作为 argv[2] 传给 h_TPSjob.js。
	ConfigPath string
}

// TPS 负责执行 zk-claim-service 的 TPS 监控脚本（h_TPSjob.js）。
// 通过注入 Runner，便于在测试中 mock 命令执行。
type TPS struct {
	Runner oscmdexec.Runner
}

func NewTPS(runner oscmdexec.Runner) *TPS {
	return &TPS{Runner: runner}
}

func DefaultTPS() *TPS {
	return &TPS{Runner: oscmdexec.DefaultRunner}
}

func (t *TPS) Run(ctx context.Context, p Params) error {
	if strings.TrimSpace(p.ConfigPath) == "" {
		return fmt.Errorf("config 不能为空")
	}

	absConfig, err := filepath.Abs(p.ConfigPath)
	if err != nil {
		return fmt.Errorf("解析 config 绝对路径失败: %w", err)
	}
	if _, err := os.Stat(absConfig); err != nil {
		return fmt.Errorf("config 文件不存在或不可访问: %w", err)
	}

	zkClaimDir, err := commonutil.ResolveZkClaimDir()
	if err != nil {
		return err
	}

	spec := oscmdexec.Spec{
		Name: "node",
		Args: []string{filepath.Join("scripts", "h_TPSjob.js"), absConfig},
		Dir:  zkClaimDir,
		Env:  os.Environ(),
	}

	runner := t.Runner
	if runner == nil {
		runner = oscmdexec.DefaultRunner
	}
	return runner(ctx, spec)
}

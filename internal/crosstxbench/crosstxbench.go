package crosstxbench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wangdayong228/ydyl-deploy-client/internal/infra/oscmdexec"
	"github.com/wangdayong228/ydyl-deploy-client/internal/utils/commonutil"
)

type Params struct {
	// ConfigPath 为 gen-cross-tx-config 的输出文件路径（jobs JSON array）。
	ConfigPath string
	// Concurrency 透传到脚本环境变量 CONCURRENCY。
	Concurrency int
}

// Bench 负责执行 zk-claim-service 的跨链交易脚本（7s_multijob.js）。
// 通过注入 Runner，便于在测试中 mock 命令执行。
type Bench struct {
	Runner oscmdexec.Runner
}

func NewBench(runner oscmdexec.Runner) *Bench {
	return &Bench{Runner: runner}
}

func DefaultBench() *Bench {
	return &Bench{Runner: oscmdexec.DefaultRunner}
}

func (b *Bench) Run(ctx context.Context, p Params) error {
	if strings.TrimSpace(p.ConfigPath) == "" {
		return fmt.Errorf("config 不能为空")
	}
	if p.Concurrency <= 0 {
		return fmt.Errorf("concurrency 必须 > 0")
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

	scriptRel := filepath.Join("scripts", "7s_multijob.js")

	env := commonutil.EnvOverride(os.Environ(), "CONCURRENCY", strconv.Itoa(p.Concurrency))

	spec := oscmdexec.Spec{
		Name: "node",
		Args: []string{scriptRel, absConfig},
		Dir:  zkClaimDir,
		Env:  env,
	}

	runner := b.Runner
	if runner == nil {
		runner = oscmdexec.DefaultRunner
	}
	return runner(ctx, spec)
}

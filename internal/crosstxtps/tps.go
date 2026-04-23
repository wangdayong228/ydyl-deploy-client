package crosstxtps

import (
	"context"

	"github.com/wangdayong228/ydyl-deploy-client/internal/benchcompose"
	"github.com/wangdayong228/ydyl-deploy-client/internal/infra/oscmdexec"
)

type Params struct {
	// ConfigPath 为 gen-cross-tx-config 的输出文件路径（jobs JSON array），
	// 传入时其 JSON 内容必须与 ydyl-deploy-client/output/jobs/all.json 等价。
	ConfigPath string
}

// TPS 负责通过 ydyl-bench-docker 启动 tps compose service。
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
	paths, err := benchcompose.ValidateConfigMatchesAllJobs(p.ConfigPath)
	if err != nil {
		return err
	}
	spec := benchcompose.DockerComposeUpSpec(paths.ComposeDir, []string{"tps"})

	runner := t.Runner
	if runner == nil {
		runner = oscmdexec.DefaultRunner
	}
	return runner(ctx, spec)
}

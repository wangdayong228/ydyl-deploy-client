package crosstxbench

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

// Bench 负责通过 ydyl-bench-docker 启动 8 个 multijob compose service。
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
	paths, err := benchcompose.ValidateConfigMatchesAllJobs(p.ConfigPath)
	if err != nil {
		return err
	}
	spec := benchcompose.DockerComposeUpSpec(paths.ComposeDir, benchcompose.MultijobServices())

	runner := b.Runner
	if runner == nil {
		runner = oscmdexec.DefaultRunner
	}
	return runner(ctx, spec)
}

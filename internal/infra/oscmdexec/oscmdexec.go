package oscmdexec

import (
	"context"
	"os"
	"os/exec"
)

// Spec 描述一次基于 os/exec 的进程执行规范。
type Spec struct {
	// Name 为可执行文件名或路径（例如 "node" / "ssh"）。
	Name string
	// Args 为参数列表（不包含 Name）。
	Args []string
	// Dir 为工作目录（为空则继承当前进程）。
	Dir string
	// Env 为环境变量列表（为空则继承当前进程）。
	Env []string
}

// Runner 执行一个 Spec 对应的进程。
// 设计为可注入，便于测试中 mock。
type Runner func(ctx context.Context, spec Spec) error

// DefaultRunner 使用 os/exec 直接启动子进程，并将 stdout/stderr 直连到当前进程。
func DefaultRunner(ctx context.Context, spec Spec) error {
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

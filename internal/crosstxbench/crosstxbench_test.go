package crosstxbench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wangdayong228/ydyl-deploy-client/internal/infra/oscmdexec"
)

func TestBench_Run_Success(t *testing.T) {
	// 构造一个临时 repoRoot 结构：<root>/zk-claim-service/scripts/7s_multijob.js
	root := t.TempDir()
	scriptPath := filepath.Join(root, "zk-claim-service", "scripts", "7s_multijob.js")
	mustNoErr(t, os.MkdirAll(filepath.Dir(scriptPath), 0o755))
	mustNoErr(t, os.WriteFile(scriptPath, []byte("#!/usr/bin/env node\n"), 0o644))

	// 准备 config 文件
	cfg := filepath.Join(root, "jobs.json")
	mustNoErr(t, os.WriteFile(cfg, []byte("[]"), 0o644))

	// 切换到 repoRoot，让 findRepoRoot() 走工作目录分支
	wd, err := os.Getwd()
	mustNoErr(t, err)
	t.Cleanup(func() { _ = os.Chdir(wd) })
	mustNoErr(t, os.Chdir(root))

	var got oscmdexec.Spec
	called := false
	runner := func(ctx context.Context, spec oscmdexec.Spec) error {
		called = true
		got = spec
		return nil
	}

	b := NewBench(runner)
	err = b.Run(context.Background(), Params{
		ConfigPath:  cfg,
		Concurrency: 50,
	})
	mustNoErr(t, err)
	if !called {
		t.Fatalf("runner 未被调用")
	}

	if got.Name != "node" {
		t.Fatalf("name 不符合，got=%q", got.Name)
	}
	if len(got.Args) != 2 {
		t.Fatalf("args 数量不符合，got=%d args=%v", len(got.Args), got.Args)
	}
	if got.Args[0] != filepath.Join("scripts", "7s_multijob.js") {
		t.Fatalf("script 参数不符合，got=%q", got.Args[0])
	}

	absCfg, err := filepath.Abs(cfg)
	mustNoErr(t, err)
	if got.Args[1] != absCfg {
		t.Fatalf("config 参数不符合，got=%q want=%q", got.Args[1], absCfg)
	}

	wantDir := filepath.Join(root, "zk-claim-service")
	// macOS 下临时目录可能出现 /var 与 /private/var 的路径别名，统一用 EvalSymlinks 归一化后比较
	gotDirNorm := mustEvalSymlinks(t, got.Dir)
	wantDirNorm := mustEvalSymlinks(t, wantDir)
	if gotDirNorm != wantDirNorm {
		t.Fatalf("cmd.Dir 不符合，got=%q(%q) want=%q(%q)", got.Dir, gotDirNorm, wantDir, wantDirNorm)
	}

	// Env 里应包含 CONCURRENCY=50（并覆盖原有同名）
	if !hasEnv(got.Env, "CONCURRENCY=50") {
		t.Fatalf("env 未包含 CONCURRENCY=50")
	}
}

func hasEnv(env []string, kv string) bool {
	for _, e := range env {
		if strings.TrimSpace(e) == kv {
			return true
		}
	}
	return false
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustEvalSymlinks(t *testing.T, p string) string {
	t.Helper()
	out, err := filepath.EvalSymlinks(p)
	if err != nil {
		// 某些环境下 EvalSymlinks 可能失败，兜底返回 clean 后的原始值
		return filepath.Clean(p)
	}
	return filepath.Clean(out)
}

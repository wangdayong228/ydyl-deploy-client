package crosstxbench

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wangdayong228/ydyl-deploy-client/internal/infra/oscmdexec"
)

func TestBench_Run_Success_StartsMultijobComposeServices(t *testing.T) {
	root := setupBenchSuite(t, `[
  {"name":"job-1","params":{"a":1,"b":2}},
  {"name":"job-2","params":{"a":3,"b":4}}
]`)
	cfg := filepath.Join(root, "jobs.input.json")
	mustNoErr(t, os.WriteFile(cfg, []byte(`[{"params":{"b":2,"a":1},"name":"job-1"},{"params":{"b":4,"a":3},"name":"job-2"}]`), 0o644))
	chdir(t, root)

	var got oscmdexec.Spec
	called := false
	runner := func(ctx context.Context, spec oscmdexec.Spec) error {
		called = true
		got = spec
		return nil
	}

	err := NewBench(runner).Run(context.Background(), Params{ConfigPath: cfg})
	mustNoErr(t, err)
	if !called {
		t.Fatalf("runner 未被调用")
	}

	if got.Name != "docker" {
		t.Fatalf("name 不符合，got=%q", got.Name)
	}
	wantArgs := []string{
		"compose", "up", "--build",
		"multijob-1", "multijob-2", "multijob-3", "multijob-4",
		"multijob-5", "multijob-6", "multijob-7", "multijob-8",
	}
	assertStringSliceEqual(t, got.Args, wantArgs)

	wantDir := filepath.Join(root, "ydyl-bench-docker")
	gotDirNorm := mustEvalSymlinks(t, got.Dir)
	wantDirNorm := mustEvalSymlinks(t, wantDir)
	if gotDirNorm != wantDirNorm {
		t.Fatalf("cmd.Dir 不符合，got=%q(%q) want=%q(%q)", got.Dir, gotDirNorm, wantDir, wantDirNorm)
	}
}

func TestBench_Run_FromDeployClientDir(t *testing.T) {
	root := setupBenchSuite(t, `[{"name":"job-1"}]`)
	cfg := filepath.Join(root, "ydyl-deploy-client", "jobs.input.json")
	mustNoErr(t, os.WriteFile(cfg, []byte(`[{"name":"job-1"}]`), 0o644))
	chdir(t, filepath.Join(root, "ydyl-deploy-client"))

	called := false
	runner := func(ctx context.Context, spec oscmdexec.Spec) error {
		called = true
		if mustEvalSymlinks(t, spec.Dir) != mustEvalSymlinks(t, filepath.Join(root, "ydyl-bench-docker")) {
			t.Fatalf("cmd.Dir 不符合，got=%q", spec.Dir)
		}
		return nil
	}

	err := NewBench(runner).Run(context.Background(), Params{ConfigPath: cfg})
	mustNoErr(t, err)
	if !called {
		t.Fatalf("runner 未被调用")
	}
}

func TestBench_Run_NoConfig_SkipsAllJobsValidation(t *testing.T) {
	root := setupBenchSuite(t, "")
	allPath := filepath.Join(root, "ydyl-deploy-client", "output", "jobs", "all.json")
	mustNoErr(t, os.Remove(allPath))
	chdir(t, root)

	var got oscmdexec.Spec
	called := false
	runner := func(ctx context.Context, spec oscmdexec.Spec) error {
		called = true
		got = spec
		return nil
	}

	err := NewBench(runner).Run(context.Background(), Params{})
	mustNoErr(t, err)
	if !called {
		t.Fatalf("runner 未被调用")
	}
	assertStringSliceEqual(t, got.Args, []string{
		"compose", "up", "--build",
		"multijob-1", "multijob-2", "multijob-3", "multijob-4",
		"multijob-5", "multijob-6", "multijob-7", "multijob-8",
	})
}

func TestBench_Run_ConfigMismatch_ReturnsErrorAndSkipsRunner(t *testing.T) {
	root := setupBenchSuite(t, `[{"name":"job-1"}]`)
	cfg := filepath.Join(root, "jobs.input.json")
	mustNoErr(t, os.WriteFile(cfg, []byte(`[{"name":"job-2"}]`), 0o644))
	chdir(t, root)

	called := false
	err := NewBench(func(ctx context.Context, spec oscmdexec.Spec) error {
		called = true
		return nil
	}).Run(context.Background(), Params{ConfigPath: cfg})

	if err == nil {
		t.Fatalf("expected error")
	}
	if called {
		t.Fatalf("runner 不应被调用")
	}
}

func TestBench_Run_AllJobsMissing_ReturnsErrorAndSkipsRunner(t *testing.T) {
	root := setupBenchSuite(t, "")
	allPath := filepath.Join(root, "ydyl-deploy-client", "output", "jobs", "all.json")
	mustNoErr(t, os.Remove(allPath))
	cfg := filepath.Join(root, "jobs.input.json")
	mustNoErr(t, os.WriteFile(cfg, []byte(`[]`), 0o644))
	chdir(t, root)

	called := false
	err := NewBench(func(ctx context.Context, spec oscmdexec.Spec) error {
		called = true
		return nil
	}).Run(context.Background(), Params{ConfigPath: cfg})

	if err == nil {
		t.Fatalf("expected error")
	}
	if called {
		t.Fatalf("runner 不应被调用")
	}
}

func setupBenchSuite(t *testing.T, allJSON string) string {
	t.Helper()

	root := t.TempDir()
	allPath := filepath.Join(root, "ydyl-deploy-client", "output", "jobs", "all.json")
	mustNoErr(t, os.MkdirAll(filepath.Dir(allPath), 0o755))
	if allJSON == "" {
		allJSON = `[]`
	}
	mustNoErr(t, os.WriteFile(allPath, []byte(allJSON), 0o644))
	mustNoErr(t, os.MkdirAll(filepath.Join(root, "ydyl-bench-docker"), 0o755))
	return root
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	mustNoErr(t, err)
	t.Cleanup(func() { _ = os.Chdir(wd) })
	mustNoErr(t, os.Chdir(dir))
}

func assertStringSliceEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("args 数量不符合，got=%d args=%v want=%d args=%v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args[%d] 不符合，got=%q want=%q args=%v", i, got[i], want[i], got)
		}
	}
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
		return filepath.Clean(p)
	}
	return filepath.Clean(out)
}

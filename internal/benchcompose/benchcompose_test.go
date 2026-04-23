package benchcompose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJSONFilesEqual_IgnoresObjectFieldOrderAndWhitespace(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.json")
	right := filepath.Join(dir, "right.json")
	mustNoErr(t, os.WriteFile(left, []byte(`[
  {"name":"job-1","params":{"a":1,"b":2}}
]`), 0o644))
	mustNoErr(t, os.WriteFile(right, []byte(`[{"params":{"b":2,"a":1},"name":"job-1"}]`), 0o644))

	equal, err := JSONFilesEqual(left, right)
	mustNoErr(t, err)
	if !equal {
		t.Fatalf("expected JSON files to be equal")
	}
}

func TestJSONFilesEqual_ArrayOrderMustMatch(t *testing.T) {
	dir := t.TempDir()
	left := filepath.Join(dir, "left.json")
	right := filepath.Join(dir, "right.json")
	mustNoErr(t, os.WriteFile(left, []byte(`[{"name":"job-1"},{"name":"job-2"}]`), 0o644))
	mustNoErr(t, os.WriteFile(right, []byte(`[{"name":"job-2"},{"name":"job-1"}]`), 0o644))

	equal, err := JSONFilesEqual(left, right)
	mustNoErr(t, err)
	if equal {
		t.Fatalf("expected JSON files to differ when array order differs")
	}
}

func TestResolvePaths_FromSuiteRootAndDeployClientDir(t *testing.T) {
	root := t.TempDir()
	mustNoErr(t, os.MkdirAll(filepath.Join(root, deployClientDir), 0o755))
	mustNoErr(t, os.MkdirAll(filepath.Join(root, benchDockerDir), 0o755))

	chdir(t, root)
	paths, err := ResolvePaths()
	mustNoErr(t, err)
	if normalizePath(t, paths.DeployClientDir) != normalizePath(t, filepath.Join(root, deployClientDir)) {
		t.Fatalf("DeployClientDir 不符合，got=%q", paths.DeployClientDir)
	}
	if normalizePath(t, paths.ComposeDir) != normalizePath(t, filepath.Join(root, benchDockerDir)) {
		t.Fatalf("ComposeDir 不符合，got=%q", paths.ComposeDir)
	}

	chdir(t, filepath.Join(root, deployClientDir))
	paths, err = ResolvePaths()
	mustNoErr(t, err)
	if normalizePath(t, paths.DeployClientDir) != normalizePath(t, filepath.Join(root, deployClientDir)) {
		t.Fatalf("DeployClientDir 不符合，got=%q", paths.DeployClientDir)
	}
	if normalizePath(t, paths.ComposeDir) != normalizePath(t, filepath.Join(root, benchDockerDir)) {
		t.Fatalf("ComposeDir 不符合，got=%q", paths.ComposeDir)
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	mustNoErr(t, err)
	t.Cleanup(func() { _ = os.Chdir(wd) })
	mustNoErr(t, os.Chdir(dir))
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func normalizePath(t *testing.T, p string) string {
	t.Helper()
	out, err := filepath.EvalSymlinks(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return filepath.Clean(out)
}

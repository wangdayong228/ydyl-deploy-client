package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyGeneratedJobsConfig_CopiesAllJSONToCurrentWorkingDir(t *testing.T) {
	sourceDir := t.TempDir()
	sourcePath := filepath.Join(sourceDir, "all.json")
	content := []byte("{\n  \"b\": 2,\n  \"a\": [1, 2, 3]\n}\n")
	mustNoErr(t, os.WriteFile(sourcePath, content, 0o644))

	destDir := t.TempDir()
	chdir(t, destDir)

	gotPath, err := copyGeneratedJobsConfig(sourcePath)
	mustNoErr(t, err)

	wantPath := filepath.Join(destDir, generatedJobsConfigFilename)
	if normalizePath(t, gotPath) != normalizePath(t, wantPath) {
		t.Fatalf("copied path 不符合，got=%q want=%q", gotPath, wantPath)
	}
	got, err := os.ReadFile(wantPath)
	mustNoErr(t, err)
	if string(got) != string(content) {
		t.Fatalf("copied content 不一致，got=%q want=%q", string(got), string(content))
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

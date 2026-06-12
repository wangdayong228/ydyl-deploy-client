package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWithClientCommandTee_WritesStdoutAndStderr(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "client", "test.log")

	err := withClientCommandTee(logPath, func() error {
		fmt.Fprintln(os.Stdout, "hello stdout")
		fmt.Fprintln(os.Stderr, "hello stderr")
		return nil
	})
	if err != nil {
		t.Fatalf("withClientCommandTee: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "hello stdout") {
		t.Fatalf("log missing stdout, got: %q", content)
	}
	if !strings.Contains(content, "hello stderr") {
		t.Fatalf("log missing stderr, got: %q", content)
	}
}

func TestWithClientCommandTee_PropagatesRunError(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "client", "test.log")
	wantErr := fmt.Errorf("boom")

	err := withClientCommandTee(logPath, func() error {
		fmt.Fprintln(os.Stderr, "before error")
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !bytes.Contains(data, []byte("before error")) {
		t.Fatalf("log missing stderr before error, got: %q", string(data))
	}
}

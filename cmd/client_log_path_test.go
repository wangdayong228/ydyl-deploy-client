package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestClientLogPath_DefaultsEmptyLogDir(t *testing.T) {
	got := clientLogPath("", "bench-cross-tx")
	if !strings.HasPrefix(got, filepath.Join("logs", "client", "bench-cross-tx-")) {
		t.Fatalf("unexpected path: %q", got)
	}
	if !strings.HasSuffix(got, ".log") {
		t.Fatalf("expected .log suffix, got: %q", got)
	}
}

func TestClientLogPath_UsesProvidedLogDir(t *testing.T) {
	got := clientLogPath("custom-logs", "deploy")
	if !strings.HasPrefix(got, filepath.Join("custom-logs", "client", "deploy-")) {
		t.Fatalf("unexpected path: %q", got)
	}
}

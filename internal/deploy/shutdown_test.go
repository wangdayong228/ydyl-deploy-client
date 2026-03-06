package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectUniqueServerIPs(t *testing.T) {
	t.Parallel()

	servers := []ServerInfo{
		{IP: "1.1.1.1"},
		{IP: " 1.1.1.1 "},
		{IP: "2.2.2.2"},
		{IP: ""},
		{IP: "   "},
		{IP: "3.3.3.3"},
	}

	got := collectUniqueServerIPs(servers)
	want := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}

	if len(got) != len(want) {
		t.Fatalf("unexpected ip count: got=%d want=%d, got=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected ip at index %d: got=%s want=%s", i, got[i], want[i])
		}
	}
}

func TestShutdown_NoServers(t *testing.T) {
	t.Parallel()

	serversPath := filepath.Join(t.TempDir(), "servers.json")
	cfg := CommonConfig{
		SSHUser: "ubuntu",
		KeyName: "dummy",
	}

	err := Shutdown(context.Background(), cfg, serversPath)
	if err == nil {
		t.Fatalf("expected error when servers.json is missing or empty")
	}
	if !strings.Contains(err.Error(), "读取 servers.json 失败") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestShutdown_AggregatesErrors(t *testing.T) {
	t.Parallel()

	serversPath := filepath.Join(t.TempDir(), "servers.json")
	servers := []ServerInfo{
		{IP: "10.0.0.1", ServiceType: "xjst", Name: "node-1"},
		{IP: "10.0.0.2", ServiceType: "xjst", Name: "node-2"},
	}
	data, err := json.Marshal(servers)
	if err != nil {
		t.Fatalf("marshal servers failed: %v", err)
	}
	if err := os.WriteFile(serversPath, data, 0o644); err != nil {
		t.Fatalf("write servers.json failed: %v", err)
	}

	original := runShutdownSSHCommandFunc
	t.Cleanup(func() {
		runShutdownSSHCommandFunc = original
	})
	runShutdownSSHCommandFunc = func(ctx context.Context, sshUser, sshKeyPath, ip string) (string, error) {
		return "permission denied", errors.New("exit status 1")
	}

	cfg := CommonConfig{
		SSHUser: "ubuntu",
		KeyName: "dummy",
	}

	err = Shutdown(context.Background(), cfg, serversPath)
	if err == nil {
		t.Fatalf("expected aggregated error")
	}

	if !strings.Contains(err.Error(), "共有 2 台机器部署失败") {
		t.Fatalf("expected multi-error summary, got: %v", err)
	}
	if !strings.Contains(err.Error(), "10.0.0.1") || !strings.Contains(err.Error(), "10.0.0.2") {
		t.Fatalf("expected both ips in error message, got: %v", err)
	}
}

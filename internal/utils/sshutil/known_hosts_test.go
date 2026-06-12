package sshutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilterKeyscanLines(t *testing.T) {
	t.Parallel()

	out := "# 54.202.92.168:22 SSH-2.0-OpenSSH\n\n54.202.92.168 ssh-ed25519 AAAAB3\n"
	got := filterKeyscanLines(out)
	if len(got) != 1 || got[0] != "54.202.92.168 ssh-ed25519 AAAAB3" {
		t.Fatalf("filterKeyscanLines=%v, want one host key line", got)
	}
}

func TestHostInKnownHosts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(path, []byte("54.202.92.168 ssh-ed25519 AAAAB3\n"), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}

	exists, err := hostInKnownHosts(path, "54.202.92.168")
	if err != nil {
		t.Fatalf("hostInKnownHosts failed: %v", err)
	}
	if !exists {
		t.Fatalf("hostInKnownHosts should find existing host")
	}

	exists, err = hostInKnownHosts(path, "44.244.76.243")
	if err != nil {
		t.Fatalf("hostInKnownHosts failed: %v", err)
	}
	if exists {
		t.Fatalf("hostInKnownHosts should not find missing host")
	}

	exists, err = hostInKnownHosts(filepath.Join(dir, "missing"), "54.202.92.168")
	if err != nil {
		t.Fatalf("hostInKnownHosts missing file failed: %v", err)
	}
	if exists {
		t.Fatalf("missing known_hosts should return false")
	}
}

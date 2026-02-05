package sshutil

import (
	"context"
	"os"
	"testing"
)

func TestWaitSSH(t *testing.T) {
	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	if keyPath == "" {
		t.Skip("跳过需要真实 SSH 环境的测试：请设置 TEST_SSH_KEY_PATH（例如 ~/.ssh/dayong-op-stack.pem）")
	}

	if err := WaitSSH(context.Background(), "52.25.28.0", "ubuntu", keyPath); err != nil {
		t.Fatalf("WaitSSH error: %v", err)
	}
}


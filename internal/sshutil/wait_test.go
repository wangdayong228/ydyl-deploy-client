package sshutil

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWaitSSH(t *testing.T) {
	keyPath := os.Getenv("TEST_SSH_KEY_PATH")
	if keyPath == "" {
		t.Skip("跳过需要真实 SSH 环境的测试：请设置 TEST_SSH_KEY_PATH（例如 ~/.ssh/dayong-op-stack.pem）")
	}

	err := WaitSSH(context.Background(), "52.25.28.0", "ubuntu", keyPath)
	assert.NoError(t, err)
}

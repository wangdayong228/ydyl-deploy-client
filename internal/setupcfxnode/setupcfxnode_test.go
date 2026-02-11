package setupcfxnode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wangdayong228/ydyl-deploy-client/internal/infra/oscmdexec"
)

func TestSetup_Run_Success(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.deploy.yaml")
	cfg := `
region: us-west-2
l1RpcUrl: https://example-rpc.local
l1VaultMnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
services: []
`
	mustNoErr(t, os.WriteFile(cfgPath, []byte(cfg), 0o644))

	var got oscmdexec.Spec
	called := false
	runner := func(ctx context.Context, spec oscmdexec.Spec) error {
		called = true
		got = spec
		return nil
	}

	s := NewSetup(runner)
	err := s.Run(context.Background(), Params{ConfigPath: cfgPath})
	mustNoErr(t, err)

	if !called {
		t.Fatalf("runner 未被调用")
	}
	if got.Name != "bash" {
		t.Fatalf("Name 不符合，got=%q", got.Name)
	}
	wantScriptPath, err := resolveScriptPath()
	mustNoErr(t, err)
	if len(got.Args) != 1 || got.Args[0] != wantScriptPath {
		t.Fatalf("Args 不符合，got=%v", got.Args)
	}

	l1RPCURL := envValue(got.Env, "L1_RPC_URL")
	if l1RPCURL != "https://example-rpc.local" {
		t.Fatalf("L1_RPC_URL 不符合，got=%q", l1RPCURL)
	}

	l1VaultPriv := envValue(got.Env, "L1_VAULT_PRIVATE_KEY")
	if !strings.HasPrefix(l1VaultPriv, "0x") {
		t.Fatalf("L1_VAULT_PRIVATE_KEY 必须为 0x 前缀，got=%q", l1VaultPriv)
	}
	if len(l1VaultPriv) != 66 {
		t.Fatalf("L1_VAULT_PRIVATE_KEY 长度不符合，got=%d", len(l1VaultPriv))
	}
}

func TestSetup_Run_EmptyL1RPCURL(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.deploy.yaml")
	cfg := `
region: us-west-2
l1RpcUrl: ""
l1VaultMnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
services: []
`
	mustNoErr(t, os.WriteFile(cfgPath, []byte(cfg), 0o644))

	called := false
	runner := func(ctx context.Context, spec oscmdexec.Spec) error {
		called = true
		return nil
	}

	s := NewSetup(runner)
	err := s.Run(context.Background(), Params{ConfigPath: cfgPath})
	if err == nil {
		t.Fatalf("期望报错，但返回 nil")
	}
	if !strings.Contains(err.Error(), "l1RpcUrl") {
		t.Fatalf("错误信息不符合，got=%v", err)
	}
	if called {
		t.Fatalf("配置校验失败时不应执行 runner")
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix)
		}
	}
	return ""
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

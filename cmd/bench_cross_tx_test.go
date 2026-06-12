package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBenchCrossTxCommand_Flags(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"bench-cross-tx"})
	if err != nil {
		t.Fatalf("find bench-cross-tx command: %v", err)
	}
	if cmd == nil {
		t.Fatalf("bench-cross-tx command not found")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Fatalf("config flag not found")
	}
	if cmd.Flags().Lookup("deploy-config") == nil {
		t.Fatalf("deploy-config flag not found")
	}
	if cmd.Flags().Lookup("log-dir") != nil {
		t.Fatalf("log-dir flag should be removed")
	}
	if err := cmd.ValidateRequiredFlags(); err != nil {
		t.Fatalf("config flag should be optional: %v", err)
	}
	if cmd.Flags().Lookup("concurrency") != nil {
		t.Fatalf("concurrency flag should be removed")
	}
}

func writeMinimalDeployConfig(t *testing.T, dir, logDir string) string {
	t.Helper()
	cfgPath := filepath.Join(dir, "config.deploy.yaml")
	cfg := `region: us-west-2
securityGroupId: sg-test
diskSizeGiB: 100
runDuration: 1h
sshUser: ubuntu
sshKeyDir: ~/.ssh
sshMaxConcurrency: 4
sshReadyRetryCount: 3
sshReadyRetryInterval: 3s
keyName: test-key
logDir: ` + logDir + `
outputDir: ./output
benchClientIP: ""
l1ChainId: "11155111"
l1RpcUrl: https://example.org/rpc
l1RpcUrlWs: wss://example.org/ws
l1VaultMnemonic: "test test test test test test test test test test test junk"
l1BridgeHubContract: "0x00000000000000000000000000000000000000ff"
l1RegisterBridgePrivateKey: "0x1111111111111111111111111111111111111111111111111111111111111111"
dryRun: true
forceDeployL2Chain: false
enableGenAccounts: false
services: []
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

func TestResolveBenchCrossTxLogDir_FromDeployConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeMinimalDeployConfig(t, dir, "custom-from-yaml")
	benchCrossTxDeployConfigPath = cfgPath

	got := resolveBenchCrossTxLogDir()
	if got != "custom-from-yaml" {
		t.Fatalf("logDir from yaml = %q, want custom-from-yaml", got)
	}
}

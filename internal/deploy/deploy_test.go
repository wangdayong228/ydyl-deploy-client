package deploy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openweb3/go-sdk-common/privatekeyhelper"
	"github.com/wangdayong228/ydyl-deploy-client/internal/constants/enums"
	"github.com/wangdayong228/ydyl-deploy-client/internal/utils/cryptoutil"
)

func TestResolveL1VaultPrivateKey_MatchesPrivateKeyHelper(t *testing.T) {
	t.Parallel()

	d := &Deployer{l1VaultDeriveRand: 20260302}
	mnemonic := "test test test test test test test test test test test junk"
	serviceType := enums.ServiceTypeOP
	index := 10001

	got, err := d.resolveL1VaultPrivateKey(mnemonic, serviceType, index)
	if err != nil {
		t.Fatalf("resolveL1VaultPrivateKey returned error: %v", err)
	}
	if got == nil {
		t.Fatalf("resolveL1VaultPrivateKey returned nil key")
	}

	want, err := privatekeyhelper.NewFromMnemonic(mnemonic, index, &privatekeyhelper.MnemonicOption{
		BaseDerivePath: fmt.Sprintf("m/44'/60'/%d/%d", d.l1VaultDeriveRand, int(serviceType)),
	})
	if err != nil {
		t.Fatalf("privatekeyhelper.NewFromMnemonic returned error: %v", err)
	}

	gotHex := cryptoutil.EcdsaPrivToWeb3Hex(got)
	wantHex := cryptoutil.EcdsaPrivToWeb3Hex(want)
	if gotHex != wantHex {
		t.Fatalf("derived key mismatch: got=%s want=%s", gotHex, wantHex)
	}
}

func TestResolveL1VaultPrivateKey_SameDeployerStable(t *testing.T) {
	t.Parallel()

	d := &Deployer{l1VaultDeriveRand: 88}
	mnemonic := "test test test test test test test test test test test junk"
	serviceType := enums.ServiceTypeCDK
	index := 10002

	first, err := d.resolveL1VaultPrivateKey(mnemonic, serviceType, index)
	if err != nil {
		t.Fatalf("first resolveL1VaultPrivateKey returned error: %v", err)
	}
	second, err := d.resolveL1VaultPrivateKey(mnemonic, serviceType, index)
	if err != nil {
		t.Fatalf("second resolveL1VaultPrivateKey returned error: %v", err)
	}

	if cryptoutil.EcdsaPrivToWeb3Hex(first) != cryptoutil.EcdsaPrivToWeb3Hex(second) {
		t.Fatalf("same deployer should derive same key for same input")
	}
}

func TestResolveL1VaultPrivateKey_DifferentDeployerDifferent(t *testing.T) {
	t.Parallel()

	mnemonic := "test test test test test test test test test test test junk"
	serviceType := enums.ServiceTypeOP
	index := 10001

	d1 := &Deployer{l1VaultDeriveRand: 1}
	d2 := &Deployer{l1VaultDeriveRand: 2}

	first, err := d1.resolveL1VaultPrivateKey(mnemonic, serviceType, index)
	if err != nil {
		t.Fatalf("d1 resolveL1VaultPrivateKey returned error: %v", err)
	}
	second, err := d2.resolveL1VaultPrivateKey(mnemonic, serviceType, index)
	if err != nil {
		t.Fatalf("d2 resolveL1VaultPrivateKey returned error: %v", err)
	}

	if cryptoutil.EcdsaPrivToWeb3Hex(first) == cryptoutil.EcdsaPrivToWeb3Hex(second) {
		t.Fatalf("different deployers with different random segment should derive different keys")
	}
}

func TestResolveXjstGroupIps(t *testing.T) {
	t.Parallel()

	d := &Deployer{}
	globalIps := []string{"192.168.1.1", "192.168.1.2", "192.168.1.3", "192.168.1.4"}
	groupId := 1
	got, err := d.resolveXjstGroupIps(globalIps, groupId)
	if err != nil {
		t.Fatalf("resolveXjstGroupIps returned error: %v", err)
	}
	want := "[192.168.1.1,192.168.1.2,192.168.1.3,192.168.1.4]"
	if got != want {
		t.Fatalf("resolveXjstGroupIps returned wrong result: got=%s want=%s", got, want)
	}
}

func TestResolveXjstGroupIps_OutOfRange(t *testing.T) {
	t.Parallel()

	d := &Deployer{}
	globalIps := []string{"192.168.1.1", "192.168.1.2", "192.168.1.3", "192.168.1.4"}

	_, err := d.resolveXjstGroupIps(globalIps, 2)
	if err == nil {
		t.Fatalf("resolveXjstGroupIps expected error for out-of-range groupId")
	}
}

func TestRotateOutputAndLogs_ShareSameTimestamp(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	outputDir := filepath.Join(base, "output")
	logDir := filepath.Join(base, "logs")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatalf("create output dir failed: %v", err)
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatalf("create log dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "servers.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatalf("write servers.json failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "a.log"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write log file failed: %v", err)
	}

	statusPath := filepath.Join(outputDir, "script_status.json")
	if err := os.WriteFile(statusPath, []byte(`[]`), 0o644); err != nil {
		t.Fatalf("write script_status.json failed: %v", err)
	}

	fixed := time.Date(2026, 3, 4, 12, 34, 56, 0, time.UTC)
	if err := os.Chtimes(statusPath, fixed, fixed); err != nil {
		t.Fatalf("chtimes script_status.json failed: %v", err)
	}

	ts, err := resolveDeployArchiveTimestamp(outputDir, logDir)
	if err != nil {
		t.Fatalf("resolveDeployArchiveTimestamp failed: %v", err)
	}
	if ts != "20260304-123456" {
		t.Fatalf("unexpected ts: got=%s", ts)
	}

	rotatedOutput, err := rotateExistingDirWithTimestamp(outputDir, ts)
	if err != nil {
		t.Fatalf("rotate output failed: %v", err)
	}
	if !rotatedOutput {
		t.Fatalf("output should be rotated")
	}

	rotatedLogs, err := rotateExistingDirWithTimestamp(logDir, ts)
	if err != nil {
		t.Fatalf("rotate logs failed: %v", err)
	}
	if !rotatedLogs {
		t.Fatalf("logs should be rotated")
	}

	outputArchive := filepath.Join(base, "output-"+ts)
	logArchive := filepath.Join(base, "logs-"+ts)
	if _, err := os.Stat(outputArchive); err != nil {
		t.Fatalf("output archive missing: %v", err)
	}
	if _, err := os.Stat(logArchive); err != nil {
		t.Fatalf("log archive missing: %v", err)
	}

	outputSuffix := strings.TrimPrefix(filepath.Base(outputArchive), "output-")
	logSuffix := strings.TrimPrefix(filepath.Base(logArchive), "logs-")
	if outputSuffix != logSuffix {
		t.Fatalf("archive timestamp mismatch: output=%s logs=%s", outputSuffix, logSuffix)
	}
}

func TestRotateExistingDirWithTimestamp_SkipsEmptyAndMissing(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	emptyDir := filepath.Join(base, "logs")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatalf("create empty dir failed: %v", err)
	}

	rotated, err := rotateExistingDirWithTimestamp(emptyDir, "20260304-010203")
	if err != nil {
		t.Fatalf("rotate empty dir failed: %v", err)
	}
	if rotated {
		t.Fatalf("empty dir should not rotate")
	}

	missing := filepath.Join(base, "output")
	rotated, err = rotateExistingDirWithTimestamp(missing, "20260304-010203")
	if err != nil {
		t.Fatalf("rotate missing dir failed: %v", err)
	}
	if rotated {
		t.Fatalf("missing dir should not rotate")
	}
}

func TestBuildLocalLogPath_NameFirstThenIP(t *testing.T) {
	t.Parallel()

	logDir := "/tmp/logs"
	got := buildLocalLogPath(logDir, "1.2.3.4", "ydyl-xjst-2-1")
	want := filepath.Join(logDir, "ydyl-xjst-2-1-1.2.3.4.log")
	if got != want {
		t.Fatalf("unexpected local log path: got=%s want=%s", got, want)
	}
}

func TestRunWithBatchLimit_RespectsLimit(t *testing.T) {
	const (
		total      = 17
		batchLimit = 4
	)

	var (
		active    int32
		maxActive int32
		visited   sync.Map
	)

	runWithBatchLimit("test-batch-limit", total, batchLimit, func(index int) {
		visited.Store(index, true)

		cur := atomic.AddInt32(&active, 1)
		for {
			prev := atomic.LoadInt32(&maxActive)
			if cur <= prev {
				break
			}
			if atomic.CompareAndSwapInt32(&maxActive, prev, cur) {
				break
			}
		}

		time.Sleep(15 * time.Millisecond)
		atomic.AddInt32(&active, -1)
	})

	if maxActive > batchLimit {
		t.Fatalf("max active exceeded batch limit: max=%d limit=%d", maxActive, batchLimit)
	}

	for i := 0; i < total; i++ {
		if _, ok := visited.Load(i); !ok {
			t.Fatalf("index %d not visited", i)
		}
	}
}

func TestWaitAllSSHReady_RetryAndPersistFail(t *testing.T) {
	outputDir := t.TempDir()
	d := &Deployer{
		ctx: context.Background(),
		cfg: DeployConfig{
			CommonConfig: CommonConfig{
				SSHUser:               "ubuntu",
				SSHMaxConcurrency:     2,
				SSHReadyRetryCount:    3,
				SSHReadyRetryInterval: 1 * time.Millisecond,
				OutputDir:             outputDir,
			},
		},
		outputMgr:  NewOutputManager(outputDir),
		sshKeyPath: "/tmp/dummy.pem",
	}

	svc := ServiceConfig{
		Type:      enums.ServiceTypeOP,
		TagPrefix: "ydyl",
	}

	oldWait := waitSSHReadyFunc
	defer func() { waitSSHReadyFunc = oldWait }()

	var (
		mu       sync.Mutex
		attempts = map[string]uint{}
	)
	waitSSHReadyFunc = func(_ context.Context, ip, _ string, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		attempts[ip]++
		if ip == "1.1.1.1" {
			return nil
		}
		return errors.New("ssh not ready")
	}

	err := d.waitAllSSHReady([]string{"1.1.1.1", "2.2.2.2"}, svc)
	if err == nil {
		t.Fatalf("waitAllSSHReady should return error when one host keeps failing")
	}

	mu.Lock()
	successAttempts := attempts["1.1.1.1"]
	failAttempts := attempts["2.2.2.2"]
	mu.Unlock()

	if successAttempts != 1 {
		t.Fatalf("unexpected success host attempts: got=%d want=1", successAttempts)
	}
	if failAttempts != 4 {
		t.Fatalf("unexpected failed host attempts: got=%d want=4", failAttempts)
	}

	data, readErr := os.ReadFile(filepath.Join(outputDir, "ssh_scripts.json"))
	if readErr != nil {
		t.Fatalf("read ssh_scripts.json failed: %v", readErr)
	}

	var list []SSHScriptStatus
	if unmarshalErr := json.Unmarshal(data, &list); unmarshalErr != nil {
		t.Fatalf("unmarshal ssh_scripts.json failed: %v", unmarshalErr)
	}
	if len(list) != 2 {
		t.Fatalf("unexpected ssh script status count: got=%d want=2", len(list))
	}

	byIP := make(map[string]SSHScriptStatus, len(list))
	for _, st := range list {
		byIP[st.IP] = st
	}

	successSt, ok := byIP["1.1.1.1"]
	if !ok {
		t.Fatalf("missing success host status")
	}
	if successSt.Status != "success" {
		t.Fatalf("unexpected success status: %s", successSt.Status)
	}
	if successSt.Attempts != 1 {
		t.Fatalf("unexpected success attempts: got=%d want=1", successSt.Attempts)
	}

	failSt, ok := byIP["2.2.2.2"]
	if !ok {
		t.Fatalf("missing failed host status")
	}
	if failSt.Status != "fail" {
		t.Fatalf("unexpected failed status: %s", failSt.Status)
	}
	if failSt.Attempts != 4 {
		t.Fatalf("unexpected failed attempts: got=%d want=4", failSt.Attempts)
	}
	if failSt.Reason == "" {
		t.Fatalf("failed status reason should not be empty")
	}
}

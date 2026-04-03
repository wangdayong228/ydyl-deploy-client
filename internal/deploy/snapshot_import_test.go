package deploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wangdayong228/ydyl-deploy-client/internal/constants/enums"
)

func TestCopyServersCreateSnapshotToTemp_ReadsAfterSourceRemoved(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "servers_create.json")
	content := `[{"serviceType":"op","ip":"1.1.1.1","name":"n1"},{"serviceType":"op","ip":"","name":"x"}]`
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	tmp, cleanup, err := CopyServersCreateSnapshotToTemp(src)
	if err != nil {
		t.Fatalf("CopyServersCreateSnapshotToTemp: %v", err)
	}
	defer cleanup()

	if err := os.Remove(src); err != nil {
		t.Fatalf("remove source: %v", err)
	}

	got, err := LoadCreatedServersFromFile(tmp)
	if err != nil {
		t.Fatalf("LoadCreatedServersFromFile(tmp): %v", err)
	}
	if len(got) != 1 || got[0].IP != "1.1.1.1" || got[0].ServiceType != "op" {
		t.Fatalf("unexpected loaded: %+v", got)
	}
}

func TestLoadCreatedServersFromFile_InvalidJSON(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(p, []byte(`not json`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadCreatedServersFromFile(p); err == nil {
		t.Fatalf("expected error")
	}
}

func TestSnapshotIPPoolForService_DedupeAndSort(t *testing.T) {
	t.Parallel()

	d := &Deployer{
		importedSnapshot: []CreatedServerInfo{
			{ServiceType: "op", IP: "2.2.2.2"},
			{ServiceType: "op", IP: "1.1.1.1"},
			{ServiceType: "op", IP: "1.1.1.1"},
			{ServiceType: "cdk", IP: "3.3.3.3"},
			{ServiceType: "OP", IP: "4.4.4.4"},
		},
	}
	pool := d.snapshotIPPoolForService("op")
	want := "1.1.1.1,2.2.2.2,4.4.4.4"
	if strings.Join(pool, ",") != want {
		t.Fatalf("pool=%v want=%s", pool, want)
	}
}

func TestAcquireSSHReadyIPsFromSnapshot_Success(t *testing.T) {
	t.Parallel()

	outputDir := t.TempDir()
	d := &Deployer{
		ctx: context.Background(),
		cfg: DeployConfig{
			CommonConfig: CommonConfig{
				SSHUser:               "ubuntu",
				SSHMaxConcurrency:     4,
				SSHReadyRetryCount:    1,
				SSHReadyRetryInterval: 1 * time.Millisecond,
				OutputDir:             outputDir,
			},
		},
		outputMgr:         NewOutputManager(outputDir),
		sshKeyPath:        "/tmp/dummy.pem",
		reuseFromSnapshot: true,
		importedSnapshot: []CreatedServerInfo{
			{ServiceType: "op", IP: "10.0.0.2"},
			{ServiceType: "op", IP: "10.0.0.1"},
		},
	}

	oldWait := waitSSHReadyFunc
	defer func() { waitSSHReadyFunc = oldWait }()
	waitSSHReadyFunc = func(context.Context, string, string, string) error { return nil }

	svc := ServiceConfig{
		Type:         enums.ServiceTypeOP,
		TagPrefix:    "ydyl",
		Count:        2,
		InstanceType: []string{"t3.micro"},
	}

	ips, err := d.acquireSSHReadyIPs(svc, 2)
	if err != nil {
		t.Fatalf("acquireSSHReadyIPs: %v", err)
	}
	if strings.Join(ips, ",") != "10.0.0.1,10.0.0.2" {
		t.Fatalf("unexpected ips: %v", ips)
	}
}

func TestAcquireSSHReadyIPsFromSnapshot_InsufficientPool(t *testing.T) {
	t.Parallel()

	d := &Deployer{
		ctx:               context.Background(),
		cfg:               DeployConfig{CommonConfig: CommonConfig{}},
		reuseFromSnapshot: true,
		importedSnapshot: []CreatedServerInfo{
			{ServiceType: "op", IP: "10.0.0.1"},
		},
	}

	svc := ServiceConfig{
		Type:         enums.ServiceTypeOP,
		TagPrefix:    "ydyl",
		Count:        3,
		InstanceType: []string{"t3.micro"},
	}

	_, err := d.acquireSSHReadyIPs(svc, 3)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !strings.Contains(err.Error(), "快照中可用 IP 不足") {
		t.Fatalf("unexpected err: %v", err)
	}
}

package deploy

import (
	"strings"
	"testing"

	"github.com/wangdayong228/ydyl-deploy-client/internal/constants/enums"
)

func TestShouldCollectXjstNodeByName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{name: "node-1 flat", in: "tps-ydyl-xjst-1", want: true},
		{name: "node-5 flat", in: "tps-ydyl-xjst-5", want: true},
		{name: "node-2 flat", in: "tps-ydyl-xjst-2", want: false},
		{name: "node-1 grouped", in: "tps-ydyl4-xjst-1-1", want: true},
		{name: "node-2 grouped", in: "tps-ydyl4-xjst-1-2", want: false},
		{name: "invalid", in: "tps-ydyl-xjst", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shouldCollectXjstNodeByName(tc.in)
			if got != tc.want {
				t.Fatalf("shouldCollectXjstNodeByName(%q)=%v, want=%v", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildCollectTargets_Xjst(t *testing.T) {
	t.Parallel()

	node1 := buildCollectTargets(&ScriptStatus{
		ServiceType: "xjst",
		Name:        "tps-ydyl4-xjst-1-1",
		LogPath:     "/home/ubuntu/ydyl-deploy-logs/tps-ydyl4-xjst-1-1.log",
	})
	if len(node1) != 2 {
		t.Fatalf("xjst node-1 should have 2 targets, got %d", len(node1))
	}
	for i, target := range node1 {
		if target.SkipByDesign {
			t.Fatalf("xjst node-1 target[%d] should not be skipped", i)
		}
	}
	if node1[0].Category != "deploy" || node1[1].Category != "runtime" {
		t.Fatalf("xjst node-1 targets should be deploy then runtime, got %q then %q", node1[0].Category, node1[1].Category)
	}
}

func TestPickLatestBenchClientLog(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		paths   []string
		want    string
		wantOK  bool
	}{
		{
			name: "multiple picks latest ts",
			paths: []string{
				"/home/ubuntu/workspace/ydyl-deployment-suite/ydyl-deploy-client/logs/client/bench-cross-tx-20260101-120000.log",
				"/home/ubuntu/workspace/ydyl-deployment-suite/ydyl-deploy-client/logs/client/bench-cross-tx-20260202-030405.log",
				"/home/ubuntu/workspace/ydyl-deployment-suite/ydyl-deploy-client/logs/client/bench-cross-tx-20260115-235959.log",
			},
			want:   "/home/ubuntu/workspace/ydyl-deployment-suite/ydyl-deploy-client/logs/client/bench-cross-tx-20260202-030405.log",
			wantOK: true,
		},
		{
			name:   "single file",
			paths:  []string{"bench-cross-tx-20260102-150405.log"},
			want:   "bench-cross-tx-20260102-150405.log",
			wantOK: true,
		},
		{
			name:   "empty list",
			paths:  nil,
			wantOK: false,
		},
		{
			name: "ignores invalid names",
			paths: []string{
				"deploy-20260102-150405.log",
				"bench-cross-tx-invalid.log",
			},
			wantOK: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := pickLatestBenchClientLog(tc.paths)
			if ok != tc.wantOK {
				t.Fatalf("pickLatestBenchClientLog ok=%v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("pickLatestBenchClientLog=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestRemoteBenchClientLogDir(t *testing.T) {
	t.Parallel()

	got := remoteBenchClientLogDir(CommonConfig{})
	want := "/home/ubuntu/workspace/ydyl-deployment-suite/ydyl-deploy-client/logs/client"
	if got != want {
		t.Fatalf("default dir=%q, want %q", got, want)
	}

	got = remoteBenchClientLogDir(CommonConfig{LogDir: "custom-logs"})
	want = "/home/ubuntu/workspace/ydyl-deployment-suite/ydyl-deploy-client/custom-logs/client"
	if got != want {
		t.Fatalf("custom logDir=%q, want %q", got, want)
	}
}

func TestBenchClientLocalDirName(t *testing.T) {
	t.Parallel()

	got := benchClientLocalDirName("44.233.222.148")
	if got != "44.233.222.148_bench-client" {
		t.Fatalf("dir name=%q, want 44.233.222.148_bench-client", got)
	}
}

func TestBuildBenchClientListCommand(t *testing.T) {
	t.Parallel()

	remoteDir := "/home/ubuntu/workspace/ydyl-deployment-suite/ydyl-deploy-client/logs/client"
	got := buildBenchClientListCommand(remoteDir)
	want := "ls -1 '/home/ubuntu/workspace/ydyl-deployment-suite/ydyl-deploy-client/logs/client' 2>/dev/null || true"
	if got != want {
		t.Fatalf("buildBenchClientListCommand=%q, want %q", got, want)
	}
	if strings.Contains(got, "*") {
		t.Fatalf("list command should not embed shell glob: %q", got)
	}
}

func TestResolveBenchClientRemotePath(t *testing.T) {
	t.Parallel()

	remoteDir := "/home/ubuntu/workspace/ydyl-deployment-suite/ydyl-deploy-client/logs/client"
	cases := []struct {
		name   string
		picked string
		want   string
	}{
		{
			name:   "basename",
			picked: "bench-cross-tx-20260102-150405.log",
			want:   remoteDir + "/bench-cross-tx-20260102-150405.log",
		},
		{
			name:   "absolute unchanged",
			picked: remoteDir + "/bench-cross-tx-20260102-150405.log",
			want:   remoteDir + "/bench-cross-tx-20260102-150405.log",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveBenchClientRemotePath(remoteDir, tc.picked)
			if got != tc.want {
				t.Fatalf("resolveBenchClientRemotePath=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseRemoteLSOutput(t *testing.T) {
	t.Parallel()

	got := parseRemoteLSOutput("  /a/b.log\n\n/c/d.log  \n")
	want := []string{"/a/b.log", "/c/d.log"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("parseRemoteLSOutput=%v, want %v", got, want)
	}
}

func TestCollectLogsWarmupIPs(t *testing.T) {
	t.Parallel()

	statuses := []*ScriptStatus{
		{ServiceType: "cdk", Name: "tps-ydyl-cdk-1", IP: "54.202.92.168"},
		{ServiceType: "op", Name: "tps-ydyl-op-1", IP: "44.244.76.243"},
		{ServiceType: "xjst", Name: "tps-ydyl4-xjst-1-1", IP: "35.92.141.61"},
		{ServiceType: "xjst", Name: "tps-ydyl4-xjst-1-2", IP: "16.145.47.68"},
	}
	got := collectLogsWarmupIPs(statuses, "127.0.0.1")
	want := []string{"127.0.0.1", "35.92.141.61", "44.244.76.243", "54.202.92.168"}
	if len(got) != len(want) {
		t.Fatalf("collectLogsWarmupIPs=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("collectLogsWarmupIPs=%v, want %v", got, want)
		}
	}
}

func TestParseLastInt64Line_SSHKnownHostsWarning(t *testing.T) {
	t.Parallel()

	out := "Warning: Permanently added '54.202.92.168' (ED25519) to the list of known hosts.\r\n1253"
	got, err := parseLastInt64Line(out)
	if err != nil {
		t.Fatalf("parseLastInt64Line failed: %v", err)
	}
	if got != 1253 {
		t.Fatalf("parseLastInt64Line=%d, want 1253", got)
	}
}

func TestBuildRsyncSSHSpec(t *testing.T) {
	t.Parallel()

	got := buildRsyncSSHSpec("/home/user/.ssh/key.pem")
	want := "ssh -o StrictHostKeyChecking=no -o IdentitiesOnly=yes -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -i /home/user/.ssh/key.pem"
	if got != want {
		t.Fatalf("buildRsyncSSHSpec=%q, want %q", got, want)
	}
}

func TestBuildRuntimeMonitorCommand(t *testing.T) {
	t.Parallel()

	cmd, ok := buildRuntimeMonitorCommand(enums.ServiceTypeCDK, 0, "demo-cdk-1")
	if !ok || cmd == "" {
		t.Fatalf("cdk monitor command should be enabled")
	}
	if !strings.Contains(cmd, "for i in $(seq 1 180)") {
		t.Fatalf("cdk monitor command should wait for script presence")
	}
	if !strings.Contains(cmd, remoteMonitorScriptMissingMarker) {
		t.Fatalf("cdk monitor command should include missing-script marker")
	}
	if !strings.Contains(cmd, remoteMonitorExitEarlyMarker) {
		t.Fatalf("cdk monitor command should include early-exit marker")
	}
	if !strings.Contains(cmd, "--stack cdk") {
		t.Fatalf("cdk monitor command should pass --stack cdk, got: %s", cmd)
	}

	cmd, ok = buildRuntimeMonitorCommand(enums.ServiceTypeOP, 0, "demo-op-1")
	if !ok || cmd == "" {
		t.Fatalf("op monitor command should be enabled")
	}
	if !strings.Contains(cmd, "--stack op") {
		t.Fatalf("op monitor command should pass --stack op, got: %s", cmd)
	}

	cmd, ok = buildRuntimeMonitorCommand(enums.ServiceTypeXJST, 1, "demo-xjst-2")
	if ok || cmd != "" {
		t.Fatalf("xjst non-node1 should not start runtime monitor")
	}

	cmd, ok = buildRuntimeMonitorCommand(enums.ServiceTypeXJST, 4, "demo-xjst-5")
	if !ok || cmd == "" {
		t.Fatalf("xjst node1 should start runtime monitor")
	}
	if strings.Contains(cmd, "--stack") {
		t.Fatalf("xjst docker monitor command should not pass --stack, got: %s", cmd)
	}
}

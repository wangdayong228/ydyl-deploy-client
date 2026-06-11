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

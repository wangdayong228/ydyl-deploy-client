package deploy

import (
	"strings"
	"testing"

	"github.com/wangdayong228/ydyl-deploy-client/internal/constants/enums"
)

func TestShouldCollectXjstRuntimeByName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want bool
	}{
		{name: "node-1", in: "tps-ydyl-xjst-1", want: true},
		{name: "node-5", in: "tps-ydyl-xjst-5", want: true},
		{name: "node-2", in: "tps-ydyl-xjst-2", want: false},
		{name: "invalid", in: "tps-ydyl-xjst", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shouldCollectXjstRuntimeByName(tc.in)
			if got != tc.want {
				t.Fatalf("shouldCollectXjstRuntimeByName(%q)=%v, want=%v", tc.in, got, tc.want)
			}
		})
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

	cmd, ok = buildRuntimeMonitorCommand(enums.ServiceTypeXJST, 1, "demo-xjst-2")
	if ok || cmd != "" {
		t.Fatalf("xjst non-node1 should not start runtime monitor")
	}

	cmd, ok = buildRuntimeMonitorCommand(enums.ServiceTypeXJST, 4, "demo-xjst-5")
	if !ok || cmd == "" {
		t.Fatalf("xjst node1 should start runtime monitor")
	}
}

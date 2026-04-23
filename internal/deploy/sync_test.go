package deploy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectTerminalStatusFromLog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		serviceType string
		logData     string
		wantStatus  string
		wantReason  string
		wantDecided bool
	}{
		{
			name:        "cdk success marker",
			serviceType: "cdk",
			logData:     "...\n所有步骤完成\n",
			wantStatus:  "success",
			wantReason:  "",
			wantDecided: true,
		},
		{
			name:        "cdk failure marker",
			serviceType: "cdk",
			logData:     "...\ncdk_pipe.sh 执行失败\n",
			wantStatus:  "failed",
			wantReason:  "cdk_pipe.sh 日志中包含失败信息，请查看详细日志",
			wantDecided: true,
		},
		{
			name:        "op success marker",
			serviceType: "op",
			logData:     "...\n所有步骤完成\n",
			wantStatus:  "success",
			wantReason:  "",
			wantDecided: true,
		},
		{
			name:        "op no terminal marker",
			serviceType: "op",
			logData:     "...\nstill running\n",
			wantStatus:  "",
			wantReason:  "",
			wantDecided: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, reason, decided := detectTerminalStatusFromLog([]byte(tt.logData), &ScriptStatus{
				ServiceType: tt.serviceType,
			})
			if status != tt.wantStatus || reason != tt.wantReason || decided != tt.wantDecided {
				t.Fatalf("detectTerminalStatusFromLog()=(%q,%q,%v), want=(%q,%q,%v)",
					status, reason, decided, tt.wantStatus, tt.wantReason, tt.wantDecided)
			}
		})
	}
}

func TestReadFileTail(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "sample.log")
	content := "0123456789"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write log failed: %v", err)
	}

	tail, err := readFileTail(logPath, 4)
	if err != nil {
		t.Fatalf("readFileTail failed: %v", err)
	}
	if got, want := string(tail), "6789"; got != want {
		t.Fatalf("unexpected tail: got=%q want=%q", got, want)
	}
}

func TestDeriveTerminalStatusFromLocalLog(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "status.log")
	if err := os.WriteFile(logPath, []byte("line1\n所有步骤完成\n"), 0o644); err != nil {
		t.Fatalf("write log failed: %v", err)
	}

	status, reason, decided, err := deriveTerminalStatusFromLocalLog(logPath, &ScriptStatus{
		ServiceType: "op",
	})
	if err != nil {
		t.Fatalf("deriveTerminalStatusFromLocalLog failed: %v", err)
	}
	if !decided {
		t.Fatalf("expected decided=true")
	}
	if status != "success" || reason != "" {
		t.Fatalf("unexpected terminal status: status=%q reason=%q", status, reason)
	}
}

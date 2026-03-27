package deploy

import "testing"

func TestFilterStatusesByIPs_WithoutTargetIPs_OnlyNonSuccess(t *testing.T) {
	statuses := []*ScriptStatus{
		{IP: "1.1.1.1", Status: "success", Command: "echo 1"},
		{IP: "2.2.2.2", Status: "running", Command: "echo 2"},
		{IP: "3.3.3.3", Status: "failed", Command: "echo 3"},
		{IP: "4.4.4.4", Status: "", Command: "echo 4"},
		{IP: "5.5.5.5", Status: "failed", Command: ""},
		nil,
	}

	filtered, err := filterStatusesByIPs(statuses, nil)
	if err != nil {
		t.Fatalf("filterStatusesByIPs returned error: %v", err)
	}

	if len(filtered) != 3 {
		t.Fatalf("unexpected filtered count: got=%d want=3", len(filtered))
	}

	if filtered[0].IP != "2.2.2.2" || filtered[1].IP != "3.3.3.3" || filtered[2].IP != "4.4.4.4" {
		t.Fatalf("unexpected filtered result: %+v", filtered)
	}
}

func TestFilterStatusesByIPs_WithoutSpecifiedIPs_BlankValuesTreatedAsUnset(t *testing.T) {
	statuses := []*ScriptStatus{
		{IP: "1.1.1.1", Status: "success", Command: "echo 1"},
		{IP: "2.2.2.2", Status: "pending", Command: "echo 2"},
		{IP: "3.3.3.3", Status: "failed", Command: ""},
	}

	filtered, err := filterStatusesByIPs(statuses, []string{"", "   "})
	if err != nil {
		t.Fatalf("filterStatusesByIPs returned error: %v", err)
	}

	if len(filtered) != 1 {
		t.Fatalf("unexpected filtered count: got=%d want=1", len(filtered))
	}
	if filtered[0].IP != "2.2.2.2" {
		t.Fatalf("unexpected filtered IP: got=%s want=2.2.2.2", filtered[0].IP)
	}
}

func TestFilterStatusesByIPs_WithTargetIPs_KeepSpecifiedIPs(t *testing.T) {
	statuses := []*ScriptStatus{
		{IP: "1.1.1.1", Status: "success", Command: "echo 1"},
		{IP: "2.2.2.2", Status: "failed", Command: "echo 2"},
	}

	filtered, err := filterStatusesByIPs(statuses, []string{"1.1.1.1"})
	if err != nil {
		t.Fatalf("filterStatusesByIPs returned error: %v", err)
	}

	if len(filtered) != 1 {
		t.Fatalf("unexpected filtered count: got=%d want=1", len(filtered))
	}
	if filtered[0].IP != "1.1.1.1" {
		t.Fatalf("unexpected filtered IP: got=%s want=1.1.1.1", filtered[0].IP)
	}
}

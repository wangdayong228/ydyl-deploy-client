package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestGenPrivateKeyCommand_Registered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"gen-private-key"})
	if err != nil {
		t.Fatalf("find gen-private-key command: %v", err)
	}
	if cmd == nil {
		t.Fatalf("gen-private-key command not found")
	}
	if cmd.Flags().Lookup("groupID") == nil {
		t.Fatalf("groupID flag not found")
	}
	if cmd.Flags().Lookup("chainID") == nil {
		t.Fatalf("chainID flag not found")
	}
	if cmd.Flags().Lookup("index") == nil {
		t.Fatalf("index flag not found")
	}
	if cmd.Flags().Lookup("l2type") == nil {
		t.Fatalf("l2type flag not found")
	}
}

func TestGenPrivateKeyCommand_GeneratesEVMKey(t *testing.T) {
	cmd := newGenPrivateKeyCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--chainID", "324", "--index", "42", "--l2type", "0"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute gen-private-key: %v", err)
	}

	got := strings.TrimSpace(out.String())
	want := "0x000000000000000000000000000000000000000001440000000000000000002a"
	if got != want {
		t.Fatalf("generated key mismatch, got=%s want=%s", got, want)
	}
}

func TestGenPrivateKeyCommand_GeneratesXJSTKey(t *testing.T) {
	cmd := newGenPrivateKeyCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--groupID", "77", "--index", "42", "--l2type", "2"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute gen-private-key: %v", err)
	}

	got := strings.TrimSpace(out.String())
	want := "0x0000000000000000000000000000000000000000004d0000000000000000002a"
	if got != want {
		t.Fatalf("generated key mismatch, got=%s want=%s", got, want)
	}
}

func TestGenPrivateKeyCommand_RequiresGroupIDForXJST(t *testing.T) {
	cmd := newGenPrivateKeyCommand()
	cmd.SetArgs([]string{"--index", "42", "--l2type", "2"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing groupID error")
	}
	if !strings.Contains(err.Error(), "--groupID") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGenPrivateKeyCommand_RequiresValidL2Type(t *testing.T) {
	cmd := newGenPrivateKeyCommand()
	cmd.SetArgs([]string{"--chainID", "324", "--index", "42", "--l2type", "9"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected invalid l2type error")
	}
	if !strings.Contains(err.Error(), "l2type") {
		t.Fatalf("unexpected error: %v", err)
	}
}

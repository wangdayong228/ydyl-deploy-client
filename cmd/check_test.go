package cmd

import (
	"testing"
)

func TestCheckCommand_HasConfigFlag(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"check"})
	if err != nil {
		t.Fatalf("find check command: %v", err)
	}
	if cmd == nil {
		t.Fatal("check command not found")
	}
	flag := cmd.Flags().Lookup("config")
	if flag == nil {
		t.Fatal("config flag not found")
	}
	if flag.Shorthand != "f" {
		t.Fatalf("config shorthand want f, got %q", flag.Shorthand)
	}
}

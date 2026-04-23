package cmd

import "testing"

func TestTPSCommand_ConfigFlagIsOptional(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"tps"})
	if err != nil {
		t.Fatalf("find tps command: %v", err)
	}
	if cmd == nil {
		t.Fatalf("tps command not found")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Fatalf("config flag not found")
	}
	if err := cmd.ValidateRequiredFlags(); err != nil {
		t.Fatalf("config flag should be optional: %v", err)
	}
}

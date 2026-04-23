package cmd

import "testing"

func TestBenchCrossTxCommand_Flags(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"bench-cross-tx"})
	if err != nil {
		t.Fatalf("find bench-cross-tx command: %v", err)
	}
	if cmd == nil {
		t.Fatalf("bench-cross-tx command not found")
	}
	if cmd.Flags().Lookup("config") == nil {
		t.Fatalf("config flag not found")
	}
	if err := cmd.ValidateRequiredFlags(); err != nil {
		t.Fatalf("config flag should be optional: %v", err)
	}
	if cmd.Flags().Lookup("concurrency") != nil {
		t.Fatalf("concurrency flag should be removed")
	}
}

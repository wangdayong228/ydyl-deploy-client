package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/samplewallets"
)

func TestSampleWalletsFlagDefaults(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"sample-wallets"})
	if err != nil {
		t.Fatalf("find sample-wallets: %v", err)
	}
	servers, err := cmd.Flags().GetString("servers")
	if err != nil {
		t.Fatalf("get servers: %v", err)
	}
	if servers != samplewallets.DefaultServersPath {
		t.Fatalf("servers default = %q, want %q", servers, samplewallets.DefaultServersPath)
	}
	maxIndex, err := cmd.Flags().GetUint64("max-index")
	if err != nil {
		t.Fatalf("get max-index: %v", err)
	}
	if maxIndex != samplewallets.DefaultMaxIndex {
		t.Fatalf("max-index default = %d, want %d", maxIndex, samplewallets.DefaultMaxIndex)
	}
}

func TestSampleWalletsL2TypeIsRequired(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"sample-wallets"})
	if err != nil {
		t.Fatalf("find sample-wallets: %v", err)
	}
	flag := cmd.Flags().Lookup("l2type")
	if flag == nil {
		t.Fatal("missing l2type flag")
	}
	vals := flag.Annotations[cobra.BashCompOneRequiredFlag]
	if len(vals) == 0 || vals[0] != "true" {
		t.Fatal("l2type should be a required flag")
	}
}

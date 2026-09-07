package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenAccountsCommandsRegistered(t *testing.T) {
	parent, _, err := rootCmd.Find([]string{"gen-accounts"})
	require.NoError(t, err)
	require.Equal(t, "gen-accounts", parent.Name())

	for _, name := range []string{"start", "stop", "resume"} {
		sub, _, err := parent.Find([]string{name})
		require.NoError(t, err, name)
		require.Equal(t, name, sub.Name())
	}

	servers, err := parent.PersistentFlags().GetString("servers")
	require.NoError(t, err)
	require.Equal(t, "./output/servers.json", servers)
}

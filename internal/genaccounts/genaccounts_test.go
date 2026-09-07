package genaccounts

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
	"github.com/wangdayong228/ydyl-deploy-client/internal/infra/oscmdexec"
)

func sampleServers() []deploy.ServerInfo {
	return []deploy.ServerInfo{
		{IP: "16.148.129.163", ServiceType: "op", Name: "tps-ydyl-op-1"},
		{IP: "32.185.252.61", ServiceType: "op", Name: "tps-ydyl-op-2"},
		{IP: "54.184.62.32", ServiceType: "cdk", Name: "tps-ydyl-cdk-1"},
		{IP: "54.190.188.240", ServiceType: "cdk", Name: "tps-ydyl-cdk-2"},
		{IP: "54.70.9.48", ServiceType: "xjst", Name: "tps-ydyl-xjst-1-1"},
		{IP: "35.89.110.26", ServiceType: "xjst", Name: "tps-ydyl-xjst-1-2"},
		{IP: "44.243.239.15", ServiceType: "xjst", Name: "tps-ydyl-xjst-1-3"},
		{IP: "35.91.1.109", ServiceType: "xjst", Name: "tps-ydyl-xjst-1-4"},
		{IP: "54.68.140.171", ServiceType: "xjst", Name: "tps-ydyl-xjst-2-1"},
		{IP: "35.92.164.22", ServiceType: "xjst", Name: "tps-ydyl-xjst-2-2"},
		{IP: "16.147.237.135", ServiceType: "xjst", Name: "tps-ydyl-xjst-2-3"},
		{IP: "34.211.242.110", ServiceType: "xjst", Name: "tps-ydyl-xjst-2-4"},
	}
}

func TestSelectTargets_KeepsOPCDKAndXjstNode1(t *testing.T) {
	got, err := SelectTargets(sampleServers())
	require.NoError(t, err)

	names := make([]string, 0, len(got))
	for _, s := range got {
		names = append(names, s.Name)
	}
	require.Equal(t, []string{
		"tps-ydyl-cdk-1",
		"tps-ydyl-cdk-2",
		"tps-ydyl-op-1",
		"tps-ydyl-op-2",
		"tps-ydyl-xjst-1-1",
		"tps-ydyl-xjst-2-1",
	}, names)
}

func TestSelectTargets_DoesNotUseGlobalDash1Suffix(t *testing.T) {
	got, err := SelectTargets([]deploy.ServerInfo{
		{IP: "1.1.1.1", ServiceType: "op", Name: "tps-ydyl-op-2"},
		{IP: "2.2.2.2", ServiceType: "cdk", Name: "tps-ydyl-cdk-2"},
		{IP: "3.3.3.3", ServiceType: "xjst", Name: "tps-ydyl-xjst-1-2"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"tps-ydyl-cdk-2", "tps-ydyl-op-2"}, targetNames(got))
}

func TestSelectTargets_InvalidNameFails(t *testing.T) {
	_, err := SelectTargets([]deploy.ServerInfo{
		{IP: "1.1.1.1", ServiceType: "xjst", Name: "prefix-xjst-1"},
	})
	require.Error(t, err)
}

func TestSelectTargets_EmptyAfterFilterFails(t *testing.T) {
	_, err := SelectTargets([]deploy.ServerInfo{
		{IP: "3.3.3.3", ServiceType: "xjst", Name: "tps-ydyl-xjst-1-2"},
	})
	require.Error(t, err)
}

func TestRemoteCommand_Scripts(t *testing.T) {
	tests := []struct {
		action Action
		script string
	}{
		{ActionStart, "npm run start"},
		{ActionStop, "npm run stop"},
		{ActionResume, "npm run resume"},
	}
	for _, tt := range tests {
		t.Run(string(tt.action), func(t *testing.T) {
			cmd, err := RemoteCommand(tt.action)
			require.NoError(t, err)
			require.Contains(t, cmd, `source "$HOME/.ydyl-env"`)
			require.Contains(t, cmd, "/home/ubuntu/workspace/ydyl-deployment-suite/ydyl-gen-accounts")
			require.Contains(t, cmd, tt.script)
			require.True(t, strings.HasPrefix(cmd, "bash -lc "))
			for _, other := range tests {
				if other.script == tt.script {
					continue
				}
				require.NotContains(t, cmd, other.script)
			}
		})
	}
}

func TestRemoteCommand_InvalidAction(t *testing.T) {
	_, err := RemoteCommand(Action("restart"))
	require.Error(t, err)
}

func TestRun_SSHOnlySelectedHostsAndActionCommand(t *testing.T) {
	dir := t.TempDir()
	serversPath := writeServersJSON(t, dir, sampleServers())

	var mu sync.Mutex
	var specs []oscmdexec.Spec
	runner := func(ctx context.Context, spec oscmdexec.Spec) error {
		mu.Lock()
		defer mu.Unlock()
		specs = append(specs, spec)
		return nil
	}

	err := Run(context.Background(), Params{
		ServersPath:    serversPath,
		Action:         ActionStop,
		SSHUser:        "ubuntu",
		SSHKeyPath:     "/tmp/test-key.pem",
		MaxConcurrency: 8,
		Runner:         runner,
	})
	require.NoError(t, err)

	hosts := sshHosts(t, specs)
	sort.Strings(hosts)
	require.Equal(t, []string{
		"ubuntu@16.148.129.163",
		"ubuntu@32.185.252.61",
		"ubuntu@54.184.62.32",
		"ubuntu@54.190.188.240",
		"ubuntu@54.68.140.171",
		"ubuntu@54.70.9.48",
	}, hosts)
	for _, spec := range specs {
		require.Equal(t, "ssh", spec.Name)
		require.Contains(t, spec.Args, "BatchMode=yes")
		require.Contains(t, spec.Args[len(spec.Args)-1], "npm run stop")
		require.NotContains(t, spec.Args[len(spec.Args)-1], "npm run start")
		require.NotContains(t, spec.Args[len(spec.Args)-1], "npm run resume")
	}
}

func TestRun_PartialSSHFailureAggregates(t *testing.T) {
	dir := t.TempDir()
	servers := []deploy.ServerInfo{
		{IP: "1.1.1.1", ServiceType: "op", Name: "tps-ydyl-op-1"},
		{IP: "2.2.2.2", ServiceType: "op", Name: "tps-ydyl-op-2"},
		{IP: "3.3.3.3", ServiceType: "cdk", Name: "tps-ydyl-cdk-1"},
	}
	serversPath := writeServersJSON(t, dir, servers)

	var mu sync.Mutex
	called := map[string]int{}
	runner := func(ctx context.Context, spec oscmdexec.Spec) error {
		host := sshUserHost(t, spec)
		mu.Lock()
		called[host]++
		mu.Unlock()
		if strings.Contains(host, "2.2.2.2") {
			return errors.New("ssh boom")
		}
		return nil
	}

	err := Run(context.Background(), Params{
		ServersPath:    serversPath,
		Action:         ActionResume,
		SSHUser:        "ubuntu",
		SSHKeyPath:     "/tmp/test-key.pem",
		MaxConcurrency: 8,
		Runner:         runner,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "2.2.2.2")
	require.Contains(t, err.Error(), "tps-ydyl-op-2")
	require.Equal(t, 3, len(called))
	require.Contains(t, err.Error(), "resume")
}

func targetNames(servers []deploy.ServerInfo) []string {
	names := make([]string, 0, len(servers))
	for _, s := range servers {
		names = append(names, s.Name)
	}
	return names
}

func writeServersJSON(t *testing.T, dir string, servers []deploy.ServerInfo) string {
	t.Helper()
	path := filepath.Join(dir, "servers.json")
	b, err := json.Marshal(servers)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o644))
	return path
}

func sshHosts(t *testing.T, specs []oscmdexec.Spec) []string {
	t.Helper()
	hosts := make([]string, 0, len(specs))
	for _, spec := range specs {
		hosts = append(hosts, sshUserHost(t, spec))
	}
	return hosts
}

func sshUserHost(t *testing.T, spec oscmdexec.Spec) string {
	t.Helper()
	require.GreaterOrEqual(t, len(spec.Args), 2)
	return spec.Args[len(spec.Args)-2]
}

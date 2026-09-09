package genaccmonitor

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
	ydylconsolesdk "github.com/wangdayong228/ydyl-deploy-client/pkg/ydyl-console-service-sdk"
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
		{IP: "10.0.0.9", ServiceType: "generic", Name: "tps-ydyl-generic-1"},
		{IP: "", ServiceType: "op", Name: "tps-ydyl-op-99"},
	}
}

func targetNames(servers []deploy.ServerInfo) []string {
	names := make([]string, 0, len(servers))
	for _, s := range servers {
		names = append(names, s.Name)
	}
	return names
}

func TestPickMonitorTargets_KeepsOPCDKAndXjstNode1(t *testing.T) {
	got, err := pickMonitorTargets(sampleServers())
	require.NoError(t, err)
	require.Equal(t, []string{
		"tps-ydyl-cdk-1",
		"tps-ydyl-cdk-2",
		"tps-ydyl-op-1",
		"tps-ydyl-op-2",
		"tps-ydyl-xjst-1-1",
		"tps-ydyl-xjst-2-1",
	}, targetNames(got))
}

func TestPickMonitorTargets_DoesNotUseGlobalDash1Suffix(t *testing.T) {
	got, err := pickMonitorTargets([]deploy.ServerInfo{
		{IP: "1.1.1.1", ServiceType: "op", Name: "tps-ydyl-op-2"},
		{IP: "2.2.2.2", ServiceType: "cdk", Name: "tps-ydyl-cdk-2"},
		{IP: "3.3.3.3", ServiceType: "xjst", Name: "tps-ydyl-xjst-1-2"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"tps-ydyl-cdk-2", "tps-ydyl-op-2"}, targetNames(got))
}

func TestPickMonitorTargets_IgnoresGeneric(t *testing.T) {
	got, err := pickMonitorTargets([]deploy.ServerInfo{
		{IP: "1.1.1.1", ServiceType: "op", Name: "tps-ydyl-op-1"},
		{IP: "9.9.9.9", ServiceType: "generic", Name: "tps-ydyl-generic-1"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"tps-ydyl-op-1"}, targetNames(got))
}

func TestPickMonitorTargets_InvalidNameFails(t *testing.T) {
	_, err := pickMonitorTargets([]deploy.ServerInfo{
		{IP: "1.1.1.1", ServiceType: "xjst", Name: "prefix-xjst-1"},
	})
	require.Error(t, err)
}

func TestAggregate_ByServiceTypeCountsAndAccounts(t *testing.T) {
	items := []SummaryFileItem{
		{
			Name:        "tps-ydyl-op-1",
			ServiceType: "op",
			Summary:     &ydylconsolesdk.GenAccSummaryResponse{AccountGenerated: 100, TotalTxSentCount: 1, Success: 1},
		},
		{
			Name:        "tps-ydyl-op-2",
			ServiceType: "op",
			Error:       "timeout",
		},
		{
			Name:        "tps-ydyl-cdk-1",
			ServiceType: "cdk",
			Summary:     &ydylconsolesdk.GenAccSummaryResponse{AccountGenerated: 40, TotalTxSentCount: 2},
		},
		{
			Name:        "tps-ydyl-xjst-2-1",
			ServiceType: "xjst",
			Summary:     &ydylconsolesdk.GenAccSummaryResponse{AccountGenerated: 7},
		},
	}

	got := aggregate(items)
	require.Equal(t, 2, got.ByServiceType.Op.Count)
	require.Equal(t, 100, got.ByServiceType.Op.AccountGenerated)
	require.Equal(t, 1, got.ByServiceType.Cdk.Count)
	require.Equal(t, 40, got.ByServiceType.Cdk.AccountGenerated)
	require.Equal(t, 1, got.ByServiceType.Xjst.Count)
	require.Equal(t, 7, got.ByServiceType.Xjst.AccountGenerated)
	require.Equal(t, 147, got.Merged.AccountGenerated)
	require.Equal(t, 3, got.Merged.TotalTxSentCount)
	require.Equal(t, 3, got.SuccessServers)
	require.Equal(t, 1, got.ErrorServers)
	require.Equal(t, got.Merged.AccountGenerated, got.ByServiceType.Op.AccountGenerated+got.ByServiceType.Cdk.AccountGenerated+got.ByServiceType.Xjst.AccountGenerated)
}

func TestFormatRoundLine(t *testing.T) {
	line := formatRoundLine("2026-09-09T07:00:00Z", aggregateResult{
		Merged:         ydylconsolesdk.GenAccSummaryResponse{AccountGenerated: 147},
		ByServiceType:  ByServiceType{Op: ChainTypeStats{Count: 2, AccountGenerated: 100}, Cdk: ChainTypeStats{Count: 1, AccountGenerated: 40}, Xjst: ChainTypeStats{Count: 1, AccountGenerated: 7}},
		SuccessServers: 3,
		ErrorServers:   1,
	})
	require.Equal(t, "[2026-09-09T07:00:00Z] totalAccountGenerated=147 successServers=3 errorServers=1 op=2/100 cdk=1/40 xjst=1/7", line)
}

func TestSummaryFileJSON_IncludesByServiceType(t *testing.T) {
	out := SummaryFile{
		UpdatedAt: "2026-09-09T07:00:00Z",
		Items:     []SummaryFileItem{},
		Summary:   ydylconsolesdk.GenAccSummaryResponse{AccountGenerated: 0},
		ByServiceType: ByServiceType{
			Op: ChainTypeStats{Count: 0, AccountGenerated: 0},
		},
	}
	b, err := json.Marshal(out)
	require.NoError(t, err)
	require.Contains(t, string(b), `"byServiceType"`)
	require.Contains(t, string(b), `"op"`)
	require.Contains(t, string(b), `"cdk"`)
	require.Contains(t, string(b), `"xjst"`)
}

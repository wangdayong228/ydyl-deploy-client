package genaccmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wangdayong228/ydyl-deploy-client/internal/crosstxconfig"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
	ydylconsolesdk "github.com/wangdayong228/ydyl-deploy-client/pkg/ydyl-console-service-sdk"
)

type Params struct {
	ServersPath string
	OutPath     string
	Interval    time.Duration
}

type SummaryFileItem struct {
	Name        string                                `json:"name,omitempty"`
	IP          string                                `json:"ip"`
	ServiceType string                                `json:"serviceType"`
	Summary     *ydylconsolesdk.GenAccSummaryResponse `json:"summary,omitempty"`
	UpdatedAt   string                                `json:"updatedAt"`
	Error       string                                `json:"error,omitempty"`
}

type ChainTypeStats struct {
	Count            int `json:"count"`
	AccountGenerated int `json:"accountGenerated"`
}

type ByServiceType struct {
	Op   ChainTypeStats `json:"op"`
	Cdk  ChainTypeStats `json:"cdk"`
	Xjst ChainTypeStats `json:"xjst"`
}

type SummaryFile struct {
	UpdatedAt     string                               `json:"updatedAt"`
	Items         []SummaryFileItem                    `json:"items"`
	Summary       ydylconsolesdk.GenAccSummaryResponse `json:"summary"`
	ByServiceType ByServiceType                        `json:"byServiceType"`
}

type aggregateResult struct {
	Merged         ydylconsolesdk.GenAccSummaryResponse
	ByServiceType  ByServiceType
	SuccessServers int
	ErrorServers   int
}

func Run(ctx context.Context, p Params) error {
	if strings.TrimSpace(p.ServersPath) == "" {
		return fmt.Errorf("servers 不能为空")
	}
	if strings.TrimSpace(p.OutPath) == "" {
		return fmt.Errorf("out 不能为空")
	}
	if p.Interval <= 0 {
		p.Interval = 2 * time.Second
	}

	servers, err := loadServers(p.ServersPath)
	if err != nil {
		return err
	}
	targets, err := pickMonitorTargets(servers)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return fmt.Errorf("servers 中没有可监控的链节点（serviceType 仅支持 op/cdk/xjst）")
	}

	if err := runOneRound(ctx, targets, p.OutPath); err != nil {
		return err
	}

	ticker := time.NewTicker(p.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := runOneRound(ctx, targets, p.OutPath); err != nil {
				return err
			}
		}
	}
}

func runOneRound(ctx context.Context, targets []deploy.ServerInfo, outPath string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	items := make([]SummaryFileItem, len(targets))

	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(idx int, server deploy.ServerInfo) {
			defer wg.Done()
			item := SummaryFileItem{
				Name:        strings.TrimSpace(server.Name),
				IP:          strings.TrimSpace(server.IP),
				ServiceType: strings.ToLower(strings.TrimSpace(server.ServiceType)),
				UpdatedAt:   now,
			}

			baseURL := fmt.Sprintf("http://%s:8080", item.IP)
			sdk := ydylconsolesdk.New(baseURL)
			summary, err := sdk.Result.GetGenAccSummary(ctx)
			if err != nil {
				item.Error = err.Error()
				items[idx] = item
				return
			}
			item.Summary = summary
			items[idx] = item
		}(i, t)
	}
	wg.Wait()

	agg := aggregate(items)
	out := SummaryFile{
		UpdatedAt:     now,
		Items:         items,
		Summary:       agg.Merged,
		ByServiceType: agg.ByServiceType,
	}
	if err := writeJSONFileAtomic(outPath, out); err != nil {
		return err
	}

	fmt.Println(formatRoundLine(now, agg))
	return nil
}

func aggregate(items []SummaryFileItem) aggregateResult {
	var got aggregateResult
	for _, item := range items {
		stats := chainTypeStatsPtr(&got.ByServiceType, item.ServiceType)
		if stats != nil {
			stats.Count++
		}
		if item.Summary == nil {
			if item.Error != "" {
				got.ErrorServers++
			}
			continue
		}
		got.SuccessServers++
		got.Merged.TotalTxSentCount += item.Summary.TotalTxSentCount
		got.Merged.AccountGenerated += item.Summary.AccountGenerated
		got.Merged.AccountRemains += item.Summary.AccountRemains
		got.Merged.Processing += item.Summary.Processing
		got.Merged.Success += item.Summary.Success
		got.Merged.Fail += item.Summary.Fail
		if stats != nil {
			stats.AccountGenerated += item.Summary.AccountGenerated
		}
	}
	return got
}

func chainTypeStatsPtr(by *ByServiceType, serviceType string) *ChainTypeStats {
	switch strings.ToLower(strings.TrimSpace(serviceType)) {
	case "op":
		return &by.Op
	case "cdk":
		return &by.Cdk
	case "xjst":
		return &by.Xjst
	default:
		return nil
	}
}

func formatRoundLine(now string, agg aggregateResult) string {
	return fmt.Sprintf(
		"[%s] totalAccountGenerated=%d successServers=%d errorServers=%d op=%d/%d cdk=%d/%d xjst=%d/%d",
		now,
		agg.Merged.AccountGenerated,
		agg.SuccessServers,
		agg.ErrorServers,
		agg.ByServiceType.Op.Count,
		agg.ByServiceType.Op.AccountGenerated,
		agg.ByServiceType.Cdk.Count,
		agg.ByServiceType.Cdk.AccountGenerated,
		agg.ByServiceType.Xjst.Count,
		agg.ByServiceType.Xjst.AccountGenerated,
	)
}

func pickMonitorTargets(servers []deploy.ServerInfo) ([]deploy.ServerInfo, error) {
	filtered := make([]deploy.ServerInfo, 0, len(servers))
	for _, s := range servers {
		t := strings.ToLower(strings.TrimSpace(s.ServiceType))
		if t != "op" && t != "cdk" && t != "xjst" {
			continue
		}
		ip := strings.TrimSpace(s.IP)
		if ip == "" {
			continue
		}
		filtered = append(filtered, deploy.ServerInfo{
			IP:          ip,
			ServiceType: t,
			Name:        strings.TrimSpace(s.Name),
		})
	}

	entries, err := crosstxconfig.PickChainEntries(filtered)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]deploy.ServerInfo, 0, len(names))
	for _, name := range names {
		out = append(out, entries[name])
	}
	return out, nil
}

func loadServers(path string) ([]deploy.ServerInfo, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 servers 文件失败: %w", err)
	}
	var servers []deploy.ServerInfo
	if err := json.Unmarshal(b, &servers); err != nil {
		return nil, fmt.Errorf("解析 servers 文件失败: %w", err)
	}
	return servers, nil
}

func writeJSONFileAtomic(path string, v any) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建输出目录失败: %w", err)
		}
	}

	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化输出 JSON 失败: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".summary-gen-accounts-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("原子替换输出文件失败: %w", err)
	}
	return nil
}

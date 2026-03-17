package genaccmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

type SummaryFile struct {
	UpdatedAt string                               `json:"updatedAt"`
	Items     []SummaryFileItem                    `json:"items"`
	Summary   ydylconsolesdk.GenAccSummaryResponse `json:"summary"`
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
	targets := pickMonitorTargets(servers)
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

	var merged ydylconsolesdk.GenAccSummaryResponse
	successServers := 0
	errorServers := 0
	for _, item := range items {
		if item.Summary == nil {
			if item.Error != "" {
				errorServers++
			}
			continue
		}
		successServers++
		merged.TotalTxSentCount += item.Summary.TotalTxSentCount
		merged.AccountGenerated += item.Summary.AccountGenerated
		merged.AccountRemains += item.Summary.AccountRemains
		merged.Processing += item.Summary.Processing
		merged.Success += item.Summary.Success
		merged.Fail += item.Summary.Fail
	}

	out := SummaryFile{
		UpdatedAt: now,
		Items:     items,
		Summary:   merged,
	}
	if err := writeJSONFileAtomic(outPath, out); err != nil {
		return err
	}

	fmt.Printf("[%s] totalAccountGenerated=%d successServers=%d errorServers=%d\n", now, merged.AccountGenerated, successServers, errorServers)
	return nil
}

func pickMonitorTargets(servers []deploy.ServerInfo) []deploy.ServerInfo {
	out := make([]deploy.ServerInfo, 0, len(servers))
	for _, s := range servers {
		t := strings.ToLower(strings.TrimSpace(s.ServiceType))
		if t != "op" && t != "cdk" && t != "xjst" {
			continue
		}
		ip := strings.TrimSpace(s.IP)
		if ip == "" {
			continue
		}
		out = append(out, deploy.ServerInfo{
			IP:          ip,
			ServiceType: t,
			Name:        strings.TrimSpace(s.Name),
		})
	}
	return out
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

package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"github.com/wangdayong228/ydyl-deploy-client/internal/chainhealth"
)

var (
	chainStatusServersPath string
	chainStatusStaleAfter  time.Duration
	chainStatusRPCTimeout  time.Duration
)

func init() {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "检查所有链节点的运行状态",
		Long: `读取 servers.json，并发探测每条链主节点的 L2 最新区块号与时间戳，输出健康状态表格。

探测规则：
  - op/cdk: 通过 ydyl-console-service 获取 L2_RPC_URL，再用 web3go 查询区块
  - xjst: 仅检查 xxxx-xjst-n-1 主节点，固定使用 http://<ip>:30010

状态含义：
  ✅  正常     — 最新区块时间戳在 stale-after 阈值内
  ⚠️  出块缓慢  — 节点可达但区块时间超出阈值
  ❌  不可达   — RPC 或 console-service 调用失败`,
		RunE: runChainStatus,
	}

	cmd.Flags().StringVar(&chainStatusServersPath, "servers", "./output/servers.json", "servers.json 路径")
	cmd.Flags().DurationVar(&chainStatusStaleAfter, "stale-after", 2*time.Minute, "区块时间超过该阈值判定为出块缓慢（例如 2m、90s）")
	cmd.Flags().DurationVar(&chainStatusRPCTimeout, "rpc-timeout", 10*time.Second, "单次 RPC 调用超时时间")

	rootCmd.AddCommand(cmd)
}

func runChainStatus(_ *cobra.Command, _ []string) error {
	ctx := context.Background()

	results, err := chainhealth.Check(ctx, chainhealth.Params{
		ServersPath: chainStatusServersPath,
		StaleAfter:  chainStatusStaleAfter,
		RPCTimeout:  chainStatusRPCTimeout,
	})
	if err != nil {
		return err
	}

	printStatusTable(results)
	return nil
}

func printStatusTable(nodes []chainhealth.NodeHealth) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"NAME", "TYPE", "BLOCK", "TIME", "AGE", "STATUS"})
	table.SetBorder(true)
	table.SetAutoWrapText(false)
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetColMinWidth(0, 20)

	for _, n := range nodes {
		row := []string{
			n.Name,
			n.ServiceType,
			endpointBlock(n.L2),
			endpointTime(n.L2),
			endpointAge(n.L2),
			n.Overall().Emoji(),
		}
		table.Append(row)
	}
	table.Render()

	ok, slow, down := 0, 0, 0
	for _, n := range nodes {
		switch n.Overall() {
		case chainhealth.StatusOK:
			ok++
		case chainhealth.StatusSlow:
			slow++
		default:
			down++
		}
	}
	fmt.Printf("\n共 %d 节点  ✅ 正常=%d  ⚠️ 缓慢=%d  ❌ 不可达=%d\n", len(nodes), ok, slow, down)
}

func endpointBlock(e chainhealth.EndpointHealth) string {
	if e.Status == chainhealth.StatusDown {
		return "❌ " + truncateStr(e.Error, 28)
	}
	return fmt.Sprintf("#%d", e.BlockNumber)
}

func endpointTime(e chainhealth.EndpointHealth) string {
	if e.Status == chainhealth.StatusDown {
		return "-"
	}
	return e.BlockTime.UTC().Format("01-02 15:04:05")
}

func endpointAge(e chainhealth.EndpointHealth) string {
	if e.Status == chainhealth.StatusDown {
		return "-"
	}
	return fmtDuration(e.Age)
}

func fmtDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%ds", m, s)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", h, m)
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

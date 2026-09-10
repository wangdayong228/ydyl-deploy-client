package samplewallets

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"

	"github.com/wangdayong228/ydyl-deploy-client/internal/crosstxconfig"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
	"github.com/wangdayong228/ydyl-deploy-client/internal/utils/cryptoutil"
	ydylconsolesdk "github.com/wangdayong228/ydyl-deploy-client/pkg/ydyl-console-service-sdk"
)

const (
	SampleCount        = 10
	DefaultMaxIndex    = uint64(1_000)
	DefaultServersPath = "./output/servers.json"
	CoreSampleName     = "core"
)

type Params struct {
	ServersPath string
	L2Type      int
	MaxIndex    uint64
	RPCURL      string
	ChainID     uint64
}

type Wallet struct {
	Index      *big.Int
	PrivateKey string
	Address    string
	BalanceWei *big.Int
}

type Result struct {
	Name    string
	L2Type  int
	ChainID uint64
	GroupID uint64
	RPC     string
	Wallets []Wallet
}

type SummaryFetcher interface {
	FetchSummary(ctx context.Context, ip string) (*ydylconsolesdk.SummaryResultResponse, error)
}

type BalanceClient interface {
	BalanceAt(ctx context.Context, rpcURL, address string, l2type int) (*big.Int, error)
}

type ConsoleSummaryFetcher struct{}

func (ConsoleSummaryFetcher) FetchSummary(ctx context.Context, ip string) (*ydylconsolesdk.SummaryResultResponse, error) {
	baseURL := fmt.Sprintf("http://%s:8080", strings.TrimSpace(ip))
	sdk := ydylconsolesdk.New(baseURL)
	return sdk.Result.GetDeploySummary(ctx)
}

type JSONRPCBalanceClient struct{}

func (JSONRPCBalanceClient) BalanceAt(ctx context.Context, rpcURL, address string, l2type int) (*big.Int, error) {
	client, err := rpc.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("连接 RPC 失败: %w", err)
	}
	defer client.Close()

	var result hexutil.Big
	if l2type == 2 || l2type == 3 {
		err = client.CallContext(ctx, &result, "cfx_getBalance", address, "latest_state")
	} else {
		err = client.CallContext(ctx, &result, "eth_getBalance", address, "latest")
	}
	if err != nil {
		return nil, err
	}
	return result.ToInt(), nil
}

func Run(ctx context.Context, p Params, fetcher SummaryFetcher, balances BalanceClient) (*Result, error) {
	if p.L2Type != 0 && p.L2Type != 1 && p.L2Type != 2 && p.L2Type != 3 {
		return nil, fmt.Errorf("invalid l2type: %d", p.L2Type)
	}
	if p.MaxIndex < SampleCount {
		return nil, fmt.Errorf("max-index 必须 >= %d，当前=%d", SampleCount, p.MaxIndex)
	}
	if balances == nil {
		return nil, fmt.Errorf("balance client 不能为空")
	}
	if p.L2Type == 3 {
		return runCore(ctx, p, balances)
	}
	if strings.TrimSpace(p.ServersPath) == "" {
		return nil, fmt.Errorf("serversPath 不能为空")
	}
	if fetcher == nil {
		return nil, fmt.Errorf("summary fetcher 不能为空")
	}

	servers, err := crosstxconfig.LoadServers(p.ServersPath)
	if err != nil {
		return nil, err
	}
	entries, err := crosstxconfig.PickChainEntries(servers)
	if err != nil {
		return nil, err
	}
	filtered, err := filterEntries(entries, p.L2Type)
	if err != nil {
		return nil, err
	}
	picked, err := pickRandom(filtered)
	if err != nil {
		return nil, err
	}

	summary, err := fetcher.FetchSummary(ctx, picked.IP)
	if err != nil {
		return nil, fmt.Errorf("获取 summary 失败: name=%s ip=%s: %w", picked.Name, picked.IP, err)
	}
	if summary == nil {
		return nil, fmt.Errorf("summary 为空: name=%s ip=%s", picked.Name, picked.IP)
	}
	rpcURL := strings.TrimSpace(p.RPCURL)
	if rpcURL == "" {
		rpcURL = strings.TrimSpace(crosstxconfig.ReplaceLocalhostWithIP(summary.L2_RPC_URL, picked.IP))
		if rpcURL == "" {
			return nil, fmt.Errorf("L2_RPC_URL 为空: name=%s", picked.Name)
		}
	}

	var chainID, groupID uint64
	if p.L2Type == 2 {
		groupID, err = parseXjstGroupID(picked.Name)
		if err != nil {
			return nil, err
		}
	} else {
		chainID, err = parseChainID(summary.L2_CHAIN_ID)
		if err != nil {
			return nil, err
		}
	}

	wallets, err := sampleWallets(groupID, chainID, p.L2Type, p.MaxIndex, SampleCount)
	if err != nil {
		return nil, err
	}
	for i := range wallets {
		bal, err := balances.BalanceAt(ctx, rpcURL, wallets[i].Address, p.L2Type)
		if err != nil {
			return nil, fmt.Errorf("查询余额失败: address=%s: %w", wallets[i].Address, err)
		}
		wallets[i].BalanceWei = bal
	}

	return &Result{
		Name:    picked.Name,
		L2Type:  p.L2Type,
		ChainID: chainID,
		GroupID: groupID,
		RPC:     rpcURL,
		Wallets: wallets,
	}, nil
}

func runCore(ctx context.Context, p Params, balances BalanceClient) (*Result, error) {
	rpcURL := strings.TrimSpace(p.RPCURL)
	if rpcURL == "" {
		return nil, fmt.Errorf("l2type=3 必须提供 --rpc-url")
	}
	if p.ChainID < 1 {
		return nil, fmt.Errorf("l2type=3 时 chainID 必须 >= 1")
	}
	wallets, err := sampleWallets(0, p.ChainID, 3, p.MaxIndex, SampleCount)
	if err != nil {
		return nil, err
	}
	for i := range wallets {
		bal, err := balances.BalanceAt(ctx, rpcURL, wallets[i].Address, 3)
		if err != nil {
			return nil, fmt.Errorf("查询余额失败: address=%s: %w", wallets[i].Address, err)
		}
		wallets[i].BalanceWei = bal
	}
	return &Result{
		Name:    CoreSampleName,
		L2Type:  3,
		ChainID: p.ChainID,
		RPC:     rpcURL,
		Wallets: wallets,
	}, nil
}

func FormatResult(r *Result) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	if r.L2Type == 2 {
		fmt.Fprintf(&b, "name=%s l2type=%d groupID=%d rpc=%s\n", r.Name, r.L2Type, r.GroupID, r.RPC)
	} else {
		fmt.Fprintf(&b, "name=%s l2type=%d chainID=%d rpc=%s\n", r.Name, r.L2Type, r.ChainID, r.RPC)
	}
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "index\tprivateKey\taddress\tbalanceWei")
	for _, w := range r.Wallets {
		idx := ""
		if w.Index != nil {
			idx = w.Index.String()
		}
		bal := ""
		if w.BalanceWei != nil {
			bal = w.BalanceWei.String()
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", idx, w.PrivateKey, w.Address, bal)
	}
	_ = tw.Flush()
	return b.String()
}

func filterEntries(entries map[string]deploy.ServerInfo, l2type int) ([]deploy.ServerInfo, error) {
	want, err := serviceTypeForL2Type(l2type)
	if err != nil {
		return nil, err
	}
	out := make([]deploy.ServerInfo, 0)
	for _, s := range entries {
		if strings.EqualFold(strings.TrimSpace(s.ServiceType), want) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("servers.json 中没有 l2type=%d (%s) 的入口链", l2type, want)
	}
	return out, nil
}

func serviceTypeForL2Type(l2type int) (string, error) {
	switch l2type {
	case 0:
		return "cdk", nil
	case 1:
		return "op", nil
	case 2:
		return "xjst", nil
	default:
		return "", fmt.Errorf("invalid l2type: %d", l2type)
	}
}

func pickRandom(entries []deploy.ServerInfo) (deploy.ServerInfo, error) {
	if len(entries) == 0 {
		return deploy.ServerInfo{}, fmt.Errorf("没有可选择的链")
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(entries))))
	if err != nil {
		return deploy.ServerInfo{}, err
	}
	return entries[n.Int64()], nil
}

func sampleWallets(groupID, chainID uint64, l2type int, maxIndex uint64, count int) ([]Wallet, error) {
	if maxIndex < uint64(count) {
		return nil, fmt.Errorf("max-index 必须 >= %d，当前=%d", count, maxIndex)
	}
	seen := make(map[string]struct{}, count)
	out := make([]Wallet, 0, count)
	max := new(big.Int).SetUint64(maxIndex)
	for attempts := 0; len(out) < count && attempts < count*10000; attempts++ {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return nil, err
		}
		key := idx.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		pk, err := cryptoutil.BuildDeterministicPrivateKey(groupID, chainID, idx, l2type)
		if err != nil {
			continue
		}
		var addr string
		if l2type == 3 {
			addr, err = cryptoutil.CoreBase32AddressFromPrivateKey(pk, chainID)
		} else {
			addr, err = cryptoutil.AddressFromPrivateKey(pk, l2type)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, Wallet{
			Index:      new(big.Int).Set(idx),
			PrivateKey: pk,
			Address:    addr,
		})
	}
	if len(out) < count {
		return nil, fmt.Errorf("无法抽满 %d 个合法 index", count)
	}
	return out, nil
}

func parseChainID(raw string) (uint64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, fmt.Errorf("L2_CHAIN_ID 为空")
	}
	id, err := strconv.ParseUint(trimmed, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("解析 L2_CHAIN_ID 失败: %q: %w", raw, err)
	}
	return id, nil
}

func parseXjstGroupID(name string) (uint64, error) {
	parts := strings.Split(strings.TrimSpace(name), "-")
	if len(parts) < 4 {
		return 0, fmt.Errorf("name 格式不合法，期望 tagPrefix-xjst-groupId-index: %q", name)
	}
	if !strings.EqualFold(parts[len(parts)-3], "xjst") {
		return 0, fmt.Errorf("name 与 xjst 不匹配: %q", name)
	}
	groupID, err := strconv.ParseUint(parts[len(parts)-2], 10, 64)
	if err != nil || groupID == 0 {
		return 0, fmt.Errorf("groupId 必须是正整数: name=%q", name)
	}
	return groupID, nil
}

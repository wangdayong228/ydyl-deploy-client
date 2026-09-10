package samplewallets

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ydylconsolesdk "github.com/wangdayong228/ydyl-deploy-client/pkg/ydyl-console-service-sdk"
)

type stubSummaryFetcher struct {
	byIP map[string]*ydylconsolesdk.SummaryResultResponse
	err  error
}

func (s stubSummaryFetcher) FetchSummary(ctx context.Context, ip string) (*ydylconsolesdk.SummaryResultResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.byIP == nil {
		return nil, nil
	}
	return s.byIP[ip], nil
}

type recordingBalanceClient struct {
	calls []balanceCall
	wei   *big.Int
	err   error
}

type balanceCall struct {
	rpcURL  string
	address string
	l2type  int
}

func (c *recordingBalanceClient) BalanceAt(ctx context.Context, rpcURL, address string, l2type int) (*big.Int, error) {
	c.calls = append(c.calls, balanceCall{rpcURL: rpcURL, address: address, l2type: l2type})
	if c.err != nil {
		return nil, c.err
	}
	if c.wei == nil {
		return big.NewInt(0), nil
	}
	return new(big.Int).Set(c.wei), nil
}

func writeServers(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write servers: %v", err)
	}
	return path
}

func TestRun_SelectsMatchingOPChainAndSamplesTenWallets(t *testing.T) {
	path := writeServers(t, `[
  {"ip":"10.0.0.1","serviceType":"op","name":"tps-ydyl-op-1"},
  {"ip":"10.0.0.2","serviceType":"cdk","name":"tps-ydyl-cdk-1"}
]`)
	fetcher := stubSummaryFetcher{byIP: map[string]*ydylconsolesdk.SummaryResultResponse{
		"10.0.0.1": {
			L2_RPC_URL:  "http://127.0.0.1/l2rpc",
			L2_CHAIN_ID: "10000",
		},
	}}
	balances := &recordingBalanceClient{wei: big.NewInt(42)}

	got, err := Run(context.Background(), Params{
		ServersPath: path,
		L2Type:      1,
		MaxIndex:    1000,
	}, fetcher, balances)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Name != "tps-ydyl-op-1" {
		t.Fatalf("name=%q want tps-ydyl-op-1", got.Name)
	}
	if got.L2Type != 1 {
		t.Fatalf("l2type=%d want 1", got.L2Type)
	}
	if got.ChainID != 10000 {
		t.Fatalf("chainID=%d want 10000", got.ChainID)
	}
	if got.RPC != "http://10.0.0.1/l2rpc" {
		t.Fatalf("rpc=%q want rewritten host", got.RPC)
	}
	if len(got.Wallets) != SampleCount {
		t.Fatalf("wallets=%d want %d", len(got.Wallets), SampleCount)
	}
	if len(balances.calls) != SampleCount {
		t.Fatalf("balance calls=%d want %d", len(balances.calls), SampleCount)
	}

	seen := map[string]struct{}{}
	max := big.NewInt(1000)
	for _, w := range got.Wallets {
		if w.Index == nil || w.Index.Sign() < 0 || w.Index.Cmp(max) >= 0 {
			t.Fatalf("index out of range: %v", w.Index)
		}
		key := w.Index.String()
		if _, ok := seen[key]; ok {
			t.Fatalf("duplicate index %s", key)
		}
		seen[key] = struct{}{}
		if w.PrivateKey == "" || w.Address == "" {
			t.Fatal("missing privateKey or address")
		}
		if w.BalanceWei == nil || w.BalanceWei.Cmp(big.NewInt(42)) != 0 {
			t.Fatalf("balance=%v want 42", w.BalanceWei)
		}
		if balances.calls[0].l2type != 1 {
			t.Fatalf("balance l2type=%d want 1", balances.calls[0].l2type)
		}
	}
}

func TestRun_XJSTUsesGroupIDFromNameAndIgnoresNonNode1(t *testing.T) {
	path := writeServers(t, `[
  {"ip":"10.0.0.8","serviceType":"xjst","name":"tps-ydyl-xjst-1-2"},
  {"ip":"10.0.0.7","serviceType":"xjst","name":"tps-ydyl-xjst-1-1"}
]`)
	fetcher := stubSummaryFetcher{byIP: map[string]*ydylconsolesdk.SummaryResultResponse{
		"10.0.0.7": {
			L2_RPC_URL:  "http://localhost:30010",
			L2_CHAIN_ID: "999",
		},
	}}
	balances := &recordingBalanceClient{wei: big.NewInt(1)}

	got, err := Run(context.Background(), Params{
		ServersPath: path,
		L2Type:      2,
		MaxIndex:    100,
	}, fetcher, balances)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Name != "tps-ydyl-xjst-1-1" {
		t.Fatalf("name=%q want node-1", got.Name)
	}
	if got.GroupID != 1 {
		t.Fatalf("groupID=%d want 1", got.GroupID)
	}
	if got.RPC != "http://10.0.0.7:30010" {
		t.Fatalf("rpc=%q", got.RPC)
	}
	if !strings.HasPrefix(strings.ToLower(got.Wallets[0].Address), "0x1") {
		t.Fatalf("xjst address should start with 0x1, got %s", got.Wallets[0].Address)
	}
	if balances.calls[0].l2type != 2 {
		t.Fatalf("balance l2type=%d want 2", balances.calls[0].l2type)
	}
}

func TestRun_FailsWhenNoMatchingChain(t *testing.T) {
	path := writeServers(t, `[
  {"ip":"10.0.0.1","serviceType":"op","name":"tps-ydyl-op-1"}
]`)
	_, err := Run(context.Background(), Params{
		ServersPath: path,
		L2Type:      2,
		MaxIndex:    100,
	}, stubSummaryFetcher{}, &recordingBalanceClient{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "l2type=2") {
		t.Fatalf("error should mention l2type, got %v", err)
	}
}

func TestRun_UsesRPCURLOverrideWithoutRewriting(t *testing.T) {
	path := writeServers(t, `[
  {"ip":"10.0.0.1","serviceType":"op","name":"tps-ydyl-op-1"}
]`)
	fetcher := stubSummaryFetcher{byIP: map[string]*ydylconsolesdk.SummaryResultResponse{
		"10.0.0.1": {
			L2_RPC_URL:  "http://127.0.0.1/l2rpc",
			L2_CHAIN_ID: "10000",
		},
	}}
	balances := &recordingBalanceClient{wei: big.NewInt(1)}
	override := "http://127.0.0.1/custom"

	got, err := Run(context.Background(), Params{
		ServersPath: path,
		L2Type:      1,
		MaxIndex:    100,
		RPCURL:      override,
	}, fetcher, balances)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.RPC != override {
		t.Fatalf("rpc=%q want override %q", got.RPC, override)
	}
	if len(balances.calls) != SampleCount {
		t.Fatalf("balance calls=%d want %d", len(balances.calls), SampleCount)
	}
	for i, c := range balances.calls {
		if c.rpcURL != override {
			t.Fatalf("call %d rpcURL=%q want %q", i, c.rpcURL, override)
		}
	}
}

func TestRun_OverrideAllowsEmptySummaryRPC(t *testing.T) {
	path := writeServers(t, `[
  {"ip":"10.0.0.1","serviceType":"op","name":"tps-ydyl-op-1"}
]`)
	fetcher := stubSummaryFetcher{byIP: map[string]*ydylconsolesdk.SummaryResultResponse{
		"10.0.0.1": {
			L2_CHAIN_ID: "10000",
		},
	}}
	override := "http://10.0.0.9/l2rpc"

	got, err := Run(context.Background(), Params{
		ServersPath: path,
		L2Type:      1,
		MaxIndex:    100,
		RPCURL:      override,
	}, fetcher, &recordingBalanceClient{wei: big.NewInt(1)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.RPC != override {
		t.Fatalf("rpc=%q want %q", got.RPC, override)
	}
}

func TestRun_BlankRPCURLFallsBackToRewrittenSummary(t *testing.T) {
	path := writeServers(t, `[
  {"ip":"10.0.0.1","serviceType":"op","name":"tps-ydyl-op-1"}
]`)
	fetcher := stubSummaryFetcher{byIP: map[string]*ydylconsolesdk.SummaryResultResponse{
		"10.0.0.1": {
			L2_RPC_URL:  "http://127.0.0.1/l2rpc",
			L2_CHAIN_ID: "10000",
		},
	}}

	got, err := Run(context.Background(), Params{
		ServersPath: path,
		L2Type:      1,
		MaxIndex:    100,
		RPCURL:      "   ",
	}, fetcher, &recordingBalanceClient{wei: big.NewInt(1)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.RPC != "http://10.0.0.1/l2rpc" {
		t.Fatalf("rpc=%q want rewritten host", got.RPC)
	}
}

func TestRun_FailsWhenMaxIndexTooSmall(t *testing.T) {
	path := writeServers(t, `[
  {"ip":"10.0.0.1","serviceType":"op","name":"tps-ydyl-op-1"}
]`)
	_, err := Run(context.Background(), Params{
		ServersPath: path,
		L2Type:      1,
		MaxIndex:    9,
	}, stubSummaryFetcher{byIP: map[string]*ydylconsolesdk.SummaryResultResponse{
		"10.0.0.1": {L2_RPC_URL: "http://127.0.0.1/l2rpc", L2_CHAIN_ID: "1"},
	}}, &recordingBalanceClient{})
	if err == nil {
		t.Fatal("expected max-index error")
	}
	if !strings.Contains(err.Error(), "max-index") {
		t.Fatalf("error should mention max-index, got %v", err)
	}
}

func TestFormatResult_IncludesMetadataAndColumns(t *testing.T) {
	out := FormatResult(&Result{
		Name:    "tps-ydyl-op-1",
		L2Type:  1,
		ChainID: 10000,
		RPC:     "http://10.0.0.1/l2rpc",
		Wallets: []Wallet{{
			Index:      big.NewInt(7),
			PrivateKey: "0xabc",
			Address:    "0xdef",
			BalanceWei: big.NewInt(9),
		}},
	})
	for _, want := range []string{
		"name=tps-ydyl-op-1",
		"l2type=1",
		"chainID=10000",
		"rpc=http://10.0.0.1/l2rpc",
		"index",
		"privateKey",
		"address",
		"balanceWei",
		"7",
		"0xabc",
		"0xdef",
		"9",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("FormatResult missing %q in:\n%s", want, out)
		}
	}
}

func TestJSONRPCBalanceClient_UsesEthGetBalanceForEVM(t *testing.T) {
	var method, block string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		method = body.Method
		if len(body.Params) > 1 {
			_ = json.Unmarshal(body.Params[1], &block)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0xa"}`))
	}))
	defer srv.Close()

	got, err := JSONRPCBalanceClient{}.BalanceAt(context.Background(), srv.URL, "0xfc737023702a09c01260252d853033ccaa587b5d", 1)
	if err != nil {
		t.Fatalf("BalanceAt: %v", err)
	}
	if method != "eth_getBalance" {
		t.Fatalf("method=%q want eth_getBalance", method)
	}
	if block != "latest" {
		t.Fatalf("block=%q want latest", block)
	}
	if got.Cmp(big.NewInt(10)) != 0 {
		t.Fatalf("balance=%s want 10", got)
	}
}

func TestRun_CoreSpaceUsesRPCAndChainIDWithoutServers(t *testing.T) {
	balances := &recordingBalanceClient{wei: big.NewInt(7)}
	rpcURL := "http://52.12.7.189/cspace"

	got, err := Run(context.Background(), Params{
		L2Type:   3,
		MaxIndex: 100,
		RPCURL:   rpcURL,
		ChainID:  7654,
	}, nil, balances)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Name != "core" {
		t.Fatalf("name=%q want core", got.Name)
	}
	if got.L2Type != 3 {
		t.Fatalf("l2type=%d want 3", got.L2Type)
	}
	if got.ChainID != 7654 {
		t.Fatalf("chainID=%d want 7654", got.ChainID)
	}
	if got.RPC != rpcURL {
		t.Fatalf("rpc=%q want %q", got.RPC, rpcURL)
	}
	if len(got.Wallets) != SampleCount {
		t.Fatalf("wallets=%d want %d", len(got.Wallets), SampleCount)
	}
	if len(balances.calls) != SampleCount {
		t.Fatalf("balance calls=%d want %d", len(balances.calls), SampleCount)
	}
	for i, w := range got.Wallets {
		if !strings.HasPrefix(w.Address, "net7654:") {
			t.Fatalf("wallet %d address=%q want net7654: prefix", i, w.Address)
		}
		if balances.calls[i].rpcURL != rpcURL {
			t.Fatalf("call %d rpcURL=%q want %q", i, balances.calls[i].rpcURL, rpcURL)
		}
		if balances.calls[i].l2type != 3 {
			t.Fatalf("call %d l2type=%d want 3", i, balances.calls[i].l2type)
		}
		if balances.calls[i].address != w.Address {
			t.Fatalf("call %d address=%q want %q", i, balances.calls[i].address, w.Address)
		}
	}
}

func TestRun_CoreSpaceRequiresRPCURL(t *testing.T) {
	_, err := Run(context.Background(), Params{
		L2Type:   3,
		MaxIndex: 100,
		ChainID:  7654,
	}, nil, &recordingBalanceClient{})
	if err == nil {
		t.Fatal("expected rpc-url error")
	}
	if !strings.Contains(err.Error(), "rpc-url") {
		t.Fatalf("error should mention rpc-url, got %v", err)
	}
}

func TestRun_CoreSpaceRequiresChainID(t *testing.T) {
	_, err := Run(context.Background(), Params{
		L2Type:   3,
		MaxIndex: 100,
		RPCURL:   "http://127.0.0.1/cspace",
		ChainID:  0,
	}, nil, &recordingBalanceClient{})
	if err == nil {
		t.Fatal("expected chainID error")
	}
	if !strings.Contains(err.Error(), "chainID") {
		t.Fatalf("error should mention chainID, got %v", err)
	}
}

func TestJSONRPCBalanceClient_UsesCfxGetBalanceForCore(t *testing.T) {
	var method, epoch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		method = body.Method
		if len(body.Params) > 1 {
			_ = json.Unmarshal(body.Params[1], &epoch)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	defer srv.Close()

	got, err := JSONRPCBalanceClient{}.BalanceAt(context.Background(), srv.URL, "net7654:aan7uwsne58wxrdz7tbdmh842vs55mvuzp4atx32zx", 3)
	if err != nil {
		t.Fatalf("BalanceAt: %v", err)
	}
	if method != "cfx_getBalance" {
		t.Fatalf("method=%q want cfx_getBalance", method)
	}
	if epoch != "latest_state" {
		t.Fatalf("epoch=%q want latest_state", epoch)
	}
	if got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("balance=%s want 1", got)
	}
}

func TestJSONRPCBalanceClient_UsesCfxGetBalanceForXJST(t *testing.T) {
	var method, epoch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		method = body.Method
		if len(body.Params) > 1 {
			_ = json.Unmarshal(body.Params[1], &epoch)
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	defer srv.Close()

	got, err := JSONRPCBalanceClient{}.BalanceAt(context.Background(), srv.URL, "0x1d22176670f087456f2760405469b25917eed45b", 2)
	if err != nil {
		t.Fatalf("BalanceAt: %v", err)
	}
	if method != "cfx_getBalance" {
		t.Fatalf("method=%q want cfx_getBalance", method)
	}
	if epoch != "latest_state" {
		t.Fatalf("epoch=%q want latest_state", epoch)
	}
	if got.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("balance=%s want 1", got)
	}
}

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/config"
	"custom-reth-chain/explorer/backend/internal/evm"
	"custom-reth-chain/explorer/backend/internal/sigs"
	"custom-reth-chain/explorer/backend/internal/testpg"
)

func testServer(t *testing.T) (*httptest.Server, func(string, ...any)) {
	t.Helper()
	pool, url := testpg.New(t)
	cfg := &config.Config{Role: config.RoleServer, DatabaseURL: url, ChainRPCURL: "http://127.0.0.1:1", ChainID: 1,
		RateLimitPerMin: 1000, VerifyLimitPerHour: 10, CallLimitPerMin: 10, TrustedProxyIP: "127.0.0.1"}
	s := New(cfg, pool, chainrpc.New(cfg.ChainRPCURL, chainrpc.Options{Retries: -1, Timeout: time.Second}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	return srv, exec
}

func getMap(t *testing.T, url string, out any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestSignatureEndpoints(t *testing.T) {
	srv, exec := testServer(t)
	base := srv.URL + "/api/v2/signatures"
	// A verified contract whose ABI has signatures no bundled list knows.
	exec(`INSERT INTO verified_contracts (address, name, compiler_version, match_type, abi, sources, verified_via)
		VALUES ('0x00000000000000000000000000000000000000aa', 'Custom', 'v0.8.28+commit.7893614a', 'full', $1, '{}', 'standard-input')`,
		`[{"type":"function","name":"customThing","inputs":[{"name":"a","type":"uint256"},{"name":"b","type":"tuple[]","components":[{"name":"x","type":"address"}]}]},
		  {"type":"event","name":"CustomHappened","inputs":[{"name":"who","type":"address","indexed":true}]},
		  {"type":"error","name":"CustomFailure","inputs":[{"name":"code","type":"uint8"}]}]`)

	customFn := sigs.KeyOf(sigs.Function, "customThing(uint256,(address)[])")
	var got map[string][]string
	if code := getMap(t, base+"/functions?selectors=0xa9059cbb,"+customFn+",0xdeadbeef", &got); code != 200 {
		t.Fatalf("functions: %d", code)
	}
	if len(got) != 2 || got["0xa9059cbb"][0] != "transfer(address,uint256)" || got[customFn][0] != "customThing(uint256,(address)[])" {
		t.Errorf("functions %v", got)
	}
	if _, ok := got["0xdeadbeef"]; ok {
		t.Error("unknown selector answered")
	}
	ev := sigs.KeyOf(sigs.Event, "CustomHappened(address)")
	got = nil
	getMap(t, base+"/events?topics="+evm.TopicTransfer+","+ev, &got)
	if got[evm.TopicTransfer][0] != "Transfer(address,address,uint256)" || got[ev][0] != "CustomHappened(address)" {
		t.Errorf("events %v", got)
	}
	errKey := sigs.KeyOf(sigs.Error, "CustomFailure(uint8)")
	got = nil
	getMap(t, base+"/errors?selectors=0x08c379a0,"+errKey, &got)
	if got["0x08c379a0"][0] != "Error(string)" || got[errKey][0] != "CustomFailure(uint8)" {
		t.Errorf("errors %v", got)
	}
	// Keys are answered lowercase whatever the case asked.
	got = nil
	getMap(t, base+"/functions?selectors=0xA9059CBB", &got)
	if len(got["0xa9059cbb"]) != 1 {
		t.Errorf("uppercase selector %v", got)
	}

	var msg map[string]string
	many := strings.TrimSuffix(strings.Repeat("0xa9059cbb,", 51), ",")
	for _, bad := range []string{"/functions", "/functions?selectors=0x1234", "/events?topics=0xa9059cbb", "/functions?selectors=" + many} {
		if code := getMap(t, base+bad, &msg); code != 400 || msg["message"] == "" {
			t.Errorf("%s: %d %v", bad, code, msg)
		}
	}
	if code := getMap(t, base+"/functions?selectors="+strings.TrimSuffix(strings.Repeat("0xa9059cbb,", 50), ","), nil); code != 200 {
		t.Errorf("50 selectors refused: %d", code)
	}
}

func TestTransactionsChart(t *testing.T) {
	srv, exec := testServer(t)
	now := time.Now().UTC()
	day := func(back int) time.Time {
		d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -back)
		return d
	}
	n := 0
	block := func(ts time.Time, txs int) {
		exec(`INSERT INTO blocks (number, hash, parent_hash, miner, timestamp, gas_used, gas_limit, tx_count, state_root,
			transactions_root, receipts_root, raw) VALUES ($1, $2, '0x', '0x', $3, 0, 0, $4, '0x', '0x', '0x', '{}')`,
			n, fmt.Sprintf("0x%064x", n), ts, txs)
		n++
	}
	block(day(0).Add(time.Minute), 3)                 // today, just after UTC midnight
	block(day(0).Add(23*time.Hour+59*time.Minute), 2) // today, just before the next midnight would be
	block(day(2).Add(12*time.Hour), 5)                // two days ago
	block(day(1).Add(-time.Second), 7)                // two days ago at 23:59:59 (not yesterday)
	block(day(100), 9)                                // outside every window
	if now.Hour() == 23 && now.Minute() == 59 {
		t.Skip("too close to UTC midnight for a stable today")
	}

	var resp struct {
		Items []chartPoint `json:"items"`
	}
	if code := getMap(t, srv.URL+"/api/v2/stats/charts/transactions?days=3", &resp); code != 200 {
		t.Fatalf("chart: %d", code)
	}
	want := []chartPoint{{day(2).Format("2006-01-02"), 12}, {day(1).Format("2006-01-02"), 0}, {day(0).Format("2006-01-02"), 5}}
	if len(resp.Items) != 3 {
		t.Fatalf("items %v", resp.Items)
	}
	for i, w := range want {
		if resp.Items[i] != w {
			t.Errorf("day %d: %+v, want %+v", i, resp.Items[i], w)
		}
	}
	resp.Items = nil
	getMap(t, srv.URL+"/api/v2/stats/charts/transactions", &resp)
	if len(resp.Items) != 14 || resp.Items[13].Date != day(0).Format("2006-01-02") {
		t.Errorf("default window: %d items", len(resp.Items))
	}
	getMap(t, srv.URL+"/api/v2/stats/charts/transactions?days=90", &resp)
	if len(resp.Items) != 90 {
		t.Errorf("90 days: %d items", len(resp.Items))
	}
	for _, bad := range []string{"0", "91", "x"} {
		if code := getMap(t, srv.URL+"/api/v2/stats/charts/transactions?days="+bad, nil); code != 400 {
			t.Errorf("days=%s: %d", bad, code)
		}
	}
}

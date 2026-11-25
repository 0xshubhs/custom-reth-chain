package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeNode stands in for the reth node: it records every body it receives and
// answers each call with {"result": "<method>"}, or the chain tip for
// eth_blockNumber.
type fakeNode struct {
	mu     sync.Mutex
	bodies []string
	head   string
}

func (f *fakeNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.bodies = append(f.bodies, string(b))
	head := f.head
	f.mu.Unlock()
	answer := func(raw json.RawMessage) json.RawMessage {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(raw, &req)
		result := req.Method
		if req.Method == "eth_blockNumber" {
			result = head
		}
		return resultResponse(req.ID, json.RawMessage(`"`+result+`"`))
	}
	// The node sends CORS for everyone; the gateway must not pass that on.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(strings.TrimSpace(string(b)), "[") {
		var items []json.RawMessage
		_ = json.Unmarshal(b, &items)
		out := make([]json.RawMessage, len(items))
		for i, it := range items {
			out[i] = answer(it)
		}
		enc, _ := json.Marshal(out)
		_, _ = w.Write(enc)
		return
	}
	_, _ = w.Write(answer(b))
}

// calls returns the non-eth_blockNumber bodies the node received (the
// gateway's own head lookups are filtered out).
func (f *fakeNode) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, b := range f.bodies {
		if strings.Contains(b, `"id":1,"method":"eth_blockNumber"`) {
			continue
		}
		out = append(out, b)
	}
	return out
}

func testGateway(t *testing.T, env map[string]string) (*Gateway, *fakeNode, *httptest.Server) {
	t.Helper()
	node := &fakeNode{head: "0x30d40"} // 200000
	upstream := httptest.NewServer(node)
	t.Cleanup(upstream.Close)
	if env == nil {
		env = map[string]string{}
	}
	env["RPC_GATEWAY_UPSTREAM"] = upstream.URL
	cfg, err := loadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	g := newGateway(cfg)
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return g, node, srv
}

func post(t *testing.T, url, body string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestNonPublicNamespacesNeverReachTheNode(t *testing.T) {
	_, node, srv := testGateway(t, nil)
	for _, m := range []string{
		"debug_traceTransaction", "debug_traceCall", "debug_traceBlockByNumber",
		"trace_block", "txpool_status", "txpool_content", "admin_nodeInfo",
		"personal_sign", "meow_chainConfig", "clique_propose", "ots_getInternalOperations",
		"rpc_modules", "miner_start",
		"eth_sendTransaction", "eth_sign", "eth_signTransaction", "eth_signTypedData_v4",
	} {
		resp, body := post(t, srv.URL, `{"jsonrpc":"2.0","id":7,"method":"`+m+`","params":[]}`, nil)
		if resp.StatusCode != 200 || !strings.Contains(body, `"code":-32601`) || !strings.Contains(body, `"id":7`) {
			t.Errorf("%s: got %d %s, want a -32601 answer for id 7", m, resp.StatusCode, body)
		}
	}
	if got := node.calls(); len(got) != 0 {
		t.Fatalf("the node received refused calls: %v", got)
	}
}

func TestEthAccountsIsEmptyWithoutAskingTheNode(t *testing.T) {
	_, node, srv := testGateway(t, nil)
	_, body := post(t, srv.URL, `{"jsonrpc":"2.0","id":1,"method":"eth_accounts","params":[]}`, nil)
	if !strings.Contains(body, `"result":[]`) {
		t.Fatalf("eth_accounts: got %s, want an empty list", body)
	}
	if len(node.calls()) != 0 {
		t.Fatal("eth_accounts reached the node")
	}
}

func TestPublicCallsAreForwardedReEncoded(t *testing.T) {
	_, node, srv := testGateway(t, nil)
	resp, body := post(t, srv.URL, `{"jsonrpc":"2.0","id":"a","method":"eth_chainId","params":[]}`, nil)
	if resp.StatusCode != 200 || !strings.Contains(body, `"result":"eth_chainId"`) {
		t.Fatalf("eth_chainId: got %d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("the node's CORS header leaked through")
	}
	// Smuggling a second method past the check with a duplicate or
	// differently-cased key: the node must only ever see what was checked.
	_, body = post(t, srv.URL, `{"jsonrpc":"2.0","id":2,"method":"eth_chainId","method":"debug_traceCall","params":[]}`, nil)
	if !strings.Contains(body, `-32601`) {
		t.Fatalf("duplicate-key smuggle: got %s, want refused", body)
	}
	_, _ = post(t, srv.URL, `{"jsonrpc":"2.0","id":3,"Method":"debug_traceCall","method":"eth_blockNumber","params":[]}`, nil)
	calls := node.calls()
	last := calls[len(calls)-1]
	if strings.Contains(last, "debug") || strings.Contains(last, "Method") {
		t.Fatalf("the node received an un-normalised request: %s", last)
	}
	for _, c := range calls {
		if strings.Contains(c, "debug_") {
			t.Fatalf("a debug call reached the node: %s", c)
		}
	}
}

func TestBatches(t *testing.T) {
	_, node, srv := testGateway(t, map[string]string{"RPC_GATEWAY_MAX_BATCH": "5"})

	six := `[` + strings.Repeat(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId"},`, 5) +
		`{"jsonrpc":"2.0","id":6,"method":"eth_chainId"}]`
	resp, _ := post(t, srv.URL, six, nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("batch of 6 with a cap of 5: got %d, want 413", resp.StatusCode)
	}
	if len(node.calls()) != 0 {
		t.Fatal("an oversized batch reached the node")
	}

	mixed := `[{"jsonrpc":"2.0","id":1,"method":"eth_chainId"},
	           {"jsonrpc":"2.0","id":2,"method":"debug_traceCall"},
	           {"jsonrpc":"2.0","id":3,"method":"eth_accounts"},
	           {"jsonrpc":"2.0","id":4,"method":"net_version"}]`
	resp, body := post(t, srv.URL, mixed, nil)
	var answers []map[string]any
	if err := json.Unmarshal([]byte(body), &answers); err != nil || resp.StatusCode != 200 {
		t.Fatalf("mixed batch: %d %s", resp.StatusCode, body)
	}
	if len(answers) != 4 {
		t.Fatalf("mixed batch: want 4 answers, got %s", body)
	}
	byID := map[float64]map[string]any{}
	for _, a := range answers {
		byID[a["id"].(float64)] = a
	}
	if byID[1]["result"] != "eth_chainId" || byID[4]["result"] != "net_version" || byID[2]["error"] == nil {
		t.Fatalf("mixed batch answers wrong: %s", body)
	}
	if res, ok := byID[3]["result"].([]any); !ok || len(res) != 0 {
		t.Fatalf("eth_accounts in a batch: %v", byID[3])
	}
	calls := node.calls()
	if len(calls) != 1 || strings.Contains(calls[0], "debug") || strings.Contains(calls[0], "eth_accounts") {
		t.Fatalf("the node should get one batch of the two public calls, got %v", calls)
	}
}

func TestGetLogsRange(t *testing.T) {
	_, node, srv := testGateway(t, nil) // unfiltered cap 2000, head 200000

	cases := []struct {
		name    string
		filter  string
		allowed bool
	}{
		{"unfiltered whole chain", `{"fromBlock":"0x0","toBlock":"latest"}`, false},
		{"unfiltered defaults to latest", `{"fromBlock":"earliest"}`, false},
		{"topics only, whole chain", `{"fromBlock":"0x0","toBlock":"latest","topics":["0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"]}`, false},
		{"unfiltered small range", `{"fromBlock":"0x30000","toBlock":"0x307cf"}`, true},
		{"unfiltered last 100 blocks by tag", `{"fromBlock":"0x30cdd","toBlock":"latest"}`, true},
		{"unfiltered 2001 blocks", `{"fromBlock":"0x0","toBlock":"0x7d0"}`, false},
		{"address, whole chain (the engines do this)", `{"fromBlock":"0x0","toBlock":"latest","address":"0x5FbDB2315678afecb367f032d93F642f64180aa3"}`, true},
		{"address list, whole chain", `{"fromBlock":"0x0","address":["0x5FbDB2315678afecb367f032d93F642f64180aa3"]}`, true},
		{"empty address list counts as none", `{"fromBlock":"0x0","toBlock":"latest","address":[]}`, false},
		{"single block by hash", `{"blockHash":"0x9b83c12c69edb74f6c8dd5d052765c1adf940e320bd1291696e6fa07829eee71"}`, true},
		{"junk block number", `{"fromBlock":"zero","toBlock":"latest"}`, false},
	}
	for _, tc := range cases {
		before := len(node.calls())
		_, body := post(t, srv.URL, `{"jsonrpc":"2.0","id":1,"method":"eth_getLogs","params":[`+tc.filter+`]}`, nil)
		forwarded := len(node.calls()) > before
		if forwarded != tc.allowed {
			t.Errorf("%s: forwarded=%v, want %v (answer %s)", tc.name, forwarded, tc.allowed, body)
		}
		if !tc.allowed && !strings.Contains(body, `"error"`) {
			t.Errorf("%s: refused without an error answer: %s", tc.name, body)
		}
	}
	// eth_newFilter is held to the same rule, or eth_getFilterLogs would be
	// a way around it.
	_, body := post(t, srv.URL, `{"jsonrpc":"2.0","id":1,"method":"eth_newFilter","params":[{"fromBlock":"0x0","toBlock":"latest"}]}`, nil)
	if !strings.Contains(body, `-32005`) {
		t.Fatalf("eth_newFilter over the whole chain: %s", body)
	}
}

func TestCORS(t *testing.T) {
	_, node, srv := testGateway(t, nil)

	resp, _ := post(t, srv.URL, `{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`,
		map[string]string{"Origin": "http://localhost:3000"})
	if resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatalf("our own site: ACAO = %q", resp.Header.Get("Access-Control-Allow-Origin"))
	}

	// Another website: answered (the origin is not a permission, as in geth)
	// but without CORS headers, so its visitors' browsers cannot read it.
	resp, body := post(t, srv.URL, `{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`,
		map[string]string{"Origin": "https://evil.example"})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "result") || resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("foreign site: got %d ACAO=%q body=%s, want 200 without CORS", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"), body)
	}
	// And its preflight gets no CORS headers, so the browser never sends the call.
	fp, _ := http.NewRequest(http.MethodOptions, srv.URL, nil)
	fp.Header.Set("Origin", "https://evil.example")
	fp.Header.Set("Access-Control-Request-Method", "POST")
	fp.Header.Set("Access-Control-Request-Headers", "content-type")
	fpr, err := http.DefaultClient.Do(fp)
	if err != nil {
		t.Fatal(err)
	}
	fpr.Body.Close()
	if fpr.Header.Get("Access-Control-Allow-Origin") != "" || fpr.Header.Get("Access-Control-Allow-Methods") != "" {
		t.Fatalf("preflight from a foreign site got CORS headers: %v", fpr.Header)
	}

	// Wallet extensions are not websites: MetaMask and friends get CORS too.
	for _, ext := range []string{
		"chrome-extension://nkbihfbeogaeaoehlefnkodbefgpgknn",
		"moz-extension://6b6a7c1e-0f1d-4c3b-9b1a-2f7f5d1c0e11",
		"safari-web-extension://4b1c8e2a-7d3f-4e5a-9c6b-1a2b3c4d5e6f",
	} {
		resp, body := post(t, srv.URL, `{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`,
			map[string]string{"Origin": ext})
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Access-Control-Allow-Origin") != ext {
			t.Fatalf("wallet extension %s: got %d ACAO=%q body=%s", ext, resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"), body)
		}
	}
	// A bare scheme, or a site dressed up to look like one, gets no CORS.
	for _, fake := range []string{"chrome-extension://", "https://chrome-extension.example", "null"} {
		resp, _ := post(t, srv.URL, `{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`,
			map[string]string{"Origin": fake})
		if resp.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("origin %q got CORS headers", fake)
		}
	}
	// The method allowlist is the same for every origin, our own sites included.
	for _, m := range []string{"eth_sendTransaction", "debug_traceTransaction", "admin_nodeInfo"} {
		_, body = post(t, srv.URL, `{"jsonrpc":"2.0","id":1,"method":"`+m+`","params":[]}`,
			map[string]string{"Origin": "http://localhost:3000"})
		if !strings.Contains(body, `"error"`) || strings.Contains(body, `"result"`) {
			t.Fatalf("%s answered for our own site: %s", m, body)
		}
	}
	_, body = post(t, srv.URL, `{"jsonrpc":"2.0","id":1,"method":"eth_accounts"}`,
		map[string]string{"Origin": "http://localhost:3000"})
	if !strings.Contains(body, `"result":[]`) {
		t.Fatalf("eth_accounts must list no accounts: %s", body)
	}
	_ = node

	req, _ := http.NewRequest(http.MethodOptions, srv.URL, nil)
	req.Header.Set("Origin", "http://127.0.0.1:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type")
	pre, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	pre.Body.Close()
	if pre.StatusCode != http.StatusNoContent ||
		pre.Header.Get("Access-Control-Allow-Origin") != "http://127.0.0.1:3000" ||
		!strings.Contains(pre.Header.Get("Access-Control-Allow-Methods"), "POST") {
		t.Fatalf("preflight from our site: %d %v", pre.StatusCode, pre.Header)
	}

	// No Origin at all (an engine, a mobile wallet, curl): allowed.
	resp, body = post(t, srv.URL, `{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`, nil)
	if resp.StatusCode != 200 || !strings.Contains(body, "result") {
		t.Fatalf("server-side caller: %d %s", resp.StatusCode, body)
	}
}

func TestBodyLimitAndMethod(t *testing.T) {
	_, _, srv := testGateway(t, map[string]string{"RPC_GATEWAY_MAX_BODY_BYTES": "1024"})
	big := `{"jsonrpc":"2.0","id":1,"method":"eth_call","params":[{"data":"0x` + strings.Repeat("ab", 2048) + `"},"latest"]}`
	resp, _ := post(t, srv.URL, big, nil)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: got %d", resp.StatusCode)
	}
	r, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET: got %d", r.StatusCode)
	}
}

func TestPerIPRateLimit(t *testing.T) {
	g, _, srv := testGateway(t, map[string]string{
		"RPC_GATEWAY_RATE_PER_IP":  "1",
		"RPC_GATEWAY_BURST_PER_IP": "3",
		"RPC_GATEWAY_EXEMPT_IPS":   "203.0.113.9",
	})
	now := time.Unix(1_700_000_000, 0)
	g.limiter.now = func() time.Time { return now }

	call := func(ip string) int {
		// httptest connects from 127.0.0.1, which is a trusted proxy, so the
		// forwarded header names the client exactly as nginx would.
		resp, _ := post(t, srv.URL, `{"jsonrpc":"2.0","id":1,"method":"eth_chainId"}`,
			map[string]string{"CF-Connecting-IP": ip})
		return resp.StatusCode
	}
	for i := 0; i < 3; i++ {
		if c := call("198.51.100.1"); c != 200 {
			t.Fatalf("call %d inside the burst: %d", i+1, c)
		}
	}
	if c := call("198.51.100.1"); c != http.StatusTooManyRequests {
		t.Fatalf("4th call over a burst of 3: got %d, want 429", c)
	}
	if c := call("198.51.100.2"); c != 200 {
		t.Fatalf("another client is not affected: %d", c)
	}
	for i := 0; i < 10; i++ {
		if c := call("203.0.113.9"); c != 200 {
			t.Fatalf("exempt address limited on call %d: %d", i+1, c)
		}
	}
	now = now.Add(2 * time.Second)
	if c := call("198.51.100.1"); c != 200 {
		t.Fatalf("after refilling: %d", c)
	}

	// A batch costs one token per call: with one token left, a batch of two
	// is refused although it is a single HTTP request.
	_ = call("198.51.100.3")
	_ = call("198.51.100.3")
	resp, _ := post(t, srv.URL, `[{"jsonrpc":"2.0","id":1,"method":"eth_chainId"},{"jsonrpc":"2.0","id":2,"method":"eth_chainId"}]`,
		map[string]string{"CF-Connecting-IP": "198.51.100.3"})
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("batch of 2 with 1 token left: %d Retry-After=%q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
}

func TestForwardedIPOnlyFromTrustedPeer(t *testing.T) {
	cfg, err := loadConfig(func(k string) string {
		if k == "RPC_GATEWAY_TRUSTED_PROXIES" {
			return "10.0.0.1"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	g := newGateway(cfg)
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.RemoteAddr = "192.0.2.10:5555"
	r.Header.Set("CF-Connecting-IP", "198.51.100.77")
	if ip := g.clientIP(r); ip != "192.0.2.10" {
		t.Fatalf("an untrusted peer named its own client IP: %s", ip)
	}
	r.RemoteAddr = "10.0.0.1:5555"
	if ip := g.clientIP(r); ip != "198.51.100.77" {
		t.Fatalf("trusted proxy's forwarded IP ignored: %s", ip)
	}
}

func TestWebsocket(t *testing.T) {
	// A websocket node that answers every message with the message it got,
	// so the test can see exactly what was forwarded.
	var mu sync.Mutex
	var got []string
	up := websocket.Upgrader{}
	wsNode := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			_, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			mu.Lock()
			got = append(got, string(msg))
			mu.Unlock()
			_ = c.WriteMessage(websocket.TextMessage, []byte(`{"echo":`+string(msg)+`}`))
		}
	}))
	defer wsNode.Close()

	_, _, srv := testGateway(t, map[string]string{
		"RPC_GATEWAY_UPSTREAM_WS": "ws" + strings.TrimPrefix(wsNode.URL, "http"),
	})
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	// A foreign site cannot open one.
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": {"https://evil.example"}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign-origin websocket: err=%v resp=%v", err, resp)
	}

	// A wallet extension can (MetaMask subscribes over websocket too).
	ext, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": {"chrome-extension://nkbihfbeogaeaoehlefnkodbefgpgknn"}})
	if err != nil {
		t.Fatalf("wallet-extension websocket: %v", err)
	}
	ext.Close()

	c, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": {"http://localhost:3000"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))

	// Refused: answered by the gateway, never forwarded.
	_ = c.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":1,"method":"debug_traceBlockByNumber","params":["0x1"]}`))
	_, msg, err := c.ReadMessage()
	if err != nil || !strings.Contains(string(msg), "-32601") {
		t.Fatalf("debug over websocket: %s %v", msg, err)
	}
	// Mixed batch: answered whole by the gateway.
	_ = c.WriteMessage(websocket.TextMessage, []byte(`[{"jsonrpc":"2.0","id":1,"method":"eth_chainId"},{"jsonrpc":"2.0","id":2,"method":"txpool_content"}]`))
	_, msg, err = c.ReadMessage()
	if err != nil || strings.Contains(string(msg), "echo") || !strings.Contains(string(msg), "-32601") {
		t.Fatalf("mixed batch over websocket: %s %v", msg, err)
	}
	// Allowed: forwarded, re-encoded.
	_ = c.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","id":3,"method":"eth_subscribe","params":["newHeads"]}`))
	_, msg, err = c.ReadMessage()
	if err != nil || !strings.Contains(string(msg), `"echo"`) || !strings.Contains(string(msg), "eth_subscribe") {
		t.Fatalf("eth_subscribe over websocket: %s %v", msg, err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, m := range got {
		if strings.Contains(m, "debug_") || strings.Contains(m, "txpool_") {
			t.Fatalf("the node received a refused websocket call: %s", m)
		}
	}
	if len(got) != 1 {
		t.Fatalf("the node should have received exactly one message, got %v", got)
	}
}

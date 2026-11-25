//go:build integration

// Package itest is the engine's end-to-end test against a throwaway anvil:
//
//	EXPLORER_TEST_PG=postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable \
//	EXPLORER_IT_OZ=/path/to/openzeppelin-contracts \
//	GOTOOLCHAIN=go1.26.11 go test -tags integration ./internal/itest/ -v
//
// It needs anvil, forge, cast and curl on PATH. EXPLORER_IT_OZ defaults to
// ../contracts/lib/openzeppelin-contracts (the vendored tree, not in git).
package itest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"custom-reth-chain/explorer/backend/internal/api"
	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/config"
	"custom-reth-chain/explorer/backend/internal/indexer"
)

const (
	key0     = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80" // anvil account 0
	acct0    = "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266"
	key1     = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	acct1    = "0x70997970c51812dc3a010c7d01b50e0d17dc79c8"
	acct2    = "0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc"
	acct3    = "0x90f79bf6eb2c4f870365e785982e1f101e93b906"
	hookTok  = "it-hook-token"
	chainID  = 31337
	timeout  = 20 * time.Second
	compiler = "v0.8.28+commit.7893614a"
)

type harness struct {
	t       *testing.T
	ctx     context.Context
	rpcURL  string
	wsURL   string
	dbURL   string
	pool    *pgxpool.Pool
	rpc     *chainrpc.Client
	art     string // forge out dir
	log     *slog.Logger
	srcDir  string
	jsonDir string
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func startAnvil(t *testing.T) (string, string) {
	t.Helper()
	port := freePort(t)
	cmd := exec.Command("anvil", "--port", fmt.Sprint(port), "--silent")
	if err := cmd.Start(); err != nil {
		t.Fatalf("anvil: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	c := chainrpc.New(url, chainrpc.Options{Retries: -1})
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.ChainID(context.Background()); err == nil {
			return url, fmt.Sprintf("ws://127.0.0.1:%d", port)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("anvil did not start")
	return "", ""
}

func buildContracts(t *testing.T) (string, string) {
	t.Helper()
	oz := os.Getenv("EXPLORER_IT_OZ")
	if oz == "" {
		oz, _ = filepath.Abs("../../../contracts/lib/openzeppelin-contracts")
	}
	if _, err := os.Stat(filepath.Join(oz, "contracts/token/ERC20/ERC20.sol")); err != nil {
		t.Skipf("OpenZeppelin not found at %s (set EXPLORER_IT_OZ)", oz)
	}
	root, _ := filepath.Abs("testdata/contracts")
	out := t.TempDir()
	cmd := exec.Command("forge", "build", "--root", root, "--contracts", "src", "--out", filepath.Join(out, "out"),
		"--cache-path", filepath.Join(out, "cache"), "--remappings", "@openzeppelin/contracts/="+oz+"/contracts/", "--use", "0.8.28", "--quiet")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("forge build: %v\n%s", err, b)
	}
	return filepath.Join(out, "out"), root
}

func (h *harness) artifact(name string) (bytecode string, abi json.RawMessage) {
	h.t.Helper()
	raw, err := os.ReadFile(filepath.Join(h.art, "Tokens.sol", name+".json"))
	if err != nil {
		h.t.Fatal(err)
	}
	var a struct {
		ABI      json.RawMessage `json:"abi"`
		Bytecode struct {
			Object string `json:"object"`
		} `json:"bytecode"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		h.t.Fatal(err)
	}
	return a.Bytecode.Object, a.ABI
}

func (h *harness) cast(args ...string) string {
	h.t.Helper()
	cmd := exec.Command("cast", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("cast %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

type receipt struct {
	TransactionHash string `json:"transactionHash"`
	ContractAddress string `json:"contractAddress"`
	Status          string `json:"status"`
	BlockNumber     string `json:"blockNumber"`
}

func (h *harness) send(key, to, sig string, args ...string) receipt {
	h.t.Helper()
	a := append([]string{"send", "--json", "--rpc-url", h.rpcURL, "--private-key", key, to, sig}, args...)
	return h.parseReceipt(h.cast(a...))
}

func (h *harness) sendValue(key, to, value, sig string, args ...string) receipt {
	h.t.Helper()
	a := append([]string{"send", "--json", "--rpc-url", h.rpcURL, "--private-key", key, "--value", value, to}, append([]string{sig}, args...)...)
	return h.parseReceipt(h.cast(a...))
}

func (h *harness) deploy(name, ctor string, args ...string) receipt {
	h.t.Helper()
	code, _ := h.artifact(name)
	a := []string{"send", "--json", "--rpc-url", h.rpcURL, "--private-key", key0, "--create", code}
	if ctor != "" {
		a = append(a, ctor)
		a = append(a, args...)
	}
	r := h.parseReceipt(h.cast(a...))
	if r.ContractAddress == "" {
		h.t.Fatalf("deploy %s: no contract address", name)
	}
	r.ContractAddress = strings.ToLower(r.ContractAddress)
	return r
}

func (h *harness) parseReceipt(out string) receipt {
	h.t.Helper()
	var r receipt
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		h.t.Fatalf("receipt: %v\n%s", err, out)
	}
	if r.Status != "0x1" && r.Status != "1" {
		h.t.Fatalf("tx %s failed (%s)", r.TransactionHash, r.Status)
	}
	return r
}

func (h *harness) call(to, sig string, args ...string) string {
	h.t.Helper()
	return h.cast(append([]string{"call", "--rpc-url", h.rpcURL, to, sig}, args...)...)
}

func (h *harness) head() uint64 {
	h.t.Helper()
	n, err := h.rpc.BlockNumber(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *harness) cursor() int64 {
	var n int64 = -2
	_ = h.pool.QueryRow(h.ctx, `SELECT last_block FROM indexer_state`).Scan(&n)
	return n
}

// waitIndexed waits until the cursor reaches the node's head and the stored
// tip is the node's block.
func (h *harness) waitIndexed(what string) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		head := h.head()
		if c := h.cursor(); c == int64(head) {
			nh, _ := h.rpc.BlockHashAt(h.ctx, head)
			var sh string
			_ = h.pool.QueryRow(h.ctx, `SELECT last_hash FROM indexer_state`).Scan(&sh)
			if nh == sh {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatalf("%s: not indexed in %s (cursor %d, head %d)", what, timeout, h.cursor(), h.head())
}

// indexerProc is an in-process indexer with its hook/health server.
type indexerProc struct {
	ix     *indexer.Indexer
	srv    *httptest.Server
	cancel context.CancelFunc
	done   chan error
}

func (h *harness) startIndexer(wsURL string, safety int) *indexerProc {
	h.t.Helper()
	cfg := &config.Config{
		Role: config.RoleIndexer, DatabaseURL: h.dbURL, ChainRPCURL: h.rpcURL, ChainWSURL: wsURL, ChainID: chainID,
		SafetyCheckSeconds: safety, Traces: "required", BackfillConcurrency: 4, IndexerHookToken: hookTok, RPCMaxConcurrent: 4,
	}
	rpc := chainrpc.New(h.rpcURL, chainrpc.Options{Timeout: 10 * time.Second, MaxConcurrent: 4})
	ix := indexer.New(cfg, h.pool, rpc, h.log)
	ctx, cancel := context.WithCancel(h.ctx)
	p := &indexerProc{ix: ix, srv: httptest.NewServer(ix.Handler()), cancel: cancel, done: make(chan error, 1)}
	go func() { p.done <- ix.Run(ctx) }()
	h.t.Cleanup(p.stop)
	return p
}

func (p *indexerProc) stop() {
	if p.cancel == nil {
		return
	}
	p.cancel()
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
	}
	p.srv.Close()
	p.cancel = nil
}

func (p *indexerProc) health(t *testing.T) map[string]any {
	t.Helper()
	resp, err := http.Get(p.srv.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return m
}

func (p *indexerProc) hook(t *testing.T, token string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, p.srv.URL+"/hooks/new-block", nil)
	if token != "" {
		req.Header.Set("X-Hook-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func (h *harness) startAPI(verifierURL, hookURL string) *httptest.Server {
	h.t.Helper()
	cfg := &config.Config{
		Role: config.RoleServer, DatabaseURL: h.dbURL, ChainRPCURL: h.rpcURL, ChainID: chainID, VerifierURL: verifierURL,
		IndexerHookURL: hookURL, IndexerHookToken: hookTok, TrustedProxyIP: "127.0.0.1", RateLimitPerMin: 100000,
		VerifyLimitPerHour: 100, CallLimitPerMin: 1000, NetworkName: "anvil", NativeSymbol: "ETH", PublicRPCURL: h.rpcURL,
		ExplorerPublicURL: "https://explorer.example",
	}
	s := api.New(cfg, h.pool, chainrpc.New(h.rpcURL, chainrpc.Options{Timeout: 5 * time.Second, Retries: 1}), h.log)
	ctx, cancel := context.WithCancel(h.ctx)
	go s.Run(ctx)
	srv := httptest.NewServer(s.Handler())
	h.t.Cleanup(func() { s.Close(); srv.Close(); cancel() })
	return srv
}

func getJSON(t *testing.T, url string, out any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("GET %s: %v\n%s", url, err, body)
		}
	}
	return resp.StatusCode
}

func getRaw(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// readSSE reads one "event: block" from the stream within d.
func readSSE(t *testing.T, url string, trigger func(), d time.Duration) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("stream content type %q", ct)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	triggered := false
	event := ""
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, ": connected") && !triggered {
			triggered = true
			go trigger()
		}
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") && event == "block" {
			return strings.TrimPrefix(line, "data: ")
		}
	}
	t.Fatalf("no block event on the stream within %s", d)
	return ""
}

func run(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

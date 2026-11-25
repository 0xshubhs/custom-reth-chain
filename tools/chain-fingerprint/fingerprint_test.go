package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
)

// fakeChain serves a synthetic chain over JSON-RPC batches.
type fakeChain struct {
	mu        sync.Mutex
	blocks    int
	mutate    func(method string, n uint64, v map[string]any) // edits an answer
	noBlockRc bool                                            // pretend eth_getBlockReceipts does not exist
	inFlight  atomic.Int32
	maxSeen   atomic.Int32
	batches   atomic.Int32
	failFirst atomic.Int32 // fail this many requests with 503 first
}

func h(prefix string, n uint64) string { return fmt.Sprintf("0x%s%062x", prefix, n) }

func (f *fakeChain) block(n uint64, full bool) map[string]any {
	parent := "0x" + strings.Repeat("0", 64)
	if n > 0 {
		parent = h("bb", n-1)
	}
	var txs []any
	if n > 0 {
		txs = append(txs, map[string]any{"hash": h("aa", n), "from": fmt.Sprintf("0x%040x", 0x1000+n%3)})
	}
	b := map[string]any{
		"number": fmt.Sprintf("0x%x", n), "hash": h("bb", n), "parentHash": parent,
		"stateRoot": h("cc", n), "transactionsRoot": h("dd", n), "receiptsRoot": h("ee", n),
		"timestamp": fmt.Sprintf("0x%x", 1700000000+n), "miner": "0x0000000000000000000000000000000000000000",
		"transactions": txs,
	}
	if f.mutate != nil {
		f.mutate("eth_getBlockByNumber", n, b)
	}
	return b
}

func (f *fakeChain) receipts(n uint64) []any {
	if n == 0 {
		return []any{}
	}
	r := map[string]any{
		"transactionHash": h("aa", n), "transactionIndex": "0x0", "blockHash": h("bb", n),
		"blockNumber": fmt.Sprintf("0x%x", n), "from": fmt.Sprintf("0x%040x", 0x1000+n%3), "to": nil,
		"contractAddress": fmt.Sprintf("0x%040x", 0x9000+n), "status": "0x1", "cumulativeGasUsed": "0x5208",
		"gasUsed": "0x5208", "effectiveGasPrice": "0x3b9aca00", "type": "0x0", "logsBloom": "0x00",
		"logs": []any{map[string]any{"address": fmt.Sprintf("0x%040x", 0x9000+n), "topics": []any{h("11", n)}, "data": "0x01",
			"logIndex": "0x0", "transactionIndex": "0x0", "transactionHash": h("aa", n), "removed": false,
			"blockTimestamp": "0x1234"}},
	}
	if f.mutate != nil {
		f.mutate("receipt", n, r)
	}
	return []any{r}
}

func (f *fakeChain) answer(method string, params []json.RawMessage) (any, *rpcError) {
	num := func(i int) uint64 {
		var s string
		json.Unmarshal(params[i], &s)
		v, _ := quantityU64(s)
		return v
	}
	switch method {
	case "eth_chainId":
		return "0x135276b", nil
	case "web3_clientVersion":
		return "fake/v1", nil
	case "eth_blockNumber":
		return fmt.Sprintf("0x%x", f.blocks-1), nil
	case "eth_getBlockByNumber":
		n := num(0)
		if n >= uint64(f.blocks) {
			return nil, nil
		}
		return f.block(n, true), nil
	case "eth_getBlockReceipts":
		if f.noBlockRc {
			return nil, &rpcError{Code: -32601, Message: "method not found"}
		}
		return f.receipts(num(0)), nil
	case "eth_getTransactionReceipt":
		var hash string
		json.Unmarshal(params[0], &hash)
		var n uint64
		fmt.Sscanf(hash[4:], "%x", &n)
		return f.receipts(n)[0], nil
	case "eth_getBalance":
		n := num(1)
		var addr string
		json.Unmarshal(params[0], &addr)
		v := map[string]any{"v": fmt.Sprintf("0x%x", 1000+n)}
		if f.mutate != nil {
			f.mutate("eth_getBalance:"+addr, n, v)
		}
		return v["v"], nil
	}
	return nil, &rpcError{Code: -32601, Message: "method not found"}
}

func (f *fakeChain) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cur := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		m := f.maxSeen.Load()
		if cur <= m || f.maxSeen.CompareAndSwap(m, cur) {
			break
		}
	}
	time.Sleep(2 * time.Millisecond)
	if f.failFirst.Load() > 0 {
		f.failFirst.Add(-1)
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	var reqs []struct {
		ID     json.RawMessage   `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqs); err != nil {
		http.Error(w, "bad", 400)
		return
	}
	f.batches.Add(1)
	if len(reqs) > 25 {
		w.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"batch too large"}}`))
		return
	}
	out := make([]map[string]any, 0, len(reqs))
	// Answer in reverse order: ids, not positions, must be what matches.
	for i := len(reqs) - 1; i >= 0; i-- {
		q := reqs[i]
		res, e := f.answer(q.Method, q.Params)
		m := map[string]any{"jsonrpc": "2.0", "id": q.ID}
		if e != nil {
			m["error"] = e
		} else {
			m["result"] = res
		}
		out = append(out, m)
	}
	json.NewEncoder(w).Encode(out)
}

func dumpFake(t *testing.T, f *fakeChain, name string, to *uint64) string {
	t.Helper()
	srv := httptest.NewServer(f)
	defer srv.Close()
	out := filepath.Join(t.TempDir(), name)
	_, n, err := runDump(context.Background(), dumpOptions{RPC: srv.URL, Out: out, Concurrency: 4, BatchCalls: 24, Timeout: 5 * time.Second, To: to})
	if err != nil {
		t.Fatalf("dump: %v", err)
	}
	want := f.blocks
	if to != nil {
		want = int(*to) + 1
	}
	if n != want {
		t.Fatalf("dumped %d blocks, want %d", n, want)
	}
	return out
}

func mustLoad(t *testing.T, p string) *fingerprintFile {
	t.Helper()
	ff, err := loadFingerprint(p)
	if err != nil {
		t.Fatal(err)
	}
	return ff
}

func TestDumpTwiceIsIdentical(t *testing.T) {
	f := &fakeChain{blocks: 137}
	a := dumpFake(t, f, "a.fp", nil)
	b := dumpFake(t, f, "b.fp", nil)
	res := compareFingerprints(mustLoad(t, a), mustLoad(t, b), false)
	if !res.Identical {
		t.Fatalf("want identical, got %s", res.Message)
	}
	if got := f.maxSeen.Load(); got > 4 {
		t.Fatalf("%d requests in flight, the cap is 4", got)
	}
	// 137 blocks, 8 per chunk, 2 batches per chunk + 1 header batch.
	if got, want := f.batches.Load(), int32(2*(2*18+1)); got != want {
		t.Fatalf("%d batches, want %d", got, want)
	}
	ff := mustLoad(t, a)
	if ff.Header.ChainID != 20260715 || ff.Header.GenesisHash != h("bb", 0) || ff.Header.Head != 136 {
		t.Fatalf("header: %+v", ff.Header)
	}
	b5 := ff.Blocks[5]
	if b5.TxCount != 1 || b5.LogCount != 1 || b5.FirstSender == nil || *b5.FirstSenderBalance != "1005" || b5.MinerBalance != "1005" {
		t.Fatalf("block 5: %+v", b5)
	}
	if ff.Blocks[0].FirstSender != nil {
		t.Fatal("block 0 has no transactions, so no sender probe")
	}
}

func TestCompareReportsFirstDifference(t *testing.T) {
	base := &fakeChain{blocks: 60}
	a := dumpFake(t, base, "a.fp", nil)
	cases := []struct {
		name   string
		mutate func(string, uint64, map[string]any)
		want   string
	}{
		{"state root", func(m string, n uint64, v map[string]any) {
			if m == "eth_getBlockByNumber" && n == 41 {
				v["stateRoot"] = h("ff", n)
			}
		}, "block 41: state_root differs"},
		{"a log", func(m string, n uint64, v map[string]any) {
			if m == "receipt" && n == 17 {
				v["logs"].([]any)[0].(map[string]any)["data"] = "0x02"
			}
		}, "block 17: receipts_digest differs"},
		{"historical balance", func(m string, n uint64, v map[string]any) {
			if strings.HasPrefix(m, "eth_getBalance:0x0000000000000000000000000000000000001") && n == 30 {
				v["v"] = "0x0"
			}
		}, "block 30: first_sender_balance differs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := dumpFake(t, &fakeChain{blocks: 60, mutate: tc.mutate}, "b.fp", nil)
			res := compareFingerprints(mustLoad(t, a), mustLoad(t, b), false)
			if res.Identical || !strings.Contains(res.Message, tc.want) {
				t.Fatalf("want %q, got identical=%v %q", tc.want, res.Identical, res.Message)
			}
		})
	}
}

func TestClientOnlyFieldsDoNotMatter(t *testing.T) {
	a := dumpFake(t, &fakeChain{blocks: 20}, "a.fp", nil)
	// A newer client that adds blockTimestamp-like fields and answers in a
	// different case still fingerprints the same.
	b := dumpFake(t, &fakeChain{blocks: 20, mutate: func(m string, n uint64, v map[string]any) {
		if m == "receipt" {
			v["blockTimestamp"] = "0x99"
			v["transactionHash"] = strings.ToUpper(v["transactionHash"].(string)[2:])
			v["transactionHash"] = "0x" + v["transactionHash"].(string)
		}
	}}, "b.fp", nil)
	if res := compareFingerprints(mustLoad(t, a), mustLoad(t, b), false); !res.Identical {
		t.Fatal(res.Message)
	}
}

func TestLongerAndShorter(t *testing.T) {
	short := uint64(39)
	a := dumpFake(t, &fakeChain{blocks: 60}, "a.fp", &short)
	b := dumpFake(t, &fakeChain{blocks: 60}, "b.fp", nil)
	if res := compareFingerprints(mustLoad(t, a), mustLoad(t, b), false); res.Identical || !strings.Contains(res.Message, "--allow-longer") {
		t.Fatalf("B longer must fail without the flag: %s", res.Message)
	}
	if res := compareFingerprints(mustLoad(t, a), mustLoad(t, b), true); !res.Identical {
		t.Fatalf("B longer with the flag: %s", res.Message)
	}
	// A longer than B is always a failure: B lost blocks.
	if res := compareFingerprints(mustLoad(t, b), mustLoad(t, a), true); res.Identical || !strings.Contains(res.Message, "block 40: in A, missing from B") {
		t.Fatalf("B shorter: %s", res.Message)
	}
}

func TestReceiptFallbackAndRetries(t *testing.T) {
	a := dumpFake(t, &fakeChain{blocks: 25}, "a.fp", nil)
	f := &fakeChain{blocks: 25, noBlockRc: true}
	f.failFirst.Store(3)
	b := dumpFake(t, f, "b.fp", nil)
	if res := compareFingerprints(mustLoad(t, a), mustLoad(t, b), false); !res.Identical {
		t.Fatal(res.Message)
	}
}

func TestBrokenFilesAreRejected(t *testing.T) {
	a := dumpFake(t, &fakeChain{blocks: 12}, "a.fp", nil)
	raw, _ := os.ReadFile(a)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")

	trunc := filepath.Join(t.TempDir(), "trunc.fp")
	os.WriteFile(trunc, []byte(strings.Join(lines[:len(lines)-3], "\n")+"\n"), 0o644)
	if _, err := loadFingerprint(trunc); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("truncated file: %v", err)
	}

	edited := filepath.Join(t.TempDir(), "edited.fp")
	e := append([]string{}, lines...)
	e[3] = strings.Replace(e[3], `"tx_count":1`, `"tx_count":2`, 1)
	os.WriteFile(edited, []byte(strings.Join(e, "\n")+"\n"), 0o644)
	if _, err := loadFingerprint(edited); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("edited file: %v", err)
	}
}

func TestDumpRejectsABrokenChain(t *testing.T) {
	f := &fakeChain{blocks: 30, mutate: func(m string, n uint64, v map[string]any) {
		if m == "eth_getBlockByNumber" && n == 20 {
			v["parentHash"] = h("ab", 19)
		}
	}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	_, _, err := runDump(context.Background(), dumpOptions{RPC: srv.URL, Out: filepath.Join(t.TempDir(), "x"), Concurrency: 2, BatchCalls: 24, Timeout: 5 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "block 20 parent_hash") {
		t.Fatalf("want a parent link error, got %v", err)
	}
}

func TestConcurrencyCap(t *testing.T) {
	_, _, err := runDump(context.Background(), dumpOptions{RPC: "http://127.0.0.1:1", Concurrency: 5})
	if err == nil || !strings.Contains(err.Error(), "1..4") {
		t.Fatalf("got %v", err)
	}
}

func TestPersistedHeight(t *testing.T) {
	dir := t.TempDir()
	sf := filepath.Join(dir, "static_files")
	os.MkdirAll(sf, 0o755)
	// 187 headers (blocks 0..186): 1 + 8*(187*3+1) bytes, as measured on a
	// real datadir of this node.
	os.WriteFile(filepath.Join(sf, "static_file_headers_0_499999.off"), make([]byte, 1+8*(187*3+1)), 0o644)
	os.WriteFile(filepath.Join(sf, "static_file_transactions_0_499999.off"), make([]byte, 1+8*187), 0o644)
	info, err := persistedHeight(dir)
	if err != nil || info.Highest != 186 {
		t.Fatalf("got %d, %v", info.Highest, err)
	}
	// A fresh file (no rows yet) means nothing on disk.
	os.WriteFile(filepath.Join(sf, "static_file_headers_0_499999.off"), make([]byte, 9), 0o644)
	if info, _ := persistedHeight(dir); info.Highest != -1 {
		t.Fatalf("empty: %d", info.Highest)
	}
	os.WriteFile(filepath.Join(sf, "static_file_headers_0_499999.off"), make([]byte, 20), 0o644)
	if _, err := persistedHeight(dir); err == nil {
		t.Fatal("a size that does not fit the layout must be an error")
	}
}

func TestSignLegacyTransferRecoversSender(t *testing.T) {
	// Hardhat account #19 (public test key).
	key, _ := crypto.HexToECDSA("df57089febbacf7ba0bc227dafbffa9fc08a93fdc68e1e42411a14efcf23656e")
	from := crypto.PubkeyToAddress(key.PublicKey)
	if from != common.HexToAddress("0x8626f6940E2eb28930eFb4CeF49B2d1F2C9C1199") {
		t.Fatalf("address %s", from.Hex())
	}
	raw, err := signLegacyTransfer(key, 7, big.NewInt(1_000_000_000), 21000, from, 20260715)
	if err != nil {
		t.Fatal(err)
	}
	var fields []rlp.RawValue
	if err := rlp.DecodeBytes(raw, &fields); err != nil || len(fields) != 9 {
		t.Fatalf("decode: %v (%d fields)", err, len(fields))
	}
	var v, r, s big.Int
	rlp.DecodeBytes(fields[6], &v)
	rlp.DecodeBytes(fields[7], &r)
	rlp.DecodeBytes(fields[8], &s)
	unsigned, _ := rlp.EncodeToBytes([]any{uint64(7), big.NewInt(1_000_000_000), uint64(21000), from, big.NewInt(0), []byte{}, big.NewInt(20260715), uint(0), uint(0)})
	recID := new(big.Int).Sub(&v, big.NewInt(20260715*2+35)).Int64()
	if recID != 0 && recID != 1 {
		t.Fatalf("v %s is not EIP-155 for chain 20260715", v.String())
	}
	sig := make([]byte, 65)
	r.FillBytes(sig[0:32])
	s.FillBytes(sig[32:64])
	sig[64] = byte(recID)
	pub, err := crypto.SigToPub(crypto.Keccak256(unsigned), sig)
	if err != nil || crypto.PubkeyToAddress(*pub) != from {
		t.Fatalf("recovered %v, %v", pub, err)
	}
}

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://user:pw@rpc.example.com/x?key=1": "https://rpc.example.com/x",
		"http://127.0.0.1:8645":                   "http://127.0.0.1:8645",
	} {
		if got := redactURL(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

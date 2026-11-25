package chainrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestQuantityLenient(t *testing.T) {
	var v struct {
		A Quantity `json:"a"`
		B Quantity `json:"b"`
		C Quantity `json:"c"`
		D Quantity `json:"d"`
	}
	if err := json.Unmarshal([]byte(`{"a":"0x01f","b":"0x","c":null,"d":12}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A.Uint64() != 31 || !v.B.Valid || v.B.Uint64() != 0 || v.C.Valid || v.D.Uint64() != 12 {
		t.Errorf("%+v", v)
	}
	if Hex64(255) != "0xff" {
		t.Error("Hex64")
	}
}

func TestErrorClassification(t *testing.T) {
	if !IsMethodNotFound(&Error{Code: -32601, Message: "Method not found"}) {
		t.Error("-32601")
	}
	if !IsMethodNotFound(&Error{Code: -32000, Message: "the method debug_traceBlockByNumber does not exist/is not available"}) {
		t.Error("geth wording")
	}
	if IsMethodNotFound(&TransportError{errors.New("x")}) {
		t.Error("transport error is not method-not-found")
	}
	for _, e := range []*Error{
		{Code: 3, Message: "execution reverted: not found"},
		{Code: -32000, Message: "execution reverted"},
		{Code: -32015, Message: "invalid opcode: INVALID"},
	} {
		if !IsExecutionError(e) {
			t.Errorf("%v should be an execution outcome", e)
		}
	}
	for _, e := range []*Error{
		{Code: -32000, Message: "header not found"},
		{Code: -32000, Message: "missing trie node abc"},
		{Code: -32602, Message: "invalid params"},
	} {
		if IsExecutionError(e) {
			t.Errorf("%v must not be treated as an answer", e)
		}
	}
}

// fakeNode answers eth_blockNumber and echoes params for "echo"; it fails
// the first failFirst HTTP requests with a 502.
type fakeNode struct {
	failFirst int32
	requests  atomic.Int32
	inFlight  atomic.Int32
	maxFlight atomic.Int32
	batchSize []int
	mu        sync.Mutex
}

func (f *fakeNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := f.requests.Add(1)
	cur := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		m := f.maxFlight.Load()
		if cur <= m || f.maxFlight.CompareAndSwap(m, cur) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	if n <= f.failFirst {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	body, _ := io.ReadAll(r.Body)
	answer := func(req map[string]any) map[string]any {
		out := map[string]any{"jsonrpc": "2.0", "id": req["id"]}
		switch req["method"] {
		case "eth_blockNumber":
			out["result"] = "0x2a"
		case "echo":
			out["result"] = req["params"]
		default:
			out["error"] = map[string]any{"code": -32601, "message": "Method not found"}
		}
		return out
	}
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(strings.TrimSpace(string(body)), "[") {
		var reqs []map[string]any
		_ = json.Unmarshal(body, &reqs)
		f.mu.Lock()
		f.batchSize = append(f.batchSize, len(reqs))
		f.mu.Unlock()
		out := make([]map[string]any, len(reqs))
		for i := range reqs {
			out[len(reqs)-1-i] = answer(reqs[i]) // reversed: ids must be matched, not positions
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	_ = json.NewEncoder(w).Encode(answer(req))
}

func TestClientRetryBatchAndConcurrency(t *testing.T) {
	node := &fakeNode{failFirst: 2}
	srv := httptest.NewServer(node)
	defer srv.Close()
	c := New(srv.URL, Options{MaxConcurrent: 2, MaxBatch: 3, Retries: 4})
	ctx := context.Background()

	n, err := c.BlockNumber(ctx)
	if err != nil || n != 42 {
		t.Fatalf("after two 502s: %d %v", n, err)
	}

	elems := make([]BatchElem, 7)
	outs := make([][]int, 7)
	for i := range elems {
		elems[i] = BatchElem{Method: "echo", Params: []any{i}, Result: &outs[i]}
	}
	elems[5].Method = "nope"
	if err := c.BatchCall(ctx, elems); err != nil {
		t.Fatal(err)
	}
	for i := range elems {
		if i == 5 {
			if !IsMethodNotFound(elems[i].Error) {
				t.Errorf("elem 5: %v", elems[i].Error)
			}
			continue
		}
		if elems[i].Error != nil || len(outs[i]) != 1 || outs[i][0] != i {
			t.Errorf("elem %d: %v %v", i, outs[i], elems[i].Error)
		}
	}
	node.mu.Lock()
	if len(node.batchSize) != 3 || node.batchSize[0] != 3 || node.batchSize[2] != 1 {
		t.Errorf("batches split as %v, want [3 3 1]", node.batchSize)
	}
	node.mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = c.BlockNumber(ctx) }()
	}
	wg.Wait()
	if m := node.maxFlight.Load(); m > 2 {
		t.Errorf("%d requests in flight, MaxConcurrent is 2", m)
	}

	// The node answering an error object is not retried.
	before := node.requests.Load()
	err = c.Call(ctx, nil, "nope")
	if !IsMethodNotFound(err) || node.requests.Load() != before+1 {
		t.Errorf("rpc error retried or misread: %v", err)
	}
}

func TestHeadSubscriberReconnects(t *testing.T) {
	var conns atomic.Int32
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		k := conns.Add(1)
		var req map[string]any
		if err := c.ReadJSON(&req); err != nil || req["method"] != "eth_subscribe" {
			return
		}
		_ = c.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": "0xsub"})
		head := func(n string) {
			_ = c.WriteJSON(map[string]any{"jsonrpc": "2.0", "method": "eth_subscription",
				"params": map[string]any{"subscription": "0xsub", "result": map[string]any{"number": n}}})
		}
		if k == 1 {
			head("0x5")
			return // drop the socket: the client must reconnect
		}
		head("0x6")
		time.Sleep(2 * time.Second)
	}))
	defer srv.Close()

	var mu sync.Mutex
	var heads []uint64
	var subs []bool
	got := make(chan struct{}, 10)
	s := &HeadSubscriber{
		URL:          "ws" + strings.TrimPrefix(srv.URL, "http"),
		PingInterval: time.Second,
		OnHead: func(n uint64) {
			mu.Lock()
			heads = append(heads, n)
			mu.Unlock()
			got <- struct{}{}
		},
		OnSubscribed: func(re bool) {
			mu.Lock()
			subs = append(subs, re)
			mu.Unlock()
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go s.Run(ctx)
	for i := 0; i < 2; i++ {
		select {
		case <-got:
		case <-ctx.Done():
			t.Fatal("timed out waiting for heads")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(heads) != 2 || heads[0] != 5 || heads[1] != 6 {
		t.Errorf("heads %v", heads)
	}
	if len(subs) != 2 || subs[0] || !subs[1] {
		t.Errorf("subscriptions %v, want [false true]", subs)
	}
	if s.Reconnects() != 1 {
		t.Errorf("reconnects %d", s.Reconnects())
	}
}

func TestHeadSubscriberDetectsSilentSocket(t *testing.T) {
	// The server never answers pings (its read loop is not running), so the
	// client must give up on the socket after two ping rounds and redial.
	var conns atomic.Int32
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		conns.Add(1)
		var req map[string]any
		if c.ReadJSON(&req) != nil {
			return
		}
		_ = c.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": "0xsub"})
		time.Sleep(8 * time.Second) // no reads: pings go unanswered
	}))
	defer srv.Close()
	s := &HeadSubscriber{URL: "ws" + strings.TrimPrefix(srv.URL, "http"), PingInterval: 200 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go s.Run(ctx)
	deadline := time.Now().Add(25 * time.Second)
	for conns.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if conns.Load() < 2 {
		t.Fatalf("silent socket never replaced (connections: %d)", conns.Load())
	}
}

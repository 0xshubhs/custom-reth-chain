package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// rpcClient is a small JSON-RPC 2.0 client over HTTP with batching, a cap on
// concurrent requests and retries. It is deliberately self-contained: this
// tool runs against production nodes during a migration and must not change
// behaviour when the shared chainrpc package does.
type rpcClient struct {
	url     string
	http    *http.Client
	sem     chan struct{} // one slot per request allowed in flight
	retries int
	nextID  atomic.Int64
	// requests counts HTTP requests sent (a batch is one request).
	requests atomic.Int64
}

type rpcCall struct {
	Method string
	Params []any
	// Result receives the decoded "result". Nil discards it.
	Result any
	// Err is set per call when the node answered with an error object.
	Err *rpcError
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func newRPCClient(url string, concurrency int, timeout time.Duration) *rpcClient {
	if concurrency < 1 {
		concurrency = 1
	}
	return &rpcClient{
		url:     url,
		http:    &http.Client{Timeout: timeout},
		sem:     make(chan struct{}, concurrency),
		retries: 6,
	}
}

// call sends one request (not batched).
func (c *rpcClient) call(ctx context.Context, method string, result any, params ...any) error {
	if params == nil {
		params = []any{}
	}
	calls := []*rpcCall{{Method: method, Params: params, Result: result}}
	if err := c.batch(ctx, calls); err != nil {
		return err
	}
	if calls[0].Err != nil {
		return calls[0].Err
	}
	return nil
}

// batch sends calls as one JSON-RPC batch. A transport failure, an HTTP
// status other than 200, or an answer that does not carry every id is
// retried with backoff; per-call error objects are returned in each call's
// Err and are not retried here.
func (c *rpcClient) batch(ctx context.Context, calls []*rpcCall) error {
	if len(calls) == 0 {
		return nil
	}
	reqs := make([]rpcRequest, len(calls))
	byID := make(map[string]int, len(calls))
	for i, cl := range calls {
		id := c.nextID.Add(1)
		params := cl.Params
		if params == nil {
			params = []any{}
		}
		reqs[i] = rpcRequest{JSONRPC: "2.0", ID: id, Method: cl.Method, Params: params}
		byID[fmt.Sprint(id)] = i
	}
	body, err := json.Marshal(reqs)
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(1<<uint(attempt-1)) * 500 * time.Millisecond
			if wait > 15*time.Second {
				wait = 15 * time.Second
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		resps, err := c.send(ctx, body)
		if err != nil {
			lastErr = err
			if errors.Is(err, errPermanent) {
				return err
			}
			continue
		}
		if len(resps) != len(calls) {
			lastErr = fmt.Errorf("batch of %d calls got %d answers", len(calls), len(resps))
			continue
		}
		seen := 0
		for _, r := range resps {
			idx, ok := byID[strings.Trim(string(r.ID), `"`)]
			if !ok {
				continue
			}
			seen++
			cl := calls[idx]
			cl.Err = r.Error
			if r.Error == nil && cl.Result != nil {
				if err := json.Unmarshal(r.Result, cl.Result); err != nil {
					return fmt.Errorf("%s: decode result: %w", cl.Method, err)
				}
			}
		}
		if seen != len(calls) {
			lastErr = fmt.Errorf("batch answers do not match the request ids")
			continue
		}
		return nil
	}
	return fmt.Errorf("rpc %s: giving up after %d attempts: %w", c.url, c.retries+1, lastErr)
}

var errPermanent = errors.New("permanent")

func (c *rpcClient) send(ctx context.Context, body []byte) ([]rpcResponse, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.sem }()
	c.requests.Add(1)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errPermanent, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "chain-fingerprint/1")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, snippet(raw))
	default:
		return nil, fmt.Errorf("%w: http %d: %s", errPermanent, resp.StatusCode, snippet(raw))
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '{' {
		// A single error object for the whole batch (e.g. a gateway refusal).
		var one rpcResponse
		if err := json.Unmarshal(raw, &one); err == nil && one.Error != nil {
			return nil, fmt.Errorf("%w: batch refused: %v", errPermanent, one.Error)
		}
		return nil, fmt.Errorf("unexpected single answer to a batch: %s", snippet(raw))
	}
	var out []rpcResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode batch answer: %w (%s)", err, snippet(raw))
	}
	return out, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

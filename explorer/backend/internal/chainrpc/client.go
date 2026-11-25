package chainrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// Error is a JSON-RPC error object returned by the node. It is never retried
// by the client: the node answered, and asking again gives the same answer.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// IsMethodNotFound reports whether err is the node saying it does not serve
// the method (JSON-RPC -32601, or the wording nodes use for a disabled
// namespace).
func IsMethodNotFound(err error) bool {
	var re *Error
	if !errors.As(err, &re) {
		return false
	}
	if re.Code == -32601 {
		return true
	}
	m := strings.ToLower(re.Message)
	return strings.Contains(m, "method not found") ||
		strings.Contains(m, "does not exist/is not available") ||
		strings.Contains(m, "not supported") ||
		strings.Contains(m, "unsupported method")
}

// IsExecutionError reports whether err is the node reporting a deterministic
// EVM outcome of an eth_call (revert, invalid opcode, out of gas). Those are
// answers, not failures: calling again at the same block gives the same one.
// A missing block or state ("header not found", "missing trie node") is not.
func IsExecutionError(err error) bool {
	var re *Error
	if !errors.As(err, &re) {
		return false
	}
	m := strings.ToLower(re.Message)
	// A revert is an answer whatever its reason text says.
	if re.Code == 3 || strings.Contains(m, "revert") {
		return true
	}
	for _, transient := range []string{"header not found", "unknown block", "missing trie node", "not found", "state is not available", "pruned", "timeout", "too many", "rate limit"} {
		if strings.Contains(m, transient) {
			return false
		}
	}
	for _, s := range []string{"invalid opcode", "out of gas", "execution", "stack underflow", "stack overflow", "invalid jump", "write protection", "opcode"} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// TransportError wraps a failure to get any JSON-RPC answer at all (network,
// timeout, HTTP status, unparseable body). These are retried.
type TransportError struct{ Err error }

func (e *TransportError) Error() string { return "rpc transport: " + e.Err.Error() }
func (e *TransportError) Unwrap() error { return e.Err }

// Options tunes a Client.
type Options struct {
	Timeout       time.Duration // per HTTP request, default 30s
	Retries       int           // extra attempts on transport errors, default 5
	MaxConcurrent int           // in-flight HTTP requests, default 4
	MaxBatch      int           // calls per batch request, default 25 (the public gateway's cap)
}

// Client is a JSON-RPC 2.0 client over HTTP. Safe for concurrent use; the
// number of requests in flight at once never exceeds MaxConcurrent, which is
// how the indexer stays polite to a node it shares.
type Client struct {
	url   string
	http  *http.Client
	opts  Options
	sem   chan struct{}
	ids   atomic.Uint64
	calls atomic.Uint64
}

// New returns a client for url.
func New(url string, opts Options) *Client {
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.Retries < 0 {
		opts.Retries = 0
	} else if opts.Retries == 0 {
		opts.Retries = 5
	}
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 4
	}
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = 25
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = opts.MaxConcurrent * 2
	return &Client{
		url:  url,
		http: &http.Client{Transport: tr},
		opts: opts,
		sem:  make(chan struct{}, opts.MaxConcurrent),
	}
}

// URL is the node URL this client talks to.
func (c *Client) URL() string { return c.url }

// Requests is the number of HTTP requests sent so far (for logs).
func (c *Client) Requests() uint64 { return c.calls.Load() }

type request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type response struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

// Call runs one method and decodes its result into result (a pointer, or nil
// to discard). A JSON null result leaves result untouched.
func (c *Client) Call(ctx context.Context, result any, method string, params ...any) error {
	if params == nil {
		params = []any{}
	}
	req := request{JSONRPC: "2.0", ID: c.ids.Add(1), Method: method, Params: params}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	raw, err := c.post(ctx, body)
	if err != nil {
		return err
	}
	var resp response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return &TransportError{fmt.Errorf("%s: decode response: %w", method, err)}
	}
	if resp.Error != nil {
		return resp.Error
	}
	return decodeResult(resp.Result, result)
}

func decodeResult(raw json.RawMessage, result any) error {
	if result == nil || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if rm, ok := result.(*json.RawMessage); ok {
		*rm = append((*rm)[:0], raw...)
		return nil
	}
	return json.Unmarshal(raw, result)
}

// BatchElem is one call of a batch. After BatchCall, Error holds that call's
// JSON-RPC error (or decode error), and Result is filled on success.
type BatchElem struct {
	Method string
	Params []any
	Result any
	Error  error
}

// BatchCall sends elems as JSON-RPC batches of at most MaxBatch calls. The
// returned error is a transport failure of the batch as a whole (after
// retries); per-call errors are in each element's Error.
func (c *Client) BatchCall(ctx context.Context, elems []BatchElem) error {
	for start := 0; start < len(elems); start += c.opts.MaxBatch {
		end := min(start+c.opts.MaxBatch, len(elems))
		if err := c.batch(ctx, elems[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) batch(ctx context.Context, elems []BatchElem) error {
	if len(elems) == 0 {
		return nil
	}
	reqs := make([]request, len(elems))
	byID := make(map[uint64]int, len(elems))
	for i := range elems {
		p := elems[i].Params
		if p == nil {
			p = []any{}
		}
		id := c.ids.Add(1)
		reqs[i] = request{JSONRPC: "2.0", ID: id, Method: elems[i].Method, Params: p}
		byID[id] = i
		elems[i].Error = nil
	}
	body, err := json.Marshal(reqs)
	if err != nil {
		return err
	}
	raw, err := c.post(ctx, body)
	if err != nil {
		return err
	}
	var resps []response
	if err := json.Unmarshal(raw, &resps); err != nil {
		// Some nodes answer a whole batch with one error object.
		var single response
		if json.Unmarshal(raw, &single) == nil && single.Error != nil {
			for i := range elems {
				elems[i].Error = single.Error
			}
			return nil
		}
		return &TransportError{fmt.Errorf("batch: decode response: %w", err)}
	}
	seen := make([]bool, len(elems))
	for _, r := range resps {
		var id uint64
		if err := json.Unmarshal(r.ID, &id); err != nil {
			continue
		}
		i, ok := byID[id]
		if !ok {
			continue
		}
		seen[i] = true
		if r.Error != nil {
			elems[i].Error = r.Error
			continue
		}
		if err := decodeResult(r.Result, elems[i].Result); err != nil {
			elems[i].Error = err
		}
	}
	for i := range elems {
		if !seen[i] {
			return &TransportError{fmt.Errorf("batch: no answer for %s", elems[i].Method)}
		}
	}
	return nil
}

// post sends one HTTP request, retrying transport failures with jittered
// exponential backoff while ctx allows.
func (c *Client) post(ctx context.Context, body []byte) ([]byte, error) {
	var last error
	backoff := 200 * time.Millisecond
	for attempt := 0; attempt <= c.opts.Retries; attempt++ {
		if attempt > 0 {
			d := backoff + time.Duration(rand.Int64N(int64(backoff/2)+1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(d):
			}
			backoff = min(backoff*2, 5*time.Second)
		}
		raw, err := c.postOnce(ctx, body)
		if err == nil {
			return raw, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		last = err
	}
	return nil, last
}

func (c *Client) postOnce(ctx context.Context, body []byte) ([]byte, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-c.sem }()
	c.calls.Add(1)

	rctx, cancel := context.WithTimeout(ctx, c.opts.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &TransportError{err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, &TransportError{err}
	}
	if resp.StatusCode != http.StatusOK {
		// A JSON-RPC error body with a non-200 status is still an answer.
		if len(raw) > 0 && (raw[0] == '{' || raw[0] == '[') && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			return raw, nil
		}
		snippet := string(raw)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, &TransportError{fmt.Errorf("http %d: %s", resp.StatusCode, snippet)}
	}
	return raw, nil
}

// WaitChainID asks the node for its chain id until it answers (or ctx ends),
// for a deployment that left CHAIN_ID unset and takes whatever chain the RPC
// URL points at.
func (c *Client) WaitChainID(ctx context.Context, log *slog.Logger) (uint64, error) {
	backoff := time.Second
	for {
		id, err := c.ChainID(ctx)
		if err == nil {
			return id, nil
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		log.Warn("node not answering eth_chainId yet", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// ServesTraces reports whether the node answers debug_traceBlockByNumber
// with the call tracer (probed on block 0). A transport failure is returned
// as an error so the caller can retry rather than guess.
func (c *Client) ServesTraces(ctx context.Context) (bool, error) {
	var out json.RawMessage
	err := c.Call(ctx, &out, "debug_traceBlockByNumber", "0x0", map[string]string{"tracer": "callTracer"})
	if err == nil {
		return true, nil
	}
	if IsMethodNotFound(err) {
		return false, nil
	}
	var rpcErr *Error
	if errors.As(err, &rpcErr) {
		// The node answered, but not with a trace (some nodes refuse block 0):
		// it does serve the namespace.
		return true, nil
	}
	return false, err
}

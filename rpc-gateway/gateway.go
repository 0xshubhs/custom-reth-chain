package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Gateway is the public face of the chain's JSON-RPC.
type Gateway struct {
	cfg      *Config
	client   *http.Client
	limiter  *ipLimiter // nil when per-IP limiting is off
	upgrader websocket.Upgrader
	dialer   *websocket.Dialer

	headMu sync.Mutex
	head_  uint64
	headAt time.Time

	// Refusals are counted and logged as a summary, never one line per call:
	// a log line per request is how this host once wrote 60 GB a day.
	refused sync.Map // reason -> *atomic.Int64
}

func newGateway(cfg *Config) *Gateway {
	g := &Gateway{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.UpstreamTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        64,
				MaxIdleConnsPerHost: 64,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		dialer: &websocket.Dialer{HandshakeTimeout: 10 * time.Second},
	}
	g.upgrader = websocket.Upgrader{
		HandshakeTimeout: 10 * time.Second,
		// The origin has already been checked against the allow-list by the
		// time Upgrade runs; see ServeHTTP.
		CheckOrigin: func(*http.Request) bool { return true },
	}
	if cfg.RatePerIP > 0 {
		g.limiter = newIPLimiter(cfg.RatePerIP, cfg.BurstPerIP)
	}
	return g
}

func (g *Gateway) count(reason string) {
	v, _ := g.refused.LoadOrStore(reason, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
}

// logRefusals writes one summary line per interval when anything was refused.
func (g *Gateway) logRefusals(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		attrs := []any{}
		g.refused.Range(func(k, v any) bool {
			if n := v.(*atomic.Int64).Swap(0); n > 0 {
				attrs = append(attrs, k.(string), n)
			}
			return true
		})
		if len(attrs) > 0 {
			slog.Info("rpc-gateway: refused calls in the last interval", attrs...)
		}
	}
}

// clientIP is the address the per-IP limit is keyed on. Forwarding headers are
// believed only from a trusted peer (nginx on loopback); anyone else could
// write any address they like into them.
func (g *Gateway) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if inNets(net.ParseIP(host), g.cfg.TrustedProxies) {
		for _, h := range []string{"CF-Connecting-IP", "X-Real-IP"} {
			if v := strings.TrimSpace(r.Header.Get(h)); net.ParseIP(v) != nil {
				return v
			}
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if net.ParseIP(first) != nil {
				return first
			}
		}
	}
	return host
}

// rateLimited applies the per-IP limit for n calls and writes the 429 itself.
func (g *Gateway) rateLimited(w http.ResponseWriter, r *http.Request, n int) bool {
	if g.limiter == nil {
		return false
	}
	ip := g.clientIP(r)
	if inNets(net.ParseIP(ip), g.cfg.Exempt) {
		return false
	}
	ok, wait := g.limiter.allow(ip, n)
	if ok {
		return false
	}
	g.count("rate_limited")
	secs := int((wait + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeRPC(w, http.StatusTooManyRequests, errorResponse(nil, codeLimitExceeded,
		fmt.Sprintf("too many requests from your address, retry in %d s", secs)))
	return true
}

// walletExtensionSchemes are the origins of browser extensions. Wallets
// (MetaMask, Rabby, Coinbase Wallet, ...) call the network's RPC from their
// own background page, so every request carries Origin
// chrome-extension://<id> (Chrome, Brave, Edge), moz-extension://<uuid>
// (Firefox) or safari-web-extension://<uuid>. They are not websites, so they
// get CORS headers and may open a websocket. (Refusing them made adding the
// network to MetaMask fail with "could not fetch chain ID".)
var walletExtensionSchemes = []string{"chrome-extension://", "moz-extension://", "safari-web-extension://"}

func isWalletExtension(origin string) bool {
	for _, scheme := range walletExtensionSchemes {
		if strings.HasPrefix(origin, scheme) && len(origin) > len(scheme) {
			return true
		}
	}
	return false
}

var headerToken = regexp.MustCompile(`^[A-Za-z0-9!#$%&'*+.^_|~-]+$`)

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	origin := strings.TrimRight(r.Header.Get("Origin"), "/")
	// The origin is not a permission, as in geth (--http.corsdomain): what a
	// caller may do is the method allowlist and the rate limits, the same for
	// everyone. It only decides who gets CORS headers: our own sites and
	// browser-extension wallets. A page on another site gets no CORS headers,
	// so its browser stops it at the preflight (JSON-RPC always needs one)
	// and never lets it read an answer. Servers, mobile wallets and curl send
	// no Origin and are not affected.
	cors := origin != "" && (g.cfg.AllowedOrigins[origin] || isWalletExtension(origin))
	if origin != "" && !cors {
		g.count("origin")
	}

	if websocket.IsWebSocketUpgrade(r) {
		// Browsers do not apply CORS to websockets, so a page on another site
		// could use one freely: its handshake is refused, as geth's
		// --ws.origins does.
		if origin != "" && !cors {
			writeRPC(w, http.StatusForbidden, errorResponse(nil, -32000, "origin not allowed"))
			return
		}
		g.serveWS(w, r)
		return
	}

	if origin != "" {
		w.Header().Add("Vary", "Origin")
	}
	if cors {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	}

	switch r.Method {
	case http.MethodOptions:
		if !cors {
			// Another site's preflight: no CORS headers, so the browser never
			// sends the request itself.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		allowHeaders := "Content-Type"
		if req := r.Header.Get("Access-Control-Request-Headers"); req != "" {
			var ok []string
			for _, h := range strings.Split(req, ",") {
				if h = strings.TrimSpace(h); headerToken.MatchString(h) {
					ok = append(ok, h)
				}
			}
			if len(ok) > 0 {
				allowHeaders = strings.Join(ok, ", ")
			}
		}
		w.Header().Set("Access-Control-Allow-Headers", allowHeaders)
		w.Header().Set("Access-Control-Max-Age", "86400")
		w.WriteHeader(http.StatusNoContent)
		return
	case http.MethodPost:
	default:
		w.Header().Set("Allow", "POST, OPTIONS")
		writeRPC(w, http.StatusMethodNotAllowed, errorResponse(nil, codeInvalidRequest, "use POST"))
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, g.cfg.MaxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			g.count("body_too_large")
			writeRPC(w, http.StatusRequestEntityTooLarge, errorResponse(nil, codeInvalidRequest,
				fmt.Sprintf("request body is larger than %d bytes", g.cfg.MaxBodyBytes)))
			return
		}
		writeRPC(w, http.StatusBadRequest, errorResponse(nil, codeParseError, "could not read the request body"))
		return
	}

	items, isBatch, perr := splitBatch(body)
	if perr != nil {
		writeRPC(w, http.StatusOK, errorResponse(nil, codeParseError, "parse error"))
		return
	}
	if isBatch && len(items) == 0 {
		writeRPC(w, http.StatusOK, errorResponse(nil, codeInvalidRequest, "empty batch"))
		return
	}
	if len(items) > g.cfg.MaxBatch {
		g.count("batch_too_large")
		writeRPC(w, http.StatusRequestEntityTooLarge, errorResponse(nil, codeInvalidRequest,
			fmt.Sprintf("batch too large: at most %d calls per batch", g.cfg.MaxBatch)))
		return
	}
	if g.rateLimited(w, r, len(items)) {
		return
	}

	decisions := make([]decision, len(items))
	for i, raw := range items {
		decisions[i] = g.decide(r.Context(), raw)
		if !decisions[i].forward {
			g.count(decisions[i].reason)
		}
	}

	if !isBatch {
		d := decisions[0]
		if !d.forward {
			if d.resp == nil { // a refused notification: nothing to say
				w.WriteHeader(http.StatusOK)
				return
			}
			writeRPC(w, http.StatusOK, d.resp)
			return
		}
		g.forwardHTTP(w, r.Context(), d.req.encode(), false, nil)
		return
	}

	var fwd []json.RawMessage
	var local []json.RawMessage
	for _, d := range decisions {
		switch {
		case d.forward:
			fwd = append(fwd, d.req.encode())
		case d.resp != nil:
			local = append(local, d.resp)
		}
	}
	if len(fwd) == 0 {
		if len(local) == 0 {
			w.WriteHeader(http.StatusOK)
			return
		}
		out, _ := json.Marshal(local)
		writeRPC(w, http.StatusOK, out)
		return
	}
	batch, _ := json.Marshal(fwd)
	g.forwardHTTP(w, r.Context(), batch, true, local)
}

// splitBatch returns the calls in a request body and whether it was a batch.
func splitBatch(body []byte) ([]json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, false, errors.New("empty body")
	}
	if trimmed[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, true, err
		}
		return items, true, nil
	}
	if !json.Valid(trimmed) {
		return nil, false, errors.New("invalid json")
	}
	return []json.RawMessage{trimmed}, false, nil
}

// forwardHTTP posts body to the node and relays the answer. For a batch with
// calls the gateway answered itself (local), the node's array and the local
// answers are merged into one array; JSON-RPC lets a batch answer come back in
// any order, and clients match answers to calls by id.
func (g *Gateway) forwardHTTP(w http.ResponseWriter, ctx context.Context, body []byte, isBatch bool, local []json.RawMessage) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.Upstream, bytes.NewReader(body))
	if err != nil {
		writeRPC(w, http.StatusInternalServerError, errorResponse(nil, codeInternal, "internal error"))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		g.count("upstream_error")
		writeRPC(w, http.StatusBadGateway, errorResponse(nil, codeInternal, "the chain node did not answer"))
		return
	}
	defer resp.Body.Close()

	if !isBatch || len(local) == 0 {
		// Only the node's own Content-Type. Its CORS headers (it was started
		// with --http-corsdomain '*') are deliberately not copied: the
		// allow-list above is the only CORS policy the public sees.
		w.Header().Set("Content-Type", contentTypeOr(resp.Header.Get("Content-Type")))
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		writeRPC(w, http.StatusBadGateway, errorResponse(nil, codeInternal, "the chain node answer was cut short"))
		return
	}
	var upstream []json.RawMessage
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &upstream) != nil {
		// Not a batch answer (the node refused the batch as a whole): relay it.
		w.Header().Set("Content-Type", contentTypeOr(resp.Header.Get("Content-Type")))
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(raw)
		return
	}
	out, _ := json.Marshal(append(upstream, local...))
	writeRPC(w, http.StatusOK, out)
}

func contentTypeOr(ct string) string {
	if ct == "" {
		return "application/json"
	}
	return ct
}

func writeRPC(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// head returns the chain tip, cached for a second so a burst of tag-based
// eth_getLogs calls costs the node one eth_blockNumber.
func (g *Gateway) head(ctx context.Context) (uint64, error) {
	g.headMu.Lock()
	defer g.headMu.Unlock()
	if !g.headAt.IsZero() && time.Since(g.headAt) < time.Second {
		return g.head_, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.cfg.Upstream,
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}`))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var out struct {
		Result string `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return 0, err
	}
	n, _, ok := parseBlockTag(json.RawMessage(strconv.Quote(out.Result)))
	if !ok || out.Result == "" {
		return 0, fmt.Errorf("bad eth_blockNumber answer %q", out.Result)
	}
	g.head_, g.headAt = n, time.Now()
	return n, nil
}

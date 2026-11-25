// Package api is the explorer's HTTP API (explorer/docs/design.md, "API").
// It reads the database the indexer writes, reads through to the node for
// what is not indexed yet, and runs contract verification jobs.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/config"
	"custom-reth-chain/explorer/backend/internal/httpx"
	"custom-reth-chain/explorer/backend/internal/sigs"
)

// Server is the API.
type Server struct {
	cfg  *config.Config
	pool *pgxpool.Pool
	rpc  *chainrpc.Client
	log  *slog.Logger
	dec  *Decoder

	getLim, verifyLim, callLim *limiter

	hub      *hub
	proxies  *proxyCache
	verifier *http.Client
	jobWake  chan struct{}

	headMu      sync.Mutex
	headVal     *int64
	headAt      time.Time
	statsMu     sync.Mutex
	statsVal    *Stats
	statsAt     time.Time
	nudgeMu     sync.Mutex
	nudgeAt     time.Time
	nudgeQueued bool
	compMu      sync.Mutex
	compVal     []byte
	compAt      time.Time
	hookHTTP    *http.Client
	sigs        sigCache
	charts      chartCache
}

// New builds the API server.
func New(cfg *config.Config, pool *pgxpool.Pool, rpc *chainrpc.Client, log *slog.Logger) *Server {
	s := &Server{
		cfg:       cfg,
		pool:      pool,
		rpc:       rpc,
		log:       log,
		dec:       NewDecoder(pool),
		getLim:    newLimiter(cfg.RateLimitPerMin, time.Minute),
		verifyLim: newLimiter(cfg.VerifyLimitPerHour, time.Hour),
		callLim:   newLimiter(cfg.CallLimitPerMin, time.Minute),
		proxies:   newProxyCache(),
		verifier:  &http.Client{Timeout: 20 * time.Minute},
		hookHTTP:  &http.Client{Timeout: 3 * time.Second},
		jobWake:   make(chan struct{}, 1),
	}
	s.hub = newHub(s)
	return s
}

// Run starts the background work (rate-limit sweeps, the event listener,
// verification workers) and blocks until ctx is done.
func (s *Server) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, l := range []*limiter{s.getLim, s.verifyLim, s.callLim} {
		wg.Add(1)
		go func(l *limiter) { defer wg.Done(); l.run(ctx) }(l)
	}
	wg.Add(2)
	go func() { defer wg.Done(); s.hub.listen(ctx) }()
	go func() { defer wg.Done(); s.runJobs(ctx) }()
	wg.Wait()
}

// Handler is the full HTTP handler with middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/v2/config", s.handleConfig)
	mux.HandleFunc("GET /api/v2/stats", s.handleStats)
	mux.HandleFunc("GET /api/v2/stats/charts/transactions", s.handleTxChart)
	mux.HandleFunc("GET /api/v2/signatures/functions", s.handleSignatures(sigs.Function, "selectors", selectorRE))
	mux.HandleFunc("GET /api/v2/signatures/events", s.handleSignatures(sigs.Event, "topics", topicRE))
	mux.HandleFunc("GET /api/v2/signatures/errors", s.handleSignatures(sigs.Error, "selectors", selectorRE))

	mux.HandleFunc("GET /api/v2/blocks", s.handleBlocks)
	mux.HandleFunc("GET /api/v2/blocks/{id}", s.handleBlock)
	mux.HandleFunc("GET /api/v2/blocks/{id}/transactions", s.handleBlockTxs)

	mux.HandleFunc("GET /api/v2/transactions", s.handleTxs)
	mux.HandleFunc("GET /api/v2/transactions/{hash}", s.handleTx)
	mux.HandleFunc("GET /api/v2/transactions/{hash}/token-transfers", s.handleTxTokenTransfers)
	mux.HandleFunc("GET /api/v2/transactions/{hash}/internal-transactions", s.handleTxInternal)
	mux.HandleFunc("GET /api/v2/transactions/{hash}/logs", s.handleTxLogs)
	mux.HandleFunc("GET /api/v2/transactions/{hash}/raw", s.handleTxRaw)

	mux.HandleFunc("GET /api/v2/addresses/{addr}", s.handleAddress)
	mux.HandleFunc("GET /api/v2/addresses/{addr}/transactions", s.handleAddressTxs)
	mux.HandleFunc("GET /api/v2/addresses/{addr}/token-transfers", s.handleAddressTokenTransfers)
	mux.HandleFunc("GET /api/v2/addresses/{addr}/internal-transactions", s.handleAddressInternal)
	mux.HandleFunc("GET /api/v2/addresses/{addr}/tokens", s.handleAddressTokens)
	mux.HandleFunc("GET /api/v2/addresses/{addr}/logs", s.handleAddressLogs)
	mux.HandleFunc("GET /api/v2/addresses/{addr}/coin-balance-history", s.handleBalanceHistory)

	mux.HandleFunc("GET /api/v2/tokens", s.handleTokens)
	mux.HandleFunc("GET /api/v2/tokens/{addr}", s.handleToken)
	mux.HandleFunc("GET /api/v2/tokens/{addr}/transfers", s.handleTokenTransfers)
	mux.HandleFunc("GET /api/v2/tokens/{addr}/holders", s.handleTokenHolders)

	mux.HandleFunc("GET /api/v2/smart-contracts", s.handleContracts)
	mux.HandleFunc("GET /api/v2/smart-contracts/{addr}", s.handleContract)
	mux.HandleFunc("POST /api/v2/smart-contracts/{addr}/verification/via/standard-input", s.handleVerify)
	mux.HandleFunc("GET /api/v2/smart-contracts/{addr}/verification/status", s.handleVerifyStatus)
	mux.HandleFunc("POST /api/v2/smart-contracts/{addr}/call", s.handleCall)
	mux.HandleFunc("GET /api/v2/compilers", s.handleCompilers)

	mux.HandleFunc("GET /api/v2/search", s.handleSearch)
	mux.HandleFunc("GET /api/v2/stream", s.handleStream)

	return httpx.WithNosniff(httpx.Recover(s.log, s.rateLimit(httpx.MethodCompat(mux, mux))))
}

// rateLimit applies the per-IP limits: verification submissions and
// read-contract calls have their own buckets, everything else shares the
// GET bucket. Health is exempt (docker and the watchdog probe it).
func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		lim, bucket := s.getLim, "get"
		if r.Method == http.MethodPost {
			switch {
			case strings.HasSuffix(r.URL.Path, "/verification/via/standard-input"):
				lim, bucket = s.verifyLim, "verify"
			case strings.HasSuffix(r.URL.Path, "/call"):
				lim, bucket = s.callLim, "call"
			}
		}
		ip := httpx.ClientIP(r, s.cfg.TrustedProxyIP)
		if ok, wait := lim.allow(bucket + "|" + ip); !ok {
			secs := int((wait + time.Second - 1) / time.Second)
			w.Header().Set("Retry-After", fmt.Sprint(secs))
			httpx.Error(w, r, http.StatusTooManyRequests, fmt.Sprintf("rate limit exceeded, retry in %d s", secs))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	var last int64 = -1
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE((SELECT last_block FROM indexer_state WHERE id = 1), -1)`).Scan(&last); err != nil {
		httpx.WriteHealth(w, r, http.StatusServiceUnavailable, "degraded", map[string]any{"db": err.Error()})
		return
	}
	detail := map[string]any{"role": "api", "last_block": nil, "node_block": nil, "lag": nil}
	if last >= 0 {
		detail["last_block"] = last
	}
	if httpx.HealthProbeIsInternal(r) {
		if h := s.nodeHead(ctx); h != nil {
			detail["node_block"] = *h
			if last >= 0 {
				detail["lag"] = max(0, *h-last)
			}
		}
	}
	httpx.WriteHealth(w, r, http.StatusOK, "ok", detail)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, r, http.StatusOK, map[string]any{
		"chain_id":       s.cfg.ChainID,
		"network_name":   s.cfg.NetworkName,
		"native_symbol":  s.cfg.NativeSymbol,
		"public_rpc_url": s.cfg.PublicRPCURL,
		"explorer_url":   s.cfg.ExplorerPublicURL,
	})
}

// nodeHead is the node's eth_blockNumber, cached for 5 s; nil when the node
// does not answer within 2 s.
func (s *Server) nodeHead(ctx context.Context) *int64 {
	s.headMu.Lock()
	if time.Since(s.headAt) < 5*time.Second {
		v := s.headVal
		s.headMu.Unlock()
		return v
	}
	s.headMu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var val *int64
	if n, err := s.rpc.BlockNumber(cctx); err == nil {
		v := int64(n)
		val = &v
	}
	s.headMu.Lock()
	s.headVal, s.headAt = val, time.Now()
	s.headMu.Unlock()
	return val
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.statsMu.Lock()
	cached := s.statsVal
	fresh := time.Since(s.statsAt) < 5*time.Second
	s.statsMu.Unlock()
	if cached == nil || !fresh {
		st := &Stats{ChainID: s.cfg.ChainID, NetworkName: s.cfg.NetworkName, NativeSymbol: s.cfg.NativeSymbol, PublicRPCURL: s.cfg.PublicRPCURL}
		var latest *int64
		var latestTime *time.Time
		err := s.pool.QueryRow(ctx, `
			SELECT (SELECT number FROM blocks ORDER BY number DESC LIMIT 1),
			       (SELECT timestamp FROM blocks ORDER BY number DESC LIMIT 1),
			       (SELECT count(*) FROM blocks),
			       (SELECT count(*) FROM transactions),
			       (SELECT count(*) FROM addresses),
			       (SELECT count(*) FROM addresses WHERE is_contract),
			       (SELECT count(*) FROM tokens),
			       (SELECT count(*) FROM verified_contracts)`).Scan(
			&latest, &latestTime, &st.TotalBlocks, &st.TotalTransactions, &st.TotalAddresses,
			&st.TotalContracts, &st.TotalTokens, &st.VerifiedContracts)
		if err != nil {
			s.dbError(w, r, err)
			return
		}
		st.LatestBlock = latest
		st.LatestBlockTime = isoPtr(latestTime)
		s.statsMu.Lock()
		s.statsVal, s.statsAt = st, time.Now()
		s.statsMu.Unlock()
		cached = st
	}
	out := *cached
	out.NodeBlock = s.nodeHead(ctx)
	httpx.WriteJSON(w, r, http.StatusOK, out)
}

// latestIndexed is the cursor (-1 before block 0).
func (s *Server) latestIndexed(ctx context.Context) int64 {
	var n int64 = -1
	_ = s.pool.QueryRow(ctx, `SELECT COALESCE((SELECT last_block FROM indexer_state WHERE id = 1), -1)`).Scan(&n)
	return n
}

// nudgeIndexer tells the indexer something may be waiting, never blocking
// the request. Nudges are coalesced to at most one every 2 s; one asked for
// inside that window is sent when it ends, never dropped.
func (s *Server) nudgeIndexer() {
	if s.cfg.IndexerHookURL == "" {
		return
	}
	const gap = 2 * time.Second
	s.nudgeMu.Lock()
	if wait := gap - time.Since(s.nudgeAt); wait > 0 {
		if !s.nudgeQueued {
			s.nudgeQueued = true
			time.AfterFunc(wait, func() {
				s.nudgeMu.Lock()
				s.nudgeQueued = false
				s.nudgeMu.Unlock()
				s.nudgeIndexer()
			})
		}
		s.nudgeMu.Unlock()
		return
	}
	s.nudgeAt = time.Now()
	s.nudgeMu.Unlock()
	go func() {
		req, err := http.NewRequest(http.MethodPost, s.cfg.IndexerHookURL, nil)
		if err != nil {
			return
		}
		if s.cfg.IndexerHookToken != "" {
			req.Header.Set("X-Hook-Token", s.cfg.IndexerHookToken)
		}
		resp, err := s.hookHTTP.Do(req)
		if err != nil {
			s.log.Debug("indexer nudge failed", "err", err)
			return
		}
		resp.Body.Close()
	}()
}

func (s *Server) dbError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	s.log.Error("query failed", "path", r.URL.Path, "err", err)
	httpx.Error(w, r, http.StatusInternalServerError, "database error")
}

func badRequest(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.Error(w, r, http.StatusBadRequest, msg)
}

func notFound(w http.ResponseWriter, r *http.Request, msg string) {
	httpx.Error(w, r, http.StatusNotFound, msg)
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

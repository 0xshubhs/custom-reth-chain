// Command rpc-gateway is the public entrance to the chain's JSON-RPC.
//
// WHY IT EXISTS. The node has one HTTP listener, and Blockscout needs that
// listener to serve debug_* and trace_* (internal transactions) and txpool_*
// (pending transactions). nginx used to hand the public RPC name straight to it,
// so the public got the same namespaces Blockscout does, unlimited batches,
// unbounded eth_getLogs and CORS for every website. nginx cannot read a
// JSON-RPC body, so it cannot filter by method. This can.
//
//	browser / wallet / engines -> Cloudflare -> nginx (your public RPC name)
//	    -> rpc-gateway 127.0.0.1:8548 -> node 127.0.0.1:8545 (HTTP) / :8546 (WS)
//	explorer   -> node 127.0.0.1:8545 directly, every namespace
//
// WHAT IT ENFORCES, per call (batches are checked call by call):
//   - only eth_*, net_*, web3_*; never the node-signing calls
//     (eth_sendTransaction, eth_sign*, eth_signTypedData*); eth_accounts is
//     answered with [] without asking the node
//   - a batch of at most RPC_GATEWAY_MAX_BATCH calls and a body of at most
//     RPC_GATEWAY_MAX_BODY_BYTES
//   - eth_getLogs / eth_newFilter without an address may span at most
//     RPC_GATEWAY_LOGS_MAX_RANGE blocks (with an address the node's own caps,
//     100,000 blocks and 20,000 logs, apply)
//   - CORS only for the allowed sites (RPC_GATEWAY_ALLOWED_ORIGINS); a
//     browser request from any other site is refused
//   - optionally, a per-client-IP rate (RPC_GATEWAY_RATE_PER_IP, off by
//     default; see the runbook before turning it on, the engines share one IP)
//
// Signed transactions (eth_sendRawTransaction) pass: that is how the engines,
// the faucet, wallets and forge deploys all send.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		slog.Error("rpc-gateway: bad configuration", "error", err)
		os.Exit(2)
	}
	origins := make([]string, 0, len(cfg.AllowedOrigins))
	for o := range cfg.AllowedOrigins {
		origins = append(origins, o)
	}
	sort.Strings(origins)
	slog.Info("rpc-gateway: starting",
		"listen", cfg.Listen, "upstream", cfg.Upstream, "upstreamWS", cfg.UpstreamWS,
		"maxBatch", cfg.MaxBatch, "maxBodyBytes", cfg.MaxBodyBytes,
		"logsMaxRange", cfg.MaxLogsRange, "logsMaxRangeFiltered", cfg.MaxLogsRangeFiltered,
		"ratePerIP", cfg.RatePerIP, "burstPerIP", cfg.BurstPerIP, "origins", origins)

	g := newGateway(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go g.logRefusals(ctx, 5*time.Minute)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           g,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: an eth_getLogs or eth_call may legitimately take
		// up to RPC_GATEWAY_UPSTREAM_TIMEOUT, and websockets live for hours.
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("rpc-gateway: server stopped", "error", err)
			os.Exit(1)
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	slog.Info("rpc-gateway: stopped")
}

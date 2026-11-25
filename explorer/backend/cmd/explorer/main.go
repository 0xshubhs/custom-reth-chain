// Command explorer runs the block explorer. The first argument selects the role:
//
//	explorer             the HTTP API (default; "api" or "server" say the same)
//	explorer indexer     the indexer (health + hook on PORT)
//	explorer admin ...   operator commands (status, check, reindex, label)
//
// See explorer/README.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"custom-reth-chain/explorer/backend/internal/admin"
	"custom-reth-chain/explorer/backend/internal/api"
	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/config"
	"custom-reth-chain/explorer/backend/internal/db"
	"custom-reth-chain/explorer/backend/internal/indexer"
)

func main() {
	role := "api"
	args := os.Args[1:]
	if len(args) > 0 {
		role, args = args[0], args[1:]
	}
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})).With("role", role)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	var err error
	switch role {
	case "api", "server":
		err = runAPI(ctx, log)
	case "indexer":
		err = runIndexer(ctx, log)
	case "admin":
		code := admin.Main(ctx, args, os.Stdout, os.Stderr, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
		stop()
		os.Exit(code)
	default:
		fmt.Fprintf(os.Stderr, "unknown role %q: use api, indexer or admin\n", role)
		os.Exit(2)
	}
	if err != nil {
		log.Error("exiting", "err", err)
		stop()
		os.Exit(1)
	}
}

func runAPI(ctx context.Context, log *slog.Logger) error {
	cfg, err := config.FromEnv(config.RoleServer)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()
	rpc := chainrpc.New(cfg.ChainRPCURL, chainrpc.Options{Timeout: 10 * time.Second, Retries: 1, MaxConcurrent: cfg.RPCMaxConcurrent})
	if cfg.ChainID == 0 {
		id, err := rpc.WaitChainID(ctx, log)
		if err != nil {
			return err
		}
		cfg.ChainID = id
		log.Info("CHAIN_ID unset: using the node's chain id", "chain_id", id)
	}

	// The node must be the configured chain. A node that does not answer yet
	// does not stop the API (it serves what is indexed); a different chain
	// does.
	chainErr := make(chan error, 1)
	go func() {
		backoff := time.Second
		for {
			id, err := rpc.ChainID(ctx)
			if err == nil {
				if id != cfg.ChainID {
					chainErr <- fmt.Errorf("node at %s is chain %d, CHAIN_ID is %d", cfg.ChainRPCURL, id, cfg.ChainID)
				}
				return
			}
			if ctx.Err() != nil {
				return
			}
			log.Warn("node not answering eth_chainId yet", "err", err, "retry_in", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, time.Minute)
		}
	}()

	srv := api.New(cfg, pool, rpc, log)
	bg, cancelBG := context.WithCancel(context.Background())
	bgDone := make(chan struct{})
	go func() { defer close(bgDone); srv.Run(bg) }()

	hs := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute, // a 20 MiB verification upload on a slow link
		WriteTimeout:      2 * time.Minute, // the stream clears its own deadline
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
	serveErr := make(chan error, 1)
	go func() {
		log.Info("API listening", "addr", cfg.Addr(), "chain_id", cfg.ChainID)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-serveErr:
	case runErr = <-chainErr:
	}
	log.Info("shutting down")
	srv.Close()
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = hs.Shutdown(sctx)
	cancelBG()
	<-bgDone
	return runErr
}

func runIndexer(ctx context.Context, log *slog.Logger) error {
	cfg, err := config.FromEnv(config.RoleIndexer)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()
	rpc := chainrpc.New(cfg.ChainRPCURL, chainrpc.Options{Timeout: 60 * time.Second, MaxConcurrent: cfg.RPCMaxConcurrent})
	if cfg.ChainID == 0 {
		id, err := rpc.WaitChainID(ctx, log)
		if err != nil {
			return err
		}
		cfg.ChainID = id
		log.Info("CHAIN_ID unset: using the node's chain id", "chain_id", id)
	}
	if cfg.Traces == "auto" {
		for {
			ok, err := rpc.ServesTraces(ctx)
			if err == nil {
				if ok {
					cfg.Traces = "required"
				} else {
					cfg.Traces = "off"
					log.Warn("the node has no debug_traceBlockByNumber: indexing without internal transactions (TRACES=off)")
				}
				break
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Warn("probing the node for call traces", "err", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
	}
	ix := indexer.New(cfg, pool, rpc, log)

	hs := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           ix.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	serveErr := make(chan error, 1)
	go func() {
		log.Info("indexer health and hook listening", "addr", cfg.Addr())
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	runErr := make(chan error, 1)
	go func() { runErr <- ix.Run(runCtx) }()

	var err2 error
	select {
	case <-ctx.Done():
		cancelRun()
		err2 = <-runErr
	case err2 = <-runErr:
	case err2 = <-serveErr:
		cancelRun()
		<-runErr
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = hs.Shutdown(sctx)
	return err2
}

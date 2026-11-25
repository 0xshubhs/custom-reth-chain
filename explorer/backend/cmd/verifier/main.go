// Command verifier is the explorer's contract verification service: it
// compiles submitted Solidity standard JSON with the official solc wasm build
// and compares the result with the code on chain. Internal only (no host
// port); the explorer API calls it. See explorer/docs/design.md, "Verifier
// service", for the interface and the environment.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"custom-reth-chain/explorer/backend/internal/verifier"
)

// shutdownGrace is how long in-flight requests get after SIGTERM before
// their compiles are killed. docker stop waits 10 s by default before it
// kills the container anyway; compose can raise that with stop_grace_period.
const shutdownGrace = 25 * time.Second

type config struct {
	addr           string
	cacheDir       string
	binBase        string
	download       bool
	bundledDir     string
	compileTimeout time.Duration
	maxCompiles    int
	nodeBin        string
	runnerPath     string
	maxInput       int64
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("verifier stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if _, err := exec.LookPath(cfg.nodeBin); err != nil {
		return fmt.Errorf("NODE_BIN %q: %w", cfg.nodeBin, err)
	}
	if _, err := os.Stat(cfg.runnerPath); err != nil {
		return fmt.Errorf("RUNNER_PATH: %w", err)
	}

	compilers, err := verifier.NewCompilers(verifier.CompilersConfig{
		BundledDir: cfg.bundledDir,
		Download:   cfg.download,
		BinBase:    cfg.binBase,
		CacheDir:   cfg.cacheDir,
		Logger:     log,
	})
	if err != nil {
		return err
	}
	bundled := compilers.Bundled()
	if len(bundled) == 0 && !cfg.download {
		log.Warn("no bundled compilers and SOLC_DOWNLOAD=off: every verification will fail")
	}
	runner := verifier.NewRunner(cfg.nodeBin, cfg.runnerPath, cfg.compileTimeout, cfg.maxCompiles)
	svc := verifier.NewService(compilers, runner, log)

	// Request contexts derive from base, so cancelling it at the end of the
	// grace period kills any compile still running.
	base, killCompiles := context.WithCancel(context.Background())
	defer killCompiles()
	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           svc.Handler(cfg.maxInput),
		BaseContext:       func(net.Listener) context.Context { return base },
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute, // a 20 MB body on the docker network takes well under a second
		// No WriteTimeout: an answer comes after the queue wait plus a compile
		// of up to COMPILE_TIMEOUT_SECONDS, and the caller's disconnect already
		// cancels the work.
		IdleTimeout: 2 * time.Minute,
	}

	stop, cancelSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancelSignals()
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("verifier listening", "addr", cfg.addr, "bundled", strings.Join(bundled, ","),
		"downloads", cfg.download, "max_concurrent_compiles", cfg.maxCompiles,
		"compile_timeout", cfg.compileTimeout)

	select {
	case err := <-errc:
		return err
	case <-stop.Done():
	}
	log.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Warn("in-flight requests did not finish, stopping their compiles", "err", err)
		killCompiles()
		_ = srv.Close()
	}
	if err := <-errc; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func loadConfig() (config, error) {
	var errs []error
	str := func(key, def string) string {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
		return def
	}
	num := func(key string, def, min int64) int64 {
		v := str(key, "")
		if v == "" {
			return def
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < min {
			errs = append(errs, fmt.Errorf("%s=%q: want a whole number of at least %d", key, v, min))
			return def
		}
		return n
	}
	var download bool
	switch d := strings.ToLower(str("SOLC_DOWNLOAD", "off")); d {
	case "off", "false", "0", "no":
	case "on", "true", "1", "yes":
		download = true
	default:
		errs = append(errs, fmt.Errorf("SOLC_DOWNLOAD=%q: want on or off", d))
	}
	cfg := config{
		addr:           net.JoinHostPort(str("HOST", "0.0.0.0"), str("PORT", "8080")),
		cacheDir:       str("COMPILER_CACHE_DIR", "/var/cache/solc"),
		binBase:        str("SOLC_BIN_BASE", "https://binaries.soliditylang.org"),
		download:       download,
		bundledDir:     str("SOLC_BUNDLED_DIR", "/app/solc"),
		compileTimeout: time.Duration(num("COMPILE_TIMEOUT_SECONDS", 900, 1)) * time.Second,
		maxCompiles:    int(num("MAX_CONCURRENT_COMPILES", 1, 1)),
		nodeBin:        str("NODE_BIN", "node"),
		runnerPath:     str("RUNNER_PATH", "/app/runner.mjs"),
		maxInput:       num("MAX_INPUT_BYTES", 20971520, 1024),
	}
	if download && !strings.HasPrefix(cfg.binBase, "https://") && !strings.HasPrefix(cfg.binBase, "http://") {
		errs = append(errs, fmt.Errorf("SOLC_BIN_BASE=%q: want an http(s) URL", cfg.binBase))
	}
	return cfg, errors.Join(errs...)
}

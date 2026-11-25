// Package config reads the engine's environment (explorer/docs/design.md,
// "Environment"). Load fails fast: every missing or malformed variable is
// reported at once, before anything connects anywhere.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Role selects what the binary runs.
type Role string

const (
	RoleServer  Role = "server"
	RoleIndexer Role = "indexer"
	RoleAdmin   Role = "admin"
)

// Config is the parsed environment.
type Config struct {
	Role Role

	// Both roles.
	DatabaseURL      string
	ChainRPCURL      string
	ChainID          uint64
	Host             string
	Port             int
	RPCMaxConcurrent int

	// API.
	VerifierURL        string
	IndexerHookURL     string
	IndexerHookToken   string
	TrustedProxyIP     string
	RateLimitPerMin    int
	VerifyLimitPerHour int
	CallLimitPerMin    int
	NetworkName        string
	NativeSymbol       string
	PublicRPCURL       string
	ExplorerPublicURL  string

	// Indexer.
	ChainWSURL          string
	SafetyCheckSeconds  int
	Traces              string // "required" or "off"
	BackfillConcurrency int
}

// Addr is the listen address.
func (c *Config) Addr() string { return net.JoinHostPort(c.Host, strconv.Itoa(c.Port)) }

// TracesRequired reports whether a block may only be committed with traces.
func (c *Config) TracesRequired() bool { return c.Traces == "required" }

// FromEnv loads the configuration for role from the process environment.
func FromEnv(role Role) (*Config, error) { return Load(role, os.Getenv) }

// Load builds the configuration for role from getenv.
func Load(role Role, getenv func(string) string) (*Config, error) {
	p := parser{get: getenv}
	c := &Config{Role: role}

	c.DatabaseURL = p.required("DATABASE_URL")
	c.ChainRPCURL = p.url("CHAIN_RPC_URL", "", true, "http", "https")
	// 0 (unset) means "whatever the node answers": the explorer then works with
	// any EVM chain from its RPC URL alone. Set it to refuse any other chain.
	c.ChainID = p.uint64("CHAIN_ID", 0)
	c.Host = p.str("HOST", "0.0.0.0")
	c.Port = p.intRange("PORT", 8080, 1, 65535)
	c.RPCMaxConcurrent = p.intRange("CHAIN_RPC_MAX_CONCURRENCY", 4, 1, 256)

	switch role {
	case RoleServer:
		c.VerifierURL = p.url("VERIFIER_URL", "http://verifier:8080", false, "http", "https")
		c.IndexerHookURL = p.url("INDEXER_HOOK_URL", "http://indexer:8080/hooks/new-block", false, "http", "https")
		c.IndexerHookToken = p.str("INDEXER_HOOK_TOKEN", "")
		c.TrustedProxyIP = p.str("TRUSTED_PROXY_IP", "127.0.0.1")
		c.RateLimitPerMin = p.intRange("RATE_LIMIT_PER_MIN", 240, 1, 1_000_000)
		c.VerifyLimitPerHour = p.intRange("VERIFY_LIMIT_PER_HOUR", 20, 1, 1_000_000)
		c.CallLimitPerMin = p.intRange("CALL_LIMIT_PER_MIN", 60, 1, 1_000_000)
		c.NetworkName = p.str("NETWORK_NAME", "Meowchain")
		c.NativeSymbol = p.str("NATIVE_SYMBOL", "ETH")
		c.PublicRPCURL = p.str("PUBLIC_RPC_URL", "http://localhost:8548")
		c.ExplorerPublicURL = p.str("EXPLORER_PUBLIC_URL", "http://localhost:3000")
	case RoleIndexer:
		// The websocket push is optional: without it the indexer polls the
		// node every SAFETY_CHECK_SECONDS (5 by default then, 300 with a push).
		c.ChainWSURL = p.url("CHAIN_WS_URL", "", false, "ws", "wss")
		defCheck := 300
		if c.ChainWSURL == "" {
			defCheck = 5
		}
		c.SafetyCheckSeconds = p.intRange("SAFETY_CHECK_SECONDS", defCheck, 0, 86400*7)
		c.IndexerHookToken = p.str("INDEXER_HOOK_TOKEN", "")
		// auto: use call traces (internal transactions) when the node serves
		// debug_traceBlockByNumber, go on without them when it does not.
		c.Traces = p.oneOf("TRACES", "auto", "auto", "required", "off")
		c.BackfillConcurrency = p.intRange("BACKFILL_CONCURRENCY", 4, 1, 64)
	case RoleAdmin:
		// The admin commands read like the indexer and nudge it like the API.
		c.Traces = p.oneOf("TRACES", "auto", "auto", "required", "off")
		c.IndexerHookURL = p.url("INDEXER_HOOK_URL", "http://indexer:8080/hooks/new-block", false, "http", "https")
		c.IndexerHookToken = p.str("INDEXER_HOOK_TOKEN", "")
	default:
		p.errs = append(p.errs, fmt.Errorf("unknown role %q", role))
	}

	if len(p.errs) > 0 {
		return nil, fmt.Errorf("configuration: %w", errors.Join(p.errs...))
	}
	return c, nil
}

type parser struct {
	get  func(string) string
	errs []error
}

func (p *parser) raw(key string) (string, bool) {
	v := strings.TrimSpace(p.get(key))
	return v, v != ""
}

func (p *parser) str(key, def string) string {
	if v, ok := p.raw(key); ok {
		return v
	}
	return def
}

func (p *parser) required(key string) string {
	v, ok := p.raw(key)
	if !ok {
		p.errs = append(p.errs, fmt.Errorf("%s is required", key))
	}
	return v
}

func (p *parser) url(key, def string, required bool, schemes ...string) string {
	v, ok := p.raw(key)
	if !ok {
		if required {
			p.errs = append(p.errs, fmt.Errorf("%s is required", key))
		}
		return def
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		p.errs = append(p.errs, fmt.Errorf("%s: not a URL: %q", key, v))
		return v
	}
	for _, s := range schemes {
		if u.Scheme == s {
			return v
		}
	}
	p.errs = append(p.errs, fmt.Errorf("%s: scheme must be one of %s, got %q", key, strings.Join(schemes, ", "), u.Scheme))
	return v
}

func (p *parser) intRange(key string, def, lo, hi int) int {
	v, ok := p.raw(key)
	if !ok {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		p.errs = append(p.errs, fmt.Errorf("%s: want an integer in %d..%d, got %q", key, lo, hi, v))
		return def
	}
	return n
}

func (p *parser) uint64(key string, def uint64) uint64 {
	v, ok := p.raw(key)
	if !ok {
		return def
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil || n == 0 {
		p.errs = append(p.errs, fmt.Errorf("%s: want a positive integer, got %q", key, v))
		return def
	}
	return n
}

func (p *parser) oneOf(key, def string, allowed ...string) string {
	v, ok := p.raw(key)
	if !ok {
		return def
	}
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	p.errs = append(p.errs, fmt.Errorf("%s: want one of %s, got %q", key, strings.Join(allowed, ", "), v))
	return def
}

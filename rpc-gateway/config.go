package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Config is everything the gateway reads from its environment. Every value
// has a default that is safe for the chain server, so the gateway starts with
// no environment at all.
type Config struct {
	// Listen is where the gateway answers. Loopback: nginx is the only thing
	// that should reach it, exactly as nginx is the only thing that reaches
	// the node.
	Listen string

	// Upstream is the node's HTTP JSON-RPC, which keeps serving every
	// namespace (debug, trace, txpool, ...) to Blockscout on loopback.
	Upstream string
	// UpstreamWS is the node's websocket JSON-RPC.
	UpstreamWS string

	// AllowedOrigins are the browser origins that may call the RPC. A request
	// with no Origin header (a server, a wallet's background page, curl) is not
	// a browser cross-site call and is always allowed through to the method
	// checks.
	AllowedOrigins map[string]bool

	MaxBodyBytes int64
	MaxBatch     int

	// MaxLogsRange caps how many blocks one eth_getLogs / eth_newFilter may
	// span when it names no address. A query that names a contract address is
	// left to the node's own caps (100,000 blocks, 20,000 logs), because the
	// engines look up their own contracts' events from block 0.
	MaxLogsRange uint64
	// MaxLogsRangeFiltered caps address-filtered queries too. 0 leaves them to
	// the node.
	MaxLogsRangeFiltered uint64

	// RatePerIP is the sustained JSON-RPC calls per second one client IP may
	// make (each call inside a batch counts). 0 turns per-IP limiting off.
	RatePerIP  float64
	BurstPerIP int
	// Exempt client IPs/CIDRs skip the per-IP limit (the app server, whose
	// engines all reach the RPC from one address).
	Exempt []*net.IPNet
	// TrustedProxies are the peers allowed to name the real client in
	// CF-Connecting-IP / X-Real-IP / X-Forwarded-For. Default: loopback, where
	// nginx connects from.
	TrustedProxies []*net.IPNet

	UpstreamTimeout time.Duration
}

// defaultOrigins are the websites allowed to call the RPC from a browser when
// RPC_GATEWAY_ALLOWED_ORIGINS is not set: the explorer that ships with this
// repo, on its default local port. Add your own sites (comma separated) with
// RPC_GATEWAY_ALLOWED_ORIGINS. Wallets and server-side callers send no Origin
// and are not affected.
var defaultOrigins = []string{
	"http://localhost:3000",
	"http://127.0.0.1:3000",
}

func loadConfig(getenv func(string) string) (*Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	cfg := &Config{
		Listen:     get("RPC_GATEWAY_LISTEN", "127.0.0.1:8548"),
		Upstream:   get("RPC_GATEWAY_UPSTREAM", "http://127.0.0.1:8545"),
		UpstreamWS: get("RPC_GATEWAY_UPSTREAM_WS", "ws://127.0.0.1:8546"),
	}

	origins := defaultOrigins
	if v := strings.TrimSpace(getenv("RPC_GATEWAY_ALLOWED_ORIGINS")); v != "" {
		origins = splitList(v)
	}
	cfg.AllowedOrigins = make(map[string]bool, len(origins))
	for _, o := range origins {
		cfg.AllowedOrigins[strings.TrimRight(o, "/")] = true
	}

	var err error
	if cfg.MaxBodyBytes, err = getInt64(get, "RPC_GATEWAY_MAX_BODY_BYTES", 5<<20); err != nil {
		return nil, err
	}
	maxBatch, err := getInt64(get, "RPC_GATEWAY_MAX_BATCH", 25)
	if err != nil {
		return nil, err
	}
	cfg.MaxBatch = int(maxBatch)
	logsRange, err := getInt64(get, "RPC_GATEWAY_LOGS_MAX_RANGE", 2000)
	if err != nil {
		return nil, err
	}
	cfg.MaxLogsRange = uint64(logsRange)
	logsRangeFiltered, err := getInt64(get, "RPC_GATEWAY_LOGS_MAX_RANGE_FILTERED", 0)
	if err != nil {
		return nil, err
	}
	cfg.MaxLogsRangeFiltered = uint64(logsRangeFiltered)

	if v := get("RPC_GATEWAY_RATE_PER_IP", "0"); v != "" {
		if cfg.RatePerIP, err = strconv.ParseFloat(v, 64); err != nil || cfg.RatePerIP < 0 {
			return nil, fmt.Errorf("RPC_GATEWAY_RATE_PER_IP: want a number >= 0, got %q", v)
		}
	}
	burst, err := getInt64(get, "RPC_GATEWAY_BURST_PER_IP", 0)
	if err != nil {
		return nil, err
	}
	cfg.BurstPerIP = int(burst)
	if cfg.RatePerIP > 0 && cfg.BurstPerIP <= 0 {
		// Room for one page load's worth of calls on top of the steady rate.
		cfg.BurstPerIP = int(cfg.RatePerIP*4) + cfg.MaxBatch
	}

	if cfg.Exempt, err = parseNets(getenv("RPC_GATEWAY_EXEMPT_IPS")); err != nil {
		return nil, fmt.Errorf("RPC_GATEWAY_EXEMPT_IPS: %w", err)
	}
	trusted := get("RPC_GATEWAY_TRUSTED_PROXIES", "127.0.0.0/8,::1/128")
	if cfg.TrustedProxies, err = parseNets(trusted); err != nil {
		return nil, fmt.Errorf("RPC_GATEWAY_TRUSTED_PROXIES: %w", err)
	}

	if cfg.UpstreamTimeout, err = time.ParseDuration(get("RPC_GATEWAY_UPSTREAM_TIMEOUT", "120s")); err != nil {
		return nil, fmt.Errorf("RPC_GATEWAY_UPSTREAM_TIMEOUT: %w", err)
	}

	if cfg.MaxBodyBytes <= 0 || cfg.MaxBatch <= 0 {
		return nil, fmt.Errorf("RPC_GATEWAY_MAX_BODY_BYTES and RPC_GATEWAY_MAX_BATCH must be positive")
	}
	return cfg, nil
}

func getInt64(get func(string, string) string, key string, def int64) (int64, error) {
	v := get(key, strconv.FormatInt(def, 10))
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s: want a whole number >= 0, got %q", key, v)
	}
	return n, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseNets reads a comma-separated list of IPs and CIDRs. A bare IP becomes a
// single-address network.
func parseNets(s string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, p := range splitList(s) {
		if !strings.Contains(p, "/") {
			ip := net.ParseIP(p)
			if ip == nil {
				return nil, fmt.Errorf("not an IP or CIDR: %q", p)
			}
			bits := 128
			if ip.To4() != nil {
				ip = ip.To4()
				bits = 32
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(p)
		if err != nil {
			return nil, fmt.Errorf("not an IP or CIDR: %q", p)
		}
		out = append(out, n)
	}
	return out, nil
}

func inNets(ip net.IP, nets []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultsAndRequired(t *testing.T) {
	c, err := Load(RoleServer, env(map[string]string{"DATABASE_URL": "postgres://x/y", "CHAIN_RPC_URL": "http://node:8645"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.ChainID != 0 || c.Port != 8080 || c.RateLimitPerMin != 240 || c.VerifyLimitPerHour != 20 || c.CallLimitPerMin != 60 ||
		c.TrustedProxyIP != "127.0.0.1" || c.NativeSymbol != "ETH" || c.VerifierURL != "http://verifier:8080" {
		t.Errorf("defaults: %+v", c)
	}

	// No websocket: allowed, the indexer polls every 5 seconds instead.
	c, err = Load(RoleIndexer, env(map[string]string{"DATABASE_URL": "postgres://x/y", "CHAIN_RPC_URL": "http://node:8645"}))
	if err != nil || c.ChainWSURL != "" || c.SafetyCheckSeconds != 5 {
		t.Errorf("indexer without CHAIN_WS_URL: %+v %v", c, err)
	}
	c, err = Load(RoleIndexer, env(map[string]string{"DATABASE_URL": "postgres://x/y", "CHAIN_RPC_URL": "http://n", "CHAIN_WS_URL": "ws://n:8646"}))
	if err != nil || c.Traces != "auto" || c.SafetyCheckSeconds != 300 || c.BackfillConcurrency != 4 {
		t.Errorf("indexer defaults: %+v %v", c, err)
	}

	// Everything wrong at once is reported at once.
	_, err = Load(RoleIndexer, env(map[string]string{"CHAIN_RPC_URL": "ftp://x", "CHAIN_WS_URL": "http://n", "TRACES": "maybe", "PORT": "0", "CHAIN_ID": "abc"}))
	for _, want := range []string{"DATABASE_URL is required", "CHAIN_RPC_URL: scheme", "CHAIN_WS_URL: scheme", "TRACES", "PORT", "CHAIN_ID"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
}

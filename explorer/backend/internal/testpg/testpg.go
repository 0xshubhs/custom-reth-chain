// Package testpg gives tests a fresh, migrated explorer database.
//
// Set EXPLORER_TEST_PG to a Postgres URL whose role may create databases
// (for example postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable).
// Each call creates explorer_engine_test_<random>, applies the migrations and
// drops it when the test ends. Without the variable the test is skipped.
package testpg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"custom-reth-chain/explorer/backend/internal/db"
)

// New returns a pool on a fresh database and that database's URL.
func New(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	base := os.Getenv("EXPLORER_TEST_PG")
	if base == "" {
		t.Skip("EXPLORER_TEST_PG not set: skipping database test")
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("EXPLORER_TEST_PG: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "explorer_engine_test_" + hex.EncodeToString(b)
	u.Path = "/" + name
	dbURL := u.String()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := db.Open(ctx, dbURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin := *u
		admin.Path = "/postgres"
		c, err := pgx.Connect(ctx, admin.String())
		if err != nil {
			t.Logf("drop %s: %v", name, err)
			return
		}
		defer c.Close(ctx)
		if _, err := c.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("drop %s: %v", name, err)
		}
	})
	return pool, dbURL
}

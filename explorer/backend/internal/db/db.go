// Package db opens the explorer database and applies its schema.
//
// The schema lives in migrations/*.sql, embedded into the binary, applied in
// file-name order and recorded in schema_migrations. Both the API and the
// indexer call Open at start-up; a Postgres advisory lock makes the first one
// apply the migrations while the other waits, so they can start in any order.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrateLockKey is the pg_advisory_lock key held while migrating. Any fixed
// number works; it only has to differ from the indexer's writer lock.
const migrateLockKey int64 = 7_026_071_501

// IndexerLockKey is the session advisory lock the single writing indexer holds.
const IndexerLockKey int64 = 7_026_071_502

// Open connects, creating the database first when it does not exist and the
// credentials are allowed to, then applies any pending migrations.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	if err := ensureDatabase(ctx, databaseURL); err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	if cfg.MaxConns < 8 {
		cfg.MaxConns = 8
	}
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// ensureDatabase creates the target database when it is missing. It connects
// to the `postgres` maintenance database with the same credentials; a login
// without CREATEDB gets a clear error naming the statement to run instead.
func ensureDatabase(ctx context.Context, databaseURL string) error {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := pgx.Connect(cctx, databaseURL)
	if err == nil {
		return conn.Close(ctx)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "3D000" { // invalid_catalog_name
		return fmt.Errorf("connect: %w", err)
	}

	u, perr := url.Parse(databaseURL)
	if perr != nil {
		return fmt.Errorf("parse DATABASE_URL: %w", perr)
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		return fmt.Errorf("DATABASE_URL names no database")
	}
	u.Path = "/postgres"
	admin, aerr := pgx.Connect(cctx, u.String())
	if aerr != nil {
		return fmt.Errorf("database %q does not exist and the maintenance database is unreachable (%v); run: CREATE DATABASE %s", name, aerr, pgx.Identifier{name}.Sanitize())
	}
	defer admin.Close(ctx)
	if _, cerr := admin.Exec(cctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); cerr != nil {
		if errors.As(cerr, &pgErr) && pgErr.Code == "42P04" { // duplicate_database: someone else won the race
			return nil
		}
		return fmt.Errorf("database %q does not exist and could not be created (%v); run: CREATE DATABASE %s", name, cerr, pgx.Identifier{name}.Sanitize())
	}
	return nil
}

// Migrate applies every embedded migration not yet recorded.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrateLockKey); err != nil {
		return fmt.Errorf("migrate: lock: %w", err)
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrateLockKey) //nolint:errcheck

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("migrate: bookkeeping table: %w", err)
	}

	names, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		version := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
		var done bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", version).Scan(&done); err != nil {
			return fmt.Errorf("migrate %s: %w", version, err)
		}
		if done {
			continue
		}
		body, err := migrationFiles.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("migrate %s: begin: %w", version, err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return fmt.Errorf("migrate %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", version); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return fmt.Errorf("migrate %s: record: %w", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("migrate %s: commit: %w", version, err)
		}
	}
	return nil
}

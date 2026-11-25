package indexer

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/config"
)

// IndexRange indexes blocks from..to on c exactly as the indexer would
// (same fetch, same per-block transaction, same parent-hash check). The
// cursor must be at from-1. The caller holds the writer lock on c. Used by
// the admin reindex command.
func IndexRange(ctx context.Context, cfg *config.Config, c *pgx.Conn, rpc *chainrpc.Client, log *slog.Logger, from, to uint64) error {
	conc := cfg.BackfillConcurrency
	if conc <= 0 {
		conc = 4
	}
	ix := &Indexer{cfg: &config.Config{BackfillConcurrency: conc, Traces: cfg.Traces}, rpc: rpc, log: log, f: newFetcher(rpc, cfg.TracesRequired())}
	cur, err := loadCursor(ctx, c)
	if err != nil {
		return err
	}
	if cur.last != int64(from)-1 {
		return fmt.Errorf("cursor is at %d, not %d", cur.last, int64(from)-1)
	}
	return ix.runRange(ctx, c, from, to)
}

// Cursor returns the stored cursor (-1 before block 0) and its hash.
func Cursor(ctx context.Context, c *pgx.Conn) (int64, string, error) {
	cur, err := loadCursor(ctx, c)
	return cur.last, cur.hash, err
}

// Package indexer copies the chain into the explorer database
// (explorer/docs/design.md, "Indexer").
//
// It sleeps until something says a block was sealed (start-up, the node's
// newHeads push, POST /hooks/new-block, the safety check), then runs one
// catch-up: every block from the cursor to the node's head, fetched ahead in
// parallel but committed strictly in order, one database transaction per
// block that also moves the cursor. A trigger that arrives during a run makes
// the run go round once more; triggers never run concurrently.
package indexer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/config"
	"custom-reth-chain/explorer/backend/internal/db"
)

// ErrFatal marks conditions the indexer must not work around: a different
// chain behind the configured URL. The process exits.
var ErrFatal = errors.New("fatal")

// Indexer is the single writer of the explorer database.
type Indexer struct {
	cfg  *config.Config
	pool *pgxpool.Pool
	rpc  *chainrpc.Client
	log  *slog.Logger
	f    *fetcher

	trigger   chan struct{}
	nodeHead  atomic.Int64
	lastBlock atomic.Int64
	isWriter  atomic.Bool
	sub       *chainrpc.HeadSubscriber
	lastRun   atomic.Int64 // unix seconds of the last finished catch-up
	lastErr   atomic.Value // string

	// connect opens the writer's dedicated session (tests may replace it).
	connect func(ctx context.Context) (*pgx.Conn, error)
}

// New builds an indexer.
func New(cfg *config.Config, pool *pgxpool.Pool, rpc *chainrpc.Client, log *slog.Logger) *Indexer {
	ix := &Indexer{
		cfg:     cfg,
		pool:    pool,
		rpc:     rpc,
		log:     log,
		f:       newFetcher(rpc, cfg.TracesRequired()),
		trigger: make(chan struct{}, 1),
	}
	ix.nodeHead.Store(-1)
	ix.lastBlock.Store(-1)
	ix.lastErr.Store("")
	ix.connect = func(ctx context.Context) (*pgx.Conn, error) { return pgx.Connect(ctx, cfg.DatabaseURL) }
	return ix
}

// Poke asks for a catch-up. Never blocks: a pending request absorbs it.
func (ix *Indexer) Poke() {
	select {
	case ix.trigger <- struct{}{}:
	default:
	}
}

func (ix *Indexer) noteHead(n uint64) {
	for {
		cur := ix.nodeHead.Load()
		if int64(n) <= cur || ix.nodeHead.CompareAndSwap(cur, int64(n)) {
			return
		}
	}
}

// Run indexes until ctx is cancelled. It returns an error wrapping ErrFatal
// for a chain mismatch, nil on shutdown.
func (ix *Indexer) Run(ctx context.Context) error {
	if err := ix.checkChainID(ctx); err != nil {
		return err
	}
	for {
		err := ix.runAsWriter(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, ErrFatal) {
			return err
		}
		ix.log.Error("indexer writer session ended, starting over", "err", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

// checkChainID waits for the node and refuses a different chain.
func (ix *Indexer) checkChainID(ctx context.Context) error {
	backoff := time.Second
	for {
		id, err := ix.rpc.ChainID(ctx)
		if err == nil {
			if id != ix.cfg.ChainID {
				return fmt.Errorf("%w: node at %s is chain %d, CHAIN_ID is %d", ErrFatal, ix.rpc.URL(), id, ix.cfg.ChainID)
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ix.log.Warn("node not answering eth_chainId yet", "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

// AcquireWriterLock opens a dedicated session and takes the single-writer
// advisory lock on it, waiting while another process holds it (the indexer
// holds it only while catching up). Every write goes through this session,
// so losing the session loses the right to write at the same moment.
// Closing the connection releases the lock.
func AcquireWriterLock(ctx context.Context, connect func(context.Context) (*pgx.Conn, error), log *slog.Logger, who string) (*pgx.Conn, error) {
	logged := false
	for {
		c, err := connect(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			log.Warn("writer session: cannot connect", "err", err)
		} else {
			var got bool
			if err := c.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, db.IndexerLockKey).Scan(&got); err == nil && got {
				if _, err := c.Exec(ctx, `SET application_name = 'explorer-`+who+`'`); err != nil {
					log.Warn("writer session: application_name", "err", err)
				}
				return c, nil
			} else if err != nil {
				log.Warn("writer session: lock query failed", "err", err)
			} else if !logged {
				log.Info("another process holds the indexer writer lock, waiting", "who", who)
				logged = true
			}
			c.Close(context.Background())
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// tryLock takes the writer lock on c without waiting.
func tryLock(ctx context.Context, c *pgx.Conn) (bool, error) {
	var got bool
	err := c.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, db.IndexerLockKey).Scan(&got)
	return got, err
}

func unlock(c *pgx.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = c.Exec(ctx, `SELECT pg_advisory_unlock($1)`, db.IndexerLockKey)
}

// runAsWriter is one database session. The writer lock is held for each
// catch-up and released between them, so the admin commands (and a second
// indexer) wait at most one catch-up, while two writers can never overlap.
func (ix *Indexer) runAsWriter(ctx context.Context) error {
	var c *pgx.Conn
	for {
		var err error
		if c, err = ix.connect(ctx); err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil
		}
		ix.log.Warn("database session: cannot connect", "err", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(5 * time.Second):
		}
	}
	defer c.Close(context.Background())
	if _, err := c.Exec(ctx, `SET application_name = 'explorer-indexer'`); err != nil {
		return err
	}
	if err := ix.checkGenesis(ctx, c); err != nil {
		return err
	}

	sctx, stop := context.WithCancel(ctx)
	defer stop()
	var wg sync.WaitGroup
	defer wg.Wait()
	defer stop()

	if ix.cfg.ChainWSURL != "" {
		ix.sub = &chainrpc.HeadSubscriber{
			URL: ix.cfg.ChainWSURL,
			Log: ix.log,
			OnHead: func(n uint64) {
				ix.noteHead(n)
				ix.Poke()
			},
			OnSubscribed: func(reconnect bool) {
				if reconnect {
					ix.log.Info("newHeads resubscribed, catching up")
				} else {
					ix.log.Info("newHeads subscribed", "url", ix.cfg.ChainWSURL)
				}
				ix.Poke()
			},
		}
		wg.Add(1)
		go func() { defer wg.Done(); ix.sub.Run(sctx) }()
	}
	if s := ix.cfg.SafetyCheckSeconds; s > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := time.NewTicker(time.Duration(s) * time.Second)
			defer t.Stop()
			for {
				select {
				case <-sctx.Done():
					return
				case <-t.C:
					ix.Poke()
				}
			}
		}()
	}

	ix.Poke() // start-up trigger
	backoff := 2 * time.Second
	waitLogged := false
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ix.trigger:
		}
		got, err := tryLock(ctx, c)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("writer lock: %w", err)
		}
		if !got {
			// Someone else is writing (an admin reindex, another indexer):
			// look again shortly. The trigger is not lost.
			if !waitLogged {
				ix.log.Info("indexer writer lock held elsewhere, waiting")
				waitLogged = true
			}
			time.AfterFunc(time.Second, ix.Poke)
			continue
		}
		waitLogged = false
		ix.isWriter.Store(true)
		err = ix.catchUp(ctx, c)
		ix.isWriter.Store(false)
		unlock(c)
		ix.lastRun.Store(time.Now().Unix())
		if err == nil {
			ix.lastErr.Store("")
			backoff = 2 * time.Second
			continue
		}
		if ctx.Err() != nil {
			return nil
		}
		ix.lastErr.Store(err.Error())
		if errors.Is(err, ErrFatal) || c.IsClosed() {
			return err
		}
		ix.log.Error("catch-up failed, retrying", "err", err, "in", backoff)
		time.AfterFunc(backoff, ix.Poke)
		backoff = min(backoff*2, time.Minute)
	}
}

// checkGenesis records the chain on first run and refuses a different one
// afterwards.
func (ix *Indexer) checkGenesis(ctx context.Context, c *pgx.Conn) error {
	g, err := ix.rpc.BlockHashAt(ctx, 0)
	if err != nil {
		return fmt.Errorf("read genesis: %w", err)
	}
	if g == "" {
		return errors.New("node has no block 0")
	}
	var chainID int64
	var stored string
	err = c.QueryRow(ctx, `SELECT chain_id, genesis_hash FROM indexer_state WHERE id = 1`).Scan(&chainID, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		tag, err := c.Exec(ctx, `INSERT INTO indexer_state (id, chain_id, genesis_hash, last_block, last_hash) VALUES (1, $1, $2, -1, '')
			ON CONFLICT (id) DO NOTHING`, int64(ix.cfg.ChainID), g)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			ix.log.Info("new explorer database", "chain_id", ix.cfg.ChainID, "genesis", g)
			return nil
		}
		return ix.checkGenesis(ctx, c) // another indexer wrote it first: compare with that
	}
	if err != nil {
		return fmt.Errorf("read indexer_state: %w", err)
	}
	if uint64(chainID) != ix.cfg.ChainID || stored != g {
		return fmt.Errorf("%w: this database holds chain %d with genesis %s, the node is chain %d with genesis %s; refusing to mix two chains in one database",
			ErrFatal, chainID, stored, ix.cfg.ChainID, g)
	}
	return nil
}

type cursor struct {
	last int64
	hash string
}

func loadCursor(ctx context.Context, c conn) (cursor, error) {
	var cur cursor
	err := c.QueryRow(ctx, `SELECT last_block, last_hash FROM indexer_state WHERE id = 1`).Scan(&cur.last, &cur.hash)
	return cur, err
}

// catchUp indexes until the cursor is at the node's head.
func (ix *Indexer) catchUp(ctx context.Context, c *pgx.Conn) error {
	for {
		cur, err := loadCursor(ctx, c)
		if err != nil {
			return fmt.Errorf("read cursor: %w", err)
		}
		ix.lastBlock.Store(cur.last)
		head, err := ix.rpc.BlockNumber(ctx)
		if err != nil {
			return fmt.Errorf("eth_blockNumber: %w", err)
		}
		ix.nodeHead.Store(int64(head))

		// The stored tip must still be the node's block at that height. This
		// catches a reorg that kept the height (and a chain that went
		// backwards, the node then having no block there at all).
		if cur.last >= 0 {
			h := ""
			if cur.last <= int64(head) {
				if h, err = ix.rpc.BlockHashAt(ctx, uint64(cur.last)); err != nil {
					return fmt.Errorf("tip check: %w", err)
				}
			}
			if h != cur.hash {
				if err := ix.handleReorg(ctx, c, min(cur.last, int64(head))); err != nil {
					return err
				}
				continue
			}
		}
		if cur.last >= int64(head) {
			return nil
		}

		err = ix.runRange(ctx, c, uint64(cur.last+1), head)
		switch {
		case err == nil, errors.Is(err, errCursorMoved):
			continue
		case errors.Is(err, errReorg), errors.Is(err, errBlockMissing):
			ix.log.Warn("chain changed under the indexer", "err", err)
			cur, lerr := loadCursor(ctx, c)
			if lerr != nil {
				return lerr
			}
			head, herr := ix.rpc.BlockNumber(ctx)
			if herr != nil {
				return herr
			}
			if err := ix.handleReorg(ctx, c, min(cur.last, int64(head))); err != nil {
				return err
			}
		default:
			return err
		}
	}
}

// handleReorg finds the highest stored block (at or below from) that is
// still on the node's chain, and rolls everything above it back.
func (ix *Indexer) handleReorg(ctx context.Context, c *pgx.Conn, from int64) error {
	keep, err := ix.forkPoint(ctx, c, from)
	if err != nil {
		return err
	}
	cur, err := loadCursor(ctx, c)
	if err != nil {
		return err
	}
	ix.log.Warn("reorg: rolling back", "from_block", keep+1, "to_block", cur.last, "keeping", keep)
	ix.f.forget()
	if err := rollbackTo(ctx, c, ix.f, keep); err != nil {
		return err
	}
	ix.lastBlock.Store(keep)
	return nil
}

func (ix *Indexer) forkPoint(ctx context.Context, c *pgx.Conn, from int64) (int64, error) {
	for hi := from; hi >= 0; {
		lo := max(0, hi-63)
		stored := map[int64]string{}
		rows, err := c.Query(ctx, `SELECT number, hash FROM blocks WHERE number BETWEEN $1 AND $2`, lo, hi)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var n int64
			var h string
			if err := rows.Scan(&n, &h); err != nil {
				rows.Close()
				return 0, err
			}
			stored[n] = h
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
		nums := make([]uint64, 0, hi-lo+1)
		for n := hi; n >= lo; n-- {
			nums = append(nums, uint64(n))
		}
		hashes, err := ix.rpc.BlockHashes(ctx, nums)
		if err != nil {
			return 0, fmt.Errorf("fork search: %w", err)
		}
		for i, n := range nums {
			if s := stored[int64(n)]; s != "" && s == hashes[i] {
				return int64(n), nil
			}
		}
		hi = lo - 1
	}
	// Not even block 0 matches: a different chain, unless nothing is stored.
	var stored string
	if err := c.QueryRow(ctx, `SELECT hash FROM blocks WHERE number = 0`).Scan(&stored); err == nil {
		return 0, fmt.Errorf("%w: stored block 0 (%s) is not the node's block 0", ErrFatal, stored)
	}
	return -1, nil
}

// runRange fetches from..to ahead in parallel and commits in order.
func (ix *Indexer) runRange(ctx context.Context, c *pgx.Conn, from, to uint64) error {
	type result struct {
		d   *blockData
		err error
	}
	conc := max(1, ix.cfg.BackfillConcurrency)
	var wg sync.WaitGroup
	defer wg.Wait()
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()

	queue := make(chan chan result, conc)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(queue)
		for n := from; n <= to; n++ {
			fut := make(chan result, 1)
			select {
			case queue <- fut:
			case <-rctx.Done():
				return
			}
			wg.Add(1)
			go func(n uint64) {
				defer wg.Done()
				d, err := ix.fetchRetry(rctx, n)
				fut <- result{d, err}
			}(n)
		}
	}()

	start := time.Now()
	lastLog := start
	done := 0
	for fut := range queue {
		r := <-fut
		if r.err != nil {
			return r.err
		}
		if err := ix.commit(ctx, c, r.d); err != nil {
			return err
		}
		done++
		ix.lastBlock.Store(int64(r.d.Number))
		if time.Since(lastLog) > 10*time.Second {
			rate := float64(done) / time.Since(start).Seconds()
			ix.log.Info("indexing", "block", r.d.Number, "head", to, "blocks_per_s", fmt.Sprintf("%.1f", rate))
			lastLog = time.Now()
		}
	}
	if done > 1 || to-from > 0 {
		ix.log.Info("caught up", "from", from, "to", to, "blocks", done, "took", time.Since(start).Round(time.Millisecond).String())
	} else if done == 1 {
		ix.log.Debug("indexed block", "block", to)
	}
	return nil
}

// fetchRetry fetches block n, retrying transient failures. A block the node
// does not have is returned at once (the chain may have gone backwards).
func (ix *Indexer) fetchRetry(ctx context.Context, n uint64) (*blockData, error) {
	backoff := 500 * time.Millisecond
	for attempt := 1; ; attempt++ {
		d, err := ix.f.fetch(ctx, n)
		if err == nil {
			return d, nil
		}
		if ctx.Err() != nil || errors.Is(err, errBlockMissing) {
			return nil, err
		}
		if attempt >= 8 {
			return nil, fmt.Errorf("block %d: %w", n, err)
		}
		lvl := slog.LevelWarn
		if errors.Is(err, errTracesUnavailable) {
			lvl = slog.LevelError // TRACES=required: never commit without them
		}
		ix.log.Log(ctx, lvl, "fetch failed, retrying", "block", n, "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 15*time.Second)
	}
}

func (ix *Indexer) commit(ctx context.Context, c *pgx.Conn, d *blockData) error {
	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := writeBlock(ctx, tx, d); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

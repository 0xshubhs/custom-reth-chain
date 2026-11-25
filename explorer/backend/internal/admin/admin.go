// Package admin holds the operator commands (`server admin <cmd>`), each
// safe to run while the API and the indexer are up (explorer/docs/design.md,
// "Operating it").
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/config"
	"custom-reth-chain/explorer/backend/internal/db"
	"custom-reth-chain/explorer/backend/internal/evm"
	"custom-reth-chain/explorer/backend/internal/indexer"
)

const usage = `usage: server admin <command>

  status [--json]                 cursor, node head, lag, row counts, chain id and genesis (stored vs node)
  check [--from N] [--to M]       compare every stored block hash and tx count with the node; exit 1 on any mismatch
  reindex --from N [--to M] --yes roll back blocks >= N and move the cursor to N-1 (with --to, re-index N..M before returning)
  label set <address> <name>      set an address label (source 'manual')
  label rm <address>              remove an address label
`

// Env is what a command runs with.
type Env struct {
	Cfg    *config.Config
	Log    *slog.Logger
	Out    io.Writer
	Err    io.Writer
	Pool   *pgxpool.Pool
	RPC    *chainrpc.Client
	HookFn func(ctx context.Context) error // pokes the indexer; nil = INDEXER_HOOK_URL
}

// Main parses args and runs one command. It returns the process exit code.
func Main(ctx context.Context, args []string, out, errw io.Writer, log *slog.Logger) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(errw, usage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	cfg, err := config.FromEnv(config.RoleAdmin)
	if err != nil {
		fmt.Fprintln(errw, err)
		return 2
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(errw, "database:", err)
		return 1
	}
	defer pool.Close()
	env := &Env{
		Cfg: cfg, Log: log, Out: out, Err: errw, Pool: pool,
		RPC: chainrpc.New(cfg.ChainRPCURL, chainrpc.Options{MaxConcurrent: cfg.RPCMaxConcurrent, Timeout: 60 * time.Second}),
	}
	// CHAIN_ID unset and TRACES=auto: settle both from the node, as the
	// indexer does, so the commands compare and refill exactly like it.
	if cfg.ChainID == 0 || cfg.Traces == "auto" {
		nctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if cfg.ChainID == 0 {
			id, err := env.RPC.ChainID(nctx)
			if err != nil {
				fmt.Fprintln(errw, "CHAIN_ID is unset and the node does not answer eth_chainId:", err)
				return 1
			}
			cfg.ChainID = id
		}
		if cfg.Traces == "auto" {
			ok, err := env.RPC.ServesTraces(nctx)
			if err != nil {
				fmt.Fprintln(errw, "TRACES=auto and the node did not answer the trace probe:", err)
				return 1
			}
			cfg.Traces = map[bool]string{true: "required", false: "off"}[ok]
		}
	}
	return Run(ctx, env, args)
}

// Run dispatches a command with a prepared environment (tests use it).
func Run(ctx context.Context, env *Env, args []string) int {
	var err error
	code := 0
	switch args[0] {
	case "status":
		err = env.status(ctx, args[1:])
	case "check":
		code, err = env.check(ctx, args[1:])
	case "reindex":
		err = env.reindex(ctx, args[1:])
	case "label":
		err = env.label(ctx, args[1:])
	default:
		fmt.Fprintf(env.Err, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
	var ue usageError
	if errors.As(err, &ue) {
		fmt.Fprintf(env.Err, "%s\n\n%s", ue.msg, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(env.Err, "error:", err)
		return 1
	}
	return code
}

type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// ---- status ----

// StatusReport is `status --json`.
type StatusReport struct {
	ChainIDConfig   uint64           `json:"chain_id_config"`
	ChainIDStored   *int64           `json:"chain_id_stored"`
	ChainIDNode     *uint64          `json:"chain_id_node"`
	GenesisStored   *string          `json:"genesis_stored"`
	GenesisNode     *string          `json:"genesis_node"`
	Cursor          *int64           `json:"cursor"`
	CursorHash      *string          `json:"cursor_hash"`
	CursorUpdatedAt *string          `json:"cursor_updated_at"`
	LastBlockTime   *string          `json:"last_block_time"`
	NodeHead        *uint64          `json:"node_head"`
	Lag             *int64           `json:"lag"`
	WriterLockHeld  bool             `json:"writer_lock_held"`
	IndexerSessions int64            `json:"indexer_sessions"`
	Rows            map[string]int64 `json:"rows"`
	Problems        []string         `json:"problems"`
}

var statusTables = []string{"blocks", "transactions", "logs", "internal_txs", "token_transfers", "addresses",
	"balance_history", "contract_code", "tokens", "token_balances", "verified_contracts", "verification_jobs", "address_names"}

func (e *Env) status(ctx context.Context, args []string) error {
	fs := newFlags("status")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	rep := StatusReport{ChainIDConfig: e.Cfg.ChainID, Rows: map[string]int64{}, Problems: []string{}}

	var cid, last int64
	var genesis, lastHash string
	var updated time.Time
	err := e.Pool.QueryRow(ctx, `SELECT chain_id, genesis_hash, last_block, last_hash, updated_at FROM indexer_state WHERE id = 1`).
		Scan(&cid, &genesis, &last, &lastHash, &updated)
	switch {
	case err == nil:
		rep.ChainIDStored, rep.GenesisStored = &cid, &genesis
		rep.Cursor, rep.CursorHash = &last, &lastHash
		u := updated.UTC().Format(time.RFC3339)
		rep.CursorUpdatedAt = &u
	case errors.Is(err, pgx.ErrNoRows):
		rep.Problems = append(rep.Problems, "indexer_state is empty: the indexer has never run on this database")
	default:
		return err
	}
	var lastTime *time.Time
	if err := e.Pool.QueryRow(ctx, `SELECT timestamp FROM blocks ORDER BY number DESC LIMIT 1`).Scan(&lastTime); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if lastTime != nil {
		t := lastTime.UTC().Format(time.RFC3339)
		rep.LastBlockTime = &t
	}
	for _, t := range statusTables {
		var n int64
		if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM `+pgx.Identifier{t}.Sanitize()).Scan(&n); err != nil {
			return err
		}
		rep.Rows[t] = n
	}
	// Someone holds the writer lock when we cannot take it.
	var got bool
	if c, err := e.Pool.Acquire(ctx); err == nil {
		if err := c.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, db.IndexerLockKey).Scan(&got); err == nil && got {
			_, _ = c.Exec(ctx, `SELECT pg_advisory_unlock($1)`, db.IndexerLockKey)
		}
		c.Release()
	}
	rep.WriterLockHeld = !got
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND application_name = 'explorer-indexer'`).Scan(&rep.IndexerSessions); err != nil {
		return err
	}
	if rep.IndexerSessions == 0 {
		rep.Problems = append(rep.Problems, "no indexer is connected to this database")
	}

	nctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if id, err := e.RPC.ChainID(nctx); err == nil {
		rep.ChainIDNode = &id
		if id != e.Cfg.ChainID {
			rep.Problems = append(rep.Problems, fmt.Sprintf("node is chain %d, CHAIN_ID is %d", id, e.Cfg.ChainID))
		}
	} else {
		rep.Problems = append(rep.Problems, "node not answering: "+err.Error())
	}
	if rep.ChainIDNode != nil {
		if g, err := e.RPC.BlockHashAt(nctx, 0); err == nil && g != "" {
			rep.GenesisNode = &g
			if rep.GenesisStored != nil && *rep.GenesisStored != g {
				rep.Problems = append(rep.Problems, "stored genesis differs from the node's: a different chain")
			}
		}
		if h, err := e.RPC.BlockNumber(nctx); err == nil {
			rep.NodeHead = &h
			if rep.Cursor != nil {
				lag := int64(h) - *rep.Cursor
				rep.Lag = &lag
				if lag < 0 {
					rep.Problems = append(rep.Problems, "cursor is ahead of the node: the chain went backwards (the indexer rolls back on its next trigger)")
				}
			}
		}
	}
	if rep.ChainIDStored != nil && uint64(*rep.ChainIDStored) != e.Cfg.ChainID {
		rep.Problems = append(rep.Problems, fmt.Sprintf("database holds chain %d, CHAIN_ID is %d", *rep.ChainIDStored, e.Cfg.ChainID))
	}

	if *asJSON {
		enc := json.NewEncoder(e.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	w := e.Out
	fmt.Fprintf(w, "chain id        config %d, stored %s, node %s\n", rep.ChainIDConfig, fmtI(rep.ChainIDStored), fmtU(rep.ChainIDNode))
	fmt.Fprintf(w, "genesis         stored %s\n                node   %s\n", fmtS(rep.GenesisStored), fmtS(rep.GenesisNode))
	fmt.Fprintf(w, "cursor          %s (%s), updated %s\n", fmtI(rep.Cursor), fmtS(rep.CursorHash), fmtS(rep.CursorUpdatedAt))
	fmt.Fprintf(w, "last block time %s\n", fmtS(rep.LastBlockTime))
	fmt.Fprintf(w, "node head       %s, lag %s\n", fmtU(rep.NodeHead), fmtI(rep.Lag))
	fmt.Fprintf(w, "indexers        %d connected\n", rep.IndexerSessions)
	fmt.Fprintf(w, "writer lock     %s\n", map[bool]string{true: "held (a catch-up or a reindex is writing)", false: "free (nothing writing right now)"}[rep.WriterLockHeld])
	fmt.Fprintln(w, "rows:")
	for _, t := range statusTables {
		fmt.Fprintf(w, "  %-20s %d\n", t, rep.Rows[t])
	}
	for _, p := range rep.Problems {
		fmt.Fprintln(w, "PROBLEM:", p)
	}
	return nil
}

func fmtI(v *int64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(*v)
}

func fmtU(v *uint64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(*v)
}

func fmtS(v *string) string {
	if v == nil || *v == "" {
		return "-"
	}
	return *v
}

// ---- check ----

// Mismatch is one disagreement between the database and the node.
type Mismatch struct {
	Block   int64  `json:"block"`
	Problem string `json:"problem"`
	Stored  string `json:"stored"`
	Node    string `json:"node"`
}

func (e *Env) check(ctx context.Context, args []string) (int, error) {
	fs := newFlags("check")
	from := fs.Int64("from", 0, "")
	to := fs.Int64("to", -1, "")
	asJSON := fs.Bool("json", false, "")
	if err := fs.Parse(args); err != nil {
		return 0, usageError{err.Error()}
	}
	if *from < 0 {
		return 0, usageError{"--from must be >= 0"}
	}
	var cursor int64 = -1
	_ = e.Pool.QueryRow(ctx, `SELECT last_block FROM indexer_state WHERE id = 1`).Scan(&cursor)
	hi := *to
	if hi < 0 || hi > cursor {
		hi = cursor
	}
	start := time.Now()
	var mism []Mismatch
	checked := 0
	const chunk = 100
	for lo := *from; lo <= hi; lo += chunk {
		end := min(lo+chunk-1, hi)
		stored := map[int64][2]string{}
		rows, err := e.Pool.Query(ctx, `SELECT number, hash, tx_count FROM blocks WHERE number BETWEEN $1 AND $2`, lo, end)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var n int64
			var h string
			var c int32
			if err := rows.Scan(&n, &h, &c); err != nil {
				rows.Close()
				return 0, err
			}
			stored[n] = [2]string{h, fmt.Sprint(c)}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
		type nb struct {
			Hash         string            `json:"hash"`
			Transactions []json.RawMessage `json:"transactions"`
		}
		elems := make([]chainrpc.BatchElem, 0, end-lo+1)
		outs := make([]*nb, 0, end-lo+1)
		for n := lo; n <= end; n++ {
			o := &nb{}
			outs = append(outs, o)
			elems = append(elems, chainrpc.BatchElem{Method: "eth_getBlockByNumber", Params: []any{chainrpc.Hex64(uint64(n)), false}, Result: o})
		}
		if err := e.RPC.BatchCall(ctx, elems); err != nil {
			return 0, fmt.Errorf("node: %w", err)
		}
		for i, n := 0, lo; n <= end; i, n = i+1, n+1 {
			checked++
			st, ok := stored[n]
			node := outs[i]
			switch {
			case elems[i].Error != nil:
				return 0, fmt.Errorf("node block %d: %w", n, elems[i].Error)
			case !ok:
				mism = append(mism, Mismatch{Block: n, Problem: "missing in database", Stored: "-", Node: chainrpc.Lower(node.Hash)})
			case node.Hash == "":
				mism = append(mism, Mismatch{Block: n, Problem: "missing on node", Stored: st[0], Node: "-"})
			case chainrpc.Lower(node.Hash) != st[0]:
				mism = append(mism, Mismatch{Block: n, Problem: "hash differs", Stored: st[0], Node: chainrpc.Lower(node.Hash)})
			case fmt.Sprint(len(node.Transactions)) != st[1]:
				mism = append(mism, Mismatch{Block: n, Problem: "tx count differs", Stored: st[1], Node: fmt.Sprint(len(node.Transactions))})
			}
		}
	}
	// Rows the cursor says should not exist.
	var beyond int64
	if err := e.Pool.QueryRow(ctx, `SELECT count(*) FROM blocks WHERE number > $1`, cursor).Scan(&beyond); err != nil {
		return 0, err
	}
	if beyond > 0 {
		mism = append(mism, Mismatch{Block: cursor + 1, Problem: fmt.Sprintf("%d stored blocks above the cursor", beyond), Stored: "-", Node: "-"})
	}
	if *asJSON {
		enc := json.NewEncoder(e.Out)
		enc.SetIndent("", "  ")
		if mism == nil {
			mism = []Mismatch{}
		}
		_ = enc.Encode(map[string]any{"from": *from, "to": hi, "checked": checked, "mismatches": mism})
	} else {
		for _, m := range mism {
			fmt.Fprintf(e.Out, "MISMATCH block %d: %s (stored %s, node %s)\n", m.Block, m.Problem, m.Stored, m.Node)
		}
		fmt.Fprintf(e.Out, "checked blocks %d..%d (%d blocks) against %s in %s: %d mismatches\n",
			*from, hi, checked, e.Cfg.ChainRPCURL, time.Since(start).Round(time.Millisecond), len(mism))
	}
	if len(mism) > 0 {
		return 1, nil
	}
	return 0, nil
}

// ---- reindex ----

func (e *Env) reindex(ctx context.Context, args []string) error {
	fs := newFlags("reindex")
	from := fs.Int64("from", -1, "")
	to := fs.Int64("to", -1, "")
	yes := fs.Bool("yes", false, "")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if *from < 0 {
		return usageError{"reindex needs --from N"}
	}
	if *to >= 0 && *to < *from {
		return usageError{"--to must be >= --from"}
	}
	if !*yes {
		return usageError{fmt.Sprintf("reindex deletes every indexed block >= %d and its derived rows; run again with --yes to do it", *from)}
	}
	id, err := e.RPC.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("node: %w", err)
	}
	if id != e.Cfg.ChainID {
		return fmt.Errorf("node is chain %d, CHAIN_ID is %d: refusing", id, e.Cfg.ChainID)
	}

	fmt.Fprintln(e.Out, "waiting for the indexer writer lock ...")
	connect := func(ctx context.Context) (*pgx.Conn, error) { return pgx.Connect(ctx, e.Cfg.DatabaseURL) }
	c, err := indexer.AcquireWriterLock(ctx, connect, e.Log, "admin-reindex")
	if err != nil {
		return err
	}
	defer c.Close(context.Background()) // releases the lock
	cursor, _, err := indexer.Cursor(ctx, c)
	if err != nil {
		return fmt.Errorf("read cursor: %w", err)
	}
	if *from > cursor {
		fmt.Fprintf(e.Out, "nothing to roll back: the cursor is at %d\n", cursor)
		return nil
	}
	start := time.Now()
	if err := indexer.RollbackTo(ctx, c, e.RPC, *from-1); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "rolled back blocks %d..%d, cursor now %d (%s)\n", *from, cursor, *from-1, time.Since(start).Round(time.Millisecond))
	if *to >= 0 {
		head, err := e.RPC.BlockNumber(ctx)
		if err != nil {
			return fmt.Errorf("node: %w", err)
		}
		end := min(uint64(*to), head)
		if uint64(*from) <= end {
			start = time.Now()
			if err := indexer.IndexRange(ctx, e.Cfg, c, e.RPC, e.Log, uint64(*from), end); err != nil {
				return fmt.Errorf("re-index %d..%d: %w", *from, end, err)
			}
			fmt.Fprintf(e.Out, "re-indexed blocks %d..%d (%s)\n", *from, end, time.Since(start).Round(time.Millisecond))
		}
	}
	c.Close(context.Background())
	if err := e.poke(ctx); err != nil {
		fmt.Fprintf(e.Out, "lock released; could not poke the indexer (%v): it refills on its next trigger\n", err)
	} else {
		fmt.Fprintln(e.Out, "lock released; indexer poked to refill")
	}
	return nil
}

func (e *Env) poke(ctx context.Context) error {
	if e.HookFn != nil {
		return e.HookFn(ctx)
	}
	if e.Cfg.IndexerHookURL == "" {
		return errors.New("no INDEXER_HOOK_URL")
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodPost, e.Cfg.IndexerHookURL, nil)
	if err != nil {
		return err
	}
	if e.Cfg.IndexerHookToken != "" {
		req.Header.Set("X-Hook-Token", e.Cfg.IndexerHookToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("hook answered %d", resp.StatusCode)
	}
	return nil
}

// ---- label ----

func (e *Env) label(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return usageError{"label needs: set <address> <name> | rm <address>"}
	}
	addr := strings.ToLower(strings.TrimSpace(args[1]))
	if !chainrpc.IsAddress(addr) {
		return usageError{fmt.Sprintf("%q is not an address", args[1])}
	}
	switch args[0] {
	case "set":
		name := strings.TrimSpace(strings.Join(args[2:], " "))
		if name == "" {
			return usageError{"label set needs a name"}
		}
		name = evm.CleanText(name, 200)
		if _, err := e.Pool.Exec(ctx, `INSERT INTO address_names (address, name, source) VALUES ($1, $2, 'manual')
			ON CONFLICT (address) DO UPDATE SET name = EXCLUDED.name, source = 'manual'`, addr, name); err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "label set: %s = %q\n", addr, name)
	case "rm":
		var src string
		err := e.Pool.QueryRow(ctx, `DELETE FROM address_names WHERE address = $1 RETURNING source`, addr).Scan(&src)
		if errors.Is(err, pgx.ErrNoRows) {
			fmt.Fprintf(e.Out, "no label on %s\n", addr)
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "label removed from %s (it came from %s)\n", addr, src)
	default:
		return usageError{fmt.Sprintf("unknown label command %q", args[0])}
	}
	return nil
}

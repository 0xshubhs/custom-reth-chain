package indexer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/similar"
	"custom-reth-chain/explorer/backend/internal/testpg"
)

var (
	sA, sB, sM, sW = addr('a'), addr('b'), addr('e'), addr('d')
	sC, sD, sE     = addr('c'), addr('1'), addr('2') // token contract, its child, a copy of the token
	sN, sX         = addr('3'), addr('4')            // an ERC-721, an ERC-1155
)

// testChain is six blocks touching every derived table.
func testChain() *simChain {
	c := newSimChain()
	c.code[sC] = "0x6080aa"
	c.code[sE] = "0x6080aa" // same runtime code as C
	c.code[sD] = "0x6080bb"
	c.code[sN] = "0x6080cc"
	c.code[sX] = "0x6080dd"
	c.add(simBlock{n: 0, hash: hash('a'), miner: sM}, map[string]int64{sA: 1000, sN: 0, sX: 0})
	c.add(simBlock{n: 1, hash: hash('b'), miner: sM, txs: []simTx{
		{hash: hash('1'), from: sA, created: sC, nonce: 0, input: "0x6080",
			logs: []simLog{transferLog(sC, chainrpc.ZeroAddress, sA, 100)},
			trace: map[string]any{"type": "CREATE", "from": sA, "to": sC, "value": "0x0", "input": "0x6080", "calls": []any{
				map[string]any{"type": "CREATE", "from": sC, "to": sD, "value": "0x0", "input": "0x60bb"},
			}}},
		{hash: hash('2'), from: sA, to: sB, value: 10, nonce: 1},
	}}, nil)
	c.add(simBlock{n: 2, hash: hash('c'), miner: sM, withdrawals: []string{sW}, txs: []simTx{
		{hash: hash('3'), from: sA, to: sC, nonce: 2, input: "0xa9059cbb", logs: []simLog{transferLog(sC, sA, sB, 40)}},
	}}, nil)
	c.add(simBlock{n: 3, hash: hash('d'), miner: sM, txs: []simTx{
		{hash: hash('4'), from: sB, to: sC, nonce: 0, input: "0xa9059cbb", logs: []simLog{
			transferLog(sC, sB, sA, 40),
			nftLog(sN, chainrpc.ZeroAddress, sB, 7),
			singleLog(sX, sA, chainrpc.ZeroAddress, sA, 1, 5),
		}},
		{hash: hash('5'), from: sA, created: sE, nonce: 3, input: "0x6080"},
	}}, nil)
	c.add(simBlock{n: 4, hash: hash('e'), miner: sM, txs: []simTx{
		{hash: hash('6'), from: sB, to: sN, nonce: 1, logs: []simLog{nftLog(sN, sB, sA, 7)}},
		{hash: hash('7'), from: sA, to: sX, nonce: 4, logs: []simLog{singleLog(sX, sA, sA, sB, 1, 5)}},
		{hash: hash('8'), from: sA, to: sW, value: 5, nonce: 5},
	}}, nil)
	c.add(simBlock{n: 5, hash: hash('f'), miner: sM, txs: []simTx{
		{hash: hash('9'), from: sA, to: sC, nonce: 6, failed: true, input: "0xa9059cbb"},
	}}, nil)
	return c
}

func initState(t *testing.T, ctx context.Context, c *pgx.Conn, genesis string) {
	t.Helper()
	if _, err := c.Exec(ctx, `INSERT INTO indexer_state (id, chain_id, genesis_hash, last_block, last_hash) VALUES (1, 31337, $1, -1, '')`, genesis); err != nil {
		t.Fatal(err)
	}
}

func commitN(t *testing.T, ctx context.Context, c *pgx.Conn, ch *simChain, from, to uint64) {
	t.Helper()
	for n := from; n <= to; n++ {
		tx, err := c.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeBlock(ctx, tx, ch.data(t, n)); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			t.Fatalf("block %d: %v", n, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// snapshot renders the derived tables as text. token_balances.block_number
// and tokens.updated_block are left out: after a rollback they name the
// block the value was re-read at, not the block that last moved it.
func snapshot(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	queries := []string{
		`SELECT last_block, last_hash FROM indexer_state`,
		`SELECT count(*) FROM blocks`, `SELECT count(*) FROM transactions`, `SELECT count(*) FROM logs`,
		`SELECT count(*) FROM internal_txs`, `SELECT count(*) FROM token_transfers`,
		`SELECT address, balance, balance_block, nonce, is_contract, code_hash, creator, creation_tx, creation_block,
			creation_trace_address, creation_input, first_block, last_block, tx_count FROM addresses ORDER BY address`,
		`SELECT address, block_number, balance, delta FROM balance_history ORDER BY address, block_number`,
		`SELECT address, type, is_erc3643, name, symbol, decimals, total_supply, holders_count, transfers_count, first_block FROM tokens ORDER BY address`,
		`SELECT token, holder, token_id, balance FROM token_balances ORDER BY token, holder, token_id`,
		`SELECT address, match_type, source_address FROM verified_contracts ORDER BY address`,
	}
	var sb strings.Builder
	for _, q := range queries {
		rows, err := pool.Query(ctx, q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		for rows.Next() {
			vals, err := rows.Values()
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(&sb, vals...)
		}
		rows.Close()
		sb.WriteString("--\n")
	}
	return sb.String()
}

func verifyC(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `INSERT INTO verified_contracts (address, name, compiler_version, match_type, abi, sources, verified_via)
		VALUES ($1, 'Tok', 'v0.8.28+commit.7893614a', 'full', '[]', '{}', 'standard-input')`, sC); err != nil {
		t.Fatal(err)
	}
}

func TestCommitThenRollbackEqualsFreshIndex(t *testing.T) {
	ctx := context.Background()
	ch := testChain()

	poolA, urlA := testpg.New(t)
	poolB, urlB := testpg.New(t)
	cA, err := pgx.Connect(ctx, urlA)
	if err != nil {
		t.Fatal(err)
	}
	defer cA.Close(ctx)
	cB, err := pgx.Connect(ctx, urlB)
	if err != nil {
		t.Fatal(err)
	}
	defer cB.Close(ctx)
	initState(t, ctx, cA, hash('a'))
	initState(t, ctx, cB, hash('a'))

	// A: index everything (C verified after block 2, so E is adopted as
	// similar when block 3 creates it), then roll back to block 2.
	commitN(t, ctx, cA, ch, 0, 2)
	verifyC(t, ctx, poolA)
	commitN(t, ctx, cA, ch, 3, 5)

	var simSrc string
	if err := poolA.QueryRow(ctx, `SELECT source_address FROM verified_contracts WHERE address = $1 AND match_type = 'similar'`, sE).Scan(&simSrc); err != nil || simSrc != sC {
		t.Fatalf("new contract with verified code not adopted as similar: %v %q", err, simSrc)
	}
	var holdersX, holdersC int
	_ = poolA.QueryRow(ctx, `SELECT holders_count FROM tokens WHERE address = $1`, sC).Scan(&holdersC)
	_ = poolA.QueryRow(ctx, `SELECT holders_count FROM tokens WHERE address = $1`, sX).Scan(&holdersX)
	if holdersC != 1 || holdersX != 1 {
		t.Fatalf("holders before rollback: C=%d X=%d, want 1 and 1", holdersC, holdersX)
	}

	if err := rollbackTo(ctx, cA, ch, 2); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	// B: index only blocks 0..2.
	commitN(t, ctx, cB, ch, 0, 2)
	verifyC(t, ctx, poolB)

	a, b := snapshot(t, ctx, poolA), snapshot(t, ctx, poolB)
	if a != b {
		t.Fatalf("rollback result differs from a fresh index of 0..2\n--- rolled back ---\n%s\n--- fresh ---\n%s", a, b)
	}

	// The chain then continues normally from the fork point.
	commitN(t, ctx, cA, ch, 3, 5)
	commitN(t, ctx, cB, ch, 3, 5)
	if a, b := snapshot(t, ctx, poolA), snapshot(t, ctx, poolB); a != b {
		t.Fatalf("re-indexing after rollback differs\n%s\n---\n%s", a, b)
	}

	// Rolling back everything empties the derived tables.
	if err := rollbackTo(ctx, cA, ch, -1); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"blocks", "addresses", "tokens", "token_balances", "balance_history"} {
		var n int
		_ = poolA.QueryRow(ctx, `SELECT count(*) FROM `+tbl).Scan(&n)
		if n != 0 {
			t.Errorf("%s has %d rows after rollback to -1", tbl, n)
		}
	}
	var last int64
	_ = poolA.QueryRow(ctx, `SELECT last_block FROM indexer_state`).Scan(&last)
	if last != -1 {
		t.Errorf("cursor %d after rollback to -1", last)
	}
}

func TestDerivedRowsAfterIndexing(t *testing.T) {
	ctx := context.Background()
	ch := testChain()
	pool, url := testpg.New(t)
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	initState(t, ctx, c, hash('a'))
	commitN(t, ctx, c, ch, 0, 5)

	check := func(q string, want string, args ...any) {
		t.Helper()
		var got string
		if err := pool.QueryRow(ctx, q, args...).Scan(&got); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if got != want {
			t.Errorf("%s = %q, want %q", q, got, want)
		}
	}
	// ERC-20 C: A minted 100, sent 40 to B, B sent it back: only A holds.
	check(`SELECT string_agg(holder || '=' || balance, ',' ORDER BY holder) FROM token_balances WHERE token = $1`, sA+"=100", sC)
	check(`SELECT type || '/' || holders_count || '/' || transfers_count || '/' || total_supply FROM tokens WHERE address = $1`, "ERC-20/1/3/100", sC)
	// ERC-721 N: id 7 minted to B then moved to A.
	check(`SELECT string_agg(holder || '#' || token_id, ',') FROM token_balances WHERE token = $1`, sA+"#7", sN)
	check(`SELECT type FROM tokens WHERE address = $1`, "ERC-721", sN)
	// ERC-1155 X: id 1 x5 minted to A, moved to B.
	check(`SELECT string_agg(holder || '#' || token_id || '=' || balance, ',') FROM token_balances WHERE token = $1`, sB+"#1=5", sX)
	check(`SELECT type FROM tokens WHERE address = $1`, "ERC-1155", sX)
	// Contracts: C from the root frame, D from an internal CREATE, E by tx.
	check(`SELECT creator || ' ' || creation_tx || ' [' || creation_trace_address || ']' FROM addresses WHERE address = $1`, sA+" "+hash('1')+" []", sC)
	check(`SELECT creator || ' ' || creation_tx || ' [' || creation_trace_address || ']' FROM addresses WHERE address = $1`, sC+" "+hash('1')+" [0]", sD)
	check(`SELECT count(DISTINCT code_hash)::text FROM addresses WHERE address IN ($1, $2)`, "1", sC, sE)
	// Internal txs: the child CREATE is depth 1 and records the contract.
	check(`SELECT call_type || ' ' || created_contract FROM internal_txs WHERE tx_hash = $1 AND depth = 1`, "CREATE "+sD, hash('1'))
	// Native balances and their history: A starts with 1000, sends 10 (block
	// 1, its first sighting, so the first point is the balance itself) and 5.
	check(`SELECT balance::text FROM addresses WHERE address = $1`, "985", sA)
	check(`SELECT string_agg(block_number || ':' || delta, ',' ORDER BY block_number) FROM balance_history WHERE address = $1`, "1:990,4:-5", sA)
	// W got a 1 gwei withdrawal in block 2 and 5 wei in block 4.
	check(`SELECT string_agg(block_number || ':' || delta, ',' ORDER BY block_number) FROM balance_history WHERE address = $1`, "2:1000000000,4:5", sW)
	// The failed tx is stored with its revert reason and touched no token.
	check(`SELECT status || ' ' || error FROM transactions WHERE hash = $1`, "0 nope", hash('9'))
	check(`SELECT tx_count::text || ' ' || nonce FROM addresses WHERE address = $1`, "7 7", sA)
	check(`SELECT last_block::text FROM indexer_state`, "5")
}

func TestWriteBlockGuards(t *testing.T) {
	ctx := context.Background()
	ch := testChain()
	_, url := testpg.New(t)
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	initState(t, ctx, c, hash('a'))
	commitN(t, ctx, c, ch, 0, 1)

	try := func(d *blockData) error {
		tx, err := c.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx) //nolint:errcheck
		return writeBlock(ctx, tx, d)
	}
	// Block 3 while the cursor is at 1.
	if err := try(ch.data(t, 3)); !errors.Is(err, errCursorMoved) {
		t.Errorf("out-of-order block: %v", err)
	}
	// Block 2 of another fork: its parent is not our block 1.
	other := newSimChain()
	other.add(simBlock{n: 0, hash: hash('a'), miner: sM}, nil)
	other.add(simBlock{n: 1, hash: hash('9'), miner: sM}, nil)
	other.add(simBlock{n: 2, hash: hash('8'), miner: sM}, nil)
	if err := try(other.data(t, 2)); !errors.Is(err, errReorg) {
		t.Errorf("wrong parent: %v", err)
	}
}

func TestSimilarFanOut(t *testing.T) {
	ctx := context.Background()
	ch := testChain()
	pool, url := testpg.New(t)
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	initState(t, ctx, c, hash('a'))
	commitN(t, ctx, c, ch, 0, 5) // C and E share code, nothing verified yet

	verifyC(t, ctx, pool)
	n, err := similar.FanOut(ctx, pool, sC)
	if err != nil || n != 1 {
		t.Fatalf("fan-out wrote %d rows (%v), want 1", n, err)
	}
	var mt, src, name string
	if err := pool.QueryRow(ctx, `SELECT match_type, source_address, name FROM verified_contracts WHERE address = $1`, sE).Scan(&mt, &src, &name); err != nil {
		t.Fatal(err)
	}
	if mt != "similar" || src != sC || name != "Tok" {
		t.Errorf("E: %s %s %s", mt, src, name)
	}
	// D has other code: untouched.
	var cnt int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM verified_contracts WHERE address = $1`, sD).Scan(&cnt)
	if cnt != 0 {
		t.Errorf("D was marked verified")
	}
	// A similar row never overwrites a contract's own verification, and a
	// fan-out from a similar row does nothing.
	if _, err := pool.Exec(ctx, `UPDATE verified_contracts SET match_type = 'partial', source_address = NULL, verified_via = 'standard-input' WHERE address = $1`, sE); err != nil {
		t.Fatal(err)
	}
	if n, err := similar.FanOut(ctx, pool, sC); err != nil || n != 0 {
		t.Errorf("fan-out over an own verification: %d %v", n, err)
	}
	// Adopt: nothing for an address that already has a row.
	if ok, err := similar.Adopt(ctx, pool, sE, "0xabc"); err != nil || ok {
		t.Errorf("adopt with unknown hash: %v %v", ok, err)
	}
}

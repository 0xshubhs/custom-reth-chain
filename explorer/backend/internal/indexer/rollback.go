package indexer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
)

// tokenReader reads token state from the chain at a block. The fetcher is
// the real one; tests stub it.
type tokenReader interface {
	readTokens(ctx context.Context, block int64, rs *tokenReadSet) (*tokenState, error)
}

func (f *fetcher) readTokens(ctx context.Context, block int64, rs *tokenReadSet) (*tokenState, error) {
	_, _, ts, err := f.read(ctx, chainrpc.Hex64(uint64(block)), nil, nil, rs)
	return ts, err
}

// conn is what rollback and commit need from a database connection.
type conn interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// RollbackTo deletes every block above keep (keep = -1 deletes them all) the
// normal cascade way, recomputes the derived rows those blocks touched, and
// moves the cursor to keep. Token balances and supplies are re-read from the
// node at keep, so the result is what indexing 0..keep would have produced.
// The caller must hold the indexer writer lock on c.
func RollbackTo(ctx context.Context, c *pgx.Conn, rpc *chainrpc.Client, keep int64) error {
	return rollbackTo(ctx, c, newFetcher(rpc, false), keep)
}

func rollbackTo(ctx context.Context, c conn, tr tokenReader, keep int64) error {
	keepHash := ""
	if keep >= 0 {
		if err := c.QueryRow(ctx, `SELECT hash FROM blocks WHERE number = $1`, keep).Scan(&keepHash); err != nil {
			return fmt.Errorf("rollback: block %d is not stored: %w", keep, err)
		}
	}

	addrs, err := queryStrings(ctx, c, `SELECT address FROM addresses WHERE last_block > $1 OR first_block > $1 OR creation_block > $1`, keep)
	if err != nil {
		return fmt.Errorf("rollback: touched addresses: %w", err)
	}

	type tokInfo struct {
		typ   string
		first int64
	}
	tokens := map[string]tokInfo{}
	rows, err := c.Query(ctx, `SELECT address, type, first_block FROM tokens WHERE updated_block > $1 OR first_block > $1`, keep)
	if err != nil {
		return fmt.Errorf("rollback: touched tokens: %w", err)
	}
	for rows.Next() {
		var a string
		var ti tokInfo
		if err := rows.Scan(&a, &ti.typ, &ti.first); err != nil {
			rows.Close()
			return err
		}
		tokens[a] = ti
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// Balance keys the removed blocks wrote or moved, including holders whose
	// row was deleted at zero and who hold again once those blocks are gone.
	rs := newTokenReadSet()
	rows, err = c.Query(ctx, `
		SELECT tb.token, tb.holder, tb.token_id::text, t.type, t.first_block
		  FROM token_balances tb JOIN tokens t ON t.address = tb.token WHERE tb.block_number > $1
		UNION
		SELECT tt.token, tt.from_addr, COALESCE(tt.token_id::text, '-1'), t.type, t.first_block
		  FROM token_transfers tt JOIN tokens t ON t.address = tt.token WHERE tt.block_number > $1
		UNION
		SELECT tt.token, tt.to_addr, COALESCE(tt.token_id::text, '-1'), t.type, t.first_block
		  FROM token_transfers tt JOIN tokens t ON t.address = tt.token WHERE tt.block_number > $1`, keep)
	if err != nil {
		return fmt.Errorf("rollback: balance keys: %w", err)
	}
	for rows.Next() {
		var token, holder, id, typ string
		var first int64
		if err := rows.Scan(&token, &holder, &id, &typ, &first); err != nil {
			rows.Close()
			return err
		}
		if first > keep {
			continue // the token itself goes away
		}
		if _, ok := tokens[token]; !ok {
			tokens[token] = tokInfo{typ: typ, first: first}
		}
		rs.Tokens[token] = true
		switch {
		case id == "-1":
			if holder != chainrpc.ZeroAddress {
				rs.Fungible[fungKey{token, holder}] = true
			}
		case typ == TypeERC721:
			rs.NFT[nftKey{token, id}] = true
		default:
			if holder != chainrpc.ZeroAddress {
				rs.Multi[multiKey{token, holder, id}] = true
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for t, ti := range tokens {
		if ti.first <= keep {
			rs.Tokens[t] = true
		}
	}

	ts := newTokenState()
	if keep >= 0 && len(rs.Tokens) > 0 {
		if ts, err = tr.readTokens(ctx, keep, rs); err != nil {
			return fmt.Errorf("rollback: read token state at %d: %w", keep, err)
		}
	}

	tx, err := c.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	steps := []struct {
		what string
		sql  string
	}{
		{"similar rows of removed contracts", `DELETE FROM verified_contracts WHERE match_type = 'similar' AND address IN (SELECT address FROM addresses WHERE creation_block > $1)`},
		{"blocks", `DELETE FROM blocks WHERE number > $1`},
		{"balances of removed tokens", `DELETE FROM token_balances WHERE token IN (SELECT address FROM tokens WHERE first_block > $1)`},
		{"removed tokens", `DELETE FROM tokens WHERE first_block > $1`},
	}
	for _, s := range steps {
		if _, err := tx.Exec(ctx, s.sql, keep); err != nil {
			return fmt.Errorf("rollback: %s: %w", s.what, err)
		}
	}

	if err := recomputeAddresses(ctx, tx, addrs, keep); err != nil {
		return err
	}

	touched := map[string]bool{}
	for t, ti := range tokens {
		if ti.first > keep {
			continue
		}
		touched[t] = true
		m := ts.Meta[t]
		if m == nil {
			m = &tokenMeta{}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE tokens SET
				transfers_count = (SELECT count(*) FROM token_transfers WHERE token = $1),
				updated_block = $2,
				name = COALESCE($3, name), symbol = COALESCE($4, symbol), decimals = COALESCE($5, decimals),
				total_supply = COALESCE($6, total_supply)
			WHERE address = $1`, t, keep, m.Name, m.Symbol, m.Decimals, num(m.TotalSupply)); err != nil {
			return fmt.Errorf("rollback: token %s: %w", t, err)
		}
	}
	if err := applyTokenState(ctx, tx, ts, keep, touched); err != nil {
		return fmt.Errorf("rollback: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE indexer_state SET last_block = $1, last_hash = $2, updated_at = now() WHERE id = 1`, keep, keepHash); err != nil {
		return fmt.Errorf("rollback: cursor: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{"type": "reorg", "number": keep})
	if _, err := tx.Exec(ctx, `SELECT pg_notify('explorer_events', $1)`, string(payload)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// recomputeAddresses rebuilds the derived address rows of addrs from what is
// still stored after a rollback to keep. An address that no longer appears
// anywhere is deleted.
func recomputeAddresses(ctx context.Context, tx pgx.Tx, addrs []string, keep int64) error {
	if len(addrs) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `
		WITH app AS (
			SELECT from_addr AS address, block_number FROM transactions WHERE from_addr = ANY($1)
			UNION ALL SELECT to_addr, block_number FROM transactions WHERE to_addr = ANY($1)
			UNION ALL SELECT created_contract, block_number FROM transactions WHERE created_contract = ANY($1)
			UNION ALL SELECT from_addr, block_number FROM internal_txs WHERE depth > 0 AND from_addr = ANY($1)
			UNION ALL SELECT to_addr, block_number FROM internal_txs WHERE depth > 0 AND to_addr = ANY($1)
			UNION ALL SELECT address, block_number FROM logs WHERE address = ANY($1)
			UNION ALL SELECT from_addr, block_number FROM token_transfers WHERE from_addr = ANY($1)
			UNION ALL SELECT to_addr, block_number FROM token_transfers WHERE to_addr = ANY($1)
			UNION ALL SELECT miner, number FROM blocks WHERE miner = ANY($1)
			UNION ALL SELECT address, block_number FROM balance_history WHERE address = ANY($1)
		)
		SELECT address, min(block_number), max(block_number) FROM app GROUP BY address`, addrs)
	if err != nil {
		return fmt.Errorf("rollback: address spans: %w", err)
	}
	live := []string{} // never nil: a NULL array would make NOT (x = ANY(NULL)) keep every row
	var firsts, lasts []int64
	for rows.Next() {
		var a string
		var f, l int64
		if err := rows.Scan(&a, &f, &l); err != nil {
			rows.Close()
			return err
		}
		live = append(live, a)
		firsts = append(firsts, f)
		lasts = append(lasts, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM addresses WHERE address = ANY($1) AND NOT (address = ANY($2))`, addrs, live); err != nil {
		return fmt.Errorf("rollback: delete addresses: %w", err)
	}
	if len(live) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE addresses a SET
			first_block = s.first_b,
			last_block = s.last_b,
			tx_count = (SELECT count(*) FROM transactions x WHERE x.from_addr = a.address OR x.to_addr = a.address OR x.created_contract = a.address),
			nonce = (SELECT max(x.nonce) + 1 FROM transactions x WHERE x.from_addr = a.address),
			balance = COALESCE((SELECT h.balance FROM balance_history h WHERE h.address = a.address ORDER BY h.block_number DESC LIMIT 1), 0),
			balance_block = s.last_b,
			is_contract = CASE WHEN a.creation_block > $4 THEN false ELSE a.is_contract END,
			code_hash = CASE WHEN a.creation_block > $4 THEN NULL ELSE a.code_hash END,
			creator = CASE WHEN a.creation_block > $4 THEN NULL ELSE a.creator END,
			creation_tx = CASE WHEN a.creation_block > $4 THEN NULL ELSE a.creation_tx END,
			creation_trace_address = CASE WHEN a.creation_block > $4 THEN NULL ELSE a.creation_trace_address END,
			creation_input = CASE WHEN a.creation_block > $4 THEN NULL ELSE a.creation_input END,
			creation_block = CASE WHEN a.creation_block > $4 THEN NULL ELSE a.creation_block END
		FROM unnest($1::text[], $2::bigint[], $3::bigint[]) AS s(address, first_b, last_b)
		WHERE a.address = s.address`, live, firsts, lasts, keep); err != nil {
		return fmt.Errorf("rollback: recompute addresses: %w", err)
	}
	return nil
}

func queryStrings(ctx context.Context, c conn, sql string, args ...any) ([]string, error) {
	rows, err := c.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

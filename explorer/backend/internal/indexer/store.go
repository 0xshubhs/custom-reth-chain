package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/similar"
)

var (
	// errReorg: block n's parent is not the stored block n-1.
	errReorg = errors.New("parent hash mismatch")
	// errCursorMoved: the cursor is not where the block expects it.
	errCursorMoved = errors.New("cursor moved")
)

// num converts to a numeric parameter (NULL for nil).
func num(v *big.Int) pgtype.Numeric {
	if v == nil {
		return pgtype.Numeric{}
	}
	return pgtype.Numeric{Int: new(big.Int).Set(v), Exp: 0, Valid: true}
}

func numU(v chainrpc.Quantity) pgtype.Numeric { return num(v.Big()) }

// writeBlock stores one block and everything derived from it, moves the
// cursor and notifies listeners, all inside tx. The caller commits.
func writeBlock(ctx context.Context, tx pgx.Tx, d *blockData) error {
	var last int64
	var lastHash string
	if err := tx.QueryRow(ctx, `SELECT last_block, last_hash FROM indexer_state WHERE id = 1 FOR UPDATE`).Scan(&last, &lastHash); err != nil {
		return fmt.Errorf("read cursor: %w", err)
	}
	n := int64(d.Number)
	if last != n-1 {
		return fmt.Errorf("%w: cursor at %d, block %d", errCursorMoved, last, n)
	}
	if n > 0 && lastHash != d.ParentHash {
		return fmt.Errorf("%w at block %d: parent %s, stored %s", errReorg, n, d.ParentHash, lastHash)
	}

	b := &d.Block
	if _, err := tx.Exec(ctx, `
		INSERT INTO blocks (number, hash, parent_hash, miner, timestamp, gas_used, gas_limit, base_fee_per_gas,
			tx_count, size, state_root, transactions_root, receipts_root, logs_bloom, extra_data, nonce, mix_hash,
			difficulty, withdrawals_root, blob_gas_used, excess_blob_gas, parent_beacon_block_root, requests_hash, raw)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)`,
		n, d.Hash, d.ParentHash, chainrpc.Lower(b.Miner), d.Time, numU(b.GasUsed), numU(b.GasLimit), numU(b.BaseFeePerGas),
		len(d.Txs), b.Size.Ptr64(), chainrpc.Lower(b.StateRoot), chainrpc.Lower(b.TransactionsRoot), chainrpc.Lower(b.ReceiptsRoot),
		lowerPtr(b.LogsBloom), orHex(chainrpc.Lower(b.ExtraData)), lowerPtr(b.Nonce), lowerPtr(b.MixHash),
		numU(b.Difficulty), lowerPtr(b.WithdrawalsRoot), numU(b.BlobGasUsed), numU(b.ExcessBlobGas),
		lowerPtr(b.ParentBeaconBlockRoot), lowerPtr(b.RequestsHash), d.HeaderRaw,
	); err != nil {
		return fmt.Errorf("insert block %d: %w", n, err)
	}

	if err := copyRows(ctx, tx, d, n); err != nil {
		return err
	}

	// Runtime code, deduplicated by hash.
	codeHash := map[string]string{}
	for _, a := range sortedKeys(d.Code) {
		raw, err := chainrpc.DecodeHex(d.Code[a])
		if err != nil {
			continue
		}
		h := chainrpc.Keccak256Hex(raw)
		codeHash[a] = h
		if _, err := tx.Exec(ctx, `INSERT INTO contract_code (code_hash, code) VALUES ($1, $2) ON CONFLICT (code_hash) DO NOTHING`, h, d.Code[a]); err != nil {
			return fmt.Errorf("insert code: %w", err)
		}
	}

	if err := upsertAddresses(ctx, tx, d, n, codeHash); err != nil {
		return err
	}
	if err := upsertTokens(ctx, tx, d, n); err != nil {
		return err
	}
	touched := map[string]bool{}
	for t := range d.TokenTypes {
		touched[t] = true
	}
	if err := applyTokenState(ctx, tx, d.Tokens, n, touched); err != nil {
		return err
	}

	// A new contract whose code is already verified is verified too.
	for _, c := range d.Creations {
		if h := codeHash[c.Address]; h != "" {
			if _, err := similar.Adopt(ctx, tx, c.Address, h); err != nil {
				return fmt.Errorf("similar adopt %s: %w", c.Address, err)
			}
		}
	}

	if _, err := tx.Exec(ctx, `UPDATE indexer_state SET last_block = $1, last_hash = $2, updated_at = now() WHERE id = 1`, n, d.Hash); err != nil {
		return fmt.Errorf("move cursor: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{"type": "block", "number": n})
	if _, err := tx.Exec(ctx, `SELECT pg_notify('explorer_events', $1)`, string(payload)); err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	return nil
}

func copyRows(ctx context.Context, tx pgx.Tx, d *blockData, n int64) error {
	if len(d.Txs) > 0 {
		rows := make([][]any, len(d.Txs))
		for i, t := range d.Txs {
			rows[i] = []any{
				t.Hash, n, d.Hash, t.TxIndex, d.Time, t.From, t.To, t.Created, num(t.Value), t.Nonce, int16(t.Type),
				t.Gas, num(t.GasPrice), num(t.MaxFee), num(t.MaxPrio), t.GasUsed, t.CumGasUsed, num(t.EffGasPrice),
				t.Status, t.Input, t.MethodID, t.Error, t.Raw, t.ReceiptRaw, nullJSON(t.TraceRaw),
			}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"transactions"}, []string{
			"hash", "block_number", "block_hash", "tx_index", "timestamp", "from_addr", "to_addr", "created_contract",
			"value", "nonce", "type", "gas", "gas_price", "max_fee_per_gas", "max_priority_fee_per_gas", "gas_used",
			"cumulative_gas_used", "effective_gas_price", "status", "input", "method_id", "error", "raw", "receipt_raw", "trace_raw",
		}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("copy transactions: %w", err)
		}
	}
	if len(d.Logs) > 0 {
		rows := make([][]any, len(d.Logs))
		for i, l := range d.Logs {
			rows[i] = []any{n, l.LogIndex, l.TxHash, l.TxIndex, l.Address, l.Topics[0], l.Topics[1], l.Topics[2], l.Topics[3], l.Data}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"logs"}, []string{
			"block_number", "log_index", "tx_hash", "tx_index", "address", "topic0", "topic1", "topic2", "topic3", "data",
		}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("copy logs: %w", err)
		}
	}
	if len(d.Internal) > 0 {
		rows := make([][]any, len(d.Internal))
		for i, f := range d.Internal {
			rows[i] = []any{n, f.TxHash, f.TxIndex, f.TraceAddress, f.Depth, f.CallType, f.From, f.To, num(f.Value),
				f.Gas, f.GasUsed, f.Input, f.Output, f.Error, f.RevertReason, f.Created}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"internal_txs"}, []string{
			"block_number", "tx_hash", "tx_index", "trace_address", "depth", "call_type", "from_addr", "to_addr", "value",
			"gas", "gas_used", "input", "output", "error", "revert_reason", "created_contract",
		}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("copy internal_txs: %w", err)
		}
	}
	if len(d.Transfers) > 0 {
		rows := make([][]any, len(d.Transfers))
		for i, t := range d.Transfers {
			rows[i] = []any{n, t.LogIndex, t.BatchIndex, t.TxHash, d.Time, t.Token, t.TokenType, t.From, t.To, t.Operator, num(t.Amount), num(t.TokenID)}
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"token_transfers"}, []string{
			"block_number", "log_index", "batch_index", "tx_hash", "timestamp", "token", "token_type", "from_addr", "to_addr", "operator", "amount", "token_id",
		}, pgx.CopyFromRows(rows)); err != nil {
			return fmt.Errorf("copy token_transfers: %w", err)
		}
	}
	return nil
}

func nullJSON(r json.RawMessage) any {
	if r == nil {
		return nil
	}
	return r
}

func lowerPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := chainrpc.Lower(*s)
	return &v
}

// upsertAddresses records every address seen in block n with its balance at
// n, and one balance_history row for each balance that changed.
func upsertAddresses(ctx context.Context, tx pgx.Tx, d *blockData, n int64, codeHash map[string]string) error {
	addrs := sortedKeys(d.Seen)
	if len(addrs) == 0 {
		return nil
	}
	prev := map[string]*big.Int{}
	rows, err := tx.Query(ctx, `SELECT address, balance::text FROM addresses WHERE address = ANY($1)`, addrs)
	if err != nil {
		return fmt.Errorf("read balances: %w", err)
	}
	for rows.Next() {
		var a, s string
		if err := rows.Scan(&a, &s); err != nil {
			rows.Close()
			return err
		}
		v, _ := new(big.Int).SetString(s, 10)
		prev[a] = v
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	created := map[string]*creation{}
	for i := range d.Creations {
		created[d.Creations[i].Address] = &d.Creations[i]
	}

	batch := &pgx.Batch{}
	for _, a := range addrs {
		s := d.Seen[a]
		bal := d.Balances[a]
		if bal == nil {
			bal = new(big.Int)
		}
		old, existed := prev[a]
		if !existed {
			old = new(big.Int)
		}
		if bal.Cmp(old) != 0 {
			batch.Queue(`INSERT INTO balance_history (address, block_number, balance, delta) VALUES ($1, $2, $3, $4)`,
				a, n, num(bal), num(new(big.Int).Sub(bal, old)))
		}
		_, hasCode := d.Code[a]
		var ch *string
		if h := codeHash[a]; h != "" {
			ch = &h
		}
		var creator, ctx_, ctrace, cinput *string
		var cblock *int64
		if c := created[a]; c != nil {
			creator, ctx_, ctrace, cinput, cblock = &c.Creator, &c.TxHash, &c.TraceAddress, &c.Input, &n
		}
		batch.Queue(`
			INSERT INTO addresses AS a (address, balance, balance_block, nonce, is_contract, code_hash, creator, creation_tx,
				creation_block, creation_trace_address, creation_input, first_block, last_block, tx_count)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$3,$3,$12)
			ON CONFLICT (address) DO UPDATE SET
				balance = EXCLUDED.balance,
				balance_block = EXCLUDED.balance_block,
				nonce = GREATEST(a.nonce, EXCLUDED.nonce),
				is_contract = a.is_contract OR EXCLUDED.is_contract,
				code_hash = COALESCE(EXCLUDED.code_hash, a.code_hash),
				creator = CASE WHEN EXCLUDED.creation_tx IS NOT NULL THEN EXCLUDED.creator ELSE a.creator END,
				creation_tx = COALESCE(EXCLUDED.creation_tx, a.creation_tx),
				creation_block = COALESCE(EXCLUDED.creation_block, a.creation_block),
				creation_trace_address = CASE WHEN EXCLUDED.creation_tx IS NOT NULL THEN EXCLUDED.creation_trace_address ELSE a.creation_trace_address END,
				creation_input = CASE WHEN EXCLUDED.creation_tx IS NOT NULL THEN EXCLUDED.creation_input ELSE a.creation_input END,
				last_block = EXCLUDED.last_block,
				tx_count = a.tx_count + EXCLUDED.tx_count`,
			a, num(bal), n, s.Nonce, hasCode, ch, creator, ctx_, cblock, ctrace, cinput, int64(s.TxCount))
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("upsert addresses: %w", err)
	}
	return nil
}

// upsertTokens creates tokens on their first transfer and refreshes the
// metadata read at n.
func upsertTokens(ctx context.Context, tx pgx.Tx, d *blockData, n int64) error {
	if len(d.TokenTypes) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, t := range sortedKeys(d.TokenTypes) {
		m := d.Tokens.Meta[t]
		if m == nil {
			m = &tokenMeta{}
		}
		batch.Queue(`
			INSERT INTO tokens AS t (address, type, is_erc3643, name, symbol, decimals, total_supply, holders_count, transfers_count, first_block, updated_block)
			VALUES ($1,$2,$3,$4,$5,$6,$7,0,$8,$9,$9)
			ON CONFLICT (address) DO UPDATE SET
				is_erc3643 = t.is_erc3643 OR EXCLUDED.is_erc3643,
				name = COALESCE(EXCLUDED.name, t.name),
				symbol = COALESCE(EXCLUDED.symbol, t.symbol),
				decimals = COALESCE(EXCLUDED.decimals, t.decimals),
				total_supply = COALESCE(EXCLUDED.total_supply, t.total_supply),
				transfers_count = t.transfers_count + EXCLUDED.transfers_count,
				updated_block = EXCLUDED.updated_block`,
			t, d.TokenTypes[t], m.ERC3643, m.Name, m.Symbol, m.Decimals, num(m.TotalSupply), int64(d.TokenCounts[t]), n)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("upsert tokens: %w", err)
	}
	return nil
}

// applyTokenState writes balances read from the chain at block n: zero rows
// are deleted, an ERC-721 id belongs to exactly its ownerOf. Then each
// touched token's holders_count is recounted.
func applyTokenState(ctx context.Context, tx pgx.Tx, ts *tokenState, n int64, touched map[string]bool) error {
	if ts == nil {
		return nil
	}
	batch := &pgx.Batch{}
	upsert := `INSERT INTO token_balances (token, holder, token_id, balance, block_number) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (token, holder, token_id) DO UPDATE SET balance = EXCLUDED.balance, block_number = EXCLUDED.block_number`
	del := `DELETE FROM token_balances WHERE token = $1 AND holder = $2 AND token_id = $3`
	minusOne := num(big.NewInt(-1))
	for k, v := range ts.Fungible {
		touched[k.Token] = true
		if v.Sign() == 0 {
			batch.Queue(del, k.Token, k.Holder, minusOne)
		} else {
			batch.Queue(upsert, k.Token, k.Holder, minusOne, num(v), n)
		}
	}
	for k, owner := range ts.NFTOwner {
		touched[k.Token] = true
		id, _ := new(big.Int).SetString(k.ID, 10)
		batch.Queue(`DELETE FROM token_balances WHERE token = $1 AND token_id = $2`, k.Token, num(id))
		if owner != "" {
			batch.Queue(upsert, k.Token, owner, num(id), num(big.NewInt(1)), n)
		}
	}
	for k, v := range ts.Multi {
		touched[k.Token] = true
		id, _ := new(big.Int).SetString(k.ID, 10)
		if v.Sign() == 0 {
			batch.Queue(del, k.Token, k.Holder, num(id))
		} else {
			batch.Queue(upsert, k.Token, k.Holder, num(id), num(v), n)
		}
	}
	for _, t := range sortedKeys(touched) {
		batch.Queue(`UPDATE tokens SET holders_count = (SELECT count(DISTINCT holder) FROM token_balances WHERE token = $1) WHERE address = $1`, t)
	}
	if batch.Len() == 0 {
		return nil
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("token balances: %w", err)
	}
	return nil
}

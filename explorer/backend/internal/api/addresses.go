package api

import (
	"context"
	"math/big"
	"net/http"
	"strings"
	"time"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/httpx"
)

func (s *Server) pathAddr(w http.ResponseWriter, r *http.Request) (string, bool) {
	a, ok := normAddr(r.PathValue("addr"))
	if !ok {
		badRequest(w, r, "not an address")
	}
	return a, ok
}

func (s *Server) handleAddress(w http.ResponseWriter, r *http.Request) {
	addr, ok := s.pathAddr(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	d := AddressDetail{Hash: EIP55(addr), Balance: "0"}
	var creator *string
	var first, last *int64
	err := s.pool.QueryRow(ctx, `SELECT a.balance::text, a.balance_block, a.nonce, a.is_contract, a.creator, a.creation_tx,
			a.creation_block, a.first_block, a.last_block, a.tx_count,
			(SELECT count(*) FROM token_transfers WHERE from_addr = a.address)
			  + (SELECT count(*) FROM token_transfers WHERE to_addr = a.address AND from_addr <> a.address),
			(SELECT count(*) FROM internal_txs WHERE depth > 0 AND (from_addr = a.address OR to_addr = a.address)),
			(SELECT count(*) FROM token_balances WHERE holder = a.address)
		FROM addresses a WHERE a.address = $1`, addr).Scan(&d.Balance, &d.BalanceBlock, &d.Nonce, &d.IsContract, &creator,
		&d.CreationTx, &d.CreationBlock, &first, &last, &d.TxCount, &d.TokenTransfersCount, &d.InternalTxsCount, &d.TokenHoldingsCount)
	switch {
	case isNoRows(err):
		// Never seen: still an address. Read what the node knows.
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var bal, code string
		elems := []chainrpc.BatchElem{
			{Method: "eth_getBalance", Params: []any{addr, "latest"}, Result: &bal},
			{Method: "eth_getCode", Params: []any{addr, "latest"}, Result: &code},
		}
		if err := s.rpc.BatchCall(cctx, elems); err == nil {
			if elems[0].Error == nil {
				if v, err := chainrpc.ParseBig(bal); err == nil {
					d.Balance = v.String()
				}
			}
			if elems[1].Error == nil && code != "" && code != "0x" {
				d.IsContract = true
			}
		}
		s.nudgeIndexer()
	case err != nil:
		s.dbError(w, r, err)
		return
	}
	d.FirstBlock, d.LastBlock = first, last

	rs := refSet{addr: {}}
	rs.addPtr(creator)
	refs, err := s.loadRefs(ctx, rs)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	self := refs.get(addr)
	d.Name, d.IsVerified = self.Name, self.IsVerified
	d.IsContract = d.IsContract || self.IsContract
	d.Creator = refs.getPtr(creator)
	if d.Token, err = s.loadToken(ctx, addr); err != nil {
		s.dbError(w, r, err)
		return
	}
	if d.IsContract {
		if p, err := s.detectProxy(ctx, addr, d.Token != nil); err == nil && p.Type != "" {
			d.ProxyType = ptr(p.Type)
			if p.Impl != "" {
				ir, err := s.loadRefs(ctx, refSet{p.Impl: {}})
				if err == nil {
					d.Implementation = ir.get(p.Impl)
				}
			}
		}
	}
	httpx.WriteJSON(w, r, http.StatusOK, d)
}

func (s *Server) handleAddressTxs(w http.ResponseWriter, r *http.Request) {
	addr, ok := s.pathAddr(w, r)
	if !ok {
		return
	}
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	const set = `SELECT hash FROM transactions WHERE from_addr = $1
		UNION SELECT hash FROM transactions WHERE to_addr = $1
		UNION SELECT hash FROM transactions WHERE created_contract = $1`
	var total int64
	if err := s.pool.QueryRow(r.Context(), `SELECT count(*) FROM (`+set+`) h`, addr).Scan(&total); err != nil {
		s.dbError(w, r, err)
		return
	}
	s.writeTxPage(w, r, p, total, `SELECT `+txSummaryCols+` FROM transactions t WHERE t.hash IN (`+set+`)
		ORDER BY t.block_number DESC, t.tx_index DESC LIMIT $2 OFFSET $3`, addr, p.Limit, p.Offset)
}

func (s *Server) handleAddressTokenTransfers(w http.ResponseWriter, r *http.Request) {
	addr, ok := s.pathAddr(w, r)
	if !ok {
		return
	}
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	where, args := `(tt.from_addr = $1 OR tt.to_addr = $1)`, []any{addr}
	if t := r.URL.Query().Get("token"); t != "" {
		ta, ok := normAddr(t)
		if !ok {
			badRequest(w, r, "token must be an address")
			return
		}
		where, args = where+` AND tt.token = $2`, append(args, ta)
	}
	s.writeTransfers(w, r, p, where, args, "tt.block_number DESC, tt.log_index DESC, tt.batch_index DESC")
}

func (s *Server) handleAddressInternal(w http.ResponseWriter, r *http.Request) {
	addr, ok := s.pathAddr(w, r)
	if !ok {
		return
	}
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	s.writeInternal(w, r, p, `i.depth > 0 AND (i.from_addr = $1 OR i.to_addr = $1)`, []any{addr},
		"i.block_number DESC, i.tx_index DESC, string_to_array(i.trace_address, '.')::int[]")
}

func (s *Server) handleAddressLogs(w http.ResponseWriter, r *http.Request) {
	addr, ok := s.pathAddr(w, r)
	if !ok {
		return
	}
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	s.writeLogs(w, r, p, `l.address = $1`, []any{addr}, "l.block_number DESC, l.log_index DESC")
}

func (s *Server) handleAddressTokens(w http.ResponseWriter, r *http.Request) {
	addr, ok := s.pathAddr(w, r)
	if !ok {
		return
	}
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	ctx := r.Context()
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM token_balances WHERE holder = $1`, addr).Scan(&total); err != nil {
		s.dbError(w, r, err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT `+tokenCols+`, tb.balance::text, tb.token_id::text
		FROM token_balances tb JOIN tokens t ON t.address = tb.token
		WHERE tb.holder = $1 ORDER BY t.type, lower(t.name), t.address, tb.token_id LIMIT $2 OFFSET $3`, addr, p.Limit, p.Offset)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	defer rows.Close()
	var items []TokenHolding
	for rows.Next() {
		var th TokenHolding
		var tid string
		var a string
		var dec *int32
		t := &th.Token
		if err := rows.Scan(&a, &t.Type, &t.IsERC3643, &t.Name, &t.Symbol, &dec, &t.TotalSupply, &t.HoldersCount,
			&t.TransfersCount, &t.FirstBlock, &th.Balance, &tid); err != nil {
			s.dbError(w, r, err)
			return
		}
		t.Address = EIP55(a)
		if dec != nil {
			v := int64(*dec)
			t.Decimals = &v
		}
		if tid != "-1" {
			th.TokenID = &tid
		}
		items = append(items, th)
	}
	if err := rows.Err(); err != nil {
		s.dbError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, NewPage(items, total, p))
}

func (s *Server) handleBalanceHistory(w http.ResponseWriter, r *http.Request) {
	addr, ok := s.pathAddr(w, r)
	if !ok {
		return
	}
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	ctx := r.Context()
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM balance_history WHERE address = $1`, addr).Scan(&total); err != nil {
		s.dbError(w, r, err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT h.block_number, b.timestamp, h.balance::text, h.delta::text
		FROM balance_history h JOIN blocks b ON b.number = h.block_number
		WHERE h.address = $1 ORDER BY h.block_number DESC LIMIT $2 OFFSET $3`, addr, p.Limit, p.Offset)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	defer rows.Close()
	var items []BalancePoint
	for rows.Next() {
		var bp BalancePoint
		var ts time.Time
		if err := rows.Scan(&bp.BlockNumber, &ts, &bp.Balance, &bp.Delta); err != nil {
			s.dbError(w, r, err)
			return
		}
		bp.Timestamp = ISO(ts)
		items = append(items, bp)
	}
	if err := rows.Err(); err != nil {
		s.dbError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, NewPage(items, total, p))
}

// ---- tokens ----

// likePattern makes a case-insensitive "contains" pattern with LIKE's
// wildcards escaped.
func likePattern(q string) string {
	q = strings.ToLower(strings.TrimSpace(q))
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	typ := r.URL.Query().Get("type")
	if typ != "" && typ != "ERC-20" && typ != "ERC-721" && typ != "ERC-1155" {
		badRequest(w, r, "type must be ERC-20, ERC-721 or ERC-1155")
		return
	}
	pat, exact := "", ""
	if q != "" {
		pat = likePattern(q)
		if a, ok := normAddr(q); ok {
			exact = a
		}
	}
	ctx := r.Context()
	where := `($1 = '' OR lower(t.name) LIKE $1 OR lower(t.symbol) LIKE $1 OR t.address = $2) AND ($3 = '' OR t.type = $3)`
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM tokens t WHERE `+where, pat, exact, typ).Scan(&total); err != nil {
		s.dbError(w, r, err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT `+tokenCols+` FROM tokens t WHERE `+where+`
		ORDER BY t.holders_count DESC, t.transfers_count DESC, t.address LIMIT $4 OFFSET $5`, pat, exact, typ, p.Limit, p.Offset)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	defer rows.Close()
	var items []Token
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			s.dbError(w, r, err)
			return
		}
		items = append(items, *t)
	}
	if err := rows.Err(); err != nil {
		s.dbError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, NewPage(items, total, p))
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	addr, ok := s.pathAddr(w, r)
	if !ok {
		return
	}
	t, err := s.loadToken(r.Context(), addr)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	if t == nil {
		notFound(w, r, "token not found")
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, t)
}

func tokenIDParam(r *http.Request) (string, bool, bool) {
	v := strings.TrimSpace(r.URL.Query().Get("token_id"))
	if v == "" {
		return "", false, true
	}
	n, ok := new(big.Int).SetString(v, 10)
	if !ok || n.Sign() < 0 || n.BitLen() > 256 {
		return "", true, false
	}
	return n.String(), true, true
}

func (s *Server) handleTokenTransfers(w http.ResponseWriter, r *http.Request) {
	addr, ok := s.pathAddr(w, r)
	if !ok {
		return
	}
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	where, args := `tt.token = $1`, []any{addr}
	id, has, valid := tokenIDParam(r)
	if !valid {
		badRequest(w, r, "token_id must be a non-negative integer")
		return
	}
	if has {
		where, args = where+` AND tt.token_id = $2::numeric`, append(args, id)
	}
	s.writeTransfers(w, r, p, where, args, "tt.block_number DESC, tt.log_index DESC, tt.batch_index DESC")
}

func (s *Server) handleTokenHolders(w http.ResponseWriter, r *http.Request) {
	addr, ok := s.pathAddr(w, r)
	if !ok {
		return
	}
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	id, hasID, valid := tokenIDParam(r)
	if !valid {
		badRequest(w, r, "token_id must be a non-negative integer")
		return
	}
	ctx := r.Context()
	tok, err := s.loadToken(ctx, addr)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	if tok == nil {
		notFound(w, r, "token not found")
		return
	}

	// count SQL, list SQL (holder, balance, token_id), denominator for share
	var countSQL, listSQL string
	args := []any{addr}
	var denom *big.Int
	switch {
	case tok.Type == "ERC-721" && !hasID:
		countSQL = `SELECT count(DISTINCT holder) FROM token_balances WHERE token = $1`
		listSQL = `SELECT holder, count(*)::text, NULL::text FROM token_balances WHERE token = $1
			GROUP BY holder ORDER BY count(*) DESC, holder LIMIT $2 OFFSET $3`
		var n int64
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM token_balances WHERE token = $1`, addr).Scan(&n); err == nil {
			denom = big.NewInt(n)
		}
	case hasID:
		args = append(args, id)
		countSQL = `SELECT count(*) FROM token_balances WHERE token = $1 AND token_id = $2::numeric`
		listSQL = `SELECT tb.holder, tb.balance::text, tb.token_id::text FROM token_balances tb WHERE tb.token = $1 AND tb.token_id = $2::numeric
			ORDER BY tb.balance DESC, tb.holder LIMIT $3 OFFSET $4`
		var sum *string
		if err := s.pool.QueryRow(ctx, `SELECT sum(balance)::text FROM token_balances WHERE token = $1 AND token_id = $2::numeric`, addr, id).Scan(&sum); err == nil && sum != nil {
			denom, _ = new(big.Int).SetString(*sum, 10)
		}
	default:
		countSQL = `SELECT count(*) FROM token_balances WHERE token = $1`
		listSQL = `SELECT tb.holder, tb.balance::text, NULLIF(tb.token_id, -1)::text FROM token_balances tb WHERE tb.token = $1
			ORDER BY tb.balance DESC, tb.holder, tb.token_id LIMIT $2 OFFSET $3`
		if tok.Type == "ERC-20" && tok.TotalSupply != nil {
			denom, _ = new(big.Int).SetString(*tok.TotalSupply, 10)
		}
	}
	var total int64
	if err := s.pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		s.dbError(w, r, err)
		return
	}
	rows, err := s.pool.Query(ctx, listSQL, append(args, p.Limit, p.Offset)...)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	type rec struct {
		holder, balance string
		tid             *string
	}
	var recs []rec
	rs := refSet{}
	for rows.Next() {
		var x rec
		if err := rows.Scan(&x.holder, &x.balance, &x.tid); err != nil {
			rows.Close()
			s.dbError(w, r, err)
			return
		}
		rs.add(x.holder)
		recs = append(recs, x)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.dbError(w, r, err)
		return
	}
	refs, err := s.loadRefs(ctx, rs)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	items := make([]TokenHolder, len(recs))
	for i, x := range recs {
		items[i] = TokenHolder{Address: refs.get(x.holder), Balance: x.balance, TokenID: x.tid, SharePct: sharePct(x.balance, denom)}
	}
	httpx.WriteJSON(w, r, http.StatusOK, NewPage(items, total, p))
}

// sharePct is balance/denom*100 rounded to 4 places; nil without a
// denominator.
func sharePct(balance string, denom *big.Int) *float64 {
	if denom == nil || denom.Sign() <= 0 {
		return nil
	}
	b, ok := new(big.Float).SetString(balance)
	if !ok {
		return nil
	}
	f, _ := new(big.Float).Quo(new(big.Float).Mul(b, big.NewFloat(100)), new(big.Float).SetInt(denom)).Float64()
	f = float64(int64(f*10000+0.5)) / 10000
	return &f
}

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/httpx"
)

// ---- blocks ----

const blockSummaryCols = `b.number, b.hash, b.timestamp, b.miner, b.tx_count, b.gas_used::text, b.gas_limit::text, b.size`

type blockRec struct {
	BlockSummary
	miner string
	ts    time.Time
}

func scanBlockSummary(row pgx.Row, extra ...any) (*blockRec, error) {
	var b blockRec
	dest := append([]any{&b.Number, &b.Hash, &b.ts, &b.miner, &b.TxCount, &b.GasUsed, &b.GasLimit, &b.Size}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	b.Timestamp = ISO(b.ts)
	return &b, nil
}

func (s *Server) finishBlocks(ctx context.Context, recs []*blockRec) ([]BlockSummary, error) {
	rs := refSet{}
	for _, b := range recs {
		rs.add(b.miner)
	}
	refs, err := s.loadRefs(ctx, rs)
	if err != nil {
		return nil, err
	}
	out := make([]BlockSummary, len(recs))
	for i, b := range recs {
		b.Miner = refs.get(b.miner)
		out[i] = b.BlockSummary
	}
	return out, nil
}

func (s *Server) handleBlocks(w http.ResponseWriter, r *http.Request) {
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	ctx := r.Context()
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM blocks`).Scan(&total); err != nil {
		s.dbError(w, r, err)
		return
	}
	rows, err := s.pool.Query(ctx, `SELECT `+blockSummaryCols+` FROM blocks b ORDER BY b.number DESC LIMIT $1 OFFSET $2`, p.Limit, p.Offset)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	var recs []*blockRec
	for rows.Next() {
		b, err := scanBlockSummary(rows)
		if err != nil {
			rows.Close()
			s.dbError(w, r, err)
			return
		}
		recs = append(recs, b)
	}
	rows.Close()
	items, err := s.finishBlocks(ctx, recs)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, NewPage(items, total, p))
}

// blockWhere turns a {number or hash} path value into a condition.
func blockWhere(id string) (string, any, bool) {
	if h, ok := normHash(id); ok {
		return "b.hash = $1", h, true
	}
	if n, err := strconv.ParseInt(id, 10, 64); err == nil && n >= 0 {
		return "b.number = $1", n, true
	}
	return "", nil, false
}

func (s *Server) handleBlock(w http.ResponseWriter, r *http.Request) {
	where, arg, ok := blockWhere(r.PathValue("id"))
	if !ok {
		badRequest(w, r, "block id must be a number or a block hash")
		return
	}
	ctx := r.Context()
	var blk Block
	b, err := scanBlockSummary(s.pool.QueryRow(ctx, `SELECT `+blockSummaryCols+`, b.parent_hash, b.state_root, b.transactions_root,
			b.receipts_root, b.base_fee_per_gas::text, b.extra_data, b.nonce, b.mix_hash FROM blocks b WHERE `+where, arg),
		&blk.ParentHash, &blk.StateRoot, &blk.TransactionsRoot, &blk.ReceiptsRoot, &blk.BaseFeePerGas, &blk.ExtraData, &blk.Nonce, &blk.MixHash)
	if isNoRows(err) {
		notFound(w, r, "block not found")
		return
	}
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	items, err := s.finishBlocks(ctx, []*blockRec{b})
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	blk.BlockSummary = items[0]
	blk.Confirmations = max(0, s.latestIndexed(ctx)-blk.Number+1)
	httpx.WriteJSON(w, r, http.StatusOK, blk)
}

func (s *Server) handleBlockTxs(w http.ResponseWriter, r *http.Request) {
	where, arg, ok := blockWhere(r.PathValue("id"))
	if !ok {
		badRequest(w, r, "block id must be a number or a block hash")
		return
	}
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	ctx := r.Context()
	var number, count int64
	if err := s.pool.QueryRow(ctx, `SELECT b.number, b.tx_count FROM blocks b WHERE `+where, arg).Scan(&number, &count); err != nil {
		if isNoRows(err) {
			notFound(w, r, "block not found")
			return
		}
		s.dbError(w, r, err)
		return
	}
	s.writeTxPage(w, r, p, count, `SELECT `+txSummaryCols+` FROM transactions t WHERE t.block_number = $1 ORDER BY t.tx_index LIMIT $2 OFFSET $3`, number, p.Limit, p.Offset)
}

// ---- transactions ----

const txSummaryCols = `t.hash, t.block_number, t.timestamp, t.tx_index, t.from_addr, t.to_addr, t.created_contract, t.value::text,
	t.gas_used, COALESCE(t.effective_gas_price, t.gas_price)::text, t.status, t.method_id, t.type`

type txRec struct {
	hash     string
	block    int64
	ts       time.Time
	idx      int64
	from     string
	to       *string
	created  *string
	value    string
	gasUsed  int64
	price    *string
	status   *int16
	methodID *string
	typ      int64
}

func (t *txRec) dest() []any {
	return []any{&t.hash, &t.block, &t.ts, &t.idx, &t.from, &t.to, &t.created, &t.value, &t.gasUsed, &t.price, &t.status, &t.methodID, &t.typ}
}

func statusText(st *int16) string {
	if st != nil && *st == 0 {
		return "failed"
	}
	return "ok"
}

// summaries renders txRecs with references and method names.
func (s *Server) summaries(ctx context.Context, recs []*txRec) ([]TxSummary, error) {
	rs := refSet{}
	for _, t := range recs {
		rs.add(t.from)
		rs.addPtr(t.to, t.created)
	}
	refs, err := s.loadRefs(ctx, rs)
	if err != nil {
		return nil, err
	}
	out := make([]TxSummary, len(recs))
	for i, t := range recs {
		out[i] = s.summary(ctx, t, refs)
	}
	return out, nil
}

func (s *Server) summary(ctx context.Context, t *txRec, refs refMap) TxSummary {
	ts := ISO(t.ts)
	sum := TxSummary{
		Hash: t.hash, BlockNumber: ptr(t.block), Timestamp: &ts, TxIndex: ptr(t.idx),
		From: refs.get(t.from), To: refs.getPtr(t.to), CreatedContract: refs.getPtr(t.created),
		Value: t.value, Fee: mulDecimal(t.gasUsed, t.price), Status: statusText(t.status), Type: t.typ,
	}
	if t.methodID != nil && t.to != nil {
		name := s.dec.MethodName(ctx, plainABIs(s.abisFor(ctx, *t.to, false)), *t.methodID)
		sum.Method = &name
	}
	return sum
}

func (s *Server) writeTxPage(w http.ResponseWriter, r *http.Request, p PageReq, total int64, sql string, args ...any) {
	ctx := r.Context()
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	var recs []*txRec
	for rows.Next() {
		t := &txRec{}
		if err := rows.Scan(t.dest()...); err != nil {
			rows.Close()
			s.dbError(w, r, err)
			return
		}
		recs = append(recs, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		s.dbError(w, r, err)
		return
	}
	items, err := s.summaries(ctx, recs)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, NewPage(items, total, p))
}

func (s *Server) handleTxs(w http.ResponseWriter, r *http.Request) {
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	var total int64
	if err := s.pool.QueryRow(r.Context(), `SELECT count(*) FROM transactions`).Scan(&total); err != nil {
		s.dbError(w, r, err)
		return
	}
	s.writeTxPage(w, r, p, total, `SELECT `+txSummaryCols+` FROM transactions t ORDER BY t.block_number DESC, t.tx_index DESC LIMIT $1 OFFSET $2`, p.Limit, p.Offset)
}

func (s *Server) handleTx(w http.ResponseWriter, r *http.Request) {
	hash, ok := normHash(r.PathValue("hash"))
	if !ok {
		badRequest(w, r, "not a transaction hash")
		return
	}
	ctx := r.Context()
	t := &txRec{}
	var tx Tx
	var input string
	dest := append(t.dest(), &tx.BlockHash, &tx.Nonce, &tx.Gas, &tx.GasPrice, &tx.EffectiveGasPrice, &tx.MaxFeePerGas,
		&tx.MaxPriorityFeePerGas, &input, &tx.Error, &tx.TokenTransfersCount, &tx.InternalTxsCount, &tx.LogsCount)
	err := s.pool.QueryRow(ctx, `SELECT `+txSummaryCols+`, t.block_hash, t.nonce, t.gas, t.gas_price::text, t.effective_gas_price::text,
			t.max_fee_per_gas::text, t.max_priority_fee_per_gas::text, t.input, t.error,
			(SELECT count(*) FROM token_transfers x WHERE x.tx_hash = t.hash),
			(SELECT count(*) FROM internal_txs x WHERE x.tx_hash = t.hash AND x.depth > 0),
			(SELECT count(*) FROM logs x WHERE x.tx_hash = t.hash)
		FROM transactions t WHERE t.hash = $1`, hash).Scan(dest...)
	if isNoRows(err) {
		s.txFromNode(w, r, hash)
		return
	}
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	rs := refSet{}
	rs.add(t.from)
	rs.addPtr(t.to, t.created)
	refs, err := s.loadRefs(ctx, rs)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	tx.TxSummary = s.summary(ctx, t, refs)
	tx.GasUsed = ptr(t.gasUsed)
	tx.Input = input
	tx.MethodID = t.methodID
	tx.Indexed = true
	tx.Confirmations = max(0, s.latestIndexed(ctx)-t.block+1)
	if t.to != nil {
		abis := plainABIs(s.abisFor(ctx, *t.to, true))
		tx.DecodedInput = s.dec.DecodeInput(ctx, abis, input)
		if t.methodID != nil {
			name := s.dec.MethodName(ctx, abis, *t.methodID)
			tx.Method = &name
		}
	}
	httpx.WriteJSON(w, r, http.StatusOK, tx)
}

// txFromNode answers a transaction the database does not have yet straight
// from the node (indexed:false), and nudges the indexer.
func (s *Server) txFromNode(w http.ResponseWriter, r *http.Request, hash string) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var txRaw, rcRaw json.RawMessage
	elems := []chainrpc.BatchElem{
		{Method: "eth_getTransactionByHash", Params: []any{hash}, Result: &txRaw},
		{Method: "eth_getTransactionReceipt", Params: []any{hash}, Result: &rcRaw},
	}
	if err := s.rpc.BatchCall(ctx, elems); err != nil || elems[0].Error != nil {
		httpx.Error(w, r, http.StatusNotFound, "transaction not found (node unreachable)")
		return
	}
	if len(txRaw) == 0 || string(txRaw) == "null" {
		notFound(w, r, "transaction not found")
		return
	}
	var nt chainrpc.Transaction
	if err := json.Unmarshal(txRaw, &nt); err != nil {
		notFound(w, r, "transaction not found")
		return
	}
	var rc *chainrpc.Receipt
	if len(rcRaw) > 0 && string(rcRaw) != "null" {
		rc = &chainrpc.Receipt{}
		if json.Unmarshal(rcRaw, rc) != nil {
			rc = nil
		}
	}
	s.nudgeIndexer()

	from := chainrpc.Lower(nt.From)
	var to, created *string
	if nt.To != nil && *nt.To != "" {
		to = ptr(chainrpc.Lower(*nt.To))
	}
	input := nt.InputHex()
	rs := refSet{}
	rs.add(from)
	rs.addPtr(to)
	tx := Tx{
		TxSummary: TxSummary{Hash: hash, Value: decimalQ(nt.Value), Fee: "0", Status: "pending", Type: int64(nt.Type.Uint64())},
		Nonce:     int64(nt.Nonce.Uint64()), Gas: int64(nt.Gas.Uint64()), Input: input,
		GasPrice: bigText(nt.GasPrice), MaxFeePerGas: bigText(nt.MaxFeePerGas), MaxPriorityFeePerGas: bigText(nt.MaxPriorityFeePerGas),
		Indexed: false,
	}
	if nt.BlockHash != nil && *nt.BlockHash != "" {
		tx.BlockHash = ptr(chainrpc.Lower(*nt.BlockHash))
		tx.BlockNumber = ptr(int64(nt.BlockNumber.Uint64()))
		tx.TxIndex = ptr(int64(nt.TransactionIndex.Uint64()))
		var hdr struct {
			Timestamp chainrpc.Quantity `json:"timestamp"`
		}
		if err := s.rpc.Call(ctx, &hdr, "eth_getBlockByHash", *tx.BlockHash, false); err == nil && hdr.Timestamp.Valid {
			ts := ISO(time.Unix(int64(hdr.Timestamp.Uint64()), 0))
			tx.Timestamp = &ts
		}
		if h := s.nodeHead(ctx); h != nil {
			tx.Confirmations = max(0, *h-*tx.BlockNumber+1)
		}
	}
	if rc != nil {
		gu := int64(rc.GasUsed.Uint64())
		tx.GasUsed = &gu
		tx.EffectiveGasPrice = bigText(rc.EffectiveGasPrice)
		price := tx.EffectiveGasPrice
		if price == nil {
			price = tx.GasPrice
		}
		tx.Fee = mulDecimal(gu, price)
		tx.Status = "ok"
		if rc.Status.Valid && rc.Status.Uint64() == 0 {
			tx.Status = "failed"
		}
		if rc.ContractAddress != nil && *rc.ContractAddress != "" && to == nil {
			created = ptr(chainrpc.Lower(*rc.ContractAddress))
			rs.addPtr(created)
		}
		tx.LogsCount = int64(len(rc.Logs))
	}
	refs, err := s.loadRefs(ctx, rs)
	if err != nil {
		refs = refMap{}
	}
	tx.From, tx.To, tx.CreatedContract = refs.get(from), refs.getPtr(to), refs.getPtr(created)
	if to != nil && len(input) >= 10 {
		mid := input[:10]
		tx.MethodID = &mid
		abis := plainABIs(s.abisFor(ctx, *to, true))
		name := s.dec.MethodName(ctx, abis, mid)
		tx.Method = &name
		tx.DecodedInput = s.dec.DecodeInput(ctx, abis, input)
	}
	httpx.WriteJSON(w, r, http.StatusOK, tx)
}

func decimalQ(q chainrpc.Quantity) string {
	if v := q.Big(); v != nil {
		return v.String()
	}
	return "0"
}

func bigText(q chainrpc.Quantity) *string {
	if v := q.Big(); v != nil {
		s := v.String()
		return &s
	}
	return nil
}

func (s *Server) txExists(ctx context.Context, hash string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM transactions WHERE hash = $1)`, hash).Scan(&ok)
	return ok, err
}

func (s *Server) handleTxTokenTransfers(w http.ResponseWriter, r *http.Request) {
	hash, ok := normHash(r.PathValue("hash"))
	if !ok {
		badRequest(w, r, "not a transaction hash")
		return
	}
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	s.writeTransfers(w, r, p, `tt.tx_hash = $1`, []any{hash}, "tt.log_index, tt.batch_index")
}

func (s *Server) handleTxInternal(w http.ResponseWriter, r *http.Request) {
	hash, ok := normHash(r.PathValue("hash"))
	if !ok {
		badRequest(w, r, "not a transaction hash")
		return
	}
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	s.writeInternal(w, r, p, `i.tx_hash = $1 AND i.depth > 0`, []any{hash}, "string_to_array(i.trace_address, '.')::int[]")
}

func (s *Server) handleTxLogs(w http.ResponseWriter, r *http.Request) {
	hash, ok := normHash(r.PathValue("hash"))
	if !ok {
		badRequest(w, r, "not a transaction hash")
		return
	}
	p, err := ParsePage(r)
	if err != nil {
		badRequest(w, r, err.Error())
		return
	}
	s.writeLogs(w, r, p, `l.tx_hash = $1`, []any{hash}, "l.log_index")
}

func (s *Server) handleTxRaw(w http.ResponseWriter, r *http.Request) {
	hash, ok := normHash(r.PathValue("hash"))
	if !ok {
		badRequest(w, r, "not a transaction hash")
		return
	}
	ctx := r.Context()
	var txr, rcr, trr []byte
	err := s.pool.QueryRow(ctx, `SELECT raw, receipt_raw, trace_raw FROM transactions WHERE hash = $1`, hash).Scan(&txr, &rcr, &trr)
	if isNoRows(err) {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var t, rc json.RawMessage
		elems := []chainrpc.BatchElem{
			{Method: "eth_getTransactionByHash", Params: []any{hash}, Result: &t},
			{Method: "eth_getTransactionReceipt", Params: []any{hash}, Result: &rc},
		}
		if err := s.rpc.BatchCall(cctx, elems); err != nil || len(t) == 0 || string(t) == "null" {
			notFound(w, r, "transaction not found")
			return
		}
		s.nudgeIndexer()
		httpx.WriteJSON(w, r, http.StatusOK, map[string]json.RawMessage{"tx": t, "receipt": orNull(rc), "trace": jsonNull})
		return
	}
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, map[string]json.RawMessage{"tx": orNull(txr), "receipt": orNull(rcr), "trace": orNull(trr)})
}

func orNull(b []byte) json.RawMessage {
	if len(b) == 0 {
		return jsonNull
	}
	return json.RawMessage(b)
}

// ---- shared list writers ----

const transferCols = `tt.tx_hash, tt.block_number, tt.log_index, tt.batch_index, tt.timestamp, tt.token, tt.token_type,
	tt.from_addr, tt.to_addr, tt.amount::text, tt.token_id::text, tok.name, tok.symbol, tok.decimals, tok.type, tx.to_addr, tx.method_id`

func (s *Server) writeTransfers(w http.ResponseWriter, r *http.Request, p PageReq, where string, args []any, order string) {
	ctx := r.Context()
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM token_transfers tt WHERE `+where, args...).Scan(&total); err != nil {
		s.dbError(w, r, err)
		return
	}
	n := len(args)
	rows, err := s.pool.Query(ctx, `SELECT `+transferCols+`
		FROM token_transfers tt
		LEFT JOIN tokens tok ON tok.address = tt.token
		LEFT JOIN transactions tx ON tx.hash = tt.tx_hash
		WHERE `+where+` ORDER BY `+order+` LIMIT $`+strconv.Itoa(n+1)+` OFFSET $`+strconv.Itoa(n+2),
		append(args, p.Limit, p.Offset)...)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	type rec struct {
		TokenTransfer
		token, ttype, from, to string
		tname, tsym, tktype    *string
		tdec                   *int32
		txTo, txMethod         *string
		ts                     time.Time
	}
	var recs []*rec
	rs := refSet{}
	for rows.Next() {
		x := &rec{}
		if err := rows.Scan(&x.TxHash, &x.BlockNumber, &x.LogIndex, &x.BatchIndex, &x.ts, &x.token, &x.ttype, &x.from, &x.to,
			&x.Amount, &x.TokenID, &x.tname, &x.tsym, &x.tdec, &x.tktype, &x.txTo, &x.txMethod); err != nil {
			rows.Close()
			s.dbError(w, r, err)
			return
		}
		rs.add(x.from, x.to)
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
	items := make([]TokenTransfer, len(recs))
	for i, x := range recs {
		t := x.TokenTransfer
		t.Timestamp = ISO(x.ts)
		typ := x.ttype
		if x.tktype != nil {
			typ = *x.tktype
		}
		t.Token = TokenRef{Address: EIP55(x.token), Name: x.tname, Symbol: x.tsym, Type: typ}
		if x.tdec != nil {
			d := int64(*x.tdec)
			t.Token.Decimals = &d
		}
		t.From, t.To = refs.get(x.from), refs.get(x.to)
		if x.txMethod != nil && x.txTo != nil {
			name := s.dec.MethodName(ctx, plainABIs(s.abisFor(ctx, *x.txTo, false)), *x.txMethod)
			t.Method = &name
		}
		items[i] = t
	}
	httpx.WriteJSON(w, r, http.StatusOK, NewPage(items, total, p))
}

func (s *Server) writeInternal(w http.ResponseWriter, r *http.Request, p PageReq, where string, args []any, order string) {
	ctx := r.Context()
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM internal_txs i WHERE `+where, args...).Scan(&total); err != nil {
		s.dbError(w, r, err)
		return
	}
	n := len(args)
	rows, err := s.pool.Query(ctx, `SELECT i.tx_hash, i.block_number, i.trace_address, i.depth, i.call_type, i.from_addr, i.to_addr,
			i.created_contract, i.value::text, i.gas, i.gas_used, COALESCE(i.revert_reason, i.error), b.timestamp
		FROM internal_txs i JOIN blocks b ON b.number = i.block_number
		WHERE `+where+` ORDER BY `+order+` LIMIT $`+strconv.Itoa(n+1)+` OFFSET $`+strconv.Itoa(n+2), append(args, p.Limit, p.Offset)...)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	type rec struct {
		InternalTx
		from        string
		to, created *string
		ts          time.Time
	}
	var recs []*rec
	rs := refSet{}
	for rows.Next() {
		x := &rec{}
		if err := rows.Scan(&x.TxHash, &x.BlockNumber, &x.TraceAddress, &x.Depth, &x.CallType, &x.from, &x.to, &x.created,
			&x.Value, &x.Gas, &x.GasUsed, &x.Error, &x.ts); err != nil {
			rows.Close()
			s.dbError(w, r, err)
			return
		}
		if strings.HasPrefix(x.CallType, "CREATE") {
			x.to = nil // the frame's "to" is the new address: shown as created_contract
		}
		rs.add(x.from)
		rs.addPtr(x.to, x.created)
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
	items := make([]InternalTx, len(recs))
	for i, x := range recs {
		it := x.InternalTx
		it.Timestamp = ISO(x.ts)
		it.From, it.To, it.CreatedContract = refs.get(x.from), refs.getPtr(x.to), refs.getPtr(x.created)
		items[i] = it
	}
	httpx.WriteJSON(w, r, http.StatusOK, NewPage(items, total, p))
}

func (s *Server) writeLogs(w http.ResponseWriter, r *http.Request, p PageReq, where string, args []any, order string) {
	ctx := r.Context()
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM logs l WHERE `+where, args...).Scan(&total); err != nil {
		s.dbError(w, r, err)
		return
	}
	n := len(args)
	rows, err := s.pool.Query(ctx, `SELECT l.tx_hash, l.block_number, l.log_index, l.address, l.topic0, l.topic1, l.topic2, l.topic3, l.data
		FROM logs l WHERE `+where+` ORDER BY `+order+` LIMIT $`+strconv.Itoa(n+1)+` OFFSET $`+strconv.Itoa(n+2), append(args, p.Limit, p.Offset)...)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	type rec struct {
		Log
		addr string
		t    [4]*string
	}
	var recs []*rec
	rs := refSet{}
	for rows.Next() {
		x := &rec{}
		if err := rows.Scan(&x.TxHash, &x.BlockNumber, &x.LogIndex, &x.addr, &x.t[0], &x.t[1], &x.t[2], &x.t[3], &x.Data); err != nil {
			rows.Close()
			s.dbError(w, r, err)
			return
		}
		rs.add(x.addr)
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
	abiCache := map[string][]*abiWithRaw{}
	items := make([]Log, len(recs))
	for i, x := range recs {
		l := x.Log
		l.Address = refs.get(x.addr)
		l.Topics = x.t[:]
		real := 0
		for real < 4 && x.t[real] != nil {
			real++
		}
		abis, ok := abiCache[x.addr]
		if !ok {
			abis = s.abisFor(ctx, x.addr, true)
			abiCache[x.addr] = abis
		}
		l.Decoded = s.dec.DecodeLog(ctx, plainABIs(abis), x.t[:real], x.Data)
		items[i] = l
	}
	httpx.WriteJSON(w, r, http.StatusOK, NewPage(items, total, p))
}

package indexer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/evm"
)

// Token types as stored.
const (
	TypeERC20   = "ERC-20"
	TypeERC721  = "ERC-721"
	TypeERC1155 = "ERC-1155"
)

type txRow struct {
	Hash        string
	TxIndex     int
	From        string
	To          *string
	Created     *string
	Value       *big.Int
	Nonce       int64
	Type        int
	Gas         int64
	GasPrice    *big.Int
	MaxFee      *big.Int
	MaxPrio     *big.Int
	GasUsed     int64
	CumGasUsed  int64
	EffGasPrice *big.Int
	Status      *int16
	Input       string
	MethodID    *string
	Error       *string
	Raw         json.RawMessage
	ReceiptRaw  json.RawMessage
	TraceRaw    json.RawMessage // nil with TRACES=off
}

type logRow struct {
	LogIndex int
	TxHash   string
	TxIndex  int
	Address  string
	Topics   [4]*string
	Data     string
}

type internalRow struct {
	TxHash       string
	TxIndex      int
	TraceAddress string
	Depth        int
	CallType     string
	From         string
	To           *string
	Value        *big.Int
	Gas          *int64
	GasUsed      *int64
	Input        *string
	Output       *string
	Error        *string
	RevertReason *string
	Created      *string
	effectiveOK  bool // neither this frame nor any ancestor failed
}

type transferRow struct {
	LogIndex   int
	BatchIndex int
	TxHash     string
	Token      string
	TokenType  string
	From       string
	To         string
	Operator   *string
	Amount     *big.Int // nil for ERC-721
	TokenID    *big.Int // nil for ERC-20
}

type creation struct {
	Address      string
	Creator      string
	TxHash       string
	TraceAddress string
	Input        string
}

type seenAddr struct {
	TxCount int
	Nonce   *int64 // next nonce after this block's txs from it
}

// parsedBlock is everything derivable from the node's block, receipts and
// traces alone, without any state read or database knowledge.
type parsedBlock struct {
	Number     uint64
	Hash       string
	ParentHash string
	Time       time.Time
	Block      chainrpc.Block
	HeaderRaw  json.RawMessage

	Txs       []txRow
	Logs      []logRow
	Internal  []internalRow
	Transfers []transferRow
	Creations []creation

	Seen        map[string]*seenAddr
	TokenTypes  map[string]string // token address -> type of its first transfer log in this block
	TokenCounts map[string]int    // transfers per token in this block
}

var errInconsistent = errors.New("node returned inconsistent block data")

// parseBlock derives rows from one block. receipts must be in transaction
// order; traces likewise (nil when traces are off).
func parseBlock(blockRaw json.RawMessage, receipts []json.RawMessage, traces []json.RawMessage) (*parsedBlock, error) {
	var b chainrpc.Block
	if err := json.Unmarshal(blockRaw, &b); err != nil {
		return nil, fmt.Errorf("decode block: %w", err)
	}
	if !b.Number.Valid || b.Hash == "" {
		return nil, fmt.Errorf("%w: block without number or hash", errInconsistent)
	}
	pb := &parsedBlock{
		Number:      b.Number.Uint64(),
		Hash:        chainrpc.Lower(b.Hash),
		ParentHash:  chainrpc.Lower(b.ParentHash),
		Time:        time.Unix(int64(b.Timestamp.Uint64()), 0).UTC(),
		Block:       b,
		Seen:        map[string]*seenAddr{},
		TokenTypes:  map[string]string{},
		TokenCounts: map[string]int{},
	}
	hdr, err := headerRaw(blockRaw)
	if err != nil {
		return nil, err
	}
	pb.HeaderRaw = hdr

	if len(receipts) != len(b.Transactions) {
		return nil, fmt.Errorf("%w: block %d has %d transactions but %d receipts", errInconsistent, pb.Number, len(b.Transactions), len(receipts))
	}
	if traces != nil && len(traces) != len(b.Transactions) {
		return nil, fmt.Errorf("%w: block %d has %d transactions but %d traces", errInconsistent, pb.Number, len(b.Transactions), len(traces))
	}

	pb.see(b.Miner)
	for _, w := range b.Withdrawals {
		pb.see(w.Address)
	}

	for i, txRaw := range b.Transactions {
		var tx chainrpc.Transaction
		if err := json.Unmarshal(txRaw, &tx); err != nil {
			return nil, fmt.Errorf("block %d tx %d: %w", pb.Number, i, err)
		}
		var rc chainrpc.Receipt
		if err := json.Unmarshal(receipts[i], &rc); err != nil {
			return nil, fmt.Errorf("block %d receipt %d: %w", pb.Number, i, err)
		}
		hash := chainrpc.Lower(tx.Hash)
		if chainrpc.Lower(rc.TransactionHash) != hash {
			return nil, fmt.Errorf("%w: block %d receipt %d is for %s, not %s", errInconsistent, pb.Number, i, rc.TransactionHash, hash)
		}
		if rc.BlockHash != "" && chainrpc.Lower(rc.BlockHash) != pb.Hash {
			return nil, fmt.Errorf("%w: block %d receipt %d belongs to block %s", errInconsistent, pb.Number, i, rc.BlockHash)
		}
		row := txRow{
			Hash:        hash,
			TxIndex:     i,
			From:        chainrpc.Lower(tx.From),
			Value:       bigOrZero(tx.Value),
			Nonce:       int64(tx.Nonce.Uint64()),
			Type:        int(tx.Type.Uint64()),
			Gas:         int64(tx.Gas.Uint64()),
			GasPrice:    tx.GasPrice.Big(),
			MaxFee:      tx.MaxFeePerGas.Big(),
			MaxPrio:     tx.MaxPriorityFeePerGas.Big(),
			GasUsed:     int64(rc.GasUsed.Uint64()),
			CumGasUsed:  int64(rc.CumulativeGasUsed.Uint64()),
			EffGasPrice: rc.EffectiveGasPrice.Big(),
			Input:       tx.InputHex(),
			Raw:         sanitizeJSON(txRaw),
			ReceiptRaw:  sanitizeJSON(receipts[i]),
		}
		if row.EffGasPrice == nil {
			row.EffGasPrice = row.GasPrice
		}
		if rc.Status.Valid {
			s := int16(rc.Status.Uint64())
			row.Status = &s
		}
		if tx.To != nil && *tx.To != "" {
			to := chainrpc.Lower(*tx.To)
			row.To = &to
			if len(row.Input) >= 10 {
				m := row.Input[:10]
				row.MethodID = &m
			}
		}
		if rc.ContractAddress != nil && *rc.ContractAddress != "" && row.To == nil {
			c := chainrpc.Lower(*rc.ContractAddress)
			if row.Status == nil || *row.Status == 1 {
				row.Created = &c
			}
		}

		// Address bookkeeping: each of from/to/created counts this tx once.
		involved := map[string]bool{row.From: true}
		if row.To != nil {
			involved[*row.To] = true
		}
		if row.Created != nil {
			involved[*row.Created] = true
		}
		for a := range involved {
			pb.see(a).TxCount++
		}
		next := row.Nonce + 1
		if s := pb.see(row.From); s.Nonce == nil || *s.Nonce < next {
			s.Nonce = &next
		}

		// Call frames.
		if traces != nil {
			row.TraceRaw = sanitizeJSON(traces[i])
			var root chainrpc.CallFrame
			if err := json.Unmarshal(traces[i], &root); err != nil {
				return nil, fmt.Errorf("block %d trace %d: %w", pb.Number, i, err)
			}
			if root.Type == "" {
				return nil, fmt.Errorf("%w: block %d trace %d has no root frame", errInconsistent, pb.Number, i)
			}
			txOK := row.Status == nil || *row.Status == 1
			frames := flattenTrace(hash, i, &root, txOK)
			for _, f := range frames {
				pb.see(f.From)
				if f.To != nil {
					pb.see(*f.To)
				}
				if f.Created != nil {
					pb.see(*f.Created)
					if f.Depth > 0 {
						pb.Creations = append(pb.Creations, creation{Address: *f.Created, Creator: f.From, TxHash: hash, TraceAddress: f.TraceAddress, Input: deref(f.Input)})
					}
				}
			}
			pb.Internal = append(pb.Internal, frames...)
			if root.Error != "" {
				e := root.Error
				if root.RevertReason != "" {
					e = root.RevertReason
				}
				e = evm.CleanText(e, 2000)
				row.Error = &e
			}
		}
		if row.Created != nil {
			pb.Creations = append(pb.Creations, creation{Address: *row.Created, Creator: row.From, TxHash: hash, TraceAddress: "", Input: row.Input})
		}
		if row.Error == nil && row.Status != nil && *row.Status == 0 {
			e := "execution failed"
			row.Error = &e
		}
		pb.Txs = append(pb.Txs, row)

		for _, lg := range rc.Logs {
			if lg.Removed {
				continue
			}
			lr := logRow{
				LogIndex: int(lg.LogIndex.Uint64()),
				TxHash:   hash,
				TxIndex:  i,
				Address:  chainrpc.Lower(lg.Address),
				Data:     strings.ToLower(orHex(lg.Data)),
			}
			for k := 0; k < len(lg.Topics) && k < 4; k++ {
				t := chainrpc.Lower(lg.Topics[k])
				lr.Topics[k] = &t
			}
			pb.see(lr.Address)
			pb.Logs = append(pb.Logs, lr)
			for _, tr := range decodeTransfers(lr, len(lg.Topics)) {
				pb.see(tr.From)
				pb.see(tr.To)
				if _, ok := pb.TokenTypes[tr.Token]; !ok {
					pb.TokenTypes[tr.Token] = tr.TokenType
				}
				pb.TokenCounts[tr.Token]++
				pb.Transfers = append(pb.Transfers, tr)
			}
		}
	}
	return pb, nil
}

func (pb *parsedBlock) see(addr string) *seenAddr {
	addr = chainrpc.Lower(addr)
	s, ok := pb.Seen[addr]
	if !ok {
		s = &seenAddr{}
		pb.Seen[addr] = s
	}
	return s
}

// flattenTrace walks a callTracer tree depth first. The root has trace
// address "" and depth 0; children are "0", "1", "0.2", ...
func flattenTrace(txHash string, txIndex int, root *chainrpc.CallFrame, txOK bool) []internalRow {
	var out []internalRow
	var walk func(f *chainrpc.CallFrame, path []int, parentOK bool)
	walk = func(f *chainrpc.CallFrame, path []int, parentOK bool) {
		ok := parentOK && f.Error == ""
		r := internalRow{
			TxHash:       txHash,
			TxIndex:      txIndex,
			TraceAddress: joinPath(path),
			Depth:        len(path),
			CallType:     strings.ToUpper(f.Type),
			From:         chainrpc.Lower(f.From),
			Value:        bigOrZero(f.Value),
			Gas:          f.Gas.Ptr64(),
			GasUsed:      f.GasUsed.Ptr64(),
			effectiveOK:  ok,
		}
		if f.To != "" {
			to := chainrpc.Lower(f.To)
			r.To = &to
		}
		if f.Input != "" {
			in := strings.ToLower(f.Input)
			r.Input = &in
		}
		if f.Output != "" {
			o := strings.ToLower(f.Output)
			r.Output = &o
		}
		if f.Error != "" {
			e := evm.CleanText(f.Error, 2000)
			r.Error = &e
		}
		if f.RevertReason != "" {
			rr := evm.CleanText(f.RevertReason, 2000)
			r.RevertReason = &rr
		}
		if (r.CallType == "CREATE" || r.CallType == "CREATE2") && ok && r.To != nil {
			c := *r.To
			r.Created = &c
		}
		out = append(out, r)
		for i := range f.Calls {
			child := append(append([]int(nil), path...), i)
			walk(&f.Calls[i], child, ok)
		}
	}
	walk(root, nil, txOK)
	return out
}

func joinPath(p []int) string {
	if len(p) == 0 {
		return ""
	}
	parts := make([]string, len(p))
	for i, v := range p {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ".")
}

// decodeTransfers turns one log into token transfers (none when it is not a
// well-formed ERC-20/721/1155 transfer event).
func decodeTransfers(lg logRow, ntopics int) []transferRow {
	if lg.Topics[0] == nil {
		return nil
	}
	data := evm.Bytes(lg.Data)
	switch *lg.Topics[0] {
	case evm.TopicTransfer:
		if ntopics != 3 && ntopics != 4 {
			return nil
		}
		from, ok1 := evm.TopicAddress(*lg.Topics[1])
		to, ok2 := evm.TopicAddress(*lg.Topics[2])
		if !ok1 || !ok2 {
			return nil
		}
		tr := transferRow{LogIndex: lg.LogIndex, TxHash: lg.TxHash, Token: lg.Address, From: from, To: to}
		if ntopics == 3 {
			amt, ok := evm.DecodeUint(data)
			if !ok {
				return nil
			}
			tr.TokenType, tr.Amount = TypeERC20, amt
		} else {
			id, ok := evm.TopicUint(*lg.Topics[3])
			if !ok {
				return nil
			}
			tr.TokenType, tr.TokenID = TypeERC721, id
		}
		return []transferRow{tr}
	case evm.TopicTransferSingle:
		if ntopics != 4 || len(data) < 64 {
			return nil
		}
		op, ok0 := evm.TopicAddress(*lg.Topics[1])
		from, ok1 := evm.TopicAddress(*lg.Topics[2])
		to, ok2 := evm.TopicAddress(*lg.Topics[3])
		if !ok0 || !ok1 || !ok2 {
			return nil
		}
		return []transferRow{{
			LogIndex: lg.LogIndex, TxHash: lg.TxHash, Token: lg.Address, TokenType: TypeERC1155,
			From: from, To: to, Operator: &op,
			TokenID: new(big.Int).SetBytes(data[:32]), Amount: new(big.Int).SetBytes(data[32:64]),
		}}
	case evm.TopicTransferBatch:
		if ntopics != 4 {
			return nil
		}
		op, ok0 := evm.TopicAddress(*lg.Topics[1])
		from, ok1 := evm.TopicAddress(*lg.Topics[2])
		to, ok2 := evm.TopicAddress(*lg.Topics[3])
		if !ok0 || !ok1 || !ok2 {
			return nil
		}
		ids, okI := decodeUintArray(data, 0)
		vals, okV := decodeUintArray(data, 1)
		if !okI || !okV || len(ids) != len(vals) {
			return nil
		}
		out := make([]transferRow, len(ids))
		for i := range ids {
			opc := op
			out[i] = transferRow{
				LogIndex: lg.LogIndex, BatchIndex: i, TxHash: lg.TxHash, Token: lg.Address, TokenType: TypeERC1155,
				From: from, To: to, Operator: &opc, TokenID: ids[i], Amount: vals[i],
			}
		}
		return out
	}
	return nil
}

// decodeUintArray reads the uint256[] whose offset is head word idx.
func decodeUintArray(data []byte, idx int) ([]*big.Int, bool) {
	if len(data) < 32*(idx+1) {
		return nil, false
	}
	off := new(big.Int).SetBytes(data[32*idx : 32*idx+32])
	if !off.IsUint64() || off.Uint64()+32 > uint64(len(data)) {
		return nil, false
	}
	o := int(off.Uint64())
	n := new(big.Int).SetBytes(data[o : o+32])
	if !n.IsUint64() || n.Uint64() > uint64((len(data)-o-32)/32) {
		return nil, false
	}
	out := make([]*big.Int, n.Uint64())
	for i := range out {
		s := o + 32 + 32*i
		out[i] = new(big.Int).SetBytes(data[s : s+32])
	}
	return out, true
}

// headerRaw is the block JSON with its transactions list replaced by hashes.
func headerRaw(blockRaw json.RawMessage) (json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(blockRaw, &m); err != nil {
		return nil, fmt.Errorf("decode block: %w", err)
	}
	if txs, ok := m["transactions"]; ok {
		var objs []json.RawMessage
		if err := json.Unmarshal(txs, &objs); err != nil {
			return nil, fmt.Errorf("decode block transactions: %w", err)
		}
		hashes := make([]string, 0, len(objs))
		for _, o := range objs {
			if len(o) > 0 && o[0] == '"' { // already a hash
				var h string
				_ = json.Unmarshal(o, &h)
				hashes = append(hashes, h)
				continue
			}
			var t struct {
				Hash string `json:"hash"`
			}
			if err := json.Unmarshal(o, &t); err != nil {
				return nil, err
			}
			hashes = append(hashes, t.Hash)
		}
		enc, _ := json.Marshal(hashes)
		m["transactions"] = enc
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return sanitizeJSON(out), nil
}

// sanitizeJSON replaces \u0000 escapes, which jsonb refuses, with �.
// Escaped backslashes are tracked so a literal "\\u0000" in text is kept.
func sanitizeJSON(raw []byte) json.RawMessage {
	if !bytes.Contains(raw, []byte(`\u0000`)) {
		return json.RawMessage(raw)
	}
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '\\' && i+1 < len(raw) {
			if raw[i+1] == 'u' && i+6 <= len(raw) && string(raw[i:i+6]) == `\u0000` {
				out = append(out, `�`...)
				i += 5
				continue
			}
			out = append(out, c, raw[i+1])
			i++
			continue
		}
		out = append(out, c)
	}
	return json.RawMessage(out)
}

func bigOrZero(q chainrpc.Quantity) *big.Int {
	if v := q.Big(); v != nil {
		return v
	}
	return new(big.Int)
}

func orHex(s string) string {
	if s == "" {
		return "0x"
	}
	return s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// sortedKeys returns map keys in order (deterministic batches and tests).
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

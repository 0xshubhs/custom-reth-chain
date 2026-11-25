package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/evm"
)

// A tiny simulated chain: blocks are built as the node would serve them
// (JSON), and the state a node would answer at each height (native and token
// balances, code) is kept so tests can stand in for the node's state reads.

func addr(c byte) string   { return "0x" + strings.Repeat(string([]byte{c}), 40) }
func hash(c byte) string   { return "0x" + strings.Repeat(string([]byte{c}), 64) }
func hex64(v int64) string { return fmt.Sprintf("0x%x", v) }
func word(v int64) string  { return fmt.Sprintf("%064x", v) }
func topicAddr(a string) string {
	return "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(a, "0x")
}
func topicNum(v int64) string { return "0x" + word(v) }

type simLog struct {
	addr   string
	topics []string
	data   string
}

type simTx struct {
	hash    string
	from    string
	to      string // "" = contract creation
	created string // receipt contractAddress
	value   int64
	nonce   int64
	failed  bool
	input   string
	logs    []simLog
	trace   map[string]any // callTracer root; nil = a plain CALL/CREATE frame
}

type simBlock struct {
	n           uint64
	hash        string
	miner       string
	txs         []simTx
	withdrawals []string
}

// simChain keeps blocks and the state after each one.
type simChain struct {
	blocks []simBlock
	// state after block n
	native []map[string]*big.Int
	fung   []map[fungKey]*big.Int
	nft    []map[nftKey]string
	multi  []map[multiKey]*big.Int
	supply []map[string]*big.Int
	code   map[string]string
}

func newSimChain() *simChain { return &simChain{code: map[string]string{}} }

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// add appends a block whose parent is the current tip and advances state.
func (c *simChain) add(b simBlock, genesisAlloc map[string]int64) {
	var nat map[string]*big.Int
	var fung map[fungKey]*big.Int
	var nft map[nftKey]string
	var multi map[multiKey]*big.Int
	var sup map[string]*big.Int
	if len(c.blocks) == 0 {
		nat, fung, nft, multi, sup = map[string]*big.Int{}, map[fungKey]*big.Int{}, map[nftKey]string{}, map[multiKey]*big.Int{}, map[string]*big.Int{}
		for a, v := range genesisAlloc {
			nat[a] = big.NewInt(v)
		}
	} else {
		i := len(c.blocks) - 1
		nat, fung, nft, multi, sup = cloneMap(c.native[i]), cloneMap(c.fung[i]), cloneMap(c.nft[i]), cloneMap(c.multi[i]), cloneMap(c.supply[i])
	}
	addBig := func(m map[string]*big.Int, k string, d int64) {
		v := new(big.Int)
		if m[k] != nil {
			v.Set(m[k])
		}
		m[k] = v.Add(v, big.NewInt(d))
	}
	for _, w := range b.withdrawals {
		addBig(nat, w, 1_000_000_000) // 1 gwei
	}
	for _, tx := range b.txs {
		if tx.failed {
			continue
		}
		if tx.value != 0 {
			to := tx.to
			if to == "" {
				to = tx.created
			}
			addBig(nat, tx.from, -tx.value)
			addBig(nat, to, tx.value)
		}
		for _, lg := range tx.logs {
			lr := logRow{Address: lg.addr, Data: "0x" + lg.data}
			for i, t := range lg.topics {
				t := t
				lr.Topics[i] = &t
			}
			for _, tr := range decodeTransfers(lr, len(lg.topics)) {
				switch tr.TokenType {
				case TypeERC20:
					a := tr.Amount.Int64()
					if tr.From == chainrpc.ZeroAddress {
						addBig(sup, tr.Token, a)
					} else {
						k := fungKey{tr.Token, tr.From}
						fung[k] = new(big.Int).Sub(bigOr0(fung[k]), tr.Amount)
					}
					if tr.To == chainrpc.ZeroAddress {
						addBig(sup, tr.Token, -a)
					} else {
						k := fungKey{tr.Token, tr.To}
						fung[k] = new(big.Int).Add(bigOr0(fung[k]), tr.Amount)
					}
				case TypeERC721:
					k := nftKey{tr.Token, tr.TokenID.String()}
					if tr.To == chainrpc.ZeroAddress {
						nft[k] = ""
					} else {
						nft[k] = tr.To
					}
				case TypeERC1155:
					id := tr.TokenID.String()
					if tr.From != chainrpc.ZeroAddress {
						k := multiKey{tr.Token, tr.From, id}
						multi[k] = new(big.Int).Sub(bigOr0(multi[k]), tr.Amount)
					}
					if tr.To != chainrpc.ZeroAddress {
						k := multiKey{tr.Token, tr.To, id}
						multi[k] = new(big.Int).Add(bigOr0(multi[k]), tr.Amount)
					}
				}
			}
		}
	}
	c.blocks = append(c.blocks, b)
	c.native = append(c.native, nat)
	c.fung = append(c.fung, fung)
	c.nft = append(c.nft, nft)
	c.multi = append(c.multi, multi)
	c.supply = append(c.supply, sup)
}

func bigOr0(v *big.Int) *big.Int {
	if v == nil {
		return new(big.Int)
	}
	return v
}

func (c *simChain) parentOf(n uint64) string {
	if n == 0 {
		return "0x" + strings.Repeat("0", 64)
	}
	return c.blocks[n-1].hash
}

// nodeJSON renders block n the way eth_getBlockByNumber(n, true),
// eth_getBlockReceipts(n) and debug_traceBlockByNumber(n) would.
func (c *simChain) nodeJSON(n uint64) (json.RawMessage, []json.RawMessage, []json.RawMessage) {
	b := c.blocks[n]
	var txs []map[string]any
	var receipts, traces []json.RawMessage
	logIndex := 0
	for i, tx := range b.txs {
		t := map[string]any{
			"hash": tx.hash, "blockHash": b.hash, "blockNumber": hex64(int64(n)), "transactionIndex": hex64(int64(i)),
			"from": tx.from, "value": hex64(tx.value), "nonce": hex64(tx.nonce), "type": "0x2", "gas": "0x7a120",
			"maxFeePerGas": "0x3b9aca00", "maxPriorityFeePerGas": "0x1", "input": orHex(tx.input), "chainId": "0x7a69",
		}
		if tx.to != "" {
			t["to"] = tx.to
		} else {
			t["to"] = nil
		}
		txs = append(txs, t)
		status := "0x1"
		if tx.failed {
			status = "0x0"
		}
		var logs []map[string]any
		if !tx.failed {
			for _, lg := range tx.logs {
				logs = append(logs, map[string]any{
					"address": lg.addr, "topics": lg.topics, "data": "0x" + lg.data, "logIndex": hex64(int64(logIndex)),
					"transactionIndex": hex64(int64(i)), "transactionHash": tx.hash, "blockHash": b.hash, "blockNumber": hex64(int64(n)), "removed": false,
				})
				logIndex++
			}
		}
		if logs == nil {
			logs = []map[string]any{}
		}
		rc := map[string]any{
			"transactionHash": tx.hash, "transactionIndex": hex64(int64(i)), "blockHash": b.hash, "blockNumber": hex64(int64(n)),
			"from": tx.from, "status": status, "gasUsed": "0x5208", "cumulativeGasUsed": hex64(int64(21000 * (i + 1))),
			"effectiveGasPrice": "0x3b9aca00", "logs": logs, "contractAddress": nil,
		}
		if tx.to != "" {
			rc["to"] = tx.to
		}
		if tx.created != "" {
			rc["contractAddress"] = tx.created
		}
		rj, _ := json.Marshal(rc)
		receipts = append(receipts, rj)
		tr := tx.trace
		if tr == nil {
			typ, to := "CALL", tx.to
			if tx.to == "" {
				typ, to = "CREATE", tx.created
			}
			tr = map[string]any{"type": typ, "from": tx.from, "to": to, "value": hex64(tx.value), "gas": "0x7a120", "gasUsed": "0x5208", "input": orHex(tx.input)}
			if tx.failed {
				tr["error"] = "execution reverted"
				tr["revertReason"] = "nope"
			}
		}
		tj, _ := json.Marshal(tr)
		traces = append(traces, tj)
	}
	if txs == nil {
		txs = []map[string]any{}
	}
	var ws []map[string]any
	for i, w := range b.withdrawals {
		ws = append(ws, map[string]any{"index": hex64(int64(i)), "validatorIndex": "0x0", "address": w, "amount": "0x1"})
	}
	if ws == nil {
		ws = []map[string]any{}
	}
	blk := map[string]any{
		"number": hex64(int64(n)), "hash": b.hash, "parentHash": c.parentOf(n), "miner": b.miner, "timestamp": hex64(1_700_000_000 + int64(n)*12),
		"gasUsed": "0x5208", "gasLimit": "0x1c9c380", "baseFeePerGas": "0x7", "size": "0x220", "stateRoot": hash('5'),
		"transactionsRoot": hash('6'), "receiptsRoot": hash('7'), "logsBloom": "0x00", "extraData": "0x", "nonce": "0x0000000000000000",
		"mixHash": hash('8'), "difficulty": "0x0", "transactions": txs, "withdrawals": ws, "uncles": []any{},
	}
	bj, _ := json.Marshal(blk)
	return bj, receipts, traces
}

// stateAt answers the reads the fetcher would make at block n.
func (c *simChain) tokenStateAt(n int64, rs *tokenReadSet) *tokenState {
	ts := newTokenState()
	for t := range rs.Tokens {
		m := &tokenMeta{Name: ptrS("Token " + t[2:6]), Symbol: ptrS("T" + t[2:4])}
		if s := c.supply[n][t]; s != nil {
			m.TotalSupply = new(big.Int).Set(s)
			d := 18
			m.Decimals = &d
		}
		ts.Meta[t] = m
	}
	for k := range rs.Fungible {
		ts.Fungible[k] = new(big.Int).Set(bigOr0(c.fung[n][k]))
	}
	for k := range rs.NFT {
		ts.NFTOwner[k] = c.nft[n][k]
	}
	for k := range rs.Multi {
		ts.Multi[k] = new(big.Int).Set(bigOr0(c.multi[n][k]))
	}
	return ts
}

func ptrS(s string) *string { return &s }

func (c *simChain) readTokens(_ context.Context, block int64, rs *tokenReadSet) (*tokenState, error) {
	return c.tokenStateAt(block, rs), nil
}

// data builds what fetch(n) would return.
func (c *simChain) data(t testing.TB, n uint64) *blockData {
	t.Helper()
	b, r, tr := c.nodeJSON(n)
	pb, err := parseBlock(b, r, tr)
	if err != nil {
		t.Fatalf("parse block %d: %v", n, err)
	}
	d := &blockData{parsedBlock: pb, Balances: map[string]*big.Int{}, Code: map[string]string{}}
	for a := range pb.Seen {
		d.Balances[a] = new(big.Int).Set(bigOr0(c.native[n][a]))
		if code, ok := c.code[a]; ok {
			d.Code[a] = code
		}
	}
	rs := newTokenReadSet()
	for i := range pb.Transfers {
		rs.addTransfer(&pb.Transfers[i])
	}
	d.Tokens = c.tokenStateAt(int64(n), rs)
	return d
}

// transferLog is an ERC-20 Transfer.
func transferLog(token, from, to string, amount int64) simLog {
	return simLog{addr: token, topics: []string{evm.TopicTransfer, topicAddr(from), topicAddr(to)}, data: word(amount)}
}

func nftLog(token, from, to string, id int64) simLog {
	return simLog{addr: token, topics: []string{evm.TopicTransfer, topicAddr(from), topicAddr(to), topicNum(id)}}
}

func singleLog(token, op, from, to string, id, value int64) simLog {
	return simLog{addr: token, topics: []string{evm.TopicTransferSingle, topicAddr(op), topicAddr(from), topicAddr(to)}, data: word(id) + word(value)}
}

package indexer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sync"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/evm"
)

// errBlockMissing means the node has no block at that height (its head went
// backwards, or it is behind the number we were told about).
var errBlockMissing = errors.New("block not on node")

// errTracesUnavailable is a TRACES=required block whose traces the node
// would not give (yet). The block is retried, never committed without them.
var errTracesUnavailable = errors.New("traces unavailable")

type tokenMeta struct {
	Name        *string
	Symbol      *string
	Decimals    *int
	TotalSupply *big.Int
	ERC3643     bool
}

type fungKey struct{ Token, Holder string }
type nftKey struct{ Token, ID string }
type multiKey struct{ Token, Holder, ID string }

// tokenState is what the chain said about tokens at one block. A key absent
// from a map means "could not be read" (the call reverted): the stored row is
// then left alone rather than guessed at.
type tokenState struct {
	Meta     map[string]*tokenMeta
	Fungible map[fungKey]*big.Int
	NFTOwner map[nftKey]string // "" = no owner (burned or never minted)
	Multi    map[multiKey]*big.Int
}

func newTokenState() *tokenState {
	return &tokenState{
		Meta:     map[string]*tokenMeta{},
		Fungible: map[fungKey]*big.Int{},
		NFTOwner: map[nftKey]string{},
		Multi:    map[multiKey]*big.Int{},
	}
}

// tokenReadSet lists what to read.
type tokenReadSet struct {
	Tokens   map[string]bool
	Fungible map[fungKey]bool
	NFT      map[nftKey]bool
	Multi    map[multiKey]bool
}

func newTokenReadSet() *tokenReadSet {
	return &tokenReadSet{Tokens: map[string]bool{}, Fungible: map[fungKey]bool{}, NFT: map[nftKey]bool{}, Multi: map[multiKey]bool{}}
}

func (s *tokenReadSet) addTransfer(t *transferRow) {
	s.Tokens[t.Token] = true
	switch t.TokenType {
	case TypeERC20:
		for _, h := range []string{t.From, t.To} {
			if h != chainrpc.ZeroAddress {
				s.Fungible[fungKey{t.Token, h}] = true
			}
		}
	case TypeERC721:
		s.NFT[nftKey{t.Token, t.TokenID.String()}] = true
	case TypeERC1155:
		for _, h := range []string{t.From, t.To} {
			if h != chainrpc.ZeroAddress {
				s.Multi[multiKey{t.Token, h, t.TokenID.String()}] = true
			}
		}
	}
}

// blockData is a fetched block ready to commit.
type blockData struct {
	*parsedBlock
	Balances map[string]*big.Int // native balance at n of every seen address
	Code     map[string]string   // runtime code at n of every address found to hold code
	Tokens   *tokenState
}

// fetcher reads blocks from the node.
type fetcher struct {
	rpc    *chainrpc.Client
	traces bool

	mu          sync.Mutex
	codeChecked map[string]bool // address -> holds code (learned from earlier fetches)
	noBlockRcpt bool            // node lacks eth_getBlockReceipts
	noBlockTrc  bool            // node lacks debug_traceBlockByNumber
	noHashTag   bool            // node refuses EIP-1898 {blockHash} tags
}

func newFetcher(rpc *chainrpc.Client, traces bool) *fetcher {
	return &fetcher{rpc: rpc, traces: traces, codeChecked: map[string]bool{}}
}

// forget drops cached knowledge (after a rollback it may describe a fork).
func (f *fetcher) forget() {
	f.mu.Lock()
	f.codeChecked = map[string]bool{}
	f.mu.Unlock()
}

func (f *fetcher) flag(p *bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *p
}

func (f *fetcher) setFlag(p *bool) {
	f.mu.Lock()
	*p = true
	f.mu.Unlock()
}

// fetch reads block n with receipts, traces and the state reads at n.
func (f *fetcher) fetch(ctx context.Context, n uint64) (*blockData, error) {
	var raw json.RawMessage
	if err := f.rpc.Call(ctx, &raw, "eth_getBlockByNumber", chainrpc.Hex64(n), true); err != nil {
		return nil, fmt.Errorf("eth_getBlockByNumber %d: %w", n, err)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errBlockMissing
	}
	var hdr struct {
		Hash         string            `json:"hash"`
		Transactions []json.RawMessage `json:"transactions"`
	}
	if err := json.Unmarshal(raw, &hdr); err != nil {
		return nil, fmt.Errorf("block %d: %w", n, err)
	}
	hashes := make([]string, len(hdr.Transactions))
	for i, t := range hdr.Transactions {
		var x struct {
			Hash string `json:"hash"`
		}
		if err := json.Unmarshal(t, &x); err != nil {
			return nil, fmt.Errorf("block %d tx %d: %w", n, i, err)
		}
		hashes[i] = chainrpc.Lower(x.Hash)
	}

	var receipts []json.RawMessage
	var traces []json.RawMessage
	if len(hashes) > 0 {
		var err error
		if receipts, err = f.receipts(ctx, n, hashes); err != nil {
			return nil, err
		}
		if f.traces {
			if traces, err = f.blockTraces(ctx, n, hashes); err != nil {
				return nil, err
			}
		}
	} else if f.traces {
		traces = []json.RawMessage{}
	}

	pb, err := parseBlock(raw, receipts, traces)
	if err != nil {
		return nil, err
	}
	d := &blockData{parsedBlock: pb}
	if err := f.stateReads(ctx, d); err != nil {
		return nil, err
	}
	return d, nil
}

func (f *fetcher) receipts(ctx context.Context, n uint64, hashes []string) ([]json.RawMessage, error) {
	if !f.flag(&f.noBlockRcpt) {
		var rs []json.RawMessage
		err := f.rpc.Call(ctx, &rs, "eth_getBlockReceipts", chainrpc.Hex64(n))
		if err == nil && len(rs) == len(hashes) {
			return rs, nil
		}
		if err != nil && !chainrpc.IsMethodNotFound(err) {
			return nil, fmt.Errorf("eth_getBlockReceipts %d: %w", n, err)
		}
		if err != nil {
			f.setFlag(&f.noBlockRcpt)
		}
	}
	elems := make([]chainrpc.BatchElem, len(hashes))
	out := make([]json.RawMessage, len(hashes))
	for i, h := range hashes {
		elems[i] = chainrpc.BatchElem{Method: "eth_getTransactionReceipt", Params: []any{h}, Result: &out[i]}
	}
	if err := f.rpc.BatchCall(ctx, elems); err != nil {
		return nil, err
	}
	for i := range elems {
		if elems[i].Error != nil {
			return nil, fmt.Errorf("receipt %s: %w", hashes[i], elems[i].Error)
		}
		if len(out[i]) == 0 || string(out[i]) == "null" {
			return nil, fmt.Errorf("%w: no receipt for %s", errInconsistent, hashes[i])
		}
	}
	return out, nil
}

// blockTraces returns one callTracer result per transaction, in order.
func (f *fetcher) blockTraces(ctx context.Context, n uint64, hashes []string) ([]json.RawMessage, error) {
	if !f.flag(&f.noBlockTrc) {
		var res []chainrpc.TraceResult
		err := f.rpc.Call(ctx, &res, "debug_traceBlockByNumber", chainrpc.Hex64(n), chainrpc.CallTracer)
		switch {
		case err == nil:
			if len(res) != len(hashes) {
				return nil, fmt.Errorf("%w: block %d: %d traces for %d transactions", errInconsistent, n, len(res), len(hashes))
			}
			out := make([]json.RawMessage, len(res))
			for i, r := range res {
				if r.TxHash != "" && chainrpc.Lower(r.TxHash) != hashes[i] {
					return nil, fmt.Errorf("%w: block %d trace %d is for %s", errInconsistent, n, i, r.TxHash)
				}
				if r.Error != "" || len(r.Result) == 0 || string(r.Result) == "null" {
					return nil, fmt.Errorf("%w: block %d tx %s: %s", errTracesUnavailable, n, hashes[i], r.Error)
				}
				out[i] = r.Result
			}
			return out, nil
		case chainrpc.IsMethodNotFound(err):
			f.setFlag(&f.noBlockTrc)
		default:
			return nil, fmt.Errorf("%w: debug_traceBlockByNumber %d: %v", errTracesUnavailable, n, err)
		}
	}
	elems := make([]chainrpc.BatchElem, len(hashes))
	out := make([]json.RawMessage, len(hashes))
	for i, h := range hashes {
		elems[i] = chainrpc.BatchElem{Method: "debug_traceTransaction", Params: []any{h, chainrpc.CallTracer}, Result: &out[i]}
	}
	if err := f.rpc.BatchCall(ctx, elems); err != nil {
		return nil, err
	}
	for i := range elems {
		if elems[i].Error != nil {
			return nil, fmt.Errorf("%w: debug_traceTransaction %s: %v", errTracesUnavailable, hashes[i], elems[i].Error)
		}
		if len(out[i]) == 0 || string(out[i]) == "null" {
			return nil, fmt.Errorf("%w: empty trace for %s", errTracesUnavailable, hashes[i])
		}
	}
	return out, nil
}

// blockTag is the state reference for reads at block d: the block hash
// (EIP-1898) when the node takes it, so a reorg between fetching the block
// and reading its state cannot mix two forks; else the number.
func (f *fetcher) blockTag(number uint64, hash string) any {
	if hash != "" && !f.flag(&f.noHashTag) {
		return map[string]any{"blockHash": hash}
	}
	return chainrpc.Hex64(number)
}

// stateReads fills balances, code and token state at block n.
func (f *fetcher) stateReads(ctx context.Context, d *blockData) error {
	addrs := sortedKeys(d.Seen)

	created := map[string]bool{}
	for _, c := range d.Creations {
		created[c.Address] = true
	}
	var codeAddrs []string
	f.mu.Lock()
	for _, a := range addrs {
		if _, known := f.codeChecked[a]; !known || created[a] {
			codeAddrs = append(codeAddrs, a)
		}
	}
	f.mu.Unlock()

	rs := newTokenReadSet()
	for i := range d.Transfers {
		rs.addTransfer(&d.Transfers[i])
	}

	for attempt := 0; ; attempt++ {
		tag := f.blockTag(d.Number, d.Hash)
		bal, code, ts, err := f.read(ctx, tag, addrs, codeAddrs, rs)
		if err != nil && attempt == 0 && isBadParams(err) && !f.flag(&f.noHashTag) {
			f.setFlag(&f.noHashTag) // node does not take {blockHash}; use numbers
			continue
		}
		if err != nil {
			return err
		}
		d.Balances, d.Code, d.Tokens = bal, code, ts
		break
	}

	f.mu.Lock()
	for _, a := range codeAddrs {
		_, has := d.Code[a]
		f.codeChecked[a] = has
	}
	for a := range d.Code {
		f.codeChecked[a] = true
	}
	f.mu.Unlock()
	return nil
}

func isBadParams(err error) bool {
	var re *chainrpc.Error
	return errors.As(err, &re) && (re.Code == -32602 || re.Code == -32600)
}

// read runs every state read of one block as JSON-RPC batches.
func (f *fetcher) read(ctx context.Context, tag any, balAddrs, codeAddrs []string, rs *tokenReadSet) (map[string]*big.Int, map[string]string, *tokenState, error) {
	type pending struct {
		apply func(res json.RawMessage, err error) error
	}
	var elems []chainrpc.BatchElem
	var handlers []pending
	results := []*json.RawMessage{}
	add := func(method string, params []any, h func(json.RawMessage, error) error) {
		r := new(json.RawMessage)
		results = append(results, r)
		elems = append(elems, chainrpc.BatchElem{Method: method, Params: params, Result: r})
		handlers = append(handlers, pending{apply: h})
	}

	balances := map[string]*big.Int{}
	for _, a := range balAddrs {
		a := a
		add("eth_getBalance", []any{a, tag}, func(res json.RawMessage, err error) error {
			if err != nil {
				return fmt.Errorf("eth_getBalance %s: %w", a, err)
			}
			var s string
			if err := json.Unmarshal(res, &s); err != nil {
				return fmt.Errorf("eth_getBalance %s: %w", a, err)
			}
			v, err := chainrpc.ParseBig(s)
			if err != nil {
				return err
			}
			balances[a] = v
			return nil
		})
	}
	code := map[string]string{}
	for _, a := range codeAddrs {
		a := a
		add("eth_getCode", []any{a, tag}, func(res json.RawMessage, err error) error {
			if err != nil {
				return fmt.Errorf("eth_getCode %s: %w", a, err)
			}
			var s string
			if err := json.Unmarshal(res, &s); err != nil {
				return fmt.Errorf("eth_getCode %s: %w", a, err)
			}
			if s != "" && s != "0x" {
				code[a] = chainrpc.Lower(s)
			}
			return nil
		})
	}

	ts := newTokenState()
	call := func(to, data string, h func(ret []byte)) {
		add("eth_call", []any{map[string]any{"to": to, "data": data}, tag}, func(res json.RawMessage, err error) error {
			if err != nil {
				if chainrpc.IsExecutionError(err) {
					return nil // reverted: no answer, not a failure
				}
				return fmt.Errorf("eth_call %s: %w", to, err)
			}
			var s string
			if err := json.Unmarshal(res, &s); err != nil {
				return nil
			}
			h(evm.Bytes(s))
			return nil
		})
	}
	for _, t := range sortedKeys(rs.Tokens) {
		m := &tokenMeta{}
		ts.Meta[t] = m
		call(t, evm.Calldata(evm.SelName), func(ret []byte) {
			if s, ok := evm.DecodeString(ret); ok {
				m.Name = &s
			}
		})
		call(t, evm.Calldata(evm.SelSymbol), func(ret []byte) {
			if s, ok := evm.DecodeString(ret); ok {
				m.Symbol = &s
			}
		})
		call(t, evm.Calldata(evm.SelDecimals), func(ret []byte) {
			if v, ok := evm.DecodeUint(ret); ok && v.IsInt64() && v.Int64() <= 255 {
				dv := int(v.Int64())
				m.Decimals = &dv
			}
		})
		call(t, evm.Calldata(evm.SelTotalSupply), func(ret []byte) {
			if v, ok := evm.DecodeUint(ret); ok {
				m.TotalSupply = v
			}
		})
		call(t, evm.Calldata(evm.SelIdentityRegistry), func(ret []byte) {
			if a, ok := evm.DecodeAddress(ret); ok && a != chainrpc.ZeroAddress {
				m.ERC3643 = true
			}
		})
	}
	for k := range rs.Fungible {
		k := k
		call(k.Token, evm.Calldata(evm.SelBalanceOf, evm.AddressWord(k.Holder)), func(ret []byte) {
			if v, ok := evm.DecodeUint(ret); ok {
				ts.Fungible[k] = v
			}
		})
	}
	for k := range rs.NFT {
		k := k
		id, _ := new(big.Int).SetString(k.ID, 10)
		add("eth_call", []any{map[string]any{"to": k.Token, "data": evm.Calldata(evm.SelOwnerOf, evm.UintWord(id))}, tag}, func(res json.RawMessage, err error) error {
			if err != nil {
				if chainrpc.IsExecutionError(err) {
					ts.NFTOwner[k] = "" // ownerOf reverts for a burned or unminted id
					return nil
				}
				return fmt.Errorf("ownerOf %s/%s: %w", k.Token, k.ID, err)
			}
			var s string
			if json.Unmarshal(res, &s) != nil {
				return nil
			}
			if a, ok := evm.DecodeAddress(evm.Bytes(s)); ok {
				if a == chainrpc.ZeroAddress {
					a = ""
				}
				ts.NFTOwner[k] = a
			}
			return nil
		})
	}
	for k := range rs.Multi {
		k := k
		id, _ := new(big.Int).SetString(k.ID, 10)
		call(k.Token, evm.Calldata(evm.SelBalanceOf1155, evm.AddressWord(k.Holder), evm.UintWord(id)), func(ret []byte) {
			if v, ok := evm.DecodeUint(ret); ok {
				ts.Multi[k] = v
			}
		})
	}

	if len(elems) == 0 {
		return balances, code, ts, nil
	}
	if err := f.rpc.BatchCall(ctx, elems); err != nil {
		return nil, nil, nil, err
	}
	for i := range elems {
		if err := handlers[i].apply(*results[i], elems[i].Error); err != nil {
			if elems[i].Error != nil && isBadParams(elems[i].Error) {
				return nil, nil, nil, elems[i].Error
			}
			return nil, nil, nil, err
		}
	}
	return balances, code, ts, nil
}

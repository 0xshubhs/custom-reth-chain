package api

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/jackc/pgx/v5/pgxpool"

	"custom-reth-chain/explorer/backend/internal/evm"
	"custom-reth-chain/explorer/backend/internal/sigs"
)

// builtinABIJSON is the table of common token events and methods used when
// no verified ABI knows a selector or topic. ERC-20 and ERC-721 share the
// Transfer and Approval topics and differ only in which argument is indexed,
// so they live in separate ABIs and the topic count picks one.
const builtinERC20JSON = `[
 {"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256","indexed":false}]},
 {"type":"event","name":"Approval","inputs":[{"name":"owner","type":"address","indexed":true},{"name":"spender","type":"address","indexed":true},{"name":"value","type":"uint256","indexed":false}]},
 {"type":"function","name":"transfer","inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]},
 {"type":"function","name":"transferFrom","inputs":[{"name":"from","type":"address"},{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]},
 {"type":"function","name":"approve","inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]},
 {"type":"function","name":"mint","inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[]},
 {"type":"function","name":"burn","inputs":[{"name":"amount","type":"uint256"}],"outputs":[]},
 {"type":"function","name":"burn","inputs":[{"name":"account","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[]},
 {"type":"function","name":"burnFrom","inputs":[{"name":"account","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[]},
 {"type":"function","name":"safeTransferFrom","inputs":[{"name":"from","type":"address"},{"name":"to","type":"address"},{"name":"tokenId","type":"uint256"}],"outputs":[]},
 {"type":"function","name":"safeTransferFrom","inputs":[{"name":"from","type":"address"},{"name":"to","type":"address"},{"name":"tokenId","type":"uint256"},{"name":"data","type":"bytes"}],"outputs":[]},
 {"type":"function","name":"safeTransferFrom","inputs":[{"name":"from","type":"address"},{"name":"to","type":"address"},{"name":"id","type":"uint256"},{"name":"value","type":"uint256"},{"name":"data","type":"bytes"}],"outputs":[]},
 {"type":"function","name":"safeBatchTransferFrom","inputs":[{"name":"from","type":"address"},{"name":"to","type":"address"},{"name":"ids","type":"uint256[]"},{"name":"values","type":"uint256[]"},{"name":"data","type":"bytes"}],"outputs":[]},
 {"type":"function","name":"setApprovalForAll","inputs":[{"name":"operator","type":"address"},{"name":"approved","type":"bool"}],"outputs":[]},
 {"type":"event","name":"ApprovalForAll","inputs":[{"name":"owner","type":"address","indexed":true},{"name":"operator","type":"address","indexed":true},{"name":"approved","type":"bool","indexed":false}]},
 {"type":"event","name":"TransferSingle","inputs":[{"name":"operator","type":"address","indexed":true},{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"id","type":"uint256","indexed":false},{"name":"value","type":"uint256","indexed":false}]},
 {"type":"event","name":"TransferBatch","inputs":[{"name":"operator","type":"address","indexed":true},{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"ids","type":"uint256[]","indexed":false},{"name":"values","type":"uint256[]","indexed":false}]}
]`

const builtinERC721JSON = `[
 {"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"tokenId","type":"uint256","indexed":true}]},
 {"type":"event","name":"Approval","inputs":[{"name":"owner","type":"address","indexed":true},{"name":"approved","type":"address","indexed":true},{"name":"tokenId","type":"uint256","indexed":true}]}
]`

var (
	builtinERC20  = mustABI(builtinERC20JSON)
	builtinERC721 = mustABI(builtinERC721JSON)
)

func mustABI(s string) *abi.ABI {
	a, err := abi.JSON(strings.NewReader(s))
	if err != nil {
		panic(err)
	}
	return &a
}

// Decoder resolves ABIs and decodes call data and logs.
type Decoder struct {
	pool *pgxpool.Pool

	mu      sync.Mutex
	byAddr  map[string]abiEntry
	global  *abiIndex
	checked time.Time
	version string
}

type abiEntry struct {
	abi  *abi.ABI // nil: not verified (or unparseable)
	raw  json.RawMessage
	name string
	at   time.Time
}

// abiIndex is every selector and event topic of every verified ABI, for a
// method name when the called contract is unverified but its code (or an
// implementation it delegates to) is.
type abiIndex struct {
	methods map[[4]byte]abi.Method
	events  map[common.Hash][]abi.Event
}

// NewDecoder returns a decoder reading verified ABIs from pool.
func NewDecoder(pool *pgxpool.Pool) *Decoder {
	return &Decoder{pool: pool, byAddr: map[string]abiEntry{}, global: &abiIndex{methods: map[[4]byte]abi.Method{}, events: map[common.Hash][]abi.Event{}}}
}

const abiTTL = time.Minute

// Invalidate forgets cached ABIs (after a verification).
func (d *Decoder) Invalidate() {
	d.mu.Lock()
	d.byAddr = map[string]abiEntry{}
	d.checked = time.Time{}
	d.version = ""
	d.mu.Unlock()
}

// ABIFor returns the verified ABI of addr (nil when not verified).
func (d *Decoder) ABIFor(ctx context.Context, addr string) (*abi.ABI, json.RawMessage, error) {
	if d.pool == nil || addr == "" {
		return nil, nil, nil
	}
	d.mu.Lock()
	e, ok := d.byAddr[addr]
	d.mu.Unlock()
	if ok && time.Since(e.at) < abiTTL {
		return e.abi, e.raw, nil
	}
	var raw []byte
	var name string
	err := d.pool.QueryRow(ctx, `SELECT abi, name FROM verified_contracts WHERE address = $1`, addr).Scan(&raw, &name)
	e = abiEntry{at: time.Now()}
	if err == nil {
		e.raw = raw
		e.name = name
		if a, perr := abi.JSON(bytes.NewReader(raw)); perr == nil {
			e.abi = &a
		}
	} else if !isNoRows(err) {
		return nil, nil, err
	}
	d.mu.Lock()
	if len(d.byAddr) > 5000 {
		d.byAddr = map[string]abiEntry{}
	}
	d.byAddr[addr] = e
	d.mu.Unlock()
	return e.abi, e.raw, nil
}

// index returns the global selector index, rebuilt when verified_contracts
// changed (checked at most every 30 s).
func (d *Decoder) index(ctx context.Context) *abiIndex {
	if d.pool == nil {
		return d.global
	}
	d.mu.Lock()
	if time.Since(d.checked) < 30*time.Second {
		g := d.global
		d.mu.Unlock()
		return g
	}
	d.checked = time.Now()
	d.mu.Unlock()

	var cnt int64
	var latest *time.Time
	if err := d.pool.QueryRow(ctx, `SELECT count(*), max(verified_at) FROM verified_contracts WHERE match_type <> 'similar'`).Scan(&cnt, &latest); err != nil {
		return d.global
	}
	ver := fmt.Sprintf("%d/%v", cnt, latest)
	d.mu.Lock()
	same := ver == d.version
	d.mu.Unlock()
	if same {
		return d.global
	}
	idx := &abiIndex{methods: map[[4]byte]abi.Method{}, events: map[common.Hash][]abi.Event{}}
	rows, err := d.pool.Query(ctx, `SELECT abi FROM verified_contracts WHERE match_type <> 'similar' ORDER BY verified_at`)
	if err != nil {
		return d.global
	}
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) != nil {
			continue
		}
		a, err := abi.JSON(bytes.NewReader(raw))
		if err != nil {
			continue
		}
		for _, m := range a.Methods {
			var sel [4]byte
			copy(sel[:], m.ID)
			if _, dup := idx.methods[sel]; !dup {
				idx.methods[sel] = m
			}
		}
		for _, e := range a.Events {
			idx.events[e.ID] = append(idx.events[e.ID], e)
		}
	}
	rows.Close()
	d.mu.Lock()
	d.global, d.version = idx, ver
	d.mu.Unlock()
	return idx
}

// MethodName resolves a 4-byte selector against abis, then the global
// index, then the built-in table; the selector hex when nobody knows it.
func (d *Decoder) MethodName(ctx context.Context, abis []*abi.ABI, methodID string) string {
	sel, ok := selectorOf(methodID)
	if !ok {
		return methodID
	}
	if m := d.findMethod(ctx, abis, sel); m != nil {
		return m.RawName
	}
	return methodID
}

func (d *Decoder) findMethod(ctx context.Context, abis []*abi.ABI, sel [4]byte) *abi.Method {
	for _, a := range abis {
		if a == nil {
			continue
		}
		if m, err := a.MethodById(sel[:]); err == nil {
			return m
		}
	}
	if m, ok := d.index(ctx).methods[sel]; ok {
		return &m
	}
	if m, err := builtinERC20.MethodById(sel[:]); err == nil {
		return m
	}
	return bundledMethod(sel)
}

// bundledMethods caches methods built from the bundled signature list
// (selector -> *abi.Method, nil when unknown). Their parameters are unnamed.
var bundledMethods sync.Map

func bundledMethod(sel [4]byte) *abi.Method {
	if v, ok := bundledMethods.Load(sel); ok {
		m, _ := v.(*abi.Method)
		return m
	}
	var m *abi.Method
	if list := sigs.Bundled().Lookup(sigs.Function, "0x"+hex.EncodeToString(sel[:])); len(list) > 0 {
		m, _ = sigs.MethodFromSignature(list[0])
	}
	bundledMethods.Store(sel, m)
	return m
}

func selectorOf(methodID string) ([4]byte, bool) {
	var sel [4]byte
	b := evm.Bytes(methodID)
	if len(b) < 4 {
		return sel, false
	}
	copy(sel[:], b[:4])
	return sel, true
}

// DecodeInput decodes call data. nil when no known ABI matches or the
// arguments do not unpack.
func (d *Decoder) DecodeInput(ctx context.Context, abis []*abi.ABI, input string) *DecodedInput {
	data := evm.Bytes(input)
	if len(data) < 4 {
		return nil
	}
	var sel [4]byte
	copy(sel[:], data[:4])
	m := d.findMethod(ctx, abis, sel)
	if m == nil {
		return nil
	}
	vals, err := m.Inputs.Unpack(data[4:])
	if err != nil || len(vals) != len(m.Inputs) {
		return nil
	}
	params := make([]DecodedParam, len(m.Inputs))
	for i, in := range m.Inputs {
		params[i] = DecodedParam{Name: in.Name, Type: in.Type.String(), Value: jsonValue(in.Type, vals[i])}
	}
	return &DecodedInput{Method: m.RawName, Signature: m.Sig, Params: params}
}

// DecodeLog decodes a log with the emitter's ABIs, then the global index,
// then the built-in token events.
func (d *Decoder) DecodeLog(ctx context.Context, abis []*abi.ABI, topics []*string, data string) *DecodedLog {
	if len(topics) == 0 || topics[0] == nil {
		return nil
	}
	t0 := common.HexToHash(*topics[0])
	var cands []abi.Event
	for _, a := range abis {
		if a == nil {
			continue
		}
		for _, e := range a.Events {
			if e.ID == t0 && !e.Anonymous {
				cands = append(cands, e)
			}
		}
	}
	cands = append(cands, d.index(ctx).events[t0]...)
	for _, b := range []*abi.ABI{builtinERC20, builtinERC721} {
		for _, e := range b.Events {
			if e.ID == t0 {
				cands = append(cands, e)
			}
		}
	}
	raw := evm.Bytes(data)
	for _, e := range cands {
		if out := decodeEvent(e, topics, raw); out != nil {
			return out
		}
	}
	return nil
}

func decodeEvent(e abi.Event, topics []*string, data []byte) *DecodedLog {
	indexed := 0
	for _, in := range e.Inputs {
		if in.Indexed {
			indexed++
		}
	}
	if indexed != len(topics)-1 {
		return nil
	}
	nonIdx, err := e.Inputs.NonIndexed().Unpack(data)
	if err != nil {
		return nil
	}
	params := make([]DecodedParam, 0, len(e.Inputs))
	ti, ni := 1, 0
	for _, in := range e.Inputs {
		p := DecodedParam{Name: in.Name, Type: in.Type.String()}
		if in.Indexed {
			yes := true
			p.Indexed = &yes
			if topics[ti] == nil {
				return nil
			}
			word := evm.Bytes(*topics[ti])
			ti++
			if len(word) != 32 {
				return nil
			}
			if isStatic(in.Type) {
				vals, err := abi.Arguments{{Type: in.Type}}.Unpack(word)
				if err != nil || len(vals) != 1 {
					return nil
				}
				p.Value = jsonValue(in.Type, vals[0])
			} else {
				p.Value = "0x" + hex.EncodeToString(word) // a dynamic value is indexed as its hash
			}
		} else {
			no := false
			p.Indexed = &no
			if ni >= len(nonIdx) {
				return nil
			}
			p.Value = jsonValue(in.Type, nonIdx[ni])
			ni++
		}
		params = append(params, p)
	}
	return &DecodedLog{Event: e.RawName, Signature: e.Sig, Params: params}
}

func isStatic(t abi.Type) bool {
	switch t.T {
	case abi.AddressTy, abi.IntTy, abi.UintTy, abi.BoolTy, abi.FixedBytesTy, abi.FunctionTy:
		return true
	}
	return false
}

// jsonValue renders an unpacked ABI value for JSON: addresses EIP-55,
// integers as decimal strings, bytes as 0x hex, tuples as objects keyed by
// component name, arrays element by element.
func jsonValue(t abi.Type, v any) any {
	rv := reflect.ValueOf(v)
	switch t.T {
	case abi.AddressTy:
		if a, ok := v.(common.Address); ok {
			return a.Hex()
		}
	case abi.IntTy, abi.UintTy:
		switch x := v.(type) {
		case *big.Int:
			return x.String()
		default:
			return fmt.Sprint(v)
		}
	case abi.BoolTy:
		return v
	case abi.StringTy:
		if s, ok := v.(string); ok {
			return strings.ToValidUTF8(s, "�")
		}
	case abi.BytesTy:
		if b, ok := v.([]byte); ok {
			return "0x" + hex.EncodeToString(b)
		}
	case abi.FixedBytesTy, abi.FunctionTy:
		if rv.Kind() == reflect.Array {
			b := make([]byte, rv.Len())
			for i := range b {
				b[i] = byte(rv.Index(i).Uint())
			}
			return "0x" + hex.EncodeToString(b)
		}
	case abi.SliceTy, abi.ArrayTy:
		if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
			out := make([]any, rv.Len())
			for i := range out {
				out[i] = jsonValue(*t.Elem, rv.Index(i).Interface())
			}
			return out
		}
	case abi.TupleTy:
		if rv.Kind() == reflect.Struct {
			out := make(map[string]any, len(t.TupleElems))
			for i, et := range t.TupleElems {
				name := fmt.Sprint(i)
				if i < len(t.TupleRawNames) && t.TupleRawNames[i] != "" {
					name = t.TupleRawNames[i]
				}
				if i < rv.NumField() {
					out[name] = jsonValue(*et, rv.Field(i).Interface())
				}
			}
			return out
		}
	}
	return fmt.Sprint(v)
}

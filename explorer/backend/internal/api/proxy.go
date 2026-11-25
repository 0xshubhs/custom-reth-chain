package api

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/evm"
)

// Proxy kinds as the API names them.
const (
	ProxyEIP1967       = "eip1967"
	ProxyEIP1967Beacon = "eip1967-beacon"
	ProxyTREX          = "trex"
)

type proxyInfo struct {
	Impl string // "" when unknown
	Type string // "" when not a proxy
	at   time.Time
}

type proxyCache struct {
	mu sync.Mutex
	m  map[string]proxyInfo
}

func newProxyCache() *proxyCache { return &proxyCache{m: map[string]proxyInfo{}} }

const proxyTTL = 2 * time.Minute

func (c *proxyCache) peek(addr string) (proxyInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.m[addr]
	if !ok || time.Since(p.at) > proxyTTL {
		return proxyInfo{}, false
	}
	return p, true
}

func (c *proxyCache) put(addr string, p proxyInfo) {
	c.mu.Lock()
	if len(c.m) > 10000 {
		c.m = map[string]proxyInfo{}
	}
	p.at = time.Now()
	c.m[addr] = p
	c.mu.Unlock()
}

// T-REX implementation authorities hand out one implementation per proxy
// kind. Which getter applies is found by what the proxy itself answers,
// through its own fallback: only a token has identityRegistry(), only an
// identity registry has identityStorage(), and so on.
var trexKinds = []struct {
	probe  [4]byte
	getter [4]byte
}{
	{evm.SelIdentityRegistry, evm.Selector("getTokenImplementation()")},
	{evm.Selector("identityStorage()"), evm.Selector("getIRImplementation()")},
	{evm.Selector("linkedIdentityRegistries()"), evm.Selector("getIRSImplementation()")},
	{evm.Selector("getTokenBound()"), evm.Selector("getMCImplementation()")},
	{evm.Selector("getClaimTopics()"), evm.Selector("getCTRImplementation()")},
	{evm.Selector("getTrustedIssuers()"), evm.Selector("getTIRImplementation()")},
}

// detectProxy reads the proxy slots of addr at latest: EIP-1967
// implementation, EIP-1967 beacon, then the T-REX authority slot.
func (s *Server) detectProxy(ctx context.Context, addr string, isToken bool) (proxyInfo, error) {
	if p, ok := s.proxies.peek(addr); ok {
		return p, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var impl, beacon, auth string
	elems := []chainrpc.BatchElem{
		{Method: "eth_getStorageAt", Params: []any{addr, evm.SlotEIP1967Impl, "latest"}, Result: &impl},
		{Method: "eth_getStorageAt", Params: []any{addr, evm.SlotEIP1967Beacon, "latest"}, Result: &beacon},
		{Method: "eth_getStorageAt", Params: []any{addr, evm.SlotTREXAuthority, "latest"}, Result: &auth},
	}
	if err := s.rpc.BatchCall(cctx, elems); err != nil {
		return proxyInfo{}, err
	}
	var p proxyInfo
	switch {
	case slotAddr(impl) != "":
		p = proxyInfo{Impl: slotAddr(impl), Type: ProxyEIP1967}
	case slotAddr(beacon) != "":
		p = proxyInfo{Type: ProxyEIP1967Beacon}
		if ret, err := s.ethCall(cctx, slotAddr(beacon), evm.Calldata(evm.SelImplementation)); err == nil {
			if a, ok := evm.DecodeAddress(ret); ok && a != chainrpc.ZeroAddress {
				p.Impl = a
			}
		}
	case slotAddr(auth) != "":
		p = proxyInfo{Type: ProxyTREX}
		authority := slotAddr(auth)
		for i, k := range trexKinds {
			// A known token skips its probe; anything else must answer the
			// probe (through the proxy's fallback) to be that kind.
			if !(i == 0 && isToken) {
				if ret, err := s.ethCall(cctx, addr, evm.Calldata(k.probe)); err != nil || len(ret) < 32 {
					continue
				}
			}
			if ret, err := s.ethCall(cctx, authority, evm.Calldata(k.getter)); err == nil {
				if a, ok := evm.DecodeAddress(ret); ok && a != chainrpc.ZeroAddress {
					p.Impl = a
				}
			}
			break
		}
	}
	s.proxies.put(addr, p)
	return p, nil
}

func slotAddr(word string) string {
	b := evm.Bytes(word)
	if len(b) != 32 {
		return ""
	}
	a, ok := evm.WordAddress(b)
	if !ok || a == chainrpc.ZeroAddress {
		return ""
	}
	return a
}

// ethCall runs eth_call at latest and returns the return data.
func (s *Server) ethCall(ctx context.Context, to, data string) ([]byte, error) {
	var res string
	if err := s.rpc.Call(ctx, &res, "eth_call", map[string]any{"to": to, "data": data}, "latest"); err != nil {
		return nil, err
	}
	return evm.Bytes(res), nil
}

// abisFor is the ABI chain used to decode calls to and logs of addr: its own
// verified ABI, then its implementation's when it is a proxy. With node set,
// proxy detection may ask the node; without, only a cached answer is used.
func (s *Server) abisFor(ctx context.Context, addr string, node bool) []*abiWithRaw {
	var out []*abiWithRaw
	if a, raw, err := s.dec.ABIFor(ctx, addr); err == nil && a != nil {
		out = append(out, &abiWithRaw{a, raw})
	}
	var p proxyInfo
	var ok bool
	if node {
		var err error
		p, err = s.detectProxy(ctx, addr, false)
		ok = err == nil
	} else {
		p, ok = s.proxies.peek(addr)
	}
	if ok && p.Impl != "" {
		if a, raw, err := s.dec.ABIFor(ctx, p.Impl); err == nil && a != nil {
			out = append(out, &abiWithRaw{a, raw})
		}
	}
	return out
}

type abiWithRaw struct {
	abi *abi.ABI
	raw json.RawMessage
}

func plainABIs(list []*abiWithRaw) []*abi.ABI {
	out := make([]*abi.ABI, len(list))
	for i, a := range list {
		out[i] = a.abi
	}
	return out
}

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// JSON-RPC error codes the gateway answers with.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
	codeLimitExceeded  = -32005
)

// rpcRequest is one JSON-RPC call as the gateway understands it.
//
// The gateway never forwards the bytes it was sent. It forwards a request it
// re-encodes from this struct, so the node sees exactly the method and filter
// the checks below looked at. Go's decoder and the node's (serde) disagree on
// duplicate keys and on key case: {"method":"eth_chainId","method":"debug_x"}
// or "Method" vs "method" would otherwise let one parser pass a call that the
// other one runs.
type rpcRequest struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`

	hasID bool // false for a notification (no "id" member at all)
}

type outgoing struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (r *rpcRequest) encode() []byte {
	b, _ := json.Marshal(outgoing{JSONRPC: "2.0", ID: r.ID, Method: r.Method, Params: r.Params})
	return b
}

func parseRequest(raw json.RawMessage) (*rpcRequest, error) {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	// An explicit "id": null decodes to the bytes `null`, so nil here means the
	// member was absent: a notification, which gets no response.
	req.hasID = req.ID != nil
	if req.Method == "" {
		return nil, fmt.Errorf("missing method")
	}
	return &req, nil
}

// decision is what the gateway does with one call.
type decision struct {
	forward bool
	req     *rpcRequest     // the call to forward (forward == true)
	resp    json.RawMessage // the gateway's own answer (forward == false); nil for a notification
	reason  string          // why it was not forwarded, for the counters
}

// publicMethod reports whether a method may reach the node from the public
// RPC. Only the eth, net and web3 namespaces, and inside eth nothing that asks
// the node to sign: the node must never hold a key the public can use, and the
// gateway refuses those calls even if a restart ever brings dev accounts back.
func publicMethod(method string) bool {
	switch {
	case strings.HasPrefix(method, "net_"), strings.HasPrefix(method, "web3_"):
		return true
	case !strings.HasPrefix(method, "eth_"):
		// debug_*, trace_*, txpool_*, admin_*, ots_*, meow_*, clique_*, rpc_*,
		// personal_*, miner_*, reth_*, ... stay on the node's loopback listener,
		// where Blockscout uses them.
		return false
	}
	switch method {
	case "eth_sendTransaction", "eth_sign", "eth_signTransaction":
		return false
	}
	return !strings.HasPrefix(method, "eth_signTypedData")
}

// decide classifies one raw call of a request.
func (g *Gateway) decide(ctx context.Context, raw json.RawMessage) decision {
	req, err := parseRequest(raw)
	if err != nil {
		return decision{resp: errorResponse(nil, codeInvalidRequest, "invalid request"), reason: "invalid"}
	}
	answer := func(code int, msg, reason string) decision {
		d := decision{reason: reason}
		if req.hasID {
			d.resp = errorResponse(req.ID, code, msg)
		}
		return d
	}

	if req.Method == "eth_accounts" {
		// The node holds no accounts the public may use. Answer what a node
		// without unlocked accounts answers, without asking it.
		d := decision{reason: "local"}
		if req.hasID {
			d.resp = resultResponse(req.ID, json.RawMessage(`[]`))
		}
		return d
	}
	if !publicMethod(req.Method) {
		return answer(codeMethodNotFound,
			fmt.Sprintf("the method %s is not available on the public RPC", req.Method), "method")
	}

	switch req.Method {
	case "eth_getLogs", "eth_newFilter":
		params, msg := g.checkLogFilter(ctx, req.Params)
		if msg != "" {
			code := codeLimitExceeded
			if strings.HasPrefix(msg, "invalid") {
				code = codeInvalidParams
			}
			return answer(code, msg, "logs")
		}
		req.Params = params
	}
	return decision{forward: true, req: req}
}

// checkLogFilter applies the block-range cap to an eth_getLogs / eth_newFilter
// filter and returns the params to forward, re-encoded from what was checked.
// A non-empty message means the call is refused.
func (g *Gateway) checkLogFilter(ctx context.Context, rawParams json.RawMessage) (json.RawMessage, string) {
	var params []json.RawMessage
	if err := json.Unmarshal(rawParams, &params); err != nil || len(params) == 0 {
		return nil, "invalid params: expected [filter]"
	}
	var filter map[string]json.RawMessage
	if err := json.Unmarshal(params[0], &filter); err != nil || filter == nil {
		return nil, "invalid params: the filter must be an object"
	}
	canonical := func() json.RawMessage {
		f, _ := json.Marshal(filter)
		out := append([]json.RawMessage{f}, params[1:]...)
		b, _ := json.Marshal(out)
		return b
	}

	if bh, ok := filter["blockHash"]; ok && !isNull(bh) {
		return canonical(), "" // one block, whatever else it asks for
	}

	limit := g.cfg.MaxLogsRange
	if hasAddress(filter["address"]) {
		limit = g.cfg.MaxLogsRangeFiltered
	}
	if limit == 0 {
		return canonical(), ""
	}

	from, fromHead, ok := parseBlockTag(filter["fromBlock"])
	if !ok {
		return nil, "invalid params: fromBlock must be a block number or tag"
	}
	to, toHead, ok := parseBlockTag(filter["toBlock"])
	if !ok {
		return nil, "invalid params: toBlock must be a block number or tag"
	}
	if fromHead || toHead {
		head, err := g.head(ctx)
		if err != nil {
			return nil, "the RPC could not resolve the latest block, try again"
		}
		if fromHead {
			from = head
		}
		if toHead {
			to = head
		}
	}
	if to >= from && to-from+1 > limit {
		if hasAddress(filter["address"]) {
			return nil, fmt.Sprintf("query exceeds the public RPC limit: eth_getLogs may span at most %d blocks", limit)
		}
		return nil, fmt.Sprintf("query exceeds the public RPC limit: eth_getLogs without an address may span at most %d blocks; name a contract address or ask for a smaller range", limit)
	}
	return canonical(), ""
}

func isNull(raw json.RawMessage) bool {
	return len(raw) == 0 || strings.TrimSpace(string(raw)) == "null"
}

// hasAddress reports whether a filter's address member names at least one
// address (a string or a non-empty array).
func hasAddress(raw json.RawMessage) bool {
	if isNull(raw) {
		return false
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return strings.TrimSpace(one) != ""
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		for _, a := range many {
			if strings.TrimSpace(a) != "" {
				return true
			}
		}
	}
	return false
}

// parseBlockTag reads fromBlock / toBlock. Missing means "latest", as on the
// node. isHead is true for a tag that means the chain tip.
func parseBlockTag(raw json.RawMessage) (n uint64, isHead bool, ok bool) {
	if isNull(raw) {
		return 0, true, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		var num uint64
		if err := json.Unmarshal(raw, &num); err == nil {
			return num, false, true
		}
		return 0, false, false
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "latest", "pending", "safe", "finalized":
		return 0, true, true
	case "earliest":
		return 0, false, true
	}
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return 0, false, false
	}
	v, okHex := new(big.Int).SetString(s[2:], 16)
	if !okHex || v.Sign() < 0 || !v.IsUint64() {
		return 0, false, false
	}
	return v.Uint64(), false, true
}

func errorResponse(id json.RawMessage, code int, msg string) json.RawMessage {
	if len(id) == 0 {
		id = json.RawMessage(`null`)
	}
	b, _ := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{JSONRPC: "2.0", ID: id, Error: struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}{code, msg}})
	return b
}

func resultResponse(id json.RawMessage, result json.RawMessage) json.RawMessage {
	b, _ := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{"2.0", id, result})
	return b
}

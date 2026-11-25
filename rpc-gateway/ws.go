package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// serveWS relays a public websocket to the node's websocket RPC, applying the
// same checks as HTTP to every message the client sends. The node's own
// websocket listener keeps every namespace for loopback callers; the public
// only ever reaches it through here.
func (g *Gateway) serveWS(w http.ResponseWriter, r *http.Request) {
	if g.rateLimited(w, r, 1) {
		return
	}
	ip := g.clientIP(r)
	exempt := inNets(net.ParseIP(ip), g.cfg.Exempt)

	dialCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	up, _, err := g.dialer.DialContext(dialCtx, g.cfg.UpstreamWS, nil)
	cancel()
	if err != nil {
		g.count("upstream_error")
		writeRPC(w, http.StatusBadGateway, errorResponse(nil, codeInternal, "the chain node did not answer"))
		return
	}
	client, err := g.upgrader.Upgrade(w, r, nil)
	if err != nil {
		_ = up.Close()
		return // Upgrade has already written the HTTP error
	}
	client.SetReadLimit(g.cfg.MaxBodyBytes)
	up.SetReadLimit(512 << 20)

	ctx, stop := context.WithCancel(context.Background())
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			stop()
			_ = client.Close()
			_ = up.Close()
		})
	}
	defer closeBoth()

	var clientWrite sync.Mutex
	toClient := func(msg []byte) error {
		clientWrite.Lock()
		defer clientWrite.Unlock()
		_ = client.SetWriteDeadline(time.Now().Add(30 * time.Second))
		return client.WriteMessage(websocket.TextMessage, msg)
	}

	// node -> client: answers and subscription notifications, untouched.
	go func() {
		defer closeBoth()
		for {
			mt, msg, err := up.ReadMessage()
			if err != nil {
				return
			}
			if mt != websocket.TextMessage && mt != websocket.BinaryMessage {
				continue
			}
			if toClient(msg) != nil {
				return
			}
		}
	}()

	// client -> node: every message is checked first.
	for {
		mt, msg, err := client.ReadMessage()
		if err != nil {
			return
		}
		if mt != websocket.TextMessage && mt != websocket.BinaryMessage {
			continue
		}
		reply, forward := g.filterWS(ctx, msg, ip, exempt)
		if reply != nil && toClient(reply) != nil {
			return
		}
		if forward != nil {
			_ = up.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if up.WriteMessage(websocket.TextMessage, forward) != nil {
				return
			}
		}
	}
}

// filterWS decides one websocket message. It returns what to send back to the
// client directly and what to send on to the node; either may be nil.
//
// A batch is forwarded only when every call in it may be. Over a websocket the
// node's answer to a batch arrives as one message at some later point, mixed
// with subscription traffic, so the gateway cannot splice its own answers into
// it as it does over HTTP. A batch with any call the gateway answers itself is
// therefore answered entirely by the gateway.
func (g *Gateway) filterWS(ctx context.Context, msg []byte, ip string, exempt bool) (reply, forward []byte) {
	items, isBatch, err := splitBatch(msg)
	if err != nil {
		return errorResponse(nil, codeParseError, "parse error"), nil
	}
	if isBatch && len(items) == 0 {
		return errorResponse(nil, codeInvalidRequest, "empty batch"), nil
	}
	if len(items) > g.cfg.MaxBatch {
		g.count("batch_too_large")
		return errorResponse(nil, codeInvalidRequest,
			fmt.Sprintf("batch too large: at most %d calls per batch", g.cfg.MaxBatch)), nil
	}
	if g.limiter != nil && !exempt {
		if ok, wait := g.limiter.allow(ip, len(items)); !ok {
			g.count("rate_limited")
			return errorResponse(nil, codeLimitExceeded,
				fmt.Sprintf("too many requests from your address, retry in %d s", int(wait.Seconds()+0.999))), nil
		}
	}

	decisions := make([]decision, len(items))
	allForward := true
	for i, raw := range items {
		decisions[i] = g.decide(ctx, raw)
		if !decisions[i].forward {
			allForward = false
			g.count(decisions[i].reason)
		}
	}

	if !isBatch {
		d := decisions[0]
		if d.forward {
			return nil, d.req.encode()
		}
		return d.resp, nil
	}

	if allForward {
		out := make([]json.RawMessage, len(decisions))
		for i, d := range decisions {
			out[i] = d.req.encode()
		}
		b, _ := json.Marshal(out)
		return nil, b
	}
	var answers []json.RawMessage
	for _, d := range decisions {
		switch {
		case d.forward && d.req.hasID:
			answers = append(answers, errorResponse(d.req.ID, codeInvalidRequest,
				"not sent: this batch also asked for a call the public RPC does not serve; send it on its own"))
		case !d.forward && d.resp != nil:
			answers = append(answers, d.resp)
		}
	}
	if len(answers) == 0 {
		return nil, nil
	}
	b, _ := json.Marshal(answers)
	return b, nil
}

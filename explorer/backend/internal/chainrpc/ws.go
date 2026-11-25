package chainrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// HeadSubscriber keeps an eth_subscribe("newHeads") open on a websocket and
// reports every pushed head. It never gives up: a dead or closed socket is
// replaced with exponential backoff, and every successful (re)subscription is
// reported through OnSubscribed so the caller can catch up on anything pushed
// while it was not listening.
type HeadSubscriber struct {
	URL          string
	Log          *slog.Logger
	PingInterval time.Duration // default 30s
	// OnHead is called for every pushed head with its block number.
	OnHead func(number uint64)
	// OnSubscribed is called after each successful subscription, the first
	// included (true when it is a reconnect).
	OnSubscribed func(reconnect bool)

	mu        sync.Mutex
	connected bool
	reconnect int
}

// Connected reports whether a subscription is currently live.
func (s *HeadSubscriber) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connected
}

// Reconnects is how many times the subscription was re-established.
func (s *HeadSubscriber) Reconnects() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reconnect
}

// Run blocks until ctx is done.
func (s *HeadSubscriber) Run(ctx context.Context) {
	if s.PingInterval <= 0 {
		s.PingInterval = 30 * time.Second
	}
	log := s.Log
	if log == nil {
		log = slog.Default()
	}
	backoff := time.Second
	first := true
	for ctx.Err() == nil {
		started := time.Now()
		err := s.session(ctx, !first)
		first = false
		s.setConnected(false)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second // it was healthy for a while: start over
		}
		log.Warn("newHeads subscription lost, reconnecting", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (s *HeadSubscriber) setConnected(v bool) {
	s.mu.Lock()
	s.connected = v
	s.mu.Unlock()
}

type wsMessage struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
	Method string          `json:"method"`
	Params *struct {
		Subscription string          `json:"subscription"`
		Result       json.RawMessage `json:"result"`
	} `json:"params"`
}

// session runs one connection until it dies.
func (s *HeadSubscriber) session(ctx context.Context, isReconnect bool) error {
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	conn, _, err := dialer.DialContext(dctx, s.URL, nil)
	cancel()
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()
	conn.SetReadLimit(64 << 20)

	// Liveness: every frame from the node, pongs included, pushes the read
	// deadline out. A socket that answers nothing for two ping rounds is dead
	// even when TCP has not noticed.
	deadline := 2*s.PingInterval + 10*time.Second
	_ = conn.SetReadDeadline(time.Now().Add(deadline))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(deadline))
	})

	var writeMu sync.Mutex
	write := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(v)
	}
	if err := write(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "eth_subscribe", "params": []any{"newHeads"}}); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	sctx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		t := time.NewTicker(s.PingInterval)
		defer t.Stop()
		for {
			select {
			case <-sctx.Done():
				writeMu.Lock()
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
				writeMu.Unlock()
				_ = conn.Close()
				return
			case <-t.C:
				writeMu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				writeMu.Unlock()
				if err != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()

	subID := ""
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read: %w", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(deadline))
		var m wsMessage
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if subID == "" {
			if m.Error != nil {
				return fmt.Errorf("eth_subscribe: %w", m.Error)
			}
			if len(m.ID) > 0 && string(m.ID) == "1" {
				if err := json.Unmarshal(m.Result, &subID); err != nil || subID == "" {
					return errors.New("eth_subscribe: no subscription id")
				}
				s.mu.Lock()
				s.connected = true
				if isReconnect {
					s.reconnect++
				}
				s.mu.Unlock()
				if s.OnSubscribed != nil {
					s.OnSubscribed(isReconnect)
				}
			}
			continue
		}
		if m.Method != "eth_subscription" || m.Params == nil || m.Params.Subscription != subID {
			continue
		}
		var head struct {
			Number Quantity `json:"number"`
		}
		if err := json.Unmarshal(m.Params.Result, &head); err != nil || !head.Number.Valid {
			continue
		}
		if s.OnHead != nil {
			s.OnHead(head.Number.Uint64())
		}
	}
}

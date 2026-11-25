package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"custom-reth-chain/explorer/backend/internal/httpx"
)

const (
	maxStreams      = 500
	maxStreamsPerIP = 5
)

// hub fans the indexer's NOTIFY explorer_events out to SSE clients.
type hub struct {
	s       *Server
	mu      sync.Mutex
	subs    map[chan []byte]string
	perIP   map[string]int
	closing chan struct{}
	once    sync.Once
}

func newHub(s *Server) *hub {
	return &hub{s: s, subs: map[chan []byte]string{}, perIP: map[string]int{}, closing: make(chan struct{})}
}

// Close ends every open stream (called at shutdown, before the HTTP server
// waits for connections to finish).
func (s *Server) Close() { s.hub.once.Do(func() { close(s.hub.closing) }) }

func (h *hub) subscribe(ip string) (chan []byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= maxStreams || h.perIP[ip] >= maxStreamsPerIP {
		return nil, false
	}
	ch := make(chan []byte, 32)
	h.subs[ch] = ip
	h.perIP[ip]++
	return ch, true
}

func (h *hub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ip, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		if h.perIP[ip]--; h.perIP[ip] <= 0 {
			delete(h.perIP, ip)
		}
	}
}

func (h *hub) broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default: // a client that does not read loses events, not the hub
		}
	}
}

// listen keeps a LISTEN session open, reconnecting with backoff.
func (h *hub) listen(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := h.listenOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		h.s.log.Warn("event listener lost, reconnecting", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (h *hub) listenOnce(ctx context.Context) error {
	c, err := pgx.Connect(ctx, h.s.cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer c.Close(context.Background())
	if _, err := c.Exec(ctx, `LISTEN explorer_events`); err != nil {
		return err
	}
	for {
		n, err := c.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var ev struct {
			Type   string `json:"type"`
			Number int64  `json:"number"`
		}
		if json.Unmarshal([]byte(n.Payload), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "block":
			h.s.statsMu.Lock()
			h.s.statsAt = time.Time{} // stats are stale now
			h.s.statsMu.Unlock()
			h.s.charts.clear()
			if h.count() == 0 {
				continue
			}
			b, err := scanBlockSummary(h.s.pool.QueryRow(ctx, `SELECT `+blockSummaryCols+` FROM blocks b WHERE b.number = $1`, ev.Number))
			if err != nil {
				continue // rolled back already, or gone
			}
			items, err := h.s.finishBlocks(ctx, []*blockRec{b})
			if err != nil {
				continue
			}
			data, _ := json.Marshal(items[0])
			h.broadcast(data)
		case "reorg":
			h.s.statsMu.Lock()
			h.s.statsAt = time.Time{}
			h.s.statsMu.Unlock()
			h.s.charts.clear()
		}
	}
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// handleStream is GET /api/v2/stream: "event: block" with a BlockSummary
// for every newly indexed block, and a comment line every 25 s so proxies
// keep the connection open.
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	ch, ok := s.hub.subscribe(httpx.ClientIP(r, s.cfg.TrustedProxyIP))
	if !ok {
		w.Header().Del("Content-Type")
		w.Header().Set("Retry-After", "30")
		httpx.Error(w, r, http.StatusTooManyRequests, "too many open streams")
		return
	}
	defer s.hub.unsubscribe(ch)
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, "retry: 3000\n: connected\n\n"); err != nil {
		return
	}
	if rc.Flush() != nil {
		return
	}
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.hub.closing:
			return
		case msg := <-ch:
			if _, err := fmt.Fprintf(w, "event: block\ndata: %s\n\n", msg); err != nil {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		}
		if rc.Flush() != nil {
			return
		}
	}
}

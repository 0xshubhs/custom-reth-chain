package indexer

import (
	"context"
	"crypto/subtle"
	"io"
	"net/http"
	"time"

	"custom-reth-chain/explorer/backend/internal/httpx"
)

// Handler serves GET /api/health and POST /hooks/new-block.
func (ix *Indexer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", ix.handleHealth)
	mux.HandleFunc("POST /hooks/new-block", ix.handleHook)
	return httpx.WithNosniff(httpx.Recover(ix.log, httpx.MethodCompat(mux, mux)))
}

func (ix *Indexer) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := ix.pool.Ping(ctx); err != nil {
		httpx.WriteHealth(w, r, http.StatusServiceUnavailable, "degraded", map[string]any{"db": err.Error()})
		return
	}
	last, head := ix.lastBlock.Load(), ix.nodeHead.Load()
	detail := map[string]any{
		"role":       "indexer",
		"writer":     ix.isWriter.Load(),
		"last_block": nullIfNeg(last),
		"node_block": nullIfNeg(head),
		"lag":        nil,
	}
	if last >= 0 && head >= 0 {
		detail["lag"] = max(0, head-last)
	}
	if ix.sub != nil {
		detail["ws_connected"] = ix.sub.Connected()
		detail["ws_reconnects"] = ix.sub.Reconnects()
	}
	if t := ix.lastRun.Load(); t > 0 {
		detail["last_catch_up"] = time.Unix(t, 0).UTC().Format(time.RFC3339)
	}
	if e, _ := ix.lastErr.Load().(string); e != "" {
		detail["last_error"] = e
	}
	httpx.WriteHealth(w, r, http.StatusOK, "ok", detail)
}

func nullIfNeg(v int64) any {
	if v < 0 {
		return nil
	}
	return v
}

func (ix *Indexer) handleHook(w http.ResponseWriter, r *http.Request) {
	if tok := ix.cfg.IndexerHookToken; tok != "" {
		got := r.Header.Get("X-Hook-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
			httpx.Error(w, r, http.StatusUnauthorized, "bad hook token")
			return
		}
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 64<<10))
	ix.Poke()
	httpx.WriteJSON(w, r, http.StatusAccepted, map[string]string{"status": "queued"})
}

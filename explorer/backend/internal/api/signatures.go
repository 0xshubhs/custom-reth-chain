package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"custom-reth-chain/explorer/backend/internal/httpx"
	"custom-reth-chain/explorer/backend/internal/sigs"
)

// Signature lookups serve the website's decoder, so it never has to ask
// 4byte or sourcify: the bundled list plus every verified contract's ABI.

const maxSignatureKeys = 50

var (
	selectorRE = regexp.MustCompile(`^0x[0-9a-fA-F]{8}$`)
	topicRE    = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
)

// sigCache is the merged index, rebuilt when verified_contracts changes
// (looked at no more often than every 30 s).
type sigCache struct {
	mu      sync.Mutex
	ix      *sigs.Index
	version string
	checked time.Time
}

func (s *Server) signatureIndex(ctx context.Context) *sigs.Index {
	c := &s.sigs
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ix != nil && time.Since(c.checked) < 30*time.Second {
		return c.ix
	}
	c.checked = time.Now()
	if c.ix == nil {
		c.ix = sigs.Bundled()
	}
	if s.pool == nil {
		return c.ix
	}
	var cnt int64
	var latest *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT count(*), max(verified_at) FROM verified_contracts WHERE match_type <> 'similar'`).Scan(&cnt, &latest); err != nil {
		return c.ix
	}
	ver := fmt.Sprintf("%d/%v", cnt, latest)
	if ver == c.version {
		return c.ix
	}
	ix := sigs.Bundled().Clone()
	rows, err := s.pool.Query(ctx, `SELECT abi FROM verified_contracts WHERE match_type <> 'similar'`)
	if err != nil {
		return c.ix
	}
	for rows.Next() {
		var raw []byte
		if rows.Scan(&raw) == nil {
			_ = ix.AddABI(raw) // an unparseable ABI adds nothing
		}
	}
	rows.Close()
	if rows.Err() != nil {
		return c.ix
	}
	c.ix, c.version = ix, ver
	return ix
}

func (s *Server) handleSignatures(kind sigs.Kind, param string, re *regexp.Regexp) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimSpace(r.URL.Query().Get(param))
		if raw == "" {
			badRequest(w, r, param+" is required (comma-separated, at most 50)")
			return
		}
		parts := strings.Split(raw, ",")
		if len(parts) > maxSignatureKeys {
			badRequest(w, r, fmt.Sprintf("at most %d %s per request", maxSignatureKeys, param))
			return
		}
		keys := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if !re.MatchString(p) {
				badRequest(w, r, fmt.Sprintf("%q is not a valid %s entry", p, param))
				return
			}
			keys = append(keys, strings.ToLower(p))
		}
		httpx.WriteJSON(w, r, http.StatusOK, s.signatureIndex(r.Context()).Response(kind, keys))
	}
}

// ---- charts ----

type chartPoint struct {
	Date         string `json:"date"`
	Transactions int64  `json:"transactions"`
}

type chartCache struct {
	mu sync.Mutex
	m  map[int]chartEntry
}

type chartEntry struct {
	items []chartPoint
	at    time.Time
}

func (c *chartCache) clear() {
	c.mu.Lock()
	c.m = nil
	c.mu.Unlock()
}

// handleTxChart is GET /api/v2/stats/charts/transactions?days=N (1..90,
// default 14): transactions per UTC day, oldest first, every day present.
func (s *Server) handleTxChart(w http.ResponseWriter, r *http.Request) {
	days := 14
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 90 {
			badRequest(w, r, "days must be an integer 1..90")
			return
		}
		days = n
	}
	today := time.Now().UTC().Format("2006-01-02")
	c := &s.charts
	c.mu.Lock()
	if e, ok := c.m[days]; ok && time.Since(e.at) < 30*time.Second && len(e.items) > 0 && e.items[len(e.items)-1].Date == today {
		items := e.items
		c.mu.Unlock()
		httpx.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
		return
	}
	c.mu.Unlock()

	rows, err := s.pool.Query(r.Context(), `
		WITH days AS (
			SELECT generate_series($2::date - ($1::int - 1), $2::date, interval '1 day')::date AS d
		)
		SELECT to_char(days.d, 'YYYY-MM-DD'), COALESCE(sum(b.tx_count), 0)::bigint
		FROM days
		LEFT JOIN blocks b ON b.timestamp >= (days.d::timestamp AT TIME ZONE 'UTC')
		                  AND b.timestamp <  ((days.d + 1)::timestamp AT TIME ZONE 'UTC')
		GROUP BY days.d ORDER BY days.d`, days, today)
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	defer rows.Close()
	items := make([]chartPoint, 0, days)
	for rows.Next() {
		var p chartPoint
		if err := rows.Scan(&p.Date, &p.Transactions); err != nil {
			s.dbError(w, r, err)
			return
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		s.dbError(w, r, err)
		return
	}
	c.mu.Lock()
	if c.m == nil {
		c.m = map[int]chartEntry{}
	}
	c.m[days] = chartEntry{items: items, at: time.Now()}
	c.mu.Unlock()
	httpx.WriteJSON(w, r, http.StatusOK, map[string]any{"items": items})
}

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"custom-reth-chain/explorer/backend/internal/httpx"
)

const maxSearchResults = 20

// handleSearch answers block numbers, tx hashes, block hashes, addresses and
// token / contract / label names. At most 20 results, best matches first.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := []SearchResult{}
	if q == "" || len(q) > 200 {
		httpx.WriteJSON(w, r, http.StatusOK, out)
		return
	}
	ctx := r.Context()
	var err error
	exact := false // q is a well-formed hash or address: no name search
	if isDigits(q) {
		if n, perr := strconv.ParseInt(q, 10, 64); perr == nil {
			var hash string
			if err = s.pool.QueryRow(ctx, `SELECT hash FROM blocks WHERE number = $1`, n).Scan(&hash); err == nil {
				out = append(out, SearchResult{Type: "block", Number: ptr(n), Hash: ptr(hash)})
			} else if isNoRows(err) {
				err = nil
			}
		}
	} else if h, ok := normHash(q); ok {
		exact = true
		out, err = s.searchHash(ctx, h)
	} else if a, ok := normAddr(q); ok {
		exact = true
		out, err = s.searchAddress(ctx, a)
	}
	if err != nil {
		s.dbError(w, r, err)
		return
	}
	if !exact && len(out) == 0 && len([]rune(q)) >= 2 {
		if out, err = s.searchText(ctx, q); err != nil {
			s.dbError(w, r, err)
			return
		}
	}
	httpx.WriteJSON(w, r, http.StatusOK, out)
}

func isDigits(s string) bool {
	if s == "" || len(s) > 18 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (s *Server) searchHash(ctx context.Context, h string) ([]SearchResult, error) {
	out := []SearchResult{}
	var ok bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM transactions WHERE hash = $1)`, h).Scan(&ok); err != nil {
		return nil, err
	}
	if ok {
		return append(out, SearchResult{Type: "transaction", Hash: ptr(h)}), nil
	}
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT number FROM blocks WHERE hash = $1`, h).Scan(&n)
	if err == nil {
		return append(out, SearchResult{Type: "block", Number: ptr(n), Hash: ptr(h)}), nil
	}
	if !isNoRows(err) {
		return nil, err
	}
	// Not indexed (yet): ask the node about a transaction.
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var raw json.RawMessage
	if err := s.rpc.Call(cctx, &raw, "eth_getTransactionByHash", h); err == nil && len(raw) > 0 && string(raw) != "null" {
		s.nudgeIndexer()
		out = append(out, SearchResult{Type: "transaction", Hash: ptr(h)})
	}
	return out, nil
}

func (s *Server) searchAddress(ctx context.Context, a string) ([]SearchResult, error) {
	refs, err := s.loadRefs(ctx, refSet{a: {}})
	if err != nil {
		return nil, err
	}
	ref := refs.get(a)
	res := SearchResult{Type: "address", Address: ptr(ref.Hash), Name: ref.Name}
	switch {
	case ref.Token != nil:
		res.Type, res.Name, res.Symbol = "token", ref.Token.Name, ref.Token.Symbol
	case ref.IsContract || ref.IsVerified:
		res.Type = "contract"
	}
	return []SearchResult{res}, nil
}

func (s *Server) searchText(ctx context.Context, q string) ([]SearchResult, error) {
	pat := likePattern(q)
	out := []SearchResult{}
	seen := map[string]bool{}
	rows, err := s.pool.Query(ctx, `
		SELECT address, name, symbol FROM tokens
		WHERE lower(name) LIKE $1 OR lower(symbol) LIKE $1
		ORDER BY (lower(symbol) = lower($2)) DESC, (lower(name) = lower($2)) DESC, holders_count DESC, address
		LIMIT 20`, pat, q)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a string
		var name, sym *string
		if err := rows.Scan(&a, &name, &sym); err != nil {
			rows.Close()
			return nil, err
		}
		seen[a] = true
		out = append(out, SearchResult{Type: "token", Address: ptr(EIP55(a)), Name: name, Symbol: sym})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.pool.Query(ctx, `
		SELECT address, name, 'contract' FROM verified_contracts WHERE lower(name) LIKE $1 AND match_type <> 'similar'
		UNION ALL
		SELECT n.address, n.name, CASE WHEN a.is_contract THEN 'contract' ELSE 'address' END
		  FROM address_names n LEFT JOIN addresses a ON a.address = n.address WHERE lower(n.name) LIKE $1
		LIMIT 40`, pat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a, name, typ string
		if err := rows.Scan(&a, &name, &typ); err != nil {
			return nil, err
		}
		if seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, SearchResult{Type: typ, Address: ptr(EIP55(a)), Name: ptr(name)})
	}
	if len(out) > maxSearchResults {
		out = out[:maxSearchResults]
	}
	return out, rows.Err()
}

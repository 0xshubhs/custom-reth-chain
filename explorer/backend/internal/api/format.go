package api

import (
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
)

// EIP55 renders a stored lowercase address in its checksummed form.
func EIP55(addr string) string {
	if !chainrpc.IsAddress(addr) {
		return addr
	}
	return common.HexToAddress(addr).Hex()
}

func eip55Ptr(addr *string) *string {
	if addr == nil {
		return nil
	}
	s := EIP55(*addr)
	return &s
}

// ISO renders a timestamp as ISO 8601 UTC with second precision.
func ISO(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05Z") }

func isoPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := ISO(*t)
	return &s
}

// Page is the list envelope: {"items":[...],"total":N,"next_offset":M|null}.
type Page[T any] struct {
	Items      []T    `json:"items"`
	Total      int64  `json:"total"`
	NextOffset *int64 `json:"next_offset"`
}

// NewPage builds the envelope; next_offset is null once the end is reached.
func NewPage[T any](items []T, total int64, p PageReq) Page[T] {
	if items == nil {
		items = []T{}
	}
	var next *int64
	if end := p.Offset + int64(len(items)); end < total && len(items) > 0 {
		next = &end
	}
	return Page[T]{Items: items, Total: total, NextOffset: next}
}

// PageReq is a parsed ?limit=&offset=.
type PageReq struct {
	Limit  int64
	Offset int64
}

const (
	defaultLimit = 25
	maxLimit     = 100
	maxOffset    = 10_000_000
)

var errBadPage = errors.New("limit must be an integer 1..100 and offset an integer >= 0")

// ParsePage reads limit (default 25, 1..100) and offset (default 0).
func ParsePage(r *http.Request) (PageReq, error) {
	p := PageReq{Limit: defaultLimit}
	q := r.URL.Query()
	if v := q.Get("limit"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 || n > maxLimit {
			return p, errBadPage
		}
		p.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 || n > maxOffset {
			return p, errBadPage
		}
		p.Offset = n
	}
	return p, nil
}

// normAddr validates and lowercases an address path/query value.
func normAddr(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !chainrpc.IsAddress(s) {
		return "", false
	}
	return strings.ToLower(s), true
}

func normHash(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !chainrpc.IsHash(s) {
		return "", false
	}
	return strings.ToLower(s), true
}

// decimal returns a numeric text value, "0" for empty.
func decimal(s *string) string {
	if s == nil || *s == "" {
		return "0"
	}
	return *s
}

// mulDecimal multiplies two decimal strings (fee = gas_used * price).
func mulDecimal(a int64, price *string) string {
	if price == nil {
		return "0"
	}
	p, ok := new(big.Int).SetString(*price, 10)
	if !ok {
		return "0"
	}
	return p.Mul(p, big.NewInt(a)).String()
}

func ptr[T any](v T) *T { return &v }

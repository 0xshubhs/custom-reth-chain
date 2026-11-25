package chainrpc

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// Quantity is a JSON-RPC hex quantity ("0x1f") decoded leniently: leading
// zeros, a missing 0x and plain JSON numbers are all accepted, so one odd node
// does not stop the indexer. A JSON null leaves Valid false.
type Quantity struct {
	Int   big.Int
	Valid bool
}

func (q *Quantity) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		q.Valid = false
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		v, err := ParseBig(s)
		if err != nil {
			return err
		}
		q.Int.Set(v)
		q.Valid = true
		return nil
	}
	if _, ok := q.Int.SetString(string(b), 10); !ok {
		return fmt.Errorf("quantity: bad number %s", b)
	}
	q.Valid = true
	return nil
}

// Uint64 returns the value, 0 when absent or too large.
func (q Quantity) Uint64() uint64 {
	if !q.Valid || !q.Int.IsUint64() {
		return 0
	}
	return q.Int.Uint64()
}

// Big returns a copy of the value, nil when absent.
func (q Quantity) Big() *big.Int {
	if !q.Valid {
		return nil
	}
	return new(big.Int).Set(&q.Int)
}

// Ptr64 returns a pointer to the value as int64, nil when absent.
func (q Quantity) Ptr64() *int64 {
	if !q.Valid || !q.Int.IsInt64() {
		return nil
	}
	v := q.Int.Int64()
	return &v
}

// ParseBig parses a hex quantity ("0x..."), tolerating leading zeros and an
// empty "0x" (zero). A string without 0x is read as decimal.
func ParseBig(s string) (*big.Int, error) {
	s = strings.TrimSpace(s)
	v := new(big.Int)
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		h := s[2:]
		if h == "" {
			return v, nil
		}
		if _, ok := v.SetString(h, 16); !ok {
			return nil, fmt.Errorf("bad hex quantity %q", s)
		}
		return v, nil
	}
	if _, ok := v.SetString(s, 10); !ok {
		return nil, fmt.Errorf("bad quantity %q", s)
	}
	return v, nil
}

// ParseUint64 parses a hex quantity into a uint64.
func ParseUint64(s string) (uint64, error) {
	v, err := ParseBig(s)
	if err != nil {
		return 0, err
	}
	if !v.IsUint64() {
		return 0, fmt.Errorf("quantity %q overflows uint64", s)
	}
	return v.Uint64(), nil
}

// Hex64 renders n as a JSON-RPC quantity.
func Hex64(n uint64) string { return "0x" + strconv.FormatUint(n, 16) }

// DecodeHex decodes 0x-prefixed hex data. An odd length is an error.
func DecodeHex(s string) ([]byte, error) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	return hex.DecodeString(s)
}

// EncodeHex renders b as lowercase 0x hex.
func EncodeHex(b []byte) string { return "0x" + hex.EncodeToString(b) }

// Lower lowercases a hex string (addresses and hashes are stored lowercase).
func Lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// IsAddress reports whether s is 0x + 40 hex digits.
func IsAddress(s string) bool { return isHexOfLen(s, 40) }

// IsHash reports whether s is 0x + 64 hex digits.
func IsHash(s string) bool { return isHexOfLen(s, 64) }

func isHexOfLen(s string, n int) bool {
	if len(s) != n+2 || (s[:2] != "0x" && s[:2] != "0X") {
		return false
	}
	for i := 2; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// ZeroAddress is the all-zero address in storage form.
const ZeroAddress = "0x0000000000000000000000000000000000000000"

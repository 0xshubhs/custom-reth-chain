// Package evm holds the few hand-rolled ABI pieces the indexer and the API
// need for token and proxy reads: selectors, argument words and tolerant
// decoding of return data (old tokens answer name() as bytes32, broken ones
// answer garbage, and neither may stop the indexer).
package evm

import (
	"encoding/hex"
	"math/big"
	"strings"
	"unicode/utf8"

	"github.com/ethereum/go-ethereum/crypto"
)

// Selector returns the 4-byte selector of a canonical signature.
func Selector(sig string) [4]byte {
	var s [4]byte
	copy(s[:], crypto.Keccak256([]byte(sig))[:4])
	return s
}

// Topic returns keccak256(sig) as lowercase 0x hex: an event's topic0.
func Topic(sig string) string { return "0x" + hex.EncodeToString(crypto.Keccak256([]byte(sig))) }

// Well-known event topics.
var (
	TopicTransfer       = Topic("Transfer(address,address,uint256)")
	TopicTransferSingle = Topic("TransferSingle(address,address,address,uint256,uint256)")
	TopicTransferBatch  = Topic("TransferBatch(address,address,address,uint256[],uint256[])")
)

// Well-known call selectors.
var (
	SelName             = Selector("name()")
	SelSymbol           = Selector("symbol()")
	SelDecimals         = Selector("decimals()")
	SelTotalSupply      = Selector("totalSupply()")
	SelIdentityRegistry = Selector("identityRegistry()")
	SelBalanceOf        = Selector("balanceOf(address)")
	SelBalanceOf1155    = Selector("balanceOf(address,uint256)")
	SelOwnerOf          = Selector("ownerOf(uint256)")
	SelImplementation   = Selector("implementation()")
)

// Proxy storage slots.
const (
	// SlotEIP1967Impl is bytes32(uint256(keccak256("eip1967.proxy.implementation")) - 1).
	SlotEIP1967Impl = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"
	// SlotEIP1967Beacon is bytes32(uint256(keccak256("eip1967.proxy.beacon")) - 1).
	SlotEIP1967Beacon = "0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50"
	// SlotTREXAuthority is keccak256("ERC-3643.proxy.beacon"), where T-REX
	// proxies keep their implementation authority.
	SlotTREXAuthority = "0x821f3e4d3d679f19eacc940c87acf846ea6eae24a63058ea750304437a62aafc"
)

// Calldata builds selector + 32-byte words.
func Calldata(sel [4]byte, words ...[]byte) string {
	b := make([]byte, 0, 4+32*len(words))
	b = append(b, sel[:]...)
	for _, w := range words {
		b = append(b, w...)
	}
	return "0x" + hex.EncodeToString(b)
}

// AddressWord left-pads a 0x address to 32 bytes.
func AddressWord(addr string) []byte {
	w := make([]byte, 32)
	raw, _ := hex.DecodeString(strings.TrimPrefix(strings.ToLower(addr), "0x"))
	if len(raw) > 20 {
		raw = raw[len(raw)-20:]
	}
	copy(w[32-len(raw):], raw)
	return w
}

// UintWord renders v as one 32-byte word (v must fit in 256 bits).
func UintWord(v *big.Int) []byte {
	w := make([]byte, 32)
	if v != nil && v.Sign() >= 0 && v.BitLen() <= 256 {
		v.FillBytes(w)
	}
	return w
}

// Bytes decodes 0x hex return data; nil on bad hex.
func Bytes(hexdata string) []byte {
	s := strings.TrimPrefix(strings.TrimPrefix(hexdata, "0x"), "0X")
	if len(s)%2 == 1 {
		return nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

// DecodeUint reads the first word of return data as uint256.
func DecodeUint(ret []byte) (*big.Int, bool) {
	if len(ret) < 32 {
		return nil, false
	}
	return new(big.Int).SetBytes(ret[:32]), true
}

// DecodeAddress reads the first word as an address. ok is false when the
// word has bytes above the low 20 (then it is not an address).
func DecodeAddress(ret []byte) (string, bool) {
	if len(ret) < 32 {
		return "", false
	}
	return WordAddress(ret[:32])
}

// WordAddress interprets a 32-byte word (a topic, a storage slot) as an
// address, refusing words with non-zero high bytes.
func WordAddress(w []byte) (string, bool) {
	if len(w) != 32 {
		return "", false
	}
	for _, c := range w[:12] {
		if c != 0 {
			return "", false
		}
	}
	return "0x" + hex.EncodeToString(w[12:]), true
}

// TopicAddress decodes an indexed address topic.
func TopicAddress(topic string) (string, bool) {
	b := Bytes(topic)
	if len(b) != 32 {
		return "", false
	}
	return WordAddress(b)
}

// TopicUint decodes an indexed uint256 topic.
func TopicUint(topic string) (*big.Int, bool) {
	b := Bytes(topic)
	if len(b) != 32 {
		return nil, false
	}
	return new(big.Int).SetBytes(b), true
}

// DecodeString decodes a string() return value: ABI string first, then a
// bytes32 padded with zeros (old tokens). Invalid UTF-8 is repaired and NULs
// are dropped so the text is always storable; at most 256 runes are kept.
func DecodeString(ret []byte) (string, bool) {
	var s string
	switch {
	case len(ret) >= 64:
		off := new(big.Int).SetBytes(ret[:32])
		if !off.IsUint64() || off.Uint64() > uint64(len(ret)-32) {
			return decodeBytes32(ret)
		}
		o := int(off.Uint64())
		ln := new(big.Int).SetBytes(ret[o : o+32])
		if !ln.IsUint64() || ln.Uint64() > uint64(len(ret)-o-32) {
			return decodeBytes32(ret)
		}
		s = string(ret[o+32 : o+32+int(ln.Uint64())])
	case len(ret) == 32:
		return decodeBytes32(ret)
	default:
		return "", false
	}
	return CleanText(s, 256), true
}

func decodeBytes32(ret []byte) (string, bool) {
	if len(ret) < 32 {
		return "", false
	}
	w := ret[:32]
	end := 0
	for end < 32 && w[end] != 0 {
		end++
	}
	for _, c := range w[end:] {
		if c != 0 {
			return "", false // not a zero-padded string
		}
	}
	if end == 0 {
		return "", false
	}
	return CleanText(string(w[:end]), 256), true
}

// CleanText makes s storable as Postgres text: valid UTF-8, no NUL, at most
// max runes (max <= 0: no limit).
func CleanText(s string, max int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "")
	}
	if max > 0 && utf8.RuneCountInString(s) > max {
		r := []rune(s)
		s = string(r[:max])
	}
	return s
}

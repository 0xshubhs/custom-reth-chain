package verifier

import (
	"bytes"
	"encoding/hex"
	"strings"
)

// MatchType is how closely compiled code equals the code on chain.
type MatchType int

const (
	NoMatch MatchType = iota
	// Partial: equal once the CBOR metadata blobs are left out, so the
	// executable code is the same but comments, whitespace or file names
	// may differ from what was deployed.
	Partial
	// Full: equal including the metadata (or there is no metadata to
	// compare, when settings.metadata.appendCBOR is false).
	Full
)

func (m MatchType) String() string {
	switch m {
	case Full:
		return "full"
	case Partial:
		return "partial"
	default:
		return "none"
	}
}

func minMatch(a, b MatchType) MatchType {
	if a < b {
		return a
	}
	return b
}

// ByteRange is a range in compiler output: linkReferences and
// immutableReferences entries.
type ByteRange struct {
	Start  int `json:"start"`
	Length int `json:"length"`
}

// Bytecode is evm.bytecode or evm.deployedBytecode from the compiler output.
type Bytecode struct {
	Object              string                            `json:"object"`
	LinkReferences      map[string]map[string][]ByteRange `json:"linkReferences"`
	ImmutableReferences map[string][]ByteRange            `json:"immutableReferences"`
}

// linked decodes a compiled object, filling every library placeholder from
// the bytes at the same offsets in onchain (the address the deployer linked
// in). It fails when a placeholder has no link reference to say where it is,
// or a reference points past the end of either side.
func linked(object string, refs map[string]map[string][]ByteRange, onchain []byte) ([]byte, bool) {
	object = strings.TrimPrefix(object, "0x")
	if len(object)%2 != 0 {
		return nil, false
	}
	hexed := []byte(object)
	for _, libs := range refs {
		for _, ranges := range libs {
			for _, r := range ranges {
				if r.Start < 0 || r.Length <= 0 || r.Start+r.Length > len(onchain) || 2*(r.Start+r.Length) > len(hexed) {
					return nil, false
				}
				hex.Encode(hexed[2*r.Start:], onchain[r.Start:r.Start+r.Length])
			}
		}
	}
	out := make([]byte, len(hexed)/2)
	if _, err := hex.Decode(out, hexed); err != nil {
		return nil, false // a placeholder nobody told us about
	}
	return out, true
}

// libraryPrefix is how a library's runtime code starts in compiler output:
// PUSH20 followed by a zero address. At deployment the constructor writes
// the library's own address there (the guard against calling a library
// other than through DELEGATECALL), so the on-chain code has its address
// where the compiler put zeros.
func isLibraryRuntime(code []byte) bool {
	if len(code) < 21 || code[0] != 0x73 {
		return false
	}
	for _, b := range code[1:21] {
		if b != 0 {
			return false
		}
	}
	return true
}

// maskAux zeroes, in both a and b, every metadata blob found in a that b also
// has, at the same place and of the same size. A region where b has no valid
// blob is left alone, so the comparison that follows still sees it.
func maskAux(a, b []byte) {
	for _, s := range auxBlobs(a) {
		if s.end > len(b) {
			continue
		}
		if end, ok := auxBlobAt(b, s.start); !ok || end != s.end {
			continue
		}
		clear(a[s.start:s.end])
		clear(b[s.start:s.end])
	}
}

// MatchRuntime compares a contract's compiled runtime code with the code at
// its address.
//
//  1. Library placeholders are filled from the on-chain code at the same
//     offsets, and a library's PUSH20 self-address guard is taken from it.
//  2. Every immutableReferences range is zeroed in both: those bytes are
//     written by the constructor and differ per deployment.
//  3. Equal = Full. When the build appends CBOR metadata (cbor true),
//     equal once the trailing metadata blob is removed from both and any
//     metadata blobs of embedded contracts are masked = Partial.
func MatchRuntime(bc Bytecode, onchain []byte, cbor bool) MatchType {
	compiled, ok := linked(bc.Object, bc.LinkReferences, onchain)
	if !ok || len(compiled) == 0 || len(onchain) == 0 {
		return NoMatch
	}
	chain := bytes.Clone(onchain)
	if isLibraryRuntime(compiled) && chain[0] == 0x73 && len(chain) >= 21 {
		copy(compiled[1:21], chain[1:21])
	}
	for _, ranges := range bc.ImmutableReferences {
		for _, r := range ranges {
			if r.Start < 0 || r.Length <= 0 || r.Start+r.Length > len(compiled) || r.Start+r.Length > len(chain) {
				return NoMatch
			}
			clear(compiled[r.Start : r.Start+r.Length])
			clear(chain[r.Start : r.Start+r.Length])
		}
	}
	if bytes.Equal(compiled, chain) {
		return Full
	}
	if !cbor {
		return NoMatch
	}
	ct, ot := cborTailLen(compiled), cborTailLen(chain)
	if ct == 0 || ot == 0 {
		return NoMatch
	}
	compiled, chain = compiled[:len(compiled)-ct], chain[:len(chain)-ot]
	if len(compiled) != len(chain) {
		return NoMatch
	}
	maskAux(compiled, chain)
	if bytes.Equal(compiled, chain) {
		return Partial
	}
	return NoMatch
}

// MatchCreation checks that a contract's compiled creation code is a prefix
// of the input that created it (a transaction's data, or the init code of a
// CREATE frame) and returns what follows: the ABI-encoded constructor
// arguments. Library placeholders are filled from the input; metadata is
// handled as in MatchRuntime, the trailing blob of the creation code being
// followed by the arguments rather than by the end of the input.
func MatchCreation(bc Bytecode, input []byte, cbor bool) (MatchType, []byte) {
	compiled, ok := linked(bc.Object, bc.LinkReferences, input)
	if !ok || len(compiled) == 0 {
		return NoMatch, nil
	}
	if len(input) >= len(compiled) && bytes.Equal(compiled, input[:len(compiled)]) {
		return Full, input[len(compiled):]
	}
	if !cbor {
		return NoMatch, nil
	}
	ct := cborTailLen(compiled)
	if ct == 0 {
		return NoMatch, nil
	}
	body := compiled[:len(compiled)-ct]
	if len(input) <= len(body) {
		return NoMatch, nil
	}
	prefix := bytes.Clone(input[:len(body)])
	maskAux(body, prefix)
	if !bytes.Equal(body, prefix) {
		return NoMatch, nil
	}
	// The deployed creation code had its own metadata blob here, possibly of
	// a different size; the arguments start where it ends.
	end, ok := auxBlobAt(input, len(body))
	if !ok {
		return NoMatch, nil
	}
	return Partial, input[end:]
}

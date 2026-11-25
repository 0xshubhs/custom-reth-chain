package verifier

import "encoding/binary"

// Solidity appends a CBOR-encoded map to the code it emits (unless
// settings.metadata.appendCBOR is false), followed by the map's length as a
// 2-byte big-endian number:
//
//	... code ... | a2 64 'ipfs' 58 22 <34 bytes> 64 'solc' 43 00 08 1c | 00 33
//
// The map holds the metadata hash (ipfs, bzzr0 or bzzr1) and the compiler
// version (solc). It is the only part of the code that changes when only
// comments, whitespace or file names change, which is what a partial match
// forgives. A contract that creates another one (`new C`) carries C's code,
// and so C's map, in the middle of its own code; those are found by scanning.
//
// The decoder below accepts only what solc writes: one map, definite lengths,
// text keys, at least one key solc uses. It is deliberately strict, because
// anything it accepts may be masked out of a comparison.

// auxKeys are the map keys solc has ever written.
var auxKeys = map[string]bool{
	"ipfs":         true,
	"bzzr0":        true,
	"bzzr1":        true,
	"solc":         true,
	"experimental": true,
}

// maxAuxLen bounds the map size. Real ones are 10 to about 85 bytes.
const maxAuxLen = 512

// cborItemLen returns the encoded length of the CBOR data item at the start
// of b, or -1 when b does not start with a complete, definite-length item.
func cborItemLen(b []byte, depth int) int {
	if len(b) == 0 || depth > 8 {
		return -1
	}
	major := b[0] >> 5
	val, hdr := cborArg(b)
	if hdr < 0 {
		return -1
	}
	switch major {
	case 0, 1: // unsigned / negative integer
		return hdr
	case 2, 3: // byte string / text string
		if val > uint64(len(b)-hdr) {
			return -1
		}
		return hdr + int(val)
	case 4, 5: // array / map
		items := val
		if major == 5 {
			items *= 2
		}
		if items > uint64(len(b)) { // every item is at least one byte
			return -1
		}
		n := hdr
		for i := uint64(0); i < items; i++ {
			l := cborItemLen(b[n:], depth+1)
			if l < 0 {
				return -1
			}
			n += l
		}
		return n
	case 6: // tag
		l := cborItemLen(b[hdr:], depth+1)
		if l < 0 {
			return -1
		}
		return hdr + l
	default: // 7: simple values and floats
		ai := b[0] & 0x1f
		if ai == 24 && hdr == 2 && b[1] < 32 {
			return -1 // simple values below 32 must use the one-byte form
		}
		return hdr
	}
}

// cborArg decodes the argument of the item header at b[0]: its value and the
// header length. hdr is -1 for indefinite lengths, reserved values or a
// truncated header.
func cborArg(b []byte) (val uint64, hdr int) {
	ai := b[0] & 0x1f
	switch {
	case ai < 24:
		return uint64(ai), 1
	case ai == 24:
		if len(b) < 2 {
			return 0, -1
		}
		return uint64(b[1]), 2
	case ai == 25:
		if len(b) < 3 {
			return 0, -1
		}
		return uint64(binary.BigEndian.Uint16(b[1:])), 3
	case ai == 26:
		if len(b) < 5 {
			return 0, -1
		}
		return uint64(binary.BigEndian.Uint32(b[1:])), 5
	case ai == 27:
		if len(b) < 9 {
			return 0, -1
		}
		return binary.BigEndian.Uint64(b[1:]), 9
	default: // 28-30 reserved, 31 indefinite
		return 0, -1
	}
}

// auxMapLen returns the length of the solc metadata map starting at b[0], or
// -1 when b does not start with one: a map of 1 to 8 entries, every key a text
// string, at least one key that solc writes.
func auxMapLen(b []byte) int {
	if len(b) == 0 || b[0]>>5 != 5 {
		return -1
	}
	count, hdr := cborArg(b)
	if hdr < 0 || count < 1 || count > 8 {
		return -1
	}
	n := hdr
	known := false
	for i := uint64(0); i < count; i++ {
		if n >= len(b) || b[n]>>5 != 3 {
			return -1 // key is not a text string
		}
		klen := cborItemLen(b[n:], 1)
		if klen < 0 {
			return -1
		}
		_, khdr := cborArg(b[n:])
		if auxKeys[string(b[n+khdr:n+klen])] {
			known = true
		}
		n += klen
		vlen := cborItemLen(b[n:], 1)
		if vlen < 0 {
			return -1
		}
		n += vlen
		if n > maxAuxLen {
			return -1
		}
	}
	if !known {
		return -1
	}
	return n
}

// auxBlobAt reports whether a metadata map plus its 2-byte length trailer
// starts at b[p], and where that blob ends.
func auxBlobAt(b []byte, p int) (end int, ok bool) {
	if p < 0 || p >= len(b) {
		return 0, false
	}
	n := auxMapLen(b[p:])
	if n < 0 || p+n+2 > len(b) {
		return 0, false
	}
	if int(binary.BigEndian.Uint16(b[p+n:])) != n {
		return 0, false
	}
	return p + n + 2, true
}

// cborTailLen returns the length of the metadata blob (map + 2-byte trailer)
// at the very end of code, or 0 when code does not end with one.
func cborTailLen(code []byte) int {
	if len(code) < 3 {
		return 0
	}
	n := int(binary.BigEndian.Uint16(code[len(code)-2:]))
	start := len(code) - 2 - n
	if n == 0 || start < 0 {
		return 0
	}
	if end, ok := auxBlobAt(code, start); !ok || end != len(code) {
		return 0
	}
	return n + 2
}

// span is a half-open byte range [start, end).
type span struct{ start, end int }

// auxBlobs finds every metadata blob inside code, in order. A blob starts
// with a map header (0xa1..0xa8) whose first key is one solc writes, which
// keeps the scan cheap: almost every byte is rejected after two compares.
func auxBlobs(code []byte) []span {
	var out []span
	for i := 0; i+1 < len(code); i++ {
		if code[i] < 0xa1 || code[i] > 0xa8 {
			continue
		}
		switch code[i+1] {
		case 0x64, 0x65, 0x6c: // text(4) "ipfs"/"solc", text(5) "bzzr0"/"bzzr1", text(12) "experimental"
		default:
			continue
		}
		if end, ok := auxBlobAt(code, i); ok {
			out = append(out, span{i, end})
			i = end - 1
		}
	}
	return out
}

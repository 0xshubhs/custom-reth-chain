// Package chainrpc talks to the chain node over JSON-RPC.
package chainrpc

import "github.com/ethereum/go-ethereum/crypto"

// Keccak256Hex returns the 0x-prefixed lowercase keccak256 of b.
func Keccak256Hex(b []byte) string {
	const hexdigits = "0123456789abcdef"
	sum := crypto.Keccak256(b)
	out := make([]byte, 2+len(sum)*2)
	out[0], out[1] = '0', 'x'
	for i, c := range sum {
		out[2+i*2] = hexdigits[c>>4]
		out[3+i*2] = hexdigits[c&0x0f]
	}
	return string(out)
}

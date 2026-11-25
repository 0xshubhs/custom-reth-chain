package verifier

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatalf("bad test hex %q: %v", s, err)
	}
	return b
}

// Metadata blobs as solc writes them (map + 2-byte length).
const (
	// bytecodeHash ipfs (the default): {"ipfs": bytes(34), "solc": bytes(3)}, 0x33 = 51 bytes.
	ipfsBlobA = "a2 64 69706673 58 22 1220" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + " 64 736f6c63 43 00081c 0033"
	ipfsBlobB = "a2 64 69706673 58 22 1220" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" + " 64 736f6c63 43 00081c 0033"
	// bytecodeHash none: {"solc": bytes(3)}, 10 bytes.
	solcOnlyBlob = "a1 64 736f6c63 43 00081c 000a"
	// solc 0.5.x: {"bzzr0": bytes(32)}, 41 bytes.
	bzzr0Blob = "a1 65 627a7a7230 58 20" + "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" + " 0029"
	// experimental features on: {"ipfs":..., "experimental": true, "solc":...}.
	experimentalBlob = "a3 64 69706673 58 22 1220" + "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" +
		" 6c 6578706572696d656e74616c f5 64 736f6c63 43 000811 0041"
)

func TestCBORTailLen(t *testing.T) {
	code := "6080604052 348015 600f57 5f80fd 5b50 fe"
	cases := []struct {
		name string
		hex  string
		want int
	}{
		{"ipfs and solc", code + ipfsBlobA, 53},
		{"bytecodeHash none", code + solcOnlyBlob, 12},
		{"legacy bzzr0", code + bzzr0Blob, 43},
		{"experimental flag", code + experimentalBlob, 67},
		{"no tail at all", code, 0},
		{"length points before the start", "ff 00 ff ffff", 0},
		{"length one off", code + strings.TrimSuffix(ipfsBlobA, "0033") + "0034", 0},
		{"map with an integer key", code + "a1 01 43 00081c 0006", 0},
		{"map with only unknown keys", code + "a1 63 666f6f 43 00081c 0009", 0},
		{"indefinite-length map", code + "bf 64 736f6c63 43 00081c ff 000b", 0},
		{"not a map", code + "64 736f6c63 0005", 0},
		{"zero length", code + "0000", 0},
		{"too short", "0033", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cborTailLen(mustHex(t, c.hex)); got != c.want {
				t.Fatalf("cborTailLen = %d, want %d", got, c.want)
			}
		})
	}
}

func TestCBORItemLenRejectsTruncatedAndReserved(t *testing.T) {
	for _, h := range []string{
		"58",             // byte string, 1-byte length missing
		"58 05 0102",     // byte string shorter than its length
		"19 01",          // uint16 truncated
		"1c",             // reserved additional info
		"5f 41 00 ff",    // indefinite byte string
		"a1 64 736f6c63", // map value missing
		"9a ffffffff",    // array claiming 4 billion items
	} {
		if n := cborItemLen(mustHex(t, h), 0); n != -1 {
			t.Errorf("cborItemLen(%s) = %d, want -1", h, n)
		}
	}
	// Nesting beyond the depth limit is refused rather than recursed into.
	deep := bytes.Repeat([]byte{0x81}, 20)
	deep = append(deep, 0x01)
	if n := cborItemLen(deep, 0); n != -1 {
		t.Errorf("deeply nested array accepted: %d", n)
	}
	if n := cborItemLen(mustHex(t, "f5"), 0); n != 1 {
		t.Errorf("true: %d", n)
	}
	if n := cborItemLen(mustHex(t, "fb 3ff0000000000000"), 0); n != 9 {
		t.Errorf("float64: %d", n)
	}
	if n := cborItemLen(mustHex(t, "c2 41 01"), 0); n != 3 {
		t.Errorf("tagged bignum: %d", n)
	}
}

func TestAuxBlobsFindsEmbeddedMetadata(t *testing.T) {
	// Parent code, then a child contract's code with its blob, then more
	// parent code, then the parent's own blob.
	code := mustHex(t, "6080 6040 52 "+"600a 600b"+ipfsBlobA+"5b 5b 5b"+solcOnlyBlob)
	spans := auxBlobs(code)
	if len(spans) != 2 {
		t.Fatalf("found %d blobs, want 2: %v", len(spans), spans)
	}
	if spans[0].start != 9 || spans[0].end != 9+53 {
		t.Errorf("first blob at %v", spans[0])
	}
	if spans[1].end != len(code) || spans[1].end-spans[1].start != 12 {
		t.Errorf("second blob at %v (len %d)", spans[1], len(code))
	}
	// Bytes that start like a map but are not a well-formed blob are not reported.
	if s := auxBlobs(mustHex(t, "a2 64 69706673 00 00 a1 64 736f6c63 43 00081c 0011")); len(s) != 0 {
		t.Errorf("malformed blob reported: %v", s)
	}
}

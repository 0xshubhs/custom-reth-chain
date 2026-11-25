package verifier

import (
	"bytes"
	"strings"
	"testing"
)

// hx joins hex fragments and drops the spaces used to lay them out.
func hx(parts ...string) string { return strings.ReplaceAll(strings.Join(parts, ""), " ", "") }

const (
	// Arbitrary but plausible code fragments.
	codeA = "6080604052 348015 600e57 5f80fd 5b50 6004361060 2a57"
	codeB = "5b 6000 35 60e0 1c 80 63 12345678 14 6030 57 5b 5f80fd"
	codeC = "5b 34 80 15 61 0040 57 5f 80 fd 5b 50 00"

	zero32 = "0000000000000000000000000000000000000000000000000000000000000000"
	imm1   = "1111111111111111111111111111111111111111111111111111111111111111"
	imm2   = "000000000000000000000000c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00"

	libAddr = "5fbdb2315678afecb367f032d93f642f64180aa3"
	zero20  = "0000000000000000000000000000000000000000"
	// solc >= 0.5 placeholder: __$ + 34 hex chars of keccak(fully qualified name) + $__.
	placeholder = "__$1a2b3c4d5e6f708192a3b4c5d6e7f80912$__"
	// solc < 0.5 placeholder: __ + name, padded with _ to 40 chars.
	legacyPlaceholder = "__lib/L.sol:L___________________________"
)

func bc(object string) Bytecode { return Bytecode{Object: object} }

func TestPlaceholdersAreFortyChars(t *testing.T) {
	for _, p := range []string{placeholder, legacyPlaceholder} {
		if len(p) != 40 {
			t.Fatalf("test placeholder %q is %d chars", p, len(p))
		}
	}
}

func TestRuntimeExactEquality(t *testing.T) {
	code := hx(codeA, codeB)
	for _, cbor := range []bool{false, true} {
		if got := MatchRuntime(bc(code), mustHex(t, code), cbor); got != Full {
			t.Errorf("cbor=%v: %v, want full", cbor, got)
		}
	}
	// With a metadata tail, byte-for-byte equality is a full match too.
	withTail := hx(codeA, codeB, ipfsBlobA)
	if got := MatchRuntime(bc(withTail), mustHex(t, withTail), true); got != Full {
		t.Errorf("identical code with tail: %v, want full", got)
	}
	// "0x" on the compiled object is tolerated.
	if got := MatchRuntime(bc("0x"+code), mustHex(t, code), false); got != Full {
		t.Errorf("0x-prefixed object: %v", got)
	}
}

func TestRuntimeOneByteDifferenceIsNoMatch(t *testing.T) {
	code := mustHex(t, hx(codeA, codeB))
	chain := bytes.Clone(code)
	chain[7] ^= 0x01
	for _, cbor := range []bool{false, true} {
		if got := MatchRuntime(bc(hx(codeA, codeB)), chain, cbor); got != NoMatch {
			t.Errorf("cbor=%v: %v, want none", cbor, got)
		}
	}
	// Extra code at the end is a difference, not something to ignore.
	if got := MatchRuntime(bc(hx(codeA)), mustHex(t, hx(codeA, codeB)), true); got != NoMatch {
		t.Errorf("prefix accepted as runtime match: %v", got)
	}
}

func TestRuntimeImmutablesMaskedInBoth(t *testing.T) {
	// Two immutables (one referenced twice) that the constructor filled in.
	compiled := hx(codeA, zero32, codeB, zero32, codeC, zero32)
	chain := hx(codeA, imm1, codeB, imm2, codeC, imm1)
	a := len(mustHex(t, codeA))
	b := len(mustHex(t, codeB))
	c := len(mustHex(t, codeC))
	refs := map[string][]ByteRange{
		"101": {{Start: a, Length: 32}, {Start: a + 32 + b + 32 + c, Length: 32}},
		"202": {{Start: a + 32 + b, Length: 32}},
	}
	code := Bytecode{Object: compiled, ImmutableReferences: refs}
	if got := MatchRuntime(code, mustHex(t, chain), false); got != Full {
		t.Fatalf("immutables: %v, want full", got)
	}

	// Without the references the same pair does not match.
	if got := MatchRuntime(bc(compiled), mustHex(t, chain), false); got != NoMatch {
		t.Errorf("immutables ignored without references: %v", got)
	}

	// A difference outside the immutable ranges is still caught.
	bad := mustHex(t, chain)
	bad[a+32] ^= 0xff
	if got := MatchRuntime(code, bad, false); got != NoMatch {
		t.Errorf("difference next to an immutable: %v", got)
	}

	// A reference past the end of the on-chain code cannot match.
	short := Bytecode{Object: compiled, ImmutableReferences: map[string][]ByteRange{"1": {{Start: len(mustHex(t, compiled)) - 16, Length: 32}}}}
	if got := MatchRuntime(short, mustHex(t, chain), false); got != NoMatch {
		t.Errorf("out-of-range immutable: %v", got)
	}
}

func TestRuntimeLibraryPlaceholdersFilledFromChain(t *testing.T) {
	// PUSH20 <library> ... DELEGATECALL
	for _, ph := range []string{placeholder, legacyPlaceholder} {
		compiled := hx("6080 73", ph, "5af4", codeB)
		chain := hx("6080 73", libAddr, "5af4", codeB)
		refs := map[string]map[string][]ByteRange{"lib/L.sol": {"L": {{Start: 3, Length: 20}}}}

		if got := MatchRuntime(Bytecode{Object: compiled, LinkReferences: refs}, mustHex(t, chain), false); got != Full {
			t.Errorf("%s: linked library: %v, want full", ph[:4], got)
		}
		// Without linkReferences the placeholder cannot be resolved.
		if got := MatchRuntime(bc(compiled), mustHex(t, chain), false); got != NoMatch {
			t.Errorf("%s: unresolved placeholder: %v", ph[:4], got)
		}
	}

	// Filling only happens at the referenced offsets: the rest must match.
	compiled := hx("6080 73", placeholder, "5af4", codeB)
	refs := map[string]map[string][]ByteRange{"lib/L.sol": {"L": {{Start: 3, Length: 20}}}}
	chain := mustHex(t, hx("6080 73", libAddr, "5af4", codeB))
	chain[len(chain)-1] ^= 1
	if got := MatchRuntime(Bytecode{Object: compiled, LinkReferences: refs}, chain, false); got != NoMatch {
		t.Errorf("difference outside the link: %v", got)
	}

	// A reference beyond the on-chain code is refused, not a panic.
	far := map[string]map[string][]ByteRange{"lib/L.sol": {"L": {{Start: 400, Length: 20}}}}
	if got := MatchRuntime(Bytecode{Object: compiled, LinkReferences: far}, mustHex(t, hx("6080")), false); got != NoMatch {
		t.Errorf("far reference: %v", got)
	}
}

func TestRuntimeLibrarySelfAddressGuard(t *testing.T) {
	// A library's runtime begins PUSH20 <zero> ADDRESS EQ; once deployed the
	// zero is its own address.
	compiled := hx("73", zero20, "30 14 6080604052", codeB)
	chain := hx("73", libAddr, "30 14 6080604052", codeB)
	if got := MatchRuntime(bc(compiled), mustHex(t, chain), false); got != Full {
		t.Fatalf("library self address: %v, want full", got)
	}
	// Only the guard is taken from chain; a later difference still fails.
	bad := mustHex(t, chain)
	bad[len(bad)-1] ^= 1
	if got := MatchRuntime(bc(compiled), bad, false); got != NoMatch {
		t.Errorf("library with a difference: %v", got)
	}
	// Code that does not start with the guard gets no such allowance.
	notLib := hx("60", zero20, "30 14", codeB)
	chainNotLib := hx("60", libAddr, "30 14", codeB)
	if got := MatchRuntime(bc(notLib), mustHex(t, chainNotLib), false); got != NoMatch {
		t.Errorf("non-library treated as library: %v", got)
	}
}

func TestRuntimeMetadataTail(t *testing.T) {
	body := hx(codeA, codeB)
	cases := []struct {
		name     string
		compiled string
		chain    string
		cbor     bool
		want     MatchType
	}{
		{"same code, other metadata hash", body + ipfsBlobA, body + ipfsBlobB, true, Partial},
		{"same code, other metadata size", body + ipfsBlobA, body + solcOnlyBlob, true, Partial},
		{"same code, legacy swarm hash on chain", body + ipfsBlobA, body + bzzr0Blob, true, Partial},
		{"appendCBOR false: no tail to forgive", body + ipfsBlobA, body + ipfsBlobB, false, NoMatch},
		{"compiled has a tail, chain has none", body + ipfsBlobA, body, true, NoMatch},
		{"chain has a tail, compiled has none", body, body + ipfsBlobA, true, NoMatch},
		{"code differs as well as metadata", hx(codeA, codeC) + ipfsBlobA, body + ipfsBlobB, true, NoMatch},
		{"code length differs before the tail", hx(codeA, codeB, "00") + ipfsBlobA, body + ipfsBlobB, true, NoMatch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MatchRuntime(bc(hx(c.compiled)), mustHex(t, hx(c.chain)), c.cbor); got != c.want {
				t.Fatalf("%v, want %v", got, c.want)
			}
		})
	}
}

func TestRuntimeEmbeddedContractMetadata(t *testing.T) {
	// A factory carries the creation code of what it creates, metadata
	// included. When only metadata differs, both blobs differ.
	compiled := hx(codeA, "6080", solcOnlyBlob, codeB, ipfsBlobA, codeC, ipfsBlobA)
	chain := hx(codeA, "6080", solcOnlyBlob, codeB, ipfsBlobB, codeC, ipfsBlobB)
	if got := MatchRuntime(bc(compiled), mustHex(t, chain), true); got != Partial {
		t.Fatalf("embedded + tail metadata differ: %v, want partial", got)
	}
	// Outer tail identical, only the embedded one differs: still partial.
	chain2 := hx(codeA, "6080", solcOnlyBlob, codeB, ipfsBlobB, codeC, ipfsBlobA)
	if got := MatchRuntime(bc(compiled), mustHex(t, chain2), true); got != Partial {
		t.Errorf("embedded metadata differs: %v, want partial", got)
	}
	// Where chain has something that is not a metadata blob, nothing is masked.
	notBlob := strings.Repeat("5b", len(mustHex(t, ipfsBlobA)))
	chain3 := hx(codeA, "6080", solcOnlyBlob, codeB, notBlob, codeC, ipfsBlobB)
	if got := MatchRuntime(bc(compiled), mustHex(t, chain3), true); got != NoMatch {
		t.Errorf("code in place of embedded metadata: %v, want none", got)
	}
	// With appendCBOR false nothing is masked.
	if got := MatchRuntime(bc(compiled), mustHex(t, chain), false); got != NoMatch {
		t.Errorf("appendCBOR false: %v, want none", got)
	}
}

func TestRuntimeImmutablesAndMetadataTogether(t *testing.T) {
	compiled := hx(codeA, zero32, codeB, ipfsBlobA)
	chain := hx(codeA, imm1, codeB, ipfsBlobB)
	code := Bytecode{Object: compiled, ImmutableReferences: map[string][]ByteRange{"7": {{Start: len(mustHex(t, codeA)), Length: 32}}}}
	if got := MatchRuntime(code, mustHex(t, chain), true); got != Partial {
		t.Fatalf("%v, want partial", got)
	}
}

func TestRuntimeBadInput(t *testing.T) {
	cases := []struct {
		name   string
		object string
		chain  string
	}{
		{"odd-length object", "608", "6080"},
		{"empty object", "", "6080"},
		{"empty on-chain code", "6080", ""},
		{"not hex", "zz80", "6080"},
	}
	for _, c := range cases {
		if got := MatchRuntime(bc(c.object), mustHex(t, c.chain), true); got != NoMatch {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestCreationPrefixAndConstructorArgs(t *testing.T) {
	initCode := hx(codeA, codeB, codeC)
	args := hx(imm1, imm2)

	mt, got := MatchCreation(bc(initCode), mustHex(t, initCode+args), false)
	if mt != Full || !bytes.Equal(got, mustHex(t, args)) {
		t.Fatalf("with args: %v %x", mt, got)
	}
	mt, got = MatchCreation(bc(initCode), mustHex(t, initCode), false)
	if mt != Full || len(got) != 0 {
		t.Fatalf("without args: %v %x", mt, got)
	}
	if mt, _ := MatchCreation(bc(initCode), mustHex(t, hx(codeA, codeB)), false); mt != NoMatch {
		t.Errorf("input shorter than the init code: %v", mt)
	}
	other := mustHex(t, initCode+args)
	other[3] ^= 1
	if mt, _ := MatchCreation(bc(initCode), other, true); mt != NoMatch {
		t.Errorf("init code differs: %v", mt)
	}
}

func TestCreationLinksFilledFromInput(t *testing.T) {
	compiled := hx("6080 73", placeholder, "5af4", codeB)
	input := hx("6080 73", libAddr, "5af4", codeB, imm1)
	refs := map[string]map[string][]ByteRange{"lib/L.sol": {"L": {{Start: 3, Length: 20}}}}
	mt, args := MatchCreation(Bytecode{Object: compiled, LinkReferences: refs}, mustHex(t, input), false)
	if mt != Full || !bytes.Equal(args, mustHex(t, imm1)) {
		t.Fatalf("%v %x", mt, args)
	}
}

func TestCreationMetadata(t *testing.T) {
	// creation = constructor + runtime (with its blob) + creation blob.
	ctor := hx(codeA)
	runtime := hx(codeB, codeC)
	args := hx(imm1)

	compiled := hx(ctor, runtime, ipfsBlobA, ipfsBlobA)
	cases := []struct {
		name     string
		input    string
		cbor     bool
		want     MatchType
		wantArgs string
	}{
		{"identical", hx(ctor, runtime, ipfsBlobA, ipfsBlobA, args), true, Full, args},
		{"both hashes differ", hx(ctor, runtime, ipfsBlobB, ipfsBlobB, args), true, Partial, args},
		{"creation blob of another size", hx(ctor, runtime, ipfsBlobB, solcOnlyBlob, args), true, Partial, args},
		{"creation blob of another size, no args", hx(ctor, runtime, ipfsBlobB, solcOnlyBlob), true, Partial, ""},
		{"appendCBOR false", hx(ctor, runtime, ipfsBlobB, ipfsBlobB, args), false, NoMatch, ""},
		{"no blob where the compiled one ends", hx(ctor, runtime, ipfsBlobB, args, args, args), true, NoMatch, ""},
		{"input shorter than the init code", hx(ctor, runtime, ipfsBlobB), true, NoMatch, ""},
		{"code differs", hx(ctor, codeA, codeC, ipfsBlobB, ipfsBlobB, args), true, NoMatch, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mt, got := MatchCreation(bc(compiled), mustHex(t, c.input), c.cbor)
			if mt != c.want {
				t.Fatalf("%v, want %v", mt, c.want)
			}
			if mt != NoMatch && !bytes.Equal(got, mustHex(t, c.wantArgs)) {
				t.Fatalf("args %x, want %s", got, c.wantArgs)
			}
		})
	}
}

func TestMinMatch(t *testing.T) {
	if minMatch(Full, Partial) != Partial || minMatch(Partial, Full) != Partial || minMatch(Full, Full) != Full {
		t.Fatal("minMatch")
	}
	if Full.String() != "full" || Partial.String() != "partial" || NoMatch.String() != "none" {
		t.Fatal("String")
	}
}

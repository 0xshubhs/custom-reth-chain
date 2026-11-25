// Package sigs maps function selectors, event topics and error selectors to
// their text signatures, like 4byte.directory, without asking anyone at
// runtime. Two sources: a list compiled into the binary (bundled.txt: every
// signature of our own contracts plus the common standards, written by
// `go run ./internal/sigs/gen`), and the ABIs of verified contracts.
//
// Signatures are canonical: `transfer(address,uint256)`, tuples spelled out
// as `(...)`, no parameter names, no `indexed`.
package sigs

import (
	"bufio"
	_ "embed"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/crypto"
)

// Kind is what a signature names.
type Kind int

const (
	Function Kind = iota
	Event
	Error
)

var kindNames = [...]string{"function", "event", "error"}

func (k Kind) String() string { return kindNames[k] }

// KeyOf is the lookup key of a canonical signature: the 4-byte selector for
// functions and errors, the 32-byte topic for events (0x lowercase hex).
func KeyOf(k Kind, sig string) string {
	h := crypto.Keccak256([]byte(sig))
	if k == Event {
		return fmt.Sprintf("0x%x", h)
	}
	return fmt.Sprintf("0x%x", h[:4])
}

// Index is a signature lookup table. Safe for concurrent reads once built.
type Index struct {
	m [3]map[string][]string
}

// NewIndex returns an empty index.
func NewIndex() *Index {
	return &Index{m: [3]map[string][]string{{}, {}, {}}}
}

// Add records a canonical signature.
func (ix *Index) Add(k Kind, sig string) {
	key := KeyOf(k, sig)
	for _, s := range ix.m[k][key] {
		if s == sig {
			return
		}
	}
	ix.m[k][key] = append(ix.m[k][key], sig)
	sort.Strings(ix.m[k][key])
}

// AddABI records every function, event and error of a JSON ABI.
func (ix *Index) AddABI(raw []byte) error {
	a, err := abi.JSON(strings.NewReader(string(raw)))
	if err != nil {
		return err
	}
	for _, m := range a.Methods {
		ix.Add(Function, m.Sig)
	}
	for _, e := range a.Events {
		if !e.Anonymous {
			ix.Add(Event, e.Sig)
		}
	}
	for _, e := range a.Errors {
		ix.Add(Error, e.Sig)
	}
	return nil
}

// Merge adds everything of other.
func (ix *Index) Merge(other *Index) {
	for k := range other.m {
		for _, list := range other.m[k] {
			for _, s := range list {
				ix.Add(Kind(k), s)
			}
		}
	}
}

// Lookup returns the signatures known for key (nil when none).
func (ix *Index) Lookup(k Kind, key string) []string {
	return ix.m[k][strings.ToLower(key)]
}

// Len is the number of signatures of kind k.
func (ix *Index) Len(k Kind) int {
	n := 0
	for _, l := range ix.m[k] {
		n += len(l)
	}
	return n
}

// Clone copies the index (so a merged one can be built on top of Bundled).
func (ix *Index) Clone() *Index {
	out := NewIndex()
	out.Merge(ix)
	return out
}

//go:embed bundled.txt
var bundledText string

var (
	bundledOnce sync.Once
	bundled     *Index
	bundledErr  error
)

// Bundled is the compiled-in list. It panics on a malformed bundled.txt,
// which the tests (and the generator) rule out.
func Bundled() *Index {
	bundledOnce.Do(func() {
		bundled, bundledErr = ParseList(bundledText)
	})
	if bundledErr != nil {
		panic("sigs: bundled.txt: " + bundledErr.Error())
	}
	return bundled
}

// ParseList reads lines of `function|event|error <signature>`; # starts a
// comment. Every signature is checked to be canonical.
func ParseList(text string) (*Index, error) {
	ix := NewIndex()
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		l := strings.TrimSpace(sc.Text())
		if i := strings.IndexByte(l, '#'); i >= 0 {
			l = strings.TrimSpace(l[:i])
		}
		if l == "" {
			continue
		}
		kindWord, sig, ok := strings.Cut(l, " ")
		if !ok {
			return nil, fmt.Errorf("line %d: want `<kind> <signature>`: %q", line, l)
		}
		k, err := parseKind(kindWord)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		canon, err := Canonical(strings.TrimSpace(sig))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		ix.Add(k, canon)
	}
	return ix, sc.Err()
}

func parseKind(s string) (Kind, error) {
	for i, n := range kindNames {
		if s == n {
			return Kind(i), nil
		}
	}
	return 0, fmt.Errorf("unknown kind %q", s)
}

// Canonical parses `name(type,...)` and returns it in canonical form
// (`uint` becomes `uint256`, spaces and parameter names are dropped). It
// fails on anything that is not a valid ABI signature.
func Canonical(sig string) (string, error) {
	name, args, err := Parse(sig)
	if err != nil {
		return "", err
	}
	types := make([]string, len(args))
	for i, a := range args {
		types[i] = a.Type.String()
	}
	return name + "(" + strings.Join(types, ",") + ")", nil
}

// Parse splits a signature into its name and argument types. Parameter
// names, if present (`transfer(address to, uint256 amount)`), are ignored.
func Parse(sig string) (string, abi.Arguments, error) {
	sig = strings.TrimSpace(sig)
	open := strings.IndexByte(sig, '(')
	if open <= 0 || !strings.HasSuffix(sig, ")") {
		return "", nil, fmt.Errorf("not a signature: %q", sig)
	}
	name := sig[:open]
	if !isIdent(name) {
		return "", nil, fmt.Errorf("bad name in %q", sig)
	}
	ms, err := parseTypeList(sig[open+1 : len(sig)-1])
	if err != nil {
		return "", nil, fmt.Errorf("%q: %w", sig, err)
	}
	args := make(abi.Arguments, len(ms))
	for i, m := range ms {
		t, err := abi.NewType(m.Type, "", m.Components)
		if err != nil {
			return "", nil, fmt.Errorf("%q: %w", sig, err)
		}
		args[i] = abi.Argument{Name: fmt.Sprintf("arg%d", i), Type: t}
	}
	return name, args, nil
}

// MethodFromSignature builds an abi.Method (unnamed parameters) for decoding
// call data when only the signature is known.
func MethodFromSignature(sig string) (*abi.Method, error) {
	name, args, err := Parse(sig)
	if err != nil {
		return nil, err
	}
	for i := range args {
		args[i].Name = "" // the signature names no parameters
	}
	m := abi.NewMethod(name, name, abi.Function, "", false, false, args, nil)
	return &m, nil
}

func isIdent(s string) bool {
	for i, c := range s {
		if !(c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return s != ""
}

// parseTypeList parses a comma-separated type list, tuples included.
func parseTypeList(s string) ([]abi.ArgumentMarshaling, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var parts []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unbalanced parentheses")
			}
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unbalanced parentheses")
	}
	parts = append(parts, s[start:])
	out := make([]abi.ArgumentMarshaling, len(parts))
	for i, p := range parts {
		m, err := parseType(strings.TrimSpace(p), i)
		if err != nil {
			return nil, err
		}
		out[i] = m
	}
	return out, nil
}

func parseType(p string, i int) (abi.ArgumentMarshaling, error) {
	name := fmt.Sprintf("c%d", i)
	if strings.HasPrefix(p, "(") {
		end := matchParen(p)
		if end < 0 {
			return abi.ArgumentMarshaling{}, fmt.Errorf("unbalanced tuple in %q", p)
		}
		comps, err := parseTypeList(p[1:end])
		if err != nil {
			return abi.ArgumentMarshaling{}, err
		}
		rest := strings.TrimSpace(p[end+1:])
		suffix, _, _ := strings.Cut(rest, " ") // array suffix, then an optional parameter name
		if suffix != "" && !strings.HasPrefix(suffix, "[") {
			suffix = ""
		}
		return abi.ArgumentMarshaling{Name: name, Type: "tuple" + suffix, Components: comps}, nil
	}
	typ, _, _ := strings.Cut(p, " ") // drop a parameter name or `indexed`
	if typ == "" {
		return abi.ArgumentMarshaling{}, fmt.Errorf("empty type")
	}
	typ, err := normalizeElementary(typ)
	if err != nil {
		return abi.ArgumentMarshaling{}, err
	}
	return abi.ArgumentMarshaling{Name: name, Type: typ}, nil
}

// normalizeElementary spells `uint`/`int` as their 256-bit forms and refuses
// integer widths Solidity does not have (go-ethereum accepts `uint7`).
func normalizeElementary(typ string) (string, error) {
	base, suffix := typ, ""
	if i := strings.IndexByte(typ, '['); i >= 0 {
		base, suffix = typ[:i], typ[i:]
	}
	for _, p := range []string{"uint", "int"} {
		if !strings.HasPrefix(base, p) {
			continue
		}
		bits := base[len(p):]
		if bits == "" {
			return p + "256" + suffix, nil
		}
		n := 0
		for _, c := range bits {
			if c < '0' || c > '9' {
				return "", fmt.Errorf("bad type %q", typ)
			}
			n = n*10 + int(c-'0')
		}
		if n < 8 || n > 256 || n%8 != 0 {
			return "", fmt.Errorf("bad integer width in %q", typ)
		}
		return typ, nil
	}
	return typ, nil
}

func matchParen(s string) int {
	depth := 0
	for i, c := range s {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// Response renders lookups for keys as {"key": [signatures]}; unknown keys
// are absent.
func (ix *Index) Response(k Kind, keys []string) map[string][]string {
	out := map[string][]string{}
	for _, key := range keys {
		if l := ix.Lookup(k, key); len(l) > 0 {
			out[strings.ToLower(key)] = append([]string(nil), l...)
		}
	}
	return out
}

// MarshalList renders the index in bundled.txt form (sorted, stable).
func (ix *Index) MarshalList() string {
	var lines []string
	for k := range ix.m {
		for _, l := range ix.m[k] {
			for _, s := range l {
				lines = append(lines, kindNames[k]+" "+s)
			}
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n"
}

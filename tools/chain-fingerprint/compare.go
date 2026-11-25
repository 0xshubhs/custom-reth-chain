package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
)

// fingerprintFile is a parsed, verified fingerprint.
type fingerprintFile struct {
	Path   string
	Header Header
	Blocks map[uint64]Block
	// First and Last block numbers present (contiguous; checked on load).
	First, Last uint64
	Count       int
}

// loadFingerprint reads and checks a file: header first, footer last, block
// lines contiguous and in order, and the footer's line hash matching. A file
// cut short (a dump that died, a copy that did not finish) fails here, so
// compare can never pass on half a file.
func loadFingerprint(path string) (*fingerprintFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseFingerprint(path, f)
}

func parseFingerprint(path string, r io.Reader) (*fingerprintFile, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	ff := &fingerprintFile{Path: path, Blocks: make(map[uint64]Block)}
	h := sha256.New()
	var footer *Footer
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if footer != nil {
			return nil, fmt.Errorf("%s:%d: data after the footer", path, lineNo)
		}
		var kind struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(line, &kind); err != nil {
			return nil, fmt.Errorf("%s:%d: not JSON: %v", path, lineNo, err)
		}
		switch kind.Kind {
		case "header":
			if lineNo != 1 {
				return nil, fmt.Errorf("%s:%d: header is not the first line", path, lineNo)
			}
			if err := json.Unmarshal(line, &ff.Header); err != nil {
				return nil, fmt.Errorf("%s:%d: %v", path, lineNo, err)
			}
			if ff.Header.Format != formatVersion {
				return nil, fmt.Errorf("%s: format %q, this tool reads %q", path, ff.Header.Format, formatVersion)
			}
		case "block":
			if ff.Header.Kind == "" {
				return nil, fmt.Errorf("%s: no header line", path)
			}
			var b Block
			if err := json.Unmarshal(line, &b); err != nil {
				return nil, fmt.Errorf("%s:%d: %v", path, lineNo, err)
			}
			if ff.Count == 0 {
				ff.First = b.Number
			} else if b.Number != ff.Last+1 {
				return nil, fmt.Errorf("%s:%d: block %d follows block %d", path, lineNo, b.Number, ff.Last)
			}
			ff.Last = b.Number
			ff.Count++
			ff.Blocks[b.Number] = b
			h.Write(line)
			h.Write([]byte{'\n'})
		case "footer":
			var ft Footer
			if err := json.Unmarshal(line, &ft); err != nil {
				return nil, fmt.Errorf("%s:%d: %v", path, lineNo, err)
			}
			footer = &ft
		default:
			return nil, fmt.Errorf("%s:%d: unknown line kind %q", path, lineNo, kind.Kind)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if ff.Header.Kind == "" {
		return nil, fmt.Errorf("%s: empty or no header", path)
	}
	if footer == nil {
		return nil, fmt.Errorf("%s: no footer line: the file is incomplete (dump interrupted or copy cut short)", path)
	}
	if footer.Blocks != ff.Count {
		return nil, fmt.Errorf("%s: footer says %d blocks, file has %d", path, footer.Blocks, ff.Count)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != footer.LinesSHA256 {
		return nil, fmt.Errorf("%s: block lines do not match the footer checksum (file edited or damaged)", path)
	}
	if ff.Count == 0 {
		return nil, fmt.Errorf("%s: no blocks", path)
	}
	if ff.First != ff.Header.From || ff.Last != ff.Header.To {
		return nil, fmt.Errorf("%s: header says blocks %d..%d, file has %d..%d", path, ff.Header.From, ff.Header.To, ff.First, ff.Last)
	}
	return ff, nil
}

type compareResult struct {
	Identical bool
	Message   string // first difference, or the summary when identical
}

// compareFingerprints checks that every block of A is identical in B. B may
// cover more blocks than A only when allowLonger is set.
func compareFingerprints(a, b *fingerprintFile, allowLonger bool) compareResult {
	if a.Header.ChainID != b.Header.ChainID {
		return compareResult{false, fmt.Sprintf("chain id differs: A=%d B=%d", a.Header.ChainID, b.Header.ChainID)}
	}
	if a.Header.GenesisHash != b.Header.GenesisHash {
		return compareResult{false, fmt.Sprintf("genesis hash differs: A=%s B=%s (a different chain, or genesis flags differ)", a.Header.GenesisHash, b.Header.GenesisHash)}
	}
	if b.First > a.First {
		return compareResult{false, fmt.Sprintf("B starts at block %d, A at %d: B is missing blocks %d..%d", b.First, a.First, a.First, b.First-1)}
	}
	for n := a.First; n <= a.Last; n++ {
		ab := a.Blocks[n]
		bb, ok := b.Blocks[n]
		if !ok {
			return compareResult{false, fmt.Sprintf("block %d: in A, missing from B (B ends at %d; A ends at %d, %d block(s) short)", n, b.Last, a.Last, a.Last-b.Last)}
		}
		if field, av, bv, diff := firstFieldDiff(ab, bb); diff {
			return compareResult{false, fmt.Sprintf("block %d: %s differs\n  A: %s\n  B: %s", n, field, av, bv)}
		}
		if n == a.Last {
			break
		}
	}
	extraBefore := a.First - b.First
	extraAfter := uint64(0)
	if b.Last > a.Last {
		extraAfter = b.Last - a.Last
	}
	if (extraBefore > 0 || extraAfter > 0) && !allowLonger {
		return compareResult{false, fmt.Sprintf("B has %d block(s) A does not (blocks %d..%d of B are outside A's %d..%d); pass --allow-longer if that is expected",
			extraBefore+extraAfter, b.First, b.Last, a.First, a.Last)}
	}
	msg := fmt.Sprintf("IDENTICAL: %d blocks (%d..%d), chain id %d, genesis %s", a.Count, a.First, a.Last, a.Header.ChainID, a.Header.GenesisHash)
	if extraBefore > 0 || extraAfter > 0 {
		msg += fmt.Sprintf("; B also has %d block(s) outside that range (allowed)", extraBefore+extraAfter)
	}
	return compareResult{true, msg}
}

// firstFieldDiff compares two blocks field by field, in declaration order.
func firstFieldDiff(a, b Block) (field, av, bv string, diff bool) {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	t := va.Type()
	for i := 0; i < t.NumField(); i++ {
		x, y := va.Field(i).Interface(), vb.Field(i).Interface()
		if !reflect.DeepEqual(x, y) {
			name := t.Field(i).Tag.Get("json")
			return name, show(x), show(y), true
		}
	}
	return "", "", "", false
}

func show(v any) string {
	switch p := v.(type) {
	case *string:
		if p == nil {
			return "null"
		}
		return *p
	}
	return fmt.Sprint(v)
}

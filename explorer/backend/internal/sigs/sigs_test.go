package sigs

import (
	"os"
	"strings"
	"testing"
)

func TestKeyOf(t *testing.T) {
	cases := []struct {
		k    Kind
		sig  string
		want string
	}{
		{Function, "transfer(address,uint256)", "0xa9059cbb"},
		{Error, "Error(string)", "0x08c379a0"},
		{Error, "Panic(uint256)", "0x4e487b71"},
		{Event, "Transfer(address,address,uint256)", "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"},
	}
	for _, c := range cases {
		if got := KeyOf(c.k, c.sig); got != c.want {
			t.Errorf("%s: %s, want %s", c.sig, got, c.want)
		}
	}
}

func TestCanonical(t *testing.T) {
	cases := map[string]string{
		"transfer(address,uint256)":                      "transfer(address,uint256)",
		"transfer(address to, uint256 amount)":           "transfer(address,uint256)",
		"f(uint,int)":                                    "f(uint256,int256)",
		"Transfer(address indexed from,address,uint256)": "Transfer(address,address,uint256)",
		"aggregate3((address,bool,bytes)[])":             "aggregate3((address,bool,bytes)[])",
		"g((uint256,(address,bytes32[2]))[],bytes)":      "g((uint256,(address,bytes32[2]))[],bytes)",
		"h()": "h()",
	}
	for in, want := range cases {
		got, err := Canonical(in)
		if err != nil || got != want {
			t.Errorf("Canonical(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "nope", "f(", "f(uint7)", "(address)", "f((address)", "1f()", "f(addres)"} {
		if got, err := Canonical(bad); err == nil {
			t.Errorf("Canonical(%q) accepted as %q", bad, got)
		}
	}
}

func TestIndexAndABI(t *testing.T) {
	ix := NewIndex()
	err := ix.AddABI([]byte(`[
	 {"type":"function","name":"setDeal","inputs":[{"name":"d","type":"tuple","components":[{"name":"id","type":"uint64"},{"name":"who","type":"address"}]}]},
	 {"type":"event","name":"Noted","anonymous":false,"inputs":[{"name":"k","type":"string","indexed":true}]},
	 {"type":"event","name":"Quiet","anonymous":true,"inputs":[]},
	 {"type":"error","name":"Nope","inputs":[{"name":"x","type":"uint256"}]}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.Lookup(Function, KeyOf(Function, "setDeal((uint64,address))")); len(got) != 1 || got[0] != "setDeal((uint64,address))" {
		t.Errorf("function %v", got)
	}
	if got := ix.Lookup(Event, KeyOf(Event, "Noted(string)")); len(got) != 1 {
		t.Errorf("event %v", got)
	}
	if ix.Len(Event) != 1 {
		t.Errorf("anonymous event indexed")
	}
	if got := ix.Lookup(Error, KeyOf(Error, "Nope(uint256)")); len(got) != 1 {
		t.Errorf("error %v", got)
	}
	// Two signatures on one selector are both kept, sorted.
	ix.Add(Function, "transfer(address,uint256)")
	ix.Add(Function, "transfer(address,uint256)")
	if n := len(ix.Lookup(Function, "0xa9059cbb")); n != 1 {
		t.Errorf("duplicate kept: %d", n)
	}
	resp := ix.Response(Function, []string{"0xA9059CBB", "0xdeadbeef"})
	if len(resp) != 1 || resp["0xa9059cbb"][0] != "transfer(address,uint256)" {
		t.Errorf("response %v", resp)
	}
}

func TestBundled(t *testing.T) {
	b := Bundled()
	for _, c := range []struct {
		k   Kind
		sig string
	}{
		{Function, "transfer(address,uint256)"},
		{Function, "aggregate3((address,bool,bytes)[])"},
		{Function, "execTransaction(address,uint256,bytes,uint8,uint256,uint256,uint256,address,address,bytes)"},
		{Function, "upgradeToAndCall(address,bytes)"},
		{Function, "execute(bytes32,bytes)"},
		{Function, "forcedTransfer(address,address,uint256)"},
		{Function, "handleOps((address,uint256,bytes,bytes,bytes32,uint256,bytes32,bytes,bytes)[],address)"},
		{Function, "permit(address,address,uint256,uint256,uint8,bytes32,bytes32)"},
		{Event, "Transfer(address,address,uint256)"},
		{Event, "TransferSingle(address,address,address,uint256,uint256)"},
		{Event, "UserOperationEvent(bytes32,address,address,uint256,bool,uint256,uint256)"},
		{Error, "Error(string)"},
		{Error, "OwnableUnauthorizedAccount(address)"},
	} {
		found := false
		for _, s := range b.Lookup(c.k, KeyOf(c.k, c.sig)) {
			found = found || s == c.sig
		}
		if !found {
			t.Errorf("bundled list lacks %s %s", c.k, c.sig)
		}
	}
	m, err := MethodFromSignature("transfer(address,uint256)")
	if err != nil || m.Sig != "transfer(address,uint256)" || m.Inputs[0].Name != "" {
		t.Errorf("method %+v %v", m, err)
	}
}

// bundled.txt must be exactly what the generator writes: canonical, sorted,
// no duplicates. A hand edit fails here.
func TestBundledFileIsGenerated(t *testing.T) {
	raw, err := os.ReadFile("bundled.txt")
	if err != nil {
		t.Fatal(err)
	}
	var body []string
	for _, l := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(l, "#") {
			body = append(body, l)
		}
	}
	ix, err := ParseList(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.MarshalList(); got != strings.Join(body, "\n") {
		t.Error("bundled.txt is not in generated form: run `go run ./internal/sigs/gen`")
	}
	std, err := os.ReadFile("standards.txt")
	if err != nil {
		t.Fatal(err)
	}
	stdIx, err := ParseList(string(std))
	if err != nil {
		t.Fatalf("standards.txt: %v", err)
	}
	for k := range stdIx.m {
		for key, list := range stdIx.m[k] {
			for _, s := range list {
				ok := false
				for _, b := range ix.Lookup(Kind(k), key) {
					ok = ok || b == s
				}
				if !ok {
					t.Errorf("standards.txt has %s %s but bundled.txt does not: regenerate", Kind(k), s)
				}
			}
		}
	}
}

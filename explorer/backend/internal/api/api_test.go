package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/evm"
)

func TestEIP55(t *testing.T) {
	// Vectors from EIP-55.
	for _, want := range []string{
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
		"0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359",
		"0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB",
		"0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb",
	} {
		if got := EIP55(strings.ToLower(want)); got != want {
			t.Errorf("EIP55(%s) = %s", strings.ToLower(want), got)
		}
	}
	if got := EIP55("not-an-address"); got != "not-an-address" {
		t.Errorf("non-address changed: %s", got)
	}
}

func TestParsePageAndEnvelope(t *testing.T) {
	cases := []struct {
		q          string
		limit, off int64
		bad        bool
	}{
		{"", 25, 0, false},
		{"limit=100&offset=50", 100, 50, false},
		{"limit=1", 1, 0, false},
		{"limit=0", 0, 0, true},
		{"limit=101", 0, 0, true},
		{"limit=x", 0, 0, true},
		{"offset=-1", 0, 0, true},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/x?"+c.q, nil)
		p, err := ParsePage(r)
		if (err != nil) != c.bad {
			t.Errorf("%q: err %v", c.q, err)
			continue
		}
		if !c.bad && (p.Limit != c.limit || p.Offset != c.off) {
			t.Errorf("%q: %+v", c.q, p)
		}
	}
	// next_offset: set while more remain, null at the end, items never null.
	pg := NewPage([]int{1, 2}, 5, PageReq{Limit: 2, Offset: 0})
	if pg.NextOffset == nil || *pg.NextOffset != 2 {
		t.Errorf("first page next_offset %v", pg.NextOffset)
	}
	pg = NewPage([]int{5}, 5, PageReq{Limit: 2, Offset: 4})
	if pg.NextOffset != nil {
		t.Errorf("last page next_offset %v", *pg.NextOffset)
	}
	b, _ := json.Marshal(NewPage[int](nil, 0, PageReq{Limit: 25}))
	if string(b) != `{"items":[],"total":0,"next_offset":null}` {
		t.Errorf("empty page JSON %s", b)
	}
}

func TestDecodeBuiltins(t *testing.T) {
	d := NewDecoder(nil)
	ctx := context.Background()
	to := "0x00000000000000000000000000000000000000aa"
	// transfer(address,uint256)
	input := evm.Calldata(evm.Selector("transfer(address,uint256)"), evm.AddressWord(to), evm.UintWord(bigInt(1234)))
	di := d.DecodeInput(ctx, nil, input)
	if di == nil || di.Method != "transfer" || di.Signature != "transfer(address,uint256)" || len(di.Params) != 2 {
		t.Fatalf("decoded %+v", di)
	}
	if di.Params[0].Value != EIP55(to) || di.Params[1].Value != "1234" || di.Params[1].Type != "uint256" {
		t.Errorf("params %+v", di.Params)
	}
	if name := d.MethodName(ctx, nil, input[:10]); name != "transfer" {
		t.Errorf("method name %s", name)
	}
	if name := d.MethodName(ctx, nil, "0xdeadbeef"); name != "0xdeadbeef" {
		t.Errorf("unknown selector rendered %s", name)
	}
	// Overloaded burn: both resolve by selector.
	if name := d.MethodName(ctx, nil, evm.Calldata(evm.Selector("burn(address,uint256)"))); name != "burn" {
		t.Errorf("burn(address,uint256) -> %s", name)
	}

	from, recv := "0x00000000000000000000000000000000000000bb", to
	tp := func(s string) *string { return &s }
	t20 := []*string{tp(evm.TopicTransfer), tp(wordHex(evm.AddressWord(from))), tp(wordHex(evm.AddressWord(recv)))}
	dl := d.DecodeLog(ctx, nil, t20, wordHex(evm.UintWord(bigInt(77))))
	if dl == nil || dl.Event != "Transfer" || dl.Params[2].Name != "value" || dl.Params[2].Value != "77" || *dl.Params[0].Indexed != true || *dl.Params[2].Indexed != false {
		t.Fatalf("erc20 log %+v", dl)
	}
	// Same topic, 4 topics: the ERC-721 variant (tokenId indexed).
	t721 := append(t20, tp(wordHex(evm.UintWord(bigInt(9)))))
	dl = d.DecodeLog(ctx, nil, t721, "0x")
	if dl == nil || dl.Params[2].Name != "tokenId" || dl.Params[2].Value != "9" || !*dl.Params[2].Indexed {
		t.Fatalf("erc721 log %+v", dl)
	}
	// Unknown topic: nil.
	if dl := d.DecodeLog(ctx, nil, []*string{tp("0x" + strings.Repeat("1", 64))}, "0x"); dl != nil {
		t.Errorf("unknown event decoded: %+v", dl)
	}
}

func TestDecodeVerifiedABIFirst(t *testing.T) {
	// A verified ABI with a tuple and a dynamic indexed argument.
	const j = `[
	 {"type":"function","name":"setDeal","inputs":[{"name":"d","type":"tuple","components":[{"name":"id","type":"uint64"},{"name":"who","type":"address"},{"name":"tags","type":"bytes32[]"}]},{"name":"memo","type":"string"}]},
	 {"type":"event","name":"Noted","inputs":[{"name":"key","type":"string","indexed":true},{"name":"by","type":"address","indexed":true},{"name":"ok","type":"bool","indexed":false}]}
	]`
	a, err := abi.JSON(strings.NewReader(j))
	if err != nil {
		t.Fatal(err)
	}
	d := NewDecoder(nil)
	ctx := context.Background()
	who := "0x00000000000000000000000000000000000000cc"
	type deal struct {
		Id   uint64
		Who  [20]byte
		Tags [][32]byte
	}
	m := a.Methods["setDeal"]
	var w [20]byte
	copy(w[:], evm.Bytes(who))
	packed, err := m.Inputs.Pack(deal{Id: 5, Who: w, Tags: [][32]byte{{1}}}, "hello")
	if err != nil {
		t.Fatal(err)
	}
	input := "0x" + hexs(append(append([]byte{}, m.ID...), packed...))
	di := d.DecodeInput(ctx, []*abi.ABI{&a}, input)
	if di == nil || di.Method != "setDeal" {
		t.Fatalf("decoded %+v", di)
	}
	tuple, ok := di.Params[0].Value.(map[string]any)
	if !ok || tuple["id"] != "5" || tuple["who"] != EIP55(who) || di.Params[1].Value != "hello" {
		t.Errorf("tuple %#v, memo %#v", di.Params[0].Value, di.Params[1].Value)
	}
	tags, _ := tuple["tags"].([]any)
	if len(tags) != 1 || !strings.HasPrefix(tags[0].(string), "0x01") {
		t.Errorf("tags %#v", tuple["tags"])
	}

	ev := a.Events["Noted"]
	tp := func(s string) *string { return &s }
	keyHash := "0x" + strings.Repeat("ab", 32)
	topics := []*string{tp(ev.ID.Hex()), tp(keyHash), tp(wordHex(evm.AddressWord(who)))}
	data := wordHex(evm.UintWord(bigInt(1)))
	dl := d.DecodeLog(ctx, []*abi.ABI{&a}, topics, data)
	if dl == nil || dl.Event != "Noted" || dl.Params[0].Value != keyHash || dl.Params[1].Value != EIP55(who) || dl.Params[2].Value != true {
		t.Fatalf("log %+v", dl)
	}
}

func TestDecodeBundledFallback(t *testing.T) {
	d := NewDecoder(nil)
	ctx := context.Background()
	// forcedTransfer is in no verified ABI and not in the built-in token
	// table, only in the bundled list: named, decoded, parameters unnamed.
	from, to := "0x00000000000000000000000000000000000000a1", "0x00000000000000000000000000000000000000b2"
	input := evm.Calldata(evm.Selector("forcedTransfer(address,address,uint256)"), evm.AddressWord(from), evm.AddressWord(to), evm.UintWord(bigInt(5)))
	if name := d.MethodName(ctx, nil, input[:10]); name != "forcedTransfer" {
		t.Errorf("method name %q", name)
	}
	di := d.DecodeInput(ctx, nil, input)
	if di == nil || di.Signature != "forcedTransfer(address,address,uint256)" || len(di.Params) != 3 || di.Params[0].Name != "" || di.Params[1].Value != EIP55(to) || di.Params[2].Value != "5" {
		t.Errorf("decoded %+v", di)
	}
	if name := d.MethodName(ctx, nil, evm.Calldata(evm.Selector("aggregate3((address,bool,bytes)[])"))); name != "aggregate3" {
		t.Errorf("aggregate3 -> %q", name)
	}
}

func TestSharePct(t *testing.T) {
	if p := sharePct("25", bigInt(100)); p == nil || *p != 25 {
		t.Errorf("25/100 -> %v", p)
	}
	if p := sharePct("1", bigInt(3)); p == nil || *p != 33.3333 {
		t.Errorf("1/3 -> %v", *p)
	}
	if p := sharePct("1", nil); p != nil {
		t.Errorf("no denominator -> %v", *p)
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newLimiter(2, time.Minute)
	l.now = func() time.Time { return now }
	for i := 0; i < 2; i++ {
		if ok, _ := l.allow("ip"); !ok {
			t.Fatalf("hit %d refused", i)
		}
	}
	ok, wait := l.allow("ip")
	if ok || wait != time.Minute {
		t.Fatalf("third hit: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.allow("other"); !ok {
		t.Fatal("other key limited")
	}
	now = now.Add(time.Minute)
	if ok, _ := l.allow("ip"); !ok {
		t.Fatal("not reset after the window")
	}
}

func TestLikePatternEscapes(t *testing.T) {
	if got := likePattern(" A_b%c\\ "); got != `%a\_b\%c\\%` {
		t.Errorf("got %s", got)
	}
}

func TestRevertMessage(t *testing.T) {
	// Reason already in the message: kept as the node said it.
	if got := revertMessage(&chainrpc.Error{Code: 3, Message: "execution reverted: nope"}); got != "execution reverted: nope" {
		t.Errorf("got %q", got)
	}
	// Reason only in the data: Error(string) decoded.
	data := "0x08c379a0" + hexs(evm.UintWord(bigInt(32))) + hexs(evm.UintWord(bigInt(6))) + hexs([]byte("undone")) + strings.Repeat("00", 26)
	raw, _ := json.Marshal(data)
	if got := revertMessage(&chainrpc.Error{Code: 3, Message: "execution reverted", Data: raw}); got != "execution reverted: undone" {
		t.Errorf("got %q", got)
	}
	// A custom error stays the node's text.
	raw, _ = json.Marshal("0xe450d38c" + strings.Repeat("00", 96))
	if got := revertMessage(&chainrpc.Error{Code: 3, Message: "execution reverted", Data: raw}); got != "execution reverted" {
		t.Errorf("got %q", got)
	}
}

func bigInt(v int64) *bigIntT { return newBig(v) }

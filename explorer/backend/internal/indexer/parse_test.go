package indexer

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/evm"
)

func TestDecodeTransfersTokenTypes(t *testing.T) {
	tok := addr('c')
	a, b := addr('a'), addr('b')
	mk := func(sl simLog) logRow {
		lr := logRow{Address: sl.addr, Data: "0x" + sl.data, TxHash: hash('1'), LogIndex: 3}
		for i, tp := range sl.topics {
			tp := tp
			lr.Topics[i] = &tp
		}
		return lr
	}

	// 3 topics: ERC-20 with the amount in data.
	sl := transferLog(tok, a, b, 42)
	got := decodeTransfers(mk(sl), len(sl.topics))
	if len(got) != 1 || got[0].TokenType != TypeERC20 || got[0].Amount.Int64() != 42 || got[0].TokenID != nil || got[0].From != a || got[0].To != b {
		t.Fatalf("erc20: %+v", got)
	}

	// 4 topics: ERC-721 with the id indexed, no amount.
	sl = nftLog(tok, chainrpc.ZeroAddress, b, 7)
	got = decodeTransfers(mk(sl), len(sl.topics))
	if len(got) != 1 || got[0].TokenType != TypeERC721 || got[0].TokenID.Int64() != 7 || got[0].Amount != nil {
		t.Fatalf("erc721: %+v", got)
	}

	// TransferSingle: ERC-1155.
	sl = singleLog(tok, a, a, b, 9, 5)
	got = decodeTransfers(mk(sl), len(sl.topics))
	if len(got) != 1 || got[0].TokenType != TypeERC1155 || got[0].TokenID.Int64() != 9 || got[0].Amount.Int64() != 5 || *got[0].Operator != a {
		t.Fatalf("erc1155 single: %+v", got)
	}

	// TransferBatch: ids [1,2], values [10,20] -> two rows with batch_index.
	data := word(64) + word(160) + word(2) + word(1) + word(2) + word(2) + word(10) + word(20)
	sl = simLog{addr: tok, topics: []string{evm.TopicTransferBatch, topicAddr(a), topicAddr(a), topicAddr(b)}, data: data}
	got = decodeTransfers(mk(sl), len(sl.topics))
	if len(got) != 2 || got[0].BatchIndex != 0 || got[1].BatchIndex != 1 || got[1].TokenID.Int64() != 2 || got[1].Amount.Int64() != 20 {
		t.Fatalf("erc1155 batch: %+v", got)
	}

	// Malformed: Transfer with a non-address topic, or a short amount.
	bad := transferLog(tok, a, b, 1)
	bad.topics[1] = "0x" + strings.Repeat("f", 64)
	if got := decodeTransfers(mk(bad), 3); got != nil {
		t.Fatalf("non-address topic accepted: %+v", got)
	}
	short := transferLog(tok, a, b, 1)
	short.data = "00"
	if got := decodeTransfers(mk(short), 3); got != nil {
		t.Fatalf("short data accepted: %+v", got)
	}
	// Two topics: not a token transfer at all.
	two := simLog{addr: tok, topics: []string{evm.TopicTransfer, topicAddr(a)}, data: word(1)}
	if got := decodeTransfers(mk(two), 2); got != nil {
		t.Fatalf("2-topic transfer accepted: %+v", got)
	}
}

func TestFlattenTraceAndCreations(t *testing.T) {
	// root CALL -> [ CREATE ok, CALL (reverts) -> [ CREATE (inside a reverted frame) ] , STATICCALL ]
	root := chainrpc.CallFrame{Type: "CALL", From: addr('a'), To: addr('f'), Calls: []chainrpc.CallFrame{
		{Type: "CREATE", From: addr('f'), To: addr('1'), Input: "0x6080"},
		{Type: "CALL", From: addr('f'), To: addr('2'), Error: "execution reverted", Calls: []chainrpc.CallFrame{
			{Type: "CREATE2", From: addr('2'), To: addr('3')},
		}},
		{Type: "STATICCALL", From: addr('f'), To: addr('4')},
	}}
	_ = json.Valid
	frames := flattenTrace(hash('9'), 0, &root, true)
	want := []struct {
		ta    string
		depth int
		typ   string
		ok    bool
	}{{"", 0, "CALL", true}, {"0", 1, "CREATE", true}, {"1", 1, "CALL", false}, {"1.0", 2, "CREATE2", false}, {"2", 1, "STATICCALL", true}}
	if len(frames) != len(want) {
		t.Fatalf("got %d frames", len(frames))
	}
	for i, w := range want {
		f := frames[i]
		if f.TraceAddress != w.ta || f.Depth != w.depth || f.CallType != w.typ || f.effectiveOK != w.ok {
			t.Errorf("frame %d: %+v, want %+v", i, f, w)
		}
	}
	if frames[1].Created == nil || *frames[1].Created != addr('1') {
		t.Errorf("successful CREATE must record the contract")
	}
	if frames[3].Created != nil {
		t.Errorf("CREATE2 under a reverted frame must not record a contract")
	}

	// Whole tx failed: nothing is created.
	frames = flattenTrace(hash('9'), 0, &root, false)
	if frames[1].Created != nil {
		t.Errorf("CREATE in a failed tx recorded a contract")
	}
}

func TestParseBlockSimChain(t *testing.T) {
	c := newSimChain()
	A, B, M, W := addr('a'), addr('b'), addr('e'), addr('d')
	C := addr('c')
	c.add(simBlock{n: 0, hash: hash('a'), miner: M}, map[string]int64{A: 1000})
	c.add(simBlock{n: 1, hash: hash('b'), miner: M, withdrawals: []string{W}, txs: []simTx{
		{hash: hash('1'), from: A, created: C, nonce: 0, input: "0x6080",
			logs: []simLog{transferLog(C, chainrpc.ZeroAddress, A, 100)}},
		{hash: hash('2'), from: A, to: B, value: 10, nonce: 1},
		{hash: hash('3'), from: A, to: C, nonce: 2, failed: true, input: "0xa9059cbb"},
	}}, nil)
	raw, rcs, trs := c.nodeJSON(1)
	pb, err := parseBlock(raw, rcs, trs)
	if err != nil {
		t.Fatal(err)
	}
	if pb.Number != 1 || pb.ParentHash != hash('a') || len(pb.Txs) != 3 {
		t.Fatalf("header: %+v", pb)
	}
	if pb.Txs[0].Created == nil || *pb.Txs[0].Created != C || pb.Txs[0].MethodID != nil {
		t.Errorf("creation tx: %+v", pb.Txs[0])
	}
	if pb.Txs[2].Error == nil || *pb.Txs[2].Error != "nope" || *pb.Txs[2].Status != 0 || *pb.Txs[2].MethodID != "0xa9059cbb" {
		t.Errorf("failed tx: %+v", pb.Txs[2])
	}
	if s := pb.Seen[A]; s == nil || s.TxCount != 3 || *s.Nonce != 3 {
		t.Errorf("sender bookkeeping: %+v", s)
	}
	for _, a := range []string{A, B, C, M, W, chainrpc.ZeroAddress} {
		if pb.Seen[a] == nil {
			t.Errorf("address %s not seen", a)
		}
	}
	if pb.TokenTypes[C] != TypeERC20 || pb.TokenCounts[C] != 1 || len(pb.Transfers) != 1 {
		t.Errorf("token detection: %v %v", pb.TokenTypes, pb.TokenCounts)
	}
	if len(pb.Creations) != 1 || pb.Creations[0].Address != C || pb.Creations[0].Creator != A {
		t.Errorf("creations: %+v", pb.Creations)
	}
	// raw header keeps the node JSON with hashes instead of tx objects
	var hdr map[string]any
	if err := json.Unmarshal(pb.HeaderRaw, &hdr); err != nil {
		t.Fatal(err)
	}
	if txs, _ := hdr["transactions"].([]any); len(txs) != 3 || txs[0] != hash('1') {
		t.Errorf("header transactions: %v", hdr["transactions"])
	}
	if _, ok := hdr["withdrawals"]; !ok {
		t.Errorf("raw header lost withdrawals")
	}

	// A receipt for the wrong tx is refused.
	rcs[0], rcs[1] = rcs[1], rcs[0]
	if _, err := parseBlock(raw, rcs, trs); !errors.Is(err, errInconsistent) {
		t.Errorf("swapped receipts: %v", err)
	}
}

func TestSanitizeJSON(t *testing.T) {
	in := []byte(`{"a":"x\u0000y","b":"lit\\u0000eral"}`)
	out := string(sanitizeJSON(in))
	if out != `{"a":"x�y","b":"lit\\u0000eral"}` {
		t.Fatalf("got %s", out)
	}
	if !json.Valid([]byte(out)) {
		t.Fatal("invalid JSON")
	}
}

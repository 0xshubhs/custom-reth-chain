//go:build integration

package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"custom-reth-chain/explorer/backend/internal/admin"
	"custom-reth-chain/explorer/backend/internal/api"
	"custom-reth-chain/explorer/backend/internal/chainrpc"
	"custom-reth-chain/explorer/backend/internal/config"
	"custom-reth-chain/explorer/backend/internal/evm"
	"custom-reth-chain/explorer/backend/internal/sigs"
	"custom-reth-chain/explorer/backend/internal/testpg"
)

func TestAnvilEndToEnd(t *testing.T) {
	for _, bin := range []string{"anvil", "forge", "cast", "curl"} {
		if _, err := run(t, "which", bin); err != nil {
			t.Skipf("%s not on PATH", bin)
		}
	}
	pool, dbURL := testpg.New(t)
	art, src := buildContracts(t)
	rpcURL, wsURL := startAnvil(t)
	h := &harness{
		t: t, ctx: context.Background(), rpcURL: rpcURL, wsURL: wsURL, dbURL: dbURL, pool: pool, art: art, srcDir: src,
		rpc: chainrpc.New(rpcURL, chainrpc.Options{Retries: 1}),
		log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}

	// ---- 1. push indexing through newHeads (safety check off, no hook) ----
	ix := h.startIndexer(wsURL, 0)
	h.waitIndexed("genesis")
	erc20 := h.deploy("TestToken", "constructor(string,string,uint256)", "Land One", "LND1", "1000000")
	h.waitIndexed("ERC-20 deployment (push)")
	erc20b := h.deploy("TestToken", "constructor(string,string,uint256)", "Land One", "LND1", "1000000") // same runtime code
	nft := h.deploy("TestNFT", "")
	multi := h.deploy("TestMulti", "")
	factory := h.deploy("Factory", "")
	h.send(key0, erc20.ContractAddress, "transfer(address,uint256)", acct1, "250000")
	h.send(key1, erc20.ContractAddress, "transfer(address,uint256)", acct2, "50000")
	h.send(key0, nft.ContractAddress, "mint(address,uint256)", acct1, "7")
	h.send(key0, nft.ContractAddress, "mint(address,uint256)", acct1, "8")
	h.send(key1, nft.ContractAddress, "transferFrom(address,address,uint256)", acct1, acct2, "7")
	h.send(key1, nft.ContractAddress, "burn(uint256)", "8") // burned: ownerOf reverts, no row may remain
	h.waitIndexed("token traffic")
	t.Log("push indexing: ok")

	// Two indexers on one database: the writer lock lets only one catch up
	// at a time, so both being pushed the same heads still gives one
	// contiguous, faithful copy.
	ix2 := h.startIndexer(wsURL, 0)
	h.send(key0, multi.ContractAddress, "mint(address,uint256,uint256)", acct1, "1", "10")
	h.send(key0, multi.ContractAddress, "mintBatch(address,uint256[],uint256[])", acct2, "[1,2]", "[3,4]")
	h.send(key1, multi.ContractAddress, "safeTransferFrom(address,address,uint256,uint256,bytes)", acct1, acct3, "1", "4", "0x")
	mk := h.sendValue(key0, factory.ContractAddress, "1000", "make(string,uint256)", "Child", "500")
	h.waitIndexed("1155 + factory, two indexers")
	ix2.stop()
	h.assertContiguous()
	if e, _ := ix.health(t)["last_error"].(string); e != "" {
		t.Errorf("indexer error with a second writer around: %s", e)
	}

	// ---- 2. internal txs and contract creation ----
	var kid, sink string
	if err := pool.QueryRow(h.ctx, `SELECT created_contract FROM internal_txs WHERE tx_hash = $1 AND trace_address = '0'`, mk.TransactionHash).Scan(&kid); err != nil {
		t.Fatalf("child token CREATE frame: %v", err)
	}
	var creator, ctrace, ctx string
	if err := pool.QueryRow(h.ctx, `SELECT creator, creation_trace_address, creation_tx FROM addresses WHERE address = $1 AND is_contract`, kid).Scan(&creator, &ctrace, &ctx); err != nil {
		t.Fatalf("child contract row: %v", err)
	}
	if creator != factory.ContractAddress || ctrace != "0" || ctx != mk.TransactionHash {
		t.Errorf("child creation: creator %s trace %q tx %s", creator, ctrace, ctx)
	}
	var sinkValue string
	if err := pool.QueryRow(h.ctx, `SELECT created_contract, value::text FROM internal_txs WHERE tx_hash = $1 AND call_type = 'CREATE' AND value > 0`, mk.TransactionHash).Scan(&sink, &sinkValue); err != nil || sinkValue != "1000" {
		t.Fatalf("value-carrying CREATE: %v %s", err, sinkValue)
	}
	var sinkBal string
	_ = pool.QueryRow(h.ctx, `SELECT balance::text FROM addresses WHERE address = $1`, sink).Scan(&sinkBal)
	if sinkBal != "1000" {
		t.Errorf("sink native balance %s, want 1000 (internal value transfer)", sinkBal)
	}
	// The CREATE inside the reverted makeAndRevert() is stored as a frame but
	// is no contract.
	var doomedFrames, doomedContracts int
	_ = pool.QueryRow(h.ctx, `SELECT count(*) FROM internal_txs WHERE tx_hash = $1 AND call_type = 'CREATE' AND created_contract IS NULL`, mk.TransactionHash).Scan(&doomedFrames)
	_ = pool.QueryRow(h.ctx, `SELECT count(*) FROM addresses a JOIN internal_txs i ON i.to_addr = a.address
		WHERE i.tx_hash = $1 AND i.call_type = 'CREATE' AND i.created_contract IS NULL AND a.creation_tx IS NOT NULL`, mk.TransactionHash).Scan(&doomedContracts)
	if doomedFrames != 1 || doomedContracts != 0 {
		t.Errorf("reverted CREATE: %d frames without contract (want 1), %d recorded as contracts (want 0)", doomedFrames, doomedContracts)
	}
	var depthCount int
	_ = pool.QueryRow(h.ctx, `SELECT count(*) FROM internal_txs WHERE tx_hash = $1 AND depth >= 1`, mk.TransactionHash).Scan(&depthCount)
	t.Logf("internal txs: factory tx has %d frames below the root; child token %s, sink %s", depthCount, kid, sink)

	// ---- 3. token balances equal the chain ----
	h.checkBalances(erc20.ContractAddress, kid, nft.ContractAddress, multi.ContractAddress)
	var types string
	_ = pool.QueryRow(h.ctx, `SELECT string_agg(type, ',' ORDER BY type) FROM tokens WHERE address IN ($1, $2, $3)`, erc20.ContractAddress, nft.ContractAddress, multi.ContractAddress).Scan(&types)
	if types != "ERC-1155,ERC-20,ERC-721" {
		t.Errorf("token types %s", types)
	}
	t.Log("token balances: ok")

	// ---- 4. nothing skipped while the indexer was down ----
	ix.stop()
	before := h.cursor()
	h.send(key0, erc20.ContractAddress, "transfer(address,uint256)", acct3, "1")
	h.cast("rpc", "--rpc-url", rpcURL, "anvil_mine", "5")
	h.send(key0, erc20.ContractAddress, "transfer(address,uint256)", acct3, "2")
	if c := h.cursor(); c != before {
		t.Fatalf("cursor moved while stopped: %d -> %d", before, c)
	}
	ix = h.startIndexer(wsURL, 0)
	h.waitIndexed("restart catch-up")
	h.assertContiguous()
	t.Logf("restart: caught up %d -> %d with no gap", before, h.cursor())

	// ---- 5. webhook trigger (websocket dead, safety check off) ----
	ix.stop()
	hookIx := h.startIndexer("ws://127.0.0.1:9", 0)
	h.waitIndexed("hook indexer start-up")
	// Its own start-up catch-up is a trigger too: wait for it to finish, or it
	// could index the next block and pass for the webhook.
	for deadline := time.Now().Add(timeout); hookIx.health(t)["last_catch_up"] == nil; {
		if time.Now().After(deadline) {
			t.Fatal("hook indexer never finished its start-up catch-up")
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.send(key0, erc20.ContractAddress, "transfer(address,uint256)", acct3, "3")
	time.Sleep(700 * time.Millisecond)
	if c, hd := h.cursor(), h.head(); c == int64(hd) {
		t.Fatal("block indexed without any trigger (no websocket, no safety check)")
	}
	if code := hookIx.hook(t, "wrong"); code != http.StatusUnauthorized {
		t.Errorf("hook with a bad token answered %d", code)
	}
	if code := hookIx.hook(t, hookTok); code != http.StatusAccepted {
		t.Errorf("hook answered %d", code)
	}
	h.waitIndexed("webhook")
	t.Log("webhook trigger: ok")

	// ---- 6. API on the same database ----
	verifier := newFakeVerifier(t, h)
	apiSrv := h.startAPI(verifier.URL, hookIx.srv.URL+"/hooks/new-block")
	base := apiSrv.URL + "/api/v2"
	h.checkAPI(base, erc20.ContractAddress, kid, nft.ContractAddress, factory.ContractAddress, mk.TransactionHash)

	// Read-through: a tx the indexer has not seen (hook indexer, no trigger).
	rt := h.send(key0, erc20.ContractAddress, "transfer(address,uint256)", acct3, "4")
	var rtx map[string]any
	if code := getJSON(t, base+"/transactions/"+rt.TransactionHash, &rtx); code != 200 {
		t.Fatalf("read-through status %d", code)
	}
	if rtx["indexed"] != false || rtx["status"] != "ok" || rtx["method"] != "transfer" {
		t.Errorf("read-through tx: indexed=%v status=%v method=%v", rtx["indexed"], rtx["status"], rtx["method"])
	}
	h.waitIndexed("nudge from the API read-through") // the API posted the hook
	if getJSON(t, base+"/transactions/"+rt.TransactionHash, &rtx); rtx["indexed"] != true {
		t.Errorf("after the nudge the tx is still not indexed")
	}
	t.Log("API read-through + nudge: ok")

	// SSE: a new block arrives on the stream.
	data := readSSE(t, base+"/stream", func() {
		h.send(key0, erc20.ContractAddress, "transfer(address,uint256)", acct3, "5")
		hookIx.hook(t, hookTok)
	}, 15*time.Second)
	var bs map[string]any
	if err := json.Unmarshal([]byte(data), &bs); err != nil || bs["hash"] == nil {
		t.Errorf("stream data %s", data)
	}
	t.Logf("SSE block event: number %v", bs["number"])

	// ---- 7. verification, like scripts/verify-contracts.sh ----
	h.verify(base, apiSrv.URL, erc20.ContractAddress, erc20b.ContractAddress, kid)
	// A token created after the verification is adopted as similar at once.
	late := h.deploy("TestToken", "constructor(string,string,uint256)", "Late", "LATE", "5")
	hookIx.hook(t, hookTok)
	h.waitIndexed("late token")
	var mt string
	if err := pool.QueryRow(h.ctx, `SELECT match_type FROM verified_contracts WHERE address = $1`, late.ContractAddress).Scan(&mt); err != nil || mt != "similar" {
		t.Errorf("token created after verification: %q %v", mt, err)
	}
	t.Log("verification + similar fan-out: ok")

	// Signatures: bundled ones, and ones only a verified ABI has.
	fnKey := sigs.KeyOf(sigs.Function, "itestOnly(uint256,bytes32[])")
	var fns map[string][]string
	getJSON(t, base+"/signatures/functions?selectors=0xa9059cbb,"+fnKey+",0x12345678", &fns)
	if len(fns) != 2 || fns["0xa9059cbb"][0] != "transfer(address,uint256)" || fns[fnKey][0] != "itestOnly(uint256,bytes32[])" {
		t.Errorf("function signatures %v", fns)
	}
	errKey := sigs.KeyOf(sigs.Error, "ItestRefused(string)")
	var errs map[string][]string
	getJSON(t, base+"/signatures/errors?selectors="+errKey+",0x08c379a0", &errs)
	if errs[errKey][0] != "ItestRefused(string)" || errs["0x08c379a0"][0] != "Error(string)" {
		t.Errorf("error signatures %v", errs)
	}
	var evs map[string][]string
	getJSON(t, base+"/signatures/events?topics="+evm.TopicTransferSingle, &evs)
	if evs[evm.TopicTransferSingle][0] != "TransferSingle(address,address,address,uint256,uint256)" {
		t.Errorf("event signatures %v", evs)
	}
	// Chart: today's bar holds every transaction (anvil blocks carry the
	// wall-clock time), and every day of the window is present.
	var chart struct {
		Items []struct {
			Date         string `json:"date"`
			Transactions int64  `json:"transactions"`
		} `json:"items"`
	}
	getJSON(t, base+"/stats/charts/transactions?days=7", &chart)
	var txs int64
	_ = pool.QueryRow(h.ctx, `SELECT count(*) FROM transactions`).Scan(&txs)
	if len(chart.Items) != 7 || chart.Items[6].Date != time.Now().UTC().Format("2006-01-02") || chart.Items[6].Transactions != txs || chart.Items[0].Transactions != 0 {
		t.Errorf("chart %+v (db has %d transactions)", chart.Items, txs)
	}
	t.Log("signatures + chart: ok")

	// ---- 8. reorg ----
	hookIx.stop()
	ix = h.startIndexer(wsURL, 0)
	h.waitIndexed("before reorg")
	h.reorg(erc20.ContractAddress)

	// ---- 9. admin commands ----
	h.admin(ix)
}

func (h *harness) assertContiguous() {
	h.t.Helper()
	var cnt, maxN int64
	_ = h.pool.QueryRow(h.ctx, `SELECT count(*), max(number) FROM blocks`).Scan(&cnt, &maxN)
	if cnt != maxN+1 {
		h.t.Fatalf("blocks not contiguous: %d rows, max %d", cnt, maxN)
	}
	rows, err := h.pool.Query(h.ctx, `SELECT number, hash FROM blocks ORDER BY number`)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var nums []uint64
	stored := map[uint64]string{}
	for rows.Next() {
		var n int64
		var hs string
		_ = rows.Scan(&n, &hs)
		nums = append(nums, uint64(n))
		stored[uint64(n)] = hs
	}
	node, err := h.rpc.BlockHashes(h.ctx, nums)
	if err != nil {
		h.t.Fatal(err)
	}
	for i, n := range nums {
		if node[i] != stored[n] {
			h.t.Fatalf("block %d: stored %s, node %s", n, stored[n], node[i])
		}
	}
}

// checkBalances compares every stored token balance with a live call, and
// every holder that should exist with a stored row.
func (h *harness) checkBalances(erc20s ...string) {
	h.t.Helper()
	holders := []string{acct0, acct1, acct2, acct3}
	for _, tok := range erc20s {
		var typ string
		if err := h.pool.QueryRow(h.ctx, `SELECT type FROM tokens WHERE address = $1`, tok).Scan(&typ); err != nil {
			h.t.Fatalf("token %s: %v", tok, err)
		}
		switch typ {
		case "ERC-20":
			for _, a := range holders {
				chain := strings.Fields(h.call(tok, "balanceOf(address)(uint256)", a))[0]
				var db string
				err := h.pool.QueryRow(h.ctx, `SELECT balance::text FROM token_balances WHERE token = $1 AND holder = $2 AND token_id = -1`, tok, a).Scan(&db)
				if err != nil {
					db = "0"
				}
				if db != chain {
					h.t.Errorf("%s balanceOf(%s): db %s, chain %s", tok, a, db, chain)
				}
			}
		case "ERC-721":
			var owner string
			err := h.pool.QueryRow(h.ctx, `SELECT holder FROM token_balances WHERE token = $1 AND token_id = 7`, tok).Scan(&owner)
			chain := strings.ToLower(h.call(tok, "ownerOf(uint256)(address)", "7"))
			if err != nil || owner != chain {
				h.t.Errorf("%s ownerOf(7): db %s (%v), chain %s", tok, owner, err, chain)
			}
			var burned int
			_ = h.pool.QueryRow(h.ctx, `SELECT count(*) FROM token_balances WHERE token = $1 AND token_id = 8`, tok).Scan(&burned)
			if burned != 0 {
				h.t.Errorf("%s: burned id 8 still has an owner row", tok)
			}
		case "ERC-1155":
			for _, a := range holders {
				for _, id := range []string{"1", "2"} {
					chain := strings.Fields(h.call(tok, "balanceOf(address,uint256)(uint256)", a, id))[0]
					var db string
					if err := h.pool.QueryRow(h.ctx, `SELECT balance::text FROM token_balances WHERE token = $1 AND holder = $2 AND token_id = $3::numeric`, tok, a, id).Scan(&db); err != nil {
						db = "0"
					}
					if db != chain {
						h.t.Errorf("%s balanceOf(%s,%s): db %s, chain %s", tok, a, id, db, chain)
					}
				}
			}
		}
		// holders_count is the distinct holders with a row.
		var hc, rows int
		_ = h.pool.QueryRow(h.ctx, `SELECT holders_count, (SELECT count(DISTINCT holder) FROM token_balances WHERE token = $1) FROM tokens WHERE address = $1`, tok).Scan(&hc, &rows)
		if hc != rows {
			h.t.Errorf("%s holders_count %d, rows %d", tok, hc, rows)
		}
	}
}

func (h *harness) checkAPI(base, erc20, kid, nft, factory, makeTx string) {
	t := h.t
	var st map[string]any
	getJSON(t, base+"/stats", &st)
	if st["total_blocks"].(float64) != float64(h.cursor()+1) || st["chain_id"].(float64) != chainID {
		t.Errorf("stats %v", st)
	}
	var blk map[string]any
	getJSON(t, fmt.Sprintf("%s/blocks/%d", base, h.cursor()), &blk)
	if blk["confirmations"] != float64(1) {
		t.Errorf("newest block confirmations %v, want 1", blk["confirmations"])
	}
	var tok map[string]any
	getJSON(t, base+"/tokens/"+erc20, &tok)
	if tok["type"] != "ERC-20" || tok["symbol"] != "LND1" || tok["decimals"].(float64) != 18 || tok["address"] != api.EIP55(erc20) {
		t.Errorf("token %v", tok)
	}
	var holders struct {
		Items []map[string]any `json:"items"`
		Total float64          `json:"total"`
	}
	getJSON(t, base+"/tokens/"+erc20+"/holders", &holders)
	if holders.Total < 3 || holders.Items[0]["share_pct"] == nil {
		t.Errorf("holders %v", holders)
	}
	var page struct {
		Items      []map[string]any `json:"items"`
		Total      float64          `json:"total"`
		NextOffset *float64         `json:"next_offset"`
	}
	getJSON(t, base+"/transactions/"+makeTx+"/internal-transactions", &page)
	if page.Total < 4 {
		t.Errorf("factory tx internal txs: %v", page.Total)
	}
	foundCreate := false
	for _, it := range page.Items {
		if it["call_type"] == "CREATE" && it["created_contract"] != nil {
			foundCreate = true
		}
	}
	if !foundCreate {
		t.Errorf("no CREATE with created_contract in %v", page.Items)
	}
	getJSON(t, base+"/blocks?limit=2", &page)
	if len(page.Items) != 2 || page.NextOffset == nil || *page.NextOffset != 2 {
		t.Errorf("blocks page: %d items, next %v", len(page.Items), page.NextOffset)
	}
	// A transfer decoded with the built-in table (TestToken not verified yet).
	getJSON(t, base+"/addresses/"+acct1+"/transactions?limit=100", &page)
	var transferTx string
	for _, it := range page.Items {
		if it["method"] == "transfer" {
			transferTx = it["hash"].(string)
		}
	}
	if transferTx == "" {
		t.Fatalf("no transfer in %s's transactions", acct1)
	}
	var tx map[string]any
	getJSON(t, base+"/transactions/"+transferTx, &tx)
	di, _ := tx["decoded_input"].(map[string]any)
	if di == nil || di["signature"] != "transfer(address,uint256)" || tx["token_transfers_count"].(float64) != 1 {
		t.Errorf("tx %v", tx)
	}
	var logs struct {
		Items []map[string]any `json:"items"`
	}
	getJSON(t, base+"/transactions/"+transferTx+"/logs", &logs)
	if len(logs.Items) != 1 || logs.Items[0]["decoded"] == nil {
		t.Errorf("logs %v", logs.Items)
	}
	var addr map[string]any
	getJSON(t, base+"/addresses/"+kid, &addr)
	if addr["is_contract"] != true || addr["creator"] == nil || addr["token"] == nil {
		t.Errorf("child token address %v", addr)
	}
	getJSON(t, base+"/addresses/"+acct2+"/tokens", &page)
	if page.Total < 3 {
		t.Errorf("acct2 holdings %v", page.Total)
	}
	getJSON(t, base+"/tokens/"+nft+"/holders?token_id=7", &page)
	if page.Total != 1 || page.Items[0]["address"].(map[string]any)["hash"] != api.EIP55(acct2) {
		t.Errorf("nft 7 owner %v", page.Items)
	}
	var sr []map[string]any
	getJSON(t, base+"/search?q=LND1", &sr)
	if len(sr) == 0 || sr[0]["type"] != "token" {
		t.Errorf("search LND1 %v", sr)
	}
	getJSON(t, base+"/search?q="+factory, &sr)
	if len(sr) != 1 || sr[0]["type"] != "contract" {
		t.Errorf("search factory %v", sr)
	}
	// Never-seen address still answers.
	var fresh map[string]any
	if code := getJSON(t, base+"/addresses/0x00000000000000000000000000000000deadbeef", &fresh); code != 200 || fresh["balance"] != "0" {
		t.Errorf("unseen address %d %v", code, fresh)
	}
	// Read-contract call.
	body := fmt.Sprintf(`{"data":"%s"}`, evm.Calldata(evm.SelBalanceOf, evm.AddressWord(acct1)))
	resp, err := http.Post(base+"/smart-contracts/"+erc20+"/call", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var cr map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&cr)
	resp.Body.Close()
	if !strings.HasPrefix(cr["result"], "0x") || len(cr["result"]) != 66 {
		t.Errorf("call result %v", cr)
	}
	// A revert is 200 with the reason; bad input is 400 with a message.
	body = fmt.Sprintf(`{"data":"%s"}`, evm.Calldata(evm.Selector("makeAndRevert()")))
	resp, err = http.Post(base+"/smart-contracts/"+factory+"/call", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	cr = map[string]string{}
	_ = json.NewDecoder(resp.Body).Decode(&cr)
	resp.Body.Close()
	if resp.StatusCode != 200 || cr["error"] != "execution reverted: undone" {
		t.Errorf("revert: %d %v", resp.StatusCode, cr)
	}
	resp, _ = http.Post(base+"/smart-contracts/"+factory+"/call", "application/json", strings.NewReader(`{"data":"zz"}`))
	cr = map[string]string{}
	_ = json.NewDecoder(resp.Body).Decode(&cr)
	resp.Body.Close()
	if resp.StatusCode != 400 || cr["message"] == "" {
		t.Errorf("bad call input: %d %v", resp.StatusCode, cr)
	}
	// No verification, and not even code: 200 is_verified:false; only an
	// invalid address is 404.
	var scEOA map[string]any
	if code := getJSON(t, base+"/smart-contracts/"+acct3, &scEOA); code != 200 || scEOA["is_verified"] != false || scEOA["deployed_bytecode"] != nil {
		t.Errorf("smart-contract of an EOA: %d %v", code, scEOA)
	}
	if code := getJSON(t, base+"/smart-contracts/"+factory, &scEOA); code != 200 || scEOA["is_verified"] != false || scEOA["deployed_bytecode"] == nil {
		t.Errorf("unverified contract: %d %v", code, scEOA)
	}
	if code := getJSON(t, base+"/smart-contracts/0x1234", nil); code != 404 {
		t.Errorf("invalid address: %d", code)
	}
	var cfgJSON map[string]any
	getJSON(t, base+"/config", &cfgJSON)
	if cfgJSON["explorer_url"] != "https://explorer.example" {
		t.Errorf("config %v", cfgJSON)
	}
	// HEAD and OPTIONS.
	hr, _ := http.Head(base + "/stats")
	if hr.StatusCode != 200 || hr.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("HEAD stats %d", hr.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodOptions, base+"/smart-contracts/"+erc20+"/call", nil)
	or, _ := http.DefaultClient.Do(req)
	if or.StatusCode != 204 || or.Header.Get("Allow") != "POST, OPTIONS" {
		t.Errorf("OPTIONS call %d %q", or.StatusCode, or.Header.Get("Allow"))
	}
	t.Log("API endpoints on the anvil data: ok")
}

func newFakeVerifier(t *testing.T, h *harness) *httptest.Server {
	_, artABI := h.artifact("TestToken")
	// Two entries no bundled list has, so the signature endpoints must find
	// them in the verified ABI.
	var entries []any
	_ = json.Unmarshal(artABI, &entries)
	entries = append(entries,
		map[string]any{"type": "function", "name": "itestOnly", "stateMutability": "nonpayable", "outputs": []any{},
			"inputs": []any{map[string]any{"name": "x", "type": "uint256"}, map[string]any{"name": "y", "type": "bytes32[]"}}},
		map[string]any{"type": "error", "name": "ItestRefused", "inputs": []any{map[string]any{"name": "why", "type": "string"}}})
	abi, _ := json.Marshal(entries)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/compilers":
			_, _ = w.Write([]byte(`{"versions":["v0.8.28+commit.7893614a"]}`))
		case "/verify":
			calls.Add(1)
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			code, _ := req["deployed_bytecode"].(string)
			if !strings.HasPrefix(code, "0x60") || req["compiler_version"] != compiler {
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "failure", "message": "No contract in the input matches the deployed bytecode"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "success", "message": "ok", "match_type": "partial", "contract_name": "TestToken",
				"file_path": "src/Tokens.sol", "abi": json.RawMessage(abi),
				"sources":           map[string]string{"src/Tokens.sol": "// test source", "lib/ERC20.sol": "// lib"},
				"compiler_settings": map[string]any{"optimizer": map[string]any{"enabled": false}}, "evm_version": "cancun",
				"optimization_enabled": false, "optimization_runs": 200, "via_ir": false, "constructor_args": "0x",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (h *harness) verify(base, root, target, twin, kid string) {
	t := h.t
	// Standard JSON input the way forge --show-standard-json-input writes it.
	input := filepath.Join(t.TempDir(), "input.json")
	_ = os.WriteFile(input, []byte(`{"language":"Solidity","sources":{"src/Tokens.sol":{"content":"// x"}},"settings":{"optimizer":{"enabled":false}}}`), 0o600)
	out, err := run(t, "curl", "-sk", "-m", "30", "-X", "POST", base+"/smart-contracts/"+target+"/verification/via/standard-input",
		"-F", "compiler_version="+compiler, "-F", "license_type=mit", "-F", "autodetect_constructor_args=true",
		"-F", "files[0]=@"+input+";type=application/json")
	if err != nil || !strings.Contains(out, "verification started") {
		t.Fatalf("submission: %v %s", err, out)
	}
	// The script's check: grep '"is_verified":true'.
	deadline := time.Now().Add(15 * time.Second)
	for !strings.Contains(getRaw(t, base+"/smart-contracts/"+target), `"is_verified":true`) {
		if time.Now().After(deadline) {
			var st map[string]any
			getJSON(t, base+"/smart-contracts/"+target+"/verification/status", &st)
			t.Fatalf("never verified; status %v", st)
		}
		time.Sleep(100 * time.Millisecond)
	}
	var sc map[string]any
	getJSON(t, base+"/smart-contracts/"+target, &sc)
	if sc["match_type"] != "partial" || sc["is_partially_verified"] != true || sc["source_code"] != "// test source" || len(sc["additional_sources"].([]any)) != 1 || sc["abi"] == nil {
		t.Errorf("smart contract %v", sc)
	}
	var st map[string]any
	getJSON(t, base+"/smart-contracts/"+target+"/verification/status", &st)
	if st["status"] != "success" {
		t.Errorf("status %v", st)
	}
	for _, other := range []string{twin, kid} {
		getJSON(t, base+"/smart-contracts/"+other, &sc)
		if sc["is_verified"] != true || sc["match_type"] != "similar" || sc["source_address"] != api.EIP55(target) || sc["is_partially_verified"] != true {
			t.Errorf("similar %s: %v", other, sc)
		}
	}
	// Methods now resolve through the verified ABI.
	var page struct {
		Items []map[string]any `json:"items"`
	}
	getJSON(t, base+"/addresses/"+target+"/logs?limit=1", &page)
	if len(page.Items) != 1 || page.Items[0]["decoded"] == nil {
		t.Errorf("logs after verification %v", page.Items)
	}
	// Not a contract.
	out, _ = run(t, "curl", "-sk", "-X", "POST", base+"/smart-contracts/"+acct3+"/verification/via/standard-input",
		"-F", "compiler_version="+compiler, "-F", "files[0]=@"+input+";type=application/json")
	if !strings.Contains(out, "not a smart-contract") {
		t.Errorf("EOA submission: %s", out)
	}
	var comp map[string][]string
	getJSON(t, base+"/compilers", &comp)
	if len(comp["versions"]) != 1 {
		t.Errorf("compilers %v", comp)
	}
}

// reorg: snapshot, extend fork A (with a token that exists only there),
// revert, build fork B to the same height and one past it.
func (h *harness) reorg(erc20 string) {
	t := h.t
	snap := strings.Trim(h.cast("rpc", "--rpc-url", h.rpcURL, "evm_snapshot"), `"`)
	base := h.cursor()
	orphan := h.deploy("TestToken", "constructor(string,string,uint256)", "Orphan", "ORPH", "77")
	h.send(key0, erc20, "transfer(address,uint256)", "0x00000000000000000000000000000000000000a1", "11")
	h.send(key0, orphan.ContractAddress, "transfer(address,uint256)", acct3, "7")
	h.waitIndexed("fork A")
	var n int
	_ = h.pool.QueryRow(h.ctx, `SELECT count(*) FROM tokens WHERE address = $1`, orphan.ContractAddress).Scan(&n)
	if n != 1 {
		t.Fatalf("fork A token not indexed")
	}
	forkAHeight := h.cursor()

	if out := h.cast("rpc", "--rpc-url", h.rpcURL, "evm_revert", snap); out != "true" {
		t.Fatalf("evm_revert: %s", out)
	}
	// Fork B: different content, same height first (tip-hash check), then one more.
	h.send(key0, erc20, "transfer(address,uint256)", "0x00000000000000000000000000000000000000b2", "22")
	h.cast("rpc", "--rpc-url", h.rpcURL, "anvil_mine", fmt.Sprint(forkAHeight-base-1))
	if int64(h.head()) != forkAHeight {
		t.Fatalf("fork B height %d, want %d", h.head(), forkAHeight)
	}
	h.waitIndexed("fork B at the same height")
	h.send(key1, erc20, "transfer(address,uint256)", acct3, "1")
	h.waitIndexed("fork B past the old tip")
	h.assertContiguous()

	_ = h.pool.QueryRow(h.ctx, `SELECT count(*) FROM tokens WHERE address = $1`, orphan.ContractAddress).Scan(&n)
	if n != 0 {
		t.Errorf("orphaned token still listed")
	}
	_ = h.pool.QueryRow(h.ctx, `SELECT count(*) FROM addresses WHERE address = $1`, orphan.ContractAddress).Scan(&n)
	if n != 0 {
		t.Errorf("orphaned contract address still stored")
	}
	_ = h.pool.QueryRow(h.ctx, `SELECT count(*) FROM token_balances WHERE token = $1 AND holder = '0x00000000000000000000000000000000000000a1'`, erc20).Scan(&n)
	if n != 0 {
		t.Errorf("fork A balance survived")
	}
	_ = h.pool.QueryRow(h.ctx, `SELECT count(*) FROM token_balances WHERE token = $1 AND holder = '0x00000000000000000000000000000000000000b2'`, erc20).Scan(&n)
	if n != 1 {
		t.Errorf("fork B balance missing")
	}
	h.checkBalances(erc20)
	t.Logf("reorg: fork A %d..%d rolled back, fork B indexed to %d", base+1, forkAHeight, h.cursor())
}

func (h *harness) admin(ix *indexerProc) {
	t := h.t
	cfg := &config.Config{Role: config.RoleAdmin, DatabaseURL: h.dbURL, ChainRPCURL: h.rpcURL, ChainID: chainID, Traces: "required"}
	runAdmin := func(args ...string) (int, string) {
		var out, errb bytes.Buffer
		env := &admin.Env{Cfg: cfg, Log: h.log, Out: &out, Err: &errb, Pool: h.pool, RPC: chainrpc.New(h.rpcURL, chainrpc.Options{}),
			HookFn: func(context.Context) error {
				if ix.hook(t, hookTok) != http.StatusAccepted {
					return fmt.Errorf("hook refused")
				}
				return nil
			}}
		code := admin.Run(h.ctx, env, args)
		return code, out.String() + errb.String()
	}

	code, out := runAdmin("status")
	if code != 0 || !strings.Contains(out, "indexers        1 connected") || strings.Contains(out, "PROBLEM") {
		t.Errorf("status %d:\n%s", code, out)
	}
	if code, out = runAdmin("check"); code != 0 || !strings.Contains(out, ": 0 mismatches") {
		t.Errorf("check on a faithful copy %d:\n%s", code, out)
	}
	t.Logf("admin check: %s", strings.TrimSpace(out))

	// Tamper with one stored hash: check must report it and exit 1.
	victim := h.cursor() - 3
	if _, err := h.pool.Exec(h.ctx, `UPDATE blocks SET hash = '0x' || repeat('0', 64) WHERE number = $1`, victim); err != nil {
		t.Fatal(err)
	}
	if code, out = runAdmin("check", "--from", fmt.Sprint(victim-2)); code != 1 || !strings.Contains(out, fmt.Sprintf("MISMATCH block %d: hash differs", victim)) {
		t.Errorf("check on a tampered copy %d:\n%s", code, out)
	}

	// reindex refuses without --yes, then repairs from the bad block.
	if code, out = runAdmin("reindex", "--from", fmt.Sprint(victim)); code != 2 || !strings.Contains(out, "--yes") {
		t.Errorf("reindex without --yes %d: %s", code, out)
	}
	if code, out = runAdmin("reindex", "--from", fmt.Sprint(victim), "--yes"); code != 0 || !strings.Contains(out, "indexer poked") {
		t.Errorf("reindex %d:\n%s", code, out)
	}
	h.waitIndexed("refill after reindex")
	if code, out = runAdmin("check"); code != 0 {
		t.Errorf("check after reindex %d:\n%s", code, out)
	}
	h.assertContiguous()
	// --to re-indexes inline before returning.
	if code, out = runAdmin("reindex", "--from", fmt.Sprint(victim), "--to", fmt.Sprint(victim+1), "--yes"); code != 0 || !strings.Contains(out, "re-indexed blocks") {
		t.Errorf("reindex --to %d:\n%s", code, out)
	}
	h.waitIndexed("refill after reindex --to")
	t.Log("admin reindex: ok")

	if code, out = runAdmin("label", "set", acct1, "Treasury", "wallet"); code != 0 {
		t.Errorf("label set %d %s", code, out)
	}
	var name, src string
	_ = h.pool.QueryRow(h.ctx, `SELECT name, source FROM address_names WHERE address = $1`, acct1).Scan(&name, &src)
	if name != "Treasury wallet" || src != "manual" {
		t.Errorf("label row %q %q", name, src)
	}
	if code, out = runAdmin("label", "rm", acct1); code != 0 || !strings.Contains(out, "removed") {
		t.Errorf("label rm %d %s", code, out)
	}
	if code, _ = runAdmin("label", "set", "nope", "x"); code != 2 {
		t.Errorf("label set with a bad address exit %d", code)
	}
	t.Log("admin label: ok")
}

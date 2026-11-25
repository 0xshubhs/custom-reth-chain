package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// formatVersion is written into every header. compare refuses files whose
// version it does not know.
const formatVersion = "chain-fingerprint/1"

// Header is the first line of a fingerprint file.
type Header struct {
	Kind          string `json:"kind"` // "header"
	Format        string `json:"format"`
	ChainID       uint64 `json:"chain_id"`
	ClientVersion string `json:"client_version"`
	GenesisHash   string `json:"genesis_hash"`
	Head          uint64 `json:"head"` // eth_blockNumber when the dump started
	From          uint64 `json:"from"`
	To            uint64 `json:"to"`
	RPC           string `json:"rpc"`
	CreatedAt     string `json:"created_at"`
}

// Block is one line per block. Every field is compared.
type Block struct {
	Kind             string `json:"kind"` // "block"
	Number           uint64 `json:"number"`
	Hash             string `json:"hash"`
	ParentHash       string `json:"parent_hash"`
	StateRoot        string `json:"state_root"`
	TransactionsRoot string `json:"transactions_root"`
	ReceiptsRoot     string `json:"receipts_root"`
	Timestamp        uint64 `json:"timestamp"`
	Miner            string `json:"miner"`
	TxCount          int    `json:"tx_count"`
	TxHashesDigest   string `json:"tx_hashes_digest"`
	ReceiptCount     int    `json:"receipt_count"`
	LogCount         int    `json:"log_count"`
	ReceiptsDigest   string `json:"receipts_digest"`
	// Historical-state probe: balances AT this block. An archive node answers
	// for every block; a pruned one fails for old blocks.
	MinerBalance       string  `json:"miner_balance"`
	FirstSender        *string `json:"first_sender"`
	FirstSenderBalance *string `json:"first_sender_balance"`
}

// Footer is the last line. Its presence proves the file was written to the
// end; LinesSHA256 covers every block line exactly as written.
type Footer struct {
	Kind        string `json:"kind"` // "footer"
	Blocks      int    `json:"blocks"`
	LinesSHA256 string `json:"lines_sha256"`
}

type dumpOptions struct {
	RPC         string
	From        uint64
	To          *uint64 // nil = head at start
	Out         string  // "" or "-" = stdout
	Concurrency int
	BatchCalls  int // max calls in one JSON-RPC batch
	Timeout     time.Duration
	Progress    io.Writer
}

// node JSON shapes (only what the fingerprint reads).
type rpcBlock struct {
	Number           string  `json:"number"`
	Hash             string  `json:"hash"`
	ParentHash       string  `json:"parentHash"`
	StateRoot        string  `json:"stateRoot"`
	TransactionsRoot string  `json:"transactionsRoot"`
	ReceiptsRoot     string  `json:"receiptsRoot"`
	Timestamp        string  `json:"timestamp"`
	Miner            string  `json:"miner"`
	Transactions     []rpcTx `json:"transactions"`
}

type rpcTx struct {
	Hash string `json:"hash"`
	From string `json:"from"`
}

type rpcReceipt struct {
	TransactionHash   string   `json:"transactionHash"`
	TransactionIndex  string   `json:"transactionIndex"`
	BlockHash         string   `json:"blockHash"`
	BlockNumber       string   `json:"blockNumber"`
	From              string   `json:"from"`
	To                *string  `json:"to"`
	ContractAddress   *string  `json:"contractAddress"`
	Status            string   `json:"status"`
	Root              string   `json:"root"`
	CumulativeGasUsed string   `json:"cumulativeGasUsed"`
	GasUsed           string   `json:"gasUsed"`
	EffectiveGasPrice string   `json:"effectiveGasPrice"`
	BlobGasUsed       string   `json:"blobGasUsed"`
	BlobGasPrice      string   `json:"blobGasPrice"`
	Type              string   `json:"type"`
	LogsBloom         string   `json:"logsBloom"`
	Logs              []rpcLog `json:"logs"`
}

type rpcLog struct {
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
	LogIndex         string   `json:"logIndex"`
	TransactionIndex string   `json:"transactionIndex"`
	TransactionHash  string   `json:"transactionHash"`
	Removed          bool     `json:"removed"`
}

// canonReceipt / canonLog fix the field set and order that goes into the
// receipts digest. Fields some client versions add (blockTimestamp on logs,
// for instance) are left out on purpose, so two nodes that store the same
// data hash the same; everything consensus-relevant is in.
type canonReceipt struct {
	TxHash            string     `json:"tx"`
	TxIndex           string     `json:"ti"`
	BlockHash         string     `json:"bh"`
	BlockNumber       string     `json:"bn"`
	From              string     `json:"from"`
	To                string     `json:"to"`
	ContractAddress   string     `json:"ca"`
	Status            string     `json:"st"`
	Root              string     `json:"root"`
	CumulativeGasUsed string     `json:"cgu"`
	GasUsed           string     `json:"gu"`
	EffectiveGasPrice string     `json:"egp"`
	BlobGasUsed       string     `json:"bgu"`
	BlobGasPrice      string     `json:"bgp"`
	Type              string     `json:"type"`
	LogsBloom         string     `json:"bloom"`
	Logs              []canonLog `json:"logs"`
}

type canonLog struct {
	Address  string   `json:"a"`
	Topics   []string `json:"t"`
	Data     string   `json:"d"`
	LogIndex string   `json:"li"`
	TxIndex  string   `json:"ti"`
	TxHash   string   `json:"th"`
	Removed  bool     `json:"r"`
}

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func lowerPtr(s *string) string {
	if s == nil {
		return ""
	}
	return lower(*s)
}

// quantity turns a hex quantity into a decimal string; "" stays "".
func quantity(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	n, ok := new(big.Int).SetString(strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X"), 16)
	if !ok || !(strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")) {
		return "", fmt.Errorf("not a hex quantity: %q", s)
	}
	return n.String(), nil
}

func quantityU64(s string) (uint64, error) {
	d, err := quantity(s)
	if err != nil {
		return 0, err
	}
	if d == "" {
		return 0, fmt.Errorf("empty quantity")
	}
	n, ok := new(big.Int).SetString(d, 10)
	if !ok || !n.IsUint64() {
		return 0, fmt.Errorf("quantity out of range: %q", s)
	}
	return n.Uint64(), nil
}

func mustQ(s string) string {
	d, err := quantity(s)
	if err != nil {
		return "invalid:" + s
	}
	return d
}

func digestReceipts(rs []rpcReceipt) (string, int, error) {
	h := sha256.New()
	logs := 0
	for _, r := range rs {
		c := canonReceipt{
			TxHash:            lower(r.TransactionHash),
			TxIndex:           mustQ(r.TransactionIndex),
			BlockHash:         lower(r.BlockHash),
			BlockNumber:       mustQ(r.BlockNumber),
			From:              lower(r.From),
			To:                lowerPtr(r.To),
			ContractAddress:   lowerPtr(r.ContractAddress),
			Status:            mustQ(r.Status),
			Root:              lower(r.Root),
			CumulativeGasUsed: mustQ(r.CumulativeGasUsed),
			GasUsed:           mustQ(r.GasUsed),
			EffectiveGasPrice: mustQ(r.EffectiveGasPrice),
			BlobGasUsed:       mustQ(r.BlobGasUsed),
			BlobGasPrice:      mustQ(r.BlobGasPrice),
			Type:              mustQ(r.Type),
			LogsBloom:         lower(r.LogsBloom),
			Logs:              make([]canonLog, 0, len(r.Logs)),
		}
		for _, l := range r.Logs {
			topics := make([]string, len(l.Topics))
			for i, t := range l.Topics {
				topics[i] = lower(t)
			}
			c.Logs = append(c.Logs, canonLog{
				Address:  lower(l.Address),
				Topics:   topics,
				Data:     lower(l.Data),
				LogIndex: mustQ(l.LogIndex),
				TxIndex:  mustQ(l.TransactionIndex),
				TxHash:   lower(l.TransactionHash),
				Removed:  l.Removed,
			})
			logs++
		}
		b, err := json.Marshal(c)
		if err != nil {
			return "", 0, err
		}
		h.Write(b)
		h.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), logs, nil
}

func digestTxHashes(txs []rpcTx) string {
	h := sha256.New()
	for _, t := range txs {
		h.Write([]byte(lower(t.Hash)))
		h.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func hexNum(n uint64) string { return fmt.Sprintf("0x%x", n) }

// fetchHeader reads chain id, client version, genesis hash and head.
func fetchHeader(ctx context.Context, c *rpcClient) (Header, error) {
	var chainID, head, clientVersion string
	var genesis rpcBlock
	calls := []*rpcCall{
		{Method: "eth_chainId", Result: &chainID},
		{Method: "web3_clientVersion", Result: &clientVersion},
		{Method: "eth_getBlockByNumber", Params: []any{"0x0", false}, Result: &genesis},
		{Method: "eth_blockNumber", Result: &head},
	}
	if err := c.batch(ctx, calls); err != nil {
		return Header{}, err
	}
	for _, cl := range calls {
		if cl.Err != nil && cl.Method != "web3_clientVersion" {
			return Header{}, fmt.Errorf("%s: %w", cl.Method, cl.Err)
		}
	}
	id, err := quantityU64(chainID)
	if err != nil {
		return Header{}, fmt.Errorf("eth_chainId: %w", err)
	}
	h, err := quantityU64(head)
	if err != nil {
		return Header{}, fmt.Errorf("eth_blockNumber: %w", err)
	}
	if genesis.Hash == "" {
		return Header{}, fmt.Errorf("the node has no block 0")
	}
	if calls[1].Err != nil {
		clientVersion = "unknown (" + calls[1].Err.Message + ")"
	}
	return Header{
		Kind: "header", Format: formatVersion, ChainID: id, ClientVersion: clientVersion,
		GenesisHash: lower(genesis.Hash), Head: h,
	}, nil
}

// fetchChunk fingerprints blocks [from, to].
func fetchChunk(ctx context.Context, c *rpcClient, from, to uint64) ([]Block, error) {
	n := int(to - from + 1)
	blocks := make([]rpcBlock, n)
	calls := make([]*rpcCall, n)
	for i := 0; i < n; i++ {
		calls[i] = &rpcCall{Method: "eth_getBlockByNumber", Params: []any{hexNum(from + uint64(i)), true}, Result: &blocks[i]}
	}
	if err := c.batch(ctx, calls); err != nil {
		return nil, err
	}
	for i, cl := range calls {
		if cl.Err != nil {
			return nil, fmt.Errorf("block %d: %w", from+uint64(i), cl.Err)
		}
		if blocks[i].Hash == "" {
			return nil, fmt.Errorf("block %d: the node does not have it", from+uint64(i))
		}
	}

	receipts := make([][]rpcReceipt, n)
	minerBal := make([]string, n)
	senderBal := make([]string, n)
	calls = calls[:0]
	type slot struct{ kind, i int }
	var slots []slot
	for i := 0; i < n; i++ {
		num := hexNum(from + uint64(i))
		calls = append(calls, &rpcCall{Method: "eth_getBlockReceipts", Params: []any{num}, Result: &receipts[i]})
		slots = append(slots, slot{0, i})
		calls = append(calls, &rpcCall{Method: "eth_getBalance", Params: []any{blocks[i].Miner, num}, Result: &minerBal[i]})
		slots = append(slots, slot{1, i})
		if len(blocks[i].Transactions) > 0 {
			calls = append(calls, &rpcCall{Method: "eth_getBalance", Params: []any{blocks[i].Transactions[0].From, num}, Result: &senderBal[i]})
			slots = append(slots, slot{2, i})
		}
	}
	if err := c.batch(ctx, calls); err != nil {
		return nil, err
	}
	for k, cl := range calls {
		if cl.Err == nil {
			continue
		}
		s := slots[k]
		if s.kind == 0 && cl.Err.Code == -32601 {
			// No eth_getBlockReceipts on this node: one receipt per tx.
			rs, err := receiptsOneByOne(ctx, c, blocks[s.i].Transactions)
			if err != nil {
				return nil, fmt.Errorf("block %d receipts: %w", from+uint64(s.i), err)
			}
			receipts[s.i] = rs
			continue
		}
		return nil, fmt.Errorf("block %d %s: %w (a pruned, non-archive node fails here for old blocks)", from+uint64(s.i), cl.Method, cl.Err)
	}

	out := make([]Block, n)
	for i := 0; i < n; i++ {
		b := blocks[i]
		num, err := quantityU64(b.Number)
		if err != nil || num != from+uint64(i) {
			return nil, fmt.Errorf("block %d: node answered number %q", from+uint64(i), b.Number)
		}
		ts, err := quantityU64(b.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("block %d timestamp: %w", num, err)
		}
		rd, logs, err := digestReceipts(receipts[i])
		if err != nil {
			return nil, err
		}
		if len(receipts[i]) != len(b.Transactions) {
			return nil, fmt.Errorf("block %d: %d transactions but %d receipts", num, len(b.Transactions), len(receipts[i]))
		}
		blk := Block{
			Kind: "block", Number: num, Hash: lower(b.Hash), ParentHash: lower(b.ParentHash),
			StateRoot: lower(b.StateRoot), TransactionsRoot: lower(b.TransactionsRoot),
			ReceiptsRoot: lower(b.ReceiptsRoot), Timestamp: ts, Miner: lower(b.Miner),
			TxCount: len(b.Transactions), TxHashesDigest: digestTxHashes(b.Transactions),
			ReceiptCount: len(receipts[i]), LogCount: logs, ReceiptsDigest: rd,
			MinerBalance: mustQ(minerBal[i]),
		}
		if len(b.Transactions) > 0 {
			s := lower(b.Transactions[0].From)
			bal := mustQ(senderBal[i])
			blk.FirstSender, blk.FirstSenderBalance = &s, &bal
		}
		out[i] = blk
	}
	return out, nil
}

func receiptsOneByOne(ctx context.Context, c *rpcClient, txs []rpcTx) ([]rpcReceipt, error) {
	rs := make([]rpcReceipt, len(txs))
	calls := make([]*rpcCall, len(txs))
	for i, t := range txs {
		calls[i] = &rpcCall{Method: "eth_getTransactionReceipt", Params: []any{t.Hash}, Result: &rs[i]}
	}
	if err := c.batch(ctx, calls); err != nil {
		return nil, err
	}
	for _, cl := range calls {
		if cl.Err != nil {
			return nil, cl.Err
		}
	}
	return rs, nil
}

func runDump(ctx context.Context, o dumpOptions) (Header, int, error) {
	if o.Concurrency < 1 || o.Concurrency > 4 {
		return Header{}, 0, fmt.Errorf("--concurrency must be 1..4 (the live RPC is production)")
	}
	if o.BatchCalls < 3 {
		o.BatchCalls = 3
	}
	c := newRPCClient(o.RPC, o.Concurrency, o.Timeout)
	hdr, err := fetchHeader(ctx, c)
	if err != nil {
		return Header{}, 0, err
	}
	hdr.From = o.From
	hdr.To = hdr.Head
	if o.To != nil {
		if *o.To > hdr.Head {
			return Header{}, 0, fmt.Errorf("--to %d is past the node's head %d", *o.To, hdr.Head)
		}
		hdr.To = *o.To
	}
	if hdr.From > hdr.To {
		return Header{}, 0, fmt.Errorf("--from %d is past --to %d", hdr.From, hdr.To)
	}
	hdr.RPC = redactURL(o.RPC)
	hdr.CreatedAt = time.Now().UTC().Format(time.RFC3339)

	// Each block costs at most 3 calls in the second batch.
	perChunk := uint64(o.BatchCalls / 3)
	type job struct{ from, to uint64 }
	jobs := make(chan job)
	var (
		mu      sync.Mutex
		results = make(map[uint64][]Block)
		firstEr error
		done    uint64
	)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	total := hdr.To - hdr.From + 1
	start := time.Now()
	for w := 0; w < o.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				bs, err := fetchChunk(cctx, c, j.from, j.to)
				mu.Lock()
				if err != nil {
					if firstEr == nil {
						firstEr = err
						cancel()
					}
				} else {
					results[j.from] = bs
					done += uint64(len(bs))
					if o.Progress != nil && (done%500 < uint64(len(bs)) || done == total) {
						fmt.Fprintf(o.Progress, "  %d/%d blocks (%.0fs)\n", done, total, time.Since(start).Seconds())
					}
				}
				mu.Unlock()
			}
		}()
	}
feed:
	for f := hdr.From; f <= hdr.To; f += perChunk {
		t := f + perChunk - 1
		if t > hdr.To {
			t = hdr.To
		}
		select {
		case jobs <- job{f, t}:
		case <-cctx.Done():
			break feed
		}
		if t == hdr.To {
			break
		}
	}
	close(jobs)
	wg.Wait()
	if firstEr != nil {
		return Header{}, 0, firstEr
	}

	keys := make([]uint64, 0, len(results))
	for k := range results {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	all := make([]Block, 0, total)
	for _, k := range keys {
		all = append(all, results[k]...)
	}
	if uint64(len(all)) != total {
		return Header{}, 0, fmt.Errorf("internal: fetched %d blocks, expected %d", len(all), total)
	}
	// The chain must link up inside the range. A break means the node
	// reorganised during the dump (or answered inconsistently).
	for i := 1; i < len(all); i++ {
		if all[i].ParentHash != all[i-1].Hash {
			return Header{}, 0, fmt.Errorf("block %d parent_hash %s is not block %d's hash %s (chain changed during the dump?)",
				all[i].Number, all[i].ParentHash, all[i-1].Number, all[i-1].Hash)
		}
	}
	if hdr.From == 0 && all[0].Hash != hdr.GenesisHash {
		return Header{}, 0, fmt.Errorf("block 0 hash changed during the dump")
	}
	if err := writeFingerprint(o.Out, hdr, all); err != nil {
		return Header{}, 0, err
	}
	if o.Progress != nil {
		fmt.Fprintf(o.Progress, "  %d HTTP requests in %.1fs\n", c.requests.Load(), time.Since(start).Seconds())
	}
	return hdr, len(all), nil
}

// writeFingerprint writes to a temp file and renames it into place, so a file
// with the final name is always complete.
func writeFingerprint(path string, hdr Header, blocks []Block) error {
	var w io.Writer
	var tmp *os.File
	if path == "" || path == "-" {
		w = os.Stdout
	} else {
		var err error
		tmp, err = os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		w = tmp
	}
	bw := bufio.NewWriter(w)
	line, err := json.Marshal(hdr)
	if err != nil {
		return err
	}
	bw.Write(line)
	bw.WriteByte('\n')
	h := sha256.New()
	for _, b := range blocks {
		line, err := json.Marshal(b)
		if err != nil {
			return err
		}
		bw.Write(line)
		bw.WriteByte('\n')
		h.Write(line)
		h.Write([]byte{'\n'})
	}
	ft, _ := json.Marshal(Footer{Kind: "footer", Blocks: len(blocks), LinesSHA256: hex.EncodeToString(h.Sum(nil))})
	bw.Write(ft)
	bw.WriteByte('\n')
	if err := bw.Flush(); err != nil {
		return err
	}
	if tmp != nil {
		if err := tmp.Sync(); err != nil {
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := os.Rename(tmp.Name(), path); err != nil {
			return err
		}
	}
	return nil
}

// redactURL drops credentials and query strings (an RPC URL can carry a key).
func redactURL(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if at := strings.Index(rest, "@"); at >= 0 && (strings.Index(rest, "/") == -1 || at < strings.Index(rest, "/")) {
			rest = rest[at+1:]
		}
		u = u[:i+3] + rest
	}
	if q := strings.IndexAny(u, "?#"); q >= 0 {
		u = u[:q]
	}
	return u
}

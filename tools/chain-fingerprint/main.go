// Command chain-fingerprint proves that two nodes hold the same chain.
//
//	chain-fingerprint dump --rpc URL [--from N] [--to M] [--out FILE]
//	chain-fingerprint compare A B [--allow-longer]
//	chain-fingerprint persisted --datadir DIR
//	chain-fingerprint settle --rpc URL --datadir DIR [--yes]   (key in $SETTLE_KEY)
//
// dump writes a header line (chain id, client version, genesis hash, head),
// one line per block (hash, parent hash, state/transactions/receipts roots,
// timestamp, tx count, a digest of the tx hashes, a digest of the receipts
// with their logs, and the balances of the miner and of the first sender AT
// that block, which only an archive node can answer for old blocks), and a
// footer with a checksum over the block lines. It only ever calls
// eth_chainId, web3_clientVersion, eth_blockNumber, eth_getBlockByNumber,
// eth_getBlockReceipts (eth_getTransactionReceipt when that is missing) and
// eth_getBalance, in JSON-RPC batches, with at most 4 requests in flight.
//
// compare exits 0 only when every block of A is identical in B. B may hold
// more blocks than A only with --allow-longer. It prints the first
// difference.
//
// persisted and settle exist because this node can lose its newest blocks on
// a kill; see persisted.go.
package main

import (
	"context"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

const usage = `chain-fingerprint: prove two nodes hold the same chain

  chain-fingerprint dump --rpc URL [--from N] [--to M] [--out FILE] [--concurrency 4] [--batch 24]
  chain-fingerprint compare A.fp B.fp [--allow-longer]
  chain-fingerprint persisted --datadir DIR
  chain-fingerprint settle --rpc URL --datadir DIR [--yes] [--max 3]   (key in $SETTLE_KEY)

exit codes: 0 ok / identical / settled, 1 different or failed, 2 usage,
            3 settle: blocks in memory and --yes not given
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1], os.Args[2:]))
}

func run(ctx context.Context, cmd string, args []string) int {
	switch cmd {
	case "dump":
		return cmdDump(ctx, args)
	case "compare":
		return cmdCompare(args)
	case "persisted":
		return cmdPersisted(args)
	case "settle":
		return cmdSettle(ctx, args)
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	}
	fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
	return 2
}

type optUint struct {
	set bool
	v   uint64
}

func (o *optUint) String() string {
	if !o.set {
		return ""
	}
	return strconv.FormatUint(o.v, 10)
}

func (o *optUint) Set(s string) error {
	v, err := strconv.ParseUint(s, 0, 64)
	if err != nil {
		return err
	}
	o.v, o.set = v, true
	return nil
}

func cmdDump(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("dump", flag.ContinueOnError)
	rpc := fs.String("rpc", "", "node JSON-RPC URL (required)")
	from := fs.Uint64("from", 0, "first block")
	var to optUint
	fs.Var(&to, "to", "last block (default: the head when the dump starts)")
	out := fs.String("out", "-", "output file (- = stdout)")
	conc := fs.Int("concurrency", 4, "HTTP requests in flight, 1..4")
	batch := fs.Int("batch", 24, "max calls per JSON-RPC batch (the public gateway allows 25)")
	timeout := fs.Duration("timeout", 60*time.Second, "per-request timeout")
	quiet := fs.Bool("quiet", false, "no progress on stderr")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *rpc == "" || fs.NArg() != 0 {
		fmt.Fprint(os.Stderr, "dump needs --rpc URL and no positional arguments\n")
		return 2
	}
	o := dumpOptions{RPC: *rpc, From: *from, Out: *out, Concurrency: *conc, BatchCalls: *batch, Timeout: *timeout}
	if to.set {
		o.To = &to.v
	}
	if !*quiet {
		o.Progress = os.Stderr
	}
	start := time.Now()
	hdr, n, err := runDump(ctx, o)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dump failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(os.Stderr, "dumped %d blocks (%d..%d) of chain %d, genesis %s, head %d, client %q in %.1fs -> %s\n",
		n, hdr.From, hdr.To, hdr.ChainID, hdr.GenesisHash, hdr.Head, hdr.ClientVersion, time.Since(start).Seconds(), *out)
	return 0
}

func cmdCompare(args []string) int {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	allowLonger := fs.Bool("allow-longer", false, "B may hold blocks outside A's range")
	// Accept flags before or after the two file names.
	var files []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		files = append(files, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(files) != 2 {
		fmt.Fprint(os.Stderr, "compare needs exactly two files: A B\n")
		return 2
	}
	a, err := loadFingerprint(files[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "A: %v\n", err)
		return 1
	}
	b, err := loadFingerprint(files[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "B: %v\n", err)
		return 1
	}
	fmt.Printf("A: %s  blocks %d..%d  client %q  dumped %s from %s\n", files[0], a.First, a.Last, a.Header.ClientVersion, a.Header.CreatedAt, a.Header.RPC)
	fmt.Printf("B: %s  blocks %d..%d  client %q  dumped %s from %s\n", files[1], b.First, b.Last, b.Header.ClientVersion, b.Header.CreatedAt, b.Header.RPC)
	res := compareFingerprints(a, b, *allowLonger)
	if !res.Identical {
		fmt.Printf("DIFFERENT: %s\n", res.Message)
		return 1
	}
	fmt.Println(res.Message)
	return 0
}

func cmdPersisted(args []string) int {
	fs := flag.NewFlagSet("persisted", flag.ContinueOnError)
	datadir := fs.String("datadir", "", "the node's datadir (read only)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *datadir == "" {
		fmt.Fprint(os.Stderr, "persisted needs --datadir\n")
		return 2
	}
	info, err := persistedHeight(*datadir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	fmt.Printf("%d\n", info.Highest)
	return 0
}

func cmdSettle(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("settle", flag.ContinueOnError)
	rpc := fs.String("rpc", "", "the node's own JSON-RPC URL, e.g. http://127.0.0.1:8545 (required)")
	datadir := fs.String("datadir", "", "that node's datadir (required, read only)")
	yes := fs.Bool("yes", false, "really send padding transactions when blocks are only in memory")
	max := fs.Int("max", 3, "most padding transactions to send")
	gasPrice := fs.String("gas-price-wei", "1000000000", "legacy gas price of a padding tx (the txpool floor is about 1 gwei)")
	lag := fs.Duration("persist-wait", 3*time.Second, "pause after each padding block for reth to finish writing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *rpc == "" || *datadir == "" {
		fmt.Fprint(os.Stderr, "settle needs --rpc and --datadir\n")
		return 2
	}
	gp, ok := new(big.Int).SetString(*gasPrice, 10)
	if !ok || gp.Sign() < 0 {
		fmt.Fprint(os.Stderr, "--gas-price-wei: want a whole number\n")
		return 2
	}
	code, err := runSettle(ctx, settleOptions{
		RPC: *rpc, Datadir: *datadir, Key: os.Getenv("SETTLE_KEY"), Yes: *yes, Max: *max,
		GasPrice: gp, PersistLag: *lag, Out: os.Stdout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "settle: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

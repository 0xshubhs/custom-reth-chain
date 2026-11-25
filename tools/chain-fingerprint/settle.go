package main

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rlp"
)

// settle makes sure every block the node has served is on disk before the
// node is killed. See persisted.go for why that is not automatic.
//
// It compares eth_blockNumber with the highest header in the datadir. When
// they are equal there is nothing in memory and the node can be killed. When
// they differ, and only with --yes, it sends ONE zero-value transfer from the
// settle key to itself, which seals one more block; reth persists the whole
// in-memory run as soon as it is more than 2 blocks long, so at most 3 such
// blocks bring the disk level with the head. It re-checks after each one and
// stops as soon as disk == head.
//
// The padding transactions are real blocks on the chain (a 0-value self
// transfer, nothing else). Use a key nothing else uses (the runbook suggests
// Hardhat account #19, prefunded at genesis and referenced nowhere else), and
// only in the quiet window when no engine is sending.
type settleOptions struct {
	RPC        string
	Datadir    string
	Key        string // hex private key (from the environment, never argv)
	Yes        bool
	Max        int
	GasPrice   *big.Int
	PersistLag time.Duration // how long reth gets to finish a persistence run
	Out        io.Writer
}

// exit codes
const (
	settleOK        = 0
	settleNeedsYes  = 3
	settleNotSettle = 4
)

func runSettle(ctx context.Context, o settleOptions) (int, error) {
	c := newRPCClient(o.RPC, 1, 30*time.Second)
	var chainHex string
	if err := c.call(ctx, "eth_chainId", &chainHex); err != nil {
		return 1, fmt.Errorf("eth_chainId: %w", err)
	}
	chainID, err := quantityU64(chainHex)
	if err != nil {
		return 1, err
	}

	var key *ecdsa.PrivateKey
	var from common.Address
	if o.Key != "" {
		key, err = crypto.HexToECDSA(strings.TrimPrefix(strings.TrimSpace(o.Key), "0x"))
		if err != nil {
			return 1, fmt.Errorf("settle key: %w", err)
		}
		from = crypto.PubkeyToAddress(key.PublicKey)
	}

	for round := 0; ; round++ {
		head, err := blockNumber(ctx, c)
		if err != nil {
			return 1, err
		}
		info, err := persistedHeight(o.Datadir)
		if err != nil {
			return 1, err
		}
		disk := info.Highest
		fmt.Fprintf(o.Out, "head (RPC) %d, on disk %d\n", head, disk)
		switch {
		case disk == int64(head):
			fmt.Fprintf(o.Out, "SETTLED: every block up to %d is on disk; the node can be stopped now\n", head)
			return settleOK, nil
		case disk > int64(head):
			return 1, fmt.Errorf("the datadir is ahead of the RPC (%d > %d): --datadir and --rpc are not the same node", disk, head)
		case int64(head)-disk > 3:
			return 1, fmt.Errorf("the datadir is %d blocks behind the RPC; reth never lags more than 2 at rest, so --datadir and --rpc are probably not the same node (or the node is still syncing)", int64(head)-disk)
		}
		fmt.Fprintf(o.Out, "%d block(s) (%d..%d) exist only in the node's memory; a kill now would lose them\n", int64(head)-disk, disk+1, head)
		if round >= o.Max {
			return settleNotSettle, fmt.Errorf("still not settled after %d padding transaction(s)", round)
		}
		if !o.Yes || key == nil {
			fmt.Fprintln(o.Out, "NOT SETTLED. To push them to disk, re-run with SETTLE_KEY set and --yes: it sends up to 3 zero-value self-transfers")
			return settleNeedsYes, nil
		}
		if err := sendPadding(ctx, c, key, from, chainID, o.GasPrice, o.Out); err != nil {
			return 1, err
		}
		time.Sleep(o.PersistLag)
	}
}

func blockNumber(ctx context.Context, c *rpcClient) (uint64, error) {
	var h string
	if err := c.call(ctx, "eth_blockNumber", &h); err != nil {
		return 0, fmt.Errorf("eth_blockNumber: %w", err)
	}
	return quantityU64(h)
}

func sendPadding(ctx context.Context, c *rpcClient, key *ecdsa.PrivateKey, from common.Address, chainID uint64, gasPrice *big.Int, out io.Writer) error {
	var nonceHex string
	if err := c.call(ctx, "eth_getTransactionCount", &nonceHex, from.Hex(), "pending"); err != nil {
		return fmt.Errorf("nonce: %w", err)
	}
	nonce, err := quantityU64(nonceHex)
	if err != nil {
		return err
	}
	raw, err := signLegacyTransfer(key, nonce, gasPrice, 21000, from, chainID)
	if err != nil {
		return err
	}
	var hash string
	if err := c.call(ctx, "eth_sendRawTransaction", &hash, "0x"+common.Bytes2Hex(raw)); err != nil {
		return fmt.Errorf("send padding tx: %w", err)
	}
	fmt.Fprintf(out, "sent padding tx %s (from %s, nonce %d); waiting for its block\n", hash, from.Hex(), nonce)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var rc *rpcReceipt
		if err := c.call(ctx, "eth_getTransactionReceipt", &rc, hash); err == nil && rc != nil && rc.BlockNumber != "" {
			bn, _ := quantityU64(rc.BlockNumber)
			fmt.Fprintf(out, "  sealed in block %d\n", bn)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("padding tx %s was not sealed within 60s", hash)
}

// signLegacyTransfer builds and signs an EIP-155 legacy transaction sending 0
// wei to `to` with no data. Done by hand (rlp + secp256k1) rather than through
// go-ethereum's core/types, which would pull the KZG libraries into this
// module for one transfer.
func signLegacyTransfer(key *ecdsa.PrivateKey, nonce uint64, gasPrice *big.Int, gas uint64, to common.Address, chainID uint64) ([]byte, error) {
	cid := new(big.Int).SetUint64(chainID)
	zero := big.NewInt(0)
	unsigned, err := rlp.EncodeToBytes([]any{nonce, gasPrice, gas, to, zero, []byte{}, cid, uint(0), uint(0)})
	if err != nil {
		return nil, err
	}
	sig, err := crypto.Sign(crypto.Keccak256(unsigned), key)
	if err != nil {
		return nil, err
	}
	r := new(big.Int).SetBytes(sig[0:32])
	sv := new(big.Int).SetBytes(sig[32:64])
	v := new(big.Int).Add(new(big.Int).Mul(cid, big.NewInt(2)), big.NewInt(int64(sig[64])+35))
	return rlp.EncodeToBytes([]any{nonce, gasPrice, gas, to, zero, []byte{}, v, r, sv})
}

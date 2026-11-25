# Meowchain

A Proof of Authority EVM chain built on [Reth](https://github.com/paradigmxyz/reth), with its own
block explorer, contract verifier and public RPC gateway. Every Ethereum hardfork through Prague is
active at genesis; POA signers replace the beacon chain, and on-chain governance contracts manage
the signers and chain settings.

Everything runs with one command:

```bash
docker compose up -d --build
```

| What | Where |
|---|---|
| Explorer | http://localhost:3000 |
| Public RPC (gateway: allowlisted methods, rate limits, CORS) | http://localhost:8548 |
| Node RPC, full (debug, trace, txpool; keep private) | http://localhost:8545, ws://localhost:8546 |
| Explorer API | http://localhost:4000/api/v2 |

That starts a **development chain**: chain id 9323310, the 20 public Hardhat/Anvil accounts
prefunded, blocks sealed as soon as a transaction arrives. Those keys are published, so never put
value on it. A real network is one script away (see [Going live](#going-live)).

```bash
cast send --rpc-url http://localhost:8545 \
  --private-key 0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 \
  0x000000000000000000000000000000000000dEaD --value 1ether
```

## Repository layout

```
.
├── src/                    the node (Rust): consensus, payload builder, EVM, genesis, RPC
├── Docker/Dockerfile       node image
├── network/                network.example.toml; your network.toml lives here (gitignored)
├── explorer/               block explorer, works with any EVM chain
│   ├── backend/            Go: indexer + API (cmd/explorer), verifier, Blockscout importer
│   ├── frontend/           React + TanStack website, served by nginx
│   ├── docs/design.md      how the indexer, verifier and API work
│   └── compose.yaml        the explorer on its own (point it at any RPC)
├── rpc-gateway/            Go: the public JSON-RPC entrance
├── tools/chain-fingerprint Go: prove two nodes hold the same chain, block by block
├── scripts/                setup-network.sh (go-live), ...
├── genesis-contracts/      Solidity source of the governance contracts
├── docs/                   deployment and operations
├── compose.yaml            node + gateway + explorer
├── compose.live.yaml       real-network override (network.toml + signer secret)
└── .env.example            every setting, with defaults
```

## Going live

```bash
scripts/setup-network.sh
docker compose up -d --build
```

The script asks for the network's name, coin symbol and chain id, how blocks are sealed, this
node's signer key (generated or imported), the other signers, the **chain admin** (the account that
governs signers and settings; use a Safe multisig) and the genesis balances. It writes
`network/network.toml`, `secrets/signer.key` (mode 600) and `.env`, and checks everything with
the node itself, which refuses public development keys, a placeholder admin and chain ids that
belong to public networks. [docs/deployment.md](docs/deployment.md) covers TLS, firewall, backups
and adding signers.

## The node

```bash
just build        # release build, locked dependencies, pinned reth revision
just dev          # development chain
just test         # unit + integration tests
```

Important flags (`meowchain --help` lists all):

| Flag | Meaning |
|---|---|
| `--network <file>` | start a real network from a network.toml (production consensus) |
| `--signer-key-file <file>` | this node's signing key, read from a file (or `--signer-key` / `SIGNER_KEY`) |
| `--check-network` | with `--network`: validate, print the genesis hash, exit |
| `--mining` / `--eager-mining` | seal blocks on this node; eagerly on each transaction (`EAGER_MINING`) |
| `--full` | prune old state (default is archive, which the explorer needs) |
| `--zero-gas`, `--gas-limit`, `--max-contract-size`, `--calldata-gas` | EVM and fee settings |
| `--osaka-time <unix>` | schedule the Osaka fork on a live chain (every node, same value) |

The node keeps every block on disk as soon as it is canonical and shuts down gracefully on
SIGTERM/SIGINT. Reth is pinned to one commit (`Cargo.toml` + `Cargo.lock`): upgrading it is a
deliberate `just upgrade-reth <rev>`, tested on a copy of a production datadir first.

### Genesis contracts

| Contract | Address |
|---|---|
| ChainConfig (gas limit, block time) | `0x00000000000000000000000000000000C04F1600` |
| SignerRegistry | `0x000000000000000000000000000000005164EB00` |
| Treasury | `0x0000000000000000000000000000000007EA5B00` |
| Timelock (24 h) | `0x00000000000000000000000000000000714E4C00` |
| Miner proxy (coinbase, EIP-1967) | `0x0000000000000000000000000000000000001967` |
| Multicall3, CREATE2 deployer, WETH9, ERC-4337 EntryPoint v0.7, Safe v1.3.0 | their canonical addresses |

The chain admin from network.toml is the governance of all of them.

## The explorer

A lightweight, self-hosted alternative to Blockscout for any EVM chain: blocks, transactions,
internal calls, logs, tokens (ERC-20/721/1155), holders, verified contracts with read/write tabs,
a calldata and log decoder, and the Blockscout API paths and verification endpoint that existing
links and scripts use. The indexer follows the node's new-block subscription, so on a quiet chain
it does nothing between transactions. See [explorer/README.md](explorer/README.md).

## License

MIT OR Apache-2.0

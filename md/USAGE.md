# Meowchain - Usage Guide

Custom POA blockchain on Reth. Chain ID **9323310**, all hardforks through Prague.

## Directory Structure

```
custom-reth-chain-/
├── src/                            # Rust source code (~16,200 lines, 47 files, 424 tests)
│   ├── main.rs                     # Entry point, block monitoring
│   ├── lib.rs                      # Library root (module declarations)
│   ├── cli.rs                      # Cli struct (clap args)
│   ├── constants.rs                # Shared constants (EXTRA_VANITY, SEAL, etc.)
│   ├── errors.rs                   # Re-exports PoaConsensusError + SignerError
│   ├── output.rs                   # Colored console output functions
│   ├── node/                       # Node type + engine validator
│   │   ├── mod.rs                  # PoaNode + Node/DebugNode impls
│   │   ├── engine.rs               # PoaEngineValidator (97-byte extra_data)
│   │   └── builder.rs              # PoaConsensusBuilder
│   ├── consensus/                  # POA consensus engine
│   │   ├── mod.rs                  # PoaConsensus + all trait impls + tests
│   │   └── errors.rs               # PoaConsensusError enum
│   ├── chainspec/                  # Chain specification
│   │   ├── mod.rs                  # PoaChainSpec + trait impls + tests
│   │   ├── config.rs               # PoaConfig struct
│   │   └── hardforks.rs            # mainnet_compatible_hardforks()
│   ├── genesis/                    # Genesis builder
│   │   ├── mod.rs                  # GenesisConfig + create_genesis() + tests
│   │   ├── addresses.rs            # Contract address constants
│   │   ├── accounts.rs             # dev_accounts(), dev_signers()
│   │   ├── contracts.rs            # System + infra contract allocs
│   │   └── governance.rs           # Governance contract allocs
│   ├── payload/                    # Payload builder
│   │   ├── mod.rs                  # PoaPayloadBuilder + sign_payload + tests
│   │   └── builder.rs              # PoaPayloadBuilderBuilder
│   ├── onchain/                    # On-chain contract reads
│   │   ├── mod.rs                  # StorageReader trait + re-exports + tests
│   │   ├── slots.rs                # Storage slot constants
│   │   ├── selectors.rs            # Function selector computation
│   │   ├── helpers.rs              # Encode/decode, slot computation
│   │   ├── readers.rs              # read_gas_limit(), read_signer_list(), etc.
│   │   └── providers.rs            # StateProviderStorageReader, GenesisStorageReader
│   ├── rpc/                        # Custom RPC namespaces (meow_*, clique_*, admin_*)
│   │   ├── mod.rs                  # MeowRpc + MeowApiServer impl + tests
│   │   ├── api.rs                  # MeowApi trait (#[rpc] macro)
│   │   ├── types.rs                # Response types (ChainConfigResponse, etc.)
│   │   ├── clique.rs               # CliqueRpc (8 methods, 28 tests)
│   │   ├── clique_types.rs         # CliqueSnapshot, CliqueStatus, CliqueProposal
│   │   ├── admin.rs                # AdminRpc (5 methods + health, 24 tests)
│   │   └── admin_types.rs          # AdminNodeInfo, PeerInfo, HealthResponse
│   ├── signer/                     # Signing + key management
│   │   ├── mod.rs                  # Re-exports + tests
│   │   ├── manager.rs              # SignerManager
│   │   ├── sealer.rs               # BlockSealer + signature helpers
│   │   ├── dev.rs                  # DEV_PRIVATE_KEYS, setup_dev_signers()
│   │   └── errors.rs               # SignerError enum
│   ├── evm/
│   │   ├── mod.rs                  # PoaEvmFactory, PoaExecutorBuilder, CalldataDiscountInspector (Phase 2.11-12)
│   │   └── parallel.rs             # TxAccessRecord, ConflictDetector, ParallelSchedule (Phase 2.13)
│   ├── keystore/
│   │   └── mod.rs                  # KeystoreManager (EIP-2335: PBKDF2+AES, 20 tests)
│   ├── cache/
│   │   └── mod.rs                  # HotStateCache, CachedStorageReader, SharedCache (Phase 5)
│   ├── statediff/
│   │   └── mod.rs                  # StateDiff, AccountDiff (Phase 5: replica sync)
│   ├── metrics/
│   │   ├── mod.rs                  # PhaseTimer, BlockMetrics, ChainMetrics (Phase 5)
│   │   └── registry.rs            # MetricsRegistry (19 counters, Prometheus HTTP, 16 tests)
│   └── bytecodes/                  # Pre-compiled contract bytecodes (26 .bin/.hex)
├── genesis/                        # Genesis JSON files
│   ├── sample-genesis.json         # Dev genesis (chain ID 9323310, 38 alloc entries)
│   └── production-genesis.json     # Production genesis (26 alloc entries)
├── genesis-contracts/              # Governance Solidity contracts
│   ├── ChainConfig.sol             # Dynamic chain parameters
│   ├── SignerRegistry.sol          # POA signer management
│   ├── Treasury.sol                # Fee distribution
│   └── Timelock.sol                # Governance timelock (24h delay)
├── Docker/                         # Docker build artifacts
│   ├── Dockerfile                  # Multi-stage build
│   ├── docker-compose.yml          # Single-node compose
│   └── docker-compose-multinode.yml # 3 signer + 1 RPC node compose
├── configs/                        # Developer configuration files
│   ├── hardhat.config.js           # Hardhat network config
│   ├── foundry.toml                # Foundry network config
│   ├── networks.json               # Network registry
│   └── grafana-meowchain.json      # Grafana dashboard template
├── .github/workflows/
│   └── ci.yml                      # GitHub Actions: check, test, clippy, fmt, build-release
├── scoutup-go-explorer/            # Blockscout explorer integration
├── signatures/                     # Contract ABI signatures
│   ├── signatures-contracts.json
│   └── signatures-contracts.txt
├── md/                             # Documentation
│   ├── Remaining.md                # Status tracker + roadmap
│   ├── main.md                     # Strategy notes
│   └── USAGE.md                    # This file
├── CLAUDE.md                       # AI context (architecture, pitfalls)
├── Justfile                        # Build automation
└── Cargo.toml
```

## Quick Start

```bash
# Build (fetches latest reth from main branch + compiles release)
just build

# Quick build without updating deps
just build-fast

# Run in dev mode (auto-mines every 1s, 3 signers, 20 prefunded accounts)
just dev

# Run tests (411 passing)
just test           # with cargo update
just test-fast      # without cargo update
```

## CLI Reference

```
meowchain [OPTIONS]

Options:
  --chain-id <ID>             Chain ID [default: 9323310]
  --block-time <SECONDS>      Block interval in seconds [default: 1]
  --datadir <PATH>            Database directory [default: data]
  --http-addr <ADDR>          HTTP RPC bind address [default: 0.0.0.0]
  --http-port <PORT>          HTTP RPC port [default: 8545]
  --ws-addr <ADDR>            WebSocket RPC bind address [default: 0.0.0.0]
  --ws-port <PORT>            WebSocket RPC port [default: 8546]
  --signer-key <HEX>          Private key for block signing (64 hex chars, no 0x)
                               Also accepts SIGNER_KEY env var
  --production                Production mode: 5 signers, 1B gas, strict POA
  --no-dev                    Disable dev mode (no auto-mining)
  --mining                    Force auto-mining in production mode (for testing)
  --gas-limit <N>             Override block gas limit (e.g., 300000000 for 300M)
  --max-contract-size <BYTES> Override EIP-170 24KB contract size limit (e.g., 524288 for 512KB)
  --calldata-gas <N>          Gas per non-zero calldata byte [1-16, default: 4] (4=POA, 16=mainnet)
  --zero-gas                  Zero-gas mode: base fee=0, gasPrice=0 accepted (no fees)
  --block-time-ms <MS>        Sub-second block interval in ms [default: 0 = use --block-time]
                              Examples: 500 (2/s), 200 (5/s), 100 (10/s)
  --cache-size <N>            Hot state cache entries [default: 1024]
  --eager-mining              Mine immediately on tx arrival instead of interval
  --port <PORT>               P2P listener port [default: 30303]
  --bootnodes <URLs>          Comma-separated enode URLs for peer discovery
  --disable-discovery         Disable P2P peer discovery
  --metrics-interval <N>      Print metrics every N blocks (0=off)
  --enable-metrics            Enable Prometheus metrics HTTP server
  --metrics-port <PORT>       Prometheus metrics port [default: 9001]
  --http-corsdomain <ORIGINS> CORS allowed origins (e.g., "*" or "http://localhost:3000")
  --http-api <APIS>           HTTP RPC namespaces (e.g., "eth,net,web3,meow,clique,admin")
  --ws-api <APIS>             WebSocket RPC namespaces
  --log-json                  Output structured JSON logs
  --rpc-max-connections <N>   Max concurrent RPC connections [default: 100]
  --rpc-max-request-size <MB> Max RPC request size in MB [default: 15]
  --rpc-max-response-size <MB> Max RPC response size in MB [default: 150]
  --archive                   Run as archive node (no state pruning)
  --gpo-blocks <N>            Gas price oracle: blocks to sample [default: 20]
  --gpo-percentile <N>        Gas price oracle: percentile [default: 60]
  -h, --help                  Print help
```

## Running Modes

### Dev Mode (default)

Auto-mines blocks every 1 second (default). Relaxed consensus (no signature verification). 20 prefunded accounts with 10,000 ETH each. 3 default signers loaded automatically. 300M gas limit. Use `--block-time-ms 500` for 500ms blocks, `--block-time-ms 200` for 200ms, etc.

```bash
# Standard dev mode
just dev

# Or directly
cargo run --release

# With custom block time (1 second)
cargo run --release -- --block-time 1

# With eager mining (mine on every tx, not just interval)
cargo run --release -- --eager-mining

# With higher gas limit
cargo run --release -- --gas-limit 100000000

# With custom signer key (env var)
SIGNER_KEY=ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 cargo run --release

# With custom signer key (flag)
cargo run --release -- --signer-key ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80
```

**Expected output:**
```
=== Meowchain POA Node ===
Chain ID:        9323310
Block period:    1s
Mode:            dev
Authorized signers (3):
  1. 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266
  2. 0x70997970C51812dc3A010C7d01b50e0d17dc79C8
  3. 0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC
...
  OnChain gas limit: 300000000 (from ChainConfig)
  OnChain signers: 3 loaded from SignerRegistry
...
  Block #1 - 0 txs (in-turn: 0x70997970C51812dc3A010C7d01b50e0d17dc79C8)
  Block #2 - 0 txs (in-turn: 0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC)
```

### Production Mode

Strict POA signature verification. 5 signers. 60M gas limit. 97-byte extra_data (vanity + ECDSA seal). Blocks are only produced via Engine API (no auto-mining) unless `--mining` is used.

```bash
# Production mode (no auto-mining — needs Engine API / CL client)
just run-production

# With explicit signer key
cargo run --release -- --production --signer-key ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80

# Production + auto-mining (for testing without CL client)
cargo run --release -- --production --mining \
  --signer-key ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80

# Production with custom block time
cargo run --release -- --production --mining --block-time 12 \
  --signer-key ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80
```

**Expected output (production+mining):**
```
=== Meowchain POA Node ===
Chain ID:        9323310
Block period:    2s
Mode:            production+mining
Authorized signers (5):
  1. 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266
  ...
Signer key loaded: 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266
...
  OnChain gas limit: 1000000000 (from ChainConfig)
  OnChain signers: 5 loaded from SignerRegistry
POA Consensus initialized: 5 signers, epoch: 30000, period: 2s, mode: production (strict)
...
  OK POA block #1 signed by 0xf39Fd6... (out-of-turn, build=3ms sign=1ms)
  OK POA block #5 signed by 0xf39Fd6... (in-turn, build=2ms sign=1ms)
```

> **Note:** With a single signer key, blocks are "out-of-turn" except when it's that signer's round-robin slot (every Nth block where N = number of signers). In a multi-node setup, each node runs with its own key and produces blocks in-turn.

### Multi-Node Setup

```bash
# Node 1 (signer 0)
cargo run --release -- --production --mining \
  --signer-key ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 \
  --port 30303 --http-port 8545 --ws-port 8546 --datadir data1

# Node 2 (signer 1) — connects to node 1 as bootnode
cargo run --release -- --production --mining \
  --signer-key 59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d \
  --port 30304 --http-port 8547 --ws-port 8548 --datadir data2 \
  --bootnodes enode://<NODE1_PUBKEY>@127.0.0.1:30303

# Node 3 (signer 2) — connects to node 1
cargo run --release -- --production --mining \
  --signer-key 5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a \
  --port 30305 --http-port 8549 --ws-port 8550 --datadir data3 \
  --bootnodes enode://<NODE1_PUBKEY>@127.0.0.1:30303

# Non-signer full node (sync only, no block production)
cargo run --release -- --production \
  --port 30306 --http-port 8551 --ws-port 8552 --datadir data4 \
  --bootnodes enode://<NODE1_PUBKEY>@127.0.0.1:30303
```

> **Tip:** Use `--disable-discovery` for isolated testing without P2P noise.

### Custom Args (Justfile)

```bash
just run-custom -- --block-time 4 --gas-limit 100000000 --eager-mining
just run-custom -- --production --mining --block-time 1 --signer-key <KEY>
```

## Chain Configuration

| Parameter | Dev | Production |
|-----------|-----|------------|
| Chain ID | 9323310 | 9323310 |
| Block time | 1s (configurable via `--block-time` / `--block-time-ms`) | 2s (configurable) |
| Gas limit | 300M (on-chain ChainConfig) | 1B (on-chain ChainConfig) |
| Signers | 3 (accounts 0-2) | 5 (accounts 0-4) |
| Prefunded accounts | 20 @ 10,000 ETH | 8 (tiered allocation) |
| Coinbase | EIP-1967 Miner Proxy | EIP-1967 Miner Proxy |
| Consensus | Relaxed (no sig check) | Strict POA (97-byte extra_data) |
| Epoch | 30,000 blocks | 30,000 blocks |
| P2P port | 30303 | 30303 |
| Mining | Auto (interval) | Engine API or `--mining` flag |
| Gas fees | EIP-1559 (or `--zero-gas` for free) | EIP-1559 (or `--zero-gas` for free) |

## RPC Endpoints

After node starts:
- **HTTP**: `http://localhost:8545` (or your `--http-addr:--http-port`)
- **WebSocket**: `ws://localhost:8546` (or your `--ws-addr:--ws-port`)
- **Auth RPC**: `127.0.0.1:8551` (Engine API, internal)

### Standard eth_* Methods

```bash
# Block number
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'

# Chain ID (returns 0x8e432e = 9323310)
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}'

# Balance (returns hex wei — 10,000 ETH = 0x21e19e0c9bab2400000)
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_getBalance","params":["0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266","latest"],"id":1}'

# Get block by number (with full tx objects)
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["latest",true],"id":1}'

# Get contract code (verify pre-deployed contracts)
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_getCode","params":["0x00000000000000000000000000000000C04F1600","latest"],"id":1}'

# Read contract storage slot (ChainConfig gas limit at slot 1)
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_getStorageAt","params":["0x00000000000000000000000000000000C04F1600","0x1","latest"],"id":1}'
```

### meow_* Custom RPC

```bash
# On-chain ChainConfig parameters (gasLimit, blockTime, signerCount, contract addresses)
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"meow_chainConfig","params":[],"id":1}'
# Returns: {"chainId":9323310,"gasLimit":30000000,"blockTime":2,"epoch":30000,"signerCount":3,
#   "governanceSafe":"0x...6f5afe00","chainConfigContract":"0x...c04f1600",
#   "signerRegistryContract":"0x...5164eb00","treasuryContract":"0x...7ea5b00"}

# Active signers (from on-chain SignerRegistry)
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"meow_signers","params":[],"id":1}'
# Returns: ["0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266","0x70997970...","0x3c44cddd..."]

# Node info (dev mode, local signers, authorized signers)
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"meow_nodeInfo","params":[],"id":1}'
# Returns: {"chainId":9323310,"devMode":true,"signerCount":3,"localSignerCount":3,
#   "localSigners":[...],"authorizedSigners":[...]}
```

### clique_* POA RPC

Standard Clique POA namespace with 8 methods for signer management and snapshot queries.

```bash
# Get current authorized signers
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"clique_getSigners","params":[],"id":1}'
# Returns: ["0xf39fd6e5...", "0x70997970...", "0x3c44cddd..."]

# Get signers at a specific block hash
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"clique_getSignersAtHash","params":["0xabc..."],"id":1}'

# Get consensus snapshot (signers, votes, tally)
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"clique_getSnapshot","params":[],"id":1}'

# Propose adding a new signer (true=add, false=remove)
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"clique_propose","params":["0xNewSignerAddress", true],"id":1}'

# Discard a pending proposal
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"clique_discard","params":["0xNewSignerAddress"],"id":1}'

# Get signing status (inturn count, total blocks signed)
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"clique_status","params":[],"id":1}'

# Get all pending proposals
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"clique_proposals","params":[],"id":1}'
```

### admin_* Node Administration RPC

Admin namespace with 5 methods for node management and a health check endpoint for load balancers.

```bash
# Node info (enode URL, network, protocols)
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"admin_nodeInfo","params":[],"id":1}'

# Connected peers
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"admin_peers","params":[],"id":1}'

# Add a peer manually
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"admin_addPeer","params":["enode://<pubkey>@<ip>:30303"],"id":1}'

# Remove a peer
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"admin_removePeer","params":["enode://<pubkey>@<ip>:30303"],"id":1}'

# Health check (for load balancers — returns syncing status, peer count, latest block)
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"admin_health","params":[],"id":1}'
# Returns: {"healthy":true,"syncing":false,"peerCount":4,"latestBlock":12345}
```

## Keystore Management

EIP-2335 compatible encrypted key storage using PBKDF2-HMAC-SHA256 + AES-128-CTR.

```bash
# Keystore files are stored in <datadir>/keystore/
# Each file is a JSON file with encrypted private key material

# Import a signer key into the keystore (programmatic API via KeystoreManager)
# The KeystoreManager provides:
#   create_account(password)       - Generate new key and encrypt
#   import_key(private_key, password) - Import existing key and encrypt
#   decrypt_key(address, password) - Decrypt and return private key
#   list_accounts()                - List all stored addresses
#   delete_account(address)        - Remove encrypted key file
#   load_into_signer_manager(password, signer_manager) - Decrypt all keys into SignerManager
```

## Prometheus Metrics

Enable Prometheus scraping with `--enable-metrics`:

```bash
# Start node with Prometheus metrics enabled
cargo run --release -- --enable-metrics --metrics-port 9001

# Scrape metrics (plain text format)
curl http://localhost:9001/metrics

# Sample output:
# meowchain_blocks_produced 1234
# meowchain_blocks_in_turn 823
# meowchain_blocks_out_of_turn 411
# meowchain_transactions_processed 56789
# meowchain_gas_used_total 1234567890
# meowchain_peer_count 4
# meowchain_chain_head 1234
# ...
```

A Grafana dashboard template is available at `configs/grafana-meowchain.json`. Import it in Grafana with the Prometheus data source pointed at the node.

## Testing with Foundry (cast)

```bash
# Check block number
cast block-number --rpc-url http://localhost:8545

# Check balance (human-readable ETH)
cast balance 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266 --rpc-url http://localhost:8545 --ether

# Send 1 ETH
cast send \
  --private-key 0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 \
  --rpc-url http://localhost:8545 \
  0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
  --value 1ether

# Deploy a contract
forge create MyContract \
  --private-key 0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 \
  --rpc-url http://localhost:8545

# Read contract storage (ChainConfig gas limit at slot 1)
cast storage 0x00000000000000000000000000000000C04F1600 1 --rpc-url http://localhost:8545

# Read Timelock minDelay (slot 0 = 86400 = 24 hours)
cast storage 0x00000000000000000000000000000000714E4C00 0 --rpc-url http://localhost:8545

# Read SignerRegistry signer count (slot 1)
cast storage 0x000000000000000000000000000000005164EB00 1 --rpc-url http://localhost:8545

# Verify contract code exists
cast code 0x00000000000000000000000000000000714E4C00 --rpc-url http://localhost:8545

# Get block with extra data (97 bytes in production = POA signature)
cast block latest --rpc-url http://localhost:8545
```

## Prefunded Dev Accounts

From mnemonic: `test test test test test test test test test test test junk`

| # | Address | Private Key | Role |
|---|---------|-------------|------|
| 0 | `0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266` | `ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80` | Signer (dev+prod) |
| 1 | `0x70997970C51812dc3A010C7d01b50e0d17dc79C8` | `59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d` | Signer (dev+prod) |
| 2 | `0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC` | `5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a` | Signer (dev+prod) |
| 3 | `0x90F79bf6EB2c4f870365E785982E1f101E93b906` | `7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6` | Signer (prod only) |
| 4 | `0x15d34AAf54267DB7D7c367839AAf71A00a2C6A65` | `47e179ec197488593b187f80a00eb0da91f1b9d0b13f8733639f19c30a34926a` | Signer (prod only) |
| 5-19 | See `src/genesis.rs:dev_accounts()` | Standard Foundry dev keys | Prefunded only |

- **Dev mode**: accounts 0-2 are signers (3 signers, round-robin)
- **Production mode**: accounts 0-4 are signers (5 signers, round-robin)
- All 20 accounts prefunded with 10,000 ETH in dev, 8 accounts tiered in production

## Genesis Files

| File | Allocs | Purpose |
|------|--------|---------|
| `genesis/sample-genesis.json` | 38 | Dev — 20 prefunded + 18 contracts |
| `genesis/production-genesis.json` | 26 | Production — 8 prefunded + 18 contracts |

Regenerate from code:
```bash
just genesis                    # regenerates sample-genesis.json
cargo test test_regenerate      # regenerates both genesis files
```

## Pre-deployed Contracts

All deployed at genesis block 0. No deployment transaction needed.

### System Contracts (EIP)

| Contract | Address | EIP |
|----------|---------|-----|
| EIP-1967 Miner Proxy (coinbase) | `0x0000000000000000000000000000000000001967` | Block rewards |
| EIP-4788 Beacon Block Root | `0x000F3df6D732807Ef1319fB7B8bB8522d0Beac02` | Cancun |
| EIP-2935 History Storage | `0x0000F90827F1C53a10cb7A02335B175320002935` | Prague |
| EIP-7002 Withdrawal Requests | `0x00000961Ef480Eb55e80D19ad83579A64c007002` | Prague |
| EIP-7251 Consolidation Requests | `0x0000BBdDc7CE488642fb579F8B00f3a590007251` | Prague |

### Infrastructure Contracts

| Contract | Address |
|----------|---------|
| ERC-4337 EntryPoint v0.7 | `0x0000000071727De22E5E9d8BAf0edAc6f37da032` |
| WETH9 | `0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2` |
| Multicall3 | `0xcA11bde05977b3631167028862bE2a173976CA11` |
| CREATE2 Deployer | `0x4e59b44847b379578588920cA78FbF26c0B4956C` |
| SimpleAccountFactory (ERC-4337) | `0x9406Cc6185a346906296840746125a0E44976454` |

### Governance Contracts

| Contract | Address | Storage |
|----------|---------|---------|
| ChainConfig | `0x00000000000000000000000000000000C04F1600` | slot 0: governance, slot 1: gasLimit, slot 2: blockTime |
| SignerRegistry | `0x000000000000000000000000000000005164EB00` | slot 0: governance, slot 1: signers.length |
| Treasury | `0x0000000000000000000000000000000007EA5B00` | Fee distribution |
| Timelock | `0x00000000000000000000000000000000714E4C00` | slot 0: minDelay (86400s), slot 1: proposer, slot 4: paused |
| Governance Safe (reserved) | `0x000000000000000000000000000000006F5AFE00` | Multisig |

### Gnosis Safe v1.3.0

| Contract | Address |
|----------|---------|
| Safe Singleton | `0xd9Db270c1B5E3Bd161E8c8503c55cEABeE709552` |
| Safe Proxy Factory | `0xa6B71E26C5e0845f74c812102Ca7114b6a896AB2` |
| Safe Fallback Handler | `0xf48f2B2d2a534e402487b3ee7C18c33Aec0Fe5e4` |
| Safe MultiSend | `0xA238CBeb142c10Ef7Ad8442C6D1f9E89e07e7761` |

## Performance Features (Phase 2)

### Sub-Second Block Times

```bash
# 500ms blocks (2 blocks/s)
cargo run --release -- --block-time-ms 500

# 200ms blocks (5 blocks/s)
cargo run --release -- --block-time-ms 200

# 100ms blocks (10 blocks/s)
cargo run --release -- --block-time-ms 100
```

### Gas Limit Override

```bash
# 100M gas
cargo run --release -- --gas-limit 100000000

# 300M gas (dev default)
cargo run --release -- --gas-limit 300000000

# 1B gas (production default)
cargo run --release -- --gas-limit 1000000000
```

### Contract Size Override

```bash
# 128KB contracts (vs 24KB Ethereum default)
cargo run --release -- --max-contract-size 131072

# 512KB contracts
cargo run --release -- --max-contract-size 524288
```

### Calldata Gas Reduction

```bash
# 4 gas/byte (default, cheap calldata)
cargo run --release -- --calldata-gas 4

# 1 gas/byte (maximum reduction)
cargo run --release -- --calldata-gas 1

# 16 gas/byte (Ethereum mainnet behaviour)
cargo run --release -- --calldata-gas 16
```

### Zero-Gas Mode

Run the chain with no gas fees. Transactions still have gas limits (preventing infinite loops), but users pay nothing. Useful for private/consortium chains.

```bash
# Enable zero-gas mode (base fee=0, gasPrice=0 accepted)
cargo run --release -- --zero-gas

# Combine with other flags
cargo run --release -- --zero-gas --gas-limit 1000000000 --block-time 1

# Production + zero-gas
cargo run --release -- --production --mining --zero-gas
```

When `--zero-gas` is enabled:
- Genesis `base_fee_per_gas` is set to 0 (stays 0 forever via EIP-1559 formula)
- revm's `disable_base_fee` is set in CfgEnv (skips base fee validation)
- Transactions with `gasPrice: 0` / `maxFeePerGas: 0` are accepted
- Gas is still metered for execution limits (no infinite loops)

### Metrics Output

```bash
# Print performance metrics every 10 blocks (default)
cargo run --release -- --metrics-interval 10

# Every 100 blocks
cargo run --release -- --metrics-interval 100

# Disable metrics
cargo run --release -- --metrics-interval 0
```

**Sample metrics output:**
```
  [metrics] block=100 total_txs=2450 in_turn_rate=66.7%
```

**Sample block output (production mode, build+sign timing):**
```
  OK POA block #42 signed by 0xf39Fd6... (in-turn, build=3ms sign=1ms)
  ~ Block #42: 5 accounts, 12 storage slots changed
```

## Hardhat / Foundry Config

### Hardhat (`hardhat.config.ts`)

```typescript
networks: {
  meowchain: {
    url: "http://localhost:8545",
    chainId: 9323310,
    accounts: [
      "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
    ],
  }
}
```

### Foundry (`foundry.toml`)

```toml
[profile.meowchain]
eth_rpc_url = "http://localhost:8545"
chain_id = 9323310
```

```bash
forge script MyScript --rpc-url http://localhost:8545 --chain-id 9323310 --broadcast \
  --private-key 0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80
```

## MetaMask Setup

1. Open MetaMask -> Settings -> Networks -> Add Network
2. Fill in:
   - **Network Name**: Meowchain
   - **RPC URL**: `http://localhost:8545`
   - **Chain ID**: `9323310`
   - **Currency Symbol**: `ETH`
3. Import a dev account private key to get 10,000 ETH

## Docker

```bash
# Build image (Dockerfile is in Docker/ subdir)
docker build -f Docker/Dockerfile -t meowchain .

# Or use compose (includes volume mount for persistence)
docker compose -f Docker/docker-compose.yml up

# Exposed ports in docker-compose:
#   8545  - HTTP RPC
#   8546  - WebSocket RPC
#   30303 - P2P (TCP + UDP)
#   9001  - Metrics
```

### Multi-Node Docker Setup

Run a 3-signer + 1 RPC node network with Docker Compose:

```bash
# Start the multi-node network (3 signers + 1 RPC node)
just docker-multinode
# or directly:
docker compose -f Docker/docker-compose-multinode.yml up

# The compose file sets up:
#   signer1: port 8545 (HTTP), 30303 (P2P) — signer account 0
#   signer2: port 8547 (HTTP), 30304 (P2P) — signer account 1
#   signer3: port 8549 (HTTP), 30305 (P2P) — signer account 2
#   rpc:     port 8551 (HTTP), 30306 (P2P) — non-signer, read-only

# Query any node:
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'

# Check peer count (should show 3 peers per signer node):
curl -s http://localhost:8545 -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"admin_peers","params":[],"id":1}'
```

## Developer Configuration Files

Pre-built configuration files are in `configs/`:

### Hardhat (`configs/hardhat.config.js`)

```javascript
// Import directly or copy to your Hardhat project:
// cp configs/hardhat.config.js hardhat.config.js
module.exports = {
  networks: {
    meowchain: {
      url: "http://localhost:8545",
      chainId: 9323310,
      accounts: [
        "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
      ],
    }
  }
};
```

### Foundry (`configs/foundry.toml`)

```toml
[profile.meowchain]
eth_rpc_url = "http://localhost:8545"
chain_id = 9323310
```

### Grafana Dashboard (`configs/grafana-meowchain.json`)

Import into Grafana for Prometheus-based monitoring. Requires `--enable-metrics` on the node.

## Justfile Recipes

```bash
just build          # cargo update + cargo build --release
just build-fast     # cargo build --release (skip update)
just build-debug    # cargo update + cargo build (debug)
just dev            # cargo update + run dev mode
just test           # cargo update + cargo test
just test-fast      # cargo test (skip update)
just run-production # cargo update + run --production --block-time 12
just run-custom     # cargo update + run with custom args
just docker         # build + docker build (single node)
just docker-multinode # 3 signer + 1 RPC node compose
just genesis        # regenerate sample-genesis.json
just check          # cargo update + cargo check
just fmt            # cargo fmt
just lint           # cargo update + cargo clippy
just clean          # cargo clean
```

## Data Directory

```
data/
├── db/                    # MDBX database
│   ├── mdbx.dat           # Main database file
│   ├── mdbx.lck           # Lock file
│   └── database.version
├── static_files/          # Headers, transactions, receipts
├── blobstore/             # EIP-4844 blob storage
├── jwt.hex                # JWT secret (Engine API auth)
├── discovery-secret       # P2P node identity
└── reth.toml              # Reth configuration
```

Clean restart:
```bash
rm -rf data/
```

## Troubleshooting

| Problem | Fix |
|---------|-----|
| Port 8545 in use | `pkill -9 -f example_custom_poa` or change `--http-port` |
| Port 30303 in use | `pkill -9 -f example_custom_poa` or change `--port` |
| Port 8551 (auth) in use | Kill zombie processes: `pkill -9 -f example_custom_poa` then wait 3s |
| Database errors | `rm -rf data/` and restart |
| Blocks not mining (dev) | Don't use `--production` or `--no-dev` |
| Blocks not mining (prod) | Use `--mining` flag or connect Engine API / CL client |
| Signer not producing | Verify `--signer-key` matches a registered signer address |
| RPC not responding | Node initializing; wait ~5s and retry |
| "invalid transaction request" | Use `cast send` with `--private-key` (Reth doesn't support `eth_sendTransaction`) |
| Out-of-turn blocks | Normal with single signer — only in-turn on every Nth block (N = signer count) |
| Wrong gas limit | Gas limit is read from ChainConfig contract on-chain, not just CLI flag |

## Environment Variables

| Variable | Description |
|----------|-------------|
| `SIGNER_KEY` | Private key for block signing (alternative to `--signer-key` flag) |
| `RUST_LOG` | Log level: `info`, `debug`, `trace` (default: `info`) |
| `RUST_BACKTRACE` | Enable backtraces: `1` or `full` |

## Testing

### Running Tests

```bash
# Run all 424 tests (with cargo update)
just test

# Run tests without cargo update (faster)
just test-fast

# Run tests directly
cargo test

# Run specific module tests
cargo test consensus::        # 59 consensus tests
cargo test onchain::          # 55 on-chain storage tests
cargo test genesis::          # 33 genesis builder tests
cargo test chainspec::        # 27 chainspec tests
cargo test evm::              # 41 EVM tests (16 factory + 25 parallel)
cargo test rpc::              # 61 RPC tests (9 meow + 28 clique + 24 admin)
cargo test statediff::        # 28 state diff tests
cargo test signer::           # 21 signer tests
cargo test cache::            # 20 cache tests
cargo test keystore::         # 20 keystore tests
cargo test metrics::          # 19 metrics tests
cargo test payload::          # 16 payload builder tests
cargo test node::             # 8 node tests
cargo test output::           # 4 output formatting tests

# Run a single test by name
cargo test test_sync_long_chain_100_blocks
cargo test test_admin_health_production_with_signer_is_healthy

# Run tests with output shown
cargo test -- --nocapture

# Run tests matching a pattern
cargo test multi_node          # All multi-node integration tests
cargo test validate_header     # All header validation tests
cargo test json_serialization  # All JSON serialization tests
```

### Test Suite Overview (424 tests)

All tests run in ~0.4s. No external services, databases, or network access needed.

| Module | Tests | Source File | What's Tested |
|--------|-------|-------------|---------------|
| **consensus** | 59 | `src/consensus/mod.rs` | POA consensus engine |
| **onchain** | 55 | `src/onchain/mod.rs` | On-chain contract storage reads |
| **genesis** | 33 | `src/genesis/mod.rs` | Genesis block builder |
| **statediff** | 28 | `src/statediff/mod.rs` | State diff for replica sync |
| **clique rpc** | 28 | `src/rpc/clique.rs` | Clique POA RPC namespace |
| **chainspec** | 27 | `src/chainspec/mod.rs` | Chain specification + live signers |
| **evm::parallel** | 25 | `src/evm/parallel.rs` | Parallel EVM scheduling |
| **admin rpc** | 24 | `src/rpc/admin.rs` | Admin RPC + health endpoint |
| **signer** | 21 | `src/signer/mod.rs` | Key management + block sealing |
| **cache** | 20 | `src/cache/mod.rs` | LRU cache for hot state |
| **keystore** | 20 | `src/keystore/mod.rs` | Encrypted key storage (EIP-2335) |
| **metrics** | 19 | `src/metrics/mod.rs` | Performance tracking + reporting |
| **evm** | 16 | `src/evm/mod.rs` | EVM factory + calldata discount |
| **payload** | 16 | `src/payload/mod.rs` | Payload builder + signing |
| **meow rpc** | 9 | `src/rpc/mod.rs` | Custom meow_* RPC namespace |
| **node** | 8 | `src/node/mod.rs` | Node type + engine validator |
| **output** | 4 | `src/output.rs` | Console output formatting |

### Test Categories

#### Consensus Tests (59)

Validates the full POA consensus engine across all Reth consensus traits.

**Header validation:**
- `test_validate_header_with_valid_signature` — valid POA signature accepted
- `test_validate_header_unauthorized_signer` — unauthorized signer rejected
- `test_validate_header_short_extra_data_dev_mode` — short extra_data allowed in dev
- `test_validate_header_short_extra_data_production` — short extra_data rejected in production
- `test_validate_header_against_parent_valid` — valid parent→child chain accepted
- `test_validate_header_against_parent_wrong_hash` — wrong parent hash rejected
- `test_validate_header_against_parent_wrong_number` — wrong block number rejected
- `test_validate_header_against_parent_timestamp_too_early` — too-fast blocks rejected
- `test_validate_header_against_parent_timestamp_exact_period` — exact period timing accepted
- `test_validate_header_against_parent_gas_limit_increase_too_large` — gas limit bounds enforced
- `test_validate_header_against_parent_gas_limit_decrease_too_large` — gas limit bounds enforced
- `test_validate_header_against_parent_gas_limit_exact_boundary` — boundary conditions

**Post-execution validation:**
- `test_validate_block_post_execution_gas_used_match` — gas_used matches receipts
- `test_validate_block_post_execution_gas_used_mismatch` — mismatch detected
- `test_validate_block_post_execution_receipt_root_mismatch` — receipt root verified
- `test_validate_block_post_execution_logs_bloom_mismatch` — bloom filter verified
- `test_validate_block_post_execution_no_receipt_root` — missing receipt root handled

**Difficulty + body validation:**
- `test_validate_difficulty_zero` / `test_validate_difficulty_nonzero_rejected` / `test_validate_difficulty_zero_any_signer`
- `test_validate_body_against_header_gas_ok` / `test_validate_body_against_header_gas_exceeds`
- `test_validate_signer_authorized` / `test_validate_signer_unauthorized`

**Signer recovery + sealing:**
- `test_recover_signer_valid_signature` — ECDSA recovery from extra_data
- `test_recover_signer_short_extra_data` — graceful error on short data
- `test_seal_hash_strips_signature` — seal hash excludes signature bytes
- `test_extract_signers_from_epoch_block` — signer list extracted at epoch
- `test_extract_signers_invalid_length` — invalid signer data rejected
- `test_full_signed_block_passes_all_consensus` — end-to-end signed block validation

**Multi-node / fork choice:**
- `test_is_in_turn_block1` / `test_is_in_turn_round_robin` — round-robin scheduling
- `test_score_chain_empty` / `test_score_chain_all_in_turn` / `test_score_chain_all_out_of_turn` / `test_score_chain_mixed`
- `test_compare_chains_in_turn_wins` — fork choice prefers in-turn blocks
- `test_compare_chains_equal_score_longer_wins` — longer chain wins ties
- `test_3_signer_round_robin_production` — 3-signer network produces valid chain
- `test_3_signer_out_of_turn_accepted` — out-of-turn blocks still valid
- `test_3_signer_unauthorized_signer_rejected` — unauthorized signer blocked
- `test_3_signer_missed_turns_and_catchup` — missed turns handled gracefully
- `test_multi_node_5_signer_sequential` — 5-signer sequential block production
- `test_multi_node_double_sign_detection` — double-signing detected
- `test_multi_node_signer_addition_at_epoch` — signer added at epoch boundary
- `test_multi_node_signer_removal_at_epoch` — signer removed at epoch boundary
- `test_multi_node_fork_choice_prefers_in_turn` — fork choice integration test
- `test_multi_node_chain_reorganization` — chain reorg handling

**Sync validation:**
- `test_sync_chain_of_10_blocks` — validates chain of 10 blocks
- `test_sync_long_chain_100_blocks` — validates chain of 100 blocks
- `test_sync_rejects_unauthorized_signer` — blocks from unauthorized signers rejected during sync
- `test_sync_rejects_wrong_parent_hash` — broken parent links detected
- `test_sync_rejects_tampered_signature` — tampered signatures caught

**Dev mode + epoch:**
- `test_consensus_creation` / `test_consensus_dev_mode` / `test_consensus_with_dev_mode`
- `test_epoch_block_detection`

#### On-Chain Storage Tests (55)

Tests the storage reader abstraction for reading governance contract state.

**StorageReader trait + mock:**
- `test_mock_storage_read_write` / `test_mock_storage_missing_returns_none`
- `test_read_gas_limit_from_mock` / `test_read_signer_list_from_mock` / `test_read_signer_list_empty`
- `test_read_block_time_from_mock` / `test_read_chain_config_from_mock` / `test_read_chain_config_missing_returns_none`
- `test_read_timelock_delay_from_mock` / `test_is_signer_on_chain_mock`

**Genesis storage reader (reads from genesis alloc):**
- `test_genesis_reader_reads_chain_config` / `test_genesis_reader_reads_production_chain_config`
- `test_genesis_reader_reads_signer_list` / `test_genesis_reader_reads_production_signer_list`
- `test_genesis_reader_custom_gas_limit` / `test_genesis_reader_custom_block_time` / `test_genesis_reader_custom_signers`
- `test_genesis_reader_gas_limit_shortcut` / `test_genesis_reader_block_time_shortcut`
- `test_genesis_reader_is_signer_check` / `test_genesis_reader_production_is_signer_check`
- `test_genesis_reader_single_signer` / `test_genesis_reader_21_signers`
- `test_genesis_reader_nonexistent_address` / `test_genesis_reader_nonexistent_slot`
- `test_read_timelock_delay_from_genesis` / `test_read_timelock_proposer_from_genesis`
- `test_timelock_in_production_genesis` / `test_timelock_not_paused_at_genesis`

**Encoding/decoding helpers:**
- `test_encode_decode_address_roundtrip` / `test_encode_decode_u64_roundtrip`
- `test_encode_address_is_left_padded` / `test_decode_address_zero` / `test_decode_u64_zero` / `test_decode_bool_true_and_false`

**Storage slot computation:**
- `test_chain_config_slot_values` / `test_signer_registry_slot_values`
- `test_mapping_slot_matches_genesis` / `test_mapping_slot_different_addresses_different_slots` / `test_mapping_slot_different_base_different_slots`
- `test_dynamic_array_slot_0` / `test_dynamic_array_base_slot_deterministic` / `test_dynamic_array_base_slot_matches_genesis` / `test_dynamic_array_different_slots_different_bases`

**Function selectors:**
- `test_function_selector_computation` / `test_different_functions_different_selectors` / `test_selector_length_always_4_bytes`

**Data structure equality:**
- `test_chain_config_reader_matches_genesis_config` / `test_signer_list_reader_matches_genesis_config`
- `test_dynamic_chain_config_equality` / `test_dynamic_chain_config_debug` / `test_dynamic_signer_list_equality`

**Simulation (governance changes):**
- `test_simulate_gas_limit_change` — gas limit update via ChainConfig
- `test_simulate_block_time_change_to_1_second` — block time update
- `test_simulate_signer_addition` — signer added via SignerRegistry

#### Genesis Tests (33)

Tests genesis block construction with all pre-deployed contracts and accounts.

- Dev/production genesis creation and validation
- All 20 dev accounts prefunded with 10,000 ETH
- Production tiered funding (8 accounts)
- 18 contract allocs (system + infra + governance + Safe)
- All contract bytecodes non-empty and addresses unique
- Extra_data format with embedded signer addresses
- ERC-4337, Gnosis Safe, and governance contracts present
- Genesis JSON serialization roundtrip
- Deterministic genesis regeneration (`test_regenerate_sample_genesis`, `test_regenerate_production_genesis`)

#### RPC Tests (61 total)

**meow_* namespace (9 tests):**
- `test_meow_chain_config` — returns chain parameters
- `test_meow_chain_config_production` — production config (1B gas, 5 signers)
- `test_meow_chain_config_governance_addresses` — governance contract addresses
- `test_meow_signers` / `test_meow_signers_empty` — signer list
- `test_meow_node_info` / `test_meow_node_info_no_signers` / `test_meow_node_info_multiple_signers` — node status
- `test_chain_config_response_json_serialization` — camelCase JSON output

**clique_* namespace (28 tests):**
- `test_get_signers_*` — getSigners, getSignersAtHash, effective signers, empty, production 5-signer
- `test_snapshot_*` — getSnapshot, getSnapshotAtHash, signers, empty, proposals as votes, effective signers
- `test_propose_*` — propose add/remove, overwrite, multiple proposals, discard lifecycle
- `test_discard_*` — discard removes proposal, discard nonexistent is noop
- `test_status_*` — status with signer count, empty signers, effective signers, activity initialized to zero
- `test_clique_*_json_*` — JSON serialization for all response types (camelCase)
- `test_rpc_with_loaded_signer` — end-to-end with actual signer key

**admin_* namespace (24 tests):**
- `test_admin_node_info_*` — returns chain ID, name, ports, genesis hash, clique config, JSON format
- `test_admin_peers_initially_empty` — peer list starts empty
- `test_admin_add_peer_*` — valid enode, invalid enode, duplicate detection
- `test_admin_remove_peer_*` — remove existing, remove nonexistent
- `test_admin_add_multiple_peers_then_remove_one` — add 3 peers, remove 1, verify 2 remain
- `test_admin_health_*` — dev mode always healthy, production with signer healthy, production without signer/peers unhealthy, uptime increases, JSON format
- `test_parse_enode_*` — enode URL parsing (id extraction, address extraction, invalid prefix, missing @)
- `test_node_version_constant` — version string format

#### State Diff Tests (28)

Tests the state diff builder for replica state streaming.

- Builder records balance/nonce/code/storage changes
- Builder ignores no-op changes (same value)
- Builder accumulates multiple changes to same account
- Diff apply/verify operations
- Storage change counting and estimation
- Slot diff change detection
- Gas and tx count metadata

#### EVM Tests (41 total)

**PoaEvmFactory + CalldataDiscount (16 tests):**
- `test_poa_evm_factory_ethereum_default_is_24kb` — default 24KB contract limit
- `test_poa_evm_factory_applies_code_size_limit` — custom contract size limit applied
- `test_poa_evm_factory_sets_initcode_limit_double` — initcode = 2× contract size
- `test_poa_evm_factory_no_override_keeps_default` / `test_poa_evm_factory_default_calldata_gas_is_4`
- `test_poa_evm_factory_at_16_no_discount` — 16 gas/byte = no discount (mainnet)
- `test_calldata_discount_inspector_discount_at_4_gas` — 4 gas/byte discount calculation
- `test_calldata_discount_inspector_discount_at_1_gas` — 1 gas/byte (maximum discount)
- `test_calldata_discount_inspector_no_discount_at_16_gas` — 16 = Ethereum standard
- `test_calldata_discount_inspector_discount_for_zero_bytes` — zero bytes unaffected
- `test_calldata_discount_inspector_clamps_cost_to_1` / `test_calldata_discount_inspector_clamps_cost_to_16`
- `test_patch_env_does_not_change_other_fields`
- `test_poa_executor_builder_creation` / `test_poa_executor_builder_no_override`

**Parallel EVM (25 tests):**
- `test_tx_access_record_*` — read/write tracking per transaction
- `test_access_key_*` — account and storage access key types
- `test_conflict_*` — WAW, WAR, RAW hazard detection between transactions
- `test_no_conflict_*` — disjoint reads, disjoint writes, same read-only slot
- `test_schedule_*` — batch scheduling (independent=1 batch, conflict=multiple batches, chain, mixed)
- `test_parallel_executor_*` — executor handles empty, single tx, conflicts, gaps

#### Chainspec Tests (27)

- Chain creation (dev/production), chain ID, signers, epoch
- Hardfork activation (Frontier through Prague)
- Fork ID computation
- Live signers (shared `Arc<RwLock>`, starts empty, shared across clones)
- Update live signers changes expected signer, overrides genesis
- Round-robin signer selection
- Large signer set (21 signers)
- EthChainSpec trait delegation, base fee params, Paris total difficulty

#### Signer Tests (21)

- SignerManager CRUD operations (add, remove, list, re-add)
- Dev key setup (3 default, up to 20 available)
- Block sealing (seal_hash, sign_hash, signature roundtrip)
- Signature verification
- Concurrent sign operations (tokio::spawn multiple signers)
- Error cases (invalid key, nonexistent address, sign after remove)

#### Cache Tests (20)

- HotStateCache LRU operations (insert, hit, miss, evict, clear)
- Cache capacity enforcement
- LRU promotion on access
- Cache statistics (hit rate calculation)
- CachedStorageReader wrapping (miss→hit, invalidate, clear, eviction)

#### Keystore Tests (20)

- Create account + decrypt roundtrip
- Import key (with/without 0x prefix) + decrypt roundtrip
- Wrong password fails decryption
- MAC verification
- Duplicate import detection
- Account lifecycle (create, list, has, delete)
- Keystore file JSON format (V3 schema)
- Persistence across KeystoreManager instances
- Load all keys into SignerManager
- Concurrent access safety
- Address derivation from private key

#### Metrics Tests (19)

- PhaseTimer elapsed time tracking
- BlockMetrics (TPS, gas/second, total duration, summary output)
- ChainMetrics (rolling window, in-turn rate, multiple blocks)
- SlidingWindow (empty, fill, evict oldest)
- Metrics report format

#### Payload Tests (16)

- Difficulty selection (in-turn=1, out-of-turn=2)
- Epoch block extra_data format (embeds all signer addresses)
- Non-epoch block extra_data (no signer list)
- Sign payload components (vanity + seal)
- Signed header verifiable by consensus (end-to-end)
- PayloadBuilderBuilder (dev/production mode, cache size, creation)
- SharedCache accessible across multiple readers

#### Node Tests (8)

- PoaNode creation and builder chain (dev mode, signer manager)
- PoaConsensusBuilder creation and dev mode injection
- PoaEngineValidator builder (default construction)
- Extra_data strip for V1 payload (97→32 byte conversion)

#### Output Tests (4)

- `format_interval()` — zero, sub-second, whole seconds, fractional seconds

### Test Patterns

**Mock-based testing:** The `StorageReader` trait + `MockStorage` struct allows testing on-chain reads without a live Reth state provider. Tests construct mock storage maps and verify read functions against them.

**Genesis-based testing:** `GenesisStorageReader` reads directly from genesis alloc, allowing end-to-end tests that verify contract storage matches expectations without a running node.

**Consensus integration tests:** Tests create signed blocks using `BlockSealer`, then validate them through `PoaConsensus` — covering the full sign→validate→recover pipeline.

**Multi-node simulation:** Tests create chains of signed blocks from multiple signers, testing round-robin scheduling, fork choice, epoch transitions, signer add/remove, and chain reorganization scenarios.

**Async tests:** RPC tests use `#[tokio::test]` for async method testing. Keystore and signer tests use async for concurrent access verification.

*Last updated: 2026-03-16 | Chain ID 9323310 | reth 1.11.0 | 424 tests passing | ~16,200 lines | 47 files*

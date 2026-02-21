# Meowchain - Usage Guide

Custom POA blockchain on Reth. Chain ID **9323310**, all hardforks through Prague.

## Directory Structure

```
custom-reth-chain-/
├── src/                            # Rust source code (~10,000 lines, 40 files, 335 tests)
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
│   ├── rpc/                        # Custom RPC namespace
│   │   ├── mod.rs                  # MeowRpc + MeowApiServer impl + tests
│   │   ├── api.rs                  # MeowApi trait (#[rpc] macro)
│   │   └── types.rs                # Response types (ChainConfigResponse, etc.)
│   ├── signer/                     # Signing + key management
│   │   ├── mod.rs                  # Re-exports + tests
│   │   ├── manager.rs              # SignerManager
│   │   ├── sealer.rs               # BlockSealer + signature helpers
│   │   ├── dev.rs                  # DEV_PRIVATE_KEYS, setup_dev_signers()
│   │   └── errors.rs               # SignerError enum
│   ├── evm/
│   │   ├── mod.rs                  # PoaEvmFactory, PoaExecutorBuilder, CalldataDiscountInspector (Phase 2.11-12)
│   │   └── parallel.rs             # TxAccessRecord, ConflictDetector, ParallelSchedule (Phase 2.13)
│   ├── cache/
│   │   └── mod.rs                  # HotStateCache, CachedStorageReader, SharedCache (Phase 5)
│   ├── statediff/
│   │   └── mod.rs                  # StateDiff, AccountDiff (Phase 5: replica sync)
│   ├── metrics/
│   │   └── mod.rs                  # PhaseTimer, BlockMetrics, ChainMetrics (Phase 5)
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
│   └── docker-compose.yml          # Single-node compose
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

# Run tests (303 passing)
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
  --cache-size <N>            Hot state cache entries [default: 1000]
  --eager-mining              Mine immediately on tx arrival instead of interval
  --port <PORT>               P2P listener port [default: 30303]
  --bootnodes <URLs>          Comma-separated enode URLs for peer discovery
  --disable-discovery         Disable P2P peer discovery
  --metrics-interval <N>      Print metrics every N blocks (0=off)
  -h, --help                  Print help
```

## Running Modes

### Dev Mode (default)

Auto-mines blocks every 1 second. Relaxed consensus (no signature verification). 20 prefunded accounts with 10,000 ETH each. 3 default signers loaded automatically. 300M gas limit.

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
Block period:    2 seconds
Mode:            dev
Authorized signers (3):
  1. 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266
  2. 0x70997970C51812dc3A010C7d01b50e0d17dc79C8
  3. 0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC
...
  OnChain gas limit: 30000000 (from ChainConfig)
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
Block period:    2 seconds
Mode:            production+mining
Authorized signers (5):
  1. 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266
  ...
Signer key loaded: 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266
...
  OnChain gas limit: 60000000 (from ChainConfig)
  OnChain signers: 5 loaded from SignerRegistry
POA Consensus initialized: 5 signers, epoch: 30000, period: 2s, mode: production (strict)
...
  POA block #1 signed by 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266 (out-of-turn)
  POA block #5 signed by 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266 (in-turn)
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
| Block time | 2s | 2s (configurable via `--block-time`) |
| Gas limit | 30M (on-chain) | 60M (on-chain) |
| Signers | 3 (accounts 0-2) | 5 (accounts 0-4) |
| Prefunded accounts | 20 @ 10,000 ETH | 8 (tiered allocation) |
| Coinbase | EIP-1967 Miner Proxy | EIP-1967 Miner Proxy |
| Consensus | Relaxed (no sig check) | Strict POA (97-byte extra_data) |
| Epoch | 30,000 blocks | 30,000 blocks |
| P2P port | 30303 | 30303 |
| Mining | Auto (interval) | Engine API or `--mining` flag |

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
just docker         # build + docker build
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

*Last updated: 2026-02-20 | Chain ID 9323310 | reth 1.11.0 | 224 tests passing*

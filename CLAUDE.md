# Meowchain - Custom POA Blockchain on Reth

## Project Overview

Custom Proof of Authority (POA) blockchain built on [Reth](https://github.com/paradigmxyz/reth) (Rust Ethereum client). The node is Ethereum mainnet-compatible for smart contract execution, hardforks, and JSON-RPC APIs, but replaces beacon consensus with a POA signer-based model.

**Reth:** Tracks `main` branch (latest). Use `just build` to fetch latest + build.

## Architecture

```
Current State:
  meowchain (PoaNode)
    ├── Consensus: PoaConsensus (validates headers, signatures, timing, gas limits)
    ├── Block Production: PoaPayloadBuilder (wraps EthereumPayloadBuilder + POA signing)
    ├── Block Rewards: EIP-1967 Miner Proxy at 0x...1967 (coinbase) → Treasury
    ├── Governance: Gnosis Safe multisig → ChainConfig / SignerRegistry / Treasury
    ├── EVM: Identical to Ethereum mainnet (sequential, all opcodes, precompiles)
    ├── Hardforks: Frontier through Prague (all active at genesis)
    ├── RPC: HTTP (8545) + WS (8546) + meow_* namespace on 0.0.0.0
    └── Storage: MDBX persistent database (production NodeBuilder)

Target State (MegaETH-inspired):
  meowchain (PoaNode)
    ├── Consensus: PoaConsensus + on-chain SignerRegistry reads
    ├── Block Production: PoaPayloadBuilder (1s blocks, eager mining)
    ├── EVM: Parallel execution (grevm) + JIT compilation (revmc)
    ├── Gas: 300M-1B dynamic limit (ChainConfig contract, governance-controlled)
    ├── RPC: HTTP + WS + admin_*/meow_* namespaces
    └── Storage: RAM hot cache + MDBX cold storage + async trie
```

## Source Files

| File | Purpose | Status |
|------|---------|--------|
| `src/main.rs` | Entry point, CLI parsing, production node launch, block monitoring | Working |
| `src/node.rs` | `PoaNode` type - injects `PoaConsensus` + `PoaPayloadBuilder` | Working |
| `src/consensus.rs` | `PoaConsensus` - signature verification, header validation, post-execution checks | Working |
| `src/chainspec.rs` | `PoaChainSpec` - hardforks, POA config, signer list | Complete |
| `src/genesis.rs` | Genesis: system contracts, ERC-4337, miner proxy, governance, Safe | Complete |
| `src/payload.rs` | `PoaPayloadBuilder` - wraps Ethereum builder + POA signing | Complete |
| `src/rpc.rs` | `meow_*` RPC namespace - chainConfig, signers, nodeInfo | Complete |
| `src/signer.rs` | `SignerManager` + `BlockSealer` - key management & signing | Complete (in pipeline) |
| `src/bytecodes/` | Pre-compiled contract bytecodes (.bin/.hex) | Complete (16 files) |
| `contracts/` | Governance Solidity contracts (ChainConfig, SignerRegistry, Treasury) | Complete |

## Key Types & Import Paths

- `PoaNode` → `src/node.rs` - custom `Node` impl, replaces `EthereumNode`
- `PoaConsensus` → `src/consensus.rs` - implements `HeaderValidator`, `Consensus`, `FullConsensus`
- `PoaConsensusBuilder` → `src/node.rs` - `ConsensusBuilder` trait impl
- `PoaPayloadBuilder` → `src/payload.rs` - wraps `EthereumPayloadBuilder` + POA signing
- `PoaPayloadBuilderBuilder` → `src/payload.rs` - `PayloadBuilderBuilder` trait impl
- `PoaChainSpec` → `src/chainspec.rs` - wraps `ChainSpec` + `PoaConfig`
- `SignerManager` → `src/signer.rs` - runtime key management (RwLock<HashMap>)
- `BlockSealer` → `src/signer.rs` - seal/verify block headers
- `MeowRpc` → `src/rpc.rs` - `meow_*` RPC namespace (chainConfig, signers, nodeInfo)
- `MINER_PROXY_ADDRESS` → `src/genesis.rs` - EIP-1967 proxy at `0x...1967`
- `CHAIN_CONFIG_ADDRESS` → `src/genesis.rs` - on-chain config contract
- `SIGNER_REGISTRY_ADDRESS` → `src/genesis.rs` - on-chain signer registry
- `TREASURY_ADDRESS` → `src/genesis.rs` - fee distribution contract

### Reth Import Conventions
```rust
reth_ethereum::node::builder::*        // = reth_node_builder
reth_ethereum::node::*                 // = reth_node_ethereum (EthereumNode, builders)
reth_ethereum::EthPrimitives           // from reth_ethereum_primitives
reth_ethereum::provider::EthStorage    // from reth_provider
reth_ethereum::rpc::eth::primitives::Block  // RPC block type
reth_ethereum::tasks::TaskExecutor     // = Runtime (alias). Create with TaskExecutor::with_existing_handle()
reth_payload_primitives::PayloadTypes  // NOT re-exported by reth_ethereum
alloy_consensus::BlockHeader           // Use this for header method access (gas_used, gas_limit, extra_data)
```

## What's Done

### P0-Alpha (All Fixed)
- [x] **A1** - `NodeConfig::default()` with proper args
- [x] **A2** - Production `NodeBuilder` with persistent MDBX (`init_db` + `with_database`)
- [x] **A3** - `PoaNode` replaces `EthereumNode`
- [x] **A5** - `PoaConsensus` wired into pipeline via `PoaConsensusBuilder`

### P0 (Mostly Fixed)
- [x] **#2** - External RPC server: HTTP + WS on 0.0.0.0
- [x] **#3** - Consensus enforces POA signatures in production mode (`recover_signer` + `validate_signer`)
- [x] **#4** - Post-execution validates gas_used, receipt root, and logs bloom
- [x] **#5** - Chain ID 9323310 everywhere including sample-genesis.json
- [x] **#6** - CLI parsing with clap
- [x] **#1** - Signer loaded at runtime + `PoaPayloadBuilder` signs blocks in pipeline
- [x] **A4** - `PoaPayloadBuilder` wraps `EthereumPayloadBuilder` with POA signing
- [x] **A6** - `BlockSealer` wired into payload pipeline via `PoaPayloadBuilder.sign_payload()`
- [~] **#7** - Keys loadable from env/CLI, but dev keys still hardcoded, no encrypted keystore

### Recently Completed
- [x] `PoaPayloadBuilder` — signs blocks, sets difficulty 1/2, embeds signer list at epoch
- [x] CLI flags: `--gas-limit`, `--eager-mining`
- [x] Governance contracts in genesis: ChainConfig, SignerRegistry, Treasury
- [x] Gnosis Safe contracts in genesis: Singleton, Proxy Factory, Fallback Handler, MultiSend
- [x] `meow_*` RPC namespace: chainConfig, signers, nodeInfo
- [x] 80 unit tests passing

## What's NOT Done (Remaining Gaps)

1. **Performance Engineering** (MegaETH-inspired) - See Remaining.md Section 12:
   - Sub-second block production (target: 1s, stretch: 100ms)
   - Parallel EVM via grevm integration (target: 5K-10K TPS)
   - High gas limits (100M-1B via on-chain ChainConfig)
   - In-memory hot state cache
   - JIT compilation (revmc)

2. **Node ↔ Contract Integration** - Node doesn't yet read ChainConfig/SignerRegistry at runtime:
   - PoaPayloadBuilder should read gas limit from ChainConfig contract
   - PoaConsensus should read signer list from SignerRegistry contract
   - Governance Safe needs to be deployed as a proxy (currently just address reserved)
   - Timelock for sensitive parameter changes

## Chain Configuration

| Parameter | Dev Mode | Production | Target (MegaETH-inspired) |
|-----------|----------|------------|---------------------------|
| Chain ID | 9323310 | 9323310 | 9323310 |
| Block Time | 2s | 12s | 1s (100ms stretch) |
| Gas Limit | 30M | 60M | 300M-1B (configurable) |
| Max Contract Size | 24KB | 24KB | 512KB (configurable) |
| Signers | 3 (first 3 dev accounts) | 5 (first 5 dev accounts) | 5-21 (via SignerRegistry) |
| Epoch | 30,000 blocks | 30,000 blocks | 30,000 blocks |
| Prefunded | 20 accounts @ 10K ETH | 8 accounts (tiered) | Governed by Treasury |
| Coinbase | EIP-1967 Miner Proxy | EIP-1967 Miner Proxy | → Treasury contract |
| Mining Mode | Interval (2s) | Interval (12s) | Eager (tx-triggered) |
| EVM Execution | Sequential | Sequential | Parallel (grevm) |
| Governance | Hardcoded | Hardcoded | Gnosis Safe multisig |

## Genesis Pre-deployed Contracts

| Contract | Address | Source |
|----------|---------|--------|
| EIP-1967 Miner Proxy | `0x0000000000000000000000000000000000001967` | Block rewards (coinbase) |
| EIP-4788 Beacon Root | `0x000F3df6D732807Ef1319fB7B8bB8522d0Beac02` | System (Cancun) |
| EIP-2935 History Storage | `0x0000F90827F1C53a10cb7A02335B175320002935` | System (Prague) |
| EIP-7002 Withdrawal Requests | `0x00000961Ef480Eb55e80D19ad83579A64c007002` | System (Prague) |
| EIP-7251 Consolidation | `0x0000BBdDc7CE488642fb579F8B00f3a590007251` | System (Prague) |
| ERC-4337 EntryPoint v0.7 | `0x0000000071727De22E5E9d8BAf0edAc6f37da032` | Infra |
| WETH9 | `0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2` | Infra |
| Multicall3 | `0xcA11bde05977b3631167028862bE2a173976CA11` | Infra |
| CREATE2 Deployer | `0x4e59b44847b379578588920cA78FbF26c0B4956C` | Infra |
| SimpleAccountFactory | `0x9406Cc6185a346906296840746125a0E44976454` | Infra |
| ChainConfig | `0x00000000000000000000000000000000C04F1600` | Governance |
| SignerRegistry | `0x000000000000000000000000000000005164EB00` | Governance |
| Treasury | `0x0000000000000000000000000000000007EA5B00` | Governance |
| Governance Safe (reserved) | `0x000000000000000000000000000000006F5AFE00` | Governance |
| Safe Singleton v1.3.0 | `0xd9Db270c1B5E3Bd161E8c8503c55cEABeE709552` | Gnosis Safe |
| Safe Proxy Factory | `0xa6B71E26C5e0845f74c812102Ca7114b6a896AB2` | Gnosis Safe |
| Safe Fallback Handler | `0xf48f2B2d2a534e402487b3ee7C18c33Aec0Fe5e4` | Gnosis Safe |
| Safe MultiSend | `0xA238CBeb142c10Ef7Ad8442C6D1f9E89e07e7761` | Gnosis Safe |

## Building & Running

```bash
# Build (fetches latest reth + all crates, then builds release)
just build

# Quick build without updating deps
just build-fast

# Dev mode (default)
just dev

# Run with custom args
just run-custom --chain-id 9323310 --block-time 12 --datadir /data/meowchain

# With signer key from environment
SIGNER_KEY=ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 just dev

# Production mode
just run-production

# Run tests
just test

# Docker
just docker
```

## Development Notes

- 80 unit tests: `just test` (or `cargo test`)
- Consensus traits use `#[auto_impl::auto_impl(&, Arc)]` - `Arc<PoaConsensus>` auto-implements traits
- `launch_with_debug_capabilities()` requires `DebugNode` impl (in node.rs)
- Dev mode: auto-mines blocks, relaxed consensus (no signature checks)
- Production mode: strict consensus with POA signature verification
- The `clique` field in genesis config JSON is informational only - not parsed by Reth
- `just build` runs `cargo update` first to fetch latest reth from main branch

## Common Pitfalls

- `alloy_consensus::BlockHeader` vs `reth_primitives_traits::BlockHeader` - use alloy version for method access
- `NodeConfig::test()` enables dev mode by default; `NodeConfig::default()` does NOT
- `launch()` vs `launch_with_debug_capabilities()` - debug version needed for dev mining
- `TaskManager` is now internal in latest reth - use `TaskExecutor::with_existing_handle(Handle::current())`
- `HeaderValidator<Header>` uses concrete type - `Consensus<B>` needs `where PoaConsensus: HeaderValidator<B::Header>`

## Performance Roadmap

See `Remaining.md` for full details (Sections 12-15). Key remaining phases:

1. **Phase 2** - Performance: 1s blocks, 300M gas limit, parallel EVM (grevm)
2. **Phase 3** - Node ↔ Contract Integration: Read ChainConfig/SignerRegistry at runtime
3. **Phase 5** - Advanced: In-memory state, JIT compilation, state-diff streaming, sub-100ms blocks

Target: **1-second blocks, 5K-10K TPS, full on-chain governance** (vs MegaETH's 10ms/100K TPS but single sequencer)

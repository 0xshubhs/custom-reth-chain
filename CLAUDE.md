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

| File | Lines | Purpose | Status |
|------|-------|---------|--------|
| `src/main.rs` | 346 | Entry point, CLI parsing, production node launch, block monitoring | Working |
| `src/node.rs` | 257 | `PoaNode` type - injects `PoaConsensus` + `PoaPayloadBuilder` | Working |
| `src/consensus.rs` | 1,255 | `PoaConsensus` - signature verification, header validation, post-execution checks | Complete |
| `src/chainspec.rs` | 536 | `PoaChainSpec` - hardforks, POA config, signer list | Complete |
| `src/genesis.rs` | 1,422 | Genesis: system contracts, ERC-4337, miner proxy, governance, Safe | Complete |
| `src/payload.rs` | 507 | `PoaPayloadBuilder` - wraps Ethereum builder + POA signing | Complete |
| `src/onchain.rs` | 1,128 | `StorageReader` trait, slot constants, `read_gas_limit()`, `read_signer_list()` | Infrastructure done — **NOT wired to runtime** |
| `src/rpc.rs` | 297 | `meow_*` RPC namespace - chainConfig, signers, nodeInfo | Complete |
| `src/signer.rs` | 605 | `SignerManager` + `BlockSealer` - key management & signing | Complete (in pipeline) |
| `src/bytecodes/` | — | Pre-compiled contract bytecodes (.bin/.hex) | Complete (16 files) |
| `contracts/` | — | Governance Solidity contracts (ChainConfig, SignerRegistry, Treasury) | Complete |

**Total: 6,353 lines Rust, 187 tests passing (2026-02-18)**

## Key Types & Import Paths

- `PoaNode` → `src/node.rs` - custom `Node` impl, replaces `EthereumNode`
- `PoaConsensus` → `src/consensus.rs` - implements `HeaderValidator`, `Consensus`, `FullConsensus`
- `PoaConsensusBuilder` → `src/node.rs` - `ConsensusBuilder` trait impl
- `PoaPayloadBuilder` → `src/payload.rs` - wraps `EthereumPayloadBuilder` + POA signing
- `PoaPayloadBuilderBuilder` → `src/payload.rs` - `PayloadBuilderBuilder` trait impl
- `PoaChainSpec` → `src/chainspec.rs` - wraps `ChainSpec` + `PoaConfig`
- `SignerManager` → `src/signer.rs` - runtime key management (RwLock<HashMap>)
- `BlockSealer` → `src/signer.rs` - seal/verify block headers
- `StorageReader` → `src/onchain.rs` - trait abstracting storage access for on-chain reads
- `GenesisStorageReader` → `src/onchain.rs` - reads genesis alloc (tests only, not runtime)
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
reth_ethereum::storage::StateProviderFactory  // available in PayloadBuilder trait bounds
```

## What's Done

### Phase 0-1 — Foundation + Connectable (100% / ~90%)
- [x] `NodeConfig::default()` with proper args, production `NodeBuilder` + MDBX
- [x] `PoaNode` replaces `EthereumNode` — injects `PoaConsensus` + `PoaPayloadBuilder`
- [x] `PoaPayloadBuilder` signs blocks, sets difficulty 1/2, embeds signer list at epoch
- [x] `BlockSealer` wired into payload pipeline via `PoaPayloadBuilder.sign_payload()`
- [x] `PoaConsensus` validates POA signatures in production (`recover_signer` + `validate_signer`)
- [x] Post-execution validates gas_used, receipt root, and logs bloom
- [x] External HTTP (8545) + WS (8546) RPC on 0.0.0.0
- [x] Chain ID 9323310 everywhere
- [x] CLI: `--gas-limit`, `--eager-mining`, `--signer-key`, `--production`, `--no-dev`
- [x] 187 unit tests passing

### Phase 3 — Governance (~60% done)
- [x] Gnosis Safe v1.3.0 in genesis: Singleton, Proxy Factory, Fallback Handler, MultiSend
- [x] ChainConfig contract deployed in genesis (`0x...C04F1600`) with pre-populated storage
- [x] SignerRegistry contract deployed in genesis (`0x...5164EB00`) with initial signers in storage
- [x] Treasury contract deployed in genesis (`0x...7EA5B00`)
- [x] `meow_*` RPC: chainConfig, signers, nodeInfo
- [x] `onchain.rs`: `StorageReader` trait, slot constants, `read_gas_limit()`, `read_signer_list()`, `is_signer_on_chain()`, `GenesisStorageReader` (50+ tests)
- [ ] **`StateProviderStorageReader` adapter** — wraps Reth `StateProvider`, implements `StorageReader` ← MISSING
- [ ] **`PoaPayloadBuilder` reads gas limit from ChainConfig at runtime** — currently uses `conf.gas_limit_for(chain)` ← NOT WIRED
- [ ] **`PoaConsensus` reads signer list from SignerRegistry at runtime** — currently uses `self.chain_spec.poa_config.signers` ← NOT WIRED
- [ ] Shared live cache (`Arc<RwLock<...>>`) in `PoaChainSpec` for consensus↔payload signer sync

## What's NOT Done (Remaining Gaps)

### #1 Priority — On-Chain Governance Wiring (Phase 3, items 20-23)
The `onchain.rs` reader infrastructure is complete with 50+ tests. The missing piece is a `StateProviderStorageReader` that bridges Reth's live `StateProvider` to the `StorageReader` trait, then wiring it into the payload builder and consensus.

```
NOT WIRED:
  payload.rs:87  → conf.gas_limit_for(chain)           // should read ChainConfig contract
  consensus.rs   → self.chain_spec.poa_config.signers  // should read SignerRegistry contract

MISSING:
  struct StateProviderStorageReader<SP>(SP) where SP: StateProvider
  impl StorageReader for StateProviderStorageReader<SP> { ... }
```

### #2 — Performance Engineering (Phase 2, ~15% done)
- 1-second blocks (trivial config change, not yet default)
- Parallel EVM via grevm integration (target: 5K-10K TPS)
- Max contract size override (128KB-512KB)
- JIT compilation (revmc)

### #3 — Multi-Node (Phase 4, ~15% done)
- Bootnodes, state sync, fork choice rule
- `meowchain init` subcommand
- 3-signer network test

### #4 — Ecosystem (Phase 6, ~15% done)
- ERC-4337 Bundler service
- Bridge, DEX, oracle, subgraph
- ERC-8004 AI agent registries
- Faucet + docs + SDK

## Chain Configuration

| Parameter | Dev Mode | Production | Target (MegaETH-inspired) |
|-----------|----------|------------|---------------------------|
| Chain ID | 9323310 | 9323310 | 9323310 |
| Block Time | 2s | 12s | 1s (100ms stretch) |
| Gas Limit | 30M | 60M | 300M-1B (on-chain ChainConfig) |
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

- **187 unit tests**: `just test` (or `cargo test`) — all pure unit tests, runs in ~40ms
- Consensus traits use `#[auto_impl::auto_impl(&, Arc)]` - `Arc<PoaConsensus>` auto-implements traits
- `launch_with_debug_capabilities()` requires `DebugNode` impl (in node.rs)
- Dev mode: auto-mines blocks, relaxed consensus (no signature checks)
- Production mode: strict consensus with POA signature verification
- The `clique` field in genesis config JSON is informational only - not parsed by Reth
- `just build` runs `cargo update` first to fetch latest reth from main branch
- `USAGE.md` in `md/` is stale — refers to old `NodeConfig::test()` and chain ID 31337; ignore it

## Common Pitfalls

- `alloy_consensus::BlockHeader` vs `reth_primitives_traits::BlockHeader` - use alloy version for method access
- `NodeConfig::test()` enables dev mode by default; `NodeConfig::default()` does NOT
- `launch()` vs `launch_with_debug_capabilities()` - debug version needed for dev mining
- `TaskManager` is now internal in latest reth - use `TaskExecutor::with_existing_handle(Handle::current())`
- `HeaderValidator<Header>` uses concrete type - `Consensus<B>` needs `where PoaConsensus: HeaderValidator<B::Header>`
- `GotExpectedBoxed<Bloom>` - use `GotExpected { got, expected }.into()` without Box wrapping
- `BuildArguments` is NOT Clone (contains CancelOnDrop) - pass directly to inner builder
- `StateProviderFactory` is available in `PayloadBuilder` trait bounds - can read contract storage
- Consensus traits have NO state provider access - need shared cache for live signer list

## Performance Roadmap

See `Remaining.md` for full details (Sections 12-15). Key remaining phases:

1. **Phase 3** - Node ↔ Contract Integration: `StateProviderStorageReader` + wire reads into payload builder & consensus ← **DO THIS FIRST**
2. **Phase 2** - Performance: 1s blocks, 300M gas limit, parallel EVM (grevm)
3. **Phase 4** - Multi-Node: bootnodes, state sync, fork choice
4. **Phase 5** - Advanced: In-memory state, JIT compilation, state-diff streaming, sub-100ms blocks

Target: **1-second blocks, 5K-10K TPS, full on-chain governance** (vs MegaETH's 10ms/100K TPS but single sequencer)

*Last updated: 2026-02-18 | reth 1.11.0, rustc 1.93.1+, 187 tests*

# Meowchain - Custom POA Blockchain on Reth

## Project Overview

Custom Proof of Authority (POA) blockchain built on [Reth](https://github.com/paradigmxyz/reth) (Rust Ethereum client). The node is Ethereum mainnet-compatible for smart contract execution, hardforks, and JSON-RPC APIs, but replaces beacon consensus with a POA signer-based model.

**Reth:** Tracks `main` branch (latest). Use `just build` to fetch latest + build.

## Architecture

```
Current State:
  meowchain (PoaNode)
    ├── Consensus: PoaConsensus (validates headers, signatures, timing, gas limits)
    │   └── Live governance: reads SignerRegistry via shared Arc<RwLock> cache
    ├── Block Production: PoaPayloadBuilder (wraps EthereumPayloadBuilder + POA signing)
    │   ├── On-chain reads: gas limit from ChainConfig, signers from SignerRegistry at epoch
    │   └── SharedCache (Arc<Mutex<HotStateCache>>) — LRU cache across reads, invalidated at epoch
    ├── EVM: PoaEvmFactory wraps EthEvmFactory (patches CfgEnv for contract size + calldata gas)
    │   ├── --max-contract-size overrides EIP-170 24KB limit per block
    │   ├── --calldata-gas (default 4): CalldataDiscountInspector via initialize_interp + Gas::erase_cost
    │   └── parallel.rs: TxAccessRecord, ConflictDetector, ParallelSchedule foundation (grevm-ready)
    ├── Engine API: PoaEngineValidator (strips/restores 97-byte extra_data around alloy's 32-byte limit)
    ├── Block Rewards: EIP-1967 Miner Proxy at 0x...1967 (coinbase) → Treasury
    ├── Governance: Gnosis Safe multisig → ChainConfig / SignerRegistry / Treasury / Timelock
    ├── Hardforks: Frontier through Prague (all active at genesis)
    ├── Metrics: PhaseTimer, BlockMetrics, ChainMetrics (rolling window, in-turn rate)
    ├── StateDiff: AccountDiff, StorageDiff for replica state-diff streaming
    ├── RPC: HTTP (8545) + WS (8546) + meow_* namespace on 0.0.0.0
    ├── P2P: Configurable bootnodes, port, discovery (--port, --bootnodes, --disable-discovery)
    └── Storage: MDBX persistent database (production NodeBuilder)

Target State (MegaETH-inspired, remaining):
  meowchain (PoaNode)
    ├── EVM: Parallel execution (grevm, when on crates.io) + JIT (revmc)  ← NEXT
    ├── RPC: HTTP + WS + admin_*/meow_* namespaces
    └── Storage: async trie hashing, state-diff streaming to replicas
```

## Source Files

The `src/` directory uses a modular structure with **~40 Rust files** across **12 subdirectories** and **16 modules**:

| Module | Directory | Key Types | Tests |
|--------|-----------|-----------|-------|
| Entry point | `src/main.rs` | — | — |
| CLI | `src/cli.rs` | `Cli` (19 args) | — |
| Node | `src/node/` | `PoaNode`, `PoaEngineValidator`, `PoaConsensusBuilder` | 8 |
| EVM | `src/evm/` | `PoaEvmFactory`, `PoaExecutorBuilder`, `CalldataDiscountInspector`, `ParallelSchedule` | 28 |
| Consensus | `src/consensus/` | `PoaConsensus`, `PoaConsensusError` | 59 |
| Chain spec | `src/chainspec/` | `PoaChainSpec`, `PoaConfig` | 27 |
| Genesis | `src/genesis/` | `GenesisConfig`, `create_genesis()` | 33 |
| Payload | `src/payload/` | `PoaPayloadBuilder`, `PoaPayloadBuilderBuilder` | 16 |
| On-chain | `src/onchain/` | `StorageReader`, `StateProviderStorageReader` | 55 |
| RPC | `src/rpc/` | `MeowRpc`, `MeowApiServer` | 9 |
| Signer | `src/signer/` | `SignerManager`, `BlockSealer` | 21 |
| Cache | `src/cache/` | `HotStateCache`, `CachedStorageReader`, `SharedCache` | 20+ |
| State diff | `src/statediff/` | `StateDiff`, `AccountDiff`, `StorageDiff` | 10+ |
| Metrics | `src/metrics/` | `PhaseTimer`, `BlockMetrics`, `ChainMetrics` | 10+ |
| Output | `src/output.rs` | Colored console output + `format_interval()`, `print_block_state_diff()`, `print_block_time_budget_warning()` | 4 |
| Shared | `src/{lib,constants,errors}.rs` | Module root + constants + re-exports | — |
| Bytecodes | `src/bytecodes/` | Pre-compiled contract bytecodes (.bin/.hex, 13 contracts) | — |

**Total: ~10,000 lines Rust across ~40 files, 339 tests passing (2026-02-22)**

### File-Level Breakdown

```
src/
├── lib.rs                  (18)   Module declarations
├── main.rs                (259)   Entry point, CLI, node launch, block monitoring
├── cli.rs                  (110)  CLI argument definitions (19 args incl. --max-contract-size, --calldata-gas, --cache-size)
├── constants.rs            (11)   EXTRA_VANITY_LENGTH, EXTRA_SEAL_LENGTH, etc.
├── errors.rs                (2)   Re-exports
├── output.rs              (255)   20 colored console output functions
├── node/
│   ├── mod.rs             (255)   PoaNode (NodeTypes, Node, DebugNode impls) — uses PoaExecutorBuilder
│   ├── builder.rs          (56)   PoaConsensusBuilder (ConsensusBuilder impl)
│   └── engine.rs          (148)   PoaEngineValidator (strip/restore 97-byte extra_data)
├── evm/
│   ├── mod.rs             (~425)  PoaEvmFactory, PoaExecutorBuilder, CalldataDiscountInspector + 16 tests
│   └── parallel.rs        (~300)  TxAccessRecord, ConflictDetector, ParallelSchedule, ParallelExecutor + 20 tests
├── consensus/
│   ├── mod.rs           (2,022)   PoaConsensus (HeaderValidator, Consensus, FullConsensus) + 59 tests
│   └── errors.rs           (67)   PoaConsensusError (8 variants)
├── chainspec/
│   ├── mod.rs             (602)   PoaChainSpec (live_signers, effective_signers, trait impls) + 27 tests
│   ├── config.rs           (24)   PoaConfig (period, epoch, signers)
│   └── hardforks.rs        (36)   mainnet_compatible_hardforks() — Frontier through Prague
├── genesis/
│   ├── mod.rs             (898)   GenesisConfig, create_genesis(), extra_data encoding + 33 tests
│   ├── accounts.rs         (38)   dev_accounts(), dev_signers()
│   ├── addresses.rs        (46)   Contract address constants (19 addresses)
│   ├── contracts.rs       (276)   System/infra contract alloc (EIP-4788/2935/7002/7251, ERC-4337, etc.)
│   └── governance.rs      (266)   Governance contract alloc (ChainConfig, SignerRegistry, Treasury, Timelock, Safe)
├── payload/
│   ├── mod.rs             (449)   PoaPayloadBuilder (try_build, sign_payload, epoch refresh, SharedCache) + 16 tests
│   └── builder.rs         (131)   PoaPayloadBuilderBuilder (startup gas+signer reads, creates SharedCache)
├── onchain/
│   ├── mod.rs             (831)   StorageReader trait, MockStorage, tests (55 tests)
│   ├── providers.rs        (54)   StateProviderStorageReader, GenesisStorageReader
│   ├── readers.rs         (144)   read_gas_limit(), read_signer_list(), is_signer_on_chain()
│   ├── slots.rs            (55)   Storage slot constants for all governance contracts
│   ├── selectors.rs        (24)   ABI function selectors
│   └── helpers.rs          (54)   encode/decode helpers (U256 ↔ Address, slot computation)
├── rpc/
│   ├── mod.rs             (257)   MeowRpc impl + 9 tests
│   ├── api.rs              (20)   MeowApi #[rpc] trait definition
│   └── types.rs            (29)   ChainConfigResponse, NodeInfoResponse
├── signer/
│   ├── mod.rs             (363)   Integration tests (21 tests)
│   ├── manager.rs          (77)   SignerManager (RwLock<HashMap<Address, PrivateKeySigner>>)
│   ├── sealer.rs          (103)   BlockSealer (seal_header, verify_signature)
│   ├── errors.rs           (18)   SignerError (3 variants)
│   └── dev.rs              (40)   DEV_PRIVATE_KEYS (20 deterministic keys)
├── cache/
│   └── mod.rs             (~200)  HotStateCache (LRU), CachedStorageReader<R>, SharedCache type alias
├── statediff/
│   └── mod.rs             (~150)  StateDiff, AccountDiff, StorageDiff (replica state streaming)
├── metrics/
│   └── mod.rs             (~150)  PhaseTimer (RAII), BlockMetrics, ChainMetrics (rolling window)
└── bytecodes/                     26 files (.bin/.hex for 13 contracts)
```

## Documentation

| File | Lines | Purpose |
|------|-------|---------|
| `CLAUDE.md` | — | Project instructions, architecture, status (this file) |
| `md/Architecture.md` | 1,348 | Comprehensive architecture doc with 12+ Mermaid diagrams covering all 12 modules |
| `md/Remaining.md` | 1,598 | Detailed roadmap with remaining phases and implementation plans |
| `md/USAGE.md` | 544 | User-facing usage guide (CLI, RPC, Docker, deployment) |
| `md/Implementation.md` | 401 | Implementation notes and design decisions |
| `md/main.md` | 175 | Project strategy and MegaETH-inspired vision |

## Key Types & Import Paths

- `PoaNode` → `src/node/mod.rs` - custom `Node` impl, replaces `EthereumNode`
- `PoaEngineValidator` → `src/node/engine.rs` - bypasses alloy 32-byte extra_data limit
- `PoaConsensusBuilder` → `src/node/builder.rs` - `ConsensusBuilder` trait impl
- `PoaConsensus` → `src/consensus/mod.rs` - implements `HeaderValidator`, `Consensus`, `FullConsensus`
- `PoaConsensusError` → `src/consensus/errors.rs` - consensus error enum (8 variants)
- `PoaPayloadBuilder` → `src/payload/mod.rs` - wraps `EthereumPayloadBuilder` + POA signing
- `PoaPayloadBuilderBuilder` → `src/payload/builder.rs` - `PayloadBuilderBuilder` trait impl
- `PoaChainSpec` → `src/chainspec/mod.rs` - wraps `ChainSpec` + `PoaConfig` + `live_signers`
- `PoaConfig` → `src/chainspec/config.rs` - POA configuration (period, epoch, signers)
- `SignerManager` → `src/signer/manager.rs` - runtime key management (RwLock<HashMap>)
- `BlockSealer` → `src/signer/sealer.rs` - seal/verify block headers
- `StorageReader` → `src/onchain/mod.rs` - trait abstracting storage access for on-chain reads
- `StateProviderStorageReader` → `src/onchain/providers.rs` - bridges live Reth `StateProvider` to `StorageReader`
- `GenesisStorageReader` → `src/onchain/providers.rs` - reads genesis alloc (tests only)
- `MeowRpc` → `src/rpc/mod.rs` - `meow_*` RPC namespace (chainConfig, signers, nodeInfo)
- `MeowApi` → `src/rpc/api.rs` - `#[rpc]` trait definition
- `PoaEvmFactory` → `src/evm/mod.rs` - wraps `EthEvmFactory`, patches `CfgEnv` (contract size + calldata gas)
- `PoaExecutorBuilder` → `src/evm/mod.rs` - replaces `EthereumExecutorBuilder` in `PoaNode`
- `CalldataDiscountInspector<I>` → `src/evm/mod.rs` - wraps any `Inspector<CTX>`, applies calldata discount via `Gas::erase_cost`
- `ParallelSchedule` → `src/evm/parallel.rs` - DAG-based tx batch scheduler (grevm-ready)
- `ConflictDetector` → `src/evm/parallel.rs` - WAW/WAR/RAW hazard detection
- `TxAccessRecord` → `src/evm/parallel.rs` - per-tx read/write access footprint
- `HotStateCache` → `src/cache/mod.rs` - LRU cache for on-chain storage reads
- `CachedStorageReader<R>` → `src/cache/mod.rs` - wraps any `StorageReader` with `SharedCache`
- `SharedCache` → `src/cache/mod.rs` - `Arc<Mutex<HotStateCache>>` shared across payload builder
- `StateDiff` / `AccountDiff` → `src/statediff/mod.rs` - state diff for replica streaming
- `PhaseTimer` / `BlockMetrics` / `ChainMetrics` → `src/metrics/mod.rs` - perf tracking
- Contract addresses → `src/genesis/addresses.rs` - MINER_PROXY, CHAIN_CONFIG, SIGNER_REGISTRY, TREASURY, TIMELOCK
- `output::*` → `src/output.rs` - colored console output functions (replaces all println!)
- `Cli` → `src/cli.rs` - clap CLI argument struct (19 args incl. --max-contract-size, --calldata-gas, --cache-size)
- Constants → `src/constants.rs` - EXTRA_VANITY_LENGTH, EXTRA_SEAL_LENGTH, ADDRESS_LENGTH, DEFAULT_CHAIN_ID, DEFAULT_EPOCH

### External Artifacts

- Genesis files: `genesis/sample-genesis.json` (dev), `genesis/production-genesis.json`
- Solidity source: `genesis-contracts/ChainConfig.sol`, `SignerRegistry.sol`, `Treasury.sol`, `Timelock.sol`
- Docker: `Docker/Dockerfile`, `Docker/docker-compose.yml`
- Contract ABI signatures: `signatures/signatures-contracts.json`, `signatures-contracts.txt`
- Explorer: `scoutup-go-explorer/` (Blockscout Go wrapper)

### Reth Import Conventions
```rust
reth_ethereum::node::builder::*        // = reth_node_builder
reth_ethereum::node::*                 // = reth_node_ethereum (EthereumNode, builders)
reth_ethereum::EthPrimitives           // from reth_ethereum_primitives
reth_ethereum::provider::EthStorage    // from reth_provider
reth_ethereum::rpc::eth::primitives::Block  // RPC block type
reth_ethereum::tasks::{RuntimeBuilder, RuntimeConfig, TokioConfig}  // Create task executor
reth_payload_primitives::PayloadTypes  // NOT re-exported by reth_ethereum
alloy_consensus::BlockHeader           // Use this for header method access (gas_used, gas_limit, extra_data)
reth_ethereum::storage::StateProviderFactory  // available in PayloadBuilder trait bounds
```

**Task executor pattern** (replaces removed `TaskExecutor::with_existing_handle()`):
```rust
RuntimeBuilder::new(
    RuntimeConfig::default()
        .with_tokio(TokioConfig::existing_handle(Handle::current())),
).build()?
```

## What's Done

### Phase 0-1 — Foundation + Connectable (100%)
- [x] `NodeConfig::default()` with proper args, production `NodeBuilder` + MDBX
- [x] `PoaNode` replaces `EthereumNode` — injects `PoaConsensus` + `PoaPayloadBuilder`
- [x] `PoaPayloadBuilder` signs blocks, sets difficulty 1/2, embeds signer list at epoch
- [x] `BlockSealer` wired into payload pipeline via `PoaPayloadBuilder.sign_payload()`
- [x] `PoaConsensus` validates POA signatures in production (`recover_signer` + `validate_signer`)
- [x] Post-execution validates gas_used, receipt root, and logs bloom
- [x] External HTTP (8545) + WS (8546) RPC on 0.0.0.0
- [x] Chain ID 9323310 everywhere
- [x] CLI: `--gas-limit`, `--eager-mining`, `--signer-key`, `--production`, `--no-dev`, `--port`, `--bootnodes`, `--disable-discovery`, `--mining`, `--max-contract-size`, `--cache-size`, `--calldata-gas`
- [x] 339 tests passing

### Phase 3 — Governance (100%)
- [x] Gnosis Safe v1.3.0 in genesis: Singleton, Proxy Factory, Fallback Handler, MultiSend
- [x] ChainConfig contract deployed in genesis (`0x...C04F1600`) with pre-populated storage
- [x] SignerRegistry contract deployed in genesis (`0x...5164EB00`) with initial signers in storage
- [x] Treasury contract deployed in genesis (`0x...7EA5B00`)
- [x] Timelock contract deployed in genesis (`0x...714E4C00`) with 24h minDelay
- [x] `meow_*` RPC: chainConfig, signers, nodeInfo
- [x] `onchain.rs`: `StorageReader` trait, slot constants, `read_gas_limit()`, `read_signer_list()`, `is_signer_on_chain()`, timelock reads, `GenesisStorageReader` (55+ tests)
- [x] `StateProviderStorageReader` adapter — wraps Reth `StateProvider`, implements `StorageReader`
- [x] `PoaPayloadBuilder` reads gas limit from ChainConfig at startup via `StateProviderStorageReader`
- [x] `PoaPayloadBuilder` refreshes live signer list from SignerRegistry at every epoch block
- [x] `PoaConsensus` reads signer list via `effective_signers()` — respects live governance changes
- [x] Shared live cache (`Arc<RwLock<Option<Vec<Address>>>>`) in `PoaChainSpec` for consensus↔payload signer sync
- [x] `PoaEngineValidator` bypasses alloy's 32-byte extra_data limit for production mode 97-byte POA blocks
- [x] Both `genesis/sample-genesis.json` and `genesis/production-genesis.json` generated from code with all contracts

### Phase 4 — Multi-Node (100%)
- [x] Bootnode CLI flags (`--port`, `--bootnodes`, `--disable-discovery`) wired to Reth `NetworkArgs`
- [x] `PoaChainSpec` supports configurable bootnodes via `with_bootnodes()`
- [x] Fork choice rule: `is_in_turn()`, `score_chain()`, `compare_chains()` — prefers in-turn signers
- [x] State sync validation: consensus correctly validates chains of 100+ blocks
- [x] 3-signer network simulation tests (round-robin, out-of-turn, unauthorized, missed turns)
- [x] Multi-node integration tests (5-signer, signer add/remove at epoch, fork choice, double sign, reorg)

### Phase 2 — Performance Engineering (items 10-13 done)
- [x] 1-second blocks default (dev=1s/300M gas, prod=2s/1B gas) — changed genesis defaults
- [x] `PoaEvmFactory` + `PoaExecutorBuilder` — replaces `EthereumExecutorBuilder` in `PoaNode`
- [x] `--max-contract-size` CLI flag — patches `CfgEnv.limit_contract_code_size` + initcode × 2
- [x] Calldata gas reduction (`--calldata-gas`, default 4 gas/byte, `CalldataDiscountInspector`)
- [x] Parallel EVM foundation (`ParallelSchedule`, `ConflictDetector`, `TxAccessRecord` — grevm-ready)
- [x] Sub-second block time `--block-time-ms` (500ms, 200ms, 100ms); overrides `--block-time`
- [x] StateDiff wiring: per-block accounts+slots changed from `execution_outcome().bundle_accounts_iter()`
- [x] Block time budget warning: fires at 3× interval (catches genuine stalls, avoids dev-mining jitter)

### Phase 5 — Advanced Performance (~40% done)
- [x] `HotStateCache` (LRU), `CachedStorageReader<R>`, `SharedCache = Arc<Mutex<HotStateCache>>`
- [x] `PoaPayloadBuilder` uses `SharedCache`: cache persists across reads, invalidated at epoch
- [x] `--cache-size` CLI flag wired through `PoaNode.with_cache_size()` → `PoaPayloadBuilderBuilder`
- [x] `StateDiff` / `AccountDiff` / `StorageDiff` for replica state-diff streaming
- [x] `PhaseTimer` (RAII timer), `BlockMetrics`, `ChainMetrics` (rolling window, in-turn rate)
- [x] `print_block_signed` now logs signing time in ms
- [ ] Async trie hashing, JIT (revmc), streaming block production, sub-100ms blocks

### Codebase Quality
- [x] Modular file structure: ~39 files across 12 subdirectories
- [x] Comprehensive architecture documentation (`md/Architecture.md`, updated)
- [x] Zero compiler warnings, clean on rustc 1.93.1+
- [x] 335 tests: consensus (59), onchain (55), genesis (33), chainspec (27), evm (28: 16 mod + 20 parallel + 8 from prev), cache (20+), signer (21), payload (16), statediff (10+), metrics (10+), rpc (9), node (8)

### Phase 2.12-13 — Calldata Gas + Parallel Foundation (100%)
- [x] `CalldataDiscountInspector<I>` — wraps any `Inspector<CTX>`, applies discount once per tx via `initialize_interp` + `Gas::erase_cost`; discount = `(16 - cost) × non_zero_bytes`
- [x] `--calldata-gas` CLI arg (default=4, range 1–16); `16` = Ethereum mainnet, `4` = POA default
- [x] `PoaEvmFactory::calldata_gas_per_byte` field; `PoaNode::with_calldata_gas()` builder method
- [x] `src/evm/parallel.rs` — `TxAccessRecord`, `AccessKey`, `ConflictDetector` (WAW/WAR/RAW), `ParallelSchedule` (batch builder), `ParallelExecutor` stub; 20 tests
- [x] Ready for grevm swap-in: replace `ParallelExecutor::execute_sequential` with grevm executor once it ships on crates.io

## What's NOT Done (Remaining Gaps)

### #1 — Performance Engineering (Phase 2, remaining)
- Live parallel EVM via grevm (foundation done; awaiting grevm on crates.io)
- JIT compilation (revmc)
- Sub-second blocks (< 500ms), async trie hashing

### #2 — Ecosystem (Phase 6, ~15% done)
- ERC-4337 Bundler service
- Bridge, DEX, oracle, subgraph
- ERC-8004 AI agent registries
- Faucet + docs + SDK

## CLI Arguments

| Argument | Type | Default | Description |
|----------|------|---------|-------------|
| `--chain-id` | `u64` | `9323310` | Chain ID for the network |
| `--block-time` | `u64` | `1` | Block production interval (seconds) |
| `--datadir` | `PathBuf` | `data` | Data directory for chain storage |
| `--http-addr` | `String` | `0.0.0.0` | HTTP RPC listen address |
| `--http-port` | `u16` | `8545` | HTTP RPC port |
| `--ws-addr` | `String` | `0.0.0.0` | WebSocket RPC listen address |
| `--ws-port` | `u16` | `8546` | WebSocket RPC port |
| `--signer-key` | `Option<String>` | — | Signer private key (hex, env: `SIGNER_KEY`) |
| `--production` | `bool` | `false` | Use production genesis configuration |
| `--no-dev` | `bool` | `false` | Disable dev mode (no auto-mining) |
| `--gas-limit` | `Option<u64>` | — | Override block gas limit |
| `--max-contract-size` | `usize` | `0` | Override EIP-170 contract size (0=default 24KB) |
| `--calldata-gas` | `u64` | `4` | Gas/byte for non-zero calldata (1–16; 4=POA, 16=mainnet) |
| `--block-time-ms` | `u64` | `0` | Sub-second block interval in ms (0=use --block-time; e.g. 500) |
| `--cache-size` | `usize` | `1000` | Hot state LRU cache entries |
| `--eager-mining` | `bool` | `false` | Mine immediately on tx arrival |
| `--mining` | `bool` | `false` | Force auto-mining in production mode |
| `--port` | `u16` | `30303` | P2P listener port |
| `--bootnodes` | `Option<Vec<String>>` | — | Comma-separated bootnode enode URLs |
| `--disable-discovery` | `bool` | `false` | Disable P2P peer discovery |
| `--metrics-interval` | `u64` | `0` | Print chain metrics every N blocks (0=off) |

## Chain Configuration

| Parameter | Dev Mode | Production | Target (MegaETH-inspired) |
|-----------|----------|------------|---------------------------|
| Chain ID | 9323310 | 9323310 | 9323310 |
| Block Time | **1s** | **2s** | 100ms stretch |
| Gas Limit | **300M** | **1B** | 300M-1B (on-chain ChainConfig) |
| Max Contract Size | **Configurable (--max-contract-size)** | **Configurable** | 512KB |
| Calldata Gas | **4 gas/byte (--calldata-gas)** | **4 gas/byte** | Custom (on-chain governed) |
| Signers | 3 (first 3 dev accounts) | 5 (first 5 dev accounts) | 5-21 (via SignerRegistry) |
| Epoch | 30,000 blocks | 30,000 blocks | 30,000 blocks |
| Prefunded | 20 accounts @ 10K ETH | 8 accounts (tiered) | Governed by Treasury |
| Coinbase | EIP-1967 Miner Proxy | EIP-1967 Miner Proxy | → Treasury contract |
| Mining Mode | Interval (2s) | Interval (12s) | Eager (tx-triggered) |
| EVM Execution | Sequential | Sequential | Parallel (grevm) |
| Governance | On-chain (live reads) | On-chain (live reads) | Gnosis Safe multisig |

## Genesis Pre-deployed Contracts

| Contract | Address | Category |
|----------|---------|----------|
| EIP-1967 Miner Proxy | `0x0000000000000000000000000000000000001967` | System (coinbase) |
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
| Timelock | `0x00000000000000000000000000000000714E4C00` | Governance |
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

# Production mode with auto-mining (for testing strict POA + 97-byte extra_data)
just run-production --mining

# Run tests
just test

# Docker
just docker
```

## Development Notes

- **303 tests**: `just test` (or `cargo test`) — unit + integration tests, runs in ~450ms
- **Modular structure**: ~39 files across 12 subdirectories
- **Architecture doc**: `md/Architecture.md` (1,348 lines, 12+ Mermaid diagrams) covers every module
- Consensus traits use `#[auto_impl::auto_impl(&, Arc)]` - `Arc<PoaConsensus>` auto-implements traits
- `launch_with_debug_capabilities()` requires `DebugNode` impl (in `src/node/mod.rs`)
- Dev mode: auto-mines blocks, relaxed consensus (no signature checks)
- Production mode: strict consensus with POA signature verification + 97-byte extra_data
- The `clique` field in genesis config JSON is informational only - not parsed by Reth
- `just build` runs `cargo update` first to fetch latest reth from main branch
- Genesis files are in `genesis/` (`sample-genesis.json`, `production-genesis.json`)
- Solidity source is in `genesis-contracts/` (not `contracts/`)
- Docker artifacts are in `Docker/` (not root)
- Explorer is `scoutup-go-explorer/` (not `scoutup/`)

## Common Pitfalls

- `alloy_consensus::BlockHeader` vs `reth_primitives_traits::BlockHeader` - use alloy version for method access
- `NodeConfig::test()` enables dev mode by default; `NodeConfig::default()` does NOT
- `launch()` vs `launch_with_debug_capabilities()` - debug version needed for dev mining
- `TaskManager` is now `pub(crate)` in reth - use `RuntimeBuilder` pattern (see import conventions above)
- `HeaderValidator<Header>` uses concrete type - `Consensus<B>` needs `where PoaConsensus: HeaderValidator<B::Header>`
- `GotExpectedBoxed<Bloom>` - use `GotExpected { got, expected }.into()` without Box wrapping
- `BuildArguments` is NOT Clone (contains CancelOnDrop) - pass directly to inner builder
- `StateProviderFactory` is available in `PayloadBuilder` trait bounds - can read contract storage
- Consensus traits have NO state provider access - need shared `Arc<RwLock>` cache for live signer list
- `alloy_consensus::Block<TransactionSigned>` = `reth_ethereum::Block` (use this, not `reth_ethereum_primitives::Block`)
- `ExecutionPayload` has V1/V2/V3 variants only (no V4) - strip extra_data with `mem::take`
- `BasicEngineApiBuilder` has no `new()` - use `BasicEngineApiBuilder::<PVB>::default()`
- Reth 1.11.0 requires rustc 1.93+ (alloy 1.7.0 needs 1.91)

## Performance Roadmap

See `md/Remaining.md` for full details. Key remaining phases:

1. **Phase 0-1** — Foundation + Connectable: **COMPLETE** (303 tests, production NodeBuilder, MDBX)
2. **Phase 3** — Governance: **COMPLETE** (Timelock, on-chain reads, live signer cache, StateProviderStorageReader)
3. **Phase 4** — Multi-Node: **COMPLETE** (bootnodes CLI, fork choice, state sync validation, integration tests)
4. **Phase 2** — Performance (items 10-16 done): 1s/500ms blocks ✅, 300M/1B gas ✅, calldata gas ✅, ParallelSchedule ✅, StateDiff wiring ✅, grevm live integration **← NEXT**
5. **Phase 5** — Advanced (~40% done): cache/statediff/metrics ✅, async trie/JIT/streaming **← NEXT**
6. **Phase 6** — Ecosystem: ERC-4337 bundler, bridge, DEX, oracle, faucet, SDK

Target: **1-second blocks, 5K-10K TPS, full on-chain governance** (vs MegaETH's 10ms/100K TPS but single sequencer)

*Last updated: 2026-02-22 | reth 1.11.0, rustc 1.93.1+, 339 tests, ~10,000 lines, ~40 files*

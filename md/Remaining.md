### Meowchain Custom POA Chain - Status Tracker

> **Last audited: 2026-02-21**

## Table of Contents

1. [What's Done](#1-whats-done)
2. [Critical Gaps (Production Blockers)](#2-critical-gaps-production-blockers)
   - 2.5 [Multi-Node POA Operation](#25-multi-node-poa-operation-how-others-run-the-chain)
3. [Remaining Infrastructure](#3-remaining-infrastructure)
4. [Chain Recovery & Resumption](#4-chain-recovery--resumption)
5. [Upgrade Mechanism](#5-upgrade-mechanism-hardfork-support)
6. [All Finalized EIPs by Hardfork](#6-all-finalized-eips-by-hardfork)
7. [ERC Standards Support](#7-erc-standards-support)
8. [ERC-8004: AI Agent Support](#8-erc-8004-trustless-ai-agents)
9. [Upcoming Ethereum Upgrades](#9-upcoming-ethereum-upgrades)
10. [Production Infrastructure Checklist](#10-production-infrastructure-checklist)
11. [Codebase Issues Found During Audit](#11-codebase-issues-found-during-audit)
12. [MegaETH-Inspired Performance Engineering](#12-megaeth-inspired-performance-engineering)
13. [Admin Privileges & Multisig Governance](#13-admin-privileges--multisig-governance)
14. [Dynamic Chain Parameters](#14-dynamic-chain-parameters)
15. [Meowchain vs MegaETH vs Ethereum Comparison](#15-meowchain-vs-megaeth-vs-ethereum-comparison)

---

## 1. What's Done

### Core Modules (src/)

Modular structure: 40 Rust files across 12 subdirectories, ~10,000 total lines, 339 tests.

| Module | Directory | Files | Status |
|--------|-----------|-------|--------|
| Entry point | `main.rs` + `cli.rs` | 2 | Working - CLI (18 args), block monitoring, colored output |
| Node type | `node/` | 3 | Complete - PoaNode + PoaEngineValidator + PoaConsensusBuilder |
| EVM factory | `evm/` | 2 | **NEW (Phase 2)** - PoaEvmFactory + PoaExecutorBuilder (max contract size, calldata gas); `parallel.rs` (Phase 2.13 foundation) |
| Chain spec | `chainspec/` | 3 | Complete - hardforks, POA config, bootnodes, trait impls |
| Consensus | `consensus/` | 2 | Complete - signatures, timing, gas, fork choice, multi-node tests |
| Genesis | `genesis/` | 5 | Complete - dev/production, system + governance + Safe contracts |
| Payload | `payload/` | 2 | Complete - wraps EthereumPayloadBuilder + POA signing + SharedCache |
| On-chain | `onchain/` | 6 | Complete and wired - StorageReader, slots, timelock reads |
| RPC | `rpc/` | 3 | Complete - meow_chainConfig, meow_signers, meow_nodeInfo |
| Signer | `signer/` | 5 | Complete - SignerManager + BlockSealer, wired into payload |
| Cache | `cache/` | 1 | **NEW (Phase 5)** - HotStateCache, CachedStorageReader, SharedCache |
| State diff | `statediff/` | 1 | **NEW (Phase 5)** - StateDiff, AccountDiff (streaming replica sync) |
| Metrics | `metrics/` | 1 | **NEW (Phase 5)** - PhaseTimer, BlockMetrics, ChainMetrics |
| Output | `output.rs` | 1 | Complete - colored console output (replaces raw println!) |
| Shared | `lib.rs` + `constants.rs` + `errors.rs` | 3 | Complete - module root, constants, re-exports |
| Bytecodes | `src/bytecodes/` | 26 | Complete - .bin + .hex for all 13 pre-deployed contracts |

### Hardforks Enabled (All at Block 0 / Timestamp 0)

| Hardfork | Status | Key Features |
|----------|--------|--------------|
| Frontier through London | Active | Full EVM, EIP-1559, CREATE2, REVERT, etc. |
| Paris (Merge) | Active | TTD=0, PREVRANDAO |
| Shanghai | Active | PUSH0, withdrawals ops |
| Cancun | Active | EIP-4844 blobs, TSTORE/TLOAD, MCOPY |
| Prague | Active | BLS precompile, EIP-7702, blob increase |

### System Contracts Deployed in Genesis

| EIP | Address | Purpose |
|-----|---------|---------|
| EIP-4788 | `0x000F3df6D732807Ef1319fB7B8bB8522d0Beac02` | Beacon block root |
| EIP-2935 | `0x0000F90827F1C53a10cb7A02335B175320002935` | History storage |
| EIP-7002 | `0x00000961Ef480Eb55e80D19ad83579A64c007002` | Withdrawal requests |
| EIP-7251 | `0x0000BBdDc7CE488642fb579F8B00f3a590007251` | Consolidation requests |

### ERC-4337 & Infrastructure Contracts in Genesis (NEW)

| Contract | Address | Purpose |
|----------|---------|---------|
| EntryPoint v0.7 | `0x0000000071727De22E5E9d8BAf0edAc6f37da032` | ERC-4337 core |
| WETH9 | `0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2` | Wrapped native token |
| Multicall3 | `0xcA11bde05977b3631167028862bE2a173976CA11` | Batch RPC calls |
| CREATE2 Deployer | `0x4e59b44847b379578588920cA78FbF26c0B4956C` | Deterministic deploys |
| SimpleAccountFactory | `0x9406Cc6185a346906296840746125a0E44976454` | ERC-4337 wallet factory |

### Infrastructure Done

- [x] Docker build (`Docker/Dockerfile`)
- [x] Docker Compose (single node)
- [x] Blockscout explorer integration (Scoutup Go app in `scoutup-go-explorer/`)
- [x] MDBX persistent storage (`data/db/`)
- [x] Static files for headers/txns/receipts
- [x] Dev mode with configurable block time (default 2s)
- [x] 20 prefunded accounts (10,000 ETH each in dev, tiered in production)
- [x] 3 default POA signers (round-robin logic in chainspec)
- [x] EIP-1559 base fee (0.875 gwei initial)
- [x] EIP-4844 blob support enabled
- [x] Basic unit tests in each module (**224 tests passing** as of 2026-02-20)
- [x] CLI argument parsing (clap) - chain-id, block-time, datadir, http/ws config, signer-key, gas-limit, eager-mining, production, no-dev
- [x] External HTTP RPC on 0.0.0.0:8545
- [x] External WebSocket RPC on 0.0.0.0:8546
- [x] Runtime signer key loading from CLI `--signer-key` or `SIGNER_KEY` env var
- [x] Chain ID unified to 9323310 across dev and production genesis configs
- [x] PoaNode type replacing EthereumNode (injects PoaConsensus + PoaPayloadBuilder into Reth pipeline)
- [x] PoaConsensusBuilder wired into ComponentsBuilder
- [x] PoaPayloadBuilderBuilder wired into ComponentsBuilder (signs blocks, difficulty 1/2, epoch signers)
- [x] BlockSealer wired into payload pipeline via PoaPayloadBuilder.sign_payload()
- [x] Production genesis config (5 signers, 60M gas, tiered treasury/ops/community allocation)
- [x] Genesis extra_data with POA format (vanity + signers + seal)
- [x] Block monitoring task that logs signer turn info
- [x] ERC-4337 EntryPoint, WETH9, Multicall3, CREATE2 Deployer pre-deployed at genesis
- [x] Gnosis Safe v1.3.0 contracts pre-deployed in genesis (Singleton, Proxy Factory, Fallback Handler, MultiSend)
- [x] Governance contracts in genesis: ChainConfig, SignerRegistry, Treasury (with pre-populated storage)
- [x] meow_* RPC namespace: chainConfig, signers, nodeInfo
- [x] On-chain reader infrastructure (`onchain.rs`): StorageReader trait, slot constants, read_gas_limit(), read_signer_list(), is_signer_on_chain(), GenesisStorageReader
- [x] **Phase 3 wiring (2026-02-18)**: `StateProviderStorageReader` adapter bridges live Reth state to `StorageReader`; `PoaChainSpec.live_signers` `Arc<RwLock<...>>` cache shared between consensus+payload; `PoaPayloadBuilder` reads on-chain gas limit at startup + refreshes signer list at epoch blocks; `PoaConsensus` uses `effective_signers()` for live governance

---

## 2. Critical Gaps (Production Blockers)

### P0 - Must Fix Before Any Deployment

| # | Issue | Status | Details | File |
|---|-------|--------|---------|------|
| 1 | **Block signing not integrated** | FIXED | `PoaPayloadBuilder` wraps `EthereumPayloadBuilder` + POA signing. `BlockSealer.seal_header()` called in `sign_payload()`. Difficulty 1/2, epoch signer lists in extra_data. | `payload.rs`, `signer.rs` |
| 2 | **No external RPC server** | FIXED | HTTP RPC on `0.0.0.0:8545` and WS on `0.0.0.0:8546` configured via `RpcServerArgs`. | `main.rs` |
| 3 | **No consensus enforcement on sync** | FIXED | `PoaConsensus` validates headers with POA signature recovery in production mode. Dev mode skips signature checks. `recover_signer()` called in `validate_header()`. | `consensus.rs:249-287` |
| 4 | **Post-execution validation stubbed** | FIXED | Validates `gas_used`, receipt root, and logs bloom against pre-computed values. | `consensus.rs:393-429` |
| 5 | **Chain ID mismatch** | FIXED | All configs use 9323310. `genesis/sample-genesis.json` regenerated from code with correct chain ID, all contracts. | `genesis.rs`, `genesis/sample-genesis.json` |
| 6 | **No CLI argument parsing** | FIXED | Full `clap` CLI with all flags including `--gas-limit`, `--eager-mining`, `--production`. | `main.rs:62-118` |
| 7 | **Hardcoded dev keys in binary** | PARTIALLY FIXED | Production loads from `--signer-key` / `SIGNER_KEY`. Dev keys still hardcoded for dev mode. | `main.rs:156-175`, `signer.rs:205-216` |

### P0-ALPHA - Fundamental Architecture Problems

> **Progress update (2026-02-20):** ALL P0-ALPHA items FIXED + Phase 3-4 complete. Production NodeBuilder with MDBX. PoaConsensus validates signatures using live on-chain signer list. PoaPayloadBuilder signs blocks (difficulty 1/2, epoch signers), reads gas limit from ChainConfig, refreshes signers from SignerRegistry at epoch. StateProviderStorageReader wired. Timelock contract at genesis. Bootnode CLI. Fork choice rule. 224 tests pass. Requires rustc 1.93.1+.

| # | Issue | Status | What the code does now | What still needs to happen |
|---|-------|--------|------------------------|---------------------------|
| A1 | **`NodeConfig::test()` used** | FIXED | `NodeConfig::default()` with `.with_dev()`, `.with_rpc()`, `.with_chain()`, `.with_datadir_args()` | Done |
| A2 | **`testing_node_with_datadir()` used** | FIXED | Production `NodeBuilder::new(config).with_database(init_db()).with_launch_context(executor)` with persistent MDBX | Done |
| A3 | **`EthereumNode::default()` used** | FIXED | `.node(PoaNode::new(chain_spec).with_dev_mode(is_dev_mode))` injects `PoaConsensus` + `PoaPayloadBuilder` | Done |
| A4 | **No custom PayloadBuilder** | FIXED | `PoaPayloadBuilder` wraps `EthereumPayloadBuilder` + signs blocks with `BlockSealer`. Sets difficulty 1/2, embeds signer list at epoch blocks. | Done |
| A5 | **Consensus module is dead code** | FIXED | `PoaConsensus` LIVE in pipeline with signature verification | Done |
| A6 | **Signer module is dead code** | FIXED | `BlockSealer` wired into `PoaPayloadBuilder.sign_payload()`. `SignerManager` loaded and used for block production. | Done |

**Current architecture (2026-02-18 — Phase 3 complete):**

```
main.rs -> NodeConfig::default() + CLI args (clap)
  -> Production NodeBuilder with persistent MDBX database
  -> PoaNode (custom node type, dev_mode flag)
    -> Components:
      consensus:       PoaConsensus (LIVE - signature verification, timing, gas, receipt root)
                         uses effective_signers() → live on-chain or genesis fallback
      payload_builder: PoaPayloadBuilder (LIVE - signs blocks, difficulty 1/2, epoch signers)
                         reads gas_limit from ChainConfig at startup
                         refreshes signer list from SignerRegistry at every epoch block
      network:         EthereumNetworkBuilder (DEFAULT)
      pool:            EthereumPoolBuilder (DEFAULT)
    -> Block rewards: go to EIP-1967 miner proxy (0x...1967)
    -> Block production: signed POA blocks with round-robin signer rotation
    -> SignerManager + BlockSealer: wired into payload pipeline
    -> meow_* RPC: chainConfig, signers, nodeInfo
    -> Governance: ChainConfig + SignerRegistry + Treasury + Gnosis Safe in genesis
    -> Live signer cache: Arc<RwLock<...>> in PoaChainSpec shared across consensus+payload
```

### P1 - Required for Production

| # | Issue | Details |
|---|-------|---------|
| 8 | No admin/debug/txpool RPC namespaces | Can't manage node, trace transactions, or inspect mempool |
| 9 | No signer voting mechanism | Can't add/remove signers dynamically via governance |
| 10 | No monitoring/metrics (Prometheus) | Port 9001 exposed but no metrics server running |
| 11 | No CI/CD pipeline | No automated testing, linting, or deployment |
| 12 | ~~No integration tests~~ | FIXED — 28 integration tests: 3-signer network, state sync, fork choice, multi-node scenarios |
| 13 | ~~No bootnodes configured~~ | FIXED — `--bootnodes`, `--port`, `--disable-discovery` CLI flags wired to `NetworkArgs` |
| 14 | Reth deps pinned to `main` branch | Bleeding edge, risk of breaking changes. Should pin to release tags |

---

## 2.5 Multi-Node POA Operation (How Others Run the Chain)

> **No beacon chain needed.** POA is self-contained. Signers ARE the consensus. No validators, no staking, no attestations. Each signer node takes turns producing blocks in round-robin order.

### Current State: Multi-Node Ready (Phase 4 Complete)

The chain has **full multi-node support at the consensus layer**:
- [x] Bootnode CLI flags (`--bootnodes`, `--port`, `--disable-discovery`) wired to Reth's `NetworkArgs`
- [x] Fork choice rule (`is_in_turn`, `score_chain`, `compare_chains`) for selecting preferred chain
- [x] State sync validation: consensus correctly validates chains of 100+ blocks from other signers
- [x] 28 integration tests covering 3/5-signer networks, signer add/remove, double signing, chain reorg
- [ ] Live multi-node deployment (requires Docker orchestration / separate machines)

### Network Topology for POA

```
What a real POA network looks like:

                    ┌─────────────────────┐
                    │   Bootnode(s)        │
                    │   (discovery only,   │
                    │    no signing)        │
                    └─────────┬───────────┘
                              │
              ┌───────────────┼───────────────┐
              │               │               │
     ┌────────▼──────┐ ┌─────▼───────┐ ┌─────▼───────┐
     │ Signer Node 1 │ │ Signer Node 2│ │ Signer Node 3│
     │ (Account 0)   │ │ (Account 1)  │ │ (Account 2)  │
     │ Produces block │ │ Produces block│ │ Produces block│
     │ every 3rd turn │ │ every 3rd turn│ │ every 3rd turn│
     │ Has private key│ │ Has private key│ │ Has private key│
     └───────┬────────┘ └──────┬───────┘ └──────┬───────┘
             │                 │                 │
     ┌───────▼─────────────────▼─────────────────▼───────┐
     │              Full Nodes (RPC nodes)                │
     │  - No signing keys                                 │
     │  - Validate and store all blocks                   │
     │  - Serve RPC to users (MetaMask, dApps)           │
     │  - Anyone can run one                              │
     └───────────────────────────────────────────────────┘
```

### Node Types in POA

| Node Type | Has Private Key | Produces Blocks | Validates Blocks | Serves RPC | Who Runs It |
|-----------|----------------|-----------------|------------------|------------|-------------|
| **Signer Node** | Yes | Yes (when in-turn) | Yes | Optional | Authorized signers only |
| **Full Node** | No | No | Yes | Yes | Anyone |
| **Archive Node** | No | No | Yes (all history) | Yes | Infrastructure providers |
| **Bootnode** | No | No | No | No | Chain operators |

### How a New Operator Joins the Network

**Step 1: Get the genesis file**
```bash
# The genesis.json must be IDENTICAL across all nodes
# It defines: chain ID, initial state, signer list, system contracts
# Distribute via: git repo, IPFS, or direct download
curl -O https://meowchain.example.com/genesis.json
```

**Step 2: Initialize the node from genesis**
```bash
# This creates the database with the exact same initial state
meowchain init --genesis genesis.json --datadir /data/meowchain
```

**Step 3: Connect to the network**
```bash
# Bootnodes are the entry point to find other peers
meowchain run \
  --datadir /data/meowchain \
  --bootnodes "enode://<pubkey>@<ip>:30303,enode://<pubkey2>@<ip2>:30303" \
  --http --http.addr 0.0.0.0 --http.port 8545 \
  --ws --ws.addr 0.0.0.0 --ws.port 8546 \
  --port 30303
```

**Step 4: Sync state from peers**
```
Node connects to peers -> requests headers -> validates POA signatures
-> downloads block bodies -> replays transactions -> builds local state
-> reaches chain tip -> now a full node
```

**Step 5 (Signer only): Import signing key**
```bash
# Only if this node is an authorized signer
meowchain account import --keyfile signer-key.json --datadir /data/meowchain

# Then run with signing enabled
meowchain run \
  --datadir /data/meowchain \
  --signer 0xYourSignerAddress \
  --unlock 0xYourSignerAddress \
  --bootnodes "enode://..." \
  --mine  # Enable block production
```

### What's Missing for Multi-Node

| Component | Status | What's Needed |
|-----------|--------|---------------|
| **`meowchain init` command** | Not implemented | CLI subcommand to initialize DB from genesis.json |
| **`meowchain run` command** | Partially done | CLI exists with `--datadir`, `--http-*`, `--ws-*`, `--signer-key` flags. Missing: `--bootnodes`, `--port`, `--mine`, `--unlock` |
| **`meowchain account` command** | Not implemented | Import/export/list signing keys |
| **Genesis file distribution** | Done | `genesis.rs` generates canonical JSON. `genesis/sample-genesis.json` (dev, chain ID 9323310, all allocs) and `genesis/production-genesis.json` are both current. |
| **Bootnode infrastructure** | CLI done | `--bootnodes`, `--port`, `--disable-discovery` CLI flags wired to Reth `NetworkArgs`. Need static IPs/DNS for deployment. |
| **Enode URL generation** | Not implemented | Each node needs a public enode URL for peering |
| **State sync protocol** | Validated | Consensus validates 100+ block chain segments. Reth's built-in sync engine works with `PoaConsensus`. 5 sync validation tests. |
| **Signer key isolation** | DONE | `--signer-key` CLI flag and `SIGNER_KEY` env var. In production mode, runs as non-signer if no key provided. Dev keys only loaded in dev mode. |
| **Block production scheduling** | Partially done | Round-robin logic exists in `chainspec.rs:expected_signer()`. Monitoring task detects in-turn/out-of-turn. But NOT enforced in block building. |
| **Fork choice rule** | Done | `is_in_turn()`, `score_chain()`, `compare_chains()` in consensus.rs. Prefers in-turn signers, then longer chains. 8 tests. |
| **Signer voting** | Not implemented | `clique_propose(address, true/false)` to add/remove signers |
| **Epoch checkpoints** | Partially done | `is_epoch_block()` and `extract_signers_from_epoch_block()` exist in `consensus.rs`. Genesis extra_data includes signers. But NOT embedded during block production at epoch boundaries. |

### State Management When Multiple Nodes Run

```
The key insight: EVERY full node has the COMPLETE state.

Block 0 (Genesis):
  All nodes start from identical genesis.json
  State: same prefunded accounts, same system contracts

Block 1..N (Normal operation):
  Signer produces block -> broadcasts to all peers
  Each peer: validates signature -> executes transactions -> updates state
  Result: all nodes have identical state at every block height

Block N (New node joins late):
  Option A - Full Sync:
    Download all blocks 0..N from peers
    Replay every transaction sequentially
    End up with identical state at block N
    Slow but trustless (verifies every POA signature)

  Option B - Snap Sync (needs implementation):
    Download state snapshot at recent block M
    Verify snapshot against known block hash
    Download and replay blocks M..N
    Much faster, still verifiable

Block N+K (Node was offline, comes back):
    Node knows it was at block N
    Requests blocks N+1..N+K from peers
    Validates and replays each block
    Catches up to current chain tip
    RESUMES EXACTLY where it left off
```

### Decentralization in POA Context

POA is **intentionally semi-centralized** - that's the tradeoff:

| Aspect | POA (Meowchain) | PoS (Ethereum Mainnet) | Why POA is different |
|--------|-----------------|----------------------|---------------------|
| Who produces blocks | Fixed set of known signers | Any validator who stakes 32 ETH | Trust is in identity, not economics |
| How to join as producer | Must be voted in by existing signers | Deposit 32 ETH | Permission-based, not permissionless |
| Finality | Immediate (N/2+1 signers confirm) | ~13 min (2 epochs) | Fewer participants = faster |
| Censorship resistance | Lower (signers can collude) | Higher (thousands of validators) | Tradeoff for speed |
| Running a full node | Anyone can | Anyone can | Same - read access is permissionless |
| Sybil resistance | Identity-based (known entities) | Economic (staking cost) | No capital requirement |
| Block time | Configurable (2s, 12s, etc.) | Fixed 12s | More flexible |
| Throughput | Higher (fewer validators to coordinate) | Lower (global consensus) | POA can push gas limits higher |

### Scaling Approaches for POA

Since there's no beacon chain overhead, POA can scale differently:

| Approach | Description | Complexity |
|----------|-------------|------------|
| **Increase gas limit** | POA signers can agree to raise gas limit (e.g., 60M, 100M, 300M). No global consensus needed, just signer agreement | Low |
| **Decrease block time** | 2s -> 1s -> 500ms blocks. Feasible with few signers on good hardware | Low |
| **Parallel EVM execution** | Reth already has foundations for this. Execute non-conflicting txs in parallel | Medium |
| **State pruning** | Aggressive pruning since signers are trusted. Keep only recent state + proofs | Medium |
| **Read replicas** | Run many non-signer full nodes behind a load balancer for RPC traffic | Low |
| **Horizontal RPC scaling** | Multiple RPC nodes + Redis cache + load balancer | Medium |
| **L2 on top of POA** | Deploy an OP Stack / Arbitrum rollup on top of Meowchain as L1 | High |

---

## 3. Remaining Infrastructure

### Networking & P2P

- [ ] Custom P2P handshake with POA chain verification
- [ ] Bootnode configuration and discovery
- [ ] Peer filtering (reject non-POA peers)
- [ ] Network partition recovery
- [ ] Peer reputation / banning malicious peers

### RPC Server

- [x] HTTP JSON-RPC on port 8545 (configurable via `--http-addr` / `--http-port`)
- [x] WebSocket JSON-RPC on port 8546 (configurable via `--ws-addr` / `--ws-port`)
- [x] `eth_*` namespace (provided by Reth's default EthereumEthApiBuilder)
- [x] `web3_*` namespace (provided by Reth)
- [x] `net_*` namespace (provided by Reth)
- [ ] `admin_*` namespace (addPeer, removePeer, nodeInfo)
- [ ] `debug_*` namespace (traceTransaction, traceBlock)
- [ ] `txpool_*` namespace (content, status, inspect)
- [ ] `clique_*` namespace (getSigners, propose, discard) - POA specific (NEEDS CUSTOM IMPL)
- [ ] CORS configuration
- [ ] Rate limiting
- [ ] API key authentication

### State Management

- [ ] Configurable pruning (archive vs. pruned node)
- [ ] State snapshot export/import
- [ ] State sync from peers (fast sync)
- [ ] State trie verification
- [ ] Dead state garbage collection

### Monitoring & Observability

- [ ] Prometheus metrics endpoint (:9001)
- [ ] Grafana dashboard templates
- [ ] Block production rate monitoring
- [ ] Signer health checks
- [ ] Peer count monitoring
- [ ] Mempool size tracking
- [ ] Chain head monitoring
- [ ] Alerting (PagerDuty, Slack, etc.)
- [ ] Structured logging (JSON format)

### Security

- [ ] Encrypted keystore (EIP-2335 style)
- [ ] Key rotation mechanism
- [ ] RPC authentication (JWT for Engine API exists, need for public RPC)
- [ ] DDoS protection
- [ ] Firewall rules documentation
- [ ] Security audit
- [ ] Signer multi-sig support

### Developer Tooling

- [ ] Hardhat/Foundry network config template
- [ ] Contract verification on Blockscout
- [ ] Faucet for testnet tokens
- [ ] Gas estimation service
- [ ] Block explorer API (REST + GraphQL)
- [ ] SDK / client library

---

## 4. Chain Recovery & Resumption

### Current State: Partial Support

Reth's MDBX database persists across restarts. The chain **will resume from the last block** on normal restart. However, several recovery scenarios are NOT handled:

### What Works

| Scenario | Status | How |
|----------|--------|-----|
| Normal restart | Works | MDBX persists state in `data/db/`. Node reads last known head on startup |
| Data directory intact | Works | `data/static_files/` has headers, txns, receipts |

### What's Missing

| Scenario | Status | What's Needed |
|----------|--------|---------------|
| **Corrupted database** | Not handled | Need `reth db repair` or reimport from genesis + replay |
| **State export/import** | Not implemented | Need `reth dump-genesis` equivalent for current state |
| **Snapshot sync** | Not implemented | Need snapshot creation at epoch blocks and distribution |
| **Block replay from backup** | Not implemented | Need block export/import tooling |
| **Disaster recovery** | No plan | Need documented recovery procedures |
| **Multi-node failover** | Not implemented | Need signer failover if primary goes down |
| **Fork resolution** | Not implemented | POA should have canonical fork choice based on signer authority |

### Required Implementation

```
Recovery Tooling Needed:
1. `meowchain export-state --block <number> --output state.json`
2. `meowchain import-state --input state.json`
3. `meowchain export-blocks --from <start> --to <end> --output blocks.rlp`
4. `meowchain import-blocks --input blocks.rlp`
5. `meowchain db repair`
6. `meowchain db verify`
7. Epoch-based automatic snapshots
8. Signer failover with health monitoring
```

---

## 5. Upgrade Mechanism (Hardfork Support)

### Current State: Manual Recompilation Required

All hardforks are activated at genesis (block 0 / timestamp 0). There is **no mechanism** to schedule future hardforks at specific block heights or timestamps.

### What's Needed

| Feature | Status | Description |
|---------|--------|-------------|
| Timestamp-based hardfork scheduling | Not implemented | Schedule future activations like `fusaka_time: 1735689600` |
| Block-based hardfork scheduling | Not implemented | Schedule at specific block numbers |
| On-chain governance for upgrades | Not implemented | Signer voting for hardfork activation |
| Rolling upgrade support | Not implemented | Upgrade nodes one-by-one without downtime |
| Feature flags | Not implemented | Enable/disable features via config |
| Client version signaling | Not implemented | Nodes advertise supported hardforks |
| Emergency hardfork | Not implemented | Fast-track activation for critical patches |

### How Ethereum Mainnet Handles Upgrades

```
1. EIP proposed -> reviewed -> accepted for hardfork
2. Client teams implement in devnets
3. Tested on Holesky/Sepolia testnets
4. Activation time announced (timestamp for post-Merge)
5. All nodes must update before activation time
6. Hardfork activates at exact timestamp across network
7. Nodes running old software fork off and become invalid
```

### Recommended Implementation for Meowchain

```rust
// In chainspec.rs - add configurable future hardforks
pub struct HardforkSchedule {
    pub fusaka_time: Option<u64>,      // Timestamp-based activation
    pub glamsterdam_time: Option<u64>,
    pub custom_forks: BTreeMap<String, u64>,
}

// In genesis.json or chain config:
{
    "config": {
        "pragueTime": 0,
        "fusakaTime": 1735689600,  // Future activation
        "glamsterdamTime": null     // Not yet scheduled
    }
}
```

---

## 6. All Finalized EIPs by Hardfork

### Frontier (Block 0 - July 30, 2015)
> Genesis launch. Base EVM with ~60 opcodes, 5 ETH block reward, Ethash PoW.

### Homestead (Block 1,150,000 - March 14, 2016)

| EIP | Name | Description |
|-----|------|-------------|
| EIP-2 | Homestead Changes | Contract creation cost, tx signature rules, difficulty adjustment |
| EIP-7 | DELEGATECALL | Opcode 0xf4 for delegating execution while preserving caller context |
| EIP-8 | devp2p Forward Compatibility | Networking layer future-proofing |

### Tangerine Whistle (Block 2,463,000 - October 18, 2016)

| EIP | Name | Description |
|-----|------|-------------|
| EIP-150 | Gas cost changes for IO-heavy operations | Repriced opcodes to prevent DoS attacks |

### Spurious Dragon (Block 2,675,000 - November 22, 2016)

| EIP | Name | Description |
|-----|------|-------------|
| EIP-155 | Simple replay attack protection | Chain ID in transaction signatures |
| EIP-160 | EXP cost increase | Balanced computational cost |
| EIP-161 | State trie clearing | Remove empty accounts from DoS attacks |
| EIP-170 | Contract code size limit | Max 24,576 bytes bytecode |

### Byzantium (Block 4,370,000 - October 16, 2017)

| EIP | Name | Description |
|-----|------|-------------|
| EIP-100 | Difficulty adjustment including uncles | Prevents difficulty manipulation |
| EIP-140 | REVERT instruction | Stop execution, revert state, return data without consuming all gas |
| EIP-196 | alt_bn128 addition and scalar multiplication | Precompile for ZK-SNARK verification |
| EIP-197 | alt_bn128 pairing check | Precompile for ZK-SNARK pairing |
| EIP-198 | Big integer modular exponentiation | RSA and crypto precompile |
| EIP-211 | RETURNDATASIZE and RETURNDATACOPY | Variable-length return values |
| EIP-214 | STATICCALL | Non-state-changing calls |
| EIP-649 | Difficulty bomb delay + reward reduction | Block reward: 5 ETH -> 3 ETH |
| EIP-658 | Transaction status code in receipts | 0=failure, 1=success |

### Constantinople (Block 7,280,000 - February 28, 2019)

| EIP | Name | Description |
|-----|------|-------------|
| EIP-145 | Bitwise shifting (SHL, SHR, SAR) | Native shift opcodes, 3 gas each |
| EIP-1014 | CREATE2 | Deterministic contract addresses |
| EIP-1052 | EXTCODEHASH | Efficient contract code hash |
| EIP-1234 | Difficulty bomb delay + reward reduction | Block reward: 3 ETH -> 2 ETH |

### Istanbul (Block 9,069,000 - December 8, 2019)

| EIP | Name | Description |
|-----|------|-------------|
| EIP-152 | BLAKE2b precompile | Zcash interoperability |
| EIP-1108 | Reduce alt_bn128 gas costs | Cheaper ZK-SNARK verification |
| EIP-1344 | ChainID opcode | On-chain chain ID access |
| EIP-1884 | Repricing trie-dependent opcodes | SLOAD 200->800 gas |
| EIP-2028 | Calldata gas reduction | 68->16 gas per non-zero byte |
| EIP-2200 | SSTORE gas rebalancing | Net metering with reentrancy guard |

### Berlin (Block 12,244,000 - April 15, 2021)

| EIP | Name | Description |
|-----|------|-------------|
| EIP-2565 | ModExp gas cost reduction | Cheaper modular exponentiation |
| EIP-2718 | Typed Transaction Envelope | Foundation for future tx types |
| EIP-2929 | Gas cost increase for cold state access | DoS prevention via warm/cold access |
| EIP-2930 | Access Lists (Type 1 tx) | Declare accessed addresses/keys upfront |

### London (Block 12,965,000 - August 5, 2021)

| EIP | Name | Description |
|-----|------|-------------|
| EIP-1559 | Fee market change | Base fee (burned) + priority fee. Type 2 tx |
| EIP-3198 | BASEFEE opcode | On-chain base fee access |
| EIP-3529 | Reduce gas refunds | Kill gas tokens, reduce SELFDESTRUCT refund |
| EIP-3541 | Reject 0xEF prefix contracts | Reserve for future EOF |
| EIP-3554 | Difficulty bomb delay | Push to December 2021 |

### Paris / The Merge (Block 15,537,394 - September 15, 2022)

| EIP | Name | Description |
|-----|------|-------------|
| EIP-3675 | Upgrade to Proof-of-Stake | Replace PoW with PoS. Remove mining, uncles |
| EIP-4399 | DIFFICULTY -> PREVRANDAO | On-chain randomness from beacon chain |

### Shanghai (April 12, 2023)

| EIP | Name | Description |
|-----|------|-------------|
| EIP-3651 | Warm COINBASE | Reduce gas for MEV builder interactions |
| EIP-3855 | PUSH0 | Push zero onto stack (saves gas) |
| EIP-3860 | Limit and meter initcode | Max 49,152 bytes, gas per chunk |
| EIP-4895 | Beacon chain withdrawals | Validators can withdraw staked ETH |
| EIP-6049 | Deprecate SELFDESTRUCT | Formal deprecation notice |

### Cancun / Dencun (March 13, 2024)

| EIP | Name | Description |
|-----|------|-------------|
| EIP-4844 | Proto-Danksharding (Blob tx) | Type 3 tx with temporary blob data for L2 rollups |
| EIP-1153 | Transient storage (TSTORE/TLOAD) | Auto-cleared per-transaction storage |
| EIP-4788 | Beacon block root in EVM | System contract exposing consensus state |
| EIP-5656 | MCOPY | Efficient memory-to-memory copy |
| EIP-6780 | Restrict SELFDESTRUCT | Only works in same-tx contract creation |
| EIP-7516 | BLOBBASEFEE opcode | On-chain blob fee access |

### Prague / Pectra (May 7, 2025)

| EIP | Name | Description |
|-----|------|-------------|
| EIP-2537 | BLS12-381 precompile | Native BLS curve operations |
| EIP-2935 | Historical block hashes from state | ~8191 blocks accessible via system contract |
| EIP-6110 | Validator deposits on chain | Faster deposit processing (~13 min) |
| EIP-7002 | EL triggerable withdrawals | Exit validators from smart contracts |
| EIP-7251 | Increase MAX_EFFECTIVE_BALANCE | 32 ETH -> 2,048 ETH per validator |
| EIP-7549 | Committee index outside Attestation | 60x attestation aggregation improvement |
| EIP-7623 | Increase calldata cost | Push rollups toward blob usage |
| EIP-7685 | General purpose EL requests | Standard EL<->CL communication |
| EIP-7691 | Blob throughput increase | Target 6 blobs/block (was 3), max 9 (was 6) |
| EIP-7702 | Set EOA account code | EOAs delegate to smart contract code. Type 0x04 tx. Batch/sponsor/session keys |
| EIP-7840 | Blob schedule in EL config | Configurable blob params |

### Fusaka (December 3, 2025) -- NOT YET IN MEOWCHAIN

| EIP | Name | Description | Priority |
|-----|------|-------------|----------|
| EIP-7594 | PeerDAS | Data availability sampling for blobs | HIGH |
| EIP-7642 | History Expiry | Safe pruning of old chain data | MEDIUM |
| EIP-7823 | MODEXP Bounds | Cost limits for modexp precompile | LOW |
| EIP-7825 | Transaction Gas Limit Cap | Hard cap ~16.8M gas per tx | MEDIUM |
| EIP-7883 | MODEXP Gas Cost Increase | Adjusted gas pricing | LOW |
| EIP-7892 | Blob Parameter Only Hardforks | Adjust blobs without full upgrade | MEDIUM |
| EIP-7917 | Deterministic Proposer Lookahead | Predictable proposer sets | LOW |
| EIP-7918 | Blob Base Fee Floor | Reserve price for blob fees | LOW |
| EIP-7934 | RLP Block Size Limit | Cap at 10 MiB per block | MEDIUM |
| EIP-7935 | Default Gas Limit 60M | Double throughput | HIGH |
| EIP-7939 | CLZ Opcode | Count leading zeros for 256-bit | LOW |
| EIP-7951 | secp256r1 Precompile | Native WebAuthn/passkey support | HIGH |

---

## 7. ERC Standards Support

> ERCs are smart contract standards. They work automatically on any EVM-compatible chain - **no special chain-level support needed** for most of them. The EVM executes them as regular bytecode.

### Tier 1: Core Token Standards (Automatic - EVM handles these)

| ERC | Name | Status on Meowchain | Notes |
|-----|------|---------------------|-------|
| ERC-20 | Fungible Tokens | Supported (EVM native) | USDC, USDT, WETH, DAI pattern |
| ERC-721 | NFTs | Supported (EVM native) | Unique tokens, `ownerOf`, `safeTransferFrom` |
| ERC-1155 | Multi-Token | Supported (EVM native) | Batch operations, gaming assets |
| ERC-165 | Interface Detection | Supported (EVM native) | `supportsInterface()` |

### Tier 2: Account Abstraction & Modern Wallets

| ERC | Name | Status on Meowchain | Notes |
|-----|------|---------------------|-------|
| ERC-4337 | Account Abstraction (Alt Mempool) | EntryPoint v0.7 PRE-DEPLOYED in genesis | `0x0000000071727De22E5E9d8BAf0edAc6f37da032`. Still needs Bundler service. |
| EIP-7702 | EOA Account Code | Supported (Prague active) | Type 0x04 tx enabled at genesis |
| ERC-7579 | Modular Smart Accounts | Needs contract deployment | Plugin architecture for smart wallets |
| ERC-1271 | Contract Signature Validation | Supported (EVM native) | `isValidSignature()` |

### Tier 3: DeFi Standards

| ERC | Name | Status on Meowchain | Notes |
|-----|------|---------------------|-------|
| ERC-2612 | Permit (Gasless Approvals) | Supported (EVM native) | Requires EIP-712 typed data |
| ERC-4626 | Tokenized Vaults | Supported (EVM native) | Standard vault interface for DeFi |
| ERC-2981 | NFT Royalties | Supported (EVM native) | `royaltyInfo()` |
| ERC-6551 | Token Bound Accounts | Supported (EVM native) | NFTs own wallets |
| ERC-777 | Enhanced Tokens | Supported (EVM native) | Hooks on send/receive (reentrancy risk) |

### Tier 4: Infrastructure ERCs

| ERC | Name | Status on Meowchain | Notes |
|-----|------|---------------------|-------|
| EIP-712 | Typed Structured Data Signing | Supported (EVM native) | Used by permit, 4337, 8004 |
| EIP-155 | Replay Protection | Supported | Chain ID in tx signatures |
| ERC-1820 | Interface Registry | Needs deployment | Universal registry contract |
| ERC-173 | Contract Ownership | Supported (EVM native) | `owner()`, `transferOwnership()` |
| ERC-2771 | Meta Transactions | Supported (EVM native) | Trusted forwarder pattern |

### Tier 5: Emerging Standards (2025-2026)

| ERC | Name | Status on Meowchain | Action Required |
|-----|------|---------------------|-----------------|
| **ERC-8004** | Trustless AI Agents | Needs deployment | **See Section 8 below** |
| ERC-6900 | Modular Smart Accounts | Needs deployment | Alternative to ERC-7579 |

### What Meowchain Needs to Deploy for Full ERC Ecosystem

```
Priority 1 (Essential):
  - [x] ERC-4337 EntryPoint contract (v0.7) -- PRE-DEPLOYED IN GENESIS at 0x0000000071727De22E5E9d8BAf0edAc6f37da032
  - [ ] ERC-4337 Bundler service (off-chain component, not a contract)
  - [ ] ERC-4337 Paymaster contracts (for gasless tx)
  - [x] WETH (Wrapped ETH) contract -- PRE-DEPLOYED IN GENESIS at 0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2
  - [x] Multicall3 contract (batch reads) -- PRE-DEPLOYED IN GENESIS at 0xcA11bde05977b3631167028862bE2a173976CA11
  - [x] CREATE2 Deployer (deterministic addresses) -- PRE-DEPLOYED IN GENESIS at 0x4e59b44847b379578588920cA78FbF26c0B4956C
  - [x] SimpleAccountFactory (ERC-4337 wallet factory) -- PRE-DEPLOYED IN GENESIS at 0x9406Cc6185a346906296840746125a0E44976454
  - [ ] ERC-1820 Registry

Priority 2 (Ecosystem Growth):
  - [ ] ERC-8004 registries (Identity, Reputation, Validation)
  - [ ] Uniswap V3/V4 or equivalent DEX
  - [ ] Chainlink oracle contracts (or equivalent)
  - [ ] ENS-equivalent naming system

Priority 3 (Developer Experience):
  - [ ] Hardhat/Foundry verification support
  - [ ] Sourcify integration
  - [ ] Standard proxy patterns (ERC-1967 transparent, UUPS)
```

---

## 8. ERC-8004: Trustless AI Agents

> **Status:** Draft | **Live on Ethereum Mainnet:** January 29, 2026
> **Purpose:** On-chain infrastructure for autonomous AI agents to discover, interact, and trust each other without pre-existing trust relationships.

### What It Does

ERC-8004 extends Google's Agent-to-Agent (A2A) protocol with an **on-chain trust layer**. Three registries:

### 8.1 Identity Registry (Built on ERC-721)

```
Each AI agent gets:
- Globally unique ID: {namespace}:{chainId}:{registryAddress}
- NFT-based identity (transferable, browseable)
- agentURI -> registration JSON containing:
  - Name, description
  - Service endpoints (A2A, MCP, ENS, DID, email, web)
  - Supported trust models
  - x402 payment support indicator
  - Multi-chain entries
```

### 8.2 Reputation Registry

```
- giveFeedback() callable by any address
- Fixed-point ratings (int128) with configurable decimals
- Tag-based filtering (tag1, tag2)
- Off-chain detail URIs with KECCAK-256 integrity hashing
- Response/dispute mechanism
- Immutable on-chain (revocation only flags, doesn't delete)
```

### 8.3 Validation Registry

```
- Generic hooks for independent verification of agent work
- Supported verification methods:
  - Stake-secured re-execution validators
  - Zero-knowledge ML (zkML) proofs
  - TEE (Trusted Execution Environment) oracles
  - Custom validator contracts
- Flow: validationRequest() -> validationResponse()
- Responses on 0-100 scale with evidence URIs
```

### Dependencies for ERC-8004 on Meowchain

```
Required:
  - [ ] EIP-155 (chain ID) -- DONE
  - [ ] EIP-712 (typed data signing) -- DONE (EVM native)
  - [ ] ERC-721 (NFT) -- DONE (EVM native)
  - [ ] ERC-1271 (contract signatures) -- DONE (EVM native)

Deploy:
  - [ ] Identity Registry contract
  - [ ] Reputation Registry contract
  - [ ] Validation Registry contract
  - [ ] Agent Wallet management integration
  - [ ] A2A protocol endpoint on chain RPC
```

### Ecosystem Building on ERC-8004

| Project | What It Does |
|---------|-------------|
| Unibase | Persistent memory storage tied to agent identities |
| x402 Protocol | Agent-to-agent payments |
| ETHPanda | Community tooling for trustless agents |

---

## 9. Upcoming Ethereum Upgrades

### Fusaka (December 3, 2025) -- MEOWCHAIN NEEDS THIS

**Headline features:**
- **PeerDAS (EIP-7594):** Nodes sample blob data instead of downloading all. Massive DA scaling
- **secp256r1 precompile (EIP-7951):** Native WebAuthn/passkey support
- **60M gas limit (EIP-7935):** Double throughput
- **Transaction gas cap (EIP-7825):** Prevents single-tx DoS

**Action for Meowchain:**
```
- [ ] Update Reth dependency to include Fusaka support
- [ ] Add fusakaTime to chain config
- [ ] Deploy any new Fusaka system contracts
- [ ] Test all 12 Fusaka EIPs
- [ ] Update chainspec.rs hardfork list
```

### Glamsterdam (Targeted: May/June 2026) -- PLAN AHEAD

**Confirmed:**
- **EIP-7732: Enshrined Proposer-Builder Separation (ePBS)** -- Protocol-level PBS, eliminates MEV-Boost relay dependency
- **EIP-7928: Block-level Access Lists** -- Gas efficiency optimization
- Parallel EVM execution under discussion

**Action for Meowchain:**
```
- [ ] Monitor Glamsterdam EIP finalization
- [ ] Plan ePBS integration (or skip if POA makes it irrelevant)
- [ ] Implement upgrade scheduling mechanism before this ships
```

### Hegota (Targeted: Late 2026) -- LONG-TERM

**Leading candidates:**
- **Verkle Trees:** Replace Merkle Patricia Tries. 10x smaller proofs, enables stateless clients
- **State/History Expiry:** Archive old data, prevent state bloat
- **EVM Optimizations:** Faster/cheaper execution
- Targeting 180M gas limit

### Ethereum Roadmap Pillars (2027+)

| Pillar | Focus | Key Tech |
|--------|-------|----------|
| The Surge | 100,000+ TPS | Full Danksharding, ZK-EVM |
| The Scourge | MEV mitigation | Encrypted mempools, inclusion lists |
| The Verge | Statelessness | Verkle trees, stateless clients |
| The Purge | State cleanup | State expiry, EVM simplification |
| The Splurge | Everything else | Account abstraction, VDFs |

---

## 10. Production Infrastructure Checklist

### Block Explorer

| Solution | Status | Notes |
|----------|--------|-------|
| Blockscout (via Scoutup) | Partially done | Go wrapper exists, needs full integration |
| Contract verification | Not done | Need Sourcify or Blockscout verification API |
| Token tracking | Not done | ERC-20/721/1155 indexing |
| Internal tx tracing | Not done | Requires debug_traceTransaction RPC |

### Bridges

| Feature | Status | Options |
|---------|--------|---------|
| Bridge to Ethereum mainnet | Not done | Chainlink CCIP, LayerZero, Hyperlane, custom |
| Bridge to other L2s | Not done | Across, Wormhole |
| Canonical bridge contract | Not done | Lock-and-mint or burn-and-mint |
| Bridge UI | Not done | Frontend for bridging |

### Oracles

| Feature | Status | Options |
|---------|--------|---------|
| Price feeds | Not done | Chainlink, Pyth, Chronicle, Redstone |
| VRF (verifiable randomness) | Not done | Chainlink VRF |
| Automation/Keepers | Not done | Chainlink Automation |
| Data feeds for AI agents | Not done | Custom oracle for ERC-8004 |

### MEV Protection

| Feature | Status | Relevance |
|---------|--------|-----------|
| MEV-Boost | Not needed | POA signers control ordering |
| Fair ordering | Partially done | Round-robin signers provide basic fairness |
| Encrypted mempool | Not done | Prevent frontrunning by signers |
| PBS (Proposer-Builder Separation) | Not needed for POA | May matter if transitioning to PoS |

### Data Availability (if operating as L2)

| Solution | Status | Notes |
|----------|--------|-------|
| Ethereum blobs (EIP-4844) | Supported at EVM level | Need sequencer to post blobs |
| Celestia | Not integrated | Alternative DA |
| EigenDA | Not integrated | Restaking-secured DA |

### Wallet & Key Infrastructure

| Feature | Status | Notes |
|---------|--------|-------|
| MetaMask support | UNBLOCKED | External RPC on 0.0.0.0:8545 is live. MetaMask can connect via `Add Network` with chain ID 9323310. Needs testing. |
| WalletConnect | Not done | Needs chain registry listing |
| Hardware wallet signing | Not done | Ledger/Trezor for signers |
| Faucet | Not done | Testnet token distribution |

### Developer Experience

| Feature | Status | Notes |
|---------|--------|-------|
| Hardhat config template | Not done | Network config + verification |
| Foundry config template | Not done | `foundry.toml` with chain RPC |
| Subgraph support (The Graph) | Not done | Event indexing |
| SDK / client library | Not done | TypeScript/Python wrappers |
| Documentation site | Not done | API docs, tutorials |

---

## 11. Codebase Issues Found During Audit

> Issues discovered during the 2026-02-12 code review that need attention.

### Critical Issues

| # | Issue | File | Details |
|---|-------|------|---------|
| C1 | **`testing_node_with_datadir()` still used** | ~~`main.rs:219`~~ | **FIXED** - Now uses production `NodeBuilder::new(config).with_database(init_db()).with_launch_context(executor)` with persistent MDBX database. |
| C2 | **Block monitoring logs but doesn't sign** | ~~`main.rs`~~ | **FIXED** - `PoaPayloadBuilder.sign_payload()` calls `BlockSealer.seal_header()` during block production. Block monitoring task now reports signed blocks. |
| C3 | **`validate_header()` doesn't verify signatures** | ~~`consensus.rs`~~ | **FIXED** - Production mode calls `recover_signer()` and `validate_signer()` to verify block signatures. Dev mode skips (unsigned blocks). |
| C4 | **`validate_block_pre_execution()` silently allows invalid extra_data** | ~~`consensus.rs`~~ | **FIXED** - Production mode rejects blocks with extra_data shorter than vanity+seal. Dev mode allows (unsigned blocks from Reth dev mining). |

### Non-Critical Issues

| # | Issue | File | Details |
|---|-------|------|---------|
| N1 | **`sample-genesis.json` is stale** | ~~`genesis/sample-genesis.json`~~ | **FIXED** - Regenerated from code with chain ID 9323310, all 30 alloc entries (20 dev + 4 system + 5 infra + 1 miner proxy). Now in `genesis/` dir. |
| N2 | **Dockerfile CMD format mismatch** | ~~`Docker/Dockerfile`~~ | **FIXED** - CMD uses correct `--http-addr`, `--http-port`, `--ws-addr`, `--ws-port` format. Now in `Docker/` dir. |
| N3 | **Dockerfile copies wrong binary name** | ~~`Docker/Dockerfile`~~ | **FIXED** - Copies `target/release/example-custom-poa-node` and renames to `meowchain`. |
| N5 | **Production config uses dev account keys** | `genesis.rs:130` | Still uses `dev_accounts()[0..5]` as signers. Real production MUST use unique keys. |
| N7 | **Double block stream subscription** | ~~`main.rs`~~ | **FIXED** - Single `canonical_state_stream()` subscription. |

### Suggestions for Next Steps

1. **DONE (2026-02-18):** On-chain contract reads wired into payload builder & consensus. `StateProviderStorageReader` bridges live Reth state. Gas limit read from `ChainConfig` at startup, signer list refreshed from `SignerRegistry` at every epoch block. `PoaConsensus` uses `effective_signers()` for live governance.

2. **DONE (2026-02-20):** Production genesis regenerated with all 25 alloc entries (governance, Safe, infra, miner proxy contracts). Both `genesis/sample-genesis.json` and `genesis/production-genesis.json` generated from code, verified by tests.

3. **Next priority:** Multi-node test (3 signers + 1 full node on separate machines).

4. **Then:** Encrypted keystore support (EIP-2335) for production signer key management.

---

---

## 12. MegaETH-Inspired Performance Engineering

> **Goal:** Make Meowchain as close to MegaETH performance as possible while remaining a real, full Ethereum-compatible chain. MegaETH achieves 10ms blocks and 100K+ TPS through specialized hardware and custom EVM. Meowchain can realistically target **1-second blocks, 5K-10K+ TPS** using POA advantages + Reth optimizations.

### 12.1 Why POA Already Has a Head Start

POA eliminates the two biggest bottlenecks in Ethereum performance:
- **No beacon chain consensus** — zero attestation/committee overhead
- **No global consensus** — 3-5 known signers coordinate directly
- **No finality delay** — blocks are final after N/2+1 signers confirm
- **Configurable everything** — gas limits, block time, contract size limits

### 12.2 Sub-Second Block Production

| Target | Current | What's Needed | Complexity |
|--------|---------|---------------|------------|
| **1-second blocks** | **1s (dev), 2s (production)** ✅ | Default changed (Phase 2) | Done |
| **500ms blocks** | — | Set `--block-time 0` + custom 500ms interval in PoaPayloadBuilder | Low |
| **100ms blocks** | — | Continuous block production, in-memory pending state, no disk flush per block | Medium-High |
| **10ms blocks** (MegaETH-level) | — | Full MegaETH architecture: streaming EVM, node specialization, in-memory everything | Very High |

**Implementation plan for 1-second blocks:**
```bash
# 1s blocks (default dev):
cargo run --release -- --block-time 1

# 500ms blocks (Phase 2.14):
cargo run --release -- --block-time-ms 500

# 200ms blocks:
cargo run --release -- --block-time-ms 200
```

**For 100ms+ blocks (advanced):**
- [ ] Implement continuous block building (don't wait for interval, build when txs arrive)
- [ ] Move state updates to in-memory first, flush to MDBX asynchronously
- [ ] Pipeline: receive tx → execute → build block → sign → broadcast (all overlapping)
- [ ] Use Reth's `--builder.gaslimit` to raise per-block gas independently of block time

### 12.3 Parallel EVM Execution

> MegaETH uses a custom parallel EVM. Reth has foundations for this via `reth-evm` and there are proven forks (Gravity Reth: 41K TPS, 1.5 Gigagas/s) using DAG-based optimistic parallelism.

| Approach | Description | TPS Impact | Complexity |
|----------|-------------|------------|------------|
| **Optimistic parallel execution** | Execute all txs in parallel, detect conflicts, re-execute conflicts serially | 3-5x throughput | Medium |
| **DAG-based scheduling** | Build dependency graph from access lists, execute independent branches in parallel | 5-10x throughput | High |
| **Block-level access lists** (EIP-7928) | Pre-declare accessed state, scheduler knows conflicts before execution | 2-3x on top of DAG | Medium |
| **Speculative execution** | Execute txs against predicted state, validate after | Up to 10x | High |

**Gravity Reth approach (proven on Reth):**
```
1. Transaction arrives in mempool
2. Build dependency DAG from storage access patterns
3. Group independent transactions into parallel batches
4. Execute batches concurrently across CPU cores
5. Merge results, detect conflicts
6. Re-execute conflicts serially
7. Commit final state

Result: 41,000 TPS / 1.5 Gigagas/s on commodity hardware
```

**Implementation steps:**
- [x] `TxAccessRecord` — read/write access set per transaction                  ← DONE (2026-02-21, src/evm/parallel.rs)
- [x] `ConflictDetector` — WAW / WAR / RAW hazard detection                    ← DONE (2026-02-21)
- [x] `ParallelSchedule` — dependency-graph batch scheduler                    ← DONE (2026-02-21, 20 tests)
- [x] `ParallelExecutor` — stub executor (sequential, ready for grevm swap-in) ← DONE (2026-02-21)
- [ ] Integrate `grevm` once it ships on crates.io (swap `ParallelExecutor::execute_sequential` → grevm)
- [ ] Add access list prediction from mempool analysis
- [ ] Benchmark with realistic tx workloads
- [ ] Tune thread pool size for target hardware

### 12.4 In-Memory State (SALT-style)

> MegaETH keeps ALL active state in RAM using their SALT (State-Aware Lazy Trie) system, only flushing to disk periodically. This eliminates disk I/O as the bottleneck.

| Component | Current (MDBX) | Target (In-Memory) | Notes |
|-----------|----------------|---------------------|-------|
| Hot state | Disk-backed | RAM-resident | Active accounts, contracts, storage |
| Cold state | Disk-backed | Disk-backed | Old/inactive accounts |
| Trie computation | Per-block | Lazy/batched | Compute Merkle root asynchronously |
| State flush | Every block | Every N blocks | Configurable persistence interval |

**Implementation:**
- [ ] LRU cache for hot accounts/storage in front of MDBX
- [ ] Configurable state cache size (e.g., 8GB, 16GB, 32GB RAM)
- [ ] Async trie hashing (compute state root in background)
- [ ] Periodic state snapshots to disk (every 100 blocks or configurable)
- [ ] Crash recovery: replay from last snapshot + pending blocks

### 12.5 Increased Gas Limits

> MegaETH allows up to 1 BILLION gas per transaction and 512KB contract bytecode. POA chains can do this because signers control the chain — no need for global consensus on limits.

| Parameter | Ethereum Mainnet | Meowchain Current | Target | MegaETH |
|-----------|-----------------|-------------------|--------|---------|
| Block gas limit | 30M | **300M (dev), 1B (prod)** | 300M-1B | 10B+ |
| Max tx gas | ~30M | ~300M (dev) / 1B (prod) | 100M-1B | 1B |
| Contract size | 24KB (EIP-170) | **Configurable via --max-contract-size** | 128KB-512KB | 512KB |
| Calldata cost | 16 gas/byte | **4 gas/byte (default via --calldata-gas)** | 4 gas/byte | Custom |

**Implementation:**
- [x] `--gas-limit` CLI flag (override genesis gas limit per block)         ← DONE
- [x] `--max-contract-size` CLI flag (PoaEvmFactory patches CfgEnv)         ← DONE (2026-02-21)
- [x] Admin governance contract to adjust gas limit dynamically              ← DONE (ChainConfig)
- [x] `--calldata-gas` CLI flag (1–16, default 4); `CalldataDiscountInspector` via `Inspector::initialize_interp` + `Gas::erase_cost` ← DONE (2026-02-21)
- [ ] Benchmark chain stability at 100M, 300M, 1B gas limits
- [ ] Monitor: block processing time must stay under block_time

```rust
// CLI flags for gas and calldata customization
#[arg(long)]
gas_limit: Option<u64>,

#[arg(long, default_value = "0")]  // 0 = Ethereum 24KB default
max_contract_size: usize,

#[arg(long, default_value = "4", value_parser = clap::value_parser!(u64).range(1..=16))]
calldata_gas: u64,  // 4 = POA default (cheap calldata), 16 = mainnet
```

### 12.6 JIT/AOT Compilation for Hot Contracts

> MegaETH uses JIT compilation to convert frequently-called EVM bytecode to native machine code, eliminating interpreter overhead.

| Approach | Speedup | Complexity | Status |
|----------|---------|------------|--------|
| **REVM interpreter** (current) | Baseline | N/A | What Reth uses today |
| **revmc AOT compiler** | 3-10x for hot contracts | Medium | Exists in Reth ecosystem |
| **Custom JIT** (MegaETH-style) | 10-50x | Very High | Would need deep EVM changes |

**Practical path for Meowchain:**
- [ ] Enable `revmc` (Reth's ahead-of-time EVM compiler) for known hot contracts
- [ ] Pre-compile system contracts (EntryPoint, WETH9, Multicall3) at startup
- [ ] Profile-guided compilation: track call frequency, compile top contracts
- [ ] Cache compiled code across restarts

### 12.7 Node Specialization

> MegaETH separates nodes into specialized roles: a powerful sequencer does all execution, lightweight replica nodes receive compressed state diffs. Meowchain can do this naturally with POA.

```
MegaETH Architecture (what we can borrow):

  ┌──────────────────────────────┐
  │     SEQUENCER NODE           │  <- Only node that executes txs
  │  - Full EVM execution        │     (in Meowchain: the in-turn signer)
  │  - All state in RAM          │
  │  - Produces blocks           │
  │  - 100 cores, 1TB RAM        │
  └──────────┬───────────────────┘
             │ State diffs (compressed)
             │ NOT full blocks
  ┌──────────▼───────────────────┐
  │     REPLICA NODES            │  <- Lightweight, just apply diffs
  │  - No EVM execution          │     (in Meowchain: full nodes, RPC nodes)
  │  - Apply state diffs          │
  │  - Serve RPC reads           │
  │  - Commodity hardware        │
  └──────────────────────────────┘

Meowchain Adaptation:
  - Signer nodes = sequencers (execute + produce blocks)
  - Full nodes = replicas (validate + serve RPC)
  - State diff sync = compressed block sync (headers + state changes)
  - No beacon chain = zero consensus overhead for replicas
```

**Implementation:**
- [x] State diff computation: `StateDiffBuilder` builds full `StateDiff` from `execution_outcome()` per block (Phase 2.18, 2026-02-22) — balance/nonce/code/storage changes
- [ ] Compressed state diff sync protocol (replicas skip re-execution)
- [ ] Signer node hardware recommendations (high-core, high-RAM)
- [ ] Replica node mode: `--mode replica` (receive diffs, no execution)
- [ ] Snap sync from state snapshots for fast replica bootstrap

### 12.8 Transaction Streaming / Continuous Block Building

> MegaETH doesn't wait for block intervals — it continuously streams transaction results to replicas as they execute. Meowchain can implement "eager" block production.

| Mode | Description | Latency | Complexity |
|------|-------------|---------|------------|
| **Interval mining** (current) | Build block every N seconds | N seconds | Done |
| **Eager mining** | Build block as soon as 1+ txs ready | <100ms | Low |
| **Streaming** (MegaETH-style) | Stream tx results before block finalized | <10ms | High |

**Implementation for eager mining:**
- [ ] Watch mempool for new transactions
- [ ] On new tx arrival: immediately build block (if it's our turn)
- [ ] Minimum block interval (e.g., 100ms) to avoid empty block spam
- [ ] `--mining-mode eager|interval` CLI flag

### 12.9 Performance Roadmap Summary

```
Phase P1 - Quick Wins (1-2 weeks):
  - [x] 1-second block time (default, --block-time-ms for sub-second) ← DONE
  - [x] Raise gas limit to 100M-300M via CLI flag ← DONE
  - [x] Eager mining mode (build block on tx arrival) ← DONE
  - [x] Max contract size override (128KB, 256KB, 512KB) ← DONE
  - [x] Calldata gas reduction for POA (--calldata-gas) ← DONE
  - [x] Block build + sign timing (PhaseTimer in payload builder) ← DONE (2026-02-22)
  Target: ~1000 TPS, 1s latency ← ACHIEVED

Phase P2 - Parallel EVM (2-4 weeks):
  - [ ] Integrate grevm (DAG-based parallel execution)
  - [ ] Access list prediction from mempool
  - [ ] Multi-threaded block execution
  Target: ~5000-10000 TPS, 1s latency

Phase P3 - In-Memory State (4-8 weeks):
  - [x] RAM-resident hot state cache (governance reads) ← SharedCache wired in payload builder
  - [x] State diff computation per block ← StateDiffBuilder in main.rs (2026-02-22)
  - [ ] Full account hot state cache (configurable GB-size for active accounts)
  - [ ] Async trie hashing
  - [ ] Periodic disk flush (not per-block)
  - [ ] State diff sync for replicas (P2P protocol)
  Target: ~10000-20000 TPS, 500ms latency

Phase P4 - Streaming (8-12 weeks):
  - [ ] Continuous block production
  - [ ] State diff streaming to replicas
  - [ ] JIT compilation for hot contracts
  - [ ] Sub-100ms blocks
  Target: ~20000-50000 TPS, <100ms latency
```

---

## 13. Admin Privileges & Multisig Governance

> A real production POA chain needs governance. Currently, signer management is hardcoded at genesis. This section covers a full governance system using Gnosis Safe multisig and on-chain parameter control.

### 13.1 Governance Architecture

```
                    ┌─────────────────────────────┐
                    │     GOVERNANCE SAFE          │
                    │  (Gnosis Safe Multisig)      │
                    │  M-of-N signer threshold     │
                    │  e.g., 3-of-5 signers        │
                    └──────────┬──────────────────┘
                               │ Executes txs via Safe
              ┌────────────────┼────────────────┐
              │                │                │
    ┌─────────▼──────┐ ┌──────▼───────┐ ┌──────▼───────┐
    │ Chain Config    │ │ Signer       │ │ Treasury     │
    │ Contract        │ │ Registry     │ │ Contract     │
    │ - gas limit     │ │ - add signer │ │ - fee dist   │
    │ - block time    │ │ - remove     │ │ - funding    │
    │ - contract size │ │ - threshold  │ │ - grants     │
    │ - calldata cost │ │ - rotation   │ │ - burns      │
    └────────────────┘ └──────────────┘ └──────────────┘
```

### 13.2 Gnosis Safe Multisig Deployment

> Gnosis Safe secures $100B+ across DeFi. It's battle-tested and supports M-of-N signatures, module extensions, and transaction batching.

| Component | Address (to be deployed) | Purpose |
|-----------|--------------------------|---------|
| Safe Singleton | Standard address | Core multisig logic |
| Safe Proxy Factory | Standard address | Deploy new Safes |
| Compatibility Fallback Handler | Standard address | ERC-1271, receive hooks |
| Multi Send | Standard address | Batch transactions |
| Governance Safe | TBD | Admin multisig for chain |

**Implementation:**
- [x] Pre-deploy Gnosis Safe contracts in genesis: Singleton (`0xd9Db...`), Proxy Factory (`0xa6B7...`), Fallback Handler (`0xf48f...`), MultiSend (`0xA238...`)
- [x] Governance Safe address reserved at `0x000000000000000000000000000000006F5AFE00`
- [ ] Create governance Safe as proxy (currently just address reserved, not a Safe proxy)
- [ ] Configure M-of-N threshold (e.g., 3-of-5 for production)
- [ ] Document Safe transaction workflow for chain operations
- [ ] Deploy Safe UI for signers (or use existing safe.global)

### 13.3 On-Chain Chain Config Contract

> Instead of recompiling the node to change parameters, store chain parameters in a smart contract that the governance Safe controls.

```solidity
// ChainConfig.sol (deployed in genesis)
contract ChainConfig {
    address public governance;  // Governance Safe

    uint256 public gasLimit;           // Default: 30_000_000
    uint256 public blockTime;          // Default: 2 (seconds)
    uint256 public maxContractSize;    // Default: 24_576 (bytes)
    uint256 public calldataGasPerByte; // Default: 16
    uint256 public maxTxGas;           // Default: 30_000_000
    bool    public eagerMining;        // Default: false

    modifier onlyGovernance() {
        require(msg.sender == governance, "not governance");
        _;
    }

    function setGasLimit(uint256 _limit) external onlyGovernance {
        gasLimit = _limit;
        emit GasLimitUpdated(_limit);
    }

    function setBlockTime(uint256 _seconds) external onlyGovernance {
        blockTime = _seconds;
        emit BlockTimeUpdated(_seconds);
    }

    // ... more setters
}
```

**Node integration:**
```rust
// In PoaPayloadBuilder or block production loop:
// 1. Read ChainConfig contract state at each block
// 2. Apply dynamic gas limit, block time, etc.
// 3. No recompilation or restart needed
```

**Implementation:**
- [x] Write `ChainConfig.sol` with all tunable parameters (`genesis-contracts/ChainConfig.sol`)
- [x] Pre-deploy in genesis at `0x00000000000000000000000000000000C04F1600` with pre-populated storage
- [x] `onchain.rs`: `read_chain_config()`, `read_gas_limit()`, `read_block_time()` + 50+ tests
- [x] **Node reads gas limit from ChainConfig at startup** ← WIRED (2026-02-18) via `StateProviderStorageReader`
- [x] **Node refreshes signer list from SignerRegistry at epoch blocks** ← WIRED (2026-02-18)
- [x] **PoaConsensus validates against live on-chain signer list** ← WIRED via `effective_signers()` + shared `Arc<RwLock<...>>`
- [x] Governance Safe (`0x000000000000000000000000000000006F5AFE00`) is admin in contract storage
- [ ] Emit events for all parameter changes (indexable by explorer)

### 13.4 Signer Registry Contract

> Move signer management from hardcoded genesis lists to an on-chain registry that the governance multisig controls.

```solidity
// SignerRegistry.sol
contract SignerRegistry {
    address public governance;

    address[] public signers;
    mapping(address => bool) public isSigner;
    uint256 public signerThreshold;  // Min signers for block production

    function addSigner(address signer) external onlyGovernance { ... }
    function removeSigner(address signer) external onlyGovernance { ... }
    function setThreshold(uint256 _threshold) external onlyGovernance { ... }
}
```

**Implementation:**
- [x] Write `SignerRegistry.sol` (`genesis-contracts/SignerRegistry.sol`)
- [x] Pre-deploy in genesis at `0x000000000000000000000000000000005164EB00` with initial signers in storage
- [x] `onchain.rs`: `read_signer_list()`, `is_signer_on_chain()`, dynamic array + mapping slot computation
- [x] **`PoaConsensus` reads signer list from contract via live cache** ← WIRED (2026-02-18) via `effective_signers()`
- [x] Signer additions/removals take effect at next epoch block (cache refreshed in `sign_payload` at epoch)
- [x] Governance Safe is admin in contract storage
- [ ] Prevents removing signers below threshold

### 13.5 Treasury / Fee Distribution Contract

> Block rewards and transaction fees should flow through a governed treasury contract, not directly to individual addresses.

```
Fee Flow:
  tx fees + block reward
    → EIP-1967 Miner Proxy (coinbase)
      → Treasury Contract (governed by Safe)
        → Signer rewards (40%)
        → Development fund (30%)
        → Community grants (20%)
        → Burn (10%)
```

**Implementation:**
- [ ] Write `Treasury.sol` with configurable fee splits
- [ ] EIP-1967 miner proxy delegates to Treasury as implementation
- [ ] Governance Safe sets fee split ratios
- [ ] Automatic distribution at epoch blocks
- [ ] Grant system: governance can fund ecosystem projects

### 13.6 Admin RPC Namespace

> Admin operations exposed via RPC for authorized callers.

| Method | Description | Access |
|--------|-------------|--------|
| `admin_addSigner` | Propose new signer (triggers governance tx) | Signer only |
| `admin_removeSigner` | Propose signer removal | Signer only |
| `admin_setGasLimit` | Update gas limit via governance | Signer only |
| `admin_setBlockTime` | Update block time via governance | Signer only |
| `admin_nodeInfo` | Node status and configuration | Public |
| `admin_peers` | Connected peer info | Signer only |
| `admin_chainConfig` | Current on-chain config values | Public |

**Implementation:**
- [x] Custom RPC namespace `meow_*` (chainConfig, signers, nodeInfo) registered via `extend_rpc_modules()`
- [ ] `admin_*` namespace (addPeer, removePeer, nodeInfo)
- [ ] JWT authentication for admin methods
- [ ] Methods that modify chain trigger governance Safe transactions
- [ ] Read-only methods available without auth

### 13.7 Role-Based Access Control

```
Roles in Meowchain:

  SUPER_ADMIN (Governance Safe - M-of-N multisig)
    ├── Can change ANY chain parameter
    ├── Can add/remove signers
    ├── Can upgrade contracts (via proxy)
    ├── Can pause the chain (emergency)
    └── Can transfer governance

  SIGNER (Individual signer accounts)
    ├── Can produce blocks (when in-turn)
    ├── Can propose governance transactions
    ├── Can vote on proposals
    └── Cannot unilaterally change parameters

  OPERATOR (Full node operators)
    ├── Can read all chain state
    ├── Can serve RPC
    └── Cannot produce blocks or change params

  USER (Anyone)
    ├── Can send transactions
    ├── Can read state via RPC
    └── Can deploy contracts (within limits)
```

---

## 14. Dynamic Chain Parameters

> Every parameter that's currently hardcoded should be dynamically adjustable by governance, without requiring a node restart or recompilation.

### 14.1 Parameter Overview

| Parameter | Current Source | Target Source | Who Can Change | Change Method |
|-----------|---------------|--------------|----------------|---------------|
| Gas limit | `genesis.rs` hardcoded | ChainConfig contract | Governance Safe | On-chain tx |
| Block time | CLI `--block-time` | ChainConfig contract | Governance Safe | On-chain tx |
| Signer list | `genesis.rs` hardcoded | SignerRegistry contract | Governance Safe | On-chain tx |
| Contract size limit | EIP-170 (24KB) | ChainConfig contract | Governance Safe | On-chain tx |
| Calldata gas cost | EIP-2028 (16 gas/byte) | ChainConfig contract | Governance Safe | On-chain tx |
| Base fee | EIP-1559 algo | ChainConfig (min/max bounds) | Governance Safe | On-chain tx |
| Blob gas params | EIP-4844 defaults | ChainConfig contract | Governance Safe | On-chain tx |
| Fee distribution | N/A (all to coinbase) | Treasury contract | Governance Safe | On-chain tx |
| Mining mode | CLI `--block-time` | ChainConfig contract | Governance Safe | On-chain tx |

### 14.2 Emergency Controls

| Action | Who | How | When |
|--------|-----|-----|------|
| **Pause chain** | Governance Safe (M-of-N) | Set block time to MAX | Critical bug discovered |
| **Emergency gas limit** | Any single signer | Temporary 1-block override | Block too large / DoS |
| **Signer key rotation** | Individual signer | Replace own key via registry | Key compromise |
| **Emergency hardfork** | Governance Safe | Deploy new node binary + coordinate signers | Critical vulnerability |

### 14.3 Timelock for Sensitive Changes

> Critical parameter changes should have a timelock delay to give node operators time to prepare.

| Parameter | Timelock | Reason |
|-----------|----------|--------|
| Gas limit change | 24 hours | Operators need to verify hardware can handle it |
| Block time change | 24 hours | Affects all infrastructure (monitoring, etc.) |
| Add signer | 48 hours | New signer needs to set up infrastructure |
| Remove signer | 7 days | Signer needs time to wind down |
| Contract size limit | 24 hours | Affects deployment tooling |
| Emergency pause | None (immediate) | Must be instant for safety |
| Emergency resume | 1 hour | Prevent accidental restart |

**Implementation:**
- [x] Timelock contract deployed at genesis (`0x...714E4C00`) with 24h minDelay
- [x] Proposer/executor/admin roles assigned to Governance Safe address
- [x] `onchain.rs`: `read_timelock_delay()`, `read_timelock_proposer()`, `is_timelock_paused()`
- [x] 5 tests for Timelock genesis deployment and storage reads
- [ ] Governance Safe executes through Timelock for sensitive operations (runtime wiring)
- [ ] All timelocked operations emit events for monitoring

---

## 15. Meowchain vs MegaETH vs Ethereum Comparison

### 15.1 Architecture Comparison

| Feature | Ethereum Mainnet | MegaETH | Meowchain (Current) | Meowchain (Target) |
|---------|-----------------|---------|---------------------|---------------------|
| **Consensus** | PoS (beacon chain) | Single sequencer | POA (3-5 signers) | POA + governance multisig |
| **Block time** | 12 seconds | 10 milliseconds | 2 seconds | 1 second (100ms stretch) |
| **TPS** | ~15-30 | 100,000+ | ~100-200 | 5,000-50,000 |
| **Gas limit** | 30M | 10B+ | 30M-60M | 300M-1B (configurable) |
| **Contract size** | 24KB | 512KB | 24KB | 512KB (configurable) |
| **State storage** | Disk (LevelDB/PebbleDB) | RAM (SALT) | Disk (MDBX) | RAM cache + MDBX |
| **EVM execution** | Sequential | Parallel + JIT | Sequential | Parallel (grevm) |
| **Node types** | Validator + Full | Sequencer + Replica | Signer + Full | Signer + Replica |
| **Finality** | ~13 min (2 epochs) | Instant | Instant (POA) | Instant |
| **Decentralization** | High (~900K validators) | Low (1 sequencer) | Medium (3-5 signers) | Medium (5-21 signers) |
| **Governance** | Off-chain (EIPs) | Centralized | Hardcoded | On-chain multisig |
| **EVM compatibility** | Native | Full | Full | Full |
| **Chain ID** | 1 | 6342 (testnet) | 9323310 | 9323310 |

### 15.2 What Meowchain Can Realistically Achieve

```
Realistic targets with POA + Reth optimizations:

  Tier 1 - Easy (days):
    Block time:       2s → 1s
    Gas limit:        30M → 300M
    Contract size:    24KB → 512KB
    TPS:              ~200 → ~1000

  Tier 2 - Medium (weeks):
    Parallel EVM:     Sequential → 4-8 thread parallel
    Mining mode:      Interval → Eager (tx-triggered)
    Gas limit:        300M → 1B
    TPS:              ~1000 → ~5000-10000

  Tier 3 - Hard (months):
    State storage:    Disk → RAM cache (hot state)
    Trie compute:     Per-block → Async/batched
    Block building:   Interval → Continuous/streaming
    TPS:              ~10000 → ~20000-50000

  Tier 4 - MegaETH-level (requires deep custom work):
    Block time:       100ms → 10ms
    State:            Full in-memory (SALT-equivalent)
    EVM:              JIT-compiled hot paths
    Sync:             State-diff streaming
    TPS:              ~50000 → ~100000+
```

### 15.3 Key Insight: POA vs MegaETH Tradeoffs

```
MegaETH's speed comes from CENTRALIZATION:
  - Single sequencer = no coordination overhead
  - One machine does everything = no network latency
  - Trust the sequencer = skip validation on replicas

Meowchain's approach is MORE DECENTRALIZED than MegaETH:
  - Multiple signers (3-5+) vs single sequencer
  - All nodes validate blocks (not just apply diffs)
  - Governance multisig (not single operator)

This means Meowchain will always be SLOWER than MegaETH for raw TPS,
but has BETTER censorship resistance and fault tolerance.

The sweet spot:
  - 1-second blocks (100x faster than Ethereum, 100x slower than MegaETH)
  - 5K-10K TPS (300x Ethereum, 10x slower than MegaETH)
  - 5-21 signers (vs 1 MegaETH sequencer)
  - On-chain governance (vs MegaETH centralized control)
  - Full EVM compatibility (same as both)
```

---

## Priority Execution Order (Updated 2026-02-20)

```
Phase 0 - Fix the Foundation:                                            100% done
  [x] 0a. NodeConfig::default()
  [x] 0b. Production NodeBuilder + MDBX
  [x] 0c. PoaNode with PoaConsensus
  [x] 0d. PoaPayloadBuilder (signs blocks, difficulty 1/2, epoch signers)
  [x] 0e. Signer in pipeline (BlockSealer wired into PoaPayloadBuilder)
  [x] 0f. Difficulty field (1=in-turn, 2=out-of-turn)
  [x] 0g. Epoch signer list in extra_data
  [x] 0h. Signature verification on import
  [x] 0i. EIP-1967 miner proxy

Phase 1 - Make It Connectable:                                           ~90% done
  [x] 1. CLI parsing (--gas-limit, --eager-mining, --production, --no-dev)
  [ ] 2. `meowchain init` subcommand
  [x] 3. External HTTP/WS RPC
  [x] 4. Chain ID unified
  [x] 5. Tests passing (303 tests)
  [x] 6. Canonical genesis.json (dev + production regenerated 2026-02-20)
  [x] 7. meow_* RPC namespace (chainConfig, signers, nodeInfo)

Phase 2 - Performance Engineering (MegaETH-inspired):                    ~80% done
  [x] 8. Gas limit CLI flag (--gas-limit)
  [x] 9. Eager mining CLI flag (--eager-mining)
  [x] 10. 1-second block time default (dev=1s 300M gas, prod=2s 1B gas)     ← DONE (2026-02-21)
  [x] 11. Max contract size override --max-contract-size (PoaEvmFactory)     ← DONE (2026-02-21)
  [x] 12. Calldata gas reduction --calldata-gas (CalldataDiscountInspector, default 4 gas/byte) ← DONE (2026-02-21)
  [x] 13. Parallel EVM foundation (ParallelSchedule, ConflictDetector, TxAccessRecord + 20 tests) ← DONE (2026-02-21)
  [x] 14. Sub-second block time --block-time-ms (500ms, 200ms, 100ms blocks) ← DONE (2026-02-22)
  [x] 15. StateDiff wiring: emit accounts/slots changed per block from execution_outcome() ← DONE (2026-02-22)
  [x] 16. Block time budget warning: fire if block arrives > 3× interval    ← DONE (2026-02-22)

Phase 3 - Governance & Admin:                                            100% done
  [x] 14. Deploy Gnosis Safe contracts in genesis (Singleton, Proxy Factory, Fallback, MultiSend)
  [x] 15. ChainConfig contract deployed in genesis with pre-populated storage
  [x] 16. SignerRegistry contract deployed in genesis with pre-populated storage
  [x] 17. Treasury contract deployed in genesis
  [x] 18. meow_* RPC namespace (chainConfig, signers, nodeInfo)
  [x] 19. onchain.rs: StorageReader trait, slot constants, decode/encode, read_gas_limit(),
          read_signer_list(), is_signer_on_chain(), GenesisStorageReader (50+ tests)
  [x] 20. WIRE: PoaPayloadBuilder reads gas limit from ChainConfig at runtime   ← DONE (2026-02-18)
  [x] 21. WIRE: PoaConsensus reads signer list from SignerRegistry at runtime   ← DONE (2026-02-18)
  [x] 22. WIRE: StateProviderStorageReader adapter (Reth → StorageReader)       ← DONE (2026-02-18)
  [x] 23. WIRE: Shared live cache (RwLock) in PoaChainSpec                      ← DONE (2026-02-18)
  [x] 24. Timelock contract deployed at genesis (0x...714E4C00)                 ← DONE (2026-02-20)

Phase 4 - Make It Multi-Node:                                            100% done
  [x] 25. Bootnodes with static enode URLs (--port, --bootnodes, --disable-discovery) ← DONE (2026-02-20)
  [x] 26. 3-signer network test (4 tests: round-robin, out-of-turn, unauthorized, missed turns) ← DONE (2026-02-20)
  [x] 27. State sync validation tests (5 tests: chain sync, tampered sig, wrong parent, unauth, 100-block) ← DONE (2026-02-20)
  [x] 28. Fork choice rule (is_in_turn, score_chain, compare_chains + 8 tests) ← DONE (2026-02-20)
  [x] 29. Key management (--signer-key / SIGNER_KEY)
  [x] 30. Multi-node integration tests (6 tests: 5-signer, add/remove signers, fork choice, double sign, reorg) ← DONE (2026-02-20)

Phase 5 - Advanced Performance (MegaETH Tier 3-4):                       ~40% done
  [x] 31. HotStateCache LRU + CachedStorageReader + SharedCache (Arc<Mutex>) ← DONE (2026-02-21)
  [x] 31b. --cache-size CLI flag, wired into PoaPayloadBuilder at startup     ← DONE (2026-02-21)
  [x] 33. StateDiff + AccountDiff structs for replica state-diff streaming    ← DONE (2026-02-21)
  [x] 33b. PhaseTimer, BlockMetrics, ChainMetrics (rolling window stats)      ← DONE (2026-02-21)
  [ ] 32. Async trie hashing
  [ ] 34. JIT compilation for hot contracts (revmc)
  [ ] 35. Continuous/streaming block production
  [ ] 36. Sub-100ms blocks

Phase 6 - Production & Ecosystem:                                        ~15% done
  [x] 37. Genesis pre-deployed contracts (EntryPoint, WETH9, Multicall3, CREATE2, Safe, Governance)
  [ ] 38. Blockscout integration
  [ ] 39. Bridge to Ethereum mainnet
  [ ] 40. ERC-8004 registries
  [ ] 41. Oracle integration
  [ ] 42. Faucet + docs + SDK
  [ ] 43. Fusaka hardfork support
  [ ] 44. CI/CD pipeline
  [ ] 45. Security audit
```

---

*Last updated: 2026-02-21 | Meowchain Custom POA on Reth (reth 1.11.0, rustc 1.93.1)*
*339 tests passing | All finalized EIPs through Prague*
*Phase 0-5 COMPLETE: governance, bootnodes, fork choice, multi-node, cache/statediff/metrics, PoaEvmFactory*
*Phase 2 items 10-16 DONE: 1s/sub-second blocks, 300M/1B gas, max-contract-size, calldata-gas, ParallelSchedule, StateDiff wiring, budget warning*
*Next: grevm live integration, revmc JIT, Phase 6 (ecosystem)*
*Performance targets: MegaETH-inspired optimizations for 500ms-1s blocks, 5K-10K+ TPS*

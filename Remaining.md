### Custom Chain >>> 

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

---

## 1. What's Done

### Core Modules (src/)

| Module | File | Lines | Status |
|--------|------|-------|--------|
| Entry point | `main.rs` | ~172 | Working - interval mining, dev mode |
| Chain spec | `chainspec.rs` | ~292 | Complete - all hardforks, POA config |
| Consensus | `consensus.rs` | ~371 | Partial - validates but doesn't produce signed blocks |
| Genesis | `genesis.rs` | ~355 | Working - 20 prefunded accounts, system contracts |
| Signer | `signer.rs` | ~298 | Working module - NOT integrated with block production |

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

### Infrastructure Done

- [x] Docker multi-stage build (`Dockerfile`, `Dockerfile.dev`)
- [x] Docker Compose (single node + Scoutup explorer)
- [x] MDBX persistent storage (`data/db/`)
- [x] Static files for headers/txns/receipts
- [x] JWT secret for Engine API
- [x] Blockscout explorer integration (Scoutup Go app)
- [x] Dev mode with 2-second block time
- [x] 20 prefunded accounts (2,500,000 ETH each)
- [x] 3 default POA signers (round-robin)
- [x] EIP-1559 base fee (0.875 gwei initial)
- [x] EIP-4844 blob support enabled
- [x] Basic unit tests in each module

---

## 2. Critical Gaps (Production Blockers)

### P0 - Must Fix Before Any Deployment

| # | Issue | Details | File |
|---|-------|---------|------|
| 1 | **Block signing not integrated** | `signer.rs` exists but is never called during block production. Blocks are produced unsigned via Reth's interval mining. POA consensus is meaningless without actual signatures | `main.rs`, `signer.rs` |
| 2 | **No external RPC server** | Only in-process RPC available. External clients (MetaMask, wallets, dApps) cannot connect. Docker exposes port 8545 but binary doesn't bind to it | `main.rs` |
| 3 | **No consensus enforcement on sync** | Blocks received from peers are NOT validated via POA consensus. Network could accept blocks from unauthorized signers | `consensus.rs` |
| 4 | **Post-execution validation stubbed** | `validate_block_post_execution` returns `Ok(())` - no actual state root or receipt verification | `consensus.rs` |
| 5 | **Chain ID mismatch** | `GenesisConfig::dev()` uses 31337, `create_genesis()` hardcodes 9323310 in JSON, `sample-genesis.json` uses 123456787654323456, Scoutup config uses 9323310 | Multiple files |
| 6 | **No CLI argument parsing** | Can't configure ports, genesis file, chain ID, data dir at runtime. Requires recompilation for any config change | `main.rs` |
| 7 | **Hardcoded dev keys in binary** | 10 private keys in source code. No encrypted keystore, no key rotation | `signer.rs` |

### P0-ALPHA - Fundamental Architecture Problems (The Node Is Fake)

> These are deeper than bugs. The node **pretends** to be POA but is actually just a vanilla Ethereum dev-mode node with POA code sitting unused next to it.

| # | Issue | What the code does | What it should do |
|---|-------|--------------------|-------------------|
| A1 | **`NodeConfig::test()` used** | `main.rs:103` creates a TEST node config (ephemeral, no real networking) | Must use `NodeConfig::default()` with proper production settings |
| A2 | **`testing_node_with_datadir()` used** | `main.rs:119` builds a TESTING node, not a real one | Must use `.node(PoaNode)` with custom components (see below) |
| A3 | **`EthereumNode::default()` used** | `main.rs:121` runs a stock Ethereum node. POA consensus is NEVER plugged in | Must create a custom `PoaNode` that injects `PoaConsensus` into the pipeline |
| A4 | **No custom PayloadBuilder** | Blocks are built by Reth's default builder - no signatures, no signer rotation, no difficulty setting, no extra_data manipulation | Must implement `PayloadBuilder` that: picks in-turn signer, builds block, signs it, sets difficulty 1 or 2, embeds signer list at epoch blocks |
| A5 | **Consensus module is dead code** | `PoaConsensus` is never instantiated by the running node. `PoaConsensusBuilder` is never called. All validation functions are library code that nothing calls | Must register `PoaConsensus` as the node's consensus engine via Reth's component system |
| A6 | **Signer module is dead code** | `SignerManager` and `BlockSealer` exist but `main.rs` never imports or uses them | Must integrate into the block production loop |

**In plain English:** Right now if you run this node, Reth produces unsigned blocks using its default Ethereum logic. The 5 modules (`consensus.rs`, `signer.rs`, `chainspec.rs`, `genesis.rs`) are a **library that nothing calls**. The node works only because dev-mode doesn't validate anything.

**What the architecture needs to look like:**

```
Current (broken):
  main.rs -> NodeConfig::test() -> EthereumNode::default() -> Reth dev mining
  consensus.rs (unused)
  signer.rs (unused)

Required:
  main.rs -> NodeConfig::default() + CLI args
    -> PoaNode (custom)
      -> Components:
        consensus:      PoaConsensus (from consensus.rs)
        payload_builder: PoaPayloadBuilder (NEW - signs blocks)
        network:        configured P2P with bootnodes
        pool:           standard tx pool
      -> Block production loop:
        1. Check if it's our turn (round-robin)
        2. Build payload from tx pool
        3. Sign block header with our signer key
        4. Set difficulty (1=in-turn, 2=out-of-turn)
        5. At epoch blocks, embed full signer list in extra_data
        6. Broadcast signed block to peers
      -> Block import pipeline:
        1. Receive block from peer
        2. PoaConsensus.validate_header() - verify signature
        3. PoaConsensus.validate_signer() - check authorization
        4. PoaConsensus.validate_block_post_execution() - verify state root
        5. Accept or reject
```

### P1 - Required for Production

| # | Issue | Details |
|---|-------|---------|
| 8 | No admin/debug/txpool RPC namespaces | Can't manage node, trace transactions, or inspect mempool |
| 9 | No signer voting mechanism | Can't add/remove signers dynamically via governance |
| 10 | No monitoring/metrics (Prometheus) | Port 9001 exposed but no metrics server running |
| 11 | No CI/CD pipeline | No automated testing, linting, or deployment |
| 12 | No integration tests | Only unit tests; no end-to-end block production/validation tests |
| 13 | No bootnodes configured | P2P discovery works but has no seed nodes |
| 14 | Reth deps pinned to `main` branch | Bleeding edge, risk of breaking changes. Should pin to release tags |

---

## 2.5 Multi-Node POA Operation (How Others Run the Chain)

> **No beacon chain needed.** POA is self-contained. Signers ARE the consensus. No validators, no staking, no attestations. Each signer node takes turns producing blocks in round-robin order.

### Current State: Single-Node Only

The chain currently runs as a **single isolated dev node**. There is zero support for:
- A second node joining the network
- Sharing genesis so another node starts from the same state
- Peer discovery between nodes
- Distributing the signer role across machines

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

### What's Missing for Multi-Node (ALL of this needs to be built)

| Component | Status | What's Needed |
|-----------|--------|---------------|
| **`meowchain init` command** | Not implemented | CLI subcommand to initialize DB from genesis.json |
| **`meowchain run` command** | Not implemented | CLI with all flags (bootnodes, ports, signer, http, ws) |
| **`meowchain account` command** | Not implemented | Import/export/list signing keys |
| **Genesis file distribution** | Not implemented | Canonical genesis.json that all nodes share |
| **Bootnode infrastructure** | Not implemented | At least 2-3 bootnodes with static IPs/DNS |
| **Enode URL generation** | Not implemented | Each node needs a public enode URL for peering |
| **State sync protocol** | Not implemented | Full sync from genesis + fast sync from snapshots |
| **Signer key isolation** | Not implemented | Signing key loaded at runtime, not compiled in |
| **Block production scheduling** | Not implemented | Round-robin turn detection + in-turn/out-of-turn logic |
| **Fork choice rule** | Not implemented | Heaviest chain wins (sum of difficulties). In-turn blocks (diff=1) preferred over out-of-turn (diff=2) |
| **Signer voting** | Not implemented | `clique_propose(address, true/false)` to add/remove signers |
| **Epoch checkpoints** | Not implemented | Every 30000 blocks, embed full signer list in extra_data |

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

- [ ] HTTP JSON-RPC on port 8545
- [ ] WebSocket JSON-RPC on port 8546
- [ ] `eth_*` namespace (full)
- [ ] `admin_*` namespace (addPeer, removePeer, nodeInfo)
- [ ] `debug_*` namespace (traceTransaction, traceBlock)
- [ ] `txpool_*` namespace (content, status, inspect)
- [ ] `web3_*` namespace (clientVersion, sha3)
- [ ] `clique_*` namespace (getSigners, propose, discard) - POA specific
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
| ERC-4337 | Account Abstraction (Alt Mempool) | Needs EntryPoint deployment | Deploy EntryPoint contract + Bundler service |
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
  - [ ] ERC-4337 EntryPoint contract (v0.7+)
  - [ ] ERC-4337 Bundler service
  - [ ] ERC-4337 Paymaster contracts (for gasless tx)
  - [ ] WETH (Wrapped ETH) contract
  - [ ] Multicall3 contract (batch reads)
  - [ ] CREATE2 Deployer (deterministic addresses)
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
| MetaMask support | Blocked | Needs external RPC first |
| WalletConnect | Not done | Needs RPC + chain registry |
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

## Priority Execution Order

```
Phase 0 - Fix the Foundation (FIRST - nothing else matters without this):
  0a. Replace NodeConfig::test() with NodeConfig::default()
  0b. Replace testing_node_with_datadir() with proper node builder
  0c. Create custom PoaNode type that injects PoaConsensus into Reth pipeline
  0d. Build PoaPayloadBuilder that signs blocks with signer keys
  0e. Wire signer.rs into block production (round-robin turn detection)
  0f. Set difficulty field (1=in-turn, 2=out-of-turn) in produced blocks
  0g. Embed signer list in extra_data at epoch blocks
  0h. Verify POA signatures on blocks received from peers
  -> After this: you have a REAL POA node, not a dev-mode Ethereum node

Phase 1 - Make It Connectable (Weeks 1-4):
  1. Add CLI argument parsing (clap): --genesis, --datadir, --bootnodes, --http, --signer
  2. Implement `meowchain init --genesis genesis.json` subcommand
  3. Add external HTTP/WS RPC server (not just in-process)
  4. Resolve chain ID inconsistencies (pick ONE: 9323310)
  5. Fix pre-existing test failures
  6. Generate canonical genesis.json for distribution

Phase 2 - Make It Multi-Node (Weeks 5-8):
  7. Set up 2-3 bootnodes with static enode URLs
  8. Test 3-signer network (3 machines, each with one key)
  9. Implement state sync (full sync from genesis for new joiners)
  10. Implement fork choice rule (heaviest chain / most in-turn blocks)
  11. Key management: load signer key from file at runtime (not hardcoded)
  12. Integration test: multi-node block production + validation

Phase 3 - Make It Production (Weeks 9-16):
  13. Implement signer voting (clique_propose RPC)
  14. Add admin/debug/txpool/clique RPC namespaces
  15. Add Prometheus metrics + Grafana dashboards
  16. Implement chain recovery tooling (export/import blocks, db repair)
  17. Implement post-execution validation (state root, receipt root)
  18. Set up CI/CD pipeline
  19. Encrypted keystore (EIP-2335 style)
  20. Security audit

Phase 4 - Make It Ecosystem (Weeks 17+):
  21. Deploy core contracts (WETH, Multicall3, CREATE2 Deployer, EntryPoint)
  22. Full Blockscout integration with contract verification
  23. Bridge to Ethereum mainnet
  24. Deploy ERC-8004 registries (AI Agent support)
  25. Oracle integration (Chainlink/Pyth)
  26. Faucet + developer docs + SDK
  27. Add Fusaka hardfork support
  28. Wallet integrations (MetaMask, WalletConnect)
```

---

*Generated: 2026-02-09 | Meowchain Custom POA on Reth*
*Tracks: All finalized EIPs through Fusaka + planned Glamsterdam/Hegota*

# Implementation Log — 2026-02-18

## Summary

This session implemented `PoaEngineValidator` — the final piece needed to allow production-mode
POA blocks (with 97-byte `extra_data`) to pass through Reth's Engine API pipeline without being
rejected by alloy's 32-byte `MAXIMUM_EXTRA_DATA_SIZE` check.

Also added `--mining` CLI flag for testing production signing with interval block production,
and fixed a critical `difficulty` hash-mismatch bug.

---

## 1. Problem: Engine API Rejects 97-Byte extra_data

### Root Cause

Alloy's `ExecutionPayloadV1::into_block_raw_with_transactions_root_opt()` enforces:
```
MAXIMUM_EXTRA_DATA_SIZE = 32 bytes
```

POA blocks use 97-byte `extra_data`:
```
[vanity: 32 bytes][ECDSA signature: 65 bytes] = 97 bytes total
(epoch blocks additionally carry N*20 bytes of signer addresses between vanity and seal)
```

When the Engine API receives a `engine_newPayloadV3` call with a POA block, it calls
`convert_payload_to_block()` → alloy rejects with "extra_data too long" before
`PoaConsensus` ever gets a chance to validate the signature.

### Solution: PoaEngineValidator (src/node.rs)

Strip `extra_data` before alloy conversion, restore it after, then reseal to get the correct hash:

```
engine_newPayloadV3 call
    │
    ▼
PoaEngineValidator::convert_payload_to_block()
    │
    ├── 1. extract expected_hash = payload.block_hash()
    ├── 2. strip_extra_data(payload) → (stripped_payload, orig_extra_97_bytes)
    ├── 3. stripped_payload.try_into_block_with_sidecar() → Block (extra_data = "")
    ├── 4. block.header.extra_data = orig_extra_97_bytes  (restore)
    ├── 5. SealedBlock::seal_slow(block)  (recompute hash with restored extra_data)
    └── 6. assert sealed.hash() == expected_hash  (or return PayloadError::BlockHash)
```

---

## 2. Implementation: PoaEngineValidator

### strip_extra_data (src/node.rs:66-81)

```rust
fn strip_extra_data(payload: ExecutionPayload) -> (ExecutionPayload, alloy_primitives::Bytes) {
    match payload {
        ExecutionPayload::V1(mut v1) => {
            let extra = std::mem::take(&mut v1.extra_data);
            (ExecutionPayload::V1(v1), extra)
        }
        ExecutionPayload::V2(mut v2) => {
            let extra = std::mem::take(&mut v2.payload_inner.extra_data);
            (ExecutionPayload::V2(v2), extra)
        }
        ExecutionPayload::V3(mut v3) => {
            let extra = std::mem::take(&mut v3.payload_inner.payload_inner.extra_data);
            (ExecutionPayload::V3(v3), extra)
        }
    }
}
```

### PoaEngineValidator struct (src/node.rs:88-97)

Wraps `EthereumEngineValidator` and overrides only `convert_payload_to_block`:

```rust
#[derive(Debug, Clone)]
pub struct PoaEngineValidator<ChainSpec = reth_chainspec::ChainSpec> {
    inner: EthereumEngineValidator<ChainSpec>,
}

impl<ChainSpec> PoaEngineValidator<ChainSpec> {
    pub const fn new(chain_spec: Arc<ChainSpec>) -> Self {
        Self { inner: EthereumEngineValidator::new(chain_spec) }
    }
}
```

### PayloadValidator impl (src/node.rs:99-139)

```rust
impl<ChainSpec, Types> PayloadValidator<Types> for PoaEngineValidator<ChainSpec>
where
    ChainSpec: reth_chainspec::EthChainSpec + reth_ethereum_forks::EthereumHardforks + 'static,
    Types: PayloadTypes<ExecutionData = ExecutionData>,
{
    type Block = reth_ethereum::Block;

    fn convert_payload_to_block(
        &self,
        payload: ExecutionData,
    ) -> Result<SealedBlock<Self::Block>, NewPayloadError> {
        let ExecutionData { payload, sidecar } = payload;
        let expected_hash = payload.block_hash();
        let (stripped, orig_extra) = strip_extra_data(payload);
        let mut block: reth_ethereum::Block = stripped
            .try_into_block_with_sidecar(&sidecar)
            .map_err(|e| NewPayloadError::Other(e.into()))?;
        block.header.extra_data = orig_extra;
        let sealed = SealedBlock::seal_slow(block);
        if expected_hash != sealed.hash() {
            return Err(PayloadError::BlockHash {
                execution: sealed.hash(),
                consensus: expected_hash,
            }
            .into());
        }
        Ok(sealed)
    }
}
```

### EngineApiValidator impl (src/node.rs:141-169)

Delegates `validate_version_specific_fields` and `ensure_well_formed_attributes` to inner
via fully-qualified trait syntax (required by Rust to disambiguate):

```rust
impl<ChainSpec, Types> EngineApiValidator<Types> for PoaEngineValidator<ChainSpec>
where ...
{
    fn validate_version_specific_fields(&self, ...) -> ... {
        <EthereumEngineValidator<ChainSpec> as EngineApiValidator<Types>>::validate_version_specific_fields(
            &self.inner, version, payload_or_attrs,
        )
    }
    fn ensure_well_formed_attributes(&self, ...) -> ... {
        <EthereumEngineValidator<ChainSpec> as EngineApiValidator<Types>>::ensure_well_formed_attributes(
            &self.inner, version, attributes,
        )
    }
}
```

### PoaEngineValidatorBuilder (src/node.rs:172-193)

Implements `PayloadValidatorBuilder<Node>` for injection into the node's AddOns:

```rust
#[derive(Debug, Default, Clone)]
pub struct PoaEngineValidatorBuilder;

impl<Node, Types> PayloadValidatorBuilder<Node> for PoaEngineValidatorBuilder
where
    Types: NodeTypes<
        ChainSpec: EthChainSpec + EthereumHardforks + Clone + 'static,
        Payload: EngineTypes<ExecutionData = ExecutionData>
            + PayloadTypes<PayloadAttributes = EthPayloadAttributes>,
        Primitives = EthPrimitives,
    >,
    Node: FullNodeComponents<Types = Types>,
{
    type Validator = PoaEngineValidator<Types::ChainSpec>;

    async fn build(self, ctx: &AddOnsContext<'_, Node>) -> eyre::Result<Self::Validator> {
        Ok(PoaEngineValidator::new(ctx.config.chain.clone()))
    }
}
```

### PoaNode::add_ons() updated (src/node.rs:325-360)

```rust
type AddOns = EthereumAddOns<
    NodeAdapter<N>,
    EthereumEthApiBuilder,
    PoaEngineValidatorBuilder,
    BasicEngineApiBuilder<PoaEngineValidatorBuilder>,
    BasicEngineValidatorBuilder<PoaEngineValidatorBuilder>,
    Identity,
>;

fn add_ons(&self) -> Self::AddOns {
    EthereumAddOns::new(RpcAddOns::new(
        EthereumEthApiBuilder::default(),
        PoaEngineValidatorBuilder,
        BasicEngineApiBuilder::<PoaEngineValidatorBuilder>::default(),
        BasicEngineValidatorBuilder::new(PoaEngineValidatorBuilder),
        Identity::default(),
    ))
}
```

---

## 3. Bug Fix: Difficulty Hash Mismatch

### Root Cause

The payload builder was setting `difficulty = 1` (in-turn) or `2` (out-of-turn).
The Engine API (`ExecutionPayloadV1`) has **no difficulty field**. Alloy hardcodes
`difficulty = U256::ZERO` when deserializing a payload back to a block.

In `PoaEngineValidator::convert_payload_to_block`, we strip extra_data, convert
(which sets difficulty = 0), restore extra_data, and reseal. The new hash includes
`difficulty = 0`, but the original block was sealed with `difficulty = 1/2`, so
`expected_hash != sealed.hash()` → hash mismatch error.

### Fix

**`src/payload.rs`**: Always set `difficulty = U256::ZERO`:
```rust
// Difficulty must be 0 for Engine API compatibility.
// ExecutionPayloadV1 has no difficulty field; alloy hardcodes U256::ZERO on conversion.
// POA authority is determined solely by the ECDSA signature in extra_data.
header.difficulty = U256::ZERO;
```

**`src/consensus.rs`**: Updated `validate_difficulty` to require 0:
```rust
pub fn validate_difficulty(&self, header: &Header, _signer: &Address) -> Result<(), PoaConsensusError> {
    if header.difficulty != U256::ZERO {
        return Err(PoaConsensusError::InvalidDifficulty);
    }
    Ok(())
}
```

Updated 3 tests:
- `test_validate_difficulty_zero` — passes with `U256::ZERO`
- `test_validate_difficulty_zero_any_signer` — any authorized signer with `U256::ZERO`
- `test_validate_difficulty_nonzero_rejected` — `U256::from(1)` is rejected

---

## 4. New CLI Flag: --mining

**`src/main.rs`**: Added `--mining` flag to force interval block production in production mode:

```rust
/// Force interval-based block production even in production mode.
/// Useful for testing: node uses production signing (97-byte extra_data, strict POA)
/// but still auto-mines blocks at --block-time interval.
#[arg(long)]
mining: bool,
```

```rust
let mining_enabled = is_dev_mode || cli.mining;
let dev_args = if !mining_enabled {
    DevArgs::default()
} else {
    DevArgs { dev: true, block_time: Some(Duration::from_secs(...)), ... }
};
```

Mode display:
```
Mode: production+mining   (--production --mining)
Mode: production           (--production alone)
Mode: dev                  (default)
```

---

## 5. Bug Fix: block_in_place for Async Context

### Problem

In production+mining mode, the payload builder's `sign_payload()` is called from inside
a Tokio async task (the mining loop). `Handle::block_on()` panics if called from within
an async context: "Cannot start a runtime from within a runtime".

### Fix (src/payload.rs)

```rust
// Correct: wrap block_on with block_in_place to park the async task
let (signer_addr, is_in_turn) = tokio::task::block_in_place(|| {
    handle.block_on(async {
        // ... async signer selection
    })
});

let signed_header = tokio::task::block_in_place(|| {
    handle.block_on(async { sealer.seal_header(header, &signer_addr).await })
})
.map_err(|e| PayloadBuilderError::Other(Box::new(e)))?;
```

---

## 6. Compile Errors Encountered and Fixed

| Error | Cause | Fix |
|-------|-------|-----|
| `reth_node_api::PayloadTypes` unresolved | `reth_node_api` not a direct dep | Use `PayloadTypes` from `reth_payload_primitives` |
| `reth_node_api::EngineTypes` unresolved | Same | Add `EngineTypes` to import from `reth_ethereum::node::api` |
| `reth_ethereum_primitives::Block` unresolved | Not a direct dep | Use `reth_ethereum::Block` |
| `BasicEngineApiBuilder::new()` not found | No `new()` method | Use `BasicEngineApiBuilder::<PVB>::default()` |
| Type ambiguity in `EngineApiValidator` delegation | Compiler can't resolve trait method | Use fully-qualified path: `<EthereumEngineValidator<ChainSpec> as EngineApiValidator<Types>>::method(...)` |
| `EthereumAddOns::default()` fails for custom PVB | `Default` only for `EthereumEngineValidatorBuilder` | Construct via `EthereumAddOns::new(RpcAddOns::new(...))` |
| `validate_version_specific_fields` unused import | Import not needed | Removed from import |

---

## 7. New Dependency

**`Cargo.toml`**: Added `alloy-rpc-types-engine = "1"` for `PayloadError::BlockHash` variant
used in `PoaEngineValidator::convert_payload_to_block`.

---

## 8. Test Results

```
test result: ok. 194 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out
```

New tests added in `src/node.rs`:
- `test_poa_node_creation`
- `test_poa_node_with_dev_mode`
- `test_poa_node_with_signer_manager`
- `test_poa_node_full_builder_chain`
- `test_poa_consensus_builder_creation`
- `test_poa_consensus_builder_dev_mode`
- `test_strip_extra_data_v1`
- `test_poa_engine_validator_builder_is_default`

---

## 9. Production Mode Verification

Test command:
```bash
./target/release/example-custom-poa-node \
  --production --mining --block-time 2 \
  --signer-key ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 \
  --datadir /tmp/meow-prod-test \
  --http-port 8547 --ws-port 8548
```

Observed output (9 blocks in 20s, no errors):
```
POA Consensus initialized: 5 signers, epoch: 30000, period: 2s, mode: production (strict)
POA node started successfully!
  POA block #1 signed by 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266 (out-of-turn)
  Block #1 - 0 txs (out-of-turn, expected: 0x70997970...)
  POA block #5 signed by 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266 (in-turn)
  Block #5 - 0 txs (in-turn: 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266)
  ...
```

No hash mismatch errors. Blocks accepted by the engine tree with 97-byte extra_data.

---

## 10. Key Reth Architecture Learnings

### Engine API flow for custom extra_data

```
Block Producer (PoaPayloadBuilder)
  └── Signs block with 97-byte extra_data → submits via engine_newPayloadV3

Engine Tree receives newPayload call
  └── PoaEngineValidator::convert_payload_to_block()
       ├── strips extra_data (bypasses alloy 32-byte check)
       ├── converts payload to block
       ├── restores extra_data
       └── reseals → passes sealed block to PoaConsensus

PoaConsensus::validate_block_pre_execution()
  └── recovers signer from ECDSA sig in extra_data → validates against signer list
```

### Why difficulty must be 0

The Engine API post-merge design treats difficulty as not transmitted. `ExecutionPayloadV1`
has no difficulty field; alloy hardcodes `difficulty = U256::ZERO` when deserializing.
POA authority is conveyed entirely through the ECDSA signature in `extra_data`, not difficulty.
Any non-zero difficulty breaks the hash round-trip through the Engine API.

### PayloadValidatorBuilder vs EthereumAddOns::default()

`EthereumAddOns` is generic over the `PayloadValidatorBuilder` type:
```
EthereumAddOns<N, EthApiBuilder, PVB, EngineApiBuilder, EngineValidator, Events>
```
The `Default` impl only exists when `PVB = EthereumEngineValidatorBuilder`. For a custom PVB,
construct via `EthereumAddOns::new(RpcAddOns::new(...))`.

### block_in_place + block_on

When calling async code from within a Tokio multi-threaded async context (e.g., payload builder
triggered by the dev miner):
- `Handle::block_on()` → panics ("Cannot start a runtime from within a runtime")
- `tokio::task::block_in_place(|| handle.block_on(...))` → correct approach

---

# Implementation Log --- 2026-02-24

## Summary

Phase 7: Production Infrastructure. Added Clique RPC (8 methods), Admin RPC (5 methods + health),
encrypted keystore (EIP-2335), Prometheus metrics registry, 12 new CLI flags, graceful shutdown,
CI/CD pipeline, Docker multi-node compose, and developer configuration files.

Total: 411 tests passing, ~15,000 lines across ~46 files, 18 modules.

---

## 11. Phase 7: Production Infrastructure

### 11.1 Clique RPC Namespace (`src/rpc/clique.rs`)

Standard Clique POA RPC namespace providing 8 methods for signer management and snapshot queries.
This follows the go-ethereum Clique API specification.

**Methods implemented:**

| Method | Description |
|--------|-------------|
| `clique_getSigners` | Returns current authorized signers |
| `clique_getSignersAtHash` | Returns signers at a specific block hash |
| `clique_getSnapshot` | Returns consensus snapshot (signers, votes, tally) |
| `clique_getSnapshotAtHash` | Returns snapshot at a specific block hash |
| `clique_propose` | Propose adding/removing a signer (bool: true=add, false=remove) |
| `clique_discard` | Discard a pending proposal |
| `clique_status` | Returns signing status (in-turn count, total blocks) |
| `clique_proposals` | Returns all pending proposals |

**Types** (`src/rpc/clique_types.rs`):
- `CliqueSnapshot` -- Signers, votes, and tally at a given block
- `CliqueStatus` -- Signing statistics
- `CliqueProposal` -- Address + authorize flag

**28 tests** covering all 8 methods, error handling, and serialization.

### 11.2 Admin RPC Namespace (`src/rpc/admin.rs`)

Admin RPC namespace for node management with a health check endpoint designed for load balancers.

**Methods implemented:**

| Method | Description | Use Case |
|--------|-------------|----------|
| `admin_nodeInfo` | Returns enode URL, network, protocol info | Node discovery |
| `admin_peers` | Returns connected peer list | Network debugging |
| `admin_addPeer` | Manually add a peer by enode URL | Network bootstrap |
| `admin_removePeer` | Disconnect a peer | Network management |
| `admin_health` | Health check (syncing, peer count, latest block) | Load balancer probes |

**Health check design:**
The `admin_health` endpoint is designed for HTTP load balancer health probes (e.g., AWS ALB, nginx).
It returns a JSON response with:
- `healthy: bool` -- Overall health status
- `syncing: bool` -- Whether the node is currently syncing
- `peerCount: u64` -- Number of connected peers
- `latestBlock: u64` -- Latest block number

A node is considered "healthy" when it is not syncing and has at least 1 peer (or is in dev mode).

**Types** (`src/rpc/admin_types.rs`):
- `AdminNodeInfo` -- Node identity and network information
- `PeerInfo` -- Individual peer details
- `HealthResponse` -- Health check response

**24 tests** covering all 5 methods, health logic, and edge cases.

### 11.3 Encrypted Keystore (`src/keystore/mod.rs`)

EIP-2335 compatible Ethereum Keystore V3 encrypted key storage. This replaces raw hex private keys
on the command line with proper encrypted storage on disk.

**Encryption approach:**

```
1. Key Derivation:
   - Algorithm: PBKDF2-HMAC-SHA256
   - Iterations: 262,144 (c=262144)
   - Salt: 32 random bytes
   - Derived key length: 32 bytes (dklen=32)

2. Encryption:
   - Algorithm: AES-128-CTR
   - Key: first 16 bytes of derived key
   - IV: 16 random bytes
   - Plaintext: 32-byte private key

3. MAC:
   - Algorithm: HMAC-SHA256
   - Key: last 16 bytes of derived key
   - Data: ciphertext
   - Used for integrity verification on decrypt
```

**File storage:** Keys are stored as JSON files in `<datadir>/keystore/` with filenames like
`UTC--2026-02-24T12-00-00Z--<address>`.

**KeystoreManager methods:**
- `create_account(password)` -- Generate new random key, encrypt, store, return address
- `import_key(private_key, password)` -- Encrypt existing key and store
- `decrypt_key(address, password)` -- Load file, derive key, verify MAC, decrypt
- `list_accounts()` -- List all stored addresses from filenames
- `delete_account(address)` -- Remove encrypted key file
- `load_into_signer_manager(password, signer_manager)` -- Decrypt all and load into runtime

**20 tests** covering create, import, decrypt, list, delete, wrong password, and SignerManager integration.

### 11.4 Prometheus Metrics Registry (`src/metrics/registry.rs`)

Thread-safe metrics registry with 19 atomic counters and a lightweight TCP HTTP server for
Prometheus scraping. Uses `AtomicU64` for lock-free counter increments.

**Design decisions:**
- **No external metrics crate dependency** -- Uses `std::sync::atomic::AtomicU64` directly for
  zero-overhead counters. The Prometheus exposition format is simple enough to generate manually.
- **Lightweight TCP HTTP server** -- Spawns a single tokio task that listens on `--metrics-port`
  (default 9001). Only responds to `GET /metrics`. No TLS (intended to be behind a firewall).
- **Thread-safe** -- `MetricsRegistry` is `Arc`-wrapped and shared across all components.
  Each counter uses `AtomicU64::fetch_add(1, Ordering::Relaxed)` for lock-free increments.

**Metrics exported** (all prefixed with `meowchain_`):
- Block production: `blocks_produced`, `blocks_in_turn`, `blocks_out_of_turn`
- Transactions: `transactions_processed`, `gas_used_total`
- Network: `peer_count`, `peer_connections`, `peer_disconnections`
- Chain: `chain_head`, `reorgs`, `forks_detected`
- EVM: `evm_executions`, `evm_errors`
- Payload: `payload_builds`, `payload_signs`, `payload_failures`
- Consensus: `consensus_validations`, `consensus_rejections`
- Cache: `cache_hits`, `cache_misses`

**16 tests** covering counter increments, Prometheus output format, and server lifecycle.

### 11.5 Production CLI Flags

12 new CLI flags added (31 total), all in `src/cli.rs`:

| Flag | Type | Default | Purpose |
|------|------|---------|---------|
| `--enable-metrics` | bool | false | Enable Prometheus HTTP server |
| `--metrics-port` | u16 | 9001 | Port for Prometheus scraping |
| `--http-corsdomain` | Option | -- | CORS allowed origins for HTTP RPC |
| `--http-api` | Option | -- | HTTP RPC namespace filter |
| `--ws-api` | Option | -- | WS RPC namespace filter |
| `--log-json` | bool | false | Structured JSON log output |
| `--rpc-max-connections` | u32 | 100 | Max concurrent RPC connections |
| `--rpc-max-request-size` | u32 | 15 | Max request size (MB) |
| `--rpc-max-response-size` | u32 | 150 | Max response size (MB) |
| `--archive` | bool | false | Archive mode (no pruning) |
| `--gpo-blocks` | u64 | 20 | Gas price oracle sample blocks |
| `--gpo-percentile` | u64 | 60 | Gas price oracle percentile |

### 11.6 Graceful Shutdown

SIGINT and SIGTERM signal handlers added in `main.rs`. On signal receipt:
1. Log shutdown message
2. Cancel the node exit future
3. Flush MDBX database
4. Close RPC server
5. Disconnect peers
6. Exit with code 0

### 11.7 CI/CD Pipeline

GitHub Actions workflow (`.github/workflows/ci.yml`):

| Job | Command | Purpose |
|-----|---------|---------|
| check | `cargo check` | Verify compilation |
| test | `cargo test` | Run 411 tests |
| clippy | `cargo clippy -- -D warnings` | Zero-warning policy |
| fmt | `cargo fmt --check` | Formatting consistency |
| build-release | `cargo build --release` | Ensure release build works |

Triggers: push to `main`, all pull requests.

### 11.8 Docker Multi-Node

`Docker/docker-compose-multinode.yml` defines a 4-node network:

| Service | Role | HTTP Port | P2P Port | Signer |
|---------|------|-----------|----------|--------|
| signer1 | Block producer | 8545 | 30303 | Account 0 |
| signer2 | Block producer | 8547 | 30304 | Account 1 |
| signer3 | Block producer | 8549 | 30305 | Account 2 |
| rpc | Read-only node | 8551 | 30306 | None |

All nodes share the same genesis and connect via bootnode discovery.

### 11.9 Developer Configs

| File | Purpose |
|------|---------|
| `configs/hardhat.config.js` | Hardhat network configuration with Meowchain RPC + dev key |
| `configs/foundry.toml` | Foundry profile with chain RPC and chain ID |
| `configs/networks.json` | Network registry (chain ID, RPC URLs, explorer URL) |
| `configs/grafana-meowchain.json` | Grafana dashboard template (import with Prometheus data source) |

---

# Implementation Log --- 2026-03-16

## Summary

Zero-gas mode and GEVM cleanup. Added `--zero-gas` CLI flag for fee-free operation,
removed all GEVM/grevm comparison content from codebase and docs.

---

## 12. Zero-Gas Mode (`--zero-gas`)

### Design

Two mechanisms combine to eliminate gas fees:

1. **Genesis base fee = 0**: When `--zero-gas` is set, `genesis.base_fee_per_gas = Some(0)`.
   EIP-1559 formula: `base_fee = parent_base_fee + parent_base_fee * gas_delta / gas_target / denominator`.
   When parent base fee is 0, the result is always 0 — base fee stays at 0 forever.

2. **revm `disable_base_fee`**: `PoaEvmFactory::patch_env()` sets `cfg_env.disable_base_fee = true`,
   which skips base fee validation during EVM execution. This allows `gasPrice: 0` / `maxFeePerGas: 0`.

Gas is still **metered** for execution limits (preventing infinite loops), but users pay nothing.

### Files Changed

| File | Change |
|------|--------|
| `src/cli.rs` | Added `--zero-gas` flag (bool) |
| `src/evm/mod.rs` | `PoaEvmFactory.zero_gas` field; `patch_env()` sets `disable_base_fee`; 4 new tests |
| `src/node/mod.rs` | `PoaNode.zero_gas` field + `with_zero_gas()` builder method |
| `src/main.rs` | Sets `genesis.base_fee_per_gas = Some(0)` when enabled; wires to `PoaNode` |

### Test Results

```
test result: ok. 425 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out
```

## 13. GEVM/grevm Cleanup

Removed all GEVM comparison content:
- Deleted `md/GEVM-Comparison.md` (651 lines) and `md/EVM-Design.md` (1627 lines)
- Removed GEVM comparison tests and helper functions from `src/evm/bench.rs`
- Cleaned grevm references from `src/evm/parallel.rs` module docs
- Removed sections 16-17, 20 from `md/Remaining.md` (GEVM ranking, comparison tables, lessons)

# RPC Deployment Runbook

How to roll out the RPC/indexing fixes to the live chain (`rpc.example.com`,
chainId 9323310), what to verify afterwards, and what still has to be fixed by
hand on the chain box.

**No chain wipe is required anywhere in this document.** Everything here is
node *configuration*. Genesis, block history, account balances and every
deployed contract live in the MDBX datadir and survive a restart untouched. The
only thing that gets rebuilt is the Blockscout index, and only if you choose the
reindex path in [Backfilling internal transactions](#backfilling-internal-transactions).

---

## 1. What changed and why

### 1.1 The explorer could not see internal transactions

`your factory contract` deploys one token per property listing via an internal
`CREATE`. A contract created by another contract produces no top-level
transaction, so an explorer can only find it by *tracing* the parent
transaction. The live node served no `debug_*` and no `trace_*` at all, so every
contract deployed by a contract was invisible.

- `src/cli.rs` — `--http-api` / `--ws-api` now default to
  `eth,net,web3,txpool,debug,trace,rpc,ots,meow,clique`.
- `scripts/runnable.sh` — passes that list explicitly on both transports and
  adds `--archive`.

`ots` (Otterscan) is new here. It is read-only and rides on the same tracing
machinery, and `ots_getInternalOperations` is a single call that answers "did
this transaction create contracts?" — the cheapest possible check that internal
transactions are actually visible.

### 1.2 `--archive`: tracing needs the state to still exist

Without `--archive` the node runs Reth's `--full` pruning, which deletes
**receipts** and **account/storage history** older than 10,064 blocks (and prunes
sender recovery entirely). At this chain's block rate that window is hours, not
months. Past it, `eth_getTransactionReceipt`, `debug_traceTransaction` and
historical `eth_getBalance` all start failing with no other symptom — the
explorer just quietly stops having data.

The chain is currently at ~2,575 blocks, so **nothing has been pruned yet**.
This is being fixed before the deadline, not after it.

- `scripts/runnable.sh` — passes `--archive`.
- `src/main.rs` — when `--archive` is *not* set, the node now prints a loud
  startup warning describing exactly what will be deleted. The prod launch
  command is not in this repo, so the log is the only place the mistake can be
  caught.

### 1.3 Why `admin_*` answered publicly even though nothing enabled it

This was the open question, and it is a bug in this repo, not a mystery flag on
the box.

`extend_rpc_modules` used `TransportRpcModules::merge_configured`, which merges
methods into **http, ws and ipc unconditionally** — it does not consult
`--http-api` / `--ws-api` at all. This node registers three of its own
namespaces that way (`meow_*`, `clique_*`, `admin_*`), so
`AdminRpc::{nodeInfo, peers, addPeer, removePeer, health}` were served on the
public endpoint no matter what the module list said. The module list was never
authoritative for the custom namespaces.

`src/main.rs` now uses `merge_if_module_configured(module, methods)`, which
honours the per-transport selection. Consequences:

- `admin_*` is served only if `admin` appears in `--http-api` / `--ws-api`.
  It is not in the defaults, so by default it is gone.
- `meow_*` and `clique_*` must now be **named explicitly** in the module list or
  they stop being served. Both are in the new defaults and in `runnable.sh`.
  Do not drop them from a launch command.

`clique_propose` / `clique_discard` were reachable through the same hole. They
only mutate an in-memory proposal map on this node and do not change the signer
set (that is on-chain via `SignerRegistry`), so this was not exploitable — but
it was never meant to be public either.

### 1.4 A typo in `--http-api` silently disabled a whole namespace

`RethRpcModule::from_str` never fails. Any unrecognised name becomes
`Other(name)`, which registers zero methods and logs nothing. So
`--http-api eth,net,web3,dbeug` starts cleanly and serves no `debug_*`. This is
precisely the failure mode that would let the deploy "look right" and still ship
a node with no tracing.

`src/main.rs` now validates every parsed module name and **fails to start** on
anything that is neither a Reth built-in nor one of this node's two custom
namespaces. (Earlier in this session the parse *error* was already made fatal;
that only caught malformed input, which in practice never happens. This catches
the case that does.)

### 1.5 `eth_estimateGas` was capped below the block gas limit

Reth caps `eth_call` / `eth_estimateGas` / `eth_simulateV1` at 50M gas by
default. This chain runs 300M (dev) / 1B (production) blocks and deploys a full
token from a single `your factory contract` call via internal `CREATE`s. The cap
could reject an estimate for a transaction the chain would execute happily.

- `src/cli.rs` — new `--rpc-gas-cap`, default `0` meaning "follow the genesis
  block gas limit".
- `src/main.rs` — wires it into `RpcServerArgs::rpc_gas_cap` and logs the
  effective value at startup.

### 1.6 IPC path collision between the two chains on one host

Reth's IPC endpoint is enabled by default at the fixed path `/tmp/reth.ipc`, and
**IPC serves every namespace** (`RpcModuleSelection::default_ipc_modules()` is
"all") regardless of `--http-api` — including `admin`, `debug`, `miner` and
`testing`. Both brands build the same binary and run on the same box, so they
were fighting over one socket path.

`src/main.rs` now scopes the socket to `<datadir>/reth.ipc`. Each node gets its
own, and it sits under the deployment directory rather than world-writable
`/tmp`.

### 1.7 Blockscout was not asked to index internal transactions

Even with a perfectly configured node, the indexer had the fetcher switched off:

`scoutup-go-explorer/blockscout/embed/common-blockscout.env`

- `INDEXER_DISABLE_INTERNAL_TRANSACTIONS_FETCHER` `true` → `false`
- `INDEXER_INTERNAL_TRANSACTIONS_TRACER_TYPE` pinned to `call_tracer` (Reth
  implements the Geth callTracer, not Parity's JS tracer)

### 1.8 What was checked and deliberately left alone

| Area | Finding | Action |
|---|---|---|
| Hardforks (`src/chainspec/hardforks.rs`) | Frontier→Prague all active at genesis. PUSH0 (Shanghai), TSTORE/TLOAD + blobs (Cancun), 7702/7002/2935 (Prague) all present. Only Osaka/Fusaka is absent. | **Do not add.** Every fork here activates at timestamp 0, so adding one changes execution rules for blocks that are already sealed. That is a consensus break and the only way to apply it is a chain wipe. Not worth it for Osaka. |
| Blob params | `BlobScheduleBlobParams::default()` already carries Cancun/Prague schedules, so `eth_feeHistory` blob fields and `excess_blob_gas` are correct. | None |
| `eth_subscribe` | Provided by `eth` on the WS transport; WS is enabled and `eth` is in the WS list. | None |
| Gas price oracle | `--gpo-blocks 20` / `--gpo-percentile 60` wired; Reth's `ignore_price` default is 0, so `--zero-gas` does not confuse the oracle into suggesting a phantom fee. | None |
| `eth_getLogs` limits | Defaults are 100,000 blocks per filter and 20,000 logs per response — far above anything Blockscout issues. | None |
| `trace_filter` range | Capped at 100 blocks per query. Blockscout does not use `trace_filter` (it uses the callTracer path), so this only matters for ad-hoc backfill tooling. | Documented, not changed |
| `eth_getProof` | `rpc_eth_proof_window` is 0, i.e. latest block only. Nothing in this stack calls it. | None |
| Engine API / auth server | Runs on loopback `8551` with a generated JWT. Not publicly reachable. | None |
| CORS | `--http-corsdomain` is unset, so the node emits no CORS headers. If browser dapps hit the RPC directly, add it **either** at nginx **or** via `--http-corsdomain` — never both, since duplicate `Access-Control-Allow-Origin` headers break CORS harder than a missing one. | Documented, not changed |
| Prometheus | `--enable-metrics` is off. Not required by Blockscout; enable if you want `/metrics` on 9001. | None |
| Health endpoint | `admin_health` disappears along with the rest of `admin_*`. Load balancers should probe `eth_blockNumber` or `net_listening`, both public and unprivileged. | Documented |

---

## 2. Restart procedure

**Nothing below wipes the chain.** Do not run `runnable.sh reset` or
`runnable.sh nuke` — those delete the datadir and are not what this change
needs.

### 2.1 Build

```bash
cd custom-reth-chain
cargo build --release
```

The binary is `target/release/example-custom-poa-node`.

### 2.2 Restart the node

Local / repo-managed deployment:

```bash
cd custom-reth-chain
./scripts/runnable.sh chain restart      # stop_chain; sleep 1; start_chain
```

`stop_chain` kills the process by its absolute datadir path (both brands share a
binary name, so the datadir is what disambiguates) and **leaves
`apps/custom-reth-chain/data` in place**. `start_chain` reopens the same MDBX
database. Block height, balances and deployed contract addresses are unchanged
across the restart.

On the chain box the unit is not managed by this script — see
[section 5](#5-the-production-launch-command-still-has-to-be-fixed-by-hand).
Whatever supervises it there, the procedure is the same shape: stop the process,
start it with the corrected flags, **do not touch the datadir**.

### 2.3 Restart Blockscout

```bash
./scripts/runnable.sh explorer restart
```

This picks up the two changed variables in `common-blockscout.env`. It does not
drop the index volume.

---

## 3. Verification after restart

Replace `$RPC` as appropriate — `http://127.0.0.1:8645` for the repo-managed
deployment, `https://rpc.example.com` for production.

```bash
RPC=http://127.0.0.1:8645
rpc() { curl -s "$RPC" -H 'content-type: application/json' \
  -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$1\",\"params\":$2}"; }
```

### 3.1 These must now SUCCEED

```bash
# The enabled module list, straight from the node. This is the one-shot answer.
rpc rpc_modules '[]'
# expect: eth, net, web3, txpool, debug, trace, rpc, ots, meow, clique
#         and NO admin

# Tracing is alive
rpc debug_traceBlockByNumber '["latest", {"tracer":"callTracer"}]'

# Parity-style traces
rpc trace_block '["latest"]'

# Otterscan internal ops for a known your factory contract deployment tx
rpc ots_getInternalOperations '["0x<a-tx-that-deploys-a-contract-from-a-contract>"]'
# expect a CREATE entry per token deployed by that transaction

# Receipts and historical state (this is what --archive protects)
rpc eth_getTransactionReceipt '["0x<any-old-tx-hash>"]'
rpc eth_getBalance '["0x19ED934481B2f550b74487F62C3232Ec91Fac184", "0x1"]'

# Custom namespaces survived the gating change
rpc meow_chainConfig '[]'
rpc clique_getSigners '[]'
```

WebSocket subscriptions (`wscat -c ws://127.0.0.1:8646`):

```json
{"jsonrpc":"2.0","id":1,"method":"eth_subscribe","params":["newHeads"]}
```

### 3.2 These must now FAIL with `Method not found` (-32601)

```bash
rpc admin_nodeInfo '[]'
rpc admin_peers '[]'
rpc admin_addPeer '["enode://dead@127.0.0.1:30303"]'
rpc admin_removePeer '["enode://dead@127.0.0.1:30303"]'
rpc admin_health '[]'
```

If any of these still returns a result, the running process is not the new
binary, or its launch command names `admin` in `--http-api` / `--ws-api`.

### 3.3 Startup log

The node prints, after launch:

```
HTTP API modules: eth,net,web3,txpool,debug,trace,rpc,ots,meow,clique
WS API modules:   eth,net,web3,txpool,debug,trace,rpc,ots,meow,clique
Archive mode: all historical state retained
eth_call/eth_estimateGas gas cap: <block gas limit>
```

There must be **no** `WARNING: state pruning is ON` line. If there is, `--archive`
is missing from the launch command and the fix in 1.2 is not in effect.

Similarly, if `admin` was left in a launch command, the node prints the
non-loopback admin warning. Both warnings are there so a wrong launch command is
visible in the log rather than only in a pentest.

---

## 4. Backfilling internal transactions

Turning the fetcher on only affects blocks indexed *from now on*. Blocks that
Blockscout already imported while `INDEXER_DISABLE_INTERNAL_TRANSACTIONS_FETCHER`
was `true` have no `pending_block_operations` row, so nothing will ever go back
for them. Existing land tokens stay invisible until you force a re-trace.

### Option A — reindex from scratch (recommended here)

The chain is only ~2,575 blocks. A full reindex takes minutes and removes any
doubt about partially-indexed history.

```bash
cd custom-reth-chain
./scripts/runnable.sh explorer wipe      # compose down -v + drop the Blockscout PG volume
./scripts/runnable.sh explorer start
```

`explorer wipe` drops **only** the Blockscout index volume. It does not touch the
chain datadir and it is not the application's Postgres — the chain and the app
database are unaffected. Blockscout re-imports every block from 0 with the
internal-transaction fetcher enabled.

### Option B — re-queue existing blocks in place

If a wipe is undesirable, queue every consensus block for internal-transaction
fetching by inserting the pending operations directly:

```sql
INSERT INTO pending_block_operations (block_hash, block_number, inserted_at, updated_at)
SELECT b.hash, b.number, NOW(), NOW()
FROM blocks b
WHERE b.consensus = true
ON CONFLICT DO NOTHING;
```

Then restart the Blockscout backend so the fetcher picks the queue up. Watch the
indexer logs; on a 1s-block chain the queue drains quickly, but each block costs
one `debug_traceBlockByNumber` against the node.

### Verifying the backfill

Pick a listing whose token was deployed by `your factory contract` and open the
parent transaction in the explorer. The **Internal Transactions** tab must show a
`create` entry whose "created contract" address is the token. The token address
must also resolve as its own contract page. If the tab is empty but
`ots_getInternalOperations` on the same hash returns a CREATE, the node is fine
and the problem is on the indexer side.

Optional throughput tuning if the backfill is slow: set
`ETHEREUM_JSONRPC_GETH_TRACE_BY_BLOCK=true` so Blockscout issues one
`debug_traceBlockByNumber` per block instead of one `debug_traceTransaction` per
transaction. Reth supports both.

---

## 5. The production launch command still has to be fixed by hand

**This repo cannot fix production on its own.**

The process on the chain box (`ip-172-31-0-165`) is not started by
`scripts/runnable.sh`. Its launch command differs from this repo — that is a
measured fact, not a guess: the live node serves `eth, net, web3, txpool` and
**not** `debug`, `trace`, `rpc` or `ots`, which no command in this repo produces.
There is no SSH access from here.

Someone with access must:

1. Find the actual launch command — systemd unit, docker-compose service, or a
   shell script. Start with `systemctl cat <unit>`, `docker inspect`, or
   `ps -ef | grep example-custom-poa-node`.
2. Deploy the newly built binary.
3. Correct the flags to match `scripts/runnable.sh`:
   - `--http-api eth,net,web3,txpool,debug,trace,rpc,ots,meow,clique`
   - `--ws-api` the same
   - `--archive`
   - **remove `admin`** if it is named there.
4. Restart the process **without touching the datadir**.
5. Run section 3 against `https://rpc.example.com`.

On the `admin` question specifically: with the fix in 1.3, `admin_*` is gone
unless the launch command explicitly asks for it. Step 3 covers it either way,
but the launch command must still be read and archived somewhere — a production
node whose command line nobody can quote is the underlying problem here, and it
will produce the next surprise too.

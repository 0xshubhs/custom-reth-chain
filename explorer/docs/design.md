# Explorer design

How the explorer works inside: what the indexer guarantees, the verifier's contract, and the HTTP API the website is built on. Setup and settings are in [../README.md](../README.md).

## Goals

1. **Nothing lost.** Every block, transaction, receipt, log and call trace the
   node has is stored, including the node's raw JSON for each, so a field we do
   not model today is still there tomorrow.
2. **Index only when something happens.** The indexer subscribes to the
   node's new blocks (`eth_subscribe newHeads`) and sleeps until one arrives;
   without a websocket it polls `eth_blockNumber`. On a chain that seals a
   block only when a transaction arrives, it does nothing between transactions.
3. **Never skip, never double.** A missed notification is caught up by the
   next one. The cursor is written in the same database transaction as the
   block, so a crash leaves either the whole block or none of it.
4. **Familiar links.** `/tx/<hash>`, `/address/<addr>`, `/token/<addr>` and
   `/block/<n>` are Blockscout's paths, and the verification endpoint takes
   Blockscout's standard-input form, so existing links and verify scripts work.

## Database

Schema is `backend/internal/db/migrations/001_init.sql`. Conventions:

- addresses and hashes: lowercase `0x` text. The API returns addresses
  EIP-55 checksummed.
- wei and token amounts: `numeric(78,0)`.
- every block, transaction, receipt and trace keeps the node's raw JSON
  (`raw`, `receipt_raw`, `trace_raw`).
- everything that belongs to a block has `block_number ... REFERENCES
  blocks(number) ON DELETE CASCADE`, so rolling back a block is one DELETE.
- derived tables (`addresses`, `tokens`, `token_balances`) are recomputed for
  the touched keys after a rollback.
- `002_verification_job_request.sql` adds `verification_jobs.request jsonb`
  (the submitted form and standard JSON, so a job survives an API restart;
  cleared when the job ends) and a partial index on pending jobs.

## Indexer

Triggers, all of which run the same catch-up (single flight: a trigger during
a run makes the run go round once more):

1. start-up;
2. `eth_subscribe newHeads` on `CHAIN_WS_URL` (the push from the node). Ping
   every 30 s; on a dead socket reconnect with backoff and catch up;
3. `POST /hooks/new-block` (internal webhook; anything may nudge it);
4. the safety check every `SAFETY_CHECK_SECONDS`.

Catch-up: `head = eth_blockNumber`; for `n = cursor+1 .. head`, fetch the block
with full transactions, `eth_getBlockReceipts`, and
`debug_traceBlockByNumber(n, {"tracer":"callTracer"})`, plus balances, code
and token reads at block `n` (the node is an archive node). Then, in ONE
database transaction: check `parent_hash` equals the stored hash of `n-1`
(otherwise roll back and refetch), insert everything, move the cursor, and
`NOTIFY explorer_events, '{"type":"block","number":n}'`. Commit.

Before the loop, the stored tip is compared with the node's block at that
height: a reorg that kept the height (or a chain that went backwards, the
node then having no block there) is found even when no new block comes.
Traces fall back to per-transaction `debug_traceTransaction` when the node
answers `debug_traceBlockByNumber` with method-not-found (anvil does), and
receipts to `eth_getTransactionReceipt` when `eth_getBlockReceipts` is
missing. State reads at `n` use the EIP-1898 `{"blockHash": ...}` tag when the
node takes it, so a reorg between fetching a block and reading its state cannot
mix two forks.

A reorg is rolled back to the highest stored block whose hash the node still
has (searched downwards in batches of 64). Derived rows are recomputed for
every key the removed blocks touched: address rows from what is still stored
(spans, tx counts, nonce, balance from `balance_history`; creation fields
reset when the creating block is gone; rows that no longer appear anywhere
deleted), tokens first seen above the fork deleted, and token balances and
supplies re-read from the node at the fork block. After a rollback
`token_balances.block_number` and `tokens.updated_block` name the fork block
(where the value was re-read). 'similar' verifications of contracts created
above the fork are deleted; own verifications are kept.

Start-up guards: the node's chain id must equal `CHAIN_ID`; the stored
`genesis_hash` (written on first run) must equal the node's block 0 hash. A
mismatch means a different chain and the indexer exits with a loud log rather
than mixing two chains in one database. Only one process writes:
`pg_try_advisory_lock(db.IndexerLockKey)` on the indexer's own session,
taken for each catch-up and released after it. A trigger that finds the lock
held (another indexer, or `admin reindex`) retries a second later, so the
admin commands wait at most one catch-up, and two indexers on one database
take turns instead of writing at once.

Derivations:

- **contracts**: every CREATE/CREATE2 frame that did not fail (the root frame
  when `to` is null). Store creator, creation tx, creation input (the frame
  input = init code + constructor args), runtime code (`eth_getCode` at `n`,
  deduplicated in `contract_code` by keccak).
- **tokens**: first log at an address with
  `Transfer(address,address,uint256)` `0xddf252ad...` and 3 topics = ERC-20,
  4 topics = ERC-721; `TransferSingle` `0xc3d58168...` /
  `TransferBatch` `0x4a39dc06...` = ERC-1155. Read `name()`, `symbol()`,
  `decimals()`, `totalSupply()` at `n`. `identityRegistry()` answering a
  non-zero address marks `is_erc3643`.
- **token balances**: for every (token, holder[, id]) touched in block `n`,
  read the real balance at `n` (`balanceOf`, `balanceOf(holder,id)`,
  `ownerOf(id)`), not a sum of transfers. Rows at zero are deleted, so
  `holders_count` is a row count. The zero address is never a holder. An
  ERC-721 id is one row (balance 1) for exactly its `ownerOf` owner; an
  `ownerOf` that reverts (burned) removes it. `holders_count` is the number of
  distinct holders for every type (for ERC-20 that is the row count). A
  `balanceOf` that reverts leaves the stored row as it was.
- **native balances**: every address seen in block `n` (miner, withdrawal
  recipients, from, to, created, every call frame's from and to, log
  emitters, token transfer parties) gets `eth_getBalance` at `n`; one
  `balance_history` row per change. The first point of an address is its
  balance at first sighting (delta = that balance). Withdrawals (this chain
  pays two every block) are only in `blocks.raw`, but their recipients are
  touched addresses.
- **nonce**: the next nonce after the address's last sent transaction
  (`max(tx.nonce) + 1`), null for an address that never sent one.
- **similar contracts**: when a contract is verified, every other contract with
  the same runtime code hash is marked verified (`match_type = 'similar'`,
  `source_address` = the verified one). Same when a new contract is created
  with a code hash that is already verified. One submission therefore
  verifies every contract a factory created from the same code.

Health: `GET /api/health` on 8080 answers `{"status":"ok"}`; with no proxy
headers present it also returns `last_block`, `node_block`, `lag` (and
`writer`, `ws_connected`, `ws_reconnects`, `last_catch_up`, `last_error`).
503 `{"status":"degraded"}` when the database does not answer.

`POST /hooks/new-block` answers 202 `{"status":"queued"}`; with
`INDEXER_HOOK_TOKEN` set, a missing or wrong `X-Hook-Token` gets 401.

## Operating it

`explorer admin <command>` (in the API or indexer container:
`docker compose exec explorer-indexer /app/explorer admin status`). Every
command is safe while the API and the indexer run.

| Command | What it does |
|---|---|
| `status [--json]` | cursor block and hash, node head, lag, last block time, row count of every table, chain id and genesis hash stored vs node, connected indexer sessions, whether the writer lock is held right now. Lists anything wrong under `PROBLEM:`. |
| `check [--from N] [--to M] [--json]` | compares every stored block hash and tx count from N (default 0) to M (default the cursor) with the node, 100 blocks per batch, and prints each mismatch (hash differs, tx count differs, missing in the database, missing on the node, rows above the cursor). Exit code 1 on any mismatch: the "are we still a faithful copy" command. |
| `reindex --from N [--to M] --yes` | waits for the writer lock, rolls back blocks >= N the normal cascade way (the same code as a reorg, derived rows recomputed), moves the cursor to N-1, releases the lock and pokes `INDEXER_HOOK_URL` so the indexer refills. With `--to M` it also re-indexes N..M itself (same fetch and commit as the indexer) before releasing. Refuses without `--yes` (exit 2). |
| `label set <address> <name>` | sets the label shown as the address name (`address_names`, source `manual`). |
| `label rm <address>` | removes the label, whatever its source. |

Exit codes: 0 done, 1 failure or mismatch, 2 usage.

## Verifier service (internal, JSON)

`POST /verify`

```json
{
  "compiler_version": "v0.8.28+commit.7893614a",
  "standard_json": { "language": "Solidity", "sources": {}, "settings": {} },
  "contract_name": "optional, 'path/File.sol:Name' or 'Name'",
  "deployed_bytecode": "0x... (eth_getCode at latest)",
  "creation_input": "0x... init code + args, or null when unknown"
}
```

Answer (200 in both cases):

```json
{
  "status": "success | failure",
  "message": "human readable",
  "match_type": "full | partial",
  "contract_name": "LandRegistry",
  "file_path": "src/land/LandRegistry.sol",
  "abi": [],
  "sources": { "src/land/LandRegistry.sol": "// SPDX ..." },
  "compiler_settings": {},
  "evm_version": "cancun",
  "optimization_enabled": true,
  "optimization_runs": 200,
  "via_ir": true,
  "constructor_args": "0x... or null"
}
```

On `failure` only `status` and `message` carry a value; the other fields are
present and `null`. Every problem with the submission (bad standard JSON,
unknown or unbundled compiler, compile errors, a timeout, no match) is a 200
`failure` whose message can be shown as it is. Only a body that is not JSON
(400) or larger than `MAX_INPUT_BYTES` (413) gets another status, with
`{"message":"..."}`. `constructor_args` is `null` when `creation_input` was not
given and also when it was given and there were no arguments. `standard_json`
may also be sent as a string holding the JSON.

`GET /compilers` -> `{"versions":["v0.8.28+commit.7893614a", ...]}`, newest
first: the bundled versions, plus (with `SOLC_DOWNLOAD=on`) the release builds
from the emscripten-wasm32 list.

Compilers: the official `emscripten-wasm32` build. The image bundles every
version in `backend/verifier/compilers.tsv` (one line each: version, file
name, sha256 copied from the official `list.json`). The image build downloads
each from `SOLC_BIN_BASE`, fails on any sha256 mismatch, and compiles an empty
contract with it; the service checks every bundled file against the manifest
at start-up (a mismatch stops it) and again before each use. Adding a version
is one reviewed line in that file plus a rebuild. With `SOLC_DOWNLOAD=on`
other versions are fetched from `SOLC_BIN_BASE`, checked against the sha256 in
its `list.json`, and cached in `COMPILER_CACHE_DIR`. Run by
`node runner.mjs <soljson.js>` with the standard
JSON on stdin and the output JSON on stdout (the raw `solidity_compile` export,
no import callback: standard JSON carries every source). One code path on every
machine, arm64 included (solc has no native arm64 build before 0.8.31).

Matching, per contract in the output (or only the named one):

1. Runtime: take `evm.deployedBytecode.object`; zero the byte ranges in
   `immutableReferences` in both compiled and on-chain code; fill library link
   placeholders from the on-chain code at the same offsets. Equal including
   the CBOR metadata tail = `full`. Equal once the metadata tail is removed
   from both = `partial`. When `settings.metadata.appendCBOR` is false there is
   no tail and equality is `full`. Two details: a library's runtime starts
   `PUSH20 <zero address>` in compiler output and `PUSH20 <its own address>`
   on chain, so those 20 bytes are taken from the chain; and for `partial`,
   the metadata blobs of contracts embedded in the code (a factory's
   `new C`) are masked too, where the chain has a metadata blob of the same
   size at the same place.
2. Creation (only when `creation_input` is given): compiled
   `evm.bytecode.object` (links filled, metadata handled the same way) must be
   a prefix of `creation_input`; the rest is `constructor_args`. For
   `partial`, the creation code's own trailing blob may differ in size; the
   arguments start where the on-chain blob ends. The result is the weaker of
   the runtime and creation matches.
3. Contracts are tried in file, then name, order (only the named one when
   `contract_name` is set, and only that one is compiled to bytecode). First
   contract that matches wins. None = `failure`, message
   `No contract in the input matches the deployed bytecode`.

## API

Base `/api/v2`, JSON. Read-only except verification and read-contract calls.

Conventions:

- `wei`/amounts: decimal strings. Block numbers, counts, gas, indexes: numbers.
- timestamps: ISO 8601 UTC (`2026-11-23T08:14:43Z`).
- addresses: EIP-55 checksummed. Hashes: lowercase.
- lists: `?limit=25&offset=0` (limit 1..100) ->
  `{"items":[...],"total":123,"next_offset":25}` (`next_offset` null at the end).
- errors: `{"message":"..."}` with 400 / 404 / 429 (429 carries `Retry-After`).

### Types (TypeScript, what the website codes against)

```ts
type Wei = string;            // decimal string
type Hash = string;           // 0x + 64 hex, lowercase
type Addr = string;           // EIP-55

interface Page<T> { items: T[]; total: number; next_offset: number | null }

interface AddressRef {
  hash: Addr;
  name: string | null;          // label, verified contract name, or token name
  is_contract: boolean;
  is_verified: boolean;
  token: { name: string | null; symbol: string | null; type: TokenType } | null;
}

type TokenType = "ERC-20" | "ERC-721" | "ERC-1155";

interface Stats {
  chain_id: number; network_name: string; native_symbol: string; public_rpc_url: string;
  latest_block: number | null;        // highest indexed block
  latest_block_time: string | null;
  node_block: number | null;          // what the node says, for the lag line
  total_blocks: number; total_transactions: number; total_addresses: number;
  total_contracts: number; total_tokens: number; verified_contracts: number;
}

interface BlockSummary {
  number: number; hash: Hash; timestamp: string; miner: AddressRef;
  tx_count: number; gas_used: string; gas_limit: string; size: number | null;
}
interface Block extends BlockSummary {
  parent_hash: Hash; state_root: Hash; transactions_root: Hash; receipts_root: Hash;
  base_fee_per_gas: Wei | null; extra_data: string; nonce: string | null; mix_hash: Hash | null;
  confirmations: number;
}

interface TxSummary {
  hash: Hash; block_number: number; timestamp: string; tx_index: number;   // all three null only for status "pending"
  from: AddressRef; to: AddressRef | null; created_contract: AddressRef | null;
  value: Wei; fee: Wei; status: "ok" | "failed" | "pending";
  method: string | null;             // decoded name when the ABI is known, else 4-byte id, null for plain transfers
  type: number;
}
interface DecodedParam { name: string; type: string; value: unknown; indexed?: boolean }
interface Tx extends TxSummary {
  block_hash: Hash | null; nonce: number; gas: number; gas_used: number | null;
  gas_price: Wei | null; effective_gas_price: Wei | null;
  max_fee_per_gas: Wei | null; max_priority_fee_per_gas: Wei | null;
  input: string; method_id: string | null;
  decoded_input: { method: string; signature: string; params: DecodedParam[] } | null;
  error: string | null;              // revert reason or trace error
  confirmations: number;
  token_transfers_count: number; internal_txs_count: number; logs_count: number;
  indexed: boolean;                  // false = read straight from the node, not in the DB yet
}

interface TokenTransfer {
  tx_hash: Hash; block_number: number; log_index: number; batch_index: number; timestamp: string;
  token: TokenRef; from: AddressRef; to: AddressRef;
  amount: string | null;             // raw integer string (ERC-20/1155); null for ERC-721
  token_id: string | null;           // ERC-721/1155
  method: string | null;             // the tx's method, for context
}
interface TokenRef { address: Addr; name: string | null; symbol: string | null; decimals: number | null; type: TokenType }

interface InternalTx {
  tx_hash: Hash; block_number: number; trace_address: string; depth: number;
  call_type: "CALL" | "STATICCALL" | "DELEGATECALL" | "CALLCODE" | "CREATE" | "CREATE2" | "SELFDESTRUCT";
  from: AddressRef; to: AddressRef | null; created_contract: AddressRef | null;
  value: Wei; gas: number | null; gas_used: number | null; error: string | null; timestamp: string;
}

interface Log {
  tx_hash: Hash; block_number: number; log_index: number; address: AddressRef;
  topics: (Hash | null)[]; data: string;
  decoded: { event: string; signature: string; params: DecodedParam[] } | null;
}

interface AddressDetail {
  hash: Addr; name: string | null; balance: Wei; balance_block: number | null; nonce: number | null;
  is_contract: boolean; is_verified: boolean;
  creator: AddressRef | null; creation_tx: Hash | null; creation_block: number | null;
  token: Token | null;               // when this address is a token contract
  implementation: AddressRef | null; // proxy target (EIP-1967 or T-REX authority)
  proxy_type: "eip1967" | "eip1967-beacon" | "trex" | null;
  tx_count: number; token_transfers_count: number; internal_txs_count: number;
  token_holdings_count: number; first_block: number | null; last_block: number | null;
}

interface Token extends TokenRef {
  is_erc3643: boolean; total_supply: string | null;
  holders_count: number; transfers_count: number; first_block: number;
}
interface TokenHolder { address: AddressRef; balance: string; token_id: string | null; share_pct: number | null }
interface TokenHolding { token: Token; balance: string; token_id: string | null }
interface BalancePoint { block_number: number; timestamp: string; balance: Wei; delta: Wei }

interface SmartContract {
  is_verified: boolean;
  is_fully_verified: boolean; is_partially_verified: boolean;
  match_type: "full" | "partial" | "similar" | "imported" | null;
  name: string | null; file_path: string | null; compiler_version: string | null;
  language: string | null; evm_version: string | null;
  optimization_enabled: boolean | null; optimization_runs: number | null; via_ir: boolean | null;
  license_type: string | null; verified_at: string | null; source_address: Addr | null;
  abi: unknown[] | null;
  source_code: string | null;        // the main file
  additional_sources: { file_path: string; source_code: string }[];
  constructor_args: string | null;
  deployed_bytecode: string | null; creation_bytecode: string | null;
  implementation: AddressRef | null; proxy_type: AddressDetail["proxy_type"];
  implementation_abi: unknown[] | null;  // so a proxy page can read through to the implementation
}

interface VerifiedContractRow { address: AddressRef; name: string; compiler_version: string; match_type: string; verified_at: string; tx_count: number }

interface SearchResult {
  type: "block" | "transaction" | "address" | "contract" | "token";
  // block: number + hash; transaction: hash; address/contract/token: address (+ name/symbol)
  number?: number; hash?: string; address?: Addr; name?: string | null; symbol?: string | null;
}

interface VerificationStatus { status: "none" | "pending" | "running" | "success" | "failed"; message: string | null; updated_at: string | null }
```

### Endpoints

| Method + path | Returns |
|---|---|
| `GET /api/health` | `{"status":"ok"}` |
| `GET /api/v2/config` | `{chain_id, network_name, native_symbol, public_rpc_url, explorer_url}` |
| `GET /api/v2/stats` | `Stats` |
| `GET /api/v2/stats/charts/transactions?days=14` | `{"items":[{"date":"2026-11-24","transactions":12}, ...]}`: transactions per UTC day, oldest first, every day of the window present (zeros included); `days` 1..90, default 14 |
| `GET /api/v2/signatures/functions?selectors=0xa9059cbb,...` | `{"0xa9059cbb":["transfer(address,uint256)"], ...}` (at most 50 selectors) |
| `GET /api/v2/signatures/events?topics=0xddf252ad...,...` | the same for event topics (32 bytes), full canonical event signatures (at most 50) |
| `GET /api/v2/signatures/errors?selectors=0x08c379a0,...` | the same for custom error selectors (at most 50) |
| `GET /api/v2/blocks` | `Page<BlockSummary>` newest first |
| `GET /api/v2/blocks/{number or hash}` | `Block` |
| `GET /api/v2/blocks/{n}/transactions` | `Page<TxSummary>` |
| `GET /api/v2/transactions` | `Page<TxSummary>` newest first |
| `GET /api/v2/transactions/{hash}` | `Tx` (falls back to the node when not indexed yet; `indexed:false`) |
| `GET /api/v2/transactions/{hash}/token-transfers` | `Page<TokenTransfer>` |
| `GET /api/v2/transactions/{hash}/internal-transactions` | `Page<InternalTx>` (depth >= 1 only) |
| `GET /api/v2/transactions/{hash}/logs` | `Page<Log>` |
| `GET /api/v2/transactions/{hash}/raw` | `{tx, receipt, trace}` the node's raw JSON |
| `GET /api/v2/addresses/{addr}` | `AddressDetail` (an address never seen still answers, balance read from the node) |
| `GET /api/v2/addresses/{addr}/transactions` | `Page<TxSummary>` (from, to or created) |
| `GET /api/v2/addresses/{addr}/token-transfers` | `Page<TokenTransfer>` (`?token=` filter) |
| `GET /api/v2/addresses/{addr}/internal-transactions` | `Page<InternalTx>` |
| `GET /api/v2/addresses/{addr}/tokens` | `Page<TokenHolding>` |
| `GET /api/v2/addresses/{addr}/logs` | `Page<Log>` (logs this contract emitted) |
| `GET /api/v2/addresses/{addr}/coin-balance-history` | `Page<BalancePoint>` |
| `GET /api/v2/tokens` | `Page<Token>` (`?q=` name/symbol, `?type=`), by holders desc |
| `GET /api/v2/tokens/{addr}` | `Token` |
| `GET /api/v2/tokens/{addr}/transfers` | `Page<TokenTransfer>` (`?token_id=`) |
| `GET /api/v2/tokens/{addr}/holders` | `Page<TokenHolder>` (`?token_id=`) by balance desc |
| `GET /api/v2/smart-contracts` | `Page<VerifiedContractRow>` (`?q=`) |
| `GET /api/v2/smart-contracts/{addr}` | `SmartContract` (Blockscout-compatible `is_verified`) |
| `POST /api/v2/smart-contracts/{addr}/verification/via/standard-input` | multipart: `compiler_version`, `license_type`, `autodetect_constructor_args`, optional `contract_name`, `files[0]` = standard JSON. 200 `{"message":"Smart-contract verification started"}`; 400 `{"message":"Address is not a smart-contract"}` |
| `GET /api/v2/smart-contracts/{addr}/verification/status` | `VerificationStatus` |
| `POST /api/v2/smart-contracts/{addr}/call` | body `{"data":"0x...","from":"0x... optional"}` -> `{"result":"0x..."}` or `{"error":"..."}`. `eth_call` at latest, nothing else |
| `GET /api/v2/compilers` | `{"versions":[...]}` (from the verifier) |
| `GET /api/v2/search?q=` | `SearchResult[]` (max 20): block number, tx hash, block hash, address, token name/symbol, contract name |
| `GET /api/v2/stream` | `text/event-stream`: `event: block` + `data: BlockSummary` on every newly indexed block (from `LISTEN explorer_events`) |

Decoding uses verified ABIs (the implementation's ABI for a proxy), and a
built-in table for the common token events and methods (Transfer, Approval,
TransferSingle, TransferBatch, transfer, transferFrom, approve, mint, burn).

Details the types leave open (as built):

- A transaction read through from the node before it is mined (`status:
  "pending"`) has `block_number`, `timestamp` and `tx_index` null; once mined
  but not indexed they are filled and `indexed` is false. Its sub-lists
  (token transfers, internal txs, logs) are empty pages until it is indexed.
- `Log.topics` always has 4 entries, null-padded (as Blockscout).
- `InternalTx`: for a CREATE/CREATE2 frame `to` is null and the new address
  is `created_contract` (null when the creation failed or was reverted).
  `error` is the revert reason when there is one, else the trace error.
- `TxSummary.method` in lists uses the called contract's verified ABI, then
  every verified ABI's selectors, then the built-in table, else the 4-byte id.
  `Tx` (detail) also follows a proxy to its implementation's ABI.
- Decoded values: addresses EIP-55, integers decimal strings, bytes 0x hex,
  tuples objects keyed by component name, arrays arrays. An indexed dynamic
  value (string, bytes, array) is its topic hash.
- `SmartContract` for a `similar` row takes `is_fully_verified` /
  `is_partially_verified` from the source contract's match type. An address
  without a verification (a contract, or an address with no code at all)
  answers 200 with `is_verified:false`, `deployed_bytecode` null when there is
  no code; only an invalid address is 404.
- `Block.confirmations` and `Tx.confirmations` count the block itself: the
  newest indexed block has 1.
- `TxSummary.method` is null for a plain value transfer (empty input) and
  for a contract creation.
- `VerificationStatus` is the latest job (the submission inserts it as
  `pending` before answering, so a poll right after never says `none`);
  without a job, a verified address
  answers `success` (for similar or imported rows), anything else `none`.
- Verification submissions: `compiler_version` must look like
  `v0.8.28+commit.7893614a`; `files[0]` must be a standard JSON with
  `sources`; over 20 MiB is 413. A partial match never replaces a full one.
- `POST .../call`: a revert is 200 `{"error":"execution reverted: <reason>"}`
  (the reason decoded from `Error(string)` revert data when the node leaves it
  out of its message; a custom error keeps the node's text); bad input 400
  `{"message":...}`; node unreachable 502. `GET /compilers`: 502 while the verifier is down.
- Holders: ERC-20 by balance with `share_pct` of `total_supply`; ERC-721
  grouped per holder (`balance` = ids held, `token_id` null), or the id's owner
  with `?token_id=`; ERC-1155 one row per (holder, id), `share_pct` only with
  `?token_id=` (share of that id's supply).
- Unknown paths answer JSON 404, a known path with the wrong method JSON 405
  with `Allow`. The stream allows 5 open streams per IP (429 beyond) and sends
  a `: ping` comment every 25 s.
- Signatures: what the website's decoder would otherwise ask 4byte or
  sourcify (no third-party calls at runtime). Canonical text signatures (no
  parameter names, no `indexed`, tuples spelled `(...)`), several per key
  when selectors collide, sorted. An unknown key is absent from the object;
  a malformed key or more than 50 is 400. Sources: every verified
  contract's ABI (functions, events, errors), merged with a list compiled
  into the binary, `internal/sigs/bundled.txt`: the common standards in
  `internal/sigs/standards.txt` (ERC-20/721/1155/2612/3643/4337,
  Multicall/Multicall3, Safe, ERC-1967/UUPS, Ownable/AccessControl,
  TimelockController, EIP-7821, OpenZeppelin errors), plus, when you
  regenerate it with them, the ABIs of your own contracts (a Foundry project
  with `-contracts`, an existing forge `out/` with `-forge-out`) and the JSON
  ABI literals in Go sources (`-go-abis`, every version in their git history
  too, for contract versions still on chain but gone from the sources). The
  file's header names every source read and how much came from each.
  Regenerate it from `backend/` with `go run ./internal/sigs/gen [flags]`
  (forge builds into a temp directory; your project is only read). A test fails
  when the file is not exactly the generator's output. The merged index is
  rebuilt when `verified_contracts` changes, looked at no more often than
  every 30 s. The API's own decoder uses the bundled list too, after the
  verified and built-in ABIs, so `method` and `decoded_input` (with unnamed
  parameters) also work for unverified contracts calling known functions.
- Chart: counted from `blocks.tx_count` by UTC day of the block timestamp;
  cached 30 s and dropped on every new block.
- Search: digits = block number (falls back to names when no such block), a
  66-char hash = transaction, then block hash, then the node; a 40-hex address
  = `token`, `contract` or `address` (always answered); anything else matches
  token names/symbols, verified contract names and labels.

## Website routes (Blockscout-compatible paths)

`/`, `/blocks`, `/block/$id` (number or hash), `/txs`, `/tx/$hash`,
`/address/$addr` (`?tab=txs|token_transfers|internal_txns|tokens|logs|contract|coin_balance_history`),
`/tokens`, `/token/$addr` (`?tab=token_transfers|holders|contract`),
`/token/$addr/instance/$id`, `/verified-contracts`, `/contract-verification`,
`/address/$addr/contract-verification`, `/search-results?q=`, and a 404.

## Security

- Read-only API. The only writes are verification jobs and the call proxy
  (`eth_call` only, `data` max 64 KB).
- Per-IP limits (see env). 429 with `Retry-After`.
- No CORS headers: the site and the API share an origin.
- Health answers minimal bodies through a proxy, like the other engines.
- Verifier and indexer have no host port; only the API is published, and only
  on 127.0.0.1.
- The verifier runs only compilers whose sha256 it has checked: the bundled
  ones against `compilers.tsv` (pinned in the repo), downloaded ones against
  the official list. By default (`SOLC_DOWNLOAD=off`) it makes no network
  request at all; downloads, when turned on, come only from `SOLC_BIN_BASE`.

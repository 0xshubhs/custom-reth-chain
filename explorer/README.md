# Explorer

A self-hosted block explorer for any EVM chain: an indexer that copies the chain into Postgres,
an HTTP API, a Solidity verifier and a website. Light by design: one Go binary for the indexer and
the API, the indexer sleeps until the node announces a block, and nothing calls a third party at
runtime (signatures and compilers are bundled or checksum-pinned).

```
                    newHeads (ws) / poll (http)
   EVM node  ───────────────────────────────►  explorer-indexer ──┐
     ▲                                                             │ one DB transaction per block
     │ eth_call, eth_getCode                                       ▼
     └──────────────────── explorer-api ◄────────────────────── explorer-db (Postgres)
                              ▲     │ POST /verify
           /api/ (nginx)      │     ▼
   browser ──► explorer-web ──┘   explorer-verifier (solc, sha256-pinned)
```

## Run it

With the bundled node, from the repository root: `docker compose up -d --build`.

Against any other chain:

```bash
CHAIN_RPC_URL=https://rpc.example.org CHAIN_WS_URL=wss://rpc.example.org \
NETWORK_NAME="Example" NATIVE_SYMBOL=EXM \
  docker compose -f explorer/compose.yaml up -d --build
```

The chain id is read from the node (`CHAIN_ID=0`). Without `CHAIN_WS_URL` the indexer polls
`eth_blockNumber`. With `TRACES=auto` it indexes internal calls when the node answers
`debug_traceBlockByNumber` (callTracer) and skips them otherwise; `required` refuses to run
without them. Every setting is in [../.env.example](../.env.example).

What the indexer guarantees: every block, transaction, receipt, log and trace with the node's raw
JSON; the cursor written in the same transaction as the block (a crash leaves all of a block or
none of it); reorgs rolled back by parent hash. Details: [docs/design.md](docs/design.md).

## Layout

```
backend/
  cmd/explorer          one binary: `explorer api`, `explorer indexer`, `explorer admin ...`
  cmd/verifier          the Solidity verifier service
  cmd/blockscout-import copies verified contracts and labels out of a Blockscout database
  internal/indexer      block fetch, commit, reorg handling, token transfers, traces
  internal/api          the HTTP API (Blockscout-compatible paths)
  internal/db           schema and migrations (applied on start)
  internal/sigs         bundled function/event/error signatures (standards.txt + generator)
  internal/verifier     standard-JSON compile, bytecode match (immutables, metadata)
  Dockerfile            explorer image; verifier.Dockerfile for the verifier
frontend/
  src/                  React + TanStack Router + TanStack Query
  deploy/               container nginx (container.conf); host reference + CSP (nginx.conf)
  Dockerfile            builds the site and serves it with unprivileged nginx
docs/design.md
compose.yaml
```

## Operating it

```bash
docker compose exec explorer-indexer /app/explorer admin status     # lag, counts, problems
docker compose exec explorer-indexer /app/explorer admin check      # every stored hash vs the node
docker compose exec explorer-indexer /app/explorer admin reindex --from N --yes
```

Verify a contract from the website (the address page's Contract tab), or with Blockscout's v2
standard-input endpoint from a script:

```bash
forge verify-contract <address> src/Token.sol:Token --show-standard-json-input > input.json
curl -F compiler_version=v0.8.28+commit.7893614a -F contract_name=src/Token.sol:Token \
     -F files[0]=@input.json \
     http://localhost:4000/api/v2/smart-contracts/<address>/verification/via/standard-input
```

Your own contracts' calls and events decode by name before they are verified when their ABIs are
in the bundled list: `cd backend && go run ./internal/sigs/gen -contracts ../../path/to/foundry`.

## Development

```bash
cd backend  && go test ./...      # unit tests; end-to-end against anvil: see internal/itest
cd frontend && bun install && bun run dev     # http://localhost:3001, proxies /api to :4000
cd frontend && bun run build                  # typecheck + build: the check CI runs
```

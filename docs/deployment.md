# Deploying a network

From an empty server to a live chain with its explorer and public RPC.

## 1. Server

- Linux with Docker Engine 24+ and the compose plugin (`docker compose version`).
- Disk: the node runs as an archive (every historical state kept, which the explorer and tracing
  need). Plan for growth: a busy chain writes gigabytes a month.
- Firewall: allow 22 (or your admin access) and 443 only. Everything the stack publishes binds to
  127.0.0.1 by default. Open 30303 tcp+udp only between your own signer nodes.

## 2. Network identity

```bash
git clone <this repository> && cd custom-reth-chain
scripts/setup-network.sh
```

It asks for:

| Question | What to answer |
|---|---|
| Network name, coin symbol | shown by the explorer and in wallets |
| Chain id | an id no public network uses; check chainlist.org |
| Eager sealing | yes: blocks only when transactions arrive (light). no: one every block period |
| Block period, gas limit | minimum block spacing; starting gas limit (ChainConfig can change it later) |
| This node's signer key | generate a new one, or import a file |
| Other signers | the addresses of the other signer nodes (each keeps its own key) |
| Chain admin | a Safe multisig or hardware wallet address; it governs signers and settings |
| Genesis balances | `address=amount`, e.g. `0xabc...=1000000eth` |
| Explorer and RPC URLs | the public https addresses you will serve them on |

Then it checks the result with the node (`meowchain --network ... --check-network`), shows the
genesis hash, and writes:

| File | Keep it |
|---|---|
| `network/network.toml` | forever; it defines the genesis. Never edit it after the chain starts |
| `secrets/signer.key` (600) | off-server too; without it this node cannot seal |
| `.env` (600) | holds the explorer's database password and hook token |

The node refuses to start a network whose signers, admin or balances are public development
accounts, whose admin is the reserved placeholder address, or whose chain id belongs to a public
network.

## 3. Start

```bash
docker compose up -d --build
docker compose ps                    # every service healthy
docker compose logs -f chain         # "Network file: chain id ..., N signer(s), admin 0x..."
```

`.env` sets `COMPOSE_FILE=compose.yaml:compose.live.yaml`, so plain `docker compose` commands use
the live configuration.

## 4. TLS and domains

Put a reverse proxy with TLS in front (nginx, Caddy, a cloud load balancer):

| Public name | Proxy to |
|---|---|
| `explorer.example.org` | `127.0.0.1:3000` (the website; it proxies `/api/` itself) |
| `rpc.example.org` | `127.0.0.1:8548` (the gateway; HTTP and WebSocket) |

Pass `X-Forwarded-For` and `X-Real-IP`. Set `EXPLORER_PUBLIC_URL`, `PUBLIC_RPC_URL`,
`RPC_GATEWAY_ALLOWED_ORIGINS` and `CHAIN_CORS` in `.env` to the https names, then rebuild the
website (`docker compose up -d --build explorer-web`): the URLs are baked into it.

Never expose 8545/8546: that is the node's full RPC (debug, trace, txpool, admin of the chain's
own namespaces). The gateway on 8548 is the public entrance.

## 5. More signers

Each extra signer node gets the same `network/network.toml`, its own `secrets/signer.key`, and
`--bootnodes` pointing at the others (remove `--disable-discovery` from the chain command for a
multi-node network). Signers listed in network.toml are signers from block 0. To add or remove one
later, the chain admin calls SignerRegistry; the change takes effect at the next epoch block.

With N signers the chain keeps sealing while more than N/2 are online.

## 6. Backups

| What | How |
|---|---|
| Chain data | `docker compose stop chain`, copy the `chain-data` volume, start again. The node shuts down cleanly on stop. |
| Explorer database | `docker compose exec explorer-db pg_dump -U explorer explorer > explorer.sql` (or reindex from the node: it is derived data) |
| `network/network.toml`, `secrets/signer.key`, `.env` | once, off-server, encrypted |

Prove a restored or moved node holds the same chain with `tools/chain-fingerprint`:
`dump` both, then `compare`.

## 7. Upgrades

- Explorer, gateway: `git pull && docker compose up -d --build <service>`. The explorer database
  migrates itself.
- Node: reth is pinned. Before deploying a new node build, run it against a **copy** of the
  datadir and compare fingerprints. Rollback is the datadir backup plus the old image.
- Hardforks (Osaka): every node restarts with the same `--osaka-time` before that time.

## Checklist

- [ ] Chain id unique (chainlist.org)
- [ ] Chain admin is a multisig you control; test one governance call on a rehearsal chain first
- [ ] network.toml, signer key and .env backed up off the server
- [ ] Only 443 (and admin access) open; 8545/8546 private
- [ ] TLS proxy in front of 3000 and 8548; public URLs set in .env; website rebuilt
- [ ] `EXPLORER_DB_PASSWORD` is not the default (the script generates one)
- [ ] Scheduled backups of the chain-data volume
- [ ] Monitoring: `docker compose ps` health, `explorer admin status` (lag), disk space

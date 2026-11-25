#!/usr/bin/env bash
# Sets up a real network: asks for its identity, signers, chain admin and
# genesis balances, then writes
#
#   network/network.toml   the genesis definition (keep it forever, with backups)
#   secrets/signer.key     this node's block-signing key (mode 600)
#   .env                   COMPOSE_FILE=compose.yaml:compose.live.yaml + settings
#
# and checks the result with the node itself (`meowchain --check-network`),
# which refuses the public development keys, a placeholder admin and chain ids
# that belong to public networks. Nothing starts until you run
# `docker compose up -d`.
#
# Usage: scripts/setup-network.sh [--force]
#   --force  overwrite an existing network/network.toml (only before the chain
#            has ever started: the file decides the genesis block).
#
# Answers can be piped in (one per line) for automated rehearsals.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

FORCE=0
for arg in "$@"; do
  case "$arg" in
    --force) FORCE=1 ;;
    -h|--help) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

NETWORK_FILE=network/network.toml
KEY_FILE=secrets/signer.key

say()  { printf '%s\n' "$*"; }
section() { printf '\n== %s\n' "$*"; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

# ask VAR "Question" "default"
ask() {
  local __var=$1 __q=$2 __def=${3:-} __ans
  if [[ -n $__def ]]; then printf '%s [%s]: ' "$__q" "$__def"; else printf '%s: ' "$__q"; fi
  IFS= read -r __ans || true
  __ans=${__ans:-$__def}
  printf -v "$__var" '%s' "$__ans"
  [[ -t 0 ]] || printf '%s\n' "$__ans"
}

is_address() { [[ $1 =~ ^0x[0-9a-fA-F]{40}$ ]]; }

command -v docker >/dev/null || die "docker is required"
command -v openssl >/dev/null || die "openssl is required (to generate a key)"

if [[ -f $NETWORK_FILE && $FORCE -ne 1 ]]; then
  die "$NETWORK_FILE already exists. It defines the genesis of a chain that may be running.
Re-run with --force only if that chain has never started (and remove its chain-data volume)."
fi

# The node image does the checks and derives addresses; build it if missing.
meow() {
  docker compose -f compose.yaml run --rm --no-deps -T \
    -v "$ROOT/network:/network:ro" -v "$ROOT/secrets:/secrets:ro" \
    --entrypoint meowchain chain "$@"
}
if ! docker image inspect "${CHAIN_IMAGE:-meowchain/node:local}" >/dev/null 2>&1; then
  section "Building the node image (first time only, several minutes)"
  docker compose -f compose.yaml build chain
fi

say "This writes the files a real network starts from. Defaults are in [brackets]."

section "Network identity"
ask NETWORK_NAME "Network name (shown by the explorer and wallets)" "Meowchain"
ask NATIVE_SYMBOL "Native coin symbol" "ETH"
SUGGESTED_ID=$(( (RANDOM << 15 | RANDOM) % 900000000 + 100000000 ))
say "Pick a chain id no public network uses (check chainlist.org)."
ask CHAIN_ID "Chain id" "$SUGGESTED_ID"
[[ $CHAIN_ID =~ ^[0-9]+$ ]] || die "chain id must be a number"

section "Blocks"
ask EAGER "Seal blocks only when transactions arrive? (lightweight; no blocks while idle) [y/n]" "y"
ask BLOCK_PERIOD "Block period in seconds (minimum spacing; the interval when not eager)" "2"
[[ $BLOCK_PERIOD =~ ^[1-9][0-9]*$ ]] || die "block period must be a whole number of seconds >= 1"
ask GAS_LIMIT "Block gas limit" "1000000000"
[[ $GAS_LIMIT =~ ^[0-9]+$ ]] || die "gas limit must be a number"

section "This node's signer key"
say "Every block is sealed by one of the network's signers. This node needs its own key."
mkdir -p secrets network
chmod 700 secrets
if [[ -f $KEY_FILE ]]; then
  ask KEYCHOICE "Use the existing $KEY_FILE? [y/n]" "y"
else
  KEYCHOICE=n
fi
if [[ ! $KEYCHOICE =~ ^[yY] ]]; then
  ask KEYSRC "Generate a new key, or copy one from a file? [generate/path]" "generate"
  umask 077
  if [[ $KEYSRC == generate ]]; then
    openssl rand -hex 32 > "$KEY_FILE"
    say "Generated $KEY_FILE."
  else
    [[ -f $KEYSRC ]] || die "no such file: $KEYSRC"
    tr -d ' \r\n' < "$KEYSRC" | sed 's/^0x//' > "$KEY_FILE"
    printf '\n' >> "$KEY_FILE"
  fi
  chmod 600 "$KEY_FILE"
fi
NODE_SIGNER=$(meow --print-signer-address --signer-key-file /secrets/signer.key 2>/dev/null | tail -1 | tr -d '\r')
is_address "$NODE_SIGNER" || die "could not read an address from $KEY_FILE"
say "This node signs as $NODE_SIGNER"

section "Other signers"
say "Addresses of the other signer nodes, comma separated (each keeps its own key)."
say "With N signers, blocks keep coming while more than N/2 are online. Enter for none."
ask OTHER_SIGNERS "Other signer addresses" ""
SIGNERS=("$NODE_SIGNER")
IFS=',' read -r -a extra <<< "${OTHER_SIGNERS// /}"
for a in "${extra[@]:-}"; do
  [[ -z $a ]] && continue
  is_address "$a" || die "not an address: $a"
  SIGNERS+=("$a")
done

section "Chain admin"
say "The admin governs ChainConfig, SignerRegistry, Treasury, the Timelock and the"
say "block-reward proxy: it adds and removes signers and changes the gas limit."
say "Use a multisig (a Safe) or a hardware wallet. Never a hot key on this server."
ask GOVERNANCE "Chain admin address" ""
is_address "$GOVERNANCE" || die "not an address: $GOVERNANCE"

section "Genesis balances"
say "Accounts that start with coins: address=amount (e.g. 0xabc...=1000000eth). Empty line to finish."
PREFUND=()
while :; do
  ask LINE "Balance" ""
  [[ -z $LINE ]] && break
  addr=${LINE%%=*}; amount=${LINE#*=}
  addr=${addr// /}; amount=${amount// /}
  is_address "$addr" || die "not an address: $addr"
  [[ -n $amount && $amount != "$LINE" ]] || die "write it as address=amount"
  PREFUND+=("\"$addr\" = \"$amount\"")
done

section "Public addresses"
ask EXPLORER_PUBLIC_URL "Explorer URL" "http://localhost:3000"
ask PUBLIC_RPC_URL "Public RPC URL" "http://localhost:8548"

# ---------------------------------------------------------------------------
quote_list() { local out="" a; for a in "$@"; do out+="${out:+, }\"$a\""; done; printf '[%s]' "$out"; }
TMP_NETWORK=$(mktemp "network/.network.toml.XXXXXX")
trap 'rm -f "$TMP_NETWORK"' EXIT
{
  printf '# %s: the genesis definition. Written by scripts/setup-network.sh on %s.\n' "$NETWORK_NAME" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf '# Never edit it after the chain has started: it decides the genesis block, and\n'
  printf '# the node refuses a datadir whose genesis differs. Back it up with the datadir.\n'
  printf '# Signers change on chain (SignerRegistry, by the admin), not here.\n\n'
  printf 'chain_id     = %s\n' "$CHAIN_ID"
  printf 'block_period = %s\n' "$BLOCK_PERIOD"
  printf 'gas_limit    = %s\n' "$GAS_LIMIT"
  printf 'epoch        = 30000\n'
  printf 'signers      = %s\n' "$(quote_list "${SIGNERS[@]}")"
  printf 'governance   = "%s"\n' "$GOVERNANCE"
  printf 'vanity       = "%s"\n' "${NETWORK_NAME:0:32}"
  if ((${#PREFUND[@]})); then
    printf '\n[prefund]\n'
    printf '%s\n' "${PREFUND[@]}"
  fi
} > "$TMP_NETWORK"

section "Checking with the node"
set +e
CHECK=$(meow --network "/network/$(basename "$TMP_NETWORK")" --check-network 2>&1)
CHECK_STATUS=$?
set -e
printf '%s\n' "$CHECK" | sed 's/\x1b\[[0-9;]*m//g' \
  | grep -E 'network file|^(chain_id|block_period|gas_limit|admin|signer|prefund|genesis_hash) |^Error' || true
((CHECK_STATUS == 0)) || die "the network file has problems (listed above); nothing was written"

ask CONFIRM "Write these files? [y/n]" "y"
[[ $CONFIRM =~ ^[yY] ]] || { say "Nothing written."; exit 1; }
mv "$TMP_NETWORK" "$NETWORK_FILE"
trap - EXIT
chmod 644 "$NETWORK_FILE"

# .env: start from the example, set what we know, generate the secrets.
if [[ -f .env ]]; then
  cp .env ".env.bak.$(date +%Y%m%d%H%M%S)"
fi
DB_PASSWORD=$(openssl rand -hex 24)
HOOK_TOKEN=$(openssl rand -hex 24)
EAGER_BOOL=false; [[ $EAGER =~ ^[yY] ]] && EAGER_BOOL=true
ORIGIN=$(printf '%s' "$EXPLORER_PUBLIC_URL" | sed -E 's#^(https?://[^/]+).*#\1#')
setenv() { # setenv KEY VALUE: replace KEY=... in .env
  local k=$1 v=$2
  if grep -q "^$k=" .env; then
    awk -v k="$k" -v v="$v" 'BEGIN{FS=OFS="="} $1==k{$0=k"="v} {print}' .env > .env.tmp && mv .env.tmp .env
  else
    printf '%s=%s\n' "$k" "$v" >> .env
  fi
}
cp .env.example .env
setenv COMPOSE_FILE "compose.yaml:compose.live.yaml"
setenv NETWORK_NAME "$NETWORK_NAME"
setenv NATIVE_SYMBOL "$NATIVE_SYMBOL"
setenv CHAIN_ID "$CHAIN_ID"
setenv CHAIN_EAGER_MINING "$EAGER_BOOL"
setenv CHAIN_CORS "$ORIGIN"
setenv EXPLORER_PUBLIC_URL "$EXPLORER_PUBLIC_URL"
setenv PUBLIC_RPC_URL "$PUBLIC_RPC_URL"
setenv RPC_GATEWAY_ALLOWED_ORIGINS "$ORIGIN"
setenv EXPLORER_DB_PASSWORD "$DB_PASSWORD"
setenv INDEXER_HOOK_TOKEN "$HOOK_TOKEN"
chmod 600 .env

section "Done"
cat <<EOF
Written: $NETWORK_FILE, $KEY_FILE (600), .env (600; the previous one, if any, as .env.bak.*)

Before you start it:
  1. Back up $NETWORK_FILE and $KEY_FILE somewhere off this server. Losing the
     key means this node can no longer seal; losing the network file means you
     cannot rebuild the node's genesis.
  2. Make sure the admin ($GOVERNANCE) is an account you control: a Safe
     multisig or a hardware wallet.
  3. Other signer nodes: give each the same network.toml and its own key.

Start it:
  docker compose up -d --build
  docker compose logs -f chain          # "Network file: chain id $CHAIN_ID ..."

Then (docs/deployment.md):
  - put a TLS proxy in front: explorer -> 127.0.0.1:3000, public RPC -> 127.0.0.1:8548
  - keep 8545/8546 (the node's full RPC) private to this host
  - back up the chain-data volume and the explorer database on a schedule
EOF

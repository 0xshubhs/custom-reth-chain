# Meowchain

Custom **Proof of Authority (POA)** blockchain built on [Reth](https://github.com/paradigmxyz/reth). Full Ethereum EVM compatibility — all hardforks through Prague — with POA consensus replacing the beacon chain.

**Chain ID:** 9323310 | **Block time:** 2s (dev) / 12s (prod) | **Gas limit:** 30M–60M | **Tests:** 194 passing

## Quick Start

```bash
# Build (fetches latest reth from main branch)
just build

# Run dev node (auto-mines, 20 prefunded accounts, 3 signers)
just dev

# Run tests
just test
```

## Architecture

```
meowchain (PoaNode)
  ├── Consensus:        PoaConsensus — validates headers, POA signatures, timing, gas, receipt root
  ├── Block Production: PoaPayloadBuilder — wraps EthereumPayloadBuilder + POA signing
  ├── Block Rewards:    EIP-1967 Miner Proxy (0x...1967) as coinbase → Treasury
  ├── Governance:       Gnosis Safe → ChainConfig / SignerRegistry / Treasury contracts
  ├── EVM:              Identical to Ethereum mainnet (sequential, all opcodes, precompiles)
  ├── Hardforks:        Frontier through Prague (all active at genesis block 0)
  ├── RPC:              HTTP (8545) + WS (8546) + meow_* custom namespace on 0.0.0.0
  └── Storage:          MDBX persistent database
```

## Directory Structure

```
custom-reth-chain-/
├── src/                        # Rust source (6,353 lines)
│   ├── main.rs                 # CLI, node launch, block monitoring
│   ├── node.rs                 # PoaNode — injects custom consensus + payload
│   ├── consensus.rs            # PoaConsensus — signature verification, header validation
│   ├── chainspec.rs            # PoaChainSpec — hardforks, POA config, signer rotation
│   ├── genesis.rs              # Genesis builder — all pre-deployed contracts
│   ├── payload.rs              # PoaPayloadBuilder — block signing pipeline
│   ├── onchain.rs              # StorageReader — reads ChainConfig/SignerRegistry contracts
│   ├── rpc.rs                  # meow_* RPC namespace
│   ├── signer.rs               # SignerManager + BlockSealer
│   └── bytecodes/              # 16 pre-compiled contract bytecodes (.bin/.hex)
├── genesis/                    # Genesis JSON files
│   ├── sample-genesis.json     # Dev genesis (chain ID 9323310, 37 alloc entries)
│   └── production-genesis.json # Production genesis (25 alloc entries, 5 signers)
├── genesis-contracts/          # Governance Solidity source
│   ├── ChainConfig.sol         # Dynamic gas limit, block time, contract size params
│   ├── SignerRegistry.sol      # On-chain POA signer management
│   └── Treasury.sol            # Block reward / fee distribution
├── Docker/                     # Docker artifacts
│   ├── Dockerfile
│   └── docker-compose.yml
├── scoutup-go-explorer/        # Blockscout explorer integration
├── signatures/                 # Contract ABI signatures (.json + .txt)
├── md/                         # Documentation
│   ├── Remaining.md            # Full status tracker + roadmap
│   ├── USAGE.md                # Usage guide (RPC, CLI, accounts, config)
│   └── main.md                 # Strategy notes
├── CLAUDE.md                   # AI assistant context file
└── Justfile                    # Build automation
```

## Pre-deployed Genesis Contracts

No deployment needed — all live at block 0.

| Contract | Address | Purpose |
|----------|---------|---------|
| EIP-1967 Miner Proxy | `0x0000000000000000000000000000000000001967` | Block reward coinbase |
| ERC-4337 EntryPoint v0.7 | `0x0000000071727De22E5E9d8BAf0edAc6f37da032` | Account abstraction |
| WETH9 | `0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2` | Wrapped ETH |
| Multicall3 | `0xcA11bde05977b3631167028862bE2a173976CA11` | Batch reads |
| CREATE2 Deployer | `0x4e59b44847b379578588920cA78FbF26c0B4956C` | Deterministic deploys |
| SimpleAccountFactory | `0x9406Cc6185a346906296840746125a0E44976454` | ERC-4337 wallet factory |
| ChainConfig | `0x00000000000000000000000000000000C04F1600` | Governance: gas, block time |
| SignerRegistry | `0x000000000000000000000000000000005164EB00` | Governance: signer list |
| Treasury | `0x0000000000000000000000000000000007EA5B00` | Governance: fee splits |
| Governance Safe (reserved) | `0x000000000000000000000000000000006F5AFE00` | Gnosis Safe multisig admin |
| Safe Singleton v1.3.0 | `0xd9Db270c1B5E3Bd161E8c8503c55cEABeE709552` | Gnosis Safe core |
| Safe Proxy Factory | `0xa6B71E26C5e0845f74c812102Ca7114b6a896AB2` | Gnosis Safe |
| Safe Fallback Handler | `0xf48f2B2d2a534e402487b3ee7C18c33Aec0Fe5e4` | Gnosis Safe |
| Safe MultiSend | `0xA238CBeb142c10Ef7Ad8442C6D1f9E89e07e7761` | Gnosis Safe |

System contracts (EIP-4788, EIP-2935, EIP-7002, EIP-7251) also deployed at genesis.

## Building & Running

```bash
# Build release binary (cargo update first, fetches latest reth)
just build

# Build without updating deps
just build-fast

# Dev node (2s blocks, relaxed consensus)
just dev

# Production node (12s blocks, strict POA signatures)
just run-production

# Custom flags
just run-custom -- --block-time 1 --gas-limit 300000000 --eager-mining

# With signer key
SIGNER_KEY=ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 just dev

# Tests
just test

# Docker
docker build -f Docker/Dockerfile -t meowchain .
docker compose -f Docker/docker-compose.yml up
```

## CLI Reference

```
--production           Strict POA signature enforcement
--no-dev               Disable auto-mining dev mode
--block-time <N>       Block interval in seconds
--gas-limit <N>        Block gas limit override
--eager-mining         Mine on tx arrival (not just interval)
--signer-key <HEX>     64-char hex private key for block signing
--datadir <PATH>       Database directory
--http-addr / --http-port   HTTP RPC bind (default: 0.0.0.0:8545)
--ws-addr / --ws-port       WS RPC bind (default: 0.0.0.0:8546)
```

## RPC

```bash
# Standard Ethereum JSON-RPC on :8545
curl -X POST http://localhost:8545 -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'

# Custom meow_* namespace
curl -X POST http://localhost:8545 -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"meow_chainConfig","params":[],"id":1}'
curl -X POST http://localhost:8545 -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"meow_signers","params":[],"id":1}'
curl -X POST http://localhost:8545 -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"meow_nodeInfo","params":[],"id":1}'
```

## Tooling Integration

**MetaMask:** RPC URL `http://localhost:8545`, Chain ID `9323310`

**Hardhat:**
```typescript
networks: { meowchain: { url: "http://localhost:8545", chainId: 9323310 } }
```

**Foundry:**
```bash
forge script ... --rpc-url http://localhost:8545 --chain-id 9323310 --broadcast
cast block-number --rpc-url http://localhost:8545
```

## Dev Accounts (20 prefunded @ 10,000 ETH)

Mnemonic: `test test test test test test test test test test test junk`

| # | Address | Private Key |
|---|---------|-------------|
| 0 (signer) | `0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266` | `ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80` |
| 1 (signer) | `0x70997970C51812dc3A010C7d01b50e0d17dc79C8` | `59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d` |
| 2 (signer) | `0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC` | `5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a` |
| 3–19 | see `src/genesis.rs:dev_accounts()` | Standard Foundry keys |

## Status

| Module | Tests | Status |
|--------|-------|--------|
| `consensus.rs` | 36 | Complete — signature verification, header validation, post-execution |
| `genesis.rs` | 31 | Complete — dev + production configs, all pre-deployed contracts |
| `onchain.rs` | 50+ | Complete — StateProviderStorageReader wired; gas limit + signers read from chain |
| `chainspec.rs` | 22 | Complete |
| `signer.rs` | 27 | Complete — in payload pipeline |
| `payload.rs` | 12 | Complete — signs blocks in pipeline |
| `rpc.rs` | 9 | Complete — meow_* namespace |
| `node.rs` | 8 | Complete — PoaEngineValidator bypasses alloy 32-byte extra_data limit |
| **Total** | **194** | **0 failed** |

**Next:** Performance engineering — parallel EVM (grevm), sub-second blocks, 300M+ gas limits, multi-node testing. See `md/Remaining.md` Section 12.

## License

MIT OR Apache-2.0

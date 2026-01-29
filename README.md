# Custom POA Node Example

This example demonstrates how to build a complete **Proof of Authority (POA)** blockchain using Reth that maintains full compatibility with Ethereum mainnet.

## Features

- ✅ **Full Ethereum EVM compatibility** - All smart contracts work identically to mainnet
- ✅ **All Ethereum hardforks enabled** - Shanghai, Cancun, Prague, and future upgrades
- ✅ **Multi-signer support** - Configure multiple authorized validators
- ✅ **Round-robin block production** - Fair signer rotation
- ✅ **Configurable block time** - Set your desired block interval
- ✅ **Epoch-based checkpoints** - Periodic signer list updates
- ✅ **Standard JSON-RPC APIs** - Compatible with all Ethereum tooling

## Quick Start

### 1. Run in Dev Mode (Single Node)

```bash
cd /path/to/reth
cargo run --release --example custom-poa-node -- --dev
```

This starts a local POA node with:
- 20 prefunded accounts (10,000 ETH each)
- 3 authorized signers
- 2-second block time
- All Ethereum hardforks enabled

### 2. Run with Custom Genesis

Create a `poa-genesis.json` file (see [sample-genesis.json](./sample-genesis.json)) and run:

```bash
cargo run --release --example custom-poa-node -- --chain ./poa-genesis.json
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                       POA Node Architecture                         │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│   ┌─────────────────┐     ┌─────────────────────────────────────┐  │
│   │  POA Consensus  │     │  Ethereum EVM                        │  │
│   │  ─────────────  │     │  ─────────────                       │  │
│   │  • Signer auth  │     │  • All opcodes (identical to mainnet)│  │
│   │  • Round-robin  │     │  • All precompiles                   │  │
│   │  • Epoch mgmt   │     │  • EIP-1559 base fee                 │  │
│   │  • Block timing │     │  • EIP-4844 blobs (if enabled)       │  │
│   └─────────────────┘     └─────────────────────────────────────┘  │
│                                                                      │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│   ┌─────────────────────────────────────────────────────────────┐  │
│   │                    Chain Specification                       │  │
│   │  ───────────────────────────────────────────────────────    │  │
│   │  Hardforks: Frontier → ... → London → Paris → Shanghai →   │  │
│   │             Cancun → Prague → [Future Upgrades]             │  │
│   │                                                              │  │
│   │  All hardforks enabled at genesis (block 0 / timestamp 0)   │  │
│   └─────────────────────────────────────────────────────────────┘  │
│                                                                      │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│   ┌───────────┐  ┌───────────┐  ┌───────────┐  ┌───────────────┐  │
│   │    P2P    │  │   RPC     │  │  Storage  │  │  Transaction  │  │
│   │ Networking│  │  Server   │  │  (MDBX)   │  │     Pool      │  │
│   └───────────┘  └───────────┘  └───────────┘  └───────────────┘  │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

## Genesis Configuration

### Sample Genesis JSON

```json
{
    "config": {
        "chainId": 31337,
        "homesteadBlock": 0,
        "eip150Block": 0,
        "eip155Block": 0,
        "eip158Block": 0,
        "byzantiumBlock": 0,
        "constantinopleBlock": 0,
        "petersburgBlock": 0,
        "istanbulBlock": 0,
        "berlinBlock": 0,
        "londonBlock": 0,
        "terminalTotalDifficulty": 0,
        "terminalTotalDifficultyPassed": true,
        "shanghaiTime": 0,
        "cancunTime": 0,
        "pragueTime": 0,
        "clique": {
            "period": 12,
            "epoch": 30000
        }
    },
    "nonce": "0x0",
    "timestamp": "0x0",
    "extraData": "0x0000000000000000000000000000000000000000000000000000000000000000<SIGNER_ADDRESSES>0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
    "gasLimit": "0x1c9c380",
    "difficulty": "0x1",
    "mixHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
    "coinbase": "0x0000000000000000000000000000000000000000",
    "alloc": {
        "0xYourAddress": {
            "balance": "0xD3C21BCECCEDA1000000"
        }
    },
    "baseFeePerGas": "0x3B9ACA00"
}
```

### Extra Data Format

The `extraData` field in POA blocks has a specific format:

```
[VANITY: 32 bytes][SIGNERS: N×20 bytes][SIGNATURE: 65 bytes]
```

- **VANITY**: 32 bytes of arbitrary data
- **SIGNERS**: List of authorized signer addresses (only in epoch blocks)
- **SIGNATURE**: Block producer's signature (zeros in genesis)

## Multi-Signer Setup

### Adding Signers

Configure multiple signers in your genesis:

```json
{
    "config": {
        "clique": {
            "period": 12,
            "epoch": 30000
        }
    },
    "extraData": "0x0000000000000000000000000000000000000000000000000000000000000000f39Fd6e51aad88F6F4ce6aB8827279cffFb9226670997970C51812dc3A010C7d01b50e0d17dc79C83C44CdDdB6a900fa2b585dd299e03d12FA4293BC0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
}
```

This example has 3 signers. They will produce blocks in round-robin order.

### Signer Rotation

- Block 0: Signer 1
- Block 1: Signer 2
- Block 2: Signer 3
- Block 3: Signer 1 (repeats)

### Difficulty Field

- **Difficulty 1**: In-turn signer (expected signer for this slot)
- **Difficulty 2**: Out-of-turn signer (backup if in-turn signer is unavailable)

## Keeping Sync with Mainnet Upgrades

To stay compatible with new Ethereum upgrades:

### 1. Update Hardfork Timestamps

When a new hardfork is announced for mainnet, add it to your chain spec:

```rust
// In chainspec.rs, add new hardforks:
(EthereumHardfork::Osaka.boxed(), ForkCondition::Timestamp(OSAKA_TIMESTAMP)),
```

### 2. Update Dependencies

Keep reth updated to get the latest EVM changes:

```bash
git pull upstream main
cargo update
```

### 3. Test Compatibility

Run the Ethereum Foundation tests to verify EVM compatibility:

```bash
make ef-tests
```

## API Compatibility

All standard Ethereum JSON-RPC APIs are supported:

```bash
# Get block number
curl -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}' \
  http://localhost:8545

# Deploy a contract
curl -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_sendRawTransaction","params":["0x..."],"id":1}' \
  http://localhost:8545

# Call a contract
curl -X POST -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_call","params":[{...},"latest"],"id":1}' \
  http://localhost:8545
```

## Development Accounts

When running in dev mode, 20 accounts are prefunded from the mnemonic:

```
test test test test test test test test test test test junk
```

| Account # | Address | Private Key |
|-----------|---------|-------------|
| 0 | 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266 | ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 |
| 1 | 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 | 59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d |
| 2 | 0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC | 5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a |

## Production Deployment

For production POA networks:

1. **Generate unique keys** for each signer (never use dev keys!)
2. **Configure multiple nodes** for high availability
3. **Set appropriate block time** (12s matches mainnet)
4. **Enable monitoring** (Prometheus/Grafana)
5. **Configure P2P networking** properly

### Multi-Node Setup with Kurtosis

```yaml
# network_params.yaml
participants:
  - el_type: reth
    el_image: your-poa-reth:latest
  - el_type: reth
    el_image: your-poa-reth:latest
  - el_type: reth
    el_image: your-poa-reth:latest
```

```bash
kurtosis run github.com/ethpandaops/ethereum-package \
  --args-file network_params.yaml
```

## Module Overview

| Module | Description |
|--------|-------------|
| `chainspec.rs` | POA chain specification with hardfork configuration |
| `consensus.rs` | POA consensus validation and signer verification |
| `genesis.rs` | Genesis configuration utilities |
| `signer.rs` | Block signing and key management |
| `main.rs` | Node entry point |

## License

MIT OR Apache-2.0

## Contributing

Contributions are welcome! Please see the main [CONTRIBUTING.md](../../CONTRIBUTING.md) for guidelines.

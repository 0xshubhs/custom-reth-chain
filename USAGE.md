# Custom POA Node - Usage Guide

This guide explains how to build, run, and test the custom POA (Proof of Authority) node.

## Quick Start

```bash
# Build the node
cargo build -p example-custom-poa-node

# Run the node (creates data in ./custompoanode/)
cargo run -p example-custom-poa-node

# Or run with debug logging
RUST_LOG=info cargo run -p example-custom-poa-node
```

## Features

- **POA Consensus**: Blocks produced every 2 seconds by authorized signers
- **Full Ethereum Compatibility**: All hardforks enabled (Frontier → Prague)
- **20 Prefunded Accounts**: Each with 10,000 ETH for testing
- **Persistent Storage**: Chain data stored in `custompoanode/` directory

## Configuration

### Chain ID
- Default: `31337` (standard local development chain ID)

### Block Period
- Default: `2 seconds`
- Configurable in `chainspec.rs` via `PoaChainSpec::block_period()`

### Authorized Signers
Default signers (from standard dev mnemonic):
1. `0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266`
2. `0x70997970C51812dc3A010C7d01b50e0d17dc79C8`
3. `0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC`

### Prefunded Accounts
20 accounts from the standard dev mnemonic, each with 10,000 ETH:

| Index | Address | Private Key |
|-------|---------|-------------|
| 0 | `0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266` | `0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80` |
| 1 | `0x70997970C51812dc3A010C7d01b50e0d17dc79C8` | `0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d` |
| 2 | `0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC` | `0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a` |
| ... | ... | ... |

## Testing with Foundry (cast)

If you have [Foundry](https://book.getfoundry.sh/) installed, you can interact with the node via RPC.

**Note**: The example uses in-process RPC. For external RPC access, modify `main.rs` to use `NodeConfig::default()` instead of `NodeConfig::test()`.

### Check Block Number
```bash
cast block-number --rpc-url http://localhost:8545
```

### Check Account Balance
```bash
cast balance 0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266 --rpc-url http://localhost:8545
```

### Send Transaction
```bash
cast send \
  --private-key 0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 \
  --rpc-url http://localhost:8545 \
  0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
  --value 1ether
```

### Deploy a Contract
```bash
# Deploy a simple contract
cast send \
  --private-key 0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 \
  --rpc-url http://localhost:8545 \
  --create \
  0x6080604052348015600f57600080fd5b50603c80601d6000396000f3fe6080604052600080fdfea164736f6c6343000800000a
```

## Testing with curl

### Get Block Number
```bash
curl -s http://localhost:8545 \
  -X POST \
  -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'
```

### Get Balance
```bash
curl -s http://localhost:8545 \
  -X POST \
  -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_getBalance","params":["0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266","latest"],"id":1}'
```

### Get Chain ID
```bash
curl -s http://localhost:8545 \
  -X POST \
  -H "Content-Type: application/json" \
  --data '{"jsonrpc":"2.0","method":"eth_chainId","params":[],"id":1}'
```

## Programmatic Usage

### Using ethers-rs

```rust
use ethers::prelude::*;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Connect to the node
    let provider = Provider::<Http>::try_from("http://localhost:8545")?;

    // Get chain ID
    let chain_id = provider.get_chainid().await?;
    println!("Chain ID: {}", chain_id);

    // Get block number
    let block_number = provider.get_block_number().await?;
    println!("Block number: {}", block_number);

    // Get balance
    let address: Address = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266".parse()?;
    let balance = provider.get_balance(address, None).await?;
    println!("Balance: {} ETH", ethers::utils::format_ether(balance));

    Ok(())
}
```

### Using alloy

```rust
use alloy::providers::{Provider, ProviderBuilder};
use alloy::primitives::address;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    // Connect to the node
    let provider = ProviderBuilder::new()
        .on_http("http://localhost:8545".parse()?);

    // Get block number
    let block_number = provider.get_block_number().await?;
    println!("Block number: {}", block_number);

    // Get balance
    let address = address!("f39Fd6e51aad88F6F4ce6aB8827279cffFb92266");
    let balance = provider.get_balance(address).await?;
    println!("Balance: {} wei", balance);

    Ok(())
}
```

## Data Directory

The node stores all chain data in `custompoanode/` directory:

```
custompoanode/
├── db/                    # MDBX database files
│   ├── mdbx.dat
│   └── mdbx.lck
├── static_files/          # Static file storage
│   ├── static_file_headers_*
│   ├── static_file_transactions_*
│   └── static_file_receipts_*
├── jwt.hex               # JWT secret for Engine API
└── reth.toml             # Node configuration
```

### Clean Start
To start fresh, delete the data directory:
```bash
rm -rf custompoanode/
```

## Customization

### Change Block Period
Edit `src/chainspec.rs`:
```rust
impl PoaChainSpec {
    pub fn block_period(&self) -> u64 {
        self.block_period  // Change default in PoaConfig
    }
}
```

### Change Chain ID
Edit `src/chainspec.rs`:
```rust
fn dev_chain() -> Self {
    // ...
    chain: Chain::from_id(YOUR_CHAIN_ID),
    // ...
}
```

### Add/Remove Signers
Edit `src/chainspec.rs`:
```rust
fn dev_chain() -> Self {
    let signers = vec![
        address!("your_signer_address_here"),
        // Add more signers...
    ];
    // ...
}
```

### Change Prefunded Accounts
Edit `src/genesis.rs`:
```rust
pub fn dev_accounts() -> Vec<Address> {
    // Return your list of addresses
}
```

## Troubleshooting

### Node exits immediately
- Check if another instance is running: `pgrep -f example-custom-poa-node`
- Check logs with `RUST_LOG=debug`

### RPC not responding
- The example uses in-process RPC by default
- For external HTTP RPC, modify to use `NodeConfig::default()` instead of `NodeConfig::test()`

### Database errors
- Delete `custompoanode/` and restart fresh
- Check disk space

### Blocks not being produced
- Ensure `dev_args.block_time` is set (not `None`)
- Check that `dev_args.dev = true`

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                    Custom POA Chain                             │
├─────────────────────────────────────────────────────────────────┤
│  ┌───────────────────┐    ┌──────────────────────────────────┐ │
│  │  POA Consensus    │    │  Ethereum EVM (Identical to      │ │
│  │  - Interval mining│    │  Mainnet - all opcodes, precompiles)│
│  │  - 2s block time  │    │                                  │ │
│  └───────────────────┘    └──────────────────────────────────┘ │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────────────────────────────────────────────────────┐│
│  │  ChainSpec with All Ethereum Hardforks                      ││
│  │  (Frontier → Shanghai → Cancun → Prague)                   ││
│  └─────────────────────────────────────────────────────────────┘│
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────────────────────────────────────────────────────┐│
│  │  Reth Node (Storage, Tx Pool, In-Process RPC)              ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
```

## Related Examples

- `examples/custom-dev-node/` - Simple dev node with transaction submission
- `examples/custom-evm/` - Custom EVM configuration
- `examples/custom-node/` - Full custom node implementation

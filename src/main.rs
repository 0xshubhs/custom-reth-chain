//! # Custom POA (Proof of Authority) Node
//!
//! This example demonstrates how to build a complete POA-based chain using Reth that is
//! fully compatible with Ethereum mainnet in terms of:
//! - Smart contract execution (identical EVM)
//! - All Ethereum hardforks (Shanghai, Cancun, Prague, etc.)
//! - Standard JSON-RPC APIs
//! - Transaction types and formats
//!
//! ## Architecture
//!
//! ```text
//! ┌─────────────────────────────────────────────────────────────────┐
//! │                    Custom POA Chain                             │
//! ├─────────────────────────────────────────────────────────────────┤
//! │  ┌───────────────────┐    ┌──────────────────────────────────┐ │
//! │  │  POA Consensus    │    │  Ethereum EVM (Identical to      │ │
//! │  │  - Signer validation│  │  Mainnet - all opcodes, precompiles)│
//! │  │  - Round-robin     │    │                                  │ │
//! │  │  - Epoch management│    └──────────────────────────────────┘ │
//! │  └───────────────────┘                                         │
//! ├─────────────────────────────────────────────────────────────────┤
//! │  ┌─────────────────────────────────────────────────────────────┐│
//! │  │  ChainSpec with All Ethereum Hardforks                      ││
//! │  │  (Homestead → Shanghai → Cancun → Prague → Future forks)   ││
//! │  └─────────────────────────────────────────────────────────────┘│
//! ├─────────────────────────────────────────────────────────────────┤
//! │  ┌─────────────────────────────────────────────────────────────┐│
//! │  │  Reth Node (P2P Networking, RPC, Storage, Tx Pool)         ││
//! │  └─────────────────────────────────────────────────────────────┘│
//! └─────────────────────────────────────────────────────────────────┘
//! ```
//!
//! ## Features
//!
//! - **Multi-signer POA consensus**: Configure multiple authorized signers
//! - **Epoch-based signer rotation**: Signers take turns producing blocks
//! - **Mainnet EVM compatibility**: All smart contracts work identically
//! - **Hardfork tracking**: Easily enable new Ethereum upgrades
//! - **Configurable block time**: Set your desired block interval
//!
//! ## Usage
//!
//! ```bash
//! # Run with default configuration (POA mode with 2-second block intervals)
//! cargo run -p example-custom-poa-node
//!
//! # The node produces blocks every 2 seconds automatically
//! ```

#![cfg_attr(not(test), warn(unused_crate_dependencies))]

pub mod chainspec;
pub mod consensus;
pub mod genesis;
pub mod signer;

use crate::chainspec::PoaChainSpec;
use alloy_consensus::BlockHeader;
use alloy_primitives::U256;
use futures_util::StreamExt;
use reth_ethereum::{
    node::{
        builder::{NodeBuilder, NodeHandle},
        core::{
            args::{DevArgs, RpcServerArgs},
            node_config::NodeConfig,
        },
        EthereumNode,
    },
    provider::CanonStateSubscriptions,
    rpc::api::eth::helpers::EthState,
    tasks::TaskManager,
};
use std::{path::PathBuf, time::Duration};

/// Main entry point for the POA node
#[tokio::main]
async fn main() -> eyre::Result<()> {
    // Initialize tracing for debug output
    reth_tracing::init_test_tracing();

    // Create the POA chain specification
    let poa_chain = PoaChainSpec::dev_chain();

    println!("Starting POA node with chain ID: {}", poa_chain.inner().chain.id());
    println!("Authorized signers: {:?}", poa_chain.signers());
    println!("Block period: {} seconds", poa_chain.block_period());

    // Set up data directory in the current working directory
    let datadir = PathBuf::from("custompoanode");

    // Configure dev args with interval-based block production (POA style)
    // This makes the node produce blocks at regular intervals, not just when transactions arrive
    let dev_args = DevArgs {
        dev: true,
        block_time: Some(Duration::from_secs(poa_chain.block_period())),
        block_max_transactions: None,
        ..Default::default()
    };

    // Build node configuration with interval-based mining for POA
    let node_config = NodeConfig::test()
        .with_dev(dev_args)
        .with_rpc(RpcServerArgs::default().with_http())
        .with_chain(poa_chain.inner().clone());

    println!("Dev mode enabled: {}", node_config.dev.dev);
    println!(
        "Mining mode: interval ({} seconds between blocks)",
        poa_chain.block_period()
    );

    // Create the task manager - IMPORTANT: keep this alive for the duration of the program!
    // Dropping the TaskManager fires the shutdown signal, which stops all spawned tasks.
    let tasks = TaskManager::current();

    let NodeHandle { node, node_exit_future } = NodeBuilder::new(node_config)
        .testing_node_with_datadir(tasks.executor(), datadir.clone())
        .node(EthereumNode::default())
        .launch_with_debug_capabilities()
        .await?;

    println!("\n✅ POA node started successfully!");
    println!("Genesis hash: {:?}", poa_chain.inner().genesis_hash());

    // Get in-process RPC API
    let eth_api = node.rpc_registry.eth_api();

    // Print prefunded accounts and their balances
    println!("\nPrefunded accounts:");
    let accounts = genesis::dev_accounts();
    for (i, account) in accounts.iter().enumerate().take(3) {
        let balance = eth_api.balance(*account, None).await?;
        println!("  {}. {} - Balance: {} ETH", i + 1, account, balance / U256::from(10u64.pow(18)));
    }

    // Subscribe to new blocks
    let mut notifications = node.provider.canonical_state_stream();

    println!("\n📖 Chain data is stored in: {:?}", datadir);
    println!(
        "\n🚀 Blocks are produced every {} seconds (POA interval mining).",
        poa_chain.block_period()
    );

    // Wait for a few blocks to be produced
    println!("\nWaiting for blocks to be produced...");
    for i in 0..5 {
        if let Some(notification) = notifications.next().await {
            let block = notification.tip();
            let block_num = block.header().number();
            let tx_count = block.body().transactions().count();
            println!(
                "  Block #{} mined - {} transactions",
                block_num, tx_count
            );

            // Check balance after each block
            if i == 2 {
                let balance = eth_api.balance(accounts[0], None).await?;
                println!("    Account 0 balance: {} ETH", balance / U256::from(10u64.pow(18)));
            }
        }
    }

    println!("\n✅ POA node is working! Blocks are being produced every {} seconds.", poa_chain.block_period());
    println!("Press Ctrl+C to stop the node...\n");

    // Keep the node running until exit signal
    node_exit_future.await
}
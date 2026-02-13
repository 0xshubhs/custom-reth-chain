//! # Custom POA (Proof of Authority) Node
//!
//! A production-grade POA blockchain node built on Reth that is fully compatible with
//! Ethereum mainnet in terms of smart contract execution, hardforks, and JSON-RPC APIs.
//!
//! ## Architecture
//!
//! ```text
//! PoaNode (custom)
//!   ├── Consensus: PoaConsensus (validates signer authority, timing, difficulty)
//!   ├── Block Production: Interval mining with POA block signing
//!   ├── EVM: Identical to Ethereum mainnet (all opcodes, precompiles)
//!   ├── Hardforks: Frontier through Prague (all at genesis)
//!   └── RPC: Full eth_*, web3_*, net_* + external HTTP/WS
//! ```
//!
//! ## Usage
//!
//! ```bash
//! # Run in dev mode (default)
//! cargo run --release
//!
//! # Run with custom settings
//! cargo run --release -- --chain-id 9323310 --block-time 12 --datadir /data/meowchain
//!
//! # Run with signer key from environment
//! SIGNER_KEY=ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80 cargo run --release
//! ```

#![cfg_attr(not(test), warn(unused_crate_dependencies))]

pub mod chainspec;
pub mod consensus;
pub mod genesis;
pub mod node;
pub mod payload;
pub mod rpc;
pub mod signer;

use crate::chainspec::{PoaChainSpec, PoaConfig};
use crate::node::PoaNode;
use crate::rpc::{MeowApiServer, MeowRpc};
use crate::signer::SignerManager;
use alloy_consensus::BlockHeader;
use clap::Parser;
use futures_util::StreamExt;
use reth_db::init_db;
use reth_ethereum::{
    node::builder::{NodeBuilder, NodeHandle},
    node::core::{
        args::{DatadirArgs, DevArgs, RpcServerArgs},
        node_config::NodeConfig,
    },
    provider::CanonStateSubscriptions,
    tasks::TaskExecutor,
};
use std::{
    net::{IpAddr, Ipv4Addr},
    path::PathBuf,
    sync::Arc,
    time::Duration,
};

/// CLI arguments for the POA node
#[derive(Parser, Debug)]
#[command(name = "meowchain", about = "Meowchain POA Node")]
struct Cli {
    /// Chain ID for the network
    #[arg(long, default_value = "9323310")]
    chain_id: u64,

    /// Block production interval in seconds
    #[arg(long, default_value = "2")]
    block_time: u64,

    /// Data directory for chain storage
    #[arg(long, default_value = "data")]
    datadir: PathBuf,

    /// HTTP RPC listen address
    #[arg(long, default_value = "0.0.0.0")]
    http_addr: String,

    /// HTTP RPC port
    #[arg(long, default_value = "8545")]
    http_port: u16,

    /// WebSocket RPC listen address
    #[arg(long, default_value = "0.0.0.0")]
    ws_addr: String,

    /// WebSocket RPC port
    #[arg(long, default_value = "8546")]
    ws_port: u16,

    /// Signer private key (hex, without 0x prefix).
    /// Can also be set via SIGNER_KEY environment variable.
    #[arg(long, env = "SIGNER_KEY")]
    signer_key: Option<String>,

    /// Use production genesis configuration (chain ID 9323310)
    #[arg(long)]
    production: bool,

    /// Disable dev mode (no auto-mining)
    #[arg(long)]
    no_dev: bool,

    /// Override block gas limit (e.g., 100000000 for 100M, 1000000000 for 1B)
    #[arg(long)]
    gas_limit: Option<u64>,

    /// Enable eager mining: build block immediately when transactions arrive
    /// instead of waiting for block-time interval
    #[arg(long)]
    eager_mining: bool,
}

/// Main entry point for the POA node
#[tokio::main]
async fn main() -> eyre::Result<()> {
    // Initialize tracing
    reth_tracing::init_test_tracing();

    // Parse CLI arguments
    let cli = Cli::parse();

    // Determine if we're in dev mode
    let is_dev_mode = !cli.no_dev && !cli.production;

    // Create chain specification based on CLI flags
    let poa_chain = if cli.production {
        let mut config = genesis::GenesisConfig::production();
        if let Some(gas_limit) = cli.gas_limit {
            config.gas_limit = gas_limit;
        }
        let genesis = genesis::create_genesis(config);
        let poa_config = PoaConfig {
            period: cli.block_time,
            epoch: 30000,
            signers: genesis::dev_accounts().into_iter().take(5).collect(),
        };
        PoaChainSpec::new(genesis, poa_config)
    } else {
        // Dev mode: use CLI chain_id and block_time
        let mut config = genesis::GenesisConfig::dev();
        config.chain_id = cli.chain_id;
        config.block_period = cli.block_time;
        if let Some(gas_limit) = cli.gas_limit {
            config.gas_limit = gas_limit;
        }
        let genesis = genesis::create_genesis(config);
        let poa_config = PoaConfig {
            period: cli.block_time,
            epoch: 30000,
            signers: genesis::dev_signers(),
        };
        PoaChainSpec::new(genesis, poa_config)
    };

    let chain_spec_arc = Arc::new(poa_chain.clone());

    println!("=== Meowchain POA Node ===");
    println!("Chain ID:        {}", poa_chain.inner().chain.id());
    println!("Block period:    {} seconds", poa_chain.block_period());
    println!("Mode:            {}", if is_dev_mode { "dev" } else { "production" });
    println!("Authorized signers ({}):", poa_chain.signers().len());
    for (i, signer) in poa_chain.signers().iter().enumerate() {
        println!("  {}. {}", i + 1, signer);
    }

    // Set up signer manager with runtime key loading
    let signer_manager = Arc::new(SignerManager::new());

    if let Some(key) = &cli.signer_key {
        // Load signer key from CLI/environment
        let addr = signer_manager.add_signer_from_hex(key).await?;
        println!("Signer key loaded: {}", addr);
    } else if is_dev_mode {
        // In dev mode, load dev signers (first 3 keys)
        for key in signer::dev::DEV_PRIVATE_KEYS.iter().take(3) {
            signer_manager
                .add_signer_from_hex(key)
                .await
                .expect("Dev keys should be valid");
        }
        println!(
            "Dev signers loaded: {} keys",
            signer_manager.signer_addresses().await.len()
        );
    } else {
        println!("WARNING: No signer key provided. Node will validate but not produce blocks.");
        println!("  Set --signer-key or SIGNER_KEY environment variable.");
    }

    // Configure dev args (interval-based or eager block production)
    let dev_args = if !is_dev_mode {
        DevArgs::default()
    } else {
        DevArgs {
            dev: true,
            block_time: if cli.eager_mining {
                None // Mine immediately on tx arrival
            } else {
                Some(Duration::from_secs(poa_chain.block_period()))
            },
            block_max_transactions: None,
            ..Default::default()
        }
    };

    // Configure RPC server to listen on all interfaces
    let http_addr: IpAddr = cli.http_addr.parse().unwrap_or(IpAddr::V4(Ipv4Addr::UNSPECIFIED));
    let ws_addr: IpAddr = cli.ws_addr.parse().unwrap_or(IpAddr::V4(Ipv4Addr::UNSPECIFIED));

    let mut rpc_args = RpcServerArgs::default();
    rpc_args.http = true;
    rpc_args.http_addr = http_addr;
    rpc_args.http_port = cli.http_port;
    rpc_args.ws = true;
    rpc_args.ws_addr = ws_addr;
    rpc_args.ws_port = cli.ws_port;

    // Build node configuration with proper data directory
    let node_config = NodeConfig::default()
        .with_dev(dev_args)
        .with_rpc(rpc_args)
        .with_chain(poa_chain.inner().clone())
        .with_datadir_args(DatadirArgs {
            datadir: cli.datadir.clone().into(),
            ..Default::default()
        });

    println!("\nNode configuration:");
    println!("  Dev mode:    {}", is_dev_mode);
    println!("  Mining mode: {}", if cli.eager_mining { "eager (tx-triggered)" } else { "interval" });
    println!("  Gas limit:   {}", poa_chain.inner().genesis().gas_limit);
    println!("  HTTP RPC:    {}:{}", http_addr, cli.http_port);
    println!("  WS RPC:      {}:{}", ws_addr, cli.ws_port);
    println!("  Data dir:    {:?}", cli.datadir);

    // Create the task executor (attaches to current tokio runtime)
    let tasks = TaskExecutor::with_existing_handle(tokio::runtime::Handle::current())?;

    // Initialize persistent MDBX database (replaces testing_node_with_datadir)
    let db_path = cli.datadir.join("db");
    std::fs::create_dir_all(&db_path)?;
    let database = Arc::new(init_db(&db_path, Default::default())?);

    // Build and launch the node with PoaNode (custom consensus + payload builder)
    // PoaNode injects PoaConsensus for validation and PoaPayloadBuilder for signed block production.
    // dev_mode controls whether signature verification is enforced.
    // Clone values for the RPC closure (captured by move)
    let rpc_chain_spec = chain_spec_arc.clone();
    let rpc_signer_manager = signer_manager.clone();
    let rpc_dev_mode = is_dev_mode;

    let NodeHandle {
        node,
        node_exit_future,
    } = NodeBuilder::new(node_config)
        .with_database(database)
        .with_launch_context(tasks)
        .node(
            PoaNode::new(chain_spec_arc.clone())
                .with_dev_mode(is_dev_mode)
                .with_signer_manager(signer_manager.clone()),
        )
        .extend_rpc_modules(move |ctx| {
            let meow_rpc = MeowRpc::new(rpc_chain_spec, rpc_signer_manager, rpc_dev_mode);
            ctx.modules.merge_configured(meow_rpc.into_rpc())?;
            println!("  meow_* RPC namespace registered");
            Ok(())
        })
        .launch_with_debug_capabilities()
        .await?;

    println!("\nPOA node started successfully!");
    println!("Genesis hash: {:?}", poa_chain.inner().genesis_hash());

    // Spawn block monitoring task (single subscription)
    let monitoring_chain_spec = chain_spec_arc.clone();
    let monitoring_signer_manager = signer_manager.clone();
    tokio::spawn(async move {
        let mut block_stream = node.provider.canonical_state_stream();

        while let Some(notification) = block_stream.next().await {
            let block = notification.tip();
            let block_num = block.header().number();
            let tx_count = block.body().transactions().count();

            // Determine which signer should sign this block (round-robin)
            let signers = monitoring_chain_spec.signers();
            if signers.is_empty() {
                println!("  Block #{} produced - {} txs (no signers configured)", block_num, tx_count);
                continue;
            }
            let signer_index = (block_num as usize) % signers.len();
            let expected_signer = signers[signer_index];

            // Check if we have the key for the expected signer
            if monitoring_signer_manager.has_signer(&expected_signer).await {
                println!(
                    "  Block #{} - {} txs (in-turn: {}, difficulty=1)",
                    block_num, tx_count, expected_signer
                );
            } else {
                let our_addresses = monitoring_signer_manager.signer_addresses().await;
                let is_our_turn = our_addresses.iter().any(|addr| signers.contains(addr));
                if is_our_turn {
                    println!(
                        "  Block #{} - {} txs (out-of-turn, expected: {})",
                        block_num, tx_count, expected_signer
                    );
                } else {
                    println!(
                        "  Block #{} - {} txs (expected signer: {})",
                        block_num, tx_count, expected_signer
                    );
                }
            }
        }
    });

    // Print prefunded accounts
    println!("\nPrefunded accounts:");
    let accounts = genesis::dev_accounts();
    for (i, account) in accounts.iter().enumerate().take(5) {
        println!("  {}. {}", i + 1, account);
    }

    println!("\nChain data stored in: {:?}", cli.datadir);
    println!(
        "Blocks produced every {} seconds (POA interval mining)",
        poa_chain.block_period()
    );

    println!("\nPOA node running. Press Ctrl+C to stop.");
    println!("  HTTP RPC: http://{}:{}", cli.http_addr, cli.http_port);
    println!("  WS RPC:   ws://{}:{}", cli.ws_addr, cli.ws_port);

    // Keep the node running
    node_exit_future.await
}


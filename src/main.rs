use example_custom_poa_node::chainspec::{PoaChainSpec, PoaConfig};
use example_custom_poa_node::cli::Cli;
use example_custom_poa_node::genesis;
use example_custom_poa_node::metrics::{BlockMetrics, ChainMetrics};
use example_custom_poa_node::node::PoaNode;
use example_custom_poa_node::output;
use example_custom_poa_node::rpc::{MeowApiServer, MeowRpc}; // Reuse signer manager for RPC signing and block sealing
use example_custom_poa_node::signer::{self, SignerManager};
use example_custom_poa_node::statediff::StateDiffBuilder;

use alloy_consensus::BlockHeader;
use alloy_primitives::B256;
use clap::Parser;
use futures_util::StreamExt;
use reth_db::init_db;
use reth_ethereum::{
    node::builder::{NodeBuilder, NodeHandle},
    node::core::{
        args::{DatadirArgs, DevArgs, NetworkArgs, RpcServerArgs},
        node_config::NodeConfig,
    },
    provider::CanonStateSubscriptions,
    tasks::{RuntimeBuilder, RuntimeConfig, TokioConfig},
};
use reth_network_peers::TrustedPeer;
use std::{
    net::{IpAddr, Ipv4Addr},
    sync::Arc,
    time::{Duration, Instant},
};

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

    // Effective mining interval: --block-time-ms overrides --block-time when non-zero (Phase 2.14).
    let mining_interval = if cli.block_time_ms > 0 {
        Duration::from_millis(cli.block_time_ms)
    } else {
        Duration::from_secs(poa_chain.block_period())
    };

    output::print_banner(poa_chain.inner().chain.id(), mining_interval);
    let mode_str = match (is_dev_mode, cli.mining) {
        (true, _) => "dev",
        (false, true) => "production+mining",
        (false, false) => "production",
    };
    output::print_mode(mode_str);
    output::print_signers(poa_chain.signers());

    // Set up signer manager with runtime key loading
    let signer_manager = Arc::new(SignerManager::new());

    if let Some(key) = &cli.signer_key {
        // Load signer key from CLI/environment
        let addr = signer_manager.add_signer_from_hex(key).await?;
        output::print_signer_loaded(&addr);
    } else if is_dev_mode {
        // In dev mode, load dev signers (first 3 keys)
        for key in signer::dev::DEV_PRIVATE_KEYS.iter().take(3) {
            signer_manager
                .add_signer_from_hex(key)
                .await
                .expect("Dev keys should be valid");
        }
        output::print_dev_signers_loaded(signer_manager.signer_addresses().await.len());
    } else {
        output::print_no_signer_warning();
    }

    // Configure dev args (interval-based or eager block production).
    // --mining forces auto-mining even in production mode (for testing PoaEngineValidator).
    let mining_enabled = is_dev_mode || cli.mining;
    let dev_args = if !mining_enabled {
        DevArgs::default()
    } else {
        DevArgs {
            dev: true,
            block_time: if cli.eager_mining {
                None // Mine immediately on tx arrival
            } else {
                Some(mining_interval)
            },
            block_max_transactions: None,
            ..Default::default()
        }
    };

    // Configure RPC server to listen on all interfaces
    let http_addr: IpAddr = cli
        .http_addr
        .parse()
        .unwrap_or(IpAddr::V4(Ipv4Addr::UNSPECIFIED));
    let ws_addr: IpAddr = cli
        .ws_addr
        .parse()
        .unwrap_or(IpAddr::V4(Ipv4Addr::UNSPECIFIED));

    let rpc_args = RpcServerArgs {
        http: true,
        http_addr,
        http_port: cli.http_port,
        ws: true,
        ws_addr,
        ws_port: cli.ws_port,
        ..Default::default()
    };

    // Configure P2P network (bootnodes, port, discovery)
    let mut network_args = NetworkArgs {
        port: cli.port,
        ..Default::default()
    };
    network_args.discovery.port = cli.port;
    if cli.disable_discovery {
        network_args.discovery.disable_discovery = true;
    }
    if let Some(ref bootnodes) = cli.bootnodes {
        let parsed: Vec<TrustedPeer> = bootnodes
            .iter()
            .filter_map(|s| s.parse::<TrustedPeer>().ok())
            .collect();
        if !parsed.is_empty() {
            network_args.bootnodes = Some(parsed);
        }
    }

    // Build node configuration with proper data directory
    let node_config = NodeConfig::default()
        .with_dev(dev_args)
        .with_rpc(rpc_args)
        .with_network(network_args)
        .with_chain(poa_chain.inner().clone())
        .with_datadir_args(DatadirArgs {
            datadir: cli.datadir.clone().into(),
            ..Default::default()
        });

    output::print_config(
        is_dev_mode,
        if cli.eager_mining {
            "eager (tx-triggered)"
        } else {
            "interval"
        },
        poa_chain.inner().genesis().gas_limit,
        &http_addr.to_string(),
        cli.http_port,
        &ws_addr.to_string(),
        cli.ws_port,
        cli.port,
        cli.bootnodes.as_ref().map(|b| b.len()),
        &cli.datadir,
    );

    // Create the task executor (attaches to current tokio runtime)
    let tasks = RuntimeBuilder::new(RuntimeConfig::default().with_tokio(
        TokioConfig::existing_handle(tokio::runtime::Handle::current()),
    ))
    .build()
    .map_err(|e| eyre::eyre!("{e}"))?;

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
                .with_signer_manager(signer_manager.clone())
                .with_cache_size(cli.cache_size)
                .with_max_contract_size(cli.max_contract_size)
                .with_calldata_gas(cli.calldata_gas),
        )
        .extend_rpc_modules(move |ctx| {
            let meow_rpc = MeowRpc::new(rpc_chain_spec, rpc_signer_manager, rpc_dev_mode);
            ctx.modules.merge_configured(meow_rpc.into_rpc())?;
            output::print_rpc_registered("meow_*");
            Ok(())
        })
        .launch_with_debug_capabilities()
        .await?;

    output::print_node_started(poa_chain.inner().genesis_hash());

    // Set up performance metrics (Phase 5)
    let chain_metrics = ChainMetrics::default_window();
    let metrics_interval = cli.metrics_interval;

    // Spawn block monitoring task (single subscription)
    let monitoring_chain_spec = chain_spec_arc.clone();
    let monitoring_signer_manager = signer_manager.clone();
    let monitoring_metrics = chain_metrics.clone();
    let monitoring_interval = mining_interval;
    tokio::spawn(async move {
        let mut block_stream = node.provider.canonical_state_stream();
        // Track wall-clock arrival time for block-time budget monitoring (Phase 2.16).
        let mut last_block_arrived = Instant::now();

        while let Some(notification) = block_stream.next().await {
            let arrived = Instant::now();
            let elapsed_ms = last_block_arrived.elapsed().as_millis() as u64;
            last_block_arrived = arrived;

            let block = notification.tip();
            let block_num = block.header().number();
            let tx_count = block.body().transactions().count();
            let gas_used = block.header().gas_used();

            // ── State diff (Phase 2.18): build StateDiff from execution_outcome ──
            // Captures balance/nonce/code + storage changes for replica sync foundation.
            let chain = notification.committed();
            let outcome = chain.execution_outcome();
            let block_hash: B256 = block.hash();
            let mut diff_builder =
                StateDiffBuilder::new(block_num, block_hash)
                    .with_gas_used(gas_used)
                    .with_tx_count(tx_count);
            for (addr, account) in outcome.bundle_accounts_iter() {
                // Account-level changes: balance, nonce, code
                match (&account.original_info, &account.info) {
                    (Some(old), Some(new)) => {
                        if old.balance != new.balance {
                            diff_builder.record_balance_change(addr, old.balance, new.balance);
                        }
                        if old.nonce != new.nonce {
                            diff_builder.record_nonce_change(addr, old.nonce, new.nonce);
                        }
                        if old.code_hash != new.code_hash {
                            diff_builder.record_code_change(addr);
                        }
                    }
                    (None, Some(_)) => diff_builder.record_code_change(addr), // created
                    (Some(_), None) => diff_builder.record_code_change(addr), // destroyed
                    (None, None) => {}
                }
                // Storage-slot changes
                for (slot_key, slot) in &account.storage {
                    if slot.is_changed() {
                        let old = B256::from(slot.previous_or_original_value.to_be_bytes::<32>());
                        let new = B256::from(slot.present_value.to_be_bytes::<32>());
                        diff_builder.record_storage_change(addr, *slot_key, old, new);
                    }
                }
            }
            let state_diff = diff_builder.build();
            let accounts_changed = state_diff.touched_account_count();
            let slots_changed = state_diff.total_storage_changes();

            // Determine which signer should sign this block (round-robin)
            let signers = monitoring_chain_spec.signers();
            if signers.is_empty() {
                output::print_block_no_signers(block_num, tx_count);
                continue;
            }
            let signer_index = (block_num as usize) % signers.len();
            let expected_signer = signers[signer_index];

            // Determine if this is an in-turn block for metrics
            let in_turn = monitoring_signer_manager.has_signer(&expected_signer).await;

            // Check if we have the key for the expected signer
            if in_turn {
                output::print_block_in_turn(block_num, tx_count, &expected_signer);
            } else {
                let our_addresses = monitoring_signer_manager.signer_addresses().await;
                let is_our_turn = our_addresses.iter().any(|addr| signers.contains(addr));
                if is_our_turn {
                    output::print_block_out_of_turn(block_num, tx_count, &expected_signer);
                } else {
                    output::print_block_observed(block_num, tx_count, &expected_signer);
                }
            }

            // Print state diff when there are transactions (Phase 2.15).
            if tx_count > 0 {
                output::print_block_state_diff(block_num, accounts_changed, slots_changed);
            }

            // Block time budget warning: fire if a block arrives > 3× the expected
            // interval (Phase 2.16). 3× threshold avoids false positives from normal
            // Reth dev-mining timer jitter (~2× is common at sub-second intervals).
            // Skip block 1 (first arrival time is not meaningful).
            let interval_ms = monitoring_interval.as_millis() as u64;
            if block_num > 1 && interval_ms > 0 && elapsed_ms > interval_ms * 3 {
                output::print_block_time_budget_warning(block_num, elapsed_ms, interval_ms);
            }

            // Record block metrics (Phase 5)
            let block_metrics = BlockMetrics {
                block_number: block_num,
                tx_count,
                gas_used,
                build_duration: Duration::ZERO, // actual timing requires payload hook
                sign_duration: Duration::ZERO,
                in_turn,
            };
            monitoring_metrics.record_block(&block_metrics);

            // Print metrics report at configured interval
            if metrics_interval > 0 && block_num > 0 && block_num.is_multiple_of(metrics_interval) {
                let snap = monitoring_metrics.snapshot();
                println!(
                    "  [metrics] block={} total_txs={} in_turn_rate={:.1}%",
                    block_num,
                    snap.total_txs,
                    snap.in_turn_rate() * 100.0,
                );
            }
        }
    });

    // Print prefunded accounts
    let accounts = genesis::dev_accounts();
    output::print_prefunded(&accounts[..5.min(accounts.len())]);
    output::print_chain_data(&cli.datadir, mining_interval);
    output::print_running(&cli.http_addr, cli.http_port, &cli.ws_addr, cli.ws_port);

    // Keep the node running
    node_exit_future.await
}

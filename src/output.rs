//! Colored console output for the Meowchain POA node.
//!
//! Replaces raw `println!` calls with structured, colored output.
//! Color scheme: blue+bold headers, cyan values, green success,
//! yellow warnings, dimmed secondary text.

use alloy_primitives::Address;
use colored::Colorize;
use std::fmt;
use std::path::Path;
use std::time::Duration;

// ── Helpers ────────────────────────────────────────────────────────

/// Format a block interval Duration as a human-readable string.
///
/// - Sub-second values → `"500ms"`
/// - Integer seconds → `"1s"`
/// - Fractional seconds → `"1.5s"` (rare, from non-round ms values)
pub fn format_interval(d: Duration) -> String {
    let ms = d.as_millis();
    if ms < 1000 {
        format!("{ms}ms")
    } else if ms % 1000 == 0 {
        format!("{}s", d.as_secs())
    } else {
        format!("{:.1}s", d.as_secs_f64())
    }
}

// ── Banner & Identity ──────────────────────────────────────────────

/// Print the startup banner with chain identity.
///
/// `mining_interval` is the effective block production interval (may be sub-second).
pub fn print_banner(chain_id: u64, mining_interval: Duration) {
    println!();
    println!("{}", "=== Meowchain POA Node ===".blue().bold());
    println!("  Chain ID:     {}", chain_id.to_string().cyan());
    println!(
        "  Block period: {}",
        format_interval(mining_interval).cyan()
    );
}

/// Print the operating mode.
pub fn print_mode(mode: &str) {
    println!("  Mode:         {}", mode.cyan());
}

// ── Signer Info ────────────────────────────────────────────────────

/// Print the authorized signer list.
pub fn print_signers(signers: &[Address]) {
    println!(
        "  Authorized signers ({}):",
        signers.len().to_string().cyan()
    );
    for (i, signer) in signers.iter().enumerate() {
        println!(
            "    {}. {}",
            (i + 1).to_string().dimmed(),
            format!("{signer}").cyan()
        );
    }
}

/// Print confirmation that a signer key was loaded.
pub fn print_signer_loaded(addr: &Address) {
    println!(
        "  {} Signer key loaded: {}",
        "OK".green().bold(),
        format!("{addr}").cyan()
    );
}

/// Print confirmation that dev signers were loaded.
pub fn print_dev_signers_loaded(count: usize) {
    println!(
        "  {} Dev signers loaded: {} keys",
        "OK".green().bold(),
        count.to_string().cyan()
    );
}

/// Print a warning when no signer key is provided.
pub fn print_no_signer_warning() {
    println!(
        "  {} No signer key provided. Node will validate but not produce blocks.",
        "WARNING:".yellow().bold()
    );
    println!(
        "  {}",
        "Set --signer-key or SIGNER_KEY environment variable.".dimmed()
    );
}

// ── Node Configuration ─────────────────────────────────────────────

/// Print the node configuration block.
#[allow(clippy::too_many_arguments)]
pub fn print_config(
    is_dev_mode: bool,
    mining_mode: &str,
    gas_limit: u64,
    http_addr: &str,
    http_port: u16,
    ws_addr: &str,
    ws_port: u16,
    p2p_port: u16,
    bootnode_count: Option<usize>,
    datadir: &Path,
) {
    println!();
    println!("{}", "Node configuration:".blue().bold());
    println!(
        "  {} {}",
        "Dev mode:   ".dimmed(),
        if is_dev_mode {
            "true".green()
        } else {
            "false".normal()
        }
    );
    println!("  {} {}", "Mining mode:".dimmed(), mining_mode.cyan());
    println!(
        "  {} {}",
        "Gas limit:  ".dimmed(),
        gas_limit.to_string().cyan()
    );
    println!(
        "  {} {}:{}",
        "HTTP RPC:   ".dimmed(),
        http_addr.cyan(),
        http_port.to_string().cyan()
    );
    println!(
        "  {} {}:{}",
        "WS RPC:     ".dimmed(),
        ws_addr.cyan(),
        ws_port.to_string().cyan()
    );
    println!(
        "  {} {}",
        "P2P port:   ".dimmed(),
        p2p_port.to_string().cyan()
    );
    if let Some(count) = bootnode_count {
        println!(
            "  {} {} configured",
            "Bootnodes:  ".dimmed(),
            count.to_string().cyan()
        );
    }
    println!("  {} {:?}", "Data dir:   ".dimmed(), datadir);
}

// ── RPC ────────────────────────────────────────────────────────────

/// Print that a custom RPC namespace was registered.
pub fn print_rpc_registered(namespace: &str) {
    println!(
        "  {} {} RPC namespace registered",
        "OK".green().bold(),
        namespace.cyan()
    );
}

// ── Node Lifecycle ─────────────────────────────────────────────────

/// Print that the node started successfully.
pub fn print_node_started(genesis_hash: impl fmt::Debug) {
    println!();
    println!("{}", "Node started successfully!".green().bold());
    println!("  Genesis hash: {:?}", genesis_hash);
}

/// Print prefunded accounts.
pub fn print_prefunded(accounts: &[Address]) {
    println!();
    println!("{}", "Prefunded accounts:".blue().bold());
    for (i, account) in accounts.iter().enumerate() {
        println!(
            "  {}. {}",
            (i + 1).to_string().dimmed(),
            format!("{account}").cyan()
        );
    }
}

/// Print chain data storage info and effective block interval.
///
/// `mining_interval` is the effective block production interval (may be sub-second).
pub fn print_chain_data(datadir: &Path, mining_interval: Duration) {
    println!();
    println!(
        "  Chain data stored in: {}",
        datadir.display().to_string().dimmed()
    );
    println!(
        "  Blocks produced every {} (POA interval mining)",
        format_interval(mining_interval).cyan()
    );
}

/// Print the final "running" message with RPC URLs.
pub fn print_running(http_addr: &str, http_port: u16, ws_addr: &str, ws_port: u16) {
    println!();
    println!(
        "{}",
        "POA node running. Press Ctrl+C to stop.".green().bold()
    );
    println!(
        "  HTTP RPC: {}",
        format!("http://{http_addr}:{http_port}").cyan()
    );
    println!("  WS RPC:   {}", format!("ws://{ws_addr}:{ws_port}").cyan());
}

// ── Consensus ──────────────────────────────────────────────────────

/// Print consensus initialization info.
pub fn print_consensus_init(signer_count: usize, epoch: u64, period: u64, mode: &str) {
    println!(
        "  {} POA Consensus: {} signers, epoch: {}, period: {}s, mode: {}",
        "OK".green().bold(),
        signer_count.to_string().cyan(),
        epoch.to_string().cyan(),
        period.to_string().cyan(),
        mode.cyan(),
    );
}

// ── On-Chain Reads ─────────────────────────────────────────────────

/// Print when on-chain gas limit differs from default.
pub fn print_onchain_gas_limit(onchain: u64, default: u64) {
    println!(
        "  {} OnChain gas limit: {} (default was {})",
        "OK".green().bold(),
        onchain.to_string().cyan(),
        default.to_string().dimmed(),
    );
}

/// Print when on-chain signers are loaded at startup.
pub fn print_onchain_signers(count: usize) {
    println!(
        "  {} OnChain signers: {} loaded from SignerRegistry",
        "OK".green().bold(),
        count.to_string().cyan(),
    );
}

// ── Payload / Block Production ─────────────────────────────────────

/// Print when signers are refreshed at an epoch block.
pub fn print_epoch_refresh(block_number: u64, signer_count: usize) {
    println!(
        "  {} Epoch #{}: refreshed {} signers from SignerRegistry",
        "OK".green().bold(),
        block_number.to_string().cyan(),
        signer_count.to_string().cyan(),
    );
}

/// Print when a block is signed by a POA signer.
///
/// `build_ms` is the wall-clock time spent building the block (Phase 2.17 timing).
/// `sign_ms` is the wall-clock time spent on ECDSA signing (Phase 5 timing).
pub fn print_block_signed(
    block_number: u64,
    signer: &Address,
    in_turn: bool,
    build_ms: u64,
    sign_ms: u64,
) {
    let turn = if in_turn {
        "in-turn".green()
    } else {
        "out-of-turn".yellow()
    };
    println!(
        "  {} POA block #{} signed by {} ({}, build={}ms sign={}ms)",
        "OK".green().bold(),
        block_number.to_string().cyan(),
        format!("{signer}").cyan(),
        turn,
        build_ms.to_string().dimmed(),
        sign_ms.to_string().dimmed(),
    );
}

// ── Block Monitor ──────────────────────────────────────────────────

/// Print block info when no signers are configured (dev/monitoring).
pub fn print_block_no_signers(block_num: u64, tx_count: usize) {
    println!(
        "  {} Block #{} - {} txs {}",
        "#".dimmed(),
        block_num.to_string().cyan(),
        tx_count.to_string().cyan(),
        "(no signers configured)".dimmed(),
    );
}

/// Print block info when in-turn signer produced it.
pub fn print_block_in_turn(block_num: u64, tx_count: usize, signer: &Address) {
    println!(
        "  {} Block #{} - {} txs (in-turn: {})",
        "#".green(),
        block_num.to_string().cyan(),
        tx_count.to_string().cyan(),
        format!("{signer}").green(),
    );
}

/// Print block info for an out-of-turn block.
pub fn print_block_out_of_turn(block_num: u64, tx_count: usize, expected: &Address) {
    println!(
        "  {} Block #{} - {} txs (out-of-turn, expected: {})",
        "#".yellow(),
        block_num.to_string().cyan(),
        tx_count.to_string().cyan(),
        format!("{expected}").dimmed(),
    );
}

/// Print block info for a block we're only observing.
pub fn print_block_observed(block_num: u64, tx_count: usize, expected: &Address) {
    println!(
        "  {} Block #{} - {} txs (expected signer: {})",
        "#".dimmed(),
        block_num.to_string().cyan(),
        tx_count.to_string().cyan(),
        format!("{expected}").dimmed(),
    );
}

/// Print per-block state diff summary (Phase 2.15): accounts and slots changed.
///
/// Only printed when there are non-trivial changes (e.g. blocks with transactions).
/// Empty blocks always change at least 1 account (coinbase), so those are filtered
/// by the caller with `if tx_count > 0`.
pub fn print_block_state_diff(block_num: u64, accounts_changed: usize, slots_changed: usize) {
    println!(
        "  {} Block #{}: {} accounts, {} storage slots changed",
        "~".dimmed(),
        block_num.to_string().dimmed(),
        accounts_changed.to_string().cyan(),
        slots_changed.to_string().cyan(),
    );
}

/// Print a warning when block processing time is approaching the block interval.
///
/// Fires when `elapsed_ms >= 80% of interval_ms`.
pub fn print_block_time_budget_warning(block_num: u64, elapsed_ms: u64, interval_ms: u64) {
    println!(
        "  {} Block #{}: processing took {}ms (budget: {}ms — {:.0}% used)",
        "WARN".yellow().bold(),
        block_num.to_string().cyan(),
        elapsed_ms.to_string().yellow(),
        interval_ms.to_string().dimmed(),
        elapsed_ms as f64 / interval_ms as f64 * 100.0,
    );
}

// ── Tests ───────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_format_interval_sub_second() {
        assert_eq!(format_interval(Duration::from_millis(500)), "500ms");
        assert_eq!(format_interval(Duration::from_millis(100)), "100ms");
        assert_eq!(format_interval(Duration::from_millis(200)), "200ms");
    }

    #[test]
    fn test_format_interval_whole_seconds() {
        assert_eq!(format_interval(Duration::from_secs(1)), "1s");
        assert_eq!(format_interval(Duration::from_secs(2)), "2s");
        assert_eq!(format_interval(Duration::from_secs(12)), "12s");
    }

    #[test]
    fn test_format_interval_fractional_seconds() {
        // 1500ms = 1.5s
        let d = Duration::from_millis(1500);
        let s = format_interval(d);
        assert!(s.contains("1.5"), "expected 1.5s, got {s}");
    }

    #[test]
    fn test_format_interval_zero() {
        assert_eq!(format_interval(Duration::ZERO), "0ms");
    }
}

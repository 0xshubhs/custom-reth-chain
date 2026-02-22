use clap::Parser;
use std::path::PathBuf;

/// CLI arguments for the POA node
#[derive(Parser, Debug)]
#[command(name = "meowchain", about = "Meowchain POA Node")]
pub struct Cli {
    /// Chain ID for the network
    #[arg(long, default_value = "9323310")]
    pub chain_id: u64,

    /// Block production interval in seconds (Phase 2: default 1s for MegaETH-inspired throughput)
    #[arg(long, default_value = "1")]
    pub block_time: u64,

    /// Data directory for chain storage
    #[arg(long, default_value = "data")]
    pub datadir: PathBuf,

    /// HTTP RPC listen address
    #[arg(long, default_value = "0.0.0.0")]
    pub http_addr: String,

    /// HTTP RPC port
    #[arg(long, default_value = "8545")]
    pub http_port: u16,

    /// WebSocket RPC listen address
    #[arg(long, default_value = "0.0.0.0")]
    pub ws_addr: String,

    /// WebSocket RPC port
    #[arg(long, default_value = "8546")]
    pub ws_port: u16,

    /// Signer private key (hex, without 0x prefix).
    /// Can also be set via SIGNER_KEY environment variable.
    #[arg(long, env = "SIGNER_KEY")]
    pub signer_key: Option<String>,

    /// Use production genesis configuration (chain ID 9323310)
    #[arg(long)]
    pub production: bool,

    /// Disable dev mode (no auto-mining)
    #[arg(long)]
    pub no_dev: bool,

    /// Override block gas limit (e.g., 100000000 for 100M, 1000000000 for 1B)
    #[arg(long)]
    pub gas_limit: Option<u64>,

    /// Enable eager mining: build block immediately when transactions arrive
    /// instead of waiting for block-time interval
    #[arg(long)]
    pub eager_mining: bool,

    /// Force interval-based block production even in production mode.
    /// Useful for testing: node uses production signing (97-byte extra_data, strict POA)
    /// but still auto-mines blocks at --block-time interval.
    #[arg(long)]
    pub mining: bool,

    /// P2P listener port for peer-to-peer connections.
    #[arg(long, default_value = "30303")]
    pub port: u16,

    /// Comma-separated bootnode enode URLs for peer discovery.
    /// Example: enode://pubkey@ip:port,enode://pubkey2@ip2:port2
    #[arg(long, value_delimiter = ',')]
    pub bootnodes: Option<Vec<String>>,

    /// Disable P2P peer discovery (useful for single-node testing).
    #[arg(long)]
    pub disable_discovery: bool,

    /// Maximum number of entries in the hot state cache (Phase 5).
    /// Caches governance contract storage reads (gas limit, signer list, etc.).
    /// Set to 0 to disable caching.
    #[arg(long, default_value = "1024")]
    pub cache_size: usize,

    /// Enable block production performance metrics logging every N blocks.
    /// Set to 0 to disable metrics output.
    #[arg(long, default_value = "10")]
    pub metrics_interval: u64,

    /// Maximum deployed contract code size in bytes (Phase 2).
    ///
    /// Ethereum mainnet default is 24,576 bytes (EIP-170).
    /// Increase to allow larger contracts (e.g. 524288 = 512KB).
    /// Override is applied to the EVM via revm's `limit_contract_code_size`.
    /// Set to 0 to use the Ethereum default (24,576 bytes).
    #[arg(long, default_value = "0")]
    pub max_contract_size: usize,

    /// Sub-second block production interval in milliseconds (Phase 2.14).
    ///
    /// When set to a non-zero value, overrides `--block-time` with millisecond precision.
    /// Enables 500ms, 200ms, or even 100ms blocks on fast hardware.
    ///
    /// Examples:
    ///   `--block-time-ms 500`   → 500ms blocks (2 blocks/s)
    ///   `--block-time-ms 200`   → 200ms blocks (5 blocks/s)
    ///   `--block-time-ms 100`   → 100ms blocks (10 blocks/s)
    ///
    /// Set to 0 (default) to use the `--block-time` value in seconds.
    #[arg(long, default_value = "0")]
    pub block_time_ms: u64,

    /// Gas cost per non-zero calldata byte (Phase 2, range 1–16).
    ///
    /// Ethereum mainnet charges 16 gas/byte for non-zero calldata (EIP-2028).
    /// A POA chain can reduce this to increase calldata-heavy throughput.
    /// Default is 4 (same cost as zero bytes), effectively making calldata cheap.
    /// Set to 16 to disable the discount and match Ethereum mainnet behaviour.
    #[arg(long, default_value = "4", value_parser = clap::value_parser!(u64).range(1..=16))]
    pub calldata_gas: u64,
}

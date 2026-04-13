use alloy_primitives::Address;
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

    /// Enable zero-gas mode: transactions cost no gas fees.
    ///
    /// Sets genesis base fee to 0, disables EIP-1559 base fee validation in the EVM,
    /// and allows transactions with `gasPrice: 0` / `maxFeePerGas: 0`.
    /// Gas is still tracked for execution limits (preventing infinite loops),
    /// but users pay nothing. Common for private/consortium POA chains.
    #[arg(long)]
    pub zero_gas: bool,

    // ── Production-grade RPC & observability flags ────────────────────
    /// Enable Prometheus metrics endpoint.
    ///
    /// Uses Reth's built-in metrics infrastructure. The endpoint is served
    /// at `http://0.0.0.0:<metrics-port>/metrics` in Prometheus exposition format.
    #[arg(long = "enable-metrics")]
    pub metrics: bool,

    /// Prometheus metrics listen port (requires --enable-metrics).
    #[arg(long, default_value = "9001")]
    pub metrics_port: u16,

    /// Comma-separated list of allowed CORS origins for the HTTP RPC server.
    ///
    /// Use "*" to allow all origins. Default: none (no CORS headers).
    /// Example: `--http-corsdomain "http://localhost:3000,https://app.example.com"`
    #[arg(long)]
    pub http_corsdomain: Option<String>,

    /// Comma-separated list of HTTP RPC API modules to enable.
    ///
    /// Available: eth, net, web3, debug, txpool, admin, trace.
    /// The `meow` namespace is always added automatically.
    /// `txpool` is enabled by default — Blockscout's indexer and some
    /// wallet flows rely on `txpool_content` / `txpool_status`.
    #[arg(long, default_value = "eth,net,web3,txpool")]
    pub http_api: String,

    /// Comma-separated list of WebSocket RPC API modules to enable.
    ///
    /// Available: eth, net, web3, debug, txpool, admin, trace.
    /// The `meow` namespace is always added automatically.
    /// `txpool` is enabled by default — matches the HTTP default.
    #[arg(long, default_value = "eth,net,web3,txpool")]
    pub ws_api: String,

    /// Enable structured JSON logging instead of human-readable output.
    ///
    /// Useful for log aggregation systems (ELK, Loki, Datadog, etc.).
    /// When enabled, all log output is emitted as newline-delimited JSON.
    #[arg(long)]
    pub log_json: bool,

    /// Maximum number of concurrent RPC connections (HTTP + WS combined).
    ///
    /// Set to 0 for unlimited. Default matches Reth's built-in default (500).
    #[arg(long, default_value = "500")]
    pub rpc_max_connections: u32,

    /// Maximum RPC request payload size in megabytes.
    ///
    /// Applies to both HTTP and WebSocket requests. Increase for large
    /// eth_call payloads or batch requests.
    #[arg(long, default_value = "15")]
    pub rpc_max_request_size: u32,

    /// Maximum RPC response payload size in megabytes.
    ///
    /// Applies to both HTTP and WebSocket responses. Increase for large
    /// trace responses or debug_traceBlock results.
    #[arg(long, default_value = "160")]
    pub rpc_max_response_size: u32,

    /// Enable archive mode (keep all historical state).
    ///
    /// By default, Reth prunes old state. Archive mode disables pruning
    /// so all historical state is available for queries like eth_getBalance
    /// at arbitrary block numbers, debug_traceTransaction, etc.
    /// Warning: requires significantly more disk space.
    #[arg(long)]
    pub archive: bool,

    /// Gas price oracle: number of recent blocks to sample for gas estimation.
    ///
    /// Higher values give smoother estimates but increase computation.
    /// Used by eth_gasPrice and eth_feeHistory.
    #[arg(long, default_value = "20")]
    pub gpo_blocks: u32,

    /// Gas price oracle: percentile of sampled gas prices to report.
    ///
    /// Lower values suggest cheaper (but slower) transactions,
    /// higher values suggest faster (but more expensive) transactions.
    /// Range: 0-100.
    #[arg(long, default_value = "60")]
    pub gpo_percentile: u32,

    /// Prefund an address with a custom balance at genesis (repeatable).
    ///
    /// Format: `<address>:<amount>` where amount is either raw wei (e.g. `1000000000000000000`)
    /// or a suffix-annotated value: `100eth`, `1000000eth`, `1e25wei`.
    /// Only applied on first boot — genesis is baked into the DB, so changing this
    /// after initial start requires wiping the datadir.
    ///
    /// Example: `--fund 0xabc...:1000000eth --fund 0xdef...:5000eth`
    #[arg(long, value_delimiter = ',')]
    pub fund: Vec<String>,

    /// Override the balance of every dev-prefunded account (20 accounts in dev mode,
    /// 8 in production mode) with this amount.
    ///
    /// Accepts the same suffix format as `--fund`: `1000000eth`, `1e24wei`, or raw wei.
    /// Applied before `--fund`, so `--fund` entries can still bump specific addresses higher.
    /// Only applied on first boot — see `--fund` for the wipe caveat.
    #[arg(long)]
    pub prefund_all: Option<String>,

    /// Infinite-fund an address via EIP-4895 withdrawals (repeatable).
    ///
    /// On every block, the payload builder injects a withdrawal crediting this
    /// address with `9223372036` gwei (~9.2B ETH) before inner block building.
    /// Withdrawals are protocol-level balance credits — they create value out
    /// of thin air, exactly like beacon-chain validator rewards on mainnet.
    ///
    /// Unlike `--fund` / `--prefund-all`, this works on a LIVE chain with an
    /// existing datadir — no wipe required. Take effect starts from the next
    /// block built after node restart.
    ///
    /// Example: `--infinite-fund 0xabc... --infinite-fund 0xdef...`
    #[arg(long, value_delimiter = ',')]
    pub infinite_fund: Vec<Address>,
}

//! Performance metrics for block production and chain health monitoring.
//!
//! Phase 5 performance engineering: tracks block production timing, TPS,
//! gas throughput, and cache efficiency. Used by the block monitoring task
//! in `main.rs` and by `PoaPayloadBuilder` for profiling.
//!
//! # Metrics collected
//! - Block production latency (build time + sign time)
//! - Transactions per second (rolling window)
//! - Gas throughput (gas/second, rolling window)
//! - Cache hit/miss rates (from `cache::CacheStats`)
//! - Signer turn statistics (in-turn vs out-of-turn blocks)
//!
//! # Design
//! Uses `std::sync::atomic` counters for thread-safe updates without locking.
//! Heavy operations (window computation) acquire a `Mutex` only on read.

use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

// ── Per-block metrics ─────────────────────────────────────────────────────────

/// Timing and statistics for a single block.
#[derive(Debug, Clone, Copy)]
pub struct BlockMetrics {
    /// Block number.
    pub block_number: u64,
    /// Number of transactions in the block.
    pub tx_count: usize,
    /// Gas consumed.
    pub gas_used: u64,
    /// Time taken to build (assemble transactions + compute state root).
    pub build_duration: Duration,
    /// Time taken to sign the block header.
    pub sign_duration: Duration,
    /// Whether this block was produced in-turn by the expected signer.
    pub in_turn: bool,
}

impl BlockMetrics {
    /// Total time from build start to signed block.
    #[inline]
    pub fn total_duration(&self) -> Duration {
        self.build_duration + self.sign_duration
    }

    /// Effective TPS for this single block (based on total duration).
    ///
    /// Returns 0.0 for zero-duration blocks (instant builds in tests).
    pub fn tps(&self) -> f64 {
        let ms = self.total_duration().as_millis();
        if ms == 0 || self.tx_count == 0 {
            0.0
        } else {
            self.tx_count as f64 / (ms as f64 / 1000.0)
        }
    }

    /// Gas throughput in gas/second.
    pub fn gas_per_second(&self) -> f64 {
        let ms = self.total_duration().as_millis();
        if ms == 0 {
            0.0
        } else {
            self.gas_used as f64 / (ms as f64 / 1000.0)
        }
    }

    /// One-line summary for logging.
    pub fn summary(&self) -> String {
        format!(
            "block={} txs={} gas={} build={:.1}ms sign={:.1}ms total={:.1}ms in_turn={}",
            self.block_number,
            self.tx_count,
            self.gas_used,
            self.build_duration.as_millis(),
            self.sign_duration.as_millis(),
            self.total_duration().as_millis(),
            self.in_turn,
        )
    }
}

// ── Sliding window ────────────────────────────────────────────────────────────

/// Fixed-size circular buffer for computing rolling statistics.
#[derive(Debug)]
struct SlidingWindow<T> {
    buf: Vec<T>,
    head: usize,
    count: usize,
    capacity: usize,
}

impl<T: Copy + Default> SlidingWindow<T> {
    fn new(capacity: usize) -> Self {
        assert!(capacity > 0);
        Self {
            buf: vec![T::default(); capacity],
            head: 0,
            count: 0,
            capacity,
        }
    }

    #[inline]
    fn push(&mut self, value: T) {
        self.buf[self.head] = value;
        self.head = (self.head + 1) % self.capacity;
        if self.count < self.capacity {
            self.count += 1;
        }
    }

    #[inline]
    fn values(&self) -> &[T] {
        &self.buf[..self.count]
    }

    #[inline]
    fn len(&self) -> usize {
        self.count
    }

    #[inline]
    fn is_empty(&self) -> bool {
        self.count == 0
    }
}

// ── ChainMetrics ──────────────────────────────────────────────────────────────

/// Snapshot of aggregated chain performance metrics.
#[derive(Debug, Clone, Copy, Default)]
pub struct MetricsSnapshot {
    /// Total blocks produced since the node started.
    pub total_blocks: u64,
    /// Total transactions processed since the node started.
    pub total_txs: u64,
    /// Total gas consumed since the node started.
    pub total_gas: u64,
    /// Blocks produced in-turn.
    pub in_turn_blocks: u64,
    /// Blocks produced out-of-turn.
    pub out_of_turn_blocks: u64,
    /// Rolling average TPS over the last N blocks.
    pub rolling_tps: f64,
    /// Rolling average gas/second over the last N blocks.
    pub rolling_gas_per_second: f64,
    /// Rolling average block build time (ms).
    pub rolling_build_ms: f64,
    /// Rolling average block sign time (ms).
    pub rolling_sign_ms: f64,
}

impl MetricsSnapshot {
    /// Fraction of blocks produced in-turn.
    #[inline]
    pub fn in_turn_rate(&self) -> f64 {
        if self.total_blocks == 0 {
            0.0
        } else {
            self.in_turn_blocks as f64 / self.total_blocks as f64
        }
    }

    /// Formatted multi-line report for logging.
    pub fn report(&self) -> String {
        format!(
            "=== Chain Metrics ===\n\
             Blocks:       {} (in-turn: {} / {:.1}%)\n\
             Transactions: {}\n\
             Gas total:    {}\n\
             Rolling TPS:  {:.1}\n\
             Rolling gas/s:{:.0}\n\
             Build time:   {:.1}ms avg\n\
             Sign time:    {:.1}ms avg",
            self.total_blocks,
            self.in_turn_blocks,
            self.in_turn_rate() * 100.0,
            self.total_txs,
            self.total_gas,
            self.rolling_tps,
            self.rolling_gas_per_second,
            self.rolling_build_ms,
            self.rolling_sign_ms,
        )
    }
}

/// Thread-safe chain performance metrics accumulator.
///
/// Uses atomics for hot-path counters and a `Mutex<SlidingWindow>` only
/// for the rolling-window computations read infrequently.
pub struct ChainMetrics {
    // Atomic counters (written on every block)
    total_blocks: AtomicU64,
    total_txs: AtomicU64,
    total_gas: AtomicU64,
    in_turn_blocks: AtomicU64,
    out_of_turn_blocks: AtomicU64,

    // Rolling windows (guarded by mutex, written on every block, read on demand)
    window: Mutex<BlockWindow>,

    /// Window size (number of recent blocks to average over).
    window_size: usize,
}

struct BlockWindow {
    build_ms: SlidingWindow<u64>,
    sign_ms: SlidingWindow<u64>,
    tx_counts: SlidingWindow<u64>,
    gas_used: SlidingWindow<u64>,
    durations_ms: SlidingWindow<u64>,
}

impl BlockWindow {
    fn new(size: usize) -> Self {
        Self {
            build_ms: SlidingWindow::new(size),
            sign_ms: SlidingWindow::new(size),
            tx_counts: SlidingWindow::new(size),
            gas_used: SlidingWindow::new(size),
            durations_ms: SlidingWindow::new(size),
        }
    }
}

impl ChainMetrics {
    /// Create a new metrics tracker with a rolling window of `window_size` blocks.
    pub fn new(window_size: usize) -> Self {
        assert!(window_size > 0);
        Self {
            total_blocks: AtomicU64::new(0),
            total_txs: AtomicU64::new(0),
            total_gas: AtomicU64::new(0),
            in_turn_blocks: AtomicU64::new(0),
            out_of_turn_blocks: AtomicU64::new(0),
            window: Mutex::new(BlockWindow::new(window_size)),
            window_size,
        }
    }

    /// Create with default window of 64 blocks.
    pub fn default_window() -> Arc<Self> {
        Arc::new(Self::new(64))
    }

    /// Record a completed block. Call from the block monitoring task.
    pub fn record_block(&self, metrics: &BlockMetrics) {
        self.total_blocks.fetch_add(1, Ordering::Relaxed);
        self.total_txs
            .fetch_add(metrics.tx_count as u64, Ordering::Relaxed);
        self.total_gas
            .fetch_add(metrics.gas_used, Ordering::Relaxed);

        if metrics.in_turn {
            self.in_turn_blocks.fetch_add(1, Ordering::Relaxed);
        } else {
            self.out_of_turn_blocks.fetch_add(1, Ordering::Relaxed);
        }

        if let Ok(mut w) = self.window.lock() {
            w.build_ms.push(metrics.build_duration.as_millis() as u64);
            w.sign_ms.push(metrics.sign_duration.as_millis() as u64);
            w.tx_counts.push(metrics.tx_count as u64);
            w.gas_used.push(metrics.gas_used);
            w.durations_ms
                .push(metrics.total_duration().as_millis() as u64);
        }
    }

    /// Take a snapshot of all metrics (momentary read — values may change concurrently).
    pub fn snapshot(&self) -> MetricsSnapshot {
        let total_blocks = self.total_blocks.load(Ordering::Relaxed);
        let total_txs = self.total_txs.load(Ordering::Relaxed);
        let total_gas = self.total_gas.load(Ordering::Relaxed);
        let in_turn_blocks = self.in_turn_blocks.load(Ordering::Relaxed);
        let out_of_turn_blocks = self.out_of_turn_blocks.load(Ordering::Relaxed);

        let (rolling_tps, rolling_gas_per_second, rolling_build_ms, rolling_sign_ms) =
            if let Ok(w) = self.window.lock() {
                let build_ms = average(&w.build_ms);
                let sign_ms = average(&w.sign_ms);
                let total_ms = average(&w.durations_ms);
                let avg_txs = average(&w.tx_counts);
                let avg_gas = average(&w.gas_used);

                let tps = if total_ms > 0.0 {
                    avg_txs / (total_ms / 1000.0)
                } else {
                    0.0
                };
                let gps = if total_ms > 0.0 {
                    avg_gas / (total_ms / 1000.0)
                } else {
                    0.0
                };

                (tps, gps, build_ms, sign_ms)
            } else {
                (0.0, 0.0, 0.0, 0.0)
            };

        MetricsSnapshot {
            total_blocks,
            total_txs,
            total_gas,
            in_turn_blocks,
            out_of_turn_blocks,
            rolling_tps,
            rolling_gas_per_second,
            rolling_build_ms,
            rolling_sign_ms,
        }
    }

    /// Total blocks recorded so far.
    #[inline]
    pub fn total_blocks(&self) -> u64 {
        self.total_blocks.load(Ordering::Relaxed)
    }

    /// Total transactions processed.
    #[inline]
    pub fn total_txs(&self) -> u64 {
        self.total_txs.load(Ordering::Relaxed)
    }

    /// Configured rolling window size.
    #[inline]
    pub fn window_size(&self) -> usize {
        self.window_size
    }
}

// ── Timer helper ──────────────────────────────────────────────────────────────

/// A simple RAII timer for measuring block build and sign phases.
///
/// ```ignore
/// let timer = PhaseTimer::start();
/// // ... do work ...
/// let build_duration = timer.elapsed();
/// ```
#[derive(Clone, Copy)]
pub struct PhaseTimer {
    start: Instant,
}

impl PhaseTimer {
    /// Start timing.
    #[inline]
    pub fn start() -> Self {
        Self {
            start: Instant::now(),
        }
    }

    /// Return elapsed duration since `start()`.
    #[inline]
    pub fn elapsed(&self) -> Duration {
        self.start.elapsed()
    }

    /// Return elapsed milliseconds.
    #[inline]
    pub fn elapsed_ms(&self) -> u64 {
        self.start.elapsed().as_millis() as u64
    }
}

// ── Utilities ─────────────────────────────────────────────────────────────────

fn average(window: &SlidingWindow<u64>) -> f64 {
    if window.is_empty() {
        return 0.0;
    }
    // `.copied()` avoids a layer of reference indirection for the `Copy` type
    // `u64`; the compiler can then fold the sum into a tighter loop.
    let sum: u64 = window.values().iter().copied().sum();
    sum as f64 / window.len() as f64
}

#[cfg(test)]
mod tests {
    use super::*;

    fn make_block(block_number: u64, tx_count: usize, gas: u64, in_turn: bool) -> BlockMetrics {
        BlockMetrics {
            block_number,
            tx_count,
            gas_used: gas,
            build_duration: Duration::from_millis(10),
            sign_duration: Duration::from_millis(2),
            in_turn,
        }
    }

    // ── BlockMetrics ──────────────────────────────────────────────────────────

    #[test]
    fn test_block_metrics_total_duration() {
        let m = BlockMetrics {
            block_number: 1,
            tx_count: 10,
            gas_used: 210_000,
            build_duration: Duration::from_millis(8),
            sign_duration: Duration::from_millis(2),
            in_turn: true,
        };
        assert_eq!(m.total_duration(), Duration::from_millis(10));
    }

    #[test]
    fn test_block_metrics_tps_zero_for_no_txs() {
        let m = make_block(1, 0, 0, true);
        assert_eq!(m.tps(), 0.0);
    }

    #[test]
    fn test_block_metrics_tps_nonzero() {
        let m = BlockMetrics {
            block_number: 1,
            tx_count: 100,
            gas_used: 2_100_000,
            build_duration: Duration::from_millis(800),
            sign_duration: Duration::from_millis(200),
            in_turn: true,
        };
        // 100 txs / 1.0 s = 100 TPS
        assert!((m.tps() - 100.0).abs() < 1.0);
    }

    #[test]
    fn test_block_metrics_gas_per_second() {
        let m = BlockMetrics {
            block_number: 1,
            tx_count: 1,
            gas_used: 21_000,
            build_duration: Duration::from_millis(500),
            sign_duration: Duration::from_millis(500),
            in_turn: true,
        };
        // 21000 gas / 1.0 s = 21000 gas/s
        assert!((m.gas_per_second() - 21_000.0).abs() < 100.0);
    }

    #[test]
    fn test_block_metrics_summary_contains_block_number() {
        let m = make_block(42, 5, 105_000, true);
        let s = m.summary();
        assert!(s.contains("block=42"));
        assert!(s.contains("txs=5"));
        assert!(s.contains("in_turn=true"));
    }

    // ── SlidingWindow ─────────────────────────────────────────────────────────

    #[test]
    fn test_sliding_window_empty() {
        let w: SlidingWindow<u64> = SlidingWindow::new(5);
        assert_eq!(w.len(), 0);
        assert!(w.is_empty());
    }

    #[test]
    fn test_sliding_window_fills_up() {
        let mut w: SlidingWindow<u64> = SlidingWindow::new(3);
        w.push(1);
        w.push(2);
        w.push(3);
        assert_eq!(w.len(), 3);
        assert_eq!(w.values(), &[1, 2, 3]);
    }

    #[test]
    fn test_sliding_window_evicts_oldest() {
        let mut w: SlidingWindow<u64> = SlidingWindow::new(3);
        w.push(10);
        w.push(20);
        w.push(30);
        w.push(40); // evicts 10
        assert_eq!(w.len(), 3);
        // Window contains 20, 30, 40 in circular order; values() returns filled portion
        let vals = w.values();
        assert!(vals.contains(&20));
        assert!(vals.contains(&30));
        assert!(vals.contains(&40));
        assert!(!vals.contains(&10));
    }

    // ── ChainMetrics ──────────────────────────────────────────────────────────

    #[test]
    fn test_chain_metrics_initial_state() {
        let m = ChainMetrics::new(10);
        assert_eq!(m.total_blocks(), 0);
        assert_eq!(m.total_txs(), 0);
        let snap = m.snapshot();
        assert_eq!(snap.total_blocks, 0);
        assert_eq!(snap.rolling_tps, 0.0);
    }

    #[test]
    fn test_chain_metrics_records_single_block() {
        let metrics = ChainMetrics::new(10);
        metrics.record_block(&make_block(1, 5, 105_000, true));

        assert_eq!(metrics.total_blocks(), 1);
        assert_eq!(metrics.total_txs(), 5);

        let snap = metrics.snapshot();
        assert_eq!(snap.total_gas, 105_000);
        assert_eq!(snap.in_turn_blocks, 1);
        assert_eq!(snap.out_of_turn_blocks, 0);
    }

    #[test]
    fn test_chain_metrics_records_multiple_blocks() {
        let metrics = ChainMetrics::new(10);
        for i in 1..=10u64 {
            metrics.record_block(&make_block(i, 10, 210_000, i % 3 != 0));
        }

        assert_eq!(metrics.total_blocks(), 10);
        assert_eq!(metrics.total_txs(), 100);
    }

    #[test]
    fn test_chain_metrics_in_turn_rate() {
        let metrics = ChainMetrics::new(10);
        metrics.record_block(&make_block(1, 1, 21_000, true));
        metrics.record_block(&make_block(2, 1, 21_000, true));
        metrics.record_block(&make_block(3, 1, 21_000, false));

        let snap = metrics.snapshot();
        assert_eq!(snap.in_turn_blocks, 2);
        assert_eq!(snap.out_of_turn_blocks, 1);
        assert!((snap.in_turn_rate() - 2.0 / 3.0).abs() < 1e-9);
    }

    #[test]
    fn test_chain_metrics_default_window_arc() {
        let m = ChainMetrics::default_window();
        assert_eq!(m.window_size(), 64);
        assert_eq!(m.total_blocks(), 0);
    }

    #[test]
    fn test_metrics_snapshot_in_turn_rate_zero_when_no_blocks() {
        let snap = MetricsSnapshot::default();
        assert_eq!(snap.in_turn_rate(), 0.0);
    }

    #[test]
    fn test_metrics_report_contains_key_info() {
        let snap = MetricsSnapshot {
            total_blocks: 100,
            in_turn_blocks: 90,
            total_txs: 1000,
            total_gas: 21_000_000,
            ..Default::default()
        };
        let report = snap.report();
        assert!(report.contains("100"));
        assert!(report.contains("1000"));
    }

    // ── PhaseTimer ────────────────────────────────────────────────────────────

    #[test]
    fn test_phase_timer_elapsed_nonnegative() {
        let timer = PhaseTimer::start();
        // Do a tiny bit of work so the elapsed time is > 0 even on fast CPUs.
        let _sum: u64 = (0u64..1_000).sum();
        let elapsed = timer.elapsed();
        // Elapsed must be non-negative (Duration is always ≥ 0) and < 1s on any machine.
        assert!(elapsed.as_secs() < 1, "timer should be well under 1 second");
    }

    #[test]
    fn test_phase_timer_elapsed_ms() {
        let timer = PhaseTimer::start();
        // Sleep not available in unit tests but elapsed_ms should return a u64
        let ms = timer.elapsed_ms();
        assert!(ms < 1000, "test should be fast");
    }

    // ── average helper ────────────────────────────────────────────────────────

    #[test]
    fn test_average_empty_window() {
        let w: SlidingWindow<u64> = SlidingWindow::new(5);
        assert_eq!(average(&w), 0.0);
    }

    #[test]
    fn test_average_with_values() {
        let mut w: SlidingWindow<u64> = SlidingWindow::new(5);
        w.push(10);
        w.push(20);
        w.push(30);
        assert!((average(&w) - 20.0).abs() < 1e-9);
    }
}

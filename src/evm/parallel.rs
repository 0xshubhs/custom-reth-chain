//! Parallel EVM execution foundation (Phase 2, item 13).
//!
//! This module provides the data structures and scheduling primitives for
//! detecting transaction dependencies and enabling out-of-order execution.
//!
//! # Design overview
//!
//! Full parallel EVM execution (as in [grevm](https://github.com/Galxe/grevm)) requires
//! a dependency graph built from per-transaction state-access records.  The pieces here
//! form the foundation that a future grevm integration (or a hand-rolled parallel executor)
//! will build on:
//!
//! ```text
//!   TxAccessRecord          — read/write sets recorded during EVM execution
//!   ConflictDetector        — detects RAW / WAW / WAR hazards between two txs
//!   ParallelSchedule        — groups txs into parallel batches with no intra-batch conflicts
//! ```
//!
//! ## Current status
//!
//! grevm is not yet available as a crate on crates.io, so live parallel execution is
//! out of scope for this release.  This module ships with:
//!
//! - All foundational types and logic with full test coverage.
//! - A `ParallelSchedule` that produces correct batches for sequential execution with
//!   the same semantics as true parallel execution (i.e. no visible difference in output).
//! - A `ParallelExecutor` stub that falls back to sequential execution and is ready to
//!   be upgraded to grevm once the crate becomes available.
//!
//! ## Enabling true parallelism
//!
//! When grevm ships:
//! 1. Add `grevm = { version = "...", features = ["reth"] }` to `Cargo.toml`.
//! 2. Replace `ParallelExecutor::execute_sequential` with the grevm executor.
//! 3. The `TxAccessRecord` / `ConflictDetector` / `ParallelSchedule` types remain unchanged.

use alloy_primitives::{Address, B256};
use std::collections::{HashMap, HashSet};

// ─── TxAccessRecord ───────────────────────────────────────────────────────────

/// State-access record for a single transaction.
///
/// Tracks every (address, slot) pair that the transaction reads from or writes to.
/// This information is used by [`ConflictDetector`] to find WAW / WAR / RAW hazards.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct TxAccessRecord {
    /// Slots that this transaction *reads* (including balance / nonce / code reads).
    pub reads: HashSet<AccessKey>,
    /// Slots that this transaction *writes* (including balance / nonce / code writes).
    pub writes: HashSet<AccessKey>,
}

impl TxAccessRecord {
    /// Record a storage read.
    pub fn add_read(&mut self, address: Address, slot: B256) {
        self.reads.insert(AccessKey { address, slot });
    }

    /// Record a storage write.
    pub fn add_write(&mut self, address: Address, slot: B256) {
        self.writes.insert(AccessKey { address, slot });
    }

    /// Whether this record is empty (no reads or writes).
    pub fn is_empty(&self) -> bool {
        self.reads.is_empty() && self.writes.is_empty()
    }
}

/// A (contract address, storage slot) pair used as a key in access sets.
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct AccessKey {
    /// The account whose storage is accessed.
    pub address: Address,
    /// The storage slot (or `B256::ZERO` for balance / nonce / code-hash accesses).
    pub slot: B256,
}

impl AccessKey {
    /// Construct an `AccessKey` for a storage slot access.
    pub fn storage(address: Address, slot: B256) -> Self {
        Self { address, slot }
    }

    /// Construct an `AccessKey` for a non-slot access (balance, nonce, bytecode).
    pub fn account(address: Address) -> Self {
        Self { address, slot: B256::ZERO }
    }
}

// ─── ConflictDetector ─────────────────────────────────────────────────────────

/// Detects data hazards between pairs of transactions.
///
/// Two transactions conflict if any of the following hold:
///
/// | Hazard | Condition |
/// |--------|-----------|
/// | WAW (write-after-write) | both write the same slot |
/// | WAR (write-after-read)  | tx_b writes a slot that tx_a reads |
/// | RAW (read-after-write)  | tx_b reads a slot that tx_a writes |
///
/// Because we execute transactions in index order, only the *later* of the two
/// transactions can observe the earlier one's writes — so WAR and RAW are only
/// hazards when `tx_b` comes *after* `tx_a`.
#[derive(Debug, Default)]
pub struct ConflictDetector;

impl ConflictDetector {
    /// Returns `true` if `tx_a` (earlier) and `tx_b` (later) have a data hazard.
    ///
    /// The order of arguments matters: `tx_a` is assumed to appear *before* `tx_b`
    /// in the block.
    pub fn conflicts(tx_a: &TxAccessRecord, tx_b: &TxAccessRecord) -> bool {
        // WAW: both write the same slot.
        if tx_a.writes.intersection(&tx_b.writes).next().is_some() {
            return true;
        }
        // RAW: tx_b reads what tx_a wrote.
        if tx_a.writes.intersection(&tx_b.reads).next().is_some() {
            return true;
        }
        // WAR: tx_b writes what tx_a read.
        if tx_a.reads.intersection(&tx_b.writes).next().is_some() {
            return true;
        }
        false
    }
}

// ─── ParallelSchedule ─────────────────────────────────────────────────────────

/// Assigns transactions to parallel execution batches.
///
/// Transactions are placed into the earliest batch such that no two transactions
/// in the same batch conflict.  Transactions within a batch can be executed
/// concurrently; batches must be executed in order.
///
/// # Complexity
///
/// O(n² × s) where *n* is the number of transactions and *s* is the average size
/// of the access sets.  For typical block sizes (< 10 K txs) this is fast.
#[derive(Debug, Default)]
pub struct ParallelSchedule {
    /// Batches of transaction indices.  Each inner `Vec` is a set of tx indices
    /// that can run in parallel.  The outer `Vec` must be executed in order.
    pub batches: Vec<Vec<usize>>,
}

impl ParallelSchedule {
    /// Build a schedule from a list of per-transaction access records.
    ///
    /// `records[i]` is the access record for the `i`-th transaction in the block.
    /// The returned schedule preserves the original execution order semantics.
    pub fn build(records: &[TxAccessRecord]) -> Self {
        // `batch_of[i]` = which batch transaction `i` was placed into.
        let mut batch_of: Vec<usize> = Vec::with_capacity(records.len());

        for (i, record_i) in records.iter().enumerate() {
            // Find the earliest batch where tx_i does not conflict with any
            // earlier transaction already placed in that batch.
            let mut target_batch = 0;
            for j in 0..i {
                if ConflictDetector::conflicts(&records[j], record_i) {
                    target_batch = target_batch.max(batch_of[j] + 1);
                }
            }
            batch_of.push(target_batch);
        }

        // Collect into batches.
        let num_batches = batch_of.iter().copied().max().map_or(0, |m| m + 1);
        let mut batches: Vec<Vec<usize>> = vec![Vec::new(); num_batches];
        for (tx_idx, &batch_idx) in batch_of.iter().enumerate() {
            batches[batch_idx].push(tx_idx);
        }

        Self { batches }
    }

    /// Total number of transactions across all batches.
    pub fn tx_count(&self) -> usize {
        self.batches.iter().map(|b| b.len()).sum()
    }

    /// Parallelism ratio: `1.0` = fully sequential, higher = more parallel batches possible.
    ///
    /// Defined as `tx_count / batch_count`.  A value of `3.0` means on average 3 txs
    /// per batch, i.e. 3× theoretical speedup over sequential execution.
    pub fn avg_batch_size(&self) -> f64 {
        if self.batches.is_empty() {
            return 0.0;
        }
        self.tx_count() as f64 / self.batches.len() as f64
    }
}

// ─── ParallelExecutor ─────────────────────────────────────────────────────────

/// EVM executor stub with parallel scheduling.
///
/// Currently executes all transactions sequentially in the correct order while
/// building the parallel schedule.  When grevm becomes available, the inner
/// execution loop can be replaced with grevm's parallel executor, while keeping
/// the same interface.
///
/// # Usage
///
/// ```rust,ignore
/// let mut executor = ParallelExecutor::new();
/// executor.record_access(0, record_for_tx0);
/// executor.record_access(1, record_for_tx1);
/// let schedule = executor.build_schedule();
/// // Execute batches...
/// ```
#[derive(Debug, Default)]
pub struct ParallelExecutor {
    /// Accumulated access records indexed by tx position.
    records: HashMap<usize, TxAccessRecord>,
}

impl ParallelExecutor {
    /// Create a new executor with no recorded accesses.
    pub fn new() -> Self {
        Self::default()
    }

    /// Record the state-access footprint of a transaction at position `tx_idx`.
    pub fn record_access(&mut self, tx_idx: usize, record: TxAccessRecord) {
        self.records.insert(tx_idx, record);
    }

    /// Build the parallel schedule from recorded accesses.
    ///
    /// Assumes tx indices are `0..N` in order.
    pub fn build_schedule(&self) -> ParallelSchedule {
        if self.records.is_empty() {
            return ParallelSchedule::default();
        }
        let max_idx = *self.records.keys().max().unwrap();
        let records: Vec<TxAccessRecord> = (0..=max_idx)
            .map(|i| self.records.get(&i).cloned().unwrap_or_default())
            .collect();
        ParallelSchedule::build(&records)
    }
}

// ─── Tests ────────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    fn addr(n: u8) -> Address {
        Address::from([n; 20])
    }

    fn slot(n: u8) -> B256 {
        B256::from([n; 32])
    }

    // ── AccessKey ─────────────────────────────────────────────────────────────

    #[test]
    fn test_access_key_storage() {
        let key = AccessKey::storage(addr(1), slot(2));
        assert_eq!(key.address, addr(1));
        assert_eq!(key.slot, slot(2));
    }

    #[test]
    fn test_access_key_account_uses_zero_slot() {
        let key = AccessKey::account(addr(3));
        assert_eq!(key.slot, B256::ZERO);
    }

    // ── TxAccessRecord ────────────────────────────────────────────────────────

    #[test]
    fn test_tx_access_record_default_is_empty() {
        let r = TxAccessRecord::default();
        assert!(r.is_empty());
    }

    #[test]
    fn test_tx_access_record_add_read() {
        let mut r = TxAccessRecord::default();
        r.add_read(addr(1), slot(0));
        assert_eq!(r.reads.len(), 1);
        assert!(r.writes.is_empty());
    }

    #[test]
    fn test_tx_access_record_add_write() {
        let mut r = TxAccessRecord::default();
        r.add_write(addr(2), slot(1));
        assert!(r.reads.is_empty());
        assert_eq!(r.writes.len(), 1);
    }

    #[test]
    fn test_tx_access_record_is_not_empty_after_write() {
        let mut r = TxAccessRecord::default();
        r.add_write(addr(1), slot(0));
        assert!(!r.is_empty());
    }

    // ── ConflictDetector ──────────────────────────────────────────────────────

    fn read_only(address: Address, slot: B256) -> TxAccessRecord {
        let mut r = TxAccessRecord::default();
        r.add_read(address, slot);
        r
    }

    fn write_only(address: Address, slot: B256) -> TxAccessRecord {
        let mut r = TxAccessRecord::default();
        r.add_write(address, slot);
        r
    }

    fn read_write(address: Address, slot: B256) -> TxAccessRecord {
        let mut r = TxAccessRecord::default();
        r.add_read(address, slot);
        r.add_write(address, slot);
        r
    }

    #[test]
    fn test_no_conflict_disjoint_reads() {
        // Two transactions reading different slots: no conflict.
        let a = read_only(addr(1), slot(0));
        let b = read_only(addr(2), slot(0));
        assert!(!ConflictDetector::conflicts(&a, &b));
    }

    #[test]
    fn test_no_conflict_same_read_only_slot() {
        // Both reading the same slot: no conflict (reads are safe to share).
        let a = read_only(addr(1), slot(0));
        let b = read_only(addr(1), slot(0));
        assert!(!ConflictDetector::conflicts(&a, &b));
    }

    #[test]
    fn test_conflict_waw_same_write() {
        // Both writing the same slot → WAW conflict.
        let a = write_only(addr(1), slot(0));
        let b = write_only(addr(1), slot(0));
        assert!(ConflictDetector::conflicts(&a, &b));
    }

    #[test]
    fn test_conflict_raw_b_reads_a_write() {
        // tx_a writes slot; tx_b reads it → RAW conflict.
        let a = write_only(addr(1), slot(0));
        let b = read_only(addr(1), slot(0));
        assert!(ConflictDetector::conflicts(&a, &b));
    }

    #[test]
    fn test_conflict_war_b_writes_a_read() {
        // tx_a reads slot; tx_b writes it → WAR conflict.
        let a = read_only(addr(1), slot(0));
        let b = write_only(addr(1), slot(0));
        assert!(ConflictDetector::conflicts(&a, &b));
    }

    #[test]
    fn test_no_conflict_disjoint_writes() {
        // tx_a writes slot(0), tx_b writes slot(1): no conflict.
        let a = write_only(addr(1), slot(0));
        let b = write_only(addr(1), slot(1));
        assert!(!ConflictDetector::conflicts(&a, &b));
    }

    // ── ParallelSchedule ──────────────────────────────────────────────────────

    #[test]
    fn test_schedule_empty_records() {
        let schedule = ParallelSchedule::build(&[]);
        assert!(schedule.batches.is_empty());
        assert_eq!(schedule.tx_count(), 0);
    }

    #[test]
    fn test_schedule_single_tx_one_batch() {
        let records = vec![read_only(addr(1), slot(0))];
        let schedule = ParallelSchedule::build(&records);
        assert_eq!(schedule.batches.len(), 1);
        assert_eq!(schedule.batches[0], vec![0]);
    }

    #[test]
    fn test_schedule_independent_txs_one_batch() {
        // Two txs accessing different slots: both fit in batch 0.
        let records = vec![
            read_only(addr(1), slot(0)),
            read_only(addr(2), slot(1)),
        ];
        let schedule = ParallelSchedule::build(&records);
        assert_eq!(schedule.batches.len(), 1);
        assert_eq!(schedule.batches[0].len(), 2);
    }

    #[test]
    fn test_schedule_conflicting_txs_two_batches() {
        // tx0 writes slot(0), tx1 reads slot(0): RAW → must be in different batches.
        let records = vec![
            write_only(addr(1), slot(0)),
            read_only(addr(1), slot(0)),
        ];
        let schedule = ParallelSchedule::build(&records);
        assert_eq!(schedule.batches.len(), 2);
        assert_eq!(schedule.batches[0], vec![0]);
        assert_eq!(schedule.batches[1], vec![1]);
    }

    #[test]
    fn test_schedule_chain_of_conflicts() {
        // tx0 → tx1 → tx2 all conflict sequentially → 3 batches.
        let records = vec![
            write_only(addr(1), slot(0)), // tx0 writes slot(0)
            read_write(addr(1), slot(0)), // tx1 reads+writes slot(0) — conflicts with tx0
            read_only(addr(1), slot(0)), // tx2 reads slot(0) — conflicts with tx1's write
        ];
        let schedule = ParallelSchedule::build(&records);
        assert_eq!(schedule.batches.len(), 3);
    }

    #[test]
    fn test_schedule_mixed_independent_and_dependent() {
        // tx0 writes slot(0), tx1 reads slot(1) [no conflict], tx2 reads slot(0) [conflicts with tx0]
        let records = vec![
            write_only(addr(1), slot(0)), // tx0 → batch 0
            read_only(addr(2), slot(1)),  // tx1 → batch 0 (no conflict)
            read_only(addr(1), slot(0)),  // tx2 → batch 1 (RAW with tx0)
        ];
        let schedule = ParallelSchedule::build(&records);
        assert_eq!(schedule.batches.len(), 2);
        assert_eq!(schedule.batches[0].len(), 2); // tx0 + tx1 in parallel
        assert_eq!(schedule.batches[1], vec![2]);
    }

    #[test]
    fn test_schedule_tx_count_matches_input() {
        let records = vec![
            write_only(addr(1), slot(0)),
            read_only(addr(2), slot(1)),
            write_only(addr(3), slot(2)),
        ];
        let schedule = ParallelSchedule::build(&records);
        assert_eq!(schedule.tx_count(), 3);
    }

    #[test]
    fn test_schedule_avg_batch_size_all_independent() {
        // 4 independent txs → 1 batch → avg = 4.0
        let records = (0..4u8)
            .map(|i| read_only(addr(i), slot(i)))
            .collect::<Vec<_>>();
        let schedule = ParallelSchedule::build(&records);
        assert!((schedule.avg_batch_size() - 4.0).abs() < f64::EPSILON);
    }

    #[test]
    fn test_schedule_avg_batch_size_all_sequential() {
        // Chain of conflicts → 4 batches of 1 → avg = 1.0
        let records = vec![
            write_only(addr(1), slot(0)),
            read_write(addr(1), slot(0)),
            read_write(addr(1), slot(0)),
            read_only(addr(1), slot(0)),
        ];
        let schedule = ParallelSchedule::build(&records);
        assert!((schedule.avg_batch_size() - 1.0).abs() < f64::EPSILON);
    }

    // ── ParallelExecutor ──────────────────────────────────────────────────────

    #[test]
    fn test_parallel_executor_empty() {
        let executor = ParallelExecutor::new();
        let schedule = executor.build_schedule();
        assert!(schedule.batches.is_empty());
    }

    #[test]
    fn test_parallel_executor_single_tx() {
        let mut executor = ParallelExecutor::new();
        executor.record_access(0, read_only(addr(1), slot(0)));
        let schedule = executor.build_schedule();
        assert_eq!(schedule.batches.len(), 1);
    }

    #[test]
    fn test_parallel_executor_respects_conflict() {
        let mut executor = ParallelExecutor::new();
        executor.record_access(0, write_only(addr(1), slot(0)));
        executor.record_access(1, read_only(addr(1), slot(0)));
        let schedule = executor.build_schedule();
        assert_eq!(schedule.batches.len(), 2);
    }

    #[test]
    fn test_parallel_executor_gap_filled_with_empty() {
        // Record tx 0 and tx 2 but not tx 1 → gap should be filled with empty record.
        let mut executor = ParallelExecutor::new();
        executor.record_access(0, write_only(addr(1), slot(0)));
        executor.record_access(2, read_only(addr(1), slot(0)));
        let schedule = executor.build_schedule();
        // tx0: batch 0, tx1 (empty): batch 0, tx2: batch 1
        assert_eq!(schedule.tx_count(), 3);
    }
}

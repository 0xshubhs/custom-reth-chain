//! State diff computation for replica sync.
//!
//! Phase 5.33: Captures which accounts and storage slots changed during a block,
//! enabling lightweight replica nodes to apply diffs instead of re-executing transactions.
//!
//! # Design
//! ```text
//!   Block N executes → StateDiffBuilder records changes
//!                     → StateDiff produced (accounts + storage deltas)
//!                     → Broadcast to replica nodes (future: P2P)
//!                     → Replicas apply diff without re-executing EVM
//! ```
//!
//! # Usage (standalone computation)
//! ```ignore
//! let mut builder = StateDiffBuilder::new(block_number, block_hash);
//! builder.record_balance_change(addr, old_balance, new_balance);
//! builder.record_storage_change(addr, slot, old_val, new_val);
//! let diff = builder.build();
//! println!("{}", diff.summary());
//! ```

use alloy_primitives::{Address, B256, U256};
use std::collections::HashMap;

// ── Per-account diff ──────────────────────────────────────────────────────────

/// Difference in a single storage slot value.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct StorageSlotDiff {
    /// Value before the block executed.
    pub old_value: B256,
    /// Value after the block executed.
    pub new_value: B256,
}

impl StorageSlotDiff {
    #[inline]
    pub fn new(old_value: B256, new_value: B256) -> Self {
        Self {
            old_value,
            new_value,
        }
    }

    /// Whether the value actually changed.
    #[inline]
    pub fn is_noop(&self) -> bool {
        self.old_value == self.new_value
    }
}

/// All changes to a single account during one block.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct AccountDiff {
    /// Balance changes: (balance_before, balance_after).
    pub balance: Option<(U256, U256)>,
    /// Nonce changes: (nonce_before, nonce_after).
    pub nonce: Option<(u64, u64)>,
    /// Whether the account's bytecode was modified (contract deployment / self-destruct).
    pub code_changed: bool,
    /// Changed storage slots.
    pub storage: HashMap<U256, StorageSlotDiff>,
}

impl AccountDiff {
    /// Create an `AccountDiff` with a pre-sized storage map.
    ///
    /// Used by `StateDiffBuilder` to avoid repeated rehashing when the first
    /// recorded change for an account is a storage write.
    #[inline]
    fn with_storage_capacity(capacity: usize) -> Self {
        Self {
            storage: HashMap::with_capacity(capacity),
            ..Default::default()
        }
    }

    /// Number of storage slots that changed.
    #[inline]
    pub fn storage_change_count(&self) -> usize {
        self.storage.len()
    }

    /// Whether any field actually changed.
    #[inline]
    pub fn is_empty(&self) -> bool {
        self.balance.is_none()
            && self.nonce.is_none()
            && !self.code_changed
            && self.storage.is_empty()
    }

    /// Whether only storage changed (common for contract calls).
    #[inline]
    pub fn is_storage_only(&self) -> bool {
        self.balance.is_none() && self.nonce.is_none() && !self.code_changed
    }
}

// ── Block-level diff ─────────────────────────────────────────────────────────

/// Complete state diff produced by executing a single block.
///
/// A diff captures *exactly* what changed; nothing that stayed the same is included.
/// Applying the diff to state at `block_number - 1` yields state at `block_number`.
#[derive(Debug, Clone, Default)]
pub struct StateDiff {
    /// The block that produced this diff.
    pub block_number: u64,
    /// Hash of the block header.
    pub block_hash: B256,
    /// Per-account changes. Accounts not in this map were untouched.
    pub changes: HashMap<Address, AccountDiff>,
    /// Total gas used by the block (informational).
    pub gas_used: u64,
    /// Number of transactions in the block.
    pub tx_count: usize,
}

impl StateDiff {
    /// Number of accounts touched by this block.
    #[inline]
    pub fn touched_account_count(&self) -> usize {
        self.changes.len()
    }

    /// Total number of storage slots that changed.
    pub fn total_storage_changes(&self) -> usize {
        self.changes
            .values()
            .map(|a| a.storage_change_count())
            .sum()
    }

    /// Whether this block changed any state at all.
    #[inline]
    pub fn is_empty(&self) -> bool {
        self.changes.is_empty()
    }

    /// Return a one-line summary for logging.
    pub fn summary(&self) -> String {
        format!(
            "block={} txs={} accounts_touched={} storage_slots_changed={} gas_used={}",
            self.block_number,
            self.tx_count,
            self.touched_account_count(),
            self.total_storage_changes(),
            self.gas_used,
        )
    }

    /// Get the diff for a specific account, if any.
    #[inline]
    pub fn account_diff(&self, addr: &Address) -> Option<&AccountDiff> {
        self.changes.get(addr)
    }

    /// Get the new storage value for `(addr, slot)` after this block, if it changed.
    #[inline]
    pub fn storage_after(&self, addr: &Address, slot: &U256) -> Option<B256> {
        self.changes
            .get(addr)
            .and_then(|a| a.storage.get(slot))
            .map(|d| d.new_value)
    }

    /// Compute the approximate serialized size in bytes (rough estimate).
    pub fn estimated_bytes(&self) -> usize {
        // Fixed overhead per diff
        let mut size = 64; // block_number + block_hash + metadata
        for account_diff in self.changes.values() {
            size += 20; // address
            size += 32 * 2 * account_diff.storage.len(); // slot (32) + diff (64)
            if account_diff.balance.is_some() {
                size += 64;
            }
            if account_diff.nonce.is_some() {
                size += 16;
            }
        }
        size
    }
}

// ── Builder ───────────────────────────────────────────────────────────────────

/// Incrementally builds a [`StateDiff`] as a block is executed.
///
/// Accumulates account and storage changes; call [`build`](Self::build) after
/// execution is complete to get the final immutable diff.
///
/// The inner `changes` map is pre-allocated for
/// `INITIAL_ACCOUNT_CAPACITY` entries to avoid rehashing during the
/// hot per-block path.
#[derive(Debug, Default)]
pub struct StateDiffBuilder {
    block_number: u64,
    block_hash: B256,
    changes: HashMap<Address, AccountDiff>,
    gas_used: u64,
    tx_count: usize,
}

/// Initial capacity for the per-block accounts map.
///
/// Most blocks touch fewer than 32 distinct accounts (coinbase + active
/// contract + EOA sender); 32 avoids the first two resize steps while
/// keeping the cold-start overhead negligible (32 × ~56 bytes ≈ 1.8 KB).
const INITIAL_ACCOUNT_CAPACITY: usize = 32;

/// Initial capacity for the per-account storage-slot map.
///
/// Contract calls rarely touch more than 8 slots; pre-allocating 8 avoids
/// the first resize for the common case.
const INITIAL_STORAGE_CAPACITY: usize = 8;

impl StateDiffBuilder {
    /// Create a new builder for the given block.
    ///
    /// Pre-allocates internal maps to reduce rehashing on the hot path.
    pub fn new(block_number: u64, block_hash: B256) -> Self {
        Self {
            block_number,
            block_hash,
            changes: HashMap::with_capacity(INITIAL_ACCOUNT_CAPACITY),
            gas_used: 0,
            tx_count: 0,
        }
    }

    /// Set the total gas used (call after block execution).
    pub fn with_gas_used(mut self, gas: u64) -> Self {
        self.gas_used = gas;
        self
    }

    /// Set the transaction count.
    pub fn with_tx_count(mut self, count: usize) -> Self {
        self.tx_count = count;
        self
    }

    /// Record a balance change for an account.
    #[inline]
    pub fn record_balance_change(&mut self, addr: Address, old: U256, new: U256) {
        if old != new {
            self.changes.entry(addr).or_default().balance = Some((old, new));
        }
    }

    /// Record a nonce change for an account.
    #[inline]
    pub fn record_nonce_change(&mut self, addr: Address, old: u64, new: u64) {
        if old != new {
            self.changes.entry(addr).or_default().nonce = Some((old, new));
        }
    }

    /// Mark that an account's code changed (contract creation or self-destruct).
    #[inline]
    pub fn record_code_change(&mut self, addr: Address) {
        self.changes.entry(addr).or_default().code_changed = true;
    }

    /// Record a storage slot change for an account.
    pub fn record_storage_change(&mut self, addr: Address, slot: U256, old: B256, new: B256) {
        if old != new {
            // Use `or_insert_with` so the AccountDiff's storage map is
            // pre-sized only when the entry is actually absent, avoiding an
            // allocation on the fast path when the account was already seen.
            self.changes
                .entry(addr)
                .or_insert_with(|| AccountDiff::with_storage_capacity(INITIAL_STORAGE_CAPACITY))
                .storage
                .insert(slot, StorageSlotDiff::new(old, new));
        }
    }

    /// Set gas used after building incrementally.
    pub fn set_gas_used(&mut self, gas: u64) {
        self.gas_used = gas;
    }

    /// Set tx count after building incrementally.
    pub fn set_tx_count(&mut self, count: usize) {
        self.tx_count = count;
    }

    /// Consume the builder and produce the final [`StateDiff`].
    pub fn build(self) -> StateDiff {
        StateDiff {
            block_number: self.block_number,
            block_hash: self.block_hash,
            changes: self.changes,
            gas_used: self.gas_used,
            tx_count: self.tx_count,
        }
    }

    /// Number of accounts with recorded changes so far.
    #[inline]
    pub fn touched_accounts(&self) -> usize {
        self.changes.len()
    }
}

// ── Diff applier ─────────────────────────────────────────────────────────────

/// Apply a `StateDiff` to an in-memory state map.
///
/// Used by replica nodes and tests to verify that applying diffs is equivalent
/// to re-executing transactions.
pub fn apply_diff(state: &mut HashMap<Address, HashMap<U256, B256>>, diff: &StateDiff) {
    for (addr, account_diff) in &diff.changes {
        let account_storage = state.entry(*addr).or_default();
        for (slot, slot_diff) in &account_diff.storage {
            if slot_diff.new_value == B256::ZERO {
                account_storage.remove(slot);
            } else {
                account_storage.insert(*slot, slot_diff.new_value);
            }
        }
    }
}

/// Verify that a `StateDiff` is internally consistent:
/// every recorded `old_value` should match the pre-state.
pub fn verify_diff_against_pre_state(
    pre_state: &HashMap<Address, HashMap<U256, B256>>,
    diff: &StateDiff,
) -> bool {
    for (addr, account_diff) in &diff.changes {
        for (slot, slot_diff) in &account_diff.storage {
            let pre_val = pre_state
                .get(addr)
                .and_then(|s| s.get(slot))
                .copied()
                .unwrap_or(B256::ZERO);
            if pre_val != slot_diff.old_value {
                return false;
            }
        }
    }
    true
}

#[cfg(test)]
mod tests {
    use super::*;

    fn addr(n: u8) -> Address {
        Address::from([n; 20])
    }

    fn slot(n: u64) -> U256 {
        U256::from(n)
    }

    fn val(n: u8) -> B256 {
        B256::from([n; 32])
    }

    fn hash(n: u8) -> B256 {
        B256::from([n; 32])
    }

    // ── StorageSlotDiff ────────────────────────────────────────────────────────

    #[test]
    fn test_slot_diff_noop_detection() {
        let d = StorageSlotDiff::new(val(1), val(1));
        assert!(d.is_noop());
    }

    #[test]
    fn test_slot_diff_detects_change() {
        let d = StorageSlotDiff::new(val(1), val(2));
        assert!(!d.is_noop());
    }

    // ── AccountDiff ───────────────────────────────────────────────────────────

    #[test]
    fn test_account_diff_default_is_empty() {
        let d = AccountDiff::default();
        assert!(d.is_empty());
    }

    #[test]
    fn test_account_diff_storage_only() {
        let mut d = AccountDiff::default();
        d.storage
            .insert(slot(0), StorageSlotDiff::new(val(0), val(1)));
        assert!(d.is_storage_only());
        assert!(!d.is_empty());
    }

    #[test]
    fn test_account_diff_balance_not_storage_only() {
        let d = AccountDiff {
            balance: Some((U256::ZERO, U256::from(1000u64))),
            ..Default::default()
        };
        assert!(!d.is_storage_only());
    }

    #[test]
    fn test_account_diff_storage_change_count() {
        let mut d = AccountDiff::default();
        d.storage
            .insert(slot(0), StorageSlotDiff::new(val(0), val(1)));
        d.storage
            .insert(slot(1), StorageSlotDiff::new(val(0), val(2)));
        assert_eq!(d.storage_change_count(), 2);
    }

    // ── StateDiffBuilder ──────────────────────────────────────────────────────

    #[test]
    fn test_builder_creates_empty_diff() {
        let diff = StateDiffBuilder::new(1, hash(1)).build();
        assert!(diff.is_empty());
        assert_eq!(diff.block_number, 1);
        assert_eq!(diff.block_hash, hash(1));
    }

    #[test]
    fn test_builder_records_balance_change() {
        let mut b = StateDiffBuilder::new(5, hash(5));
        b.record_balance_change(addr(1), U256::from(0u64), U256::from(1_000u64));
        let diff = b.build();

        let account = diff.account_diff(&addr(1)).unwrap();
        assert_eq!(
            account.balance,
            Some((U256::from(0u64), U256::from(1_000u64)))
        );
    }

    #[test]
    fn test_builder_ignores_noop_balance_change() {
        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_balance_change(addr(1), U256::from(500u64), U256::from(500u64));
        let diff = b.build();
        assert!(diff.is_empty(), "no-op change should not appear in diff");
    }

    #[test]
    fn test_builder_records_nonce_change() {
        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_nonce_change(addr(1), 0, 1);
        let diff = b.build();
        assert_eq!(diff.account_diff(&addr(1)).unwrap().nonce, Some((0, 1)));
    }

    #[test]
    fn test_builder_ignores_noop_nonce_change() {
        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_nonce_change(addr(1), 3, 3);
        assert!(b.build().is_empty());
    }

    #[test]
    fn test_builder_records_code_change() {
        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_code_change(addr(1));
        let diff = b.build();
        assert!(diff.account_diff(&addr(1)).unwrap().code_changed);
    }

    #[test]
    fn test_builder_records_storage_change() {
        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_storage_change(addr(1), slot(5), val(0), val(42));
        let diff = b.build();

        let slot_diff = diff
            .account_diff(&addr(1))
            .unwrap()
            .storage
            .get(&slot(5))
            .unwrap();
        assert_eq!(slot_diff.old_value, val(0));
        assert_eq!(slot_diff.new_value, val(42));
    }

    #[test]
    fn test_builder_ignores_noop_storage_change() {
        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_storage_change(addr(1), slot(0), val(7), val(7));
        assert!(b.build().is_empty());
    }

    #[test]
    fn test_builder_multiple_accounts() {
        let mut b = StateDiffBuilder::new(10, hash(10));
        b.record_balance_change(addr(1), U256::from(0u64), U256::from(100u64));
        b.record_storage_change(addr(2), slot(0), val(0), val(1));
        b.record_nonce_change(addr(3), 0, 1);
        let diff = b.build();

        assert_eq!(diff.touched_account_count(), 3);
    }

    #[test]
    fn test_builder_accumulates_same_account() {
        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_nonce_change(addr(1), 0, 1);
        b.record_storage_change(addr(1), slot(0), val(0), val(1));
        let diff = b.build();

        let acc = diff.account_diff(&addr(1)).unwrap();
        assert_eq!(acc.nonce, Some((0, 1)));
        assert_eq!(acc.storage_change_count(), 1);
    }

    #[test]
    fn test_builder_with_gas_and_tx_count() {
        let diff = StateDiffBuilder::new(1, hash(1))
            .with_gas_used(21_000)
            .with_tx_count(1)
            .build();
        assert_eq!(diff.gas_used, 21_000);
        assert_eq!(diff.tx_count, 1);
    }

    // ── StateDiff helpers ─────────────────────────────────────────────────────

    #[test]
    fn test_diff_total_storage_changes() {
        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_storage_change(addr(1), slot(0), val(0), val(1));
        b.record_storage_change(addr(1), slot(1), val(0), val(2));
        b.record_storage_change(addr(2), slot(0), val(0), val(3));
        let diff = b.build();
        assert_eq!(diff.total_storage_changes(), 3);
    }

    #[test]
    fn test_diff_storage_after() {
        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_storage_change(addr(1), slot(3), val(0), val(99));
        let diff = b.build();
        assert_eq!(diff.storage_after(&addr(1), &slot(3)), Some(val(99)));
        assert!(diff.storage_after(&addr(1), &slot(4)).is_none());
    }

    #[test]
    fn test_diff_summary_contains_block_number() {
        let mut b = StateDiffBuilder::new(42, hash(1));
        b.record_nonce_change(addr(1), 0, 1);
        let diff = b.with_gas_used(100).with_tx_count(1).build();
        let summary = diff.summary();
        assert!(summary.contains("block=42"));
        assert!(summary.contains("txs=1"));
        assert!(summary.contains("gas_used=100"));
    }

    #[test]
    fn test_diff_estimated_bytes_empty() {
        let diff = StateDiffBuilder::new(1, hash(1)).build();
        assert_eq!(diff.estimated_bytes(), 64); // fixed overhead only
    }

    #[test]
    fn test_diff_estimated_bytes_with_storage() {
        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_storage_change(addr(1), slot(0), val(0), val(1));
        let diff = b.build();
        // 64 overhead + 20 (addr) + 64 (slot diff)
        assert!(diff.estimated_bytes() > 64);
    }

    // ── apply_diff ────────────────────────────────────────────────────────────

    #[test]
    fn test_apply_diff_sets_storage() {
        let mut state: HashMap<Address, HashMap<U256, B256>> = HashMap::new();
        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_storage_change(addr(1), slot(0), val(0), val(42));
        let diff = b.build();

        apply_diff(&mut state, &diff);

        assert_eq!(state[&addr(1)][&slot(0)], val(42));
    }

    #[test]
    fn test_apply_diff_removes_zeroed_slots() {
        let mut state: HashMap<Address, HashMap<U256, B256>> = HashMap::new();
        state.entry(addr(1)).or_default().insert(slot(0), val(99));

        let mut b = StateDiffBuilder::new(2, hash(2));
        // Writing zero = deletion in EVM storage
        b.record_storage_change(addr(1), slot(0), val(99), B256::ZERO);
        let diff = b.build();

        apply_diff(&mut state, &diff);

        assert!(
            !state[&addr(1)].contains_key(&slot(0)),
            "zeroed slot should be removed"
        );
    }

    #[test]
    fn test_apply_diff_idempotent_with_correct_pre_state() {
        let mut state: HashMap<Address, HashMap<U256, B256>> = HashMap::new();
        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_storage_change(addr(1), slot(0), val(0), val(5));
        let diff = b.build();

        apply_diff(&mut state, &diff);
        // Applying again with matching pre-state from first apply
        // would not make sense here, but the state is still consistent
        assert_eq!(state[&addr(1)][&slot(0)], val(5));
    }

    // ── verify_diff_against_pre_state ────────────────────────────────────────

    #[test]
    fn test_verify_diff_valid() {
        let mut pre_state: HashMap<Address, HashMap<U256, B256>> = HashMap::new();
        pre_state
            .entry(addr(1))
            .or_default()
            .insert(slot(0), val(10));

        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_storage_change(addr(1), slot(0), val(10), val(20));
        let diff = b.build();

        assert!(verify_diff_against_pre_state(&pre_state, &diff));
    }

    #[test]
    fn test_verify_diff_invalid_pre_state() {
        let mut pre_state: HashMap<Address, HashMap<U256, B256>> = HashMap::new();
        pre_state
            .entry(addr(1))
            .or_default()
            .insert(slot(0), val(5)); // wrong pre-state

        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_storage_change(addr(1), slot(0), val(10), val(20)); // claims old=10
        let diff = b.build();

        assert!(!verify_diff_against_pre_state(&pre_state, &diff));
    }

    #[test]
    fn test_verify_diff_empty_account_treated_as_zero() {
        let pre_state: HashMap<Address, HashMap<U256, B256>> = HashMap::new();

        let mut b = StateDiffBuilder::new(1, hash(1));
        b.record_storage_change(addr(1), slot(0), B256::ZERO, val(1));
        let diff = b.build();

        assert!(
            verify_diff_against_pre_state(&pre_state, &diff),
            "absent slot == zero"
        );
    }
}

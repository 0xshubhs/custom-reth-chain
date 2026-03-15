//! Hot state cache for high-performance on-chain storage reads.
//!
//! Phase 5.31: In-memory LRU cache that sits in front of the on-chain `StorageReader`.
//! Reduces MDBX I/O for frequently-read governance contract storage slots
//! (ChainConfig gas limit, SignerRegistry signer list, Timelock delay, etc.).
//!
//! Architecture:
//! ```text
//!   PoaPayloadBuilder / PoaConsensus
//!     → CachedStorageReader   (this module, in-memory LRU)
//!       → StateProviderStorageReader / GenesisStorageReader  (MDBX / genesis alloc)
//! ```
//!
//! The cache is safe to share across threads via `Arc<Mutex<HotStateCache>>`.

use alloy_primitives::{Address, B256, U256};
use std::collections::{BTreeMap, HashMap};
use std::sync::{Arc, Mutex};

use crate::onchain::StorageReader;

/// A reference-counted, thread-safe handle to a [`HotStateCache`].
///
/// Store one of these in long-lived components (e.g. `PoaPayloadBuilder`) and
/// pass `Arc::clone` to short-lived [`CachedStorageReader`] instances created
/// per block build.
pub type SharedCache = Arc<Mutex<HotStateCache>>;

/// Configuration for the hot state cache.
#[derive(Debug, Clone, Copy)]
pub struct CacheConfig {
    /// Maximum number of `(address, slot) → value` entries held in RAM.
    pub max_entries: usize,
    /// Automatically invalidate the cache every N block-builds (0 = never auto-invalidate).
    pub invalidate_every_n_blocks: u64,
}

impl Default for CacheConfig {
    fn default() -> Self {
        Self {
            max_entries: 1_024,
            invalidate_every_n_blocks: 0,
        }
    }
}

impl CacheConfig {
    /// Create a cache config optimised for governance contract reads.
    pub fn for_governance() -> Self {
        Self {
            max_entries: 256,
            invalidate_every_n_blocks: 30_000, // re-seed at every epoch
        }
    }

    /// Create a large cache for heavy state workloads.
    pub fn large(max_entries: usize) -> Self {
        Self {
            max_entries,
            invalidate_every_n_blocks: 0,
        }
    }
}

/// Snapshot of cache performance counters.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct CacheStats {
    /// Number of reads that were satisfied from the cache.
    pub hits: u64,
    /// Number of reads that required a downstream lookup.
    pub misses: u64,
    /// Number of entries evicted to make room for new ones.
    pub evictions: u64,
    /// Current number of entries in the cache.
    pub current_entries: usize,
    /// Maximum configured capacity.
    pub max_entries: usize,
}

impl CacheStats {
    /// Cache hit rate in the range `[0.0, 1.0]`.
    #[inline]
    pub fn hit_rate(&self) -> f64 {
        let total = self.hits + self.misses;
        if total == 0 {
            0.0
        } else {
            self.hits as f64 / total as f64
        }
    }

    /// Whether any lookups have been performed yet.
    #[inline]
    pub fn is_cold(&self) -> bool {
        self.hits == 0 && self.misses == 0
    }
}

/// LRU hot state cache mapping `(Address, slot) → B256`.
///
/// Uses a `HashMap` for O(1) value lookup and a `BTreeMap<clock, key>` as the
/// LRU eviction index.  Every access stamps the entry with a monotonically
/// increasing clock value so the minimum clock in the `BTreeMap` is always the
/// least-recently-used entry.
///
/// Complexity:
///   - get   → O(1) map lookup + O(log n) clock update
///   - insert → O(1) map insert + O(log n) clock insert/eviction
///   - invalidate_address → O(k log n) where k = matched entries
///     (all previously O(n) with the VecDeque approach)
#[derive(Debug)]
pub struct HotStateCache {
    /// Primary value store: key → (value, current_clock).
    map: HashMap<(Address, U256), (B256, u64)>,
    /// Eviction index: clock → key.  The minimum clock is the LRU entry.
    lru: BTreeMap<u64, (Address, U256)>,
    max_entries: usize,
    /// Monotonic counter incremented on every access/insert.
    clock: u64,
    stats: CacheStats,
}

impl HotStateCache {
    /// Create a new cache with the given maximum capacity.
    pub fn new(max_entries: usize) -> Self {
        assert!(max_entries > 0, "cache capacity must be > 0");
        Self {
            map: HashMap::with_capacity(max_entries),
            lru: BTreeMap::new(),
            max_entries,
            clock: 0,
            stats: CacheStats {
                max_entries,
                ..Default::default()
            },
        }
    }

    /// Advance the clock and return the new tick.
    #[inline]
    fn tick(&mut self) -> u64 {
        self.clock += 1;
        self.clock
    }

    /// Look up a slot value. Updates LRU order on hit (O(1) + O(log n)).
    pub fn get(&mut self, addr: Address, slot: U256) -> Option<B256> {
        let key = (addr, slot);
        if let Some(&(value, prev_clock)) = self.map.get(&key) {
            // Remove old position from the eviction index
            self.lru.remove(&prev_clock);
            // Stamp with fresh clock so this entry sorts to the MRU end
            self.clock += 1;
            let new_clock = self.clock;
            self.map.insert(key, (value, new_clock));
            self.lru.insert(new_clock, key);
            self.stats.hits += 1;
            Some(value)
        } else {
            self.stats.misses += 1;
            None
        }
    }

    /// Insert or update a slot value. Evicts LRU entry if at capacity
    /// (O(log n)).
    pub fn insert(&mut self, addr: Address, slot: U256, value: B256) {
        let key = (addr, slot);
        if let Some(&(_, prev_clock)) = self.map.get(&key) {
            // Update value and refresh MRU position
            self.lru.remove(&prev_clock);
            self.clock += 1;
            let new_clock = self.clock;
            self.map.insert(key, (value, new_clock));
            self.lru.insert(new_clock, key);
        } else {
            // Evict LRU entry when at capacity
            if self.map.len() >= self.max_entries {
                // pop_first() removes the minimum clock entry (= LRU) in one operation.
                if let Some((_, lru_key)) = self.lru.pop_first() {
                    self.map.remove(&lru_key);
                    self.stats.evictions += 1;
                }
            }
            let new_clock = self.tick();
            self.map.insert(key, (value, new_clock));
            self.lru.insert(new_clock, key);
        }
        self.stats.current_entries = self.map.len();
    }

    /// Invalidate all slots cached for a specific contract address.
    ///
    /// Uses `HashMap::retain` to drain matching entries in a single pass,
    /// collecting only the cheap `u64` clock values for a second-pass LRU
    /// removal.  Avoids allocating the full `Vec<(key, clock)>` of the previous
    /// implementation — only `u64`s are collected.
    /// Call this after an on-chain governance transaction modifies the contract.
    pub fn invalidate_address(&mut self, addr: Address) {
        // Governance contracts typically have ≤ ~10 slots cached; pre-allocate a
        // small Vec to avoid re-growing during the collect pass.
        let mut clocks_to_remove: Vec<u64> = Vec::with_capacity(16);
        self.map.retain(|(a, _), (_, clock)| {
            if *a == addr {
                clocks_to_remove.push(*clock);
                false // remove from map
            } else {
                true // keep
            }
        });
        for clock in clocks_to_remove {
            self.lru.remove(&clock);
        }
        self.stats.current_entries = self.map.len();
    }

    /// Evict all entries.
    pub fn clear(&mut self) {
        self.map.clear();
        self.lru.clear();
        self.stats.current_entries = 0;
    }

    /// Current number of entries.
    #[inline]
    pub fn len(&self) -> usize {
        self.map.len()
    }

    /// Whether the cache contains no entries.
    #[inline]
    pub fn is_empty(&self) -> bool {
        self.map.is_empty()
    }

    /// Snapshot of performance counters.
    #[inline]
    pub fn stats(&self) -> CacheStats {
        self.stats
    }
}

/// A [`StorageReader`] wrapper that adds a thread-safe LRU cache in front of
/// any inner `StorageReader` (e.g. `StateProviderStorageReader`).
///
/// The cache is stored as a [`SharedCache`] (`Arc<Mutex<HotStateCache>>`), so
/// the same cache can be reused across multiple per-block reader instances.
///
/// # Example
/// ```ignore
/// // Create a long-lived cache (store in PoaPayloadBuilder):
/// let shared: SharedCache = Arc::new(Mutex::new(HotStateCache::new(1024)));
///
/// // Per block: wrap the short-lived state provider with the shared cache:
/// let reader = CachedStorageReader::new_shared(state_reader, Arc::clone(&shared));
/// let gas_limit = read_gas_limit(&reader);  // first call hits MDBX
/// let gas_limit2 = read_gas_limit(&reader); // subsequent calls hit cache
/// ```
pub struct CachedStorageReader<R> {
    inner: R,
    cache: SharedCache,
}

impl<R: StorageReader> CachedStorageReader<R> {
    /// Wrap an existing reader with a **new** LRU cache (owned, not shared).
    pub fn new(inner: R, config: CacheConfig) -> Self {
        Self {
            inner,
            cache: Arc::new(Mutex::new(HotStateCache::new(config.max_entries))),
        }
    }

    /// Wrap with default cache configuration (new, unshared cache).
    pub fn with_defaults(inner: R) -> Self {
        Self::new(inner, CacheConfig::default())
    }

    /// Wrap an existing reader with a **shared** cache.
    ///
    /// Use this when the same cache must persist across multiple reader instances
    /// (e.g. across block builds in `PoaPayloadBuilder`).
    pub fn new_shared(inner: R, cache: SharedCache) -> Self {
        Self { inner, cache }
    }

    /// Return a snapshot of cache performance counters.
    pub fn stats(&self) -> CacheStats {
        self.cache.lock().expect("cache lock poisoned").stats()
    }

    /// Invalidate all cached slots for the given contract address.
    pub fn invalidate_address(&self, addr: Address) {
        self.cache
            .lock()
            .expect("cache lock poisoned")
            .invalidate_address(addr);
    }

    /// Clear the entire cache.
    pub fn clear(&self) {
        self.cache.lock().expect("cache lock poisoned").clear();
    }

    /// Borrow the inner reader.
    #[inline]
    pub fn inner(&self) -> &R {
        &self.inner
    }

    /// Clone the underlying [`SharedCache`] handle.
    #[inline]
    pub fn shared_cache(&self) -> SharedCache {
        Arc::clone(&self.cache)
    }
}

impl<R: StorageReader> StorageReader for CachedStorageReader<R> {
    fn read_storage(&self, address: Address, slot: U256) -> Option<B256> {
        // Fast path: single lock acquisition — return immediately on hit.
        {
            let mut cache = self.cache.lock().expect("cache lock poisoned");
            if let Some(v) = cache.get(address, slot) {
                return Some(v);
            }
            // Lock is dropped here before the potentially-blocking IO below.
        }

        // Slow path: IO outside the lock so we never hold the Mutex during a
        // potentially-blocking MDBX read.
        let value = self.inner.read_storage(address, slot)?;

        // Re-acquire the lock only to insert the freshly-fetched value.
        // This is a single acquisition (not two), and no IO is done while
        // holding it.
        self.cache
            .lock()
            .expect("cache lock poisoned")
            .insert(address, slot, value);

        Some(value)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::onchain::StorageReader;
    use alloy_primitives::B256;
    use std::collections::HashMap;

    // ── Test helpers ──────────────────────────────────────────────────────────

    fn addr(n: u8) -> Address {
        Address::from([n; 20])
    }

    fn slot(n: u64) -> U256 {
        U256::from(n)
    }

    fn val(n: u8) -> B256 {
        B256::from([n; 32])
    }

    /// Minimal in-memory StorageReader for testing.
    struct MockStorage(HashMap<(Address, U256), B256>);

    impl MockStorage {
        fn new() -> Self {
            Self(HashMap::new())
        }

        fn with_entry(mut self, a: Address, s: U256, v: B256) -> Self {
            self.0.insert((a, s), v);
            self
        }
    }

    impl StorageReader for MockStorage {
        fn read_storage(&self, address: Address, slot: U256) -> Option<B256> {
            self.0.get(&(address, slot)).copied()
        }
    }

    // ── HotStateCache unit tests ──────────────────────────────────────────────

    #[test]
    fn test_cache_empty_on_creation() {
        let cache = HotStateCache::new(10);
        assert_eq!(cache.len(), 0);
        assert!(cache.is_empty());
    }

    #[test]
    fn test_cache_miss_on_empty() {
        let mut cache = HotStateCache::new(10);
        assert!(cache.get(addr(1), slot(0)).is_none());
        assert_eq!(cache.stats().misses, 1);
        assert_eq!(cache.stats().hits, 0);
    }

    #[test]
    fn test_cache_insert_and_hit() {
        let mut cache = HotStateCache::new(10);
        cache.insert(addr(1), slot(0), val(42));
        let result = cache.get(addr(1), slot(0));
        assert_eq!(result, Some(val(42)));
        assert_eq!(cache.stats().hits, 1);
        assert_eq!(cache.stats().misses, 0);
    }

    #[test]
    fn test_cache_multiple_entries() {
        let mut cache = HotStateCache::new(10);
        for i in 0..5u8 {
            cache.insert(addr(i), slot(i as u64), val(i));
        }
        assert_eq!(cache.len(), 5);
        for i in 0..5u8 {
            assert_eq!(cache.get(addr(i), slot(i as u64)), Some(val(i)));
        }
    }

    #[test]
    fn test_cache_lru_eviction() {
        let mut cache = HotStateCache::new(3);
        cache.insert(addr(1), slot(0), val(1)); // LRU: [1]
        cache.insert(addr(2), slot(0), val(2)); // LRU: [1, 2]
        cache.insert(addr(3), slot(0), val(3)); // LRU: [1, 2, 3] — full

        // Insert a 4th entry: addr(1) should be evicted
        cache.insert(addr(4), slot(0), val(4));

        assert!(
            cache.get(addr(1), slot(0)).is_none(),
            "addr(1) should be evicted"
        );
        assert_eq!(cache.get(addr(2), slot(0)), Some(val(2)));
        assert_eq!(cache.get(addr(3), slot(0)), Some(val(3)));
        assert_eq!(cache.get(addr(4), slot(0)), Some(val(4)));
        assert_eq!(cache.stats().evictions, 1);
    }

    #[test]
    fn test_cache_access_promotes_lru() {
        let mut cache = HotStateCache::new(3);
        cache.insert(addr(1), slot(0), val(1));
        cache.insert(addr(2), slot(0), val(2));
        cache.insert(addr(3), slot(0), val(3));

        // Access addr(1) to make it MRU
        cache.get(addr(1), slot(0));

        // Now inserting addr(4) should evict addr(2) (new LRU)
        cache.insert(addr(4), slot(0), val(4));

        assert_eq!(
            cache.get(addr(1), slot(0)),
            Some(val(1)),
            "addr(1) should survive"
        );
        assert!(
            cache.get(addr(2), slot(0)).is_none(),
            "addr(2) should be evicted"
        );
    }

    #[test]
    fn test_cache_update_existing_entry() {
        let mut cache = HotStateCache::new(10);
        cache.insert(addr(1), slot(0), val(10));
        cache.insert(addr(1), slot(0), val(20)); // update
        assert_eq!(cache.get(addr(1), slot(0)), Some(val(20)));
        assert_eq!(cache.len(), 1, "update should not create duplicate");
    }

    #[test]
    fn test_cache_update_existing_entry_promotes_to_mru() {
        // Re-inserting an existing entry (value update) must move it to MRU
        // so the next eviction picks a *different* entry.
        let mut cache = HotStateCache::new(3);
        cache.insert(addr(1), slot(0), val(1)); // LRU: [1]
        cache.insert(addr(2), slot(0), val(2)); // LRU: [1, 2]
        cache.insert(addr(3), slot(0), val(3)); // LRU: [1, 2, 3] — full

        // Re-insert addr(1) with a new value — should promote it to MRU.
        cache.insert(addr(1), slot(0), val(99));
        assert_eq!(cache.len(), 3, "re-insert should not grow the cache");

        // Now insert addr(4) — should evict addr(2) (new LRU), NOT addr(1).
        cache.insert(addr(4), slot(0), val(4));

        assert_eq!(
            cache.get(addr(1), slot(0)),
            Some(val(99)),
            "addr(1) was promoted to MRU, should survive"
        );
        assert!(
            cache.get(addr(2), slot(0)).is_none(),
            "addr(2) should be evicted (new LRU after addr(1) was promoted)"
        );
        assert_eq!(cache.get(addr(3), slot(0)), Some(val(3)));
        assert_eq!(cache.get(addr(4), slot(0)), Some(val(4)));
    }


    #[test]
    fn test_cache_invalidate_address() {
        let mut cache = HotStateCache::new(10);
        cache.insert(addr(1), slot(0), val(1));
        cache.insert(addr(1), slot(1), val(2));
        cache.insert(addr(2), slot(0), val(3));

        cache.invalidate_address(addr(1));

        assert!(cache.get(addr(1), slot(0)).is_none());
        assert!(cache.get(addr(1), slot(1)).is_none());
        assert_eq!(
            cache.get(addr(2), slot(0)),
            Some(val(3)),
            "addr(2) should survive"
        );
    }

    #[test]
    fn test_cache_clear() {
        let mut cache = HotStateCache::new(10);
        for i in 0..5u8 {
            cache.insert(addr(i), slot(0), val(i));
        }
        cache.clear();
        assert_eq!(cache.len(), 0);
        assert!(cache.is_empty());
    }

    #[test]
    fn test_cache_stats_hit_rate_zero_when_cold() {
        let cache = HotStateCache::new(10);
        assert_eq!(cache.stats().hit_rate(), 0.0);
        assert!(cache.stats().is_cold());
    }

    #[test]
    fn test_cache_stats_hit_rate_calculation() {
        let mut cache = HotStateCache::new(10);
        cache.insert(addr(1), slot(0), val(1));
        cache.get(addr(1), slot(0)); // hit
        cache.get(addr(2), slot(0)); // miss
        cache.get(addr(1), slot(0)); // hit

        let stats = cache.stats();
        assert_eq!(stats.hits, 2);
        assert_eq!(stats.misses, 1);
        assert!((stats.hit_rate() - 2.0 / 3.0).abs() < 1e-9);
    }

    #[test]
    fn test_cache_capacity_single_entry() {
        let mut cache = HotStateCache::new(1);
        cache.insert(addr(1), slot(0), val(1));
        cache.insert(addr(2), slot(0), val(2)); // evicts addr(1)
        assert!(cache.get(addr(1), slot(0)).is_none());
        assert_eq!(cache.get(addr(2), slot(0)), Some(val(2)));
    }

    // ── CachedStorageReader tests ─────────────────────────────────────────────

    #[test]
    fn test_cached_reader_miss_then_hit() {
        let storage = MockStorage::new().with_entry(addr(1), slot(5), val(99));
        let reader = CachedStorageReader::new(storage, CacheConfig::default());

        // First read: cache miss → fetches from inner
        let v1 = reader.read_storage(addr(1), slot(5));
        assert_eq!(v1, Some(val(99)));
        assert_eq!(reader.stats().misses, 1);
        assert_eq!(reader.stats().hits, 0);

        // Second read: cache hit
        let v2 = reader.read_storage(addr(1), slot(5));
        assert_eq!(v2, Some(val(99)));
        assert_eq!(reader.stats().hits, 1);
        assert_eq!(reader.stats().misses, 1);
    }

    #[test]
    fn test_cached_reader_miss_for_absent_slot() {
        let storage = MockStorage::new();
        let reader = CachedStorageReader::new(storage, CacheConfig::default());

        let result = reader.read_storage(addr(1), slot(0));
        assert!(result.is_none());
        // A None result is NOT stored in the cache (absent slots stay absent)
        assert_eq!(reader.stats().misses, 1);
    }

    #[test]
    fn test_cached_reader_multiple_addresses() {
        let storage = MockStorage::new()
            .with_entry(addr(1), slot(0), val(10))
            .with_entry(addr(2), slot(0), val(20))
            .with_entry(addr(1), slot(1), val(11));
        let reader = CachedStorageReader::new(storage, CacheConfig::default());

        assert_eq!(reader.read_storage(addr(1), slot(0)), Some(val(10)));
        assert_eq!(reader.read_storage(addr(2), slot(0)), Some(val(20)));
        assert_eq!(reader.read_storage(addr(1), slot(1)), Some(val(11)));
        assert_eq!(reader.stats().misses, 3);

        // Second round: all from cache
        assert_eq!(reader.read_storage(addr(1), slot(0)), Some(val(10)));
        assert_eq!(reader.read_storage(addr(2), slot(0)), Some(val(20)));
        assert_eq!(reader.stats().hits, 2);
    }

    #[test]
    fn test_cached_reader_invalidate() {
        let storage = MockStorage::new().with_entry(addr(1), slot(0), val(50));
        let reader = CachedStorageReader::new(storage, CacheConfig::default());

        reader.read_storage(addr(1), slot(0)); // populate cache
        reader.invalidate_address(addr(1));

        // Next read goes back to the inner reader
        let result = reader.read_storage(addr(1), slot(0));
        assert_eq!(result, Some(val(50)));
        assert_eq!(
            reader.stats().misses,
            2,
            "should have 2 misses after invalidation"
        );
    }

    #[test]
    fn test_cached_reader_clear() {
        let storage = MockStorage::new().with_entry(addr(1), slot(0), val(77));
        let reader = CachedStorageReader::new(storage, CacheConfig::default());

        reader.read_storage(addr(1), slot(0));
        reader.clear();

        reader.read_storage(addr(1), slot(0));
        assert_eq!(reader.stats().misses, 2, "should re-fetch after clear");
    }

    #[test]
    fn test_cache_config_for_governance() {
        let cfg = CacheConfig::for_governance();
        assert_eq!(cfg.max_entries, 256);
        assert_eq!(cfg.invalidate_every_n_blocks, 30_000);
    }

    #[test]
    fn test_cache_config_large() {
        let cfg = CacheConfig::large(8192);
        assert_eq!(cfg.max_entries, 8192);
        assert_eq!(cfg.invalidate_every_n_blocks, 0);
    }

    #[test]
    fn test_cached_reader_evicts_when_full() {
        let mut storage = MockStorage::new();
        for i in 0..5u8 {
            storage.0.insert((addr(i), slot(0)), val(i));
        }
        let reader = CachedStorageReader::new(storage, CacheConfig::large(3));

        // Fill cache
        reader.read_storage(addr(0), slot(0));
        reader.read_storage(addr(1), slot(0));
        reader.read_storage(addr(2), slot(0));

        // This read evicts the LRU entry (addr(0))
        reader.read_storage(addr(3), slot(0));

        let stats = reader.stats();
        assert_eq!(stats.misses, 4);
        assert_eq!(stats.evictions, 1);
    }
}

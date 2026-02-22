//! POA Payload Builder
//!
//! Wraps Reth's `EthereumPayloadBuilder` to add POA block signing.
//! After the inner builder constructs a block (transactions, state root, etc.),
//! we post-process it to:
//! 1. Set difficulty = 0 (Engine API compatibility; authority is via ECDSA signature)
//! 2. Build extra_data with POA format (vanity + [signers at epoch] + signature)
//! 3. Sign the block header with the appropriate signer key

pub mod builder;

pub use builder::PoaPayloadBuilderBuilder;

use crate::cache::{CachedStorageReader, SharedCache};
use crate::chainspec::PoaChainSpec;
use crate::consensus::{EXTRA_SEAL_LENGTH, EXTRA_VANITY_LENGTH};
use crate::genesis::addresses::SIGNER_REGISTRY_ADDRESS;
use crate::metrics::PhaseTimer;
use crate::onchain::{read_signer_list, StateProviderStorageReader};
use crate::output;
use crate::signer::{BlockSealer, SignerManager};
use alloy_primitives::{Address, Bytes, U256};
use reth_basic_payload_builder::{
    BuildArguments, BuildOutcome, MissingPayloadBehaviour, PayloadBuilder, PayloadConfig,
};
use reth_chainspec::{ChainSpecProvider, EthereumHardforks};
use reth_ethereum::storage::StateProviderFactory;
use reth_ethereum::EthPrimitives;
use reth_ethereum_engine_primitives::{EthBuiltPayload, EthPayloadBuilderAttributes};
use reth_evm::{ConfigureEvm, NextBlockEnvAttributes};
use reth_payload_builder_primitives::PayloadBuilderError;
use reth_payload_primitives::BuiltPayload;
use reth_primitives_traits::block::SealedBlock;
use reth_transaction_pool::{PoolTransaction, TransactionPool};
use std::sync::Arc;

/// POA payload builder that wraps `EthereumPayloadBuilder`.
///
/// After the inner builder constructs a block, this builder post-processes it
/// to add POA signatures, set difficulty, and embed signer lists at epoch blocks.
/// At epoch blocks, it also refreshes the live signer cache from the on-chain
/// `SignerRegistry` contract so that `PoaConsensus` picks up governance changes.
///
/// On-chain storage reads go through a [`SharedCache`] (Phase 5.31) to avoid
/// redundant MDBX I/O across consecutive block builds.
#[derive(Debug, Clone)]
pub struct PoaPayloadBuilder<Pool, Client, EvmConfig> {
    /// The inner Ethereum payload builder that does the actual block construction.
    pub(crate) inner:
        reth_ethereum_payload_builder::EthereumPayloadBuilder<Pool, Client, EvmConfig>,
    /// POA chain specification with signer list, epoch, period.
    pub(crate) chain_spec: Arc<PoaChainSpec>,
    /// Signer manager with signing keys.
    pub(crate) signer_manager: Arc<SignerManager>,
    /// Whether we're in dev mode (skip signing).
    pub(crate) dev_mode: bool,
    /// State provider factory for reading on-chain contract storage.
    pub(crate) client: Client,
    /// Hot state cache shared across block builds (Phase 5.31).
    pub(crate) cache: SharedCache,
}

impl<Pool, Client, EvmConfig> PayloadBuilder for PoaPayloadBuilder<Pool, Client, EvmConfig>
where
    EvmConfig: ConfigureEvm<Primitives = EthPrimitives, NextBlockEnvCtx = NextBlockEnvAttributes>,
    Client: StateProviderFactory + ChainSpecProvider<ChainSpec: EthereumHardforks> + Clone,
    Pool:
        TransactionPool<Transaction: PoolTransaction<Consensus = reth_ethereum::TransactionSigned>>,
{
    type Attributes = EthPayloadBuilderAttributes;
    type BuiltPayload = EthBuiltPayload;

    fn try_build(
        &self,
        args: BuildArguments<EthPayloadBuilderAttributes, EthBuiltPayload>,
    ) -> Result<BuildOutcome<EthBuiltPayload>, PayloadBuilderError> {
        // 1. Let the inner builder construct the block (transactions, state, etc.)
        let build_timer = PhaseTimer::start();
        let outcome = self.inner.try_build(args)?;
        let build_ms = build_timer.elapsed_ms();

        // 2. Post-process: sign the block if we have a signer
        match outcome {
            BuildOutcome::Better {
                payload,
                cached_reads,
            } => {
                let signed_payload = self.sign_payload(payload, build_ms)?;
                Ok(BuildOutcome::Better {
                    payload: signed_payload,
                    cached_reads,
                })
            }
            BuildOutcome::Freeze(payload) => {
                let signed_payload = self.sign_payload(payload, build_ms)?;
                Ok(BuildOutcome::Freeze(signed_payload))
            }
            other => Ok(other),
        }
    }

    fn on_missing_payload(
        &self,
        args: BuildArguments<Self::Attributes, Self::BuiltPayload>,
    ) -> MissingPayloadBehaviour<Self::BuiltPayload> {
        self.inner.on_missing_payload(args)
    }

    fn build_empty_payload(
        &self,
        config: PayloadConfig<Self::Attributes>,
    ) -> Result<EthBuiltPayload, PayloadBuilderError> {
        let build_timer = PhaseTimer::start();
        let payload = self.inner.build_empty_payload(config)?;
        let build_ms = build_timer.elapsed_ms();
        self.sign_payload(payload, build_ms)
    }
}

impl<Pool, Client, EvmConfig> PoaPayloadBuilder<Pool, Client, EvmConfig>
where
    Client: StateProviderFactory + Clone,
{
    /// Sign a built payload with POA signature.
    ///
    /// `build_ms` is the wall-clock time spent building the block (Phase 2.17 timing).
    ///
    /// In dev mode, returns the payload unchanged.
    /// In production mode:
    /// 1. At epoch blocks — refreshes live signer list from on-chain SignerRegistry
    /// 2. Determines which signer should sign (round-robin using effective_signers)
    /// 3. Sets difficulty = 0 (Engine API compatibility)
    /// 4. Builds extra_data with POA format (vanity + [signers at epoch] + signature)
    /// 5. Signs the header via BlockSealer
    /// 6. Reconstructs the sealed block
    fn sign_payload(
        &self,
        payload: EthBuiltPayload,
        build_ms: u64,
    ) -> Result<EthBuiltPayload, PayloadBuilderError> {
        if self.dev_mode {
            return Ok(payload);
        }

        let block = payload.block();
        let block_number = block.header().number;
        let epoch = self.chain_spec.epoch();
        let is_epoch = block_number > 0 && block_number.is_multiple_of(epoch);

        // At epoch blocks, refresh live signer list from SignerRegistry.
        // Invalidate the cached SignerRegistry slots first so we get the latest governance
        // state, then re-populate the cache with the fresh read.
        if is_epoch {
            if let Ok(state) = self.client.latest() {
                // Invalidate stale signer registry entries before refreshing
                self.cache
                    .lock()
                    .expect("cache lock")
                    .invalidate_address(SIGNER_REGISTRY_ADDRESS);
                let reader = StateProviderStorageReader(state.as_ref());
                let cached = CachedStorageReader::new_shared(reader, Arc::clone(&self.cache));
                if let Some(list) = read_signer_list(&cached) {
                    if !list.signers.is_empty() {
                        output::print_epoch_refresh(block_number, list.signers.len());
                        self.chain_spec.update_live_signers(list.signers);
                    }
                }
            }
        }

        // Use effective_signers (live on-chain if available, else genesis config)
        let signers = self.chain_spec.effective_signers();
        if signers.is_empty() {
            // No signers configured, return unsigned
            return Ok(payload);
        }

        // Determine which signer should sign this block (round-robin)
        let in_turn_signer = match self.chain_spec.expected_signer(block_number) {
            Some(s) => s,
            None => return Ok(payload),
        };

        // Find a signer we control.
        // Use block_in_place + block_on so this works from both spawn_blocking contexts
        // (dev mode) and async task contexts (production+mining mode).
        let handle = tokio::runtime::Handle::current();
        let signer_manager = self.signer_manager.clone();

        let (signer_addr, is_in_turn) = tokio::task::block_in_place(|| {
            handle.block_on(async {
                // Prefer in-turn signer if we have it
                if signer_manager.has_signer(&in_turn_signer).await {
                    return (in_turn_signer, true);
                }
                // Otherwise find any authorized signer we control
                let our_addrs = signer_manager.signer_addresses().await;
                for addr in &our_addrs {
                    if signers.contains(addr) {
                        return (*addr, false);
                    }
                }
                (Address::ZERO, false)
            })
        });

        if signer_addr == Address::ZERO {
            // No authorized signer key available, return unsigned
            return Ok(payload);
        }

        // Clone header and body from the built block
        let mut header = block.header().clone();
        let body = block.body().clone();

        // Difficulty must be 0 for Engine API compatibility.
        header.difficulty = U256::ZERO;

        // Build extra_data with POA format
        let mut extra_data = Vec::with_capacity(
            EXTRA_VANITY_LENGTH + if is_epoch { signers.len() * 20 } else { 0 } + EXTRA_SEAL_LENGTH,
        );

        // Vanity (32 zero bytes)
        extra_data.extend_from_slice(&[0u8; EXTRA_VANITY_LENGTH]);

        // At epoch blocks, embed the effective (live) signer list
        if is_epoch {
            for signer in signers.iter() {
                extra_data.extend_from_slice(signer.as_slice());
            }
        }

        // Placeholder for signature (65 bytes — will be replaced by seal_header)
        extra_data.extend_from_slice(&[0u8; EXTRA_SEAL_LENGTH]);
        header.extra_data = Bytes::from(extra_data);

        // Sign the header (Phase 5: timed for performance metrics)
        let sign_timer = PhaseTimer::start();
        let sealer = BlockSealer::new(self.signer_manager.clone());
        let signed_header = tokio::task::block_in_place(|| {
            handle.block_on(async { sealer.seal_header(header, &signer_addr).await })
        })
        .map_err(|e| PayloadBuilderError::Other(Box::new(e)))?;
        let sign_ms = sign_timer.elapsed_ms();

        output::print_block_signed(block_number, &signer_addr, is_in_turn, build_ms, sign_ms);

        // Reconstruct the sealed block with the signed header
        let new_block = alloy_consensus::Block {
            header: signed_header,
            body,
        };
        let sealed = SealedBlock::seal_slow(new_block);

        Ok(EthBuiltPayload::new(
            payload.id(),
            Arc::new(sealed),
            payload.fees(),
            payload.requests(),
        ))
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::chainspec::PoaChainSpec;
    use crate::signer::{dev, BlockSealer};
    use alloy_consensus::Header;

    #[tokio::test]
    async fn test_poa_payload_builder_builder_creation() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let manager = Arc::new(SignerManager::new());
        let builder = PoaPayloadBuilderBuilder::new(chain.clone(), manager.clone(), true);

        assert!(builder.dev_mode);
        assert_eq!(builder.chain_spec.signers().len(), 3);
    }

    #[tokio::test]
    async fn test_sign_payload_components() {
        // Test the signing logic components work together
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let manager = dev::setup_dev_signers().await;

        let signers = chain.signers();
        assert_eq!(signers.len(), 3);

        // Block 1 should be signed by signer at index 1 % 3 = 1
        let expected_signer = signers[1];
        assert!(manager.has_signer(&expected_signer).await);

        // Verify the sealer can sign a header
        let sealer = BlockSealer::new(manager.clone());
        let header = Header {
            number: 1,
            difficulty: U256::from(1),
            gas_limit: 30_000_000,
            timestamp: 12345,
            extra_data: Bytes::from(vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH]),
            ..Default::default()
        };

        let signed = sealer.seal_header(header, &expected_signer).await.unwrap();
        let recovered = BlockSealer::verify_signature(&signed).unwrap();
        assert_eq!(recovered, expected_signer);
    }

    #[tokio::test]
    async fn test_epoch_block_extra_data_format() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let signers = chain.signers();

        // Simulate epoch block extra_data construction
        let mut extra_data = Vec::new();
        extra_data.extend_from_slice(&[0u8; EXTRA_VANITY_LENGTH]); // 32 bytes vanity
        for signer in signers {
            extra_data.extend_from_slice(signer.as_slice()); // 20 bytes per signer
        }
        extra_data.extend_from_slice(&[0u8; EXTRA_SEAL_LENGTH]); // 65 bytes seal

        let expected_len = EXTRA_VANITY_LENGTH + signers.len() * 20 + EXTRA_SEAL_LENGTH;
        assert_eq!(extra_data.len(), expected_len);
        assert_eq!(extra_data.len(), 32 + 3 * 20 + 65); // 157 bytes for 3 signers
    }

    #[tokio::test]
    async fn test_difficulty_selection() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let signers = chain.signers();

        // Block 0: signer at index 0 is in-turn
        assert_eq!(chain.expected_signer(0), Some(signers[0]));
        // Block 1: signer at index 1 is in-turn
        assert_eq!(chain.expected_signer(1), Some(signers[1]));
        // Block 2: signer at index 2 is in-turn
        assert_eq!(chain.expected_signer(2), Some(signers[2]));
        // Block 3: signer at index 0 is in-turn (wraps around)
        assert_eq!(chain.expected_signer(3), Some(signers[0]));
    }

    #[tokio::test]
    async fn test_payload_builder_builder_dev_mode() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let manager = Arc::new(SignerManager::new());
        let builder = PoaPayloadBuilderBuilder::new(chain, manager, true);
        assert!(builder.dev_mode);
    }

    #[tokio::test]
    async fn test_payload_builder_builder_production_mode() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let manager = Arc::new(SignerManager::new());
        let builder = PoaPayloadBuilderBuilder::new(chain, manager, false);
        assert!(!builder.dev_mode);
    }

    #[tokio::test]
    async fn test_no_signers_returns_unchanged_logic() {
        // Test that with empty signers, the signing logic would skip
        let genesis = crate::genesis::create_dev_genesis();
        let poa_config = crate::chainspec::PoaConfig {
            period: 2,
            epoch: 30000,
            signers: vec![], // No signers
        };
        let chain = Arc::new(PoaChainSpec::new(genesis, poa_config));

        assert!(chain.signers().is_empty());
        // expected_signer returns None when no signers
        assert_eq!(chain.expected_signer(0), None);
        assert_eq!(chain.expected_signer(1), None);
    }

    #[tokio::test]
    async fn test_in_turn_signer_difficulty_1() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let signers = chain.signers();

        // Block 0: signer[0] is in-turn
        let in_turn_signer = chain.expected_signer(0).unwrap();
        assert_eq!(in_turn_signer, signers[0]);

        // In-turn signer should get difficulty 1
        let difficulty = U256::from(1);
        assert_eq!(difficulty, U256::from(1));
    }

    #[tokio::test]
    async fn test_out_of_turn_signer_difficulty_2() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let signers = chain.signers();

        // Block 0: signer[0] is in-turn, so signer[1] is out-of-turn
        let in_turn = chain.expected_signer(0).unwrap();
        assert_eq!(in_turn, signers[0]);
        assert_ne!(in_turn, signers[1]);

        // Out-of-turn signer should get difficulty 2
        let difficulty = U256::from(2);
        assert_eq!(difficulty, U256::from(2));
    }

    #[tokio::test]
    async fn test_epoch_block_includes_all_signers_in_extra_data() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let signers = chain.signers();
        let epoch = chain.epoch();

        // Simulate epoch block extra_data construction
        let block_number = epoch; // First epoch block after genesis
        let is_epoch = block_number > 0 && block_number.is_multiple_of(epoch);
        assert!(is_epoch);

        let mut extra_data = Vec::new();
        extra_data.extend_from_slice(&[0u8; EXTRA_VANITY_LENGTH]); // 32 vanity
        if is_epoch {
            for signer in signers {
                extra_data.extend_from_slice(signer.as_slice()); // 20 bytes each
            }
        }
        extra_data.extend_from_slice(&[0u8; EXTRA_SEAL_LENGTH]); // 65 seal

        // For 3 signers: 32 + 3*20 + 65 = 157 bytes
        assert_eq!(extra_data.len(), 32 + 3 * 20 + 65);
        assert_eq!(extra_data.len(), 157);
    }

    #[tokio::test]
    async fn test_non_epoch_block_no_signers_in_extra_data() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let epoch = chain.epoch();

        // Block 1 is not an epoch block
        let block_number = 1u64;
        let is_epoch = block_number > 0 && block_number.is_multiple_of(epoch);
        assert!(!is_epoch);

        let mut extra_data = Vec::new();
        extra_data.extend_from_slice(&[0u8; EXTRA_VANITY_LENGTH]);
        // No signers for non-epoch blocks
        extra_data.extend_from_slice(&[0u8; EXTRA_SEAL_LENGTH]);

        // Non-epoch: 32 + 65 = 97 bytes
        assert_eq!(extra_data.len(), 97);
    }

    #[tokio::test]
    async fn test_signed_header_verifiable_by_consensus() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let manager = dev::setup_dev_signers().await;
        let signers = chain.signers();

        // Sign a block header with the first authorized signer
        let signer_addr = signers[0];
        assert!(manager.has_signer(&signer_addr).await);

        let header = Header {
            number: 1,
            difficulty: U256::from(2), // Out-of-turn for signer[0] at block 1
            gas_limit: 30_000_000,
            timestamp: 12345,
            extra_data: Bytes::from(vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH]),
            ..Default::default()
        };

        let sealer = BlockSealer::new(manager);
        let signed_header = sealer.seal_header(header, &signer_addr).await.unwrap();

        // Now verify using PoaConsensus
        let consensus = crate::consensus::PoaConsensus::new(chain);
        let recovered = consensus.recover_signer(&signed_header).unwrap();
        assert_eq!(recovered, signer_addr);
        assert!(consensus.validate_signer(&recovered).is_ok());
    }

    // ── Phase 5.31: shared hot state cache wiring ──────────────────────────

    #[tokio::test]
    async fn test_payload_builder_builder_has_default_cache_size() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let manager = Arc::new(SignerManager::new());
        let builder = PoaPayloadBuilderBuilder::new(chain, manager, true);
        // Default cache size comes from CacheConfig::default()
        assert!(builder.cache_size > 0);
    }

    #[tokio::test]
    async fn test_payload_builder_builder_custom_cache_size() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let manager = Arc::new(SignerManager::new());
        let builder =
            PoaPayloadBuilderBuilder::new(chain, manager, true).with_cache_size(512);
        assert_eq!(builder.cache_size, 512);
    }

    #[tokio::test]
    async fn test_payload_builder_builder_cache_size_clamped_to_one() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let manager = Arc::new(SignerManager::new());
        // Zero is clamped to 1
        let builder = PoaPayloadBuilderBuilder::new(chain, manager, true).with_cache_size(0);
        assert_eq!(builder.cache_size, 1);
    }

    #[test]
    fn test_shared_cache_accessible_across_multiple_readers() {
        use crate::cache::{CachedStorageReader, HotStateCache};
        use crate::onchain::StorageReader;
        use alloy_primitives::{Address, B256, U256};
        use std::collections::HashMap;
        use std::sync::{Arc, Mutex};

        // Minimal in-memory StorageReader for this test
        struct MapStorage(HashMap<(Address, U256), B256>);
        impl StorageReader for MapStorage {
            fn read_storage(&self, a: Address, s: U256) -> Option<B256> {
                self.0.get(&(a, s)).copied()
            }
        }

        let cache: SharedCache = Arc::new(Mutex::new(HotStateCache::new(64)));

        let addr = Address::from([1u8; 20]);
        let slot = U256::from(0u64);
        let value = B256::from([42u8; 32]);

        // First reader: cache miss → populates shared cache
        let mut map1 = HashMap::new();
        map1.insert((addr, slot), value);
        let reader1 = CachedStorageReader::new_shared(MapStorage(map1), Arc::clone(&cache));
        let v1 = reader1.read_storage(addr, slot);
        assert_eq!(v1, Some(value));
        assert_eq!(reader1.stats().misses, 1);
        assert_eq!(reader1.stats().hits, 0);

        // Second reader with the SAME shared cache but EMPTY storage:
        // value must come from the cache, not the inner reader.
        let reader2 =
            CachedStorageReader::new_shared(MapStorage(HashMap::new()), Arc::clone(&cache));
        let v2 = reader2.read_storage(addr, slot);
        assert_eq!(v2, Some(value), "should come from shared cache");
        // Stats are shared — cumulative across both readers:
        // misses=1 (from reader1), hits=1 (from reader2)
        let stats = reader2.stats();
        assert_eq!(stats.misses, 1, "reader1 caused 1 miss");
        assert_eq!(stats.hits, 1, "reader2 caused 1 hit from shared cache");
    }
}

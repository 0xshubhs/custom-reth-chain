use crate::cache::{CacheConfig, CachedStorageReader, HotStateCache, SharedCache};
use crate::chainspec::PoaChainSpec;
use crate::consensus::{EXTRA_SEAL_LENGTH, EXTRA_VANITY_LENGTH};
use crate::onchain::{read_gas_limit, read_signer_list, StateProviderStorageReader};
use crate::output;
use crate::signer::SignerManager;
use alloy_primitives::Bytes;
use reth_chainspec::{EthChainSpec, EthereumHardforks};
use reth_ethereum::node::api::{FullNodeTypes, NodeTypes, PrimitivesTy, TxTy};
use reth_ethereum::node::builder::{components::PayloadBuilderBuilder, BuilderContext};
use reth_ethereum::node::core::cli::config::PayloadBuilderConfig;
use reth_ethereum::storage::StateProviderFactory;
use reth_ethereum::EthPrimitives;
use reth_ethereum_engine_primitives::{EthBuiltPayload, EthPayloadAttributes};
use reth_ethereum_payload_builder::EthereumBuilderConfig;
use reth_evm::{ConfigureEvm, NextBlockEnvAttributes};
use reth_payload_primitives::PayloadTypes;
use reth_transaction_pool::{PoolTransaction, TransactionPool};
use std::sync::{Arc, Mutex};

use super::PoaPayloadBuilder;

/// Component-level builder that creates `PoaPayloadBuilder` instances.
/// Plugs into `BasicPayloadServiceBuilder` in the node's `ComponentsBuilder`.
#[derive(Clone, Debug)]
pub struct PoaPayloadBuilderBuilder {
    pub(crate) chain_spec: Arc<PoaChainSpec>,
    pub(crate) signer_manager: Arc<SignerManager>,
    pub(crate) dev_mode: bool,
    /// Capacity for the per-builder hot state cache (number of (address, slot) entries).
    pub(crate) cache_size: usize,
}

impl PoaPayloadBuilderBuilder {
    /// Create a new POA payload builder builder with a given cache capacity.
    pub fn new(
        chain_spec: Arc<PoaChainSpec>,
        signer_manager: Arc<SignerManager>,
        dev_mode: bool,
    ) -> Self {
        Self {
            chain_spec,
            signer_manager,
            dev_mode,
            cache_size: CacheConfig::default().max_entries,
        }
    }

    /// Override the hot state cache capacity (Phase 5.31).
    pub fn with_cache_size(mut self, size: usize) -> Self {
        self.cache_size = size.max(1); // at least 1 entry
        self
    }
}

impl<Types, Node, Pool, Evm> PayloadBuilderBuilder<Node, Pool, Evm> for PoaPayloadBuilderBuilder
where
    Types: NodeTypes<ChainSpec: EthereumHardforks, Primitives = EthPrimitives>,
    Node: FullNodeTypes<Types = Types>,
    Pool: TransactionPool<Transaction: PoolTransaction<Consensus = TxTy<Node::Types>>>
        + Unpin
        + 'static,
    Evm: ConfigureEvm<Primitives = PrimitivesTy<Types>, NextBlockEnvCtx = NextBlockEnvAttributes>
        + 'static,
    Types::Payload: PayloadTypes<
        BuiltPayload = EthBuiltPayload,
        PayloadAttributes = EthPayloadAttributes,
    >,
{
    type PayloadBuilder = PoaPayloadBuilder<Pool, Node::Provider, Evm>;

    async fn build_payload_builder(
        self,
        ctx: &BuilderContext<Node>,
        pool: Pool,
        evm_config: Evm,
    ) -> eyre::Result<Self::PayloadBuilder> {
        let conf = ctx.payload_builder_config();
        let chain = ctx.chain_spec().chain();
        let default_gas_limit = conf.gas_limit_for(chain);

        // Create the shared hot state cache (Phase 5.31).
        // Startup reads populate the cache; subsequent epoch reads re-use it.
        let cache: SharedCache = Arc::new(Mutex::new(HotStateCache::new(self.cache_size)));

        // Read gas limit and signer list from on-chain contracts at startup.
        // Uses a single state snapshot and shared cache for both reads.
        let gas_limit = match ctx.provider().latest() {
            Ok(state) => {
                let reader = StateProviderStorageReader(state.as_ref());
                let cached = CachedStorageReader::new_shared(reader, Arc::clone(&cache));

                // Read gas limit from ChainConfig
                let onchain_gas = read_gas_limit(&cached).filter(|&gl| gl > 0);
                let gl = if let Some(gl) = onchain_gas {
                    if gl != default_gas_limit {
                        output::print_onchain_gas_limit(gl, default_gas_limit);
                    }
                    gl
                } else {
                    default_gas_limit
                };

                // Seed live signer cache from SignerRegistry
                if let Some(list) = read_signer_list(&cached) {
                    if !list.signers.is_empty() {
                        output::print_onchain_signers(list.signers.len());
                        self.chain_spec.update_live_signers(list.signers);
                    }
                }

                gl
            }
            Err(_) => default_gas_limit,
        };

        // In production mode, pre-allocate POA extra_data (vanity + seal placeholder).
        // In dev mode, leave extra_data empty — blocks are unsigned and Reth's engine
        // rejects extra_data > 32 bytes (Ethereum mainnet limit).
        let extra_data = if self.dev_mode {
            Bytes::new()
        } else {
            Bytes::from(vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH])
        };

        let inner = reth_ethereum_payload_builder::EthereumPayloadBuilder::new(
            ctx.provider().clone(),
            pool,
            evm_config,
            EthereumBuilderConfig::new()
                .with_gas_limit(gas_limit)
                .with_max_blobs_per_block(conf.max_blobs_per_block())
                .with_extra_data(extra_data),
        );

        Ok(PoaPayloadBuilder {
            inner,
            chain_spec: self.chain_spec,
            signer_manager: self.signer_manager,
            dev_mode: self.dev_mode,
            client: ctx.provider().clone(),
            cache,
        })
    }
}

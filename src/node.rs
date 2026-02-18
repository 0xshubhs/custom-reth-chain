//! Custom POA Node Type
//!
//! Defines a `PoaNode` that replaces Ethereum's beacon consensus with `PoaConsensus`.
//! This is the core architectural change that makes the node actually use POA consensus
//! instead of being a vanilla Ethereum dev-mode node with unused POA code.

use crate::chainspec::PoaChainSpec;
use crate::consensus::PoaConsensus;
use crate::payload::PoaPayloadBuilderBuilder;
use crate::signer::SignerManager;
use std::sync::Arc;

// Node builder types
use reth_ethereum::node::builder::{
    components::{BasicPayloadServiceBuilder, ComponentsBuilder, ConsensusBuilder},
    node::{FullNodeTypes, NodeTypes},
    BuilderContext, DebugNode, Node, NodeAdapter,
};

// Node API types
use reth_ethereum::node::api::{EngineTypes, FullNodeComponents, PayloadAttributesBuilder};

// Ethereum component builders (pool, network, executor, payload)
use reth_ethereum::node::{
    EthEngineTypes, EthereumAddOns, EthereumEngineValidator,
    EthereumEthApiBuilder, EthereumExecutorBuilder, EthereumNetworkBuilder, EthereumPoolBuilder,
};

// Primitive and storage types
use reth_ethereum::{provider::EthStorage, EthPrimitives};

// Engine types for payload attributes
use reth_ethereum::engine::local::LocalPayloadAttributesBuilder;

// Payload types
use reth_payload_primitives::PayloadTypes;

// Chain spec
use reth_chainspec::ChainSpec;

// ─── PoaEngineValidator ──────────────────────────────────────────────────────
//
// Alloy's ExecutionPayloadV1::into_block_raw_with_transactions_root_opt() rejects
// extra_data > MAXIMUM_EXTRA_DATA_SIZE (32 bytes). POA blocks use 97-byte extra_data
// (65-byte vanity + 32-byte ECDSA seal). This validator strips extra_data before the
// alloy conversion, then restores it and reseals, so the block hash is correct.

use alloy_rpc_types_engine::{ExecutionData, ExecutionPayload, PayloadError};
use reth_ethereum_engine_primitives::EthPayloadAttributes;
use reth_ethereum::node::api::{EngineApiValidator, PayloadValidator};
use reth_ethereum::node::builder::rpc::{
    BasicEngineApiBuilder, BasicEngineValidatorBuilder, Identity, PayloadValidatorBuilder,
    RpcAddOns,
};
use reth_ethereum::node::api::AddOnsContext;
use reth_payload_primitives::{
    EngineApiMessageVersion, EngineObjectValidationError, NewPayloadError, PayloadOrAttributes,
};
use reth_primitives_traits::SealedBlock;

/// Strip `extra_data` from an [`ExecutionPayload`], returning `(stripped, original_extra_data)`.
///
/// POA blocks carry 97 bytes in extra_data (vanity + seal). Alloy's conversion
/// rejects any extra_data > 32 bytes, so we must strip it before conversion and
/// restore it after.
fn strip_extra_data(payload: ExecutionPayload) -> (ExecutionPayload, alloy_primitives::Bytes) {
    match payload {
        ExecutionPayload::V1(mut v1) => {
            let extra = std::mem::take(&mut v1.extra_data);
            (ExecutionPayload::V1(v1), extra)
        }
        ExecutionPayload::V2(mut v2) => {
            let extra = std::mem::take(&mut v2.payload_inner.extra_data);
            (ExecutionPayload::V2(v2), extra)
        }
        ExecutionPayload::V3(mut v3) => {
            let extra = std::mem::take(&mut v3.payload_inner.payload_inner.extra_data);
            (ExecutionPayload::V3(v3), extra)
        }
    }
}

/// Custom engine validator that allows POA blocks with extra_data > 32 bytes.
///
/// Wraps [`EthereumEngineValidator`] and overrides only [`PayloadValidator::convert_payload_to_block`]
/// to strip/restore POA extra_data around alloy's strict 32-byte check.
#[derive(Debug, Clone)]
pub struct PoaEngineValidator<ChainSpec = reth_chainspec::ChainSpec> {
    inner: EthereumEngineValidator<ChainSpec>,
}

impl<ChainSpec> PoaEngineValidator<ChainSpec> {
    /// Creates a new validator with the given chain spec.
    pub const fn new(chain_spec: Arc<ChainSpec>) -> Self {
        Self { inner: EthereumEngineValidator::new(chain_spec) }
    }
}

impl<ChainSpec, Types> PayloadValidator<Types> for PoaEngineValidator<ChainSpec>
where
    ChainSpec: reth_chainspec::EthChainSpec + reth_ethereum_forks::EthereumHardforks + 'static,
    Types: PayloadTypes<ExecutionData = ExecutionData>,
{
    type Block = reth_ethereum::Block;

    fn convert_payload_to_block(
        &self,
        payload: ExecutionData,
    ) -> Result<SealedBlock<Self::Block>, NewPayloadError> {
        let ExecutionData { payload, sidecar } = payload;
        let expected_hash = payload.block_hash();

        // Strip extra_data to bypass alloy's 32-byte MAXIMUM_EXTRA_DATA_SIZE check.
        // POA blocks use 97 bytes (65-byte vanity + 32-byte ECDSA seal).
        let (stripped, orig_extra) = strip_extra_data(payload);

        // Convert to block — succeeds now because extra_data is empty.
        let mut block: reth_ethereum::Block = stripped
            .try_into_block_with_sidecar(&sidecar)
            .map_err(|e| NewPayloadError::Other(e.into()))?;

        // Restore the original extra_data.
        block.header.extra_data = orig_extra;

        // Reseal: recompute the block hash with the restored extra_data.
        let sealed = SealedBlock::seal_slow(block);

        // Verify the hash matches what the engine sent us.
        if expected_hash != sealed.hash() {
            return Err(PayloadError::BlockHash {
                execution: sealed.hash(),
                consensus: expected_hash,
            }
            .into());
        }

        Ok(sealed)
    }
}

impl<ChainSpec, Types> EngineApiValidator<Types> for PoaEngineValidator<ChainSpec>
where
    ChainSpec: reth_chainspec::EthChainSpec + reth_ethereum_forks::EthereumHardforks + 'static,
    Types: PayloadTypes<PayloadAttributes = EthPayloadAttributes, ExecutionData = ExecutionData>,
{
    fn validate_version_specific_fields(
        &self,
        version: EngineApiMessageVersion,
        payload_or_attrs: PayloadOrAttributes<'_, Types::ExecutionData, EthPayloadAttributes>,
    ) -> Result<(), EngineObjectValidationError> {
        <EthereumEngineValidator<ChainSpec> as EngineApiValidator<Types>>::validate_version_specific_fields(
            &self.inner,
            version,
            payload_or_attrs,
        )
    }

    fn ensure_well_formed_attributes(
        &self,
        version: EngineApiMessageVersion,
        attributes: &EthPayloadAttributes,
    ) -> Result<(), EngineObjectValidationError> {
        <EthereumEngineValidator<ChainSpec> as EngineApiValidator<Types>>::ensure_well_formed_attributes(
            &self.inner,
            version,
            attributes,
        )
    }
}

/// Builder for [`PoaEngineValidator`].
#[derive(Debug, Default, Clone)]
pub struct PoaEngineValidatorBuilder;

impl<Node, Types> PayloadValidatorBuilder<Node> for PoaEngineValidatorBuilder
where
    Types: NodeTypes<
        ChainSpec: reth_chainspec::EthChainSpec
            + reth_ethereum_forks::EthereumHardforks
            + Clone
            + 'static,
        Payload: EngineTypes<ExecutionData = ExecutionData>
            + PayloadTypes<PayloadAttributes = EthPayloadAttributes>,
        Primitives = EthPrimitives,
    >,
    Node: FullNodeComponents<Types = Types>,
{
    type Validator = PoaEngineValidator<Types::ChainSpec>;

    async fn build(self, ctx: &AddOnsContext<'_, Node>) -> eyre::Result<Self::Validator> {
        Ok(PoaEngineValidator::new(ctx.config.chain.clone()))
    }
}

// ─── PoaConsensusBuilder ─────────────────────────────────────────────────────

/// Custom consensus builder that provides `PoaConsensus` instead of `EthBeaconConsensus`.
///
/// This is the key integration point: when the node builder constructs components,
/// it calls this builder to create the consensus engine. By providing `PoaConsensus`,
/// all block validation flows through our POA rules.
#[derive(Debug, Clone)]
pub struct PoaConsensusBuilder {
    /// The POA chain specification with signer list, epoch, period, etc.
    chain_spec: Arc<PoaChainSpec>,
    /// Whether to create consensus in dev mode (relaxed validation)
    dev_mode: bool,
}

impl PoaConsensusBuilder {
    /// Create a new consensus builder with the given POA chain spec.
    pub fn new(chain_spec: Arc<PoaChainSpec>) -> Self {
        Self { chain_spec, dev_mode: false }
    }

    /// Set dev mode on the consensus builder
    pub fn with_dev_mode(mut self, dev_mode: bool) -> Self {
        self.dev_mode = dev_mode;
        self
    }
}

impl<N> ConsensusBuilder<N> for PoaConsensusBuilder
where
    N: FullNodeTypes<Types: NodeTypes<Primitives = EthPrimitives>>,
{
    type Consensus = Arc<PoaConsensus>;

    async fn build_consensus(self, _ctx: &BuilderContext<N>) -> eyre::Result<Self::Consensus> {
        let mode = if self.dev_mode { "dev (relaxed)" } else { "production (strict)" };
        println!(
            "POA Consensus initialized: {} signers, epoch: {}, period: {}s, mode: {}",
            self.chain_spec.signers().len(),
            self.chain_spec.epoch(),
            self.chain_spec.block_period(),
            mode,
        );
        Ok(Arc::new(
            PoaConsensus::new(self.chain_spec).with_dev_mode(self.dev_mode),
        ))
    }
}

// ─── PoaNode ─────────────────────────────────────────────────────────────────

/// Custom POA Node type.
///
/// This replaces `EthereumNode` as the node type passed to the builder.
/// It uses the exact same primitives, storage, and engine types as Ethereum,
/// but provides `PoaConsensus` instead of `EthBeaconConsensus` for block validation,
/// and `PoaEngineValidator` to accept POA blocks with 97-byte extra_data.
///
/// The architecture is:
/// ```text
/// PoaNode
///   ├── Primitives: EthPrimitives (identical to mainnet)
///   ├── ChainSpec: ChainSpec (standard Reth chain spec)
///   ├── Storage: EthStorage (standard MDBX storage)
///   ├── Payload: EthEngineTypes (standard engine API)
///   └── Components:
///       ├── Pool: EthereumPoolBuilder (standard tx pool)
///       ├── Network: EthereumNetworkBuilder (standard P2P)
///       ├── Executor: EthereumExecutorBuilder (standard EVM)
///       ├── Payload: PoaPayloadBuilder ← CUSTOM (signs blocks, sets difficulty)
///       └── Consensus: PoaConsensusBuilder ← CUSTOM (validates POA signatures)
///   └── AddOns:
///       └── PoaEngineValidator ← CUSTOM (allows 97-byte POA extra_data)
/// ```
#[derive(Debug, Clone)]
pub struct PoaNode {
    /// POA chain specification with signer config.
    chain_spec: Arc<PoaChainSpec>,
    /// Signer manager with signing keys for block production.
    signer_manager: Arc<SignerManager>,
    /// Whether the node runs in dev mode (relaxed consensus validation)
    dev_mode: bool,
}

impl PoaNode {
    /// Create a new PoaNode with the given chain specification.
    pub fn new(chain_spec: Arc<PoaChainSpec>) -> Self {
        Self {
            chain_spec,
            signer_manager: Arc::new(SignerManager::new()),
            dev_mode: false,
        }
    }

    /// Set dev mode on the node
    pub fn with_dev_mode(mut self, dev_mode: bool) -> Self {
        self.dev_mode = dev_mode;
        self
    }

    /// Set the signer manager for block production
    pub fn with_signer_manager(mut self, signer_manager: Arc<SignerManager>) -> Self {
        self.signer_manager = signer_manager;
        self
    }
}

// PoaNode uses the same type configuration as EthereumNode
impl NodeTypes for PoaNode {
    type Primitives = EthPrimitives;
    type ChainSpec = ChainSpec;
    type Storage = EthStorage;
    type Payload = EthEngineTypes;
}

// The Node implementation provides the ComponentsBuilder that wires everything together.
// The only difference from EthereumNode is the consensus builder and the engine validator.
impl<N> Node<N> for PoaNode
where
    N: FullNodeTypes<Types = Self>,
{
    type ComponentsBuilder = ComponentsBuilder<
        N,
        EthereumPoolBuilder,
        BasicPayloadServiceBuilder<PoaPayloadBuilderBuilder>,
        EthereumNetworkBuilder,
        EthereumExecutorBuilder,
        PoaConsensusBuilder,
    >;

    type AddOns = EthereumAddOns<
        NodeAdapter<N>,
        EthereumEthApiBuilder,
        PoaEngineValidatorBuilder,
        BasicEngineApiBuilder<PoaEngineValidatorBuilder>,
        BasicEngineValidatorBuilder<PoaEngineValidatorBuilder>,
        Identity,
    >;

    fn components_builder(&self) -> Self::ComponentsBuilder {
        ComponentsBuilder::default()
            .node_types::<N>()
            .pool(EthereumPoolBuilder::default())
            .executor(EthereumExecutorBuilder::default())
            .payload(BasicPayloadServiceBuilder::new(
                PoaPayloadBuilderBuilder::new(
                    self.chain_spec.clone(),
                    self.signer_manager.clone(),
                    self.dev_mode,
                ),
            ))
            .network(EthereumNetworkBuilder::default())
            .consensus(
                PoaConsensusBuilder::new(self.chain_spec.clone()).with_dev_mode(self.dev_mode),
            )
    }

    fn add_ons(&self) -> Self::AddOns {
        EthereumAddOns::new(RpcAddOns::new(
            EthereumEthApiBuilder::default(),
            PoaEngineValidatorBuilder,
            BasicEngineApiBuilder::<PoaEngineValidatorBuilder>::default(),
            BasicEngineValidatorBuilder::new(PoaEngineValidatorBuilder),
            Identity::default(),
        ))
    }
}

// DebugNode enables launch_with_debug_capabilities(), which properly sets up dev mining.
impl<N: FullNodeComponents<Types = Self>> DebugNode<N> for PoaNode {
    type RpcBlock = reth_ethereum::rpc::eth::primitives::Block;

    fn rpc_to_primitive_block(rpc_block: Self::RpcBlock) -> reth_ethereum::Block {
        rpc_block.into_consensus().convert_transactions()
    }

    fn local_payload_attributes_builder(
        chain_spec: &Self::ChainSpec,
    ) -> impl PayloadAttributesBuilder<<Self::Payload as PayloadTypes>::PayloadAttributes> {
        LocalPayloadAttributesBuilder::new(Arc::new(chain_spec.clone()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_poa_node_creation() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let node = PoaNode::new(chain.clone());
        assert_eq!(node.chain_spec.signers().len(), 3);
    }

    #[test]
    fn test_poa_node_with_dev_mode() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let node = PoaNode::new(chain).with_dev_mode(true);
        assert!(node.dev_mode);
    }

    #[test]
    fn test_poa_node_with_signer_manager() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let manager = Arc::new(SignerManager::new());
        let node = PoaNode::new(chain).with_signer_manager(manager.clone());
        // Verify the manager is set (compare Arc pointers)
        assert!(Arc::ptr_eq(&node.signer_manager, &manager));
    }

    #[test]
    fn test_poa_node_full_builder_chain() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let manager = Arc::new(SignerManager::new());
        let node = PoaNode::new(chain)
            .with_dev_mode(true)
            .with_signer_manager(manager.clone());
        assert!(node.dev_mode);
        assert!(Arc::ptr_eq(&node.signer_manager, &manager));
        assert_eq!(node.chain_spec.signers().len(), 3);
    }

    #[test]
    fn test_poa_consensus_builder_creation() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let builder = PoaConsensusBuilder::new(chain);
        assert!(!builder.dev_mode);
    }

    #[test]
    fn test_poa_consensus_builder_dev_mode() {
        let chain = Arc::new(PoaChainSpec::dev_chain());
        let builder = PoaConsensusBuilder::new(chain).with_dev_mode(true);
        assert!(builder.dev_mode);
    }

    #[test]
    fn test_strip_extra_data_v1() {
        use alloy_rpc_types_engine::{ExecutionPayload, ExecutionPayloadV1};
        use alloy_primitives::{Bytes, B256, U256, Address, Bloom};

        let v1 = ExecutionPayloadV1 {
            parent_hash: B256::ZERO,
            fee_recipient: Address::ZERO,
            state_root: B256::ZERO,
            receipts_root: B256::ZERO,
            logs_bloom: Bloom::default(),
            prev_randao: B256::ZERO,
            block_number: 1,
            gas_limit: 30_000_000,
            gas_used: 0,
            timestamp: 0,
            extra_data: Bytes::from(vec![0u8; 97]),
            base_fee_per_gas: U256::from(1000000000u64),
            block_hash: B256::ZERO,
            transactions: vec![],
        };

        let payload = ExecutionPayload::V1(v1);
        let (stripped, orig) = strip_extra_data(payload);
        assert_eq!(orig.len(), 97);
        match stripped {
            ExecutionPayload::V1(v) => assert_eq!(v.extra_data.len(), 0),
            _ => panic!("expected V1"),
        }
    }

    #[test]
    fn test_poa_engine_validator_builder_is_default() {
        let _builder = PoaEngineValidatorBuilder;
        let _default = PoaEngineValidatorBuilder::default();
    }
}

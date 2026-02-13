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
use reth_ethereum::node::api::{FullNodeComponents, PayloadAttributesBuilder};

// Ethereum component builders (pool, network, executor, payload)
use reth_ethereum::node::{
    EthEngineTypes, EthereumAddOns, EthereumEngineValidatorBuilder,
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

/// Custom POA Node type.
///
/// This replaces `EthereumNode` as the node type passed to the builder.
/// It uses the exact same primitives, storage, and engine types as Ethereum,
/// but provides `PoaConsensus` instead of `EthBeaconConsensus` for block validation.
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
// The only difference from EthereumNode is the consensus builder.
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

    type AddOns =
        EthereumAddOns<NodeAdapter<N>, EthereumEthApiBuilder, EthereumEngineValidatorBuilder>;

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
        EthereumAddOns::default()
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
}

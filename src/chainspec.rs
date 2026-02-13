//! POA Chain Specification
//!
//! This module defines the chain specification for a POA network that maintains
//! full compatibility with Ethereum mainnet's EVM and hardforks.

use alloy_consensus::Header;
use alloy_eips::eip7840::BlobParams;
use alloy_genesis::Genesis;
use alloy_primitives::{Address, B256, U256};
use reth_chainspec::{
    BaseFeeParams, BaseFeeParamsKind, Chain, ChainHardforks, ChainSpec, DepositContract,
    EthChainSpec, EthereumHardforks, ForkCondition, ForkFilter, ForkId, Hardfork, Hardforks, Head,
};
use reth_ethereum_forks::EthereumHardfork;
use reth_network_peers::NodeRecord;
use reth_primitives_traits::SealedHeader;
use serde::{Deserialize, Serialize};
use std::sync::Arc;

/// POA-specific configuration that extends the standard chain config
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PoaConfig {
    /// Block period in seconds (time between blocks)
    pub period: u64,
    /// Number of blocks after which to checkpoint and reset the pending votes
    pub epoch: u64,
    /// List of authorized signer addresses
    pub signers: Vec<Address>,
}

impl Default for PoaConfig {
    fn default() -> Self {
        Self {
            period: 12, // 12 second block time like mainnet
            epoch: 30000,
            signers: vec![],
        }
    }
}

/// Custom POA chain specification
#[derive(Debug, Clone)]
pub struct PoaChainSpec {
    /// The underlying Ethereum chain spec
    inner: Arc<ChainSpec>,
    /// POA-specific configuration
    poa_config: PoaConfig,
}

impl PoaChainSpec {
    /// Creates a new POA chain spec from genesis and POA config
    pub fn new(genesis: Genesis, poa_config: PoaConfig) -> Self {
        // Build hardforks - enable all Ethereum hardforks for mainnet compatibility
        let hardforks = Self::mainnet_compatible_hardforks();

        let genesis_header = reth_chainspec::make_genesis_header(&genesis, &hardforks);

        let inner = ChainSpec {
            chain: Chain::from_id(genesis.config.chain_id),
            genesis_header: SealedHeader::seal_slow(genesis_header),
            genesis,
            // Post-merge from the start (POA doesn't use proof of work)
            paris_block_and_final_difficulty: Some((0, U256::ZERO)),
            hardforks,
            deposit_contract: None,
            base_fee_params: BaseFeeParamsKind::Constant(BaseFeeParams::ethereum()),
            prune_delete_limit: 10000,
            blob_params: Default::default(),
        };

        Self { inner: Arc::new(inner), poa_config }
    }

    /// Creates a development POA chain with prefunded accounts
    pub fn dev_chain() -> Self {
        let genesis = crate::genesis::create_dev_genesis();
        let poa_config = PoaConfig {
            period: 2, // Fast 2-second blocks for dev
            epoch: 30000,
            signers: crate::genesis::dev_signers(),
        };
        Self::new(genesis, poa_config)
    }

    /// Creates hardforks configuration that matches Ethereum mainnet
    /// This ensures full smart contract compatibility
    fn mainnet_compatible_hardforks() -> ChainHardforks {
        // Enable all hardforks at genesis (block 0 / timestamp 0)
        // This gives you the latest Ethereum features immediately
        ChainHardforks::new(vec![
            // Block-based hardforks (all at block 0)
            (EthereumHardfork::Frontier.boxed(), ForkCondition::Block(0)),
            (EthereumHardfork::Homestead.boxed(), ForkCondition::Block(0)),
            (EthereumHardfork::Tangerine.boxed(), ForkCondition::Block(0)),
            (EthereumHardfork::SpuriousDragon.boxed(), ForkCondition::Block(0)),
            (EthereumHardfork::Byzantium.boxed(), ForkCondition::Block(0)),
            (EthereumHardfork::Constantinople.boxed(), ForkCondition::Block(0)),
            (EthereumHardfork::Petersburg.boxed(), ForkCondition::Block(0)),
            (EthereumHardfork::Istanbul.boxed(), ForkCondition::Block(0)),
            (EthereumHardfork::Berlin.boxed(), ForkCondition::Block(0)),
            (EthereumHardfork::London.boxed(), ForkCondition::Block(0)),
            // The Merge - we use TTD of 0 since POA doesn't have proof of work
            (
                EthereumHardfork::Paris.boxed(),
                ForkCondition::TTD {
                    activation_block_number: 0,
                    fork_block: None,
                    total_difficulty: U256::ZERO,
                },
            ),
            // Timestamp-based hardforks (all at timestamp 0)
            (EthereumHardfork::Shanghai.boxed(), ForkCondition::Timestamp(0)),
            (EthereumHardfork::Cancun.boxed(), ForkCondition::Timestamp(0)),
            (EthereumHardfork::Prague.boxed(), ForkCondition::Timestamp(0)),
        ])
    }

    /// Returns the inner ChainSpec
    pub fn inner(&self) -> &Arc<ChainSpec> {
        &self.inner
    }

    /// Returns the POA configuration
    pub fn poa_config(&self) -> &PoaConfig {
        &self.poa_config
    }

    /// Returns the list of authorized signers
    pub fn signers(&self) -> &[Address] {
        &self.poa_config.signers
    }

    /// Returns the block period in seconds
    pub fn block_period(&self) -> u64 {
        self.poa_config.period
    }

    /// Returns the epoch length
    pub fn epoch(&self) -> u64 {
        self.poa_config.epoch
    }

    /// Check if an address is an authorized signer
    pub fn is_authorized_signer(&self, address: &Address) -> bool {
        self.poa_config.signers.contains(address)
    }

    /// Get the expected signer for a given block number (round-robin)
    pub fn expected_signer(&self, block_number: u64) -> Option<&Address> {
        if self.poa_config.signers.is_empty() {
            return None;
        }
        let index = (block_number as usize) % self.poa_config.signers.len();
        self.poa_config.signers.get(index)
    }
}

// Implement required traits to make PoaChainSpec work with Reth

impl Hardforks for PoaChainSpec {
    fn fork<H: Hardfork>(&self, fork: H) -> ForkCondition {
        self.inner.fork(fork)
    }

    fn forks_iter(&self) -> impl Iterator<Item = (&dyn Hardfork, ForkCondition)> {
        self.inner.forks_iter()
    }

    fn fork_id(&self, head: &Head) -> ForkId {
        self.inner.fork_id(head)
    }

    fn latest_fork_id(&self) -> ForkId {
        self.inner.latest_fork_id()
    }

    fn fork_filter(&self, head: Head) -> ForkFilter {
        self.inner.fork_filter(head)
    }
}

impl EthChainSpec for PoaChainSpec {
    type Header = Header;

    fn chain(&self) -> Chain {
        self.inner.chain()
    }

    fn base_fee_params_at_timestamp(&self, timestamp: u64) -> BaseFeeParams {
        self.inner.base_fee_params_at_timestamp(timestamp)
    }

    fn blob_params_at_timestamp(&self, timestamp: u64) -> Option<BlobParams> {
        self.inner.blob_params_at_timestamp(timestamp)
    }

    fn deposit_contract(&self) -> Option<&DepositContract> {
        self.inner.deposit_contract()
    }

    fn genesis_hash(&self) -> B256 {
        self.inner.genesis_hash()
    }

    fn prune_delete_limit(&self) -> usize {
        self.inner.prune_delete_limit()
    }

    fn display_hardforks(&self) -> Box<dyn core::fmt::Display> {
        self.inner.display_hardforks()
    }

    fn genesis_header(&self) -> &Self::Header {
        self.inner.genesis_header()
    }

    fn genesis(&self) -> &Genesis {
        self.inner.genesis()
    }

    fn bootnodes(&self) -> Option<Vec<NodeRecord>> {
        self.inner.bootnodes()
    }

    fn final_paris_total_difficulty(&self) -> Option<U256> {
        self.inner.get_final_paris_total_difficulty()
    }
}

impl EthereumHardforks for PoaChainSpec {
    fn ethereum_fork_activation(&self, fork: EthereumHardfork) -> ForkCondition {
        self.inner.ethereum_fork_activation(fork)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_dev_chain_creation() {
        let chain = PoaChainSpec::dev_chain();
        assert!(!chain.signers().is_empty());
        assert_eq!(chain.block_period(), 2);
    }

    #[test]
    fn test_dev_chain_id() {
        let chain = PoaChainSpec::dev_chain();
        assert_eq!(chain.inner().chain.id(), 9323310);
    }

    #[test]
    fn test_dev_chain_signers_count() {
        let chain = PoaChainSpec::dev_chain();
        assert_eq!(chain.signers().len(), 3); // First 3 dev accounts
    }

    #[test]
    fn test_dev_chain_epoch() {
        let chain = PoaChainSpec::dev_chain();
        assert_eq!(chain.epoch(), 30000);
    }

    #[test]
    fn test_hardforks_enabled() {
        let chain = PoaChainSpec::dev_chain();

        // All major hardforks should be active at block 0
        assert!(chain.fork(EthereumHardfork::Frontier).active_at_block(0));
        assert!(chain.fork(EthereumHardfork::Homestead).active_at_block(0));
        assert!(chain.fork(EthereumHardfork::Byzantium).active_at_block(0));
        assert!(chain.fork(EthereumHardfork::Constantinople).active_at_block(0));
        assert!(chain.fork(EthereumHardfork::Istanbul).active_at_block(0));
        assert!(chain.fork(EthereumHardfork::Berlin).active_at_block(0));
        assert!(chain.fork(EthereumHardfork::London).active_at_block(0));
        assert!(chain.fork(EthereumHardfork::Shanghai).active_at_timestamp(0));
        assert!(chain.fork(EthereumHardfork::Cancun).active_at_timestamp(0));
        assert!(chain.fork(EthereumHardfork::Prague).active_at_timestamp(0));
    }

    #[test]
    fn test_authorized_signer_check() {
        let chain = PoaChainSpec::dev_chain();
        let signers = chain.signers();

        // First signer should be authorized
        assert!(chain.is_authorized_signer(&signers[0]));

        // Random address should NOT be authorized
        let fake: Address = "0x0000000000000000000000000000000000000099".parse().unwrap();
        assert!(!chain.is_authorized_signer(&fake));
    }

    #[test]
    fn test_round_robin_signer() {
        let genesis = crate::genesis::create_dev_genesis();
        let poa_config = PoaConfig {
            period: 2,
            epoch: 30000,
            signers: vec![
                "0x0000000000000000000000000000000000000001".parse().unwrap(),
                "0x0000000000000000000000000000000000000002".parse().unwrap(),
                "0x0000000000000000000000000000000000000003".parse().unwrap(),
            ],
        };
        let chain = PoaChainSpec::new(genesis, poa_config);

        // Test round-robin assignment
        assert_eq!(
            chain.expected_signer(0),
            Some(&"0x0000000000000000000000000000000000000001".parse().unwrap())
        );
        assert_eq!(
            chain.expected_signer(1),
            Some(&"0x0000000000000000000000000000000000000002".parse().unwrap())
        );
        assert_eq!(
            chain.expected_signer(2),
            Some(&"0x0000000000000000000000000000000000000003".parse().unwrap())
        );
        assert_eq!(
            chain.expected_signer(3),
            Some(&"0x0000000000000000000000000000000000000001".parse().unwrap())
        );
    }

    #[test]
    fn test_empty_signers_expected_signer() {
        let genesis = crate::genesis::create_dev_genesis();
        let poa_config = PoaConfig {
            period: 2,
            epoch: 30000,
            signers: vec![], // No signers
        };
        let chain = PoaChainSpec::new(genesis, poa_config);

        assert_eq!(chain.expected_signer(0), None);
        assert_eq!(chain.expected_signer(100), None);
    }

    #[test]
    fn test_poa_config_default() {
        let config = PoaConfig::default();
        assert_eq!(config.period, 12);
        assert_eq!(config.epoch, 30000);
        assert!(config.signers.is_empty());
    }

    #[test]
    fn test_production_chain_creation() {
        let genesis = crate::genesis::create_genesis(crate::genesis::GenesisConfig::production());
        let poa_config = PoaConfig {
            period: 12,
            epoch: 30000,
            signers: crate::genesis::dev_accounts().into_iter().take(5).collect(),
        };
        let chain = PoaChainSpec::new(genesis, poa_config);

        assert_eq!(chain.signers().len(), 5);
        assert_eq!(chain.block_period(), 12);
        assert_eq!(chain.epoch(), 30000);
        assert_eq!(chain.inner().chain.id(), 9323310);
    }

    #[test]
    fn test_paris_total_difficulty() {
        let chain = PoaChainSpec::dev_chain();
        // POA starts post-merge with TTD=0
        assert_eq!(chain.inner().paris_block_and_final_difficulty, Some((0, U256::ZERO)));
    }

    #[test]
    fn test_genesis_hash_deterministic() {
        let chain1 = PoaChainSpec::dev_chain();
        let chain2 = PoaChainSpec::dev_chain();
        assert_eq!(chain1.inner().genesis_hash(), chain2.inner().genesis_hash());
    }

    #[test]
    fn test_eth_chain_spec_trait() {
        let chain = PoaChainSpec::dev_chain();
        // Test EthChainSpec trait methods
        assert_eq!(chain.chain().id(), 9323310);
        assert!(chain.deposit_contract().is_none()); // POA has no deposit contract
        assert_eq!(chain.prune_delete_limit(), 10000);
    }

    #[test]
    fn test_fork_id_and_filter() {
        let chain = PoaChainSpec::dev_chain();
        let head = Head { number: 0, timestamp: 0, ..Default::default() };

        // Should not panic
        let _fork_id = chain.fork_id(&head);
        let _latest = chain.latest_fork_id();
        let _filter = chain.fork_filter(head);
    }

    #[test]
    fn test_dev_vs_production_config_comparison() {
        // Verify the CLAUDE.md configuration table
        let dev_chain = PoaChainSpec::dev_chain();
        assert_eq!(dev_chain.inner().chain.id(), 9323310);
        assert_eq!(dev_chain.block_period(), 2);
        assert_eq!(dev_chain.inner().genesis().gas_limit, 30_000_000);
        assert_eq!(dev_chain.signers().len(), 3);
        assert_eq!(dev_chain.epoch(), 30000);

        let prod_genesis = crate::genesis::create_genesis(crate::genesis::GenesisConfig::production());
        let prod_config = PoaConfig {
            period: 12,
            epoch: 30000,
            signers: crate::genesis::dev_accounts().into_iter().take(5).collect(),
        };
        let prod_chain = PoaChainSpec::new(prod_genesis, prod_config);
        assert_eq!(prod_chain.inner().chain.id(), 9323310);
        assert_eq!(prod_chain.block_period(), 12);
        assert_eq!(prod_chain.inner().genesis().gas_limit, 60_000_000);
        assert_eq!(prod_chain.signers().len(), 5);
        assert_eq!(prod_chain.epoch(), 30000);
    }

    #[test]
    fn test_all_dev_signers_authorized() {
        let chain = PoaChainSpec::dev_chain();
        let signers = chain.signers();
        for signer in signers {
            assert!(
                chain.is_authorized_signer(signer),
                "Signer {} should be authorized",
                signer
            );
        }
    }

    #[test]
    fn test_single_signer_chain() {
        let genesis = crate::genesis::create_dev_genesis();
        let signer: Address = "0x0000000000000000000000000000000000000042".parse().unwrap();
        let poa_config = PoaConfig {
            period: 2,
            epoch: 30000,
            signers: vec![signer],
        };
        let chain = PoaChainSpec::new(genesis, poa_config);

        // Single signer should always be the expected signer
        for block_num in 0..100u64 {
            assert_eq!(chain.expected_signer(block_num), Some(&signer));
        }
    }

    #[test]
    fn test_large_signer_set() {
        let genesis = crate::genesis::create_dev_genesis();
        let signers: Vec<Address> = (1..=21u64)
            .map(|i| {
                let mut bytes = [0u8; 20];
                bytes[19] = i as u8;
                Address::from(bytes)
            })
            .collect();
        let poa_config = PoaConfig {
            period: 2,
            epoch: 30000,
            signers: signers.clone(),
        };
        let chain = PoaChainSpec::new(genesis, poa_config);

        assert_eq!(chain.signers().len(), 21);

        // Round-robin should wrap at 21
        for i in 0..21u64 {
            assert_eq!(chain.expected_signer(i), Some(&signers[i as usize]));
        }
        // Block 21 wraps back to signer[0]
        assert_eq!(chain.expected_signer(21), Some(&signers[0]));
        assert_eq!(chain.expected_signer(42), Some(&signers[0]));
    }

    #[test]
    fn test_custom_chain_parameters() {
        let genesis = crate::genesis::create_genesis(
            crate::genesis::GenesisConfig::default().with_chain_id(42),
        );
        let poa_config = PoaConfig {
            period: 5,
            epoch: 100,
            signers: vec!["0x0000000000000000000000000000000000000001".parse().unwrap()],
        };
        let chain = PoaChainSpec::new(genesis, poa_config);

        assert_eq!(chain.inner().chain.id(), 42);
        assert_eq!(chain.block_period(), 5);
        assert_eq!(chain.epoch(), 100);
        assert_eq!(chain.signers().len(), 1);
    }

    #[test]
    fn test_base_fee_params_delegation() {
        let chain = PoaChainSpec::dev_chain();
        let params = chain.base_fee_params_at_timestamp(0);
        let inner_params = chain.inner().base_fee_params_at_timestamp(0);
        assert_eq!(params, inner_params);
    }

    #[test]
    fn test_bootnodes_returns_none() {
        let chain = PoaChainSpec::dev_chain();
        assert!(chain.bootnodes().is_none());
    }

    #[test]
    fn test_ethereum_fork_activation_all_forks() {
        let chain = PoaChainSpec::dev_chain();

        // Block-based forks should be active at block 0
        let frontier = chain.ethereum_fork_activation(EthereumHardfork::Frontier);
        assert!(frontier.active_at_block(0));

        let london = chain.ethereum_fork_activation(EthereumHardfork::London);
        assert!(london.active_at_block(0));

        // Timestamp-based forks should be active at timestamp 0
        let shanghai = chain.ethereum_fork_activation(EthereumHardfork::Shanghai);
        assert!(shanghai.active_at_timestamp(0));

        let cancun = chain.ethereum_fork_activation(EthereumHardfork::Cancun);
        assert!(cancun.active_at_timestamp(0));

        let prague = chain.ethereum_fork_activation(EthereumHardfork::Prague);
        assert!(prague.active_at_timestamp(0));
    }
}

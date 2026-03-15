//! Genesis Configuration for POA Chain
//!
//! This module provides utilities for creating genesis configurations
//! that are compatible with Ethereum tooling while supporting POA consensus.

pub mod accounts;
pub mod addresses;
mod contracts;
mod governance;

// Re-export public API
pub use accounts::{default_prefund_balance, dev_accounts, dev_signers};
pub use addresses::{
    CHAIN_CONFIG_ADDRESS, GOVERNANCE_SAFE_ADDRESS, MINER_PROXY_ADDRESS,
    SAFE_FALLBACK_HANDLER_ADDRESS, SAFE_MULTISEND_ADDRESS, SAFE_PROXY_FACTORY_ADDRESS,
    SAFE_SINGLETON_ADDRESS, SIGNER_REGISTRY_ADDRESS, TIMELOCK_ADDRESS, TREASURY_ADDRESS,
};

use alloy_genesis::{Genesis, GenesisAccount};
use alloy_primitives::{Address, U256};
use std::collections::BTreeMap;

/// Create a development genesis configuration
pub fn create_dev_genesis() -> Genesis {
    create_genesis(GenesisConfig::dev())
}

/// Configuration for creating a genesis
#[derive(Debug, Clone)]
pub struct GenesisConfig {
    /// Chain ID
    pub chain_id: u64,
    /// Gas limit for the genesis block
    pub gas_limit: u64,
    /// Accounts to prefund with their balances
    pub prefunded_accounts: BTreeMap<Address, U256>,
    /// POA signers (encoded in extra data)
    pub signers: Vec<Address>,
    /// Block time in seconds
    pub block_period: u64,
    /// Epoch length for checkpoint blocks
    pub epoch: u64,
    /// Optional extra vanity data (32 bytes)
    pub vanity: [u8; 32],
}

impl Default for GenesisConfig {
    fn default() -> Self {
        Self {
            chain_id: 9323310,
            gas_limit: 30_000_000,
            prefunded_accounts: BTreeMap::new(),
            signers: vec![],
            block_period: 12,
            epoch: 30000,
            vanity: [0u8; 32],
        }
    }
}

impl GenesisConfig {
    /// Create a development configuration with prefunded accounts
    pub fn dev() -> Self {
        let balance = default_prefund_balance();
        let prefunded = dev_accounts().into_iter().map(|a| (a, balance)).collect();

        Self {
            chain_id: 9323310,
            gas_limit: 300_000_000, // Phase 2: 300M gas limit (MegaETH-inspired, 10x Ethereum mainnet)
            prefunded_accounts: prefunded,
            signers: dev_signers(),
            block_period: 1, // Phase 2: 1-second blocks
            epoch: 30000,
            vanity: [0u8; 32],
        }
    }

    /// Create a mainnet-like configuration
    pub fn mainnet_compatible(chain_id: u64, signers: Vec<Address>) -> Self {
        Self {
            chain_id,
            gas_limit: 30_000_000,
            prefunded_accounts: BTreeMap::new(),
            signers,
            block_period: 12, // Same as Ethereum mainnet
            epoch: 30000,
            vanity: [0u8; 32],
        }
    }

    /// Create a production configuration for Meowchain
    ///
    /// - Chain ID: 9323310
    /// - 5 signers (fault-tolerant: survives 2 offline, needs 3/5 online)
    /// - 12-second block time
    /// - 60M gas limit (high throughput for POA)
    /// - Treasury, operations, and community accounts prefunded
    /// - "Meowchain" vanity in genesis block
    pub fn production() -> Self {
        let accounts = dev_accounts();
        let signers = accounts[..5].to_vec();

        let eth = U256::from(10u64).pow(U256::from(18u64)); // 1 ETH in wei
        // Treasury: 2,500,000 ETH - ecosystem development fund
        let treasury_balance = U256::from(2_500_000u64) * eth;
        // Operations: 500,000 ETH - infrastructure and running costs
        let operations_balance = U256::from(500_000u64) * eth;
        // Community: 100,000 ETH - faucet, airdrops, grants
        let community_balance = U256::from(100_000u64) * eth;
        // Signer gas: 10,000 ETH per signer - for block production gas costs
        let signer_balance = U256::from(10_000u64) * eth;

        let mut prefunded = BTreeMap::new();

        // Signers get gas money (first 5 accounts)
        for signer in &signers {
            prefunded.insert(*signer, signer_balance);
        }

        // Treasury (account index 5)
        prefunded.insert(accounts[5], treasury_balance);
        // Operations (account index 6)
        prefunded.insert(accounts[6], operations_balance);
        // Community/Faucet (account index 7)
        prefunded.insert(accounts[7], community_balance);

        // "Meowchain" as vanity data
        let mut vanity = [0u8; 32];
        let tag = b"Meowchain";
        vanity[..tag.len()].copy_from_slice(tag);

        Self {
            chain_id: 9323310,
            gas_limit: 1_000_000_000, // Phase 2: 1B gas (on-chain ChainConfig can lower it)
            prefunded_accounts: prefunded,
            signers,
            block_period: 2, // Production: 2s (faster than Ethereum's 12s)
            epoch: 30000,
            vanity,
        }
    }

    /// Builder method to add a prefunded account
    pub fn with_prefunded_account(mut self, address: Address, balance: U256) -> Self {
        self.prefunded_accounts.insert(address, balance);
        self
    }

    /// Builder method to set signers
    pub fn with_signers(mut self, signers: Vec<Address>) -> Self {
        self.signers = signers;
        self
    }

    /// Builder method to set chain ID
    pub fn with_chain_id(mut self, chain_id: u64) -> Self {
        self.chain_id = chain_id;
        self
    }

    /// Builder method to set block period
    pub fn with_block_period(mut self, period: u64) -> Self {
        self.block_period = period;
        self
    }

    /// Builder method to set vanity data
    pub fn with_vanity(mut self, vanity: [u8; 32]) -> Self {
        self.vanity = vanity;
        self
    }
}

/// Create a genesis configuration from the config
pub fn create_genesis(config: GenesisConfig) -> Genesis {
    // Build the extra data field for POA:
    // Format: [vanity (32 bytes)][signers (N*20 bytes)][signature (65 bytes, all zeros for genesis)]
    let mut extra_data = Vec::with_capacity(32 + config.signers.len() * 20 + 65);

    // Add vanity (32 bytes)
    extra_data.extend_from_slice(&config.vanity);

    // Add signer addresses
    for signer in &config.signers {
        extra_data.extend_from_slice(signer.as_slice());
    }

    // Add empty signature (65 bytes of zeros for genesis block)
    extra_data.extend_from_slice(&[0u8; 65]);

    // Convert prefunded accounts to genesis alloc format
    let mut alloc: BTreeMap<Address, GenesisAccount> = config
        .prefunded_accounts
        .into_iter()
        .map(|(addr, balance)| (addr, GenesisAccount { balance, ..Default::default() }))
        .collect();

    // Add system contracts required by Cancun/Prague hardforks
    alloc.extend(contracts::system_contract_alloc());

    // Add ERC-4337 Account Abstraction and infrastructure contracts
    alloc.extend(contracts::erc4337_contract_alloc());

    // Add EIP-1967 Miner Proxy for anonymous block reward collection
    // Admin is set to the governance Safe address
    alloc.extend(contracts::miner_proxy_alloc(GOVERNANCE_SAFE_ADDRESS));

    // Add governance contracts (ChainConfig, SignerRegistry, Treasury)
    // Governance is set to the governance Safe address
    alloc.extend(governance::governance_contract_alloc(
        GOVERNANCE_SAFE_ADDRESS,
        &config.signers,
        config.gas_limit,
        config.block_period,
    ));

    // Add Gnosis Safe contracts for multisig governance
    alloc.extend(contracts::safe_contract_alloc());

    // Build the chain config JSON
    let chain_config = serde_json::json!({
        "chainId": config.chain_id,
        "homesteadBlock": 0,
        "eip150Block": 0,
        "eip155Block": 0,
        "eip158Block": 0,
        "byzantiumBlock": 0,
        "constantinopleBlock": 0,
        "petersburgBlock": 0,
        "istanbulBlock": 0,
        "berlinBlock": 0,
        "londonBlock": 0,
        "terminalTotalDifficulty": 0,
        "terminalTotalDifficultyPassed": true,
        "shanghaiTime": 0,
        "cancunTime": 0,
        "pragueTime": 0,
        // POA-specific config (stored in extra fields)
        "clique": {
            "period": config.block_period,
            "epoch": config.epoch
        }
    });

    Genesis {
        config: serde_json::from_value(chain_config).expect("valid chain config"),
        nonce: 0,
        timestamp: 0,
        extra_data: extra_data.into(),
        gas_limit: config.gas_limit,
        difficulty: U256::from(1),
        mix_hash: Default::default(),
        coinbase: MINER_PROXY_ADDRESS,
        alloc,
        number: None,
        parent_hash: None,
        base_fee_per_gas: Some(875_000_000), // EIP-1559 initial base fee (0.875 gwei)
        excess_blob_gas: Some(0),
        blob_gas_used: Some(0),
    }
}

/// Helper to serialize genesis to JSON (for use with other tools)
pub fn genesis_to_json(genesis: &Genesis) -> String {
    serde_json::to_string_pretty(genesis).expect("genesis serialization should not fail")
}

/// Helper to create a genesis file on disk
pub fn write_genesis_file(genesis: &Genesis, path: &std::path::Path) -> std::io::Result<()> {
    let json = genesis_to_json(genesis);
    std::fs::write(path, json)
}

#[cfg(test)]
mod tests {
    use super::*;
    use addresses::EIP1967_ADMIN_SLOT;
    use alloy_primitives::{address, b256, B256};

    #[test]
    fn test_dev_genesis_creation() {
        let genesis = create_dev_genesis();

        // Verify chain ID (dev config uses 9323310, create_genesis now uses config.chain_id)
        assert_eq!(genesis.config.chain_id, 9323310);

        // Verify accounts are prefunded
        assert!(!genesis.alloc.is_empty());
        assert_eq!(genesis.alloc.len(), 38); // 20 dev accounts + 4 system contracts + 5 ERC-4337/infra + 1 miner proxy + 4 governance (incl. Timelock) + 4 Safe

        // Verify extra data contains signers
        assert!(genesis.extra_data.len() >= 32 + 65); // At least vanity + seal
    }

    #[test]
    fn test_production_genesis_creation() {
        let config = GenesisConfig::production();
        let genesis = create_genesis(config);

        // Verify production chain ID
        assert_eq!(genesis.config.chain_id, 9323310);

        // Verify: 8 prefunded accounts (5 signers + treasury + ops + community) + 4 system contracts + 5 ERC-4337/infra + 1 miner proxy + 4 governance (incl. Timelock) + 4 Safe
        assert_eq!(genesis.alloc.len(), 26);

        // Verify gas limit is 1B (Phase 2 default)
        assert_eq!(genesis.gas_limit, 1_000_000_000);

        // Verify extra data has 5 signers: 32 + 5*20 + 65 = 197 bytes
        assert_eq!(genesis.extra_data.len(), 197);

        // Verify vanity starts with "Meowchain"
        assert_eq!(&genesis.extra_data[..9], b"Meowchain");
    }

    #[test]
    fn test_custom_genesis() {
        let signer = address!("0000000000000000000000000000000000000001");
        let funded = address!("0000000000000000000000000000000000000002");

        let config = GenesisConfig::default()
            .with_chain_id(12345)
            .with_signers(vec![signer])
            .with_prefunded_account(funded, U256::from(1000));

        let genesis = create_genesis(config);

        // chain_id bug fixed: create_genesis now uses config.chain_id
        assert_eq!(genesis.config.chain_id, 12345);
        assert!(genesis.alloc.contains_key(&funded));
        assert_eq!(
            genesis.alloc.get(&funded).unwrap().balance,
            U256::from(1000)
        );
    }

    #[test]
    fn test_genesis_json_serialization() {
        let genesis = create_dev_genesis();
        let json = genesis_to_json(&genesis);

        // Verify it's valid JSON
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();
        assert!(parsed.is_object());
    }

    #[test]
    fn test_extra_data_format() {
        let signers = vec![
            address!("0000000000000000000000000000000000000001"),
            address!("0000000000000000000000000000000000000002"),
        ];

        let config = GenesisConfig::default().with_signers(signers);
        let genesis = create_genesis(config);

        // Extra data should be: 32 (vanity) + 2*20 (signers) + 65 (seal) = 137 bytes
        assert_eq!(genesis.extra_data.len(), 32 + 40 + 65);
    }

    #[test]
    fn test_dev_accounts_count() {
        assert_eq!(dev_accounts().len(), 20);
    }

    #[test]
    fn test_dev_signers_count() {
        assert_eq!(dev_signers().len(), 3);
    }

    #[test]
    fn test_dev_signers_are_subset_of_accounts() {
        let accounts = dev_accounts();
        let signers = dev_signers();
        for signer in &signers {
            assert!(accounts.contains(signer));
        }
    }

    #[test]
    fn test_genesis_base_fee() {
        let genesis = create_dev_genesis();
        assert_eq!(genesis.base_fee_per_gas, Some(875_000_000)); // 0.875 gwei
    }

    #[test]
    fn test_genesis_blob_support() {
        let genesis = create_dev_genesis();
        assert_eq!(genesis.excess_blob_gas, Some(0));
        assert_eq!(genesis.blob_gas_used, Some(0));
    }

    #[test]
    fn test_genesis_difficulty() {
        let genesis = create_dev_genesis();
        assert_eq!(genesis.difficulty, U256::from(1));
    }

    #[test]
    fn test_default_prefund_balance() {
        let balance = default_prefund_balance();
        // 10,000 ETH in wei
        let expected = U256::from(10_000u64) * U256::from(10u64).pow(U256::from(18u64));
        assert_eq!(balance, expected);
    }

    #[test]
    fn test_system_contracts_have_code() {
        let genesis = create_dev_genesis();

        // EIP-4788 Beacon Root
        let contract = genesis
            .alloc
            .get(&address!("000F3df6D732807Ef1319fB7B8bB8522d0Beac02"));
        assert!(contract.is_some());
        assert!(contract.unwrap().code.is_some());
        assert!(!contract.unwrap().code.as_ref().unwrap().is_empty());

        // EIP-2935 History Storage
        let contract = genesis
            .alloc
            .get(&address!("0000F90827F1C53a10cb7A02335B175320002935"));
        assert!(contract.is_some());
        assert!(contract.unwrap().code.is_some());

        // EIP-7002 Withdrawal Requests
        let contract = genesis
            .alloc
            .get(&address!("00000961Ef480Eb55e80D19ad83579A64c007002"));
        assert!(contract.is_some());
        assert!(contract.unwrap().code.is_some());

        // EIP-7251 Consolidation
        let contract = genesis
            .alloc
            .get(&address!("0000BBdDc7CE488642fb579F8B00f3a590007251"));
        assert!(contract.is_some());
        assert!(contract.unwrap().code.is_some());
    }

    #[test]
    fn test_mainnet_compatible_config() {
        let signers = vec![
            address!("0000000000000000000000000000000000000001"),
            address!("0000000000000000000000000000000000000002"),
        ];
        let config = GenesisConfig::mainnet_compatible(12345, signers.clone());
        assert_eq!(config.chain_id, 12345);
        assert_eq!(config.gas_limit, 30_000_000);
        assert_eq!(config.block_period, 12);
        assert_eq!(config.signers, signers);
    }

    #[test]
    fn test_genesis_config_builders() {
        let config = GenesisConfig::default()
            .with_chain_id(999)
            .with_block_period(5)
            .with_vanity([0xAA; 32]);
        assert_eq!(config.chain_id, 999);
        assert_eq!(config.block_period, 5);
        assert_eq!(config.vanity, [0xAA; 32]);
    }

    #[test]
    fn test_regenerate_sample_genesis() {
        // This test regenerates sample-genesis.json from the actual code
        let genesis = create_dev_genesis();
        let json = genesis_to_json(&genesis);
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();

        // Verify chain ID is correct
        assert_eq!(parsed["config"]["chainId"], 9323310);

        // Write to genesis/ directory (canonical location)
        let genesis_dir = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("genesis");
        std::fs::create_dir_all(&genesis_dir).unwrap();
        let path = genesis_dir.join("sample-genesis.json");
        write_genesis_file(&genesis, &path).unwrap();
    }

    #[test]
    fn test_regenerate_production_genesis() {
        // This test regenerates production-genesis.json from the actual code
        let config = GenesisConfig::production();
        let genesis = create_genesis(config);
        let json = genesis_to_json(&genesis);
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();

        // Verify production chain ID
        assert_eq!(parsed["config"]["chainId"], 9323310);

        // Verify gas limit is 1B (Phase 2 default)
        assert_eq!(genesis.gas_limit, 1_000_000_000);

        // Verify all contracts present: 8 prefunded + 4 system + 5 infra + 1 miner proxy + 4 governance (incl. Timelock) + 4 safe = 26
        assert_eq!(genesis.alloc.len(), 26);

        // Write to genesis/ directory (canonical location)
        let genesis_dir = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("genesis");
        std::fs::create_dir_all(&genesis_dir).unwrap();
        let path = genesis_dir.join("production-genesis.json");
        write_genesis_file(&genesis, &path).unwrap();
    }

    #[test]
    fn test_production_genesis_has_all_contracts() {
        let config = GenesisConfig::production();
        let genesis = create_genesis(config);

        // All governance contracts must be present
        assert!(
            genesis.alloc.contains_key(&CHAIN_CONFIG_ADDRESS),
            "Production must have ChainConfig"
        );
        assert!(
            genesis.alloc.contains_key(&SIGNER_REGISTRY_ADDRESS),
            "Production must have SignerRegistry"
        );
        assert!(
            genesis.alloc.contains_key(&TREASURY_ADDRESS),
            "Production must have Treasury"
        );
        assert!(
            genesis.alloc.contains_key(&TIMELOCK_ADDRESS),
            "Production must have Timelock"
        );

        // Miner proxy must be present (coinbase target)
        assert!(
            genesis.alloc.contains_key(&MINER_PROXY_ADDRESS),
            "Production must have Miner Proxy"
        );
        assert_eq!(genesis.coinbase, MINER_PROXY_ADDRESS);

        // Safe contracts must be present
        assert!(
            genesis.alloc.contains_key(&SAFE_SINGLETON_ADDRESS),
            "Production must have Safe Singleton"
        );
        assert!(
            genesis.alloc.contains_key(&SAFE_PROXY_FACTORY_ADDRESS),
            "Production must have Safe Proxy Factory"
        );
        assert!(
            genesis.alloc.contains_key(&SAFE_FALLBACK_HANDLER_ADDRESS),
            "Production must have Safe Fallback Handler"
        );
        assert!(
            genesis.alloc.contains_key(&SAFE_MULTISEND_ADDRESS),
            "Production must have Safe MultiSend"
        );

        // ERC-4337 infrastructure must be present
        assert!(
            genesis
                .alloc
                .contains_key(&address!("0000000071727De22E5E9d8BAf0edAc6f37da032")),
            "Production must have EntryPoint"
        );
        assert!(
            genesis
                .alloc
                .contains_key(&address!("C02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2")),
            "Production must have WETH9"
        );
        assert!(
            genesis
                .alloc
                .contains_key(&address!("cA11bde05977b3631167028862bE2a173976CA11")),
            "Production must have Multicall3"
        );

        // System contracts must be present
        assert!(
            genesis
                .alloc
                .contains_key(&address!("000F3df6D732807Ef1319fB7B8bB8522d0Beac02")),
            "Production must have EIP-4788"
        );
        assert!(
            genesis
                .alloc
                .contains_key(&address!("0000F90827F1C53a10cb7A02335B175320002935")),
            "Production must have EIP-2935"
        );

        // ChainConfig should have 1B gas limit in storage (Phase 2 default)
        let chain_config = genesis.alloc.get(&CHAIN_CONFIG_ADDRESS).unwrap();
        let storage = chain_config.storage.as_ref().unwrap();
        let slot1 = b256!("0000000000000000000000000000000000000000000000000000000000000001");
        assert_eq!(
            *storage.get(&slot1).unwrap(),
            B256::from(U256::from(1_000_000_000u64).to_be_bytes()),
            "Production ChainConfig gas limit should be 1B"
        );

        // SignerRegistry should have 5 signers
        let signer_registry = genesis.alloc.get(&SIGNER_REGISTRY_ADDRESS).unwrap();
        let storage = signer_registry.storage.as_ref().unwrap();
        assert_eq!(
            *storage.get(&slot1).unwrap(),
            B256::from(U256::from(5u64).to_be_bytes()),
            "Production SignerRegistry should have 5 signers"
        );
    }

    #[test]
    fn test_miner_proxy_in_genesis() {
        let genesis = create_dev_genesis();

        // Coinbase should be the miner proxy address
        assert_eq!(genesis.coinbase, MINER_PROXY_ADDRESS);

        // Miner proxy contract should be in genesis alloc
        let proxy = genesis.alloc.get(&MINER_PROXY_ADDRESS);
        assert!(proxy.is_some(), "Miner proxy must be in genesis");
        let proxy = proxy.unwrap();
        assert!(proxy.code.is_some(), "Miner proxy must have bytecode");
        assert_eq!(proxy.nonce, Some(1));
        assert!(
            proxy.storage.is_some(),
            "Miner proxy must have storage (admin slot)"
        );

        // Admin should be first dev signer
        let storage = proxy.storage.as_ref().unwrap();
        assert!(
            storage.contains_key(&EIP1967_ADMIN_SLOT),
            "Must have EIP-1967 admin slot"
        );
    }

    #[test]
    fn test_erc4337_contracts_in_genesis() {
        let genesis = create_dev_genesis();

        // EntryPoint v0.7 at canonical address
        let entrypoint = genesis
            .alloc
            .get(&address!("0000000071727De22E5E9d8BAf0edAc6f37da032"));
        assert!(entrypoint.is_some(), "EntryPoint v0.7 must be in genesis");
        assert!(
            entrypoint.unwrap().code.is_some(),
            "EntryPoint must have code"
        );
        assert_eq!(entrypoint.unwrap().nonce, Some(1));

        // WETH9 at canonical mainnet address
        let weth = genesis
            .alloc
            .get(&address!("C02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"));
        assert!(weth.is_some(), "WETH9 must be in genesis");
        assert!(weth.unwrap().code.is_some(), "WETH9 must have code");
        assert!(
            weth.unwrap().storage.is_some(),
            "WETH9 must have initialized storage"
        );
        let weth_storage = weth.unwrap().storage.as_ref().unwrap();
        assert_eq!(
            weth_storage.len(),
            3,
            "WETH9 needs name, symbol, decimals slots"
        );

        // Multicall3 at canonical address
        assert!(
            genesis
                .alloc
                .contains_key(&address!("cA11bde05977b3631167028862bE2a173976CA11")),
            "Multicall3 must be in genesis"
        );

        // CREATE2 Deployer at canonical address
        assert!(
            genesis
                .alloc
                .contains_key(&address!("4e59b44847b379578588920cA78FbF26c0B4956C")),
            "CREATE2 Deployer must be in genesis"
        );

        // SimpleAccountFactory
        assert!(
            genesis
                .alloc
                .contains_key(&address!("9406Cc6185a346906296840746125a0E44976454")),
            "SimpleAccountFactory must be in genesis"
        );
    }

    #[test]
    fn test_governance_contracts_in_genesis() {
        let genesis = create_dev_genesis();

        // ChainConfig contract
        let chain_config = genesis.alloc.get(&CHAIN_CONFIG_ADDRESS);
        assert!(chain_config.is_some(), "ChainConfig must be in genesis");
        let chain_config = chain_config.unwrap();
        assert!(chain_config.code.is_some(), "ChainConfig must have code");
        assert_eq!(chain_config.nonce, Some(1));
        assert!(
            chain_config.storage.is_some(),
            "ChainConfig must have storage"
        );
        let storage = chain_config.storage.as_ref().unwrap();
        // Should have slots for governance, gasLimit, blockTime, maxContractSize, calldataGasPerByte, maxTxGas
        assert!(
            storage.len() >= 6,
            "ChainConfig needs at least 6 storage slots"
        );

        // SignerRegistry contract
        let signer_registry = genesis.alloc.get(&SIGNER_REGISTRY_ADDRESS);
        assert!(
            signer_registry.is_some(),
            "SignerRegistry must be in genesis"
        );
        let signer_registry = signer_registry.unwrap();
        assert!(
            signer_registry.code.is_some(),
            "SignerRegistry must have code"
        );
        assert_eq!(signer_registry.nonce, Some(1));
        assert!(
            signer_registry.storage.is_some(),
            "SignerRegistry must have storage"
        );
        let storage = signer_registry.storage.as_ref().unwrap();
        // governance + signers.length + 3 signer array entries + 3 isSigner mapping entries + threshold
        assert!(
            storage.len() >= 8,
            "SignerRegistry needs at least 8 storage slots (got {})",
            storage.len()
        );

        // Treasury contract
        let treasury = genesis.alloc.get(&TREASURY_ADDRESS);
        assert!(treasury.is_some(), "Treasury must be in genesis");
        let treasury = treasury.unwrap();
        assert!(treasury.code.is_some(), "Treasury must have code");
        assert_eq!(treasury.nonce, Some(1));
        assert!(treasury.storage.is_some(), "Treasury must have storage");
        let storage = treasury.storage.as_ref().unwrap();
        // governance + signerShare + devShare + communityShare + burnShare + devFund + communityFund + signerRegistry
        assert_eq!(storage.len(), 8, "Treasury needs 8 storage slots");
    }

    #[test]
    fn test_safe_contracts_in_genesis() {
        let genesis = create_dev_genesis();

        // Safe Singleton
        let singleton = genesis.alloc.get(&SAFE_SINGLETON_ADDRESS);
        assert!(singleton.is_some(), "Safe Singleton must be in genesis");
        assert!(
            singleton.unwrap().code.is_some(),
            "Safe Singleton must have code"
        );
        assert_eq!(singleton.unwrap().nonce, Some(1));

        // Safe Proxy Factory
        let factory = genesis.alloc.get(&SAFE_PROXY_FACTORY_ADDRESS);
        assert!(factory.is_some(), "Safe Proxy Factory must be in genesis");
        assert!(
            factory.unwrap().code.is_some(),
            "Safe Proxy Factory must have code"
        );

        // Safe Fallback Handler
        let handler = genesis.alloc.get(&SAFE_FALLBACK_HANDLER_ADDRESS);
        assert!(
            handler.is_some(),
            "Safe Fallback Handler must be in genesis"
        );
        assert!(
            handler.unwrap().code.is_some(),
            "Safe Fallback Handler must have code"
        );

        // Safe MultiSend
        let multisend = genesis.alloc.get(&SAFE_MULTISEND_ADDRESS);
        assert!(multisend.is_some(), "Safe MultiSend must be in genesis");
        assert!(
            multisend.unwrap().code.is_some(),
            "Safe MultiSend must have code"
        );
    }

    #[test]
    fn test_governance_safe_is_admin() {
        let genesis = create_dev_genesis();

        // Miner proxy admin should be the governance Safe address
        let proxy = genesis.alloc.get(&MINER_PROXY_ADDRESS).unwrap();
        let storage = proxy.storage.as_ref().unwrap();
        let admin_slot = storage.get(&EIP1967_ADMIN_SLOT).unwrap();
        let mut expected = [0u8; 32];
        expected[12..32].copy_from_slice(GOVERNANCE_SAFE_ADDRESS.as_slice());
        assert_eq!(
            *admin_slot,
            B256::from(expected),
            "Miner proxy admin should be governance Safe"
        );
    }

    // =========================================================================
    // New comprehensive tests
    // =========================================================================

    #[test]
    fn test_governance_contract_storage_values() {
        let genesis = create_dev_genesis();

        // --- ChainConfig storage verification ---
        let chain_config = genesis.alloc.get(&CHAIN_CONFIG_ADDRESS).unwrap();
        let storage = chain_config.storage.as_ref().unwrap();

        // slot 0: governance = GOVERNANCE_SAFE_ADDRESS
        let mut expected_gov = [0u8; 32];
        expected_gov[12..32].copy_from_slice(GOVERNANCE_SAFE_ADDRESS.as_slice());
        assert_eq!(
            *storage.get(&B256::ZERO).unwrap(),
            B256::from(expected_gov),
            "ChainConfig slot 0 should be governance Safe address"
        );

        // slot 1: gasLimit = 300_000_000 (Phase 2: 300M for dev genesis)
        let slot1 = b256!("0000000000000000000000000000000000000000000000000000000000000001");
        assert_eq!(
            *storage.get(&slot1).unwrap(),
            B256::from(U256::from(300_000_000u64).to_be_bytes()),
            "ChainConfig slot 1 should be gas limit 300M"
        );

        // slot 2: blockTime = 1 (Phase 2: 1-second blocks for dev genesis)
        let slot2 = b256!("0000000000000000000000000000000000000000000000000000000000000002");
        assert_eq!(
            *storage.get(&slot2).unwrap(),
            B256::from(U256::from(1u64).to_be_bytes()),
            "ChainConfig slot 2 should be block time 1s"
        );

        // --- SignerRegistry storage verification ---
        let signer_registry = genesis.alloc.get(&SIGNER_REGISTRY_ADDRESS).unwrap();
        let storage = signer_registry.storage.as_ref().unwrap();

        // slot 0: governance
        assert_eq!(
            *storage.get(&B256::ZERO).unwrap(),
            B256::from(expected_gov),
            "SignerRegistry slot 0 should be governance Safe address"
        );

        // slot 1: signers.length = 3
        let slot1 = b256!("0000000000000000000000000000000000000000000000000000000000000001");
        assert_eq!(
            *storage.get(&slot1).unwrap(),
            B256::from(U256::from(3u64).to_be_bytes()),
            "SignerRegistry slot 1 (signers.length) should be 3"
        );

        // --- Treasury storage verification ---
        let treasury = genesis.alloc.get(&TREASURY_ADDRESS).unwrap();
        let storage = treasury.storage.as_ref().unwrap();

        // slot 0: governance
        assert_eq!(
            *storage.get(&B256::ZERO).unwrap(),
            B256::from(expected_gov),
            "Treasury slot 0 should be governance Safe address"
        );

        // slot 1: signerShare = 4000
        assert_eq!(
            *storage.get(&slot1).unwrap(),
            B256::from(U256::from(4000u64).to_be_bytes()),
            "Treasury slot 1 (signerShare) should be 4000"
        );
    }

    #[test]
    fn test_all_contract_bytecodes_non_empty() {
        let genesis = create_dev_genesis();

        for (address, account) in &genesis.alloc {
            if let Some(code) = &account.code {
                assert!(
                    !code.is_empty(),
                    "Contract at {} has empty bytecode",
                    address
                );
            }
        }
    }

    #[test]
    fn test_genesis_with_custom_gas_limit() {
        let mut config = GenesisConfig::dev();
        config.gas_limit = 100_000_000;
        let genesis = create_genesis(config);
        assert_eq!(genesis.gas_limit, 100_000_000);
    }

    #[test]
    fn test_all_dev_accounts_prefunded() {
        let genesis = create_dev_genesis();
        let accounts = dev_accounts();
        let expected_balance = default_prefund_balance();

        for account in &accounts {
            let alloc = genesis.alloc.get(account);
            assert!(
                alloc.is_some(),
                "Dev account {} should be in genesis",
                account
            );
            assert_eq!(
                alloc.unwrap().balance,
                expected_balance,
                "Dev account {} should have 10,000 ETH",
                account
            );
        }
    }

    #[test]
    fn test_production_accounts_tiered_funding() {
        let config = GenesisConfig::production();
        let genesis = create_genesis(config);

        let eth = U256::from(10u64).pow(U256::from(18u64));

        // First 5 accounts are signers with 10,000 ETH each
        let signer_balance = U256::from(10_000u64) * eth;
        for account in dev_accounts().iter().take(5) {
            let alloc = genesis.alloc.get(account).unwrap();
            assert_eq!(
                alloc.balance, signer_balance,
                "Signer {} should have 10,000 ETH",
                account
            );
        }

        // Account 5 (treasury): 2,500,000 ETH
        let treasury_balance = U256::from(2_500_000u64) * eth;
        assert_eq!(
            genesis.alloc.get(&dev_accounts()[5]).unwrap().balance,
            treasury_balance
        );

        // Account 6 (operations): 500,000 ETH
        let operations_balance = U256::from(500_000u64) * eth;
        assert_eq!(
            genesis.alloc.get(&dev_accounts()[6]).unwrap().balance,
            operations_balance
        );

        // Account 7 (community): 100,000 ETH
        let community_balance = U256::from(100_000u64) * eth;
        assert_eq!(
            genesis.alloc.get(&dev_accounts()[7]).unwrap().balance,
            community_balance
        );
    }

    #[test]
    fn test_dev_genesis_alloc_breakdown() {
        let genesis = create_dev_genesis();

        // Count categories
        let dev_count = dev_accounts().len(); // 20
        let system_contracts = 4; // EIP-4788, 2935, 7002, 7251
        let infra_contracts = 5; // EntryPoint, WETH9, Multicall3, CREATE2, SimpleAccountFactory
        let miner_proxy = 1;
        let governance = 4; // ChainConfig, SignerRegistry, Treasury, Timelock
        let safe = 4; // Singleton, ProxyFactory, FallbackHandler, MultiSend

        let expected =
            dev_count + system_contracts + infra_contracts + miner_proxy + governance + safe;
        assert_eq!(expected, 38);
        assert_eq!(genesis.alloc.len(), expected);
    }

    #[test]
    fn test_all_contract_addresses_unique() {
        use std::collections::HashSet;
        let genesis = create_dev_genesis();

        let addresses: Vec<Address> = genesis.alloc.keys().copied().collect();
        let unique: HashSet<_> = addresses.iter().collect();

        assert_eq!(
            addresses.len(),
            unique.len(),
            "All genesis alloc addresses should be unique"
        );
    }

    #[test]
    fn test_genesis_coinbase_is_miner_proxy() {
        let genesis = create_dev_genesis();
        assert_eq!(genesis.coinbase, MINER_PROXY_ADDRESS);

        let prod_genesis = create_genesis(GenesisConfig::production());
        assert_eq!(prod_genesis.coinbase, MINER_PROXY_ADDRESS);
    }

    #[test]
    fn test_genesis_with_zero_signers() {
        let config = GenesisConfig::default().with_signers(vec![]);
        let genesis = create_genesis(config);

        // Extra data with 0 signers: 32 vanity + 0 * 20 + 65 seal = 97 bytes
        assert_eq!(genesis.extra_data.len(), 97);
    }

    #[test]
    fn test_governance_safe_address_constant() {
        // Verify the governance Safe address matches expected value
        assert_eq!(
            format!("{:?}", GOVERNANCE_SAFE_ADDRESS),
            "0x000000000000000000000000000000006f5afe00"
        );
    }
}

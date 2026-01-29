//! Genesis Configuration for POA Chain
//!
//! This module provides utilities for creating genesis configurations
//! that are compatible with Ethereum tooling while supporting POA consensus.

use alloy_genesis::{Genesis, GenesisAccount};
use alloy_primitives::{address, Address, U256};
use std::collections::BTreeMap;

/// Default balance for prefunded accounts (10,000 ETH in wei)
/// 10,000 ETH = 10,000 * 10^18 wei = 10,000,000,000,000,000,000,000 wei
pub fn default_prefund_balance() -> U256 {
    U256::from(10_000u64) * U256::from(10u64).pow(U256::from(18u64))
}

/// Standard dev mnemonic accounts (derived from "test test test test test test test test test test test junk")
pub fn dev_accounts() -> Vec<Address> {
    vec![
        address!("f39Fd6e51aad88F6F4ce6aB8827279cffFb92266"),
        address!("70997970C51812dc3A010C7d01b50e0d17dc79C8"),
        address!("3C44CdDdB6a900fa2b585dd299e03d12FA4293BC"),
        address!("90F79bf6EB2c4f870365E785982E1f101E93b906"),
        address!("15d34AAf54267DB7D7c367839AAf71A00a2C6A65"),
        address!("9965507D1a55bcC2695C58ba16FB37d819B0A4dc"),
        address!("976EA74026E726554dB657fA54763abd0C3a0aa9"),
        address!("14dC79964da2C08b23698B3D3cc7Ca32193d9955"),
        address!("23618e81E3f5cdF7f54C3d65f7FBc0aBf5B21E8f"),
        address!("a0Ee7A142d267C1f36714E4a8F75612F20a79720"),
        address!("Bcd4042DE499D14e55001CcbB24a551F3b954096"),
        address!("71bE63f3384f5fb98995898A86B02Fb2426c5788"),
        address!("FABB0ac9d68B0B445fB7357272Ff202C5651694a"),
        address!("1CBd3b2770909D4e10f157cABC84C7264073C9Ec"),
        address!("dF3e18d64BC6A983f673Ab319CCaE4f1a57C7097"),
        address!("cd3B766CCDd6AE721141F452C550Ca635964ce71"),
        address!("2546BcD3c84621e976D8185a91A922aE77ECEc30"),
        address!("bDA5747bFD65F08deb54cb465eB87D40e51B197E"),
        address!("dD2FD4581271e230360230F9337D5c0430Bf44C0"),
        address!("8626f6940E2eb28930eFb4CeF49B2d1F2C9C1199"),
    ]
}

/// Default dev signers (first 3 accounts from dev mnemonic)
pub fn dev_signers() -> Vec<Address> {
    dev_accounts().into_iter().take(3).collect()
}

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
            chain_id: 31337, // Common local dev chain ID
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
        let accounts = dev_accounts();
        let signers = dev_signers();

        let balance = default_prefund_balance();
        let mut prefunded = BTreeMap::new();
        for account in accounts {
            prefunded.insert(account, balance);
        }

        Self {
            chain_id: 31337,
            gas_limit: 30_000_000,
            prefunded_accounts: prefunded,
            signers,
            block_period: 2, // Fast blocks for dev
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
    let mut alloc = BTreeMap::new();
    for (address, balance) in config.prefunded_accounts {
        alloc.insert(
            address,
            GenesisAccount { balance, nonce: None, code: None, storage: None, private_key: None },
        );
    }

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
        coinbase: Default::default(),
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

    #[test]
    fn test_dev_genesis_creation() {
        let genesis = create_dev_genesis();

        // Verify chain ID
        assert_eq!(genesis.config.chain_id, 31337);

        // Verify accounts are prefunded
        assert!(!genesis.alloc.is_empty());
        assert_eq!(genesis.alloc.len(), 20); // 20 dev accounts

        // Verify extra data contains signers
        assert!(genesis.extra_data.len() >= 32 + 65); // At least vanity + seal
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

        assert_eq!(genesis.config.chain_id, 12345);
        assert!(genesis.alloc.contains_key(&funded));
        assert_eq!(genesis.alloc.get(&funded).unwrap().balance, U256::from(1000));
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
}

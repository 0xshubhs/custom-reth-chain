//! On-chain Governance Contract Readers
//!
//! Reads dynamic chain parameters from the ChainConfig and SignerRegistry
//! contracts deployed in genesis. This is THE critical wiring that makes
//! the chain governable without downtime.
//!
//! Architecture:
//!   Governance Safe → ChainConfig.setGasLimit(300_000_000)
//!   Governance Safe → SignerRegistry.addSigner(addr)
//!   ↓
//!   PoaPayloadBuilder reads ChainConfig.gasLimit at each block
//!   PoaConsensus reads SignerRegistry.getSigners at epoch blocks
//!   ↓
//!   No restart, no recompile, no downtime
//!
//! Storage layout must match genesis.rs pre-population and the Solidity contracts.

pub mod helpers;
pub mod providers;
pub mod readers;
pub mod selectors;
pub mod slots;

// Re-export the StorageReader trait and key types at module level
pub use helpers::{
    decode_address, decode_bool, decode_u64, dynamic_array_base_slot, encode_address, encode_u64,
    mapping_address_bool_slot,
};
pub use providers::{GenesisStorageReader, StateProviderStorageReader};
pub use readers::{
    is_signer_on_chain, is_timelock_paused, read_block_time, read_chain_config, read_gas_limit,
    read_signer_list, read_timelock_delay, read_timelock_proposer, DynamicChainConfig,
    DynamicSignerList,
};
pub use selectors::function_selector;
pub use slots::{chain_config_slots, signer_registry_slots, timelock_slots};

use alloy_primitives::{Address, B256, U256};

/// Trait for reading contract storage slots.
///
/// In production: implemented by the state provider (MDBX database)
/// In tests: implemented by GenesisStorageReader (reads from genesis alloc)
pub trait StorageReader {
    /// Read a storage slot value from a contract address.
    /// Returns None if the contract or slot doesn't exist.
    fn read_storage(&self, address: Address, slot: U256) -> Option<B256>;
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::genesis::{
        create_dev_genesis, create_genesis, dev_accounts, dev_signers, GenesisConfig,
        CHAIN_CONFIG_ADDRESS, GOVERNANCE_SAFE_ADDRESS, SIGNER_REGISTRY_ADDRESS, TIMELOCK_ADDRESS,
    };
    use alloy_primitives::Keccak256;
    use std::collections::BTreeMap;

    // =========================================================================
    // Helper: In-memory storage reader for unit tests
    // =========================================================================

    struct MockStorage {
        storage: BTreeMap<(Address, U256), B256>,
    }

    impl MockStorage {
        fn new() -> Self {
            Self {
                storage: BTreeMap::new(),
            }
        }

        fn set(&mut self, address: Address, slot: U256, value: B256) {
            self.storage.insert((address, slot), value);
        }
    }

    impl StorageReader for MockStorage {
        fn read_storage(&self, address: Address, slot: U256) -> Option<B256> {
            self.storage.get(&(address, slot)).copied()
        }
    }

    // =========================================================================
    // Storage slot constant tests
    // =========================================================================

    #[test]
    fn test_chain_config_slot_values() {
        assert_eq!(chain_config_slots::GOVERNANCE, U256::ZERO);
        assert_eq!(chain_config_slots::GAS_LIMIT, U256::from(1));
        assert_eq!(chain_config_slots::BLOCK_TIME, U256::from(2));
        assert_eq!(chain_config_slots::MAX_CONTRACT_SIZE, U256::from(3));
        assert_eq!(chain_config_slots::CALLDATA_GAS_PER_BYTE, U256::from(4));
        assert_eq!(chain_config_slots::MAX_TX_GAS, U256::from(5));
        assert_eq!(chain_config_slots::EAGER_MINING, U256::from(6));
    }

    #[test]
    fn test_signer_registry_slot_values() {
        assert_eq!(signer_registry_slots::GOVERNANCE, U256::ZERO);
        assert_eq!(signer_registry_slots::SIGNERS_LENGTH, U256::from(1));
        assert_eq!(signer_registry_slots::IS_SIGNER_MAPPING, U256::from(2));
        assert_eq!(signer_registry_slots::SIGNER_THRESHOLD, U256::from(3));
    }

    // =========================================================================
    // ABI function selector tests
    // =========================================================================

    #[test]
    fn test_function_selector_computation() {
        let gas_limit_sel = function_selector("gasLimit()");
        assert_eq!(gas_limit_sel.len(), 4);
        assert_eq!(gas_limit_sel, function_selector("gasLimit()"));
    }

    /// Verify every pre-computed const selector matches the runtime keccak256 computation.
    /// This guards against copy-paste errors in the hardcoded byte arrays.
    #[test]
    fn test_const_selectors_match_runtime_keccak() {
        assert_eq!(selectors::gas_limit(),            function_selector("gasLimit()"));
        assert_eq!(selectors::block_time(),           function_selector("blockTime()"));
        assert_eq!(selectors::max_contract_size(),    function_selector("maxContractSize()"));
        assert_eq!(selectors::calldata_gas_per_byte(),function_selector("calldataGasPerByte()"));
        assert_eq!(selectors::max_tx_gas(),           function_selector("maxTxGas()"));
        assert_eq!(selectors::eager_mining(),         function_selector("eagerMining()"));
        assert_eq!(selectors::governance(),           function_selector("governance()"));
        assert_eq!(selectors::get_signers(),          function_selector("getSigners()"));
        assert_eq!(selectors::signer_count(),         function_selector("signerCount()"));
        assert_eq!(selectors::signer_threshold(),     function_selector("signerThreshold()"));
        assert_eq!(selectors::is_signer(),            function_selector("isSigner(address)"));
    }

    #[test]
    fn test_different_functions_different_selectors() {
        let gas_limit = selectors::gas_limit();
        let block_time = selectors::block_time();
        let max_contract_size = selectors::max_contract_size();
        let governance = selectors::governance();
        let get_signers = selectors::get_signers();
        let signer_count = selectors::signer_count();

        let all = [
            gas_limit,
            block_time,
            max_contract_size,
            governance,
            get_signers,
            signer_count,
        ];
        for i in 0..all.len() {
            for j in (i + 1)..all.len() {
                assert_ne!(
                    all[i], all[j],
                    "Selectors at index {} and {} should differ",
                    i, j
                );
            }
        }
    }

    #[test]
    fn test_selector_length_always_4_bytes() {
        assert_eq!(selectors::gas_limit().len(), 4);
        assert_eq!(selectors::block_time().len(), 4);
        assert_eq!(selectors::max_contract_size().len(), 4);
        assert_eq!(selectors::calldata_gas_per_byte().len(), 4);
        assert_eq!(selectors::max_tx_gas().len(), 4);
        assert_eq!(selectors::eager_mining().len(), 4);
        assert_eq!(selectors::governance().len(), 4);
        assert_eq!(selectors::get_signers().len(), 4);
        assert_eq!(selectors::signer_count().len(), 4);
        assert_eq!(selectors::signer_threshold().len(), 4);
        assert_eq!(selectors::is_signer().len(), 4);
    }

    // =========================================================================
    // Encoding/Decoding helpers
    // =========================================================================

    #[test]
    fn test_encode_decode_u64_roundtrip() {
        let values = [
            0u64,
            1,
            30_000_000,
            60_000_000,
            100_000_000,
            1_000_000_000,
            u64::MAX,
        ];
        for val in values {
            let encoded = encode_u64(val);
            let decoded = decode_u64(encoded);
            assert_eq!(decoded, val, "Roundtrip failed for {}", val);
        }
    }

    #[test]
    fn test_encode_decode_address_roundtrip() {
        let addresses = [
            Address::ZERO,
            GOVERNANCE_SAFE_ADDRESS,
            CHAIN_CONFIG_ADDRESS,
            SIGNER_REGISTRY_ADDRESS,
            dev_accounts()[0],
            dev_accounts()[19],
        ];
        for addr in addresses {
            let encoded = encode_address(addr);
            let decoded = decode_address(encoded);
            assert_eq!(decoded, addr, "Roundtrip failed for {}", addr);
        }
    }

    #[test]
    fn test_decode_bool_true_and_false() {
        assert!(!decode_bool(B256::ZERO));
        assert!(decode_bool(B256::from(U256::from(1).to_be_bytes())));
        let mut b = [0u8; 32];
        b[31] = 0xFF;
        assert!(decode_bool(B256::from(b)));
    }

    #[test]
    fn test_encode_address_is_left_padded() {
        let addr = dev_accounts()[0];
        let encoded = encode_address(addr);
        assert_eq!(&encoded[..12], &[0u8; 12]);
        assert_eq!(&encoded[12..32], addr.as_slice());
    }

    // =========================================================================
    // Dynamic array base slot computation
    // =========================================================================

    #[test]
    fn test_dynamic_array_base_slot_matches_genesis() {
        let our_base = dynamic_array_base_slot(U256::from(1));

        let mut hasher = Keccak256::new();
        hasher.update(B256::from(U256::from(1u64).to_be_bytes()).as_slice());
        let genesis_base = U256::from_be_bytes(hasher.finalize().0);

        assert_eq!(
            our_base, genesis_base,
            "Array base slot must match genesis.rs computation"
        );
    }

    #[test]
    fn test_dynamic_array_different_slots_different_bases() {
        let base1 = dynamic_array_base_slot(U256::from(1));
        let base2 = dynamic_array_base_slot(U256::from(2));
        assert_ne!(base1, base2);
    }

    // =========================================================================
    // Mapping slot computation
    // =========================================================================

    #[test]
    fn test_mapping_slot_matches_genesis() {
        let signer = dev_signers()[0];
        let our_slot = mapping_address_bool_slot(signer, U256::from(2));

        let mut hasher = Keccak256::new();
        let mut key_padded = [0u8; 32];
        key_padded[12..32].copy_from_slice(signer.as_slice());
        hasher.update(key_padded);
        hasher.update(B256::from(U256::from(2u64).to_be_bytes()).as_slice());
        let genesis_slot = hasher.finalize();

        assert_eq!(
            our_slot, genesis_slot,
            "Mapping slot must match genesis.rs computation"
        );
    }

    #[test]
    fn test_mapping_slot_different_addresses_different_slots() {
        let slot1 = mapping_address_bool_slot(dev_accounts()[0], U256::from(2));
        let slot2 = mapping_address_bool_slot(dev_accounts()[1], U256::from(2));
        assert_ne!(slot1, slot2);
    }

    #[test]
    fn test_mapping_slot_different_base_different_slots() {
        let slot1 = mapping_address_bool_slot(dev_accounts()[0], U256::from(2));
        let slot2 = mapping_address_bool_slot(dev_accounts()[0], U256::from(3));
        assert_ne!(slot1, slot2);
    }

    // =========================================================================
    // MockStorage reader tests
    // =========================================================================

    #[test]
    fn test_mock_storage_read_write() {
        let mut mock = MockStorage::new();
        let value = encode_u64(42);
        mock.set(CHAIN_CONFIG_ADDRESS, U256::from(1), value);
        assert_eq!(
            mock.read_storage(CHAIN_CONFIG_ADDRESS, U256::from(1)),
            Some(value)
        );
    }

    #[test]
    fn test_mock_storage_missing_returns_none() {
        let mock = MockStorage::new();
        assert_eq!(
            mock.read_storage(CHAIN_CONFIG_ADDRESS, U256::from(999)),
            None
        );
    }

    #[test]
    fn test_read_chain_config_from_mock() {
        let mut mock = MockStorage::new();
        let gov = GOVERNANCE_SAFE_ADDRESS;

        mock.set(
            CHAIN_CONFIG_ADDRESS,
            chain_config_slots::GOVERNANCE,
            encode_address(gov),
        );
        mock.set(
            CHAIN_CONFIG_ADDRESS,
            chain_config_slots::GAS_LIMIT,
            encode_u64(300_000_000),
        );
        mock.set(
            CHAIN_CONFIG_ADDRESS,
            chain_config_slots::BLOCK_TIME,
            encode_u64(1),
        );
        mock.set(
            CHAIN_CONFIG_ADDRESS,
            chain_config_slots::MAX_CONTRACT_SIZE,
            encode_u64(524_288),
        );
        mock.set(
            CHAIN_CONFIG_ADDRESS,
            chain_config_slots::CALLDATA_GAS_PER_BYTE,
            encode_u64(4),
        );
        mock.set(
            CHAIN_CONFIG_ADDRESS,
            chain_config_slots::MAX_TX_GAS,
            encode_u64(300_000_000),
        );

        let config = read_chain_config(&mock).unwrap();
        assert_eq!(config.governance, gov);
        assert_eq!(config.gas_limit, 300_000_000);
        assert_eq!(config.block_time, 1);
        assert_eq!(config.max_contract_size, 524_288);
        assert_eq!(config.calldata_gas_per_byte, 4);
        assert_eq!(config.max_tx_gas, 300_000_000);
        assert!(!config.eager_mining);
    }

    #[test]
    fn test_read_chain_config_missing_returns_none() {
        let mock = MockStorage::new();
        assert!(read_chain_config(&mock).is_none());
    }

    #[test]
    fn test_read_gas_limit_from_mock() {
        let mut mock = MockStorage::new();
        mock.set(
            CHAIN_CONFIG_ADDRESS,
            chain_config_slots::GAS_LIMIT,
            encode_u64(1_000_000_000),
        );
        assert_eq!(read_gas_limit(&mock), Some(1_000_000_000));
    }

    #[test]
    fn test_read_block_time_from_mock() {
        let mut mock = MockStorage::new();
        mock.set(
            CHAIN_CONFIG_ADDRESS,
            chain_config_slots::BLOCK_TIME,
            encode_u64(12),
        );
        assert_eq!(read_block_time(&mock), Some(12));
    }

    #[test]
    fn test_read_signer_list_from_mock() {
        let mut mock = MockStorage::new();
        let gov = GOVERNANCE_SAFE_ADDRESS;
        let signers = dev_signers();

        mock.set(
            SIGNER_REGISTRY_ADDRESS,
            signer_registry_slots::GOVERNANCE,
            encode_address(gov),
        );
        mock.set(
            SIGNER_REGISTRY_ADDRESS,
            signer_registry_slots::SIGNERS_LENGTH,
            encode_u64(3),
        );
        mock.set(
            SIGNER_REGISTRY_ADDRESS,
            signer_registry_slots::SIGNER_THRESHOLD,
            encode_u64(2),
        );

        let base = dynamic_array_base_slot(signer_registry_slots::SIGNERS_LENGTH);
        for (i, signer) in signers.iter().enumerate() {
            mock.set(
                SIGNER_REGISTRY_ADDRESS,
                base + U256::from(i),
                encode_address(*signer),
            );
        }

        let list = read_signer_list(&mock).unwrap();
        assert_eq!(list.governance, gov);
        assert_eq!(list.signers.len(), 3);
        assert_eq!(list.signers, signers);
        assert_eq!(list.threshold, 2);
    }

    #[test]
    fn test_read_signer_list_empty() {
        let mut mock = MockStorage::new();
        mock.set(
            SIGNER_REGISTRY_ADDRESS,
            signer_registry_slots::GOVERNANCE,
            encode_address(GOVERNANCE_SAFE_ADDRESS),
        );
        mock.set(
            SIGNER_REGISTRY_ADDRESS,
            signer_registry_slots::SIGNERS_LENGTH,
            encode_u64(0),
        );
        mock.set(
            SIGNER_REGISTRY_ADDRESS,
            signer_registry_slots::SIGNER_THRESHOLD,
            encode_u64(0),
        );

        let list = read_signer_list(&mock).unwrap();
        assert!(list.signers.is_empty());
    }

    #[test]
    fn test_is_signer_on_chain_mock() {
        let mut mock = MockStorage::new();
        let signer = dev_signers()[0];

        let slot_hash = mapping_address_bool_slot(signer, signer_registry_slots::IS_SIGNER_MAPPING);
        let slot = U256::from_be_bytes(slot_hash.0);
        mock.set(SIGNER_REGISTRY_ADDRESS, slot, encode_u64(1));

        assert!(is_signer_on_chain(&mock, signer));
        assert!(!is_signer_on_chain(&mock, dev_accounts()[5]));
    }

    // =========================================================================
    // GenesisStorageReader tests
    // =========================================================================

    #[test]
    fn test_genesis_reader_reads_chain_config() {
        let genesis = create_dev_genesis();
        let reader = GenesisStorageReader::from_genesis(&genesis);

        let config = read_chain_config(&reader).unwrap();

        assert_eq!(config.governance, GOVERNANCE_SAFE_ADDRESS);
        assert_eq!(config.gas_limit, 300_000_000); // Phase 2: 300M dev gas limit
        assert_eq!(config.block_time, 1); // Phase 2: 1-second blocks
        assert_eq!(config.max_contract_size, 24_576);
        assert_eq!(config.calldata_gas_per_byte, 16);
        assert_eq!(config.max_tx_gas, 300_000_000); // matches gas_limit
        assert!(!config.eager_mining);
    }

    #[test]
    fn test_genesis_reader_reads_production_chain_config() {
        let genesis = create_genesis(GenesisConfig::production());
        let reader = GenesisStorageReader::from_genesis(&genesis);

        let config = read_chain_config(&reader).unwrap();

        assert_eq!(config.governance, GOVERNANCE_SAFE_ADDRESS);
        assert_eq!(config.gas_limit, 1_000_000_000); // Phase 2: 1B production gas limit
        assert_eq!(config.block_time, 2); // Production: 2-second blocks
        assert_eq!(config.max_contract_size, 24_576);
        assert_eq!(config.calldata_gas_per_byte, 16);
        assert_eq!(config.max_tx_gas, 1_000_000_000); // matches gas_limit
    }

    #[test]
    fn test_genesis_reader_reads_signer_list() {
        let genesis = create_dev_genesis();
        let reader = GenesisStorageReader::from_genesis(&genesis);

        let list = read_signer_list(&reader).unwrap();

        assert_eq!(list.governance, GOVERNANCE_SAFE_ADDRESS);
        assert_eq!(list.signers.len(), 3);
        assert_eq!(list.signers, dev_signers());
        assert_eq!(list.threshold, 2);
    }

    #[test]
    fn test_genesis_reader_reads_production_signer_list() {
        let genesis = create_genesis(GenesisConfig::production());
        let reader = GenesisStorageReader::from_genesis(&genesis);

        let list = read_signer_list(&reader).unwrap();

        assert_eq!(list.governance, GOVERNANCE_SAFE_ADDRESS);
        assert_eq!(list.signers.len(), 5);
        assert_eq!(
            list.signers,
            dev_accounts().into_iter().take(5).collect::<Vec<_>>()
        );
        assert_eq!(list.threshold, 3);
    }

    #[test]
    fn test_genesis_reader_gas_limit_shortcut() {
        let genesis = create_dev_genesis();
        let reader = GenesisStorageReader::from_genesis(&genesis);
        assert_eq!(read_gas_limit(&reader), Some(300_000_000)); // Phase 2: 300M default
    }

    #[test]
    fn test_genesis_reader_block_time_shortcut() {
        let genesis = create_dev_genesis();
        let reader = GenesisStorageReader::from_genesis(&genesis);
        assert_eq!(read_block_time(&reader), Some(1));
    }

    #[test]
    fn test_genesis_reader_is_signer_check() {
        let genesis = create_dev_genesis();
        let reader = GenesisStorageReader::from_genesis(&genesis);

        for signer in dev_signers() {
            assert!(
                is_signer_on_chain(&reader, signer),
                "Signer {} should be registered on-chain",
                signer
            );
        }

        for account in dev_accounts().into_iter().skip(3) {
            assert!(
                !is_signer_on_chain(&reader, account),
                "Account {} should NOT be registered as signer",
                account
            );
        }
    }

    #[test]
    fn test_genesis_reader_production_is_signer_check() {
        let genesis = create_genesis(GenesisConfig::production());
        let reader = GenesisStorageReader::from_genesis(&genesis);

        for account in dev_accounts().into_iter().take(5) {
            assert!(
                is_signer_on_chain(&reader, account),
                "Account {} should be a production signer",
                account
            );
        }

        for account in dev_accounts().into_iter().skip(5) {
            assert!(
                !is_signer_on_chain(&reader, account),
                "Account {} should NOT be a production signer",
                account
            );
        }
    }

    // =========================================================================
    // Custom genesis configurations
    // =========================================================================

    #[test]
    fn test_genesis_reader_custom_gas_limit() {
        let mut config = GenesisConfig::dev();
        config.gas_limit = 100_000_000;
        let genesis = create_genesis(config);
        let reader = GenesisStorageReader::from_genesis(&genesis);

        let chain_config = read_chain_config(&reader).unwrap();
        assert_eq!(chain_config.gas_limit, 100_000_000);
        assert_eq!(chain_config.max_tx_gas, 100_000_000);
    }

    #[test]
    fn test_genesis_reader_custom_block_time() {
        let mut config = GenesisConfig::dev();
        config.block_period = 1;
        let genesis = create_genesis(config);
        let reader = GenesisStorageReader::from_genesis(&genesis);

        let chain_config = read_chain_config(&reader).unwrap();
        assert_eq!(chain_config.block_time, 1);
    }

    #[test]
    fn test_genesis_reader_custom_signers() {
        let custom_signers: Vec<Address> = (1..=5u64)
            .map(|i| {
                let mut bytes = [0u8; 20];
                bytes[19] = i as u8;
                Address::from(bytes)
            })
            .collect();

        let config = GenesisConfig::default().with_signers(custom_signers.clone());
        let genesis = create_genesis(config);
        let reader = GenesisStorageReader::from_genesis(&genesis);

        let list = read_signer_list(&reader).unwrap();
        assert_eq!(list.signers.len(), 5);
        assert_eq!(list.signers, custom_signers);
        assert_eq!(list.threshold, 3);

        for signer in &custom_signers {
            assert!(is_signer_on_chain(&reader, *signer));
        }
    }

    #[test]
    fn test_genesis_reader_single_signer() {
        let signer = dev_accounts()[0];
        let config = GenesisConfig::default().with_signers(vec![signer]);
        let genesis = create_genesis(config);
        let reader = GenesisStorageReader::from_genesis(&genesis);

        let list = read_signer_list(&reader).unwrap();
        assert_eq!(list.signers.len(), 1);
        assert_eq!(list.signers[0], signer);
        assert_eq!(list.threshold, 1);
        assert!(is_signer_on_chain(&reader, signer));
    }

    // =========================================================================
    // Consistency tests
    // =========================================================================

    #[test]
    fn test_chain_config_reader_matches_genesis_config() {
        let configs = vec![
            GenesisConfig::dev(),
            GenesisConfig::production(),
            GenesisConfig::default()
                .with_chain_id(42)
                .with_block_period(5)
                .with_signers(dev_signers()),
        ];

        for config in configs {
            let gas_limit = config.gas_limit;
            let block_time = config.block_period;
            let genesis = create_genesis(config);
            let reader = GenesisStorageReader::from_genesis(&genesis);

            let chain_config = read_chain_config(&reader).unwrap();
            assert_eq!(chain_config.gas_limit, gas_limit);
            assert_eq!(chain_config.block_time, block_time);
        }
    }

    #[test]
    fn test_signer_list_reader_matches_genesis_config() {
        let configs = vec![
            (GenesisConfig::dev(), 3usize),
            (GenesisConfig::production(), 5usize),
        ];

        for (config, expected_count) in configs {
            let signers = config.signers.clone();
            let genesis = create_genesis(config);
            let reader = GenesisStorageReader::from_genesis(&genesis);

            let list = read_signer_list(&reader).unwrap();
            assert_eq!(list.signers.len(), expected_count);
            assert_eq!(list.signers, signers);
        }
    }

    // =========================================================================
    // Edge cases
    // =========================================================================

    #[test]
    fn test_decode_u64_zero() {
        assert_eq!(decode_u64(B256::ZERO), 0);
    }

    #[test]
    fn test_decode_address_zero() {
        assert_eq!(decode_address(B256::ZERO), Address::ZERO);
    }

    #[test]
    fn test_dynamic_array_slot_0() {
        let base = dynamic_array_base_slot(U256::ZERO);
        assert_ne!(base, U256::ZERO);
    }

    #[test]
    fn test_genesis_reader_nonexistent_address() {
        let genesis = create_dev_genesis();
        let reader = GenesisStorageReader::from_genesis(&genesis);

        let fake_addr: Address = "0x0000000000000000000000000000000000000099"
            .parse()
            .unwrap();
        assert_eq!(reader.read_storage(fake_addr, U256::ZERO), None);
    }

    #[test]
    fn test_genesis_reader_nonexistent_slot() {
        let genesis = create_dev_genesis();
        let reader = GenesisStorageReader::from_genesis(&genesis);
        assert_eq!(
            reader.read_storage(CHAIN_CONFIG_ADDRESS, U256::from(999)),
            None
        );
    }

    // =========================================================================
    // DynamicChainConfig struct tests
    // =========================================================================

    #[test]
    fn test_dynamic_chain_config_equality() {
        let a = DynamicChainConfig {
            governance: GOVERNANCE_SAFE_ADDRESS,
            gas_limit: 30_000_000,
            block_time: 2,
            max_contract_size: 24_576,
            calldata_gas_per_byte: 16,
            max_tx_gas: 30_000_000,
            eager_mining: false,
        };
        let b = a.clone();
        assert_eq!(a, b);
    }

    #[test]
    fn test_dynamic_chain_config_debug() {
        let config = DynamicChainConfig {
            governance: Address::ZERO,
            gas_limit: 0,
            block_time: 0,
            max_contract_size: 0,
            calldata_gas_per_byte: 0,
            max_tx_gas: 0,
            eager_mining: false,
        };
        let debug = format!("{:?}", config);
        assert!(debug.contains("DynamicChainConfig"));
    }

    #[test]
    fn test_dynamic_signer_list_equality() {
        let a = DynamicSignerList {
            governance: GOVERNANCE_SAFE_ADDRESS,
            signers: dev_signers(),
            threshold: 2,
        };
        let b = a.clone();
        assert_eq!(a, b);
    }

    // =========================================================================
    // Integration: simulate governance parameter changes
    // =========================================================================

    #[test]
    fn test_simulate_gas_limit_change() {
        let genesis = create_dev_genesis();
        let reader = GenesisStorageReader::from_genesis(&genesis);
        assert_eq!(read_gas_limit(&reader), Some(300_000_000)); // Phase 2: 300M default

        let mut mock = MockStorage::new();
        let chain_config_account = genesis.alloc.get(&CHAIN_CONFIG_ADDRESS).unwrap();
        if let Some(storage) = &chain_config_account.storage {
            for (key, value) in storage {
                let slot = U256::from_be_bytes(key.0);
                mock.set(CHAIN_CONFIG_ADDRESS, slot, *value);
            }
        }
        mock.set(
            CHAIN_CONFIG_ADDRESS,
            chain_config_slots::GAS_LIMIT,
            encode_u64(1_000_000_000),
        );

        let config = read_chain_config(&mock).unwrap();
        assert_eq!(config.gas_limit, 1_000_000_000);
        assert_eq!(config.block_time, 1); // Phase 2: 1s default
        assert_eq!(config.governance, GOVERNANCE_SAFE_ADDRESS);
    }

    #[test]
    fn test_simulate_signer_addition() {
        let genesis = create_dev_genesis();
        let reader = GenesisStorageReader::from_genesis(&genesis);

        let list = read_signer_list(&reader).unwrap();
        assert_eq!(list.signers.len(), 3);

        let new_signer = dev_accounts()[3];
        let mut mock = MockStorage::new();

        let sr_account = genesis.alloc.get(&SIGNER_REGISTRY_ADDRESS).unwrap();
        if let Some(storage) = &sr_account.storage {
            for (key, value) in storage {
                let slot = U256::from_be_bytes(key.0);
                mock.set(SIGNER_REGISTRY_ADDRESS, slot, *value);
            }
        }

        mock.set(
            SIGNER_REGISTRY_ADDRESS,
            signer_registry_slots::SIGNERS_LENGTH,
            encode_u64(4),
        );

        let base = dynamic_array_base_slot(signer_registry_slots::SIGNERS_LENGTH);
        mock.set(
            SIGNER_REGISTRY_ADDRESS,
            base + U256::from(3),
            encode_address(new_signer),
        );

        let is_signer_slot =
            mapping_address_bool_slot(new_signer, signer_registry_slots::IS_SIGNER_MAPPING);
        mock.set(
            SIGNER_REGISTRY_ADDRESS,
            U256::from_be_bytes(is_signer_slot.0),
            encode_u64(1),
        );

        let updated_list = read_signer_list(&mock).unwrap();
        assert_eq!(updated_list.signers.len(), 4);
        assert_eq!(updated_list.signers[3], new_signer);
        assert!(is_signer_on_chain(&mock, new_signer));
    }

    #[test]
    fn test_simulate_block_time_change_to_1_second() {
        let genesis = create_dev_genesis();
        let reader = GenesisStorageReader::from_genesis(&genesis);
        assert_eq!(read_block_time(&reader), Some(1)); // Phase 2: 1s default

        let mut mock = MockStorage::new();
        let chain_config_account = genesis.alloc.get(&CHAIN_CONFIG_ADDRESS).unwrap();
        if let Some(storage) = &chain_config_account.storage {
            for (key, value) in storage {
                let slot = U256::from_be_bytes(key.0);
                mock.set(CHAIN_CONFIG_ADDRESS, slot, *value);
            }
        }
        mock.set(
            CHAIN_CONFIG_ADDRESS,
            chain_config_slots::BLOCK_TIME,
            encode_u64(2),
        );

        assert_eq!(read_block_time(&mock), Some(2));
        assert_eq!(read_gas_limit(&mock), Some(300_000_000)); // Phase 2: 300M default
    }

    // =========================================================================
    // Large signer set test
    // =========================================================================

    #[test]
    fn test_genesis_reader_21_signers() {
        let signers: Vec<Address> = (1..=21u64)
            .map(|i| {
                let mut bytes = [0u8; 20];
                bytes[18] = (i >> 8) as u8;
                bytes[19] = i as u8;
                Address::from(bytes)
            })
            .collect();

        let config = GenesisConfig::default().with_signers(signers.clone());
        let genesis = create_genesis(config);
        let reader = GenesisStorageReader::from_genesis(&genesis);

        let list = read_signer_list(&reader).unwrap();
        assert_eq!(list.signers.len(), 21);
        assert_eq!(list.signers, signers);
        assert_eq!(list.threshold, 11);

        for signer in &signers {
            assert!(is_signer_on_chain(&reader, *signer));
        }
    }

    // =========================================================================
    // Timelock tests
    // =========================================================================

    #[test]
    fn test_read_timelock_delay_from_genesis() {
        let genesis = create_dev_genesis();
        let reader = GenesisStorageReader::from_genesis(&genesis);
        let delay = read_timelock_delay(&reader);
        assert_eq!(
            delay,
            Some(86400),
            "Timelock minDelay should be 86400 (24h)"
        );
    }

    #[test]
    fn test_read_timelock_proposer_from_genesis() {
        let genesis = create_dev_genesis();
        let reader = GenesisStorageReader::from_genesis(&genesis);
        let proposer = read_timelock_proposer(&reader);
        assert_eq!(proposer, Some(GOVERNANCE_SAFE_ADDRESS));
    }

    #[test]
    fn test_timelock_not_paused_at_genesis() {
        let genesis = create_dev_genesis();
        let reader = GenesisStorageReader::from_genesis(&genesis);
        assert!(!is_timelock_paused(&reader));
    }

    #[test]
    fn test_read_timelock_delay_from_mock() {
        let mut mock = MockStorage::new();
        mock.set(
            TIMELOCK_ADDRESS,
            timelock_slots::MIN_DELAY,
            encode_u64(172800),
        );
        assert_eq!(read_timelock_delay(&mock), Some(172800));
    }

    #[test]
    fn test_timelock_in_production_genesis() {
        let config = GenesisConfig::production();
        let genesis = create_genesis(config);
        let reader = GenesisStorageReader::from_genesis(&genesis);
        assert_eq!(read_timelock_delay(&reader), Some(86400));
        assert_eq!(
            read_timelock_proposer(&reader),
            Some(GOVERNANCE_SAFE_ADDRESS)
        );
        assert!(!is_timelock_paused(&reader));
    }
}

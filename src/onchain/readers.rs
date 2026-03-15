use super::helpers::{
    decode_address, decode_bool, decode_u64, dynamic_array_base_slot, mapping_address_bool_slot,
};
use super::slots::{chain_config_slots, signer_registry_slots, timelock_slots};
use super::StorageReader;
use crate::genesis::{CHAIN_CONFIG_ADDRESS, SIGNER_REGISTRY_ADDRESS, TIMELOCK_ADDRESS};
use alloy_primitives::{Address, B256, U256};

/// Dynamic chain configuration read from the on-chain ChainConfig contract.
///
/// Replaces hardcoded values from genesis/CLI with governance-controlled parameters.
/// Updated via: Governance Safe → ChainConfig.setGasLimit(300_000_000)
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DynamicChainConfig {
    /// Governance address (the Safe multisig)
    pub governance: Address,
    /// Block gas limit (default: 30_000_000)
    pub gas_limit: u64,
    /// Block interval in seconds (default: 2)
    pub block_time: u64,
    /// Max contract bytecode size (default: 24_576)
    pub max_contract_size: u64,
    /// Calldata gas cost per byte (default: 16)
    pub calldata_gas_per_byte: u64,
    /// Max gas per transaction (default: gasLimit)
    pub max_tx_gas: u64,
    /// Mine on tx arrival vs interval (default: false)
    pub eager_mining: bool,
}

/// Dynamic signer list read from the on-chain SignerRegistry contract.
///
/// Updated via: Governance Safe → SignerRegistry.addSigner(addr)
/// Changes take effect at the next epoch block.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DynamicSignerList {
    /// Governance address (the Safe multisig)
    pub governance: Address,
    /// Ordered list of authorized signers
    pub signers: Vec<Address>,
    /// Minimum signers for chain liveness
    pub threshold: u64,
}

/// Read the full ChainConfig from on-chain storage.
///
/// This is called by PoaPayloadBuilder at each block to get the current gas limit
/// and other parameters. The Governance Safe can change these live via transactions.
pub fn read_chain_config(reader: &impl StorageReader) -> Option<DynamicChainConfig> {
    let addr = CHAIN_CONFIG_ADDRESS;

    let governance_val = reader.read_storage(addr, chain_config_slots::GOVERNANCE)?;
    let gas_limit_val = reader.read_storage(addr, chain_config_slots::GAS_LIMIT)?;
    let block_time_val = reader.read_storage(addr, chain_config_slots::BLOCK_TIME)?;
    let max_contract_size_val = reader.read_storage(addr, chain_config_slots::MAX_CONTRACT_SIZE)?;
    let calldata_gas_val = reader.read_storage(addr, chain_config_slots::CALLDATA_GAS_PER_BYTE)?;
    let max_tx_gas_val = reader.read_storage(addr, chain_config_slots::MAX_TX_GAS)?;
    let eager_mining_val = reader
        .read_storage(addr, chain_config_slots::EAGER_MINING)
        .unwrap_or(B256::ZERO);

    Some(DynamicChainConfig {
        governance: decode_address(governance_val),
        gas_limit: decode_u64(gas_limit_val),
        block_time: decode_u64(block_time_val),
        max_contract_size: decode_u64(max_contract_size_val),
        calldata_gas_per_byte: decode_u64(calldata_gas_val),
        max_tx_gas: decode_u64(max_tx_gas_val),
        eager_mining: decode_bool(eager_mining_val),
    })
}

/// Read just the gas limit from ChainConfig (hot path for payload builder).
#[inline]
pub fn read_gas_limit(reader: &impl StorageReader) -> Option<u64> {
    reader
        .read_storage(CHAIN_CONFIG_ADDRESS, chain_config_slots::GAS_LIMIT)
        .map(decode_u64)
}

/// Read just the block time from ChainConfig.
#[inline]
pub fn read_block_time(reader: &impl StorageReader) -> Option<u64> {
    reader
        .read_storage(CHAIN_CONFIG_ADDRESS, chain_config_slots::BLOCK_TIME)
        .map(decode_u64)
}

/// Read the full signer list from SignerRegistry storage.
///
/// This is called by PoaConsensus at epoch blocks to update the authorized
/// signer list. Changes propagate on-chain without node restart.
pub fn read_signer_list(reader: &impl StorageReader) -> Option<DynamicSignerList> {
    let addr = SIGNER_REGISTRY_ADDRESS;

    let governance_val = reader.read_storage(addr, signer_registry_slots::GOVERNANCE)?;
    let length_val = reader.read_storage(addr, signer_registry_slots::SIGNERS_LENGTH)?;
    let threshold_val = reader.read_storage(addr, signer_registry_slots::SIGNER_THRESHOLD)?;

    let signer_count = decode_u64(length_val) as usize;
    let base_slot = dynamic_array_base_slot(signer_registry_slots::SIGNERS_LENGTH);

    let mut signers = Vec::with_capacity(signer_count);
    // Increment a U256 slot counter directly instead of calling U256::from(i)
    // on each iteration — avoids a 256-bit construction per loop iteration.
    let mut slot = base_slot;
    for _ in 0..signer_count {
        if let Some(val) = reader.read_storage(addr, slot) {
            signers.push(decode_address(val));
        }
        slot += U256::ONE;
    }

    Some(DynamicSignerList {
        governance: decode_address(governance_val),
        signers,
        threshold: decode_u64(threshold_val),
    })
}

/// Check if a specific address is a signer via the on-chain mapping.
pub fn is_signer_on_chain(reader: &impl StorageReader, address: Address) -> bool {
    let slot_hash = mapping_address_bool_slot(address, signer_registry_slots::IS_SIGNER_MAPPING);
    let slot = U256::from_be_bytes(slot_hash.0);
    reader
        .read_storage(SIGNER_REGISTRY_ADDRESS, slot)
        .map(decode_bool)
        .unwrap_or(false)
}

/// Read the minimum delay from the Timelock contract.
pub fn read_timelock_delay(reader: &impl StorageReader) -> Option<u64> {
    reader
        .read_storage(TIMELOCK_ADDRESS, timelock_slots::MIN_DELAY)
        .map(decode_u64)
}

/// Read the proposer address from the Timelock contract.
pub fn read_timelock_proposer(reader: &impl StorageReader) -> Option<Address> {
    reader
        .read_storage(TIMELOCK_ADDRESS, timelock_slots::PROPOSER)
        .map(decode_address)
}

/// Check if the Timelock is paused.
pub fn is_timelock_paused(reader: &impl StorageReader) -> bool {
    reader
        .read_storage(TIMELOCK_ADDRESS, timelock_slots::PAUSED)
        .map(decode_bool)
        .unwrap_or(false)
}

use alloy_genesis::GenesisAccount;
use alloy_primitives::{b256, Address, Bytes, B256, U256};
use std::collections::BTreeMap;

use super::accounts::dev_accounts;
use super::addresses::{
    CHAIN_CONFIG_ADDRESS, SIGNER_REGISTRY_ADDRESS, TIMELOCK_ADDRESS, TREASURY_ADDRESS,
};

/// Slot keys for sequential storage positions 1–7 (avoids repeating 32-byte literals).
const SLOT1: B256 = b256!("0000000000000000000000000000000000000000000000000000000000000001");
const SLOT2: B256 = b256!("0000000000000000000000000000000000000000000000000000000000000002");
const SLOT3: B256 = b256!("0000000000000000000000000000000000000000000000000000000000000003");
const SLOT4: B256 = b256!("0000000000000000000000000000000000000000000000000000000000000004");
const SLOT5: B256 = b256!("0000000000000000000000000000000000000000000000000000000000000005");
const SLOT6: B256 = b256!("0000000000000000000000000000000000000000000000000000000000000006");
const SLOT7: B256 = b256!("0000000000000000000000000000000000000000000000000000000000000007");

/// Encodes an `Address` as a right-aligned 32-byte value (EVM ABI encoding for addresses).
#[inline]
fn addr_to_b256(addr: Address) -> B256 {
    let mut slot = [0u8; 32];
    slot[12..32].copy_from_slice(addr.as_slice());
    B256::from(slot)
}

/// Encodes a `u64` as a big-endian 32-byte value.
#[inline]
fn u64_to_b256(val: u64) -> B256 {
    B256::from(U256::from(val).to_be_bytes())
}

/// Returns governance contract allocs for genesis.
///
/// Deploys ChainConfig, SignerRegistry, Treasury, and Timelock contracts with initial
/// storage slots pre-populated to match constructor arguments.
///
/// Storage layout reference (Solidity):
///   - slot 0: governance address
///   - subsequent slots: contract-specific state
pub(crate) fn governance_contract_alloc(
    governance: Address,
    signers: &[Address],
    gas_limit: u64,
    block_time: u64,
) -> BTreeMap<Address, GenesisAccount> {
    let mut contracts = BTreeMap::new();

    // Shared governance slot value (address right-aligned in 32 bytes).
    let gov_b256 = addr_to_b256(governance);

    // --- ChainConfig ---
    // Storage layout:
    //   slot 0: governance
    //   slot 1: gasLimit
    //   slot 2: blockTime
    //   slot 3: maxContractSize
    //   slot 4: calldataGasPerByte
    //   slot 5: maxTxGas
    //   slot 6: eagerMining (bool)
    {
        let gas_limit_b256 = u64_to_b256(gas_limit);
        let mut storage = BTreeMap::new();
        storage.insert(B256::ZERO, gov_b256);          // slot 0: governance
        storage.insert(SLOT1, gas_limit_b256);          // slot 1: gasLimit
        storage.insert(SLOT2, u64_to_b256(block_time)); // slot 2: blockTime
        storage.insert(SLOT3, u64_to_b256(24576));      // slot 3: maxContractSize = 24576
        storage.insert(SLOT4, u64_to_b256(16));         // slot 4: calldataGasPerByte = 16
        storage.insert(SLOT5, gas_limit_b256);          // slot 5: maxTxGas = gasLimit
        // slot 6: eagerMining = false (0) — default, no need to set

        contracts.insert(
            CHAIN_CONFIG_ADDRESS,
            GenesisAccount {
                balance: U256::ZERO,
                nonce: Some(1),
                code: Some(Bytes::from_static(include_bytes!(
                    "../bytecodes/chain_config.bin"
                ))),
                storage: Some(storage),
                private_key: None,
            },
        );
    }

    // --- SignerRegistry ---
    // Storage layout:
    //   slot 0: governance
    //   slot 1: signers.length (dynamic array)
    //   slot 2: isSigner mapping (mapping, individual slots)
    //   slot 3: signerThreshold
    //   keccak256(1): signers[0], signers[1], ... (dynamic array data)
    {
        use alloy_primitives::Keccak256;

        let mut storage = BTreeMap::new();
        storage.insert(B256::ZERO, gov_b256); // slot 0: governance

        // slot 1: signers.length
        storage.insert(SLOT1, B256::from(U256::from(signers.len()).to_be_bytes()));

        // Dynamic array data: keccak256(slot_1) + index
        let mut hasher = Keccak256::new();
        hasher.update(SLOT1.as_slice());
        let array_base = U256::from_be_bytes(hasher.finalize().0);

        for (i, signer) in signers.iter().enumerate() {
            let slot = array_base + U256::from(i);
            storage.insert(B256::from(slot.to_be_bytes()), addr_to_b256(*signer));
        }

        // slot 2: isSigner mapping — keccak256(address . slot_2)
        let true_b256 = u64_to_b256(1);
        for signer in signers {
            let mut hasher = Keccak256::new();
            hasher.update(addr_to_b256(*signer).as_slice());
            hasher.update(SLOT2.as_slice());
            storage.insert(hasher.finalize(), true_b256);
        }

        // slot 3: signerThreshold = (signers.len() / 2 + 1) for majority
        storage.insert(SLOT3, u64_to_b256((signers.len() / 2 + 1) as u64));

        contracts.insert(
            SIGNER_REGISTRY_ADDRESS,
            GenesisAccount {
                balance: U256::ZERO,
                nonce: Some(1),
                code: Some(Bytes::from_static(include_bytes!(
                    "../bytecodes/signer_registry.bin"
                ))),
                storage: Some(storage),
                private_key: None,
            },
        );
    }

    // --- Treasury ---
    // Storage layout:
    //   slot 0: governance
    //   slot 1: signerShare = 4000
    //   slot 2: devShare = 3000
    //   slot 3: communityShare = 2000
    //   slot 4: burnShare = 1000
    //   slot 5: devFund
    //   slot 6: communityFund
    //   slot 7: signerRegistry
    {
        let accounts = dev_accounts();
        let mut storage = BTreeMap::new();
        storage.insert(B256::ZERO, gov_b256);          // slot 0: governance
        storage.insert(SLOT1, u64_to_b256(4000));       // slot 1: signerShare = 4000
        storage.insert(SLOT2, u64_to_b256(3000));       // slot 2: devShare = 3000
        storage.insert(SLOT3, u64_to_b256(2000));       // slot 3: communityShare = 2000
        storage.insert(SLOT4, u64_to_b256(1000));       // slot 4: burnShare = 1000
        storage.insert(SLOT5, addr_to_b256(accounts[5])); // slot 5: devFund
        storage.insert(SLOT6, addr_to_b256(accounts[7])); // slot 6: communityFund
        storage.insert(SLOT7, addr_to_b256(SIGNER_REGISTRY_ADDRESS)); // slot 7: signerRegistry

        contracts.insert(
            TREASURY_ADDRESS,
            GenesisAccount {
                balance: U256::ZERO,
                nonce: Some(1),
                code: Some(Bytes::from_static(include_bytes!(
                    "../bytecodes/treasury.bin"
                ))),
                storage: Some(storage),
                private_key: None,
            },
        );
    }

    // --- Timelock ---
    // Delay-enforcing contract for sensitive governance operations.
    // Storage layout:
    //   slot 0: minDelay (uint256) = 86400 (24 hours)
    //   slot 1: proposer (address) = governance
    //   slot 2: executor (address) = governance
    //   slot 3: admin (address) = governance
    //   slot 4: paused (bool) = false
    //   slot 5: timestamps mapping (mapping, individual slots — empty at genesis)
    {
        let mut storage = BTreeMap::new();
        storage.insert(B256::ZERO, u64_to_b256(86400)); // slot 0: minDelay = 86400 (24 hours)
        storage.insert(SLOT1, gov_b256);                 // slot 1: proposer = governance
        storage.insert(SLOT2, gov_b256);                 // slot 2: executor = governance
        storage.insert(SLOT3, gov_b256);                 // slot 3: admin = governance
        // slot 4: paused = false (0) — default, no need to set

        contracts.insert(
            TIMELOCK_ADDRESS,
            GenesisAccount {
                balance: U256::ZERO,
                nonce: Some(1),
                code: Some(Bytes::from_static(include_bytes!(
                    "../bytecodes/timelock.bin"
                ))),
                storage: Some(storage),
                private_key: None,
            },
        );
    }

    contracts
}

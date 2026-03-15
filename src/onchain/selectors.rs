use alloy_primitives::keccak256;

/// Compute the Solidity function selector (first 4 bytes of keccak256(signature)).
///
/// Prefer the pre-computed constants below for known selectors. This function
/// exists for ad-hoc use with signatures not covered by those constants.
pub fn function_selector(signature: &str) -> [u8; 4] {
    let hash = keccak256(signature.as_bytes());
    [hash[0], hash[1], hash[2], hash[3]]
}

// ---------------------------------------------------------------------------
// ChainConfig getters — keccak256 pre-computed at compile time
// ---------------------------------------------------------------------------

/// `gasLimit()` → keccak256 first 4 bytes = 0xf680_16b7
pub const fn gas_limit() -> [u8; 4] { [0xf6, 0x80, 0x16, 0xb7] }

/// `blockTime()` → keccak256 first 4 bytes = 0x48b1_5166
pub const fn block_time() -> [u8; 4] { [0x48, 0xb1, 0x51, 0x66] }

/// `maxContractSize()` → keccak256 first 4 bytes = 0x0386_4e5c
pub const fn max_contract_size() -> [u8; 4] { [0x03, 0x86, 0x4e, 0x5c] }

/// `calldataGasPerByte()` → keccak256 first 4 bytes = 0x31dc_6249
pub const fn calldata_gas_per_byte() -> [u8; 4] { [0x31, 0xdc, 0x62, 0x49] }

/// `maxTxGas()` → keccak256 first 4 bytes = 0x01c6_4ce8
pub const fn max_tx_gas() -> [u8; 4] { [0x01, 0xc6, 0x4c, 0xe8] }

/// `eagerMining()` → keccak256 first 4 bytes = 0x848c_ee85
pub const fn eager_mining() -> [u8; 4] { [0x84, 0x8c, 0xee, 0x85] }

/// `governance()` → keccak256 first 4 bytes = 0x5aa6_e675
pub const fn governance() -> [u8; 4] { [0x5a, 0xa6, 0xe6, 0x75] }

// ---------------------------------------------------------------------------
// SignerRegistry getters — keccak256 pre-computed at compile time
// ---------------------------------------------------------------------------

/// `getSigners()` → keccak256 first 4 bytes = 0x94cf_795e
pub const fn get_signers() -> [u8; 4] { [0x94, 0xcf, 0x79, 0x5e] }

/// `signerCount()` → keccak256 first 4 bytes = 0x7ca5_48c6
pub const fn signer_count() -> [u8; 4] { [0x7c, 0xa5, 0x48, 0xc6] }

/// `signerThreshold()` → keccak256 first 4 bytes = 0xa4a4_f390
pub const fn signer_threshold() -> [u8; 4] { [0xa4, 0xa4, 0xf3, 0x90] }

/// `isSigner(address)` → keccak256 first 4 bytes = 0x7df7_3e27
pub const fn is_signer() -> [u8; 4] { [0x7d, 0xf7, 0x3e, 0x27] }

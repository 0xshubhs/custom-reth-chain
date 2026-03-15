use alloy_primitives::{Address, Keccak256, B256, U256};

/// Compute the base slot for a Solidity dynamic array's data.
///
/// For `address[] public signers` at slot 1:
///   base = keccak256(abi.encode(1))
///   signers\[0\] lives at base + 0
///   signers\[1\] lives at base + 1
///   etc.
#[inline]
pub fn dynamic_array_base_slot(array_slot: U256) -> U256 {
    let mut hasher = Keccak256::new();
    // Pass the 32-byte big-endian representation directly — no intermediate B256 allocation.
    hasher.update(array_slot.to_be_bytes::<32>());
    U256::from_be_bytes(hasher.finalize().0)
}

/// Compute the storage slot for a Solidity `mapping(address => bool)` entry.
///
/// For `isSigner[addr]` at mapping slot 2:
///   slot = keccak256(abi.encode(addr, 2))
#[inline]
pub fn mapping_address_bool_slot(key: Address, mapping_slot: U256) -> B256 {
    let mut hasher = Keccak256::new();
    // ABI-encode address as 32 bytes (12 zero bytes of left-padding + 20 address bytes).
    let mut key_padded = [0u8; 32];
    key_padded[12..32].copy_from_slice(key.as_slice());
    hasher.update(key_padded);
    // Pass slot bytes directly — no intermediate B256 allocation.
    hasher.update(mapping_slot.to_be_bytes::<32>());
    hasher.finalize()
}

/// Decode an address from a B256 storage value (left-padded with zeros).
#[inline]
pub fn decode_address(value: B256) -> Address {
    Address::from_slice(&value[12..32])
}

/// Decode a u64 from a B256 storage value.
///
/// Solidity stores all integer types right-aligned in a 32-byte slot, so the
/// u64 value occupies the last 8 bytes (bytes 24–31).
#[inline]
pub fn decode_u64(value: B256) -> u64 {
    u64::from_be_bytes(value[24..32].try_into().expect("slice is exactly 8 bytes"))
}

/// Decode a bool from a B256 storage value.
#[inline]
pub fn decode_bool(value: B256) -> bool {
    value[31] != 0
}

/// Encode a u64 value into a B256 storage value (right-aligned, left zero-padded).
#[inline]
pub fn encode_u64(value: u64) -> B256 {
    let mut bytes = [0u8; 32];
    bytes[24..32].copy_from_slice(&value.to_be_bytes());
    B256::from(bytes)
}

/// Encode an address into a B256 storage value (left-padded).
#[inline]
pub fn encode_address(addr: Address) -> B256 {
    let mut bytes = [0u8; 32];
    bytes[12..32].copy_from_slice(addr.as_slice());
    B256::from(bytes)
}

use alloy_primitives::{Address, Keccak256, B256, U256};

/// Compute the base slot for a Solidity dynamic array's data.
///
/// For `address[] public signers` at slot 1:
///   base = keccak256(abi.encode(1))
///   signers\[0\] lives at base + 0
///   signers\[1\] lives at base + 1
///   etc.
pub fn dynamic_array_base_slot(array_slot: U256) -> U256 {
    let mut hasher = Keccak256::new();
    hasher.update(B256::from(array_slot.to_be_bytes()).as_slice());
    U256::from_be_bytes(hasher.finalize().0)
}

/// Compute the storage slot for a Solidity `mapping(address => bool)` entry.
///
/// For `isSigner[addr]` at mapping slot 2:
///   slot = keccak256(abi.encode(addr, 2))
pub fn mapping_address_bool_slot(key: Address, mapping_slot: U256) -> B256 {
    let mut hasher = Keccak256::new();
    let mut key_padded = [0u8; 32];
    key_padded[12..32].copy_from_slice(key.as_slice());
    hasher.update(key_padded);
    hasher.update(B256::from(mapping_slot.to_be_bytes()).as_slice());
    hasher.finalize()
}

/// Decode an address from a B256 storage value (left-padded with zeros).
pub fn decode_address(value: B256) -> Address {
    Address::from_slice(&value[12..32])
}

/// Decode a u64 from a B256 storage value.
pub fn decode_u64(value: B256) -> u64 {
    U256::from_be_bytes(value.0).as_limbs()[0]
}

/// Decode a bool from a B256 storage value.
pub fn decode_bool(value: B256) -> bool {
    value[31] != 0
}

/// Encode a u64 value into a B256 storage value.
pub fn encode_u64(value: u64) -> B256 {
    B256::from(U256::from(value).to_be_bytes())
}

/// Encode an address into a B256 storage value (left-padded).
pub fn encode_address(addr: Address) -> B256 {
    let mut bytes = [0u8; 32];
    bytes[12..32].copy_from_slice(addr.as_slice());
    B256::from(bytes)
}

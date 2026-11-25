/**
 * A few very common signatures, bundled so the decoder still names them when
 * our signature API has no answer (or is not deployed yet). The API is always
 * asked first; this is only the floor. Keys are lowercase selectors / topic0.
 */

export const COMMON_FUNCTIONS: Record<string, string[]> = {
  "0xa9059cbb": ["transfer(address,uint256)"],
  "0x23b872dd": ["transferFrom(address,address,uint256)"],
  "0x095ea7b3": ["approve(address,uint256)"],
  "0x40c10f19": ["mint(address,uint256)"],
  "0x42966c68": ["burn(uint256)"],
  "0x9dc29fac": ["burn(address,uint256)"],
  "0x79cc6790": ["burnFrom(address,uint256)"],
  "0x42842e0e": ["safeTransferFrom(address,address,uint256)"],
  "0xb88d4fde": ["safeTransferFrom(address,address,uint256,bytes)"],
  "0xf242432a": ["safeTransferFrom(address,address,uint256,uint256,bytes)"],
  "0x2eb2c2d6": ["safeBatchTransferFrom(address,address,uint256[],uint256[],bytes)"],
  "0xa22cb465": ["setApprovalForAll(address,bool)"],
  "0xd505accf": ["permit(address,address,uint256,uint256,uint8,bytes32,bytes32)"],
  "0xac9650d8": ["multicall(bytes[])"],
  "0x5ae401dc": ["multicall(uint256,bytes[])"],
  "0x252dba42": ["aggregate((address,bytes)[])"],
  "0x82ad56cb": ["aggregate3((address,bool,bytes)[])"],
  "0x174dea71": ["aggregate3Value((address,bool,uint256,bytes)[])"],
  "0xbce38bd7": ["tryAggregate(bool,(address,bytes)[])"],
  "0x8d80ff0a": ["multiSend(bytes)"],
  "0x6a761202": ["execTransaction(address,uint256,bytes,uint8,uint256,uint256,uint256,address,address,bytes)"],
  "0xb61d27f6": ["execute(address,uint256,bytes)"],
  "0x34fcd5be": ["executeBatch((address,uint256,bytes)[])"],
  "0x47e1da2a": ["executeBatch(address[],uint256[],bytes[])"],
  "0x18dfb3c7": ["executeBatch(address[],bytes[])"],
  "0xe9ae5c53": ["execute(bytes32,bytes)"],
  "0x3659cfe6": ["upgradeTo(address)"],
  "0x4f1ef286": ["upgradeToAndCall(address,bytes)"],
  "0xf2fde38b": ["transferOwnership(address)"],
  "0x715018a6": ["renounceOwnership()"],
  "0x2f2ff15d": ["grantRole(bytes32,address)"],
  "0xd547741f": ["revokeRole(bytes32,address)"],
  "0x8456cb59": ["pause()"],
  "0x3f4ba83a": ["unpause()"],
  "0xd0e30db0": ["deposit()"],
  "0x2e1a7d4d": ["withdraw(uint256)"],
};

export const COMMON_EVENTS: Record<string, string[]> = {
  "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef": ["Transfer(address,address,uint256)"],
  "0x8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925": ["Approval(address,address,uint256)"],
  "0x17307eab39ab6107e8899845ad3d59bd9653f200f220920489ca2b5937696c31": ["ApprovalForAll(address,address,bool)"],
  "0xc3d58168c5ae7397731d063d5bbf3d657854427343f4c083240f7aacaa2d0f62": [
    "TransferSingle(address,address,address,uint256,uint256)",
  ],
  "0x4a39dc06d4c0dbc64b70af90fd698a233a518aa5d07e595d983b8c0526c8f7fb": [
    "TransferBatch(address,address,address,uint256[],uint256[])",
  ],
  "0xbc7cd75a20ee27fd9adebab32041f755214dbc6bffa90cc0225b39da2e5c2d3b": ["Upgraded(address)"],
  "0x7e644d79422f17c01e4894b5f4f588d331ebfa28653d42ae832dc59e38c9798f": ["AdminChanged(address,address)"],
  "0x8be0079c531659141344cd1fd0a4f28419497f9722a3daafe3b4186f6b6457e0": ["OwnershipTransferred(address,address)"],
  "0x2f8788117e7eff1d82e926ec794901d17c78024a50270940304540a733656f0d": ["RoleGranted(bytes32,address,address)"],
  "0xf6391f5c32d9c69d2a47ea670b442974b53935d1edc7fd64eb21e047a839171b": ["RoleRevoked(bytes32,address,address)"],
  "0xc7f505b2f371ae2175ee4913f4499e1f2633a7b5936321eed1cdaeb6115181d2": ["Initialized(uint64)"],
  "0x7f26b83ff96e1f2b6a682f133852f6798a09c465da95921460cefb3847402498": ["Initialized(uint8)"],
  "0x62e78cea01bee320cd4e420270b5ea74000d11b0c9f74754ebdbfc544b05a258": ["Paused(address)"],
  "0x5db9ee0a495bf2e6ff9c91a7834c1ba4fdd244a5e8aa4e537bd38aeae4b073aa": ["Unpaused(address)"],
  "0xe1fffcc4923d04b559f4d29a8bfc6cda04eb5b0d3c460751c2402c5c5cc9109c": ["Deposit(address,uint256)"],
  "0x7fcf532c15f0a6db0bd6d0e038bea71d30d808c7d98cb3bf7268a95bf5081b65": ["Withdrawal(address,uint256)"],
};

export const COMMON_ERRORS: Record<string, string[]> = {
  "0x08c379a0": ["Error(string)"],
  "0x4e487b71": ["Panic(uint256)"],
};

/** Solidity's Panic(uint256) codes. */
export const PANIC_CODES: Record<string, string> = {
  "0": "generic compiler panic",
  "1": "assert(false)",
  "17": "arithmetic overflow or underflow",
  "18": "division or modulo by zero",
  "33": "invalid enum value",
  "34": "corrupt storage byte array",
  "49": "pop() on an empty array",
  "50": "array index out of bounds",
  "65": "out of memory",
  "81": "call to an uninitialized function",
};

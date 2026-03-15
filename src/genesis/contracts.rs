use alloy_genesis::GenesisAccount;
use alloy_primitives::{address, b256, bytes, Address, Bytes, B256, U256};
use std::collections::BTreeMap;

use super::addresses::{
    EIP1967_ADMIN_SLOT, MINER_PROXY_ADDRESS, SAFE_FALLBACK_HANDLER_ADDRESS, SAFE_MULTISEND_ADDRESS,
    SAFE_PROXY_FACTORY_ADDRESS, SAFE_SINGLETON_ADDRESS,
};

/// Constructs a standard deployed-contract `GenesisAccount` with no balance,
/// nonce=1, the given bytecode, and no storage.  The caller may mutate the
/// returned value to add storage.
#[inline]
fn code_account(code: Bytes) -> GenesisAccount {
    GenesisAccount {
        balance: U256::ZERO,
        nonce: Some(1),
        code: Some(code),
        storage: None,
        private_key: None,
    }
}

/// Encodes an `Address` as a right-aligned 32-byte EVM word.
#[inline]
fn addr_to_b256(addr: Address) -> B256 {
    let mut slot = [0u8; 32];
    slot[12..32].copy_from_slice(addr.as_slice());
    B256::from(slot)
}

/// Returns system contracts required by Cancun and Prague hardforks.
/// These must be pre-deployed in genesis for the EVM to function correctly.
pub(crate) fn system_contract_alloc() -> BTreeMap<Address, GenesisAccount> {
    let mut contracts = BTreeMap::new();

    // EIP-4788: Beacon block root contract (Cancun)
    // Stores parent beacon block root at the start of each block
    contracts.insert(
        address!("000F3df6D732807Ef1319fB7B8bB8522d0Beac02"),
        code_account(bytes!("3373fffffffffffffffffffffffffffffffffffffffe14604d57602036146024575f5ffd5b5f35801560495762001fff810690815414603c575f5ffd5b62001fff01545f5260205ff35b5f5ffd5b62001fff42064281555f359062001fff015500")),
    );

    // EIP-2935: History storage contract (Prague)
    // Serves historical block hashes from state
    contracts.insert(
        address!("0000F90827F1C53a10cb7A02335B175320002935"),
        code_account(bytes!("3373fffffffffffffffffffffffffffffffffffffffe14604657602036036042575f35600143038111604257611fff81430311604257611fff9006545f5260205ff35b5f5ffd5b5f35611fff60014303065500")),
    );

    // EIP-7002: Withdrawal requests contract (Prague)
    // Execution layer triggerable withdrawals
    contracts.insert(
        address!("00000961Ef480Eb55e80D19ad83579A64c007002"),
        code_account(bytes!("3373fffffffffffffffffffffffffffffffffffffffe1460cb5760115f54807fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff146101f457600182026001905f5b5f82111560685781019083028483029004916001019190604d565b909390049250505036603814608857366101f457346101f4575f5260205ff35b34106101f457600154600101600155600354806003026004013381556001015f35815560010160203590553360601b5f5260385f601437604c5fa0600101600355005b6003546002548082038060101160df575060105b5f5b8181146101835782810160030260040181604c02815460601b8152601401816001015481526020019060020154807fffffffffffffffffffffffffffffffff00000000000000000000000000000000168252906010019060401c908160381c81600701538160301c81600601538160281c81600501538160201c81600401538160181c81600301538160101c81600201538160081c81600101535360010160e1565b910180921461019557906002556101a0565b90505f6002555f6003555b5f54807fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff14156101cd57505f5b6001546002828201116101e25750505f6101e8565b01600290035b5f555f600155604c025ff35b5f5ffd")),
    );

    // EIP-7251: Consolidation requests contract (Prague)
    // Validator consolidation requests
    contracts.insert(
        address!("0000BBdDc7CE488642fb579F8B00f3a590007251"),
        code_account(bytes!("3373fffffffffffffffffffffffffffffffffffffffe1460d35760115f54807fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff1461019a57600182026001905f5b5f82111560685781019083028483029004916001019190604d565b9093900492505050366060146088573661019a573461019a575f5260205ff35b341061019a57600154600101600155600354806004026004013381556001015f358155600101602035815560010160403590553360601b5f5260605f60143760745fa0600101600355005b6003546002548082038060021160e7575060025b5f5b8181146101295782810160040260040181607402815460601b815260140181600101548152602001816002015481526020019060030154905260010160e9565b910180921461013b5790600255610146565b90505f6002555f6003555b5f54807fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff141561017357505f5b6001546001828201116101885750505f61018e565b01600190035b5f555f6001556074025ff35b5f5ffd0000")),
    );

    contracts
}

/// Returns the EIP-1967 Miner Proxy contract for block reward collection.
///
/// This is a minimal proxy that:
/// - Accepts ETH (block rewards go to coinbase which is this address)
/// - Delegates all calls to the implementation address stored at EIP-1967 slot
/// - Admin can upgrade the implementation
///
/// Initial implementation is address(0) - just a receiver.
/// Deploy an implementation contract later to add reward distribution logic.
pub(crate) fn miner_proxy_alloc(admin: Address) -> BTreeMap<Address, GenesisAccount> {
    let mut contracts = BTreeMap::new();

    let proxy_bytecode = bytes!(
        "36"             // calldatasize
        "15"             // iszero
        "60" "43"        // push1 0x43 (STOP_DEST)
        "57"             // jumpi
        "36"             // calldatasize
        "60" "00"        // push1 0x00
        "60" "00"        // push1 0x00
        "37"             // calldatacopy
        "60" "00"        // push1 0x00
        "60" "00"        // push1 0x00
        "36"             // calldatasize
        "60" "00"        // push1 0x00
        "7f"             // push32 (EIP-1967 implementation slot)
        "360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"
        "54"             // sload
        "5a"             // gas
        "f4"             // delegatecall
        "3d"             // returndatasize
        "60" "00"        // push1 0x00
        "60" "00"        // push1 0x00
        "3e"             // returndatacopy
        "90"             // swap1
        "60" "3d"        // push1 0x3d (RETURN_DEST)
        "57"             // jumpi
        "3d"             // returndatasize
        "60" "00"        // push1 0x00
        "fd"             // revert
        "5b"             // jumpdest (RETURN_DEST = 0x3d)
        "3d"             // returndatasize
        "60" "00"        // push1 0x00
        "f3"             // return
        "5b"             // jumpdest (STOP_DEST = 0x43)
        "00"             // stop
    );

    // Set admin in EIP-1967 admin slot
    let mut storage = BTreeMap::new();
    storage.insert(EIP1967_ADMIN_SLOT, addr_to_b256(admin));

    let mut account = code_account(proxy_bytecode);
    account.storage = Some(storage);
    contracts.insert(MINER_PROXY_ADDRESS, account);

    contracts
}

/// Returns ERC-4337 Account Abstraction and ecosystem infrastructure contracts.
/// These are pre-deployed in genesis for immediate availability from block 0.
pub(crate) fn erc4337_contract_alloc() -> BTreeMap<Address, GenesisAccount> {
    let mut contracts = BTreeMap::new();

    // ERC-4337 EntryPoint v0.7 (canonical address)
    contracts.insert(
        address!("0000000071727De22E5E9d8BAf0edAc6f37da032"),
        code_account(Bytes::from_static(include_bytes!(
            "../bytecodes/entrypoint_v07.bin"
        ))),
    );

    // WETH9: Wrapped Ether (canonical Ethereum mainnet address)
    let mut weth_storage = BTreeMap::new();
    // Slot 0: name = "Wrapped Ether" (Solidity short string encoding)
    weth_storage.insert(
        B256::ZERO,
        b256!("577261707065642045746865720000000000000000000000000000000000001a"),
    );
    // Slot 1: symbol = "WETH" (Solidity short string encoding)
    weth_storage.insert(
        b256!("0000000000000000000000000000000000000000000000000000000000000001"),
        b256!("5745544800000000000000000000000000000000000000000000000000000008"),
    );
    // Slot 2: decimals = 18
    weth_storage.insert(
        b256!("0000000000000000000000000000000000000000000000000000000000000002"),
        b256!("0000000000000000000000000000000000000000000000000000000000000012"),
    );
    let mut weth = code_account(Bytes::from_static(include_bytes!("../bytecodes/weth9.bin")));
    weth.storage = Some(weth_storage);
    contracts.insert(address!("C02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"), weth);

    // Multicall3 (canonical address)
    contracts.insert(
        address!("cA11bde05977b3631167028862bE2a173976CA11"),
        code_account(Bytes::from_static(include_bytes!(
            "../bytecodes/multicall3.bin"
        ))),
    );

    // CREATE2 Deterministic Deployment Proxy (Nick's method, canonical address)
    contracts.insert(
        address!("4e59b44847b379578588920cA78FbF26c0B4956C"),
        code_account(Bytes::from_static(include_bytes!(
            "../bytecodes/create2_deployer.bin"
        ))),
    );

    // SimpleAccountFactory (ERC-4337 reference implementation)
    contracts.insert(
        address!("9406Cc6185a346906296840746125a0E44976454"),
        code_account(Bytes::from_static(include_bytes!(
            "../bytecodes/simple_account_factory.bin"
        ))),
    );

    contracts
}

/// Returns Gnosis Safe contract allocs for genesis.
/// Deploys the 4 core Safe contracts at their canonical addresses.
pub(crate) fn safe_contract_alloc() -> BTreeMap<Address, GenesisAccount> {
    let mut contracts = BTreeMap::new();

    // Safe Singleton v1.3.0
    contracts.insert(
        SAFE_SINGLETON_ADDRESS,
        code_account(Bytes::from_static(include_bytes!(
            "../bytecodes/safe_singleton.bin"
        ))),
    );

    // Safe Proxy Factory
    contracts.insert(
        SAFE_PROXY_FACTORY_ADDRESS,
        code_account(Bytes::from_static(include_bytes!(
            "../bytecodes/safe_proxy_factory.bin"
        ))),
    );

    // Compatibility Fallback Handler
    contracts.insert(
        SAFE_FALLBACK_HANDLER_ADDRESS,
        code_account(Bytes::from_static(include_bytes!(
            "../bytecodes/safe_fallback_handler.bin"
        ))),
    );

    // MultiSend
    contracts.insert(
        SAFE_MULTISEND_ADDRESS,
        code_account(Bytes::from_static(include_bytes!(
            "../bytecodes/safe_multisend.bin"
        ))),
    );

    contracts
}

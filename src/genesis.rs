//! Genesis Configuration for POA Chain
//!
//! This module provides utilities for creating genesis configurations
//! that are compatible with Ethereum tooling while supporting POA consensus.

use alloy_genesis::{Genesis, GenesisAccount};
use alloy_primitives::{address, b256, bytes, Address, Bytes, B256, U256};
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
        let accounts = dev_accounts();
        let signers = dev_signers();

        let balance = default_prefund_balance();
        let mut prefunded = BTreeMap::new();
        for account in accounts {
            prefunded.insert(account, balance);
        }

        Self {
            chain_id: 9323310,
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

    /// Create a production configuration for Meowchain
    ///
    /// - Chain ID: 9323310
    /// - 5 signers (fault-tolerant: survives 2 offline, needs 3/5 online)
    /// - 12-second block time
    /// - 60M gas limit (high throughput for POA)
    /// - Treasury, operations, and community accounts prefunded
    /// - "Meowchain" vanity in genesis block
    pub fn production() -> Self {
        let signers = dev_accounts().into_iter().take(5).collect::<Vec<_>>();

        // Treasury: 2,500,000 ETH - ecosystem development fund
        let treasury_balance =
            U256::from(2_500_000u64) * U256::from(10u64).pow(U256::from(18u64));
        // Operations: 500,000 ETH - infrastructure and running costs
        let operations_balance =
            U256::from(500_000u64) * U256::from(10u64).pow(U256::from(18u64));
        // Community: 100,000 ETH - faucet, airdrops, grants
        let community_balance =
            U256::from(100_000u64) * U256::from(10u64).pow(U256::from(18u64));
        // Signer gas: 10,000 ETH per signer - for block production gas costs
        let signer_balance = U256::from(10_000u64) * U256::from(10u64).pow(U256::from(18u64));

        let mut prefunded = BTreeMap::new();

        // Signers get gas money (first 5 accounts)
        for signer in &signers {
            prefunded.insert(*signer, signer_balance);
        }

        // Treasury (account index 5)
        prefunded.insert(dev_accounts()[5], treasury_balance);
        // Operations (account index 6)
        prefunded.insert(dev_accounts()[6], operations_balance);
        // Community/Faucet (account index 7)
        prefunded.insert(dev_accounts()[7], community_balance);

        // "Meowchain" as vanity data
        let mut vanity = [0u8; 32];
        let tag = b"Meowchain";
        vanity[..tag.len()].copy_from_slice(tag);

        Self {
            chain_id: 9323310,
            gas_limit: 60_000_000, // 60M - high throughput for POA
            prefunded_accounts: prefunded,
            signers,
            block_period: 12, // Production block time
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

/// Returns system contracts required by Cancun and Prague hardforks.
/// These must be pre-deployed in genesis for the EVM to function correctly.
fn system_contract_alloc() -> BTreeMap<Address, GenesisAccount> {
    let mut contracts = BTreeMap::new();

    // EIP-4788: Beacon block root contract (Cancun)
    // Stores parent beacon block root at the start of each block
    contracts.insert(
        address!("000F3df6D732807Ef1319fB7B8bB8522d0Beac02"),
        GenesisAccount {
            balance: U256::ZERO,
            nonce: Some(1),
            code: Some(bytes!("3373fffffffffffffffffffffffffffffffffffffffe14604d57602036146024575f5ffd5b5f35801560495762001fff810690815414603c575f5ffd5b62001fff01545f5260205ff35b5f5ffd5b62001fff42064281555f359062001fff015500").into()),
            storage: None,
            private_key: None,
        },
    );

    // EIP-2935: History storage contract (Prague)
    // Serves historical block hashes from state
    contracts.insert(
        address!("0000F90827F1C53a10cb7A02335B175320002935"),
        GenesisAccount {
            balance: U256::ZERO,
            nonce: Some(1),
            code: Some(bytes!("3373fffffffffffffffffffffffffffffffffffffffe14604657602036036042575f35600143038111604257611fff81430311604257611fff9006545f5260205ff35b5f5ffd5b5f35611fff60014303065500").into()),
            storage: None,
            private_key: None,
        },
    );

    // EIP-7002: Withdrawal requests contract (Prague)
    // Execution layer triggerable withdrawals
    contracts.insert(
        address!("00000961Ef480Eb55e80D19ad83579A64c007002"),
        GenesisAccount {
            balance: U256::ZERO,
            nonce: Some(1),
            code: Some(bytes!("3373fffffffffffffffffffffffffffffffffffffffe1460cb5760115f54807fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff146101f457600182026001905f5b5f82111560685781019083028483029004916001019190604d565b909390049250505036603814608857366101f457346101f4575f5260205ff35b34106101f457600154600101600155600354806003026004013381556001015f35815560010160203590553360601b5f5260385f601437604c5fa0600101600355005b6003546002548082038060101160df575060105b5f5b8181146101835782810160030260040181604c02815460601b8152601401816001015481526020019060020154807fffffffffffffffffffffffffffffffff00000000000000000000000000000000168252906010019060401c908160381c81600701538160301c81600601538160281c81600501538160201c81600401538160181c81600301538160101c81600201538160081c81600101535360010160e1565b910180921461019557906002556101a0565b90505f6002555f6003555b5f54807fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff14156101cd57505f5b6001546002828201116101e25750505f6101e8565b01600290035b5f555f600155604c025ff35b5f5ffd").into()),
            storage: None,
            private_key: None,
        },
    );

    // EIP-7251: Consolidation requests contract (Prague)
    // Validator consolidation requests
    contracts.insert(
        address!("0000BBdDc7CE488642fb579F8B00f3a590007251"),
        GenesisAccount {
            balance: U256::ZERO,
            nonce: Some(1),
            code: Some(bytes!("3373fffffffffffffffffffffffffffffffffffffffe1460d35760115f54807fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff1461019a57600182026001905f5b5f82111560685781019083028483029004916001019190604d565b9093900492505050366060146088573661019a573461019a575f5260205ff35b341061019a57600154600101600155600354806004026004013381556001015f358155600101602035815560010160403590553360601b5f5260605f60143760745fa0600101600355005b6003546002548082038060021160e7575060025b5f5b8181146101295782810160040260040181607402815460601b815260140181600101548152602001816002015481526020019060030154905260010160e9565b910180921461013b5790600255610146565b90505f6002555f6003555b5f54807fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff141561017357505f5b6001546001828201116101885750505f61018e565b01600190035b5f555f6001556074025ff35b5f5ffd0000").into()),
            storage: None,
            private_key: None,
        },
    );

    contracts
}

/// EIP-1967 Miner Proxy address - block rewards (coinbase) are sent here.
/// This proxy allows upgrading the reward distribution logic without changing consensus.
///
/// Storage layout (EIP-1967):
/// - Implementation slot: 0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc
/// - Admin slot: 0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103
pub const MINER_PROXY_ADDRESS: Address = address!("0000000000000000000000000000000000001967");

/// Admin slot for EIP-1967 proxy
const EIP1967_ADMIN_SLOT: B256 =
    b256!("b53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103");

/// Returns the EIP-1967 Miner Proxy contract for block reward collection.
///
/// This is a minimal proxy that:
/// - Accepts ETH (block rewards go to coinbase which is this address)
/// - Delegates all calls to the implementation address stored at EIP-1967 slot
/// - Admin can upgrade the implementation
///
/// Initial implementation is address(0) - just a receiver.
/// Deploy an implementation contract later to add reward distribution logic.
fn miner_proxy_alloc(admin: Address) -> BTreeMap<Address, GenesisAccount> {
    let mut contracts = BTreeMap::new();

    // Minimal EIP-1967 proxy runtime bytecode:
    // - On receive (no calldata): accept ETH silently (STOP)
    // - On call: delegatecall to implementation at EIP-1967 slot
    //
    // Assembly:
    //   calldatasize          // cds
    //   iszero                // cds == 0?
    //   push1 STOP_DEST      // dest
    //   jumpi                 // if no calldata, just stop (accept ETH)
    //   calldatasize          // cds
    //   push1 0x00            // 0, cds
    //   push1 0x00            // 0, 0, cds
    //   calldatacopy          // copy calldata to memory
    //   push1 0x00            // 0
    //   push1 0x00            // 0, 0
    //   calldatasize          // cds, 0, 0
    //   push1 0x00            // 0, cds, 0, 0
    //   push32 IMPL_SLOT      // slot, 0, cds, 0, 0
    //   sload                 // impl, 0, cds, 0, 0
    //   gas                   // gas, impl, 0, cds, 0, 0
    //   delegatecall          // success
    //   returndatasize        // rds, success
    //   push1 0x00            // 0, rds, success
    //   push1 0x00            // 0, 0, rds, success
    //   returndatacopy        // copy return data
    //   swap1                 // success, ...
    //   push1 RETURN_DEST     // dest, success
    //   jumpi                 // if success, jump to return
    //   returndatasize        // rds
    //   push1 0x00            // 0, rds
    //   revert                // revert(0, rds)
    //   jumpdest              // RETURN_DEST
    //   returndatasize        // rds
    //   push1 0x00            // 0, rds
    //   return                // return(0, rds)
    //   jumpdest              // STOP_DEST
    //   stop                  // accept ETH
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
    let mut admin_value = [0u8; 32];
    admin_value[12..32].copy_from_slice(admin.as_slice());
    storage.insert(EIP1967_ADMIN_SLOT, B256::from(admin_value));

    contracts.insert(
        MINER_PROXY_ADDRESS,
        GenesisAccount {
            balance: U256::ZERO,
            nonce: Some(1),
            code: Some(proxy_bytecode.into()),
            storage: Some(storage),
            private_key: None,
        },
    );

    contracts
}

/// Returns ERC-4337 Account Abstraction and ecosystem infrastructure contracts.
/// These are pre-deployed in genesis for immediate availability from block 0.
///
/// Contracts included:
/// - EntryPoint v0.7: Core ERC-4337 contract for UserOperation validation/execution
/// - WETH9: Wrapped native token for paymaster operations and DeFi
/// - Multicall3: Batch RPC calls used by bundlers for validation
/// - CREATE2 Deployer: Deterministic contract deployment (Nick's method)
/// - SimpleAccountFactory: Reference ERC-4337 smart wallet factory
fn erc4337_contract_alloc() -> BTreeMap<Address, GenesisAccount> {
    let mut contracts = BTreeMap::new();

    // ERC-4337 EntryPoint v0.7 (canonical address)
    // Core contract that validates and executes UserOperations for account abstraction
    contracts.insert(
        address!("0000000071727De22E5E9d8BAf0edAc6f37da032"),
        GenesisAccount {
            balance: U256::ZERO,
            nonce: Some(1),
            code: Some(Bytes::from_static(include_bytes!("bytecodes/entrypoint_v07.bin"))),
            storage: None,
            private_key: None,
        },
    );

    // WETH9: Wrapped Ether (canonical Ethereum mainnet address)
    // Required for paymaster operations, DeFi protocols, and token wrapping
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
    contracts.insert(
        address!("C02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"),
        GenesisAccount {
            balance: U256::ZERO,
            nonce: Some(1),
            code: Some(Bytes::from_static(include_bytes!("bytecodes/weth9.bin"))),
            storage: Some(weth_storage),
            private_key: None,
        },
    );

    // Multicall3 (canonical address)
    // Batch RPC calls used by bundlers for UserOp validation and frontends for efficient reads
    contracts.insert(
        address!("cA11bde05977b3631167028862bE2a173976CA11"),
        GenesisAccount {
            balance: U256::ZERO,
            nonce: Some(1),
            code: Some(Bytes::from_static(include_bytes!("bytecodes/multicall3.bin"))),
            storage: None,
            private_key: None,
        },
    );

    // CREATE2 Deterministic Deployment Proxy (Nick's method, canonical address)
    // Enables deterministic contract addresses across all EVM chains
    contracts.insert(
        address!("4e59b44847b379578588920cA78FbF26c0B4956C"),
        GenesisAccount {
            balance: U256::ZERO,
            nonce: Some(1),
            code: Some(Bytes::from_static(include_bytes!("bytecodes/create2_deployer.bin"))),
            storage: None,
            private_key: None,
        },
    );

    // SimpleAccountFactory (ERC-4337 reference implementation)
    // Factory for creating minimal smart wallet accounts compatible with EntryPoint v0.7
    contracts.insert(
        address!("9406Cc6185a346906296840746125a0E44976454"),
        GenesisAccount {
            balance: U256::ZERO,
            nonce: Some(1),
            code: Some(Bytes::from_static(include_bytes!("bytecodes/simple_account_factory.bin"))),
            storage: None,
            private_key: None,
        },
    );

    contracts
}

// =============================================================================
// Governance contracts — deployed at genesis, controlled by governance Safe
// =============================================================================

/// ChainConfig contract address (deterministic, pre-assigned)
pub const CHAIN_CONFIG_ADDRESS: Address = address!("00000000000000000000000000000000C04F1600");

/// SignerRegistry contract address (deterministic, pre-assigned)
pub const SIGNER_REGISTRY_ADDRESS: Address = address!("000000000000000000000000000000005164EB00");

/// Treasury contract address (deterministic, pre-assigned)
pub const TREASURY_ADDRESS: Address = address!("0000000000000000000000000000000007EA5B00");

/// Governance Safe address (the multisig that controls all governance contracts).
/// In dev mode this is just the first dev account; in production it would be a
/// deployed Gnosis Safe proxy.
pub const GOVERNANCE_SAFE_ADDRESS: Address = address!("000000000000000000000000000000006F5AFE00");

/// Returns governance contract allocs for genesis.
///
/// Deploys ChainConfig, SignerRegistry, and Treasury contracts with initial
/// storage slots pre-populated to match constructor arguments.
///
/// Storage layout reference (Solidity):
///   - slot 0: governance address
///   - subsequent slots: contract-specific state
fn governance_contract_alloc(
    governance: Address,
    signers: &[Address],
    gas_limit: u64,
    block_time: u64,
) -> BTreeMap<Address, GenesisAccount> {
    let mut contracts = BTreeMap::new();

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
        let mut storage = BTreeMap::new();
        // slot 0: governance
        let mut gov_slot = [0u8; 32];
        gov_slot[12..32].copy_from_slice(governance.as_slice());
        storage.insert(B256::ZERO, B256::from(gov_slot));

        // slot 1: gasLimit
        storage.insert(
            b256!("0000000000000000000000000000000000000000000000000000000000000001"),
            B256::from(U256::from(gas_limit).to_be_bytes()),
        );
        // slot 2: blockTime
        storage.insert(
            b256!("0000000000000000000000000000000000000000000000000000000000000002"),
            B256::from(U256::from(block_time).to_be_bytes()),
        );
        // slot 3: maxContractSize = 24576
        storage.insert(
            b256!("0000000000000000000000000000000000000000000000000000000000000003"),
            B256::from(U256::from(24576u64).to_be_bytes()),
        );
        // slot 4: calldataGasPerByte = 16
        storage.insert(
            b256!("0000000000000000000000000000000000000000000000000000000000000004"),
            B256::from(U256::from(16u64).to_be_bytes()),
        );
        // slot 5: maxTxGas = gasLimit
        storage.insert(
            b256!("0000000000000000000000000000000000000000000000000000000000000005"),
            B256::from(U256::from(gas_limit).to_be_bytes()),
        );
        // slot 6: eagerMining = false (0)

        contracts.insert(
            CHAIN_CONFIG_ADDRESS,
            GenesisAccount {
                balance: U256::ZERO,
                nonce: Some(1),
                code: Some(Bytes::from_static(include_bytes!("bytecodes/chain_config.bin"))),
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
        // slot 0: governance
        let mut gov_slot = [0u8; 32];
        gov_slot[12..32].copy_from_slice(governance.as_slice());
        storage.insert(B256::ZERO, B256::from(gov_slot));

        // slot 1: signers.length
        storage.insert(
            b256!("0000000000000000000000000000000000000000000000000000000000000001"),
            B256::from(U256::from(signers.len()).to_be_bytes()),
        );

        // Dynamic array data: keccak256(slot_1) + index
        let mut hasher = Keccak256::new();
        hasher.update(B256::from(U256::from(1u64).to_be_bytes()).as_slice());
        let array_base = U256::from_be_bytes(hasher.finalize().0);

        for (i, signer) in signers.iter().enumerate() {
            let slot = array_base + U256::from(i);
            let mut addr_slot = [0u8; 32];
            addr_slot[12..32].copy_from_slice(signer.as_slice());
            storage.insert(B256::from(slot.to_be_bytes()), B256::from(addr_slot));
        }

        // slot 2: isSigner mapping — keccak256(address . slot_2)
        for signer in signers {
            let mut hasher = Keccak256::new();
            let mut key_padded = [0u8; 32];
            key_padded[12..32].copy_from_slice(signer.as_slice());
            hasher.update(&key_padded);
            hasher.update(B256::from(U256::from(2u64).to_be_bytes()).as_slice());
            let mapping_slot = hasher.finalize();
            storage.insert(mapping_slot, B256::from(U256::from(1u64).to_be_bytes()));
        }

        // slot 3: signerThreshold = (signers.len() / 2 + 1) for majority
        let threshold = signers.len() / 2 + 1;
        storage.insert(
            b256!("0000000000000000000000000000000000000000000000000000000000000003"),
            B256::from(U256::from(threshold).to_be_bytes()),
        );

        contracts.insert(
            SIGNER_REGISTRY_ADDRESS,
            GenesisAccount {
                balance: U256::ZERO,
                nonce: Some(1),
                code: Some(Bytes::from_static(include_bytes!("bytecodes/signer_registry.bin"))),
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
        let mut storage = BTreeMap::new();
        // slot 0: governance
        let mut gov_slot = [0u8; 32];
        gov_slot[12..32].copy_from_slice(governance.as_slice());
        storage.insert(B256::ZERO, B256::from(gov_slot));

        // slot 1: signerShare = 4000
        storage.insert(
            b256!("0000000000000000000000000000000000000000000000000000000000000001"),
            B256::from(U256::from(4000u64).to_be_bytes()),
        );
        // slot 2: devShare = 3000
        storage.insert(
            b256!("0000000000000000000000000000000000000000000000000000000000000002"),
            B256::from(U256::from(3000u64).to_be_bytes()),
        );
        // slot 3: communityShare = 2000
        storage.insert(
            b256!("0000000000000000000000000000000000000000000000000000000000000003"),
            B256::from(U256::from(2000u64).to_be_bytes()),
        );
        // slot 4: burnShare = 1000
        storage.insert(
            b256!("0000000000000000000000000000000000000000000000000000000000000004"),
            B256::from(U256::from(1000u64).to_be_bytes()),
        );
        // slot 5: devFund = dev_accounts()[5] (treasury account in production)
        let dev_fund = dev_accounts()[5];
        let mut dev_fund_slot = [0u8; 32];
        dev_fund_slot[12..32].copy_from_slice(dev_fund.as_slice());
        storage.insert(
            b256!("0000000000000000000000000000000000000000000000000000000000000005"),
            B256::from(dev_fund_slot),
        );
        // slot 6: communityFund = dev_accounts()[7] (community account)
        let community_fund = dev_accounts()[7];
        let mut community_fund_slot = [0u8; 32];
        community_fund_slot[12..32].copy_from_slice(community_fund.as_slice());
        storage.insert(
            b256!("0000000000000000000000000000000000000000000000000000000000000006"),
            B256::from(community_fund_slot),
        );
        // slot 7: signerRegistry
        let mut sr_slot = [0u8; 32];
        sr_slot[12..32].copy_from_slice(SIGNER_REGISTRY_ADDRESS.as_slice());
        storage.insert(
            b256!("0000000000000000000000000000000000000000000000000000000000000007"),
            B256::from(sr_slot),
        );

        contracts.insert(
            TREASURY_ADDRESS,
            GenesisAccount {
                balance: U256::ZERO,
                nonce: Some(1),
                code: Some(Bytes::from_static(include_bytes!("bytecodes/treasury.bin"))),
                storage: Some(storage),
                private_key: None,
            },
        );
    }

    contracts
}

// =============================================================================
// Gnosis Safe contracts — canonical deployments for multisig governance
// =============================================================================

/// Gnosis Safe Singleton v1.3.0 canonical address
pub const SAFE_SINGLETON_ADDRESS: Address =
    address!("d9Db270c1B5E3Bd161E8c8503c55cEABeE709552");

/// Gnosis Safe Proxy Factory canonical address
pub const SAFE_PROXY_FACTORY_ADDRESS: Address =
    address!("a6B71E26C5e0845f74c812102Ca7114b6a896AB2");

/// Gnosis Safe Compatibility Fallback Handler canonical address
pub const SAFE_FALLBACK_HANDLER_ADDRESS: Address =
    address!("f48f2B2d2a534e402487b3ee7C18c33Aec0Fe5e4");

/// Gnosis Safe MultiSend canonical address
pub const SAFE_MULTISEND_ADDRESS: Address =
    address!("A238CBeb142c10Ef7Ad8442C6D1f9E89e07e7761");

/// Returns Gnosis Safe contract allocs for genesis.
/// Deploys the 4 core Safe contracts at their canonical addresses.
fn safe_contract_alloc() -> BTreeMap<Address, GenesisAccount> {
    let mut contracts = BTreeMap::new();

    // Safe Singleton v1.3.0
    contracts.insert(
        SAFE_SINGLETON_ADDRESS,
        GenesisAccount {
            balance: U256::ZERO,
            nonce: Some(1),
            code: Some(Bytes::from_static(include_bytes!("bytecodes/safe_singleton.bin"))),
            storage: None,
            private_key: None,
        },
    );

    // Safe Proxy Factory
    contracts.insert(
        SAFE_PROXY_FACTORY_ADDRESS,
        GenesisAccount {
            balance: U256::ZERO,
            nonce: Some(1),
            code: Some(Bytes::from_static(include_bytes!("bytecodes/safe_proxy_factory.bin"))),
            storage: None,
            private_key: None,
        },
    );

    // Compatibility Fallback Handler
    contracts.insert(
        SAFE_FALLBACK_HANDLER_ADDRESS,
        GenesisAccount {
            balance: U256::ZERO,
            nonce: Some(1),
            code: Some(Bytes::from_static(include_bytes!("bytecodes/safe_fallback_handler.bin"))),
            storage: None,
            private_key: None,
        },
    );

    // MultiSend
    contracts.insert(
        SAFE_MULTISEND_ADDRESS,
        GenesisAccount {
            balance: U256::ZERO,
            nonce: Some(1),
            code: Some(Bytes::from_static(include_bytes!("bytecodes/safe_multisend.bin"))),
            storage: None,
            private_key: None,
        },
    );

    contracts
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

    // Add system contracts required by Cancun/Prague hardforks
    alloc.extend(system_contract_alloc());

    // Add ERC-4337 Account Abstraction and infrastructure contracts
    alloc.extend(erc4337_contract_alloc());

    // Add EIP-1967 Miner Proxy for anonymous block reward collection
    // Admin is set to the governance Safe address
    alloc.extend(miner_proxy_alloc(GOVERNANCE_SAFE_ADDRESS));

    // Add governance contracts (ChainConfig, SignerRegistry, Treasury)
    // Governance is set to the governance Safe address
    alloc.extend(governance_contract_alloc(
        GOVERNANCE_SAFE_ADDRESS,
        &config.signers,
        config.gas_limit,
        config.block_period,
    ));

    // Add Gnosis Safe contracts for multisig governance
    alloc.extend(safe_contract_alloc());

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

    #[test]
    fn test_dev_genesis_creation() {
        let genesis = create_dev_genesis();

        // Verify chain ID (dev config uses 9323310, create_genesis now uses config.chain_id)
        assert_eq!(genesis.config.chain_id, 9323310);

        // Verify accounts are prefunded
        assert!(!genesis.alloc.is_empty());
        assert_eq!(genesis.alloc.len(), 37); // 20 dev accounts + 4 system contracts + 5 ERC-4337/infra + 1 miner proxy + 3 governance + 4 Safe

        // Verify extra data contains signers
        assert!(genesis.extra_data.len() >= 32 + 65); // At least vanity + seal
    }

    #[test]
    fn test_production_genesis_creation() {
        let config = GenesisConfig::production();
        let genesis = create_genesis(config);

        // Verify production chain ID
        assert_eq!(genesis.config.chain_id, 9323310);

        // Verify: 8 prefunded accounts (5 signers + treasury + ops + community) + 4 system contracts + 5 ERC-4337/infra + 1 miner proxy + 3 governance + 4 Safe
        assert_eq!(genesis.alloc.len(), 25);

        // Verify gas limit is 60M
        assert_eq!(genesis.gas_limit, 60_000_000);

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
        let contract = genesis.alloc.get(&address!("000F3df6D732807Ef1319fB7B8bB8522d0Beac02"));
        assert!(contract.is_some());
        assert!(contract.unwrap().code.is_some());
        assert!(!contract.unwrap().code.as_ref().unwrap().is_empty());

        // EIP-2935 History Storage
        let contract = genesis.alloc.get(&address!("0000F90827F1C53a10cb7A02335B175320002935"));
        assert!(contract.is_some());
        assert!(contract.unwrap().code.is_some());

        // EIP-7002 Withdrawal Requests
        let contract = genesis.alloc.get(&address!("00000961Ef480Eb55e80D19ad83579A64c007002"));
        assert!(contract.is_some());
        assert!(contract.unwrap().code.is_some());

        // EIP-7251 Consolidation
        let contract = genesis.alloc.get(&address!("0000BBdDc7CE488642fb579F8B00f3a590007251"));
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

        // Write to file
        let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("sample-genesis.json");
        write_genesis_file(&genesis, &path).unwrap();
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
        assert!(proxy.storage.is_some(), "Miner proxy must have storage (admin slot)");

        // Admin should be first dev signer
        let storage = proxy.storage.as_ref().unwrap();
        assert!(storage.contains_key(&EIP1967_ADMIN_SLOT), "Must have EIP-1967 admin slot");
    }

    #[test]
    fn test_erc4337_contracts_in_genesis() {
        let genesis = create_dev_genesis();

        // EntryPoint v0.7 at canonical address
        let entrypoint = genesis
            .alloc
            .get(&address!("0000000071727De22E5E9d8BAf0edAc6f37da032"));
        assert!(entrypoint.is_some(), "EntryPoint v0.7 must be in genesis");
        assert!(entrypoint.unwrap().code.is_some(), "EntryPoint must have code");
        assert_eq!(entrypoint.unwrap().nonce, Some(1));

        // WETH9 at canonical mainnet address
        let weth = genesis
            .alloc
            .get(&address!("C02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"));
        assert!(weth.is_some(), "WETH9 must be in genesis");
        assert!(weth.unwrap().code.is_some(), "WETH9 must have code");
        assert!(weth.unwrap().storage.is_some(), "WETH9 must have initialized storage");
        let weth_storage = weth.unwrap().storage.as_ref().unwrap();
        assert_eq!(weth_storage.len(), 3, "WETH9 needs name, symbol, decimals slots");

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
        assert!(chain_config.storage.is_some(), "ChainConfig must have storage");
        let storage = chain_config.storage.as_ref().unwrap();
        // Should have slots for governance, gasLimit, blockTime, maxContractSize, calldataGasPerByte, maxTxGas
        assert!(storage.len() >= 6, "ChainConfig needs at least 6 storage slots");

        // SignerRegistry contract
        let signer_registry = genesis.alloc.get(&SIGNER_REGISTRY_ADDRESS);
        assert!(signer_registry.is_some(), "SignerRegistry must be in genesis");
        let signer_registry = signer_registry.unwrap();
        assert!(signer_registry.code.is_some(), "SignerRegistry must have code");
        assert_eq!(signer_registry.nonce, Some(1));
        assert!(signer_registry.storage.is_some(), "SignerRegistry must have storage");
        let storage = signer_registry.storage.as_ref().unwrap();
        // governance + signers.length + 3 signer array entries + 3 isSigner mapping entries + threshold
        assert!(storage.len() >= 8, "SignerRegistry needs at least 8 storage slots (got {})", storage.len());

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
        assert!(singleton.unwrap().code.is_some(), "Safe Singleton must have code");
        assert_eq!(singleton.unwrap().nonce, Some(1));

        // Safe Proxy Factory
        let factory = genesis.alloc.get(&SAFE_PROXY_FACTORY_ADDRESS);
        assert!(factory.is_some(), "Safe Proxy Factory must be in genesis");
        assert!(factory.unwrap().code.is_some(), "Safe Proxy Factory must have code");

        // Safe Fallback Handler
        let handler = genesis.alloc.get(&SAFE_FALLBACK_HANDLER_ADDRESS);
        assert!(handler.is_some(), "Safe Fallback Handler must be in genesis");
        assert!(handler.unwrap().code.is_some(), "Safe Fallback Handler must have code");

        // Safe MultiSend
        let multisend = genesis.alloc.get(&SAFE_MULTISEND_ADDRESS);
        assert!(multisend.is_some(), "Safe MultiSend must be in genesis");
        assert!(multisend.unwrap().code.is_some(), "Safe MultiSend must have code");
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
        assert_eq!(*admin_slot, B256::from(expected), "Miner proxy admin should be governance Safe");
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

        // slot 1: gasLimit = 30_000_000
        let slot1 = b256!("0000000000000000000000000000000000000000000000000000000000000001");
        assert_eq!(
            *storage.get(&slot1).unwrap(),
            B256::from(U256::from(30_000_000u64).to_be_bytes()),
            "ChainConfig slot 1 should be gas limit 30M"
        );

        // slot 2: blockTime = 2
        let slot2 = b256!("0000000000000000000000000000000000000000000000000000000000000002");
        assert_eq!(
            *storage.get(&slot2).unwrap(),
            B256::from(U256::from(2u64).to_be_bytes()),
            "ChainConfig slot 2 should be block time 2"
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
            assert!(alloc.is_some(), "Dev account {} should be in genesis", account);
            assert_eq!(
                alloc.unwrap().balance, expected_balance,
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
            assert_eq!(alloc.balance, signer_balance, "Signer {} should have 10,000 ETH", account);
        }

        // Account 5 (treasury): 2,500,000 ETH
        let treasury_balance = U256::from(2_500_000u64) * eth;
        assert_eq!(genesis.alloc.get(&dev_accounts()[5]).unwrap().balance, treasury_balance);

        // Account 6 (operations): 500,000 ETH
        let operations_balance = U256::from(500_000u64) * eth;
        assert_eq!(genesis.alloc.get(&dev_accounts()[6]).unwrap().balance, operations_balance);

        // Account 7 (community): 100,000 ETH
        let community_balance = U256::from(100_000u64) * eth;
        assert_eq!(genesis.alloc.get(&dev_accounts()[7]).unwrap().balance, community_balance);
    }

    #[test]
    fn test_dev_genesis_alloc_breakdown() {
        let genesis = create_dev_genesis();

        // Count categories
        let dev_count = dev_accounts().len(); // 20
        let system_contracts = 4; // EIP-4788, 2935, 7002, 7251
        let infra_contracts = 5; // EntryPoint, WETH9, Multicall3, CREATE2, SimpleAccountFactory
        let miner_proxy = 1;
        let governance = 3; // ChainConfig, SignerRegistry, Treasury
        let safe = 4; // Singleton, ProxyFactory, FallbackHandler, MultiSend

        let expected = dev_count + system_contracts + infra_contracts + miner_proxy + governance + safe;
        assert_eq!(expected, 37);
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

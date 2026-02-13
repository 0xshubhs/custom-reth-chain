// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

/// @title ChainConfig - On-chain tunable chain parameters
/// @notice Governance-controlled contract for dynamic chain configuration.
///         The node reads these values at block production time, allowing
///         parameter changes without node restart or recompilation.
contract ChainConfig {
    // ---- State ----
    address public governance;

    uint256 public gasLimit;           // Block gas limit (default: 30_000_000)
    uint256 public blockTime;          // Block interval in seconds (default: 2)
    uint256 public maxContractSize;    // Max contract bytecode size (default: 24_576)
    uint256 public calldataGasPerByte; // Calldata gas cost per byte (default: 16)
    uint256 public maxTxGas;           // Max gas per transaction (default: 30_000_000)
    bool    public eagerMining;        // Mine on tx arrival vs interval (default: false)

    // ---- Events ----
    event GovernanceTransferred(address indexed previous, address indexed newGovernance);
    event GasLimitUpdated(uint256 newLimit);
    event BlockTimeUpdated(uint256 newSeconds);
    event MaxContractSizeUpdated(uint256 newSize);
    event CalldataGasPerByteUpdated(uint256 newCost);
    event MaxTxGasUpdated(uint256 newMaxTxGas);
    event EagerMiningUpdated(bool enabled);

    // ---- Modifiers ----
    modifier onlyGovernance() {
        require(msg.sender == governance, "ChainConfig: not governance");
        _;
    }

    // ---- Constructor ----
    constructor(
        address _governance,
        uint256 _gasLimit,
        uint256 _blockTime,
        uint256 _maxContractSize,
        uint256 _calldataGasPerByte,
        uint256 _maxTxGas,
        bool _eagerMining
    ) {
        governance = _governance;
        gasLimit = _gasLimit;
        blockTime = _blockTime;
        maxContractSize = _maxContractSize;
        calldataGasPerByte = _calldataGasPerByte;
        maxTxGas = _maxTxGas;
        eagerMining = _eagerMining;
    }

    // ---- Setters ----

    function setGasLimit(uint256 _limit) external onlyGovernance {
        require(_limit >= 1_000_000, "ChainConfig: gas limit too low");
        gasLimit = _limit;
        emit GasLimitUpdated(_limit);
    }

    function setBlockTime(uint256 _seconds) external onlyGovernance {
        require(_seconds >= 1, "ChainConfig: block time too low");
        blockTime = _seconds;
        emit BlockTimeUpdated(_seconds);
    }

    function setMaxContractSize(uint256 _size) external onlyGovernance {
        require(_size >= 1024, "ChainConfig: contract size too low");
        maxContractSize = _size;
        emit MaxContractSizeUpdated(_size);
    }

    function setCalldataGasPerByte(uint256 _cost) external onlyGovernance {
        calldataGasPerByte = _cost;
        emit CalldataGasPerByteUpdated(_cost);
    }

    function setMaxTxGas(uint256 _maxTxGas) external onlyGovernance {
        require(_maxTxGas >= 21_000, "ChainConfig: max tx gas too low");
        maxTxGas = _maxTxGas;
        emit MaxTxGasUpdated(_maxTxGas);
    }

    function setEagerMining(bool _enabled) external onlyGovernance {
        eagerMining = _enabled;
        emit EagerMiningUpdated(_enabled);
    }

    function transferGovernance(address _newGovernance) external onlyGovernance {
        require(_newGovernance != address(0), "ChainConfig: zero address");
        emit GovernanceTransferred(governance, _newGovernance);
        governance = _newGovernance;
    }
}

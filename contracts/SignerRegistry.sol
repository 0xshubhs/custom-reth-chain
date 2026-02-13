// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

/// @title SignerRegistry - On-chain POA signer management
/// @notice Governance-controlled signer list for POA consensus.
///         The node reads this contract to determine authorized block producers.
///         Changes take effect at the next epoch block.
contract SignerRegistry {
    // ---- State ----
    address public governance;

    address[] public signers;
    mapping(address => bool) public isSigner;
    uint256 public signerThreshold; // Minimum signers for chain liveness

    // ---- Events ----
    event GovernanceTransferred(address indexed previous, address indexed newGovernance);
    event SignerAdded(address indexed signer);
    event SignerRemoved(address indexed signer);
    event ThresholdUpdated(uint256 newThreshold);

    // ---- Modifiers ----
    modifier onlyGovernance() {
        require(msg.sender == governance, "SignerRegistry: not governance");
        _;
    }

    // ---- Constructor ----
    constructor(
        address _governance,
        address[] memory _initialSigners,
        uint256 _threshold
    ) {
        require(_initialSigners.length >= _threshold, "SignerRegistry: insufficient signers");
        require(_threshold > 0, "SignerRegistry: zero threshold");

        governance = _governance;
        signerThreshold = _threshold;

        for (uint256 i = 0; i < _initialSigners.length; i++) {
            address s = _initialSigners[i];
            require(s != address(0), "SignerRegistry: zero address signer");
            require(!isSigner[s], "SignerRegistry: duplicate signer");
            signers.push(s);
            isSigner[s] = true;
            emit SignerAdded(s);
        }
    }

    // ---- Signer Management ----

    function addSigner(address _signer) external onlyGovernance {
        require(_signer != address(0), "SignerRegistry: zero address");
        require(!isSigner[_signer], "SignerRegistry: already a signer");
        signers.push(_signer);
        isSigner[_signer] = true;
        emit SignerAdded(_signer);
    }

    function removeSigner(address _signer) external onlyGovernance {
        require(isSigner[_signer], "SignerRegistry: not a signer");
        require(
            signers.length - 1 >= signerThreshold,
            "SignerRegistry: below threshold"
        );

        isSigner[_signer] = false;

        // Remove from array by swapping with last element
        for (uint256 i = 0; i < signers.length; i++) {
            if (signers[i] == _signer) {
                signers[i] = signers[signers.length - 1];
                signers.pop();
                break;
            }
        }
        emit SignerRemoved(_signer);
    }

    function setThreshold(uint256 _threshold) external onlyGovernance {
        require(_threshold > 0, "SignerRegistry: zero threshold");
        require(
            _threshold <= signers.length,
            "SignerRegistry: threshold exceeds signer count"
        );
        signerThreshold = _threshold;
        emit ThresholdUpdated(_threshold);
    }

    function transferGovernance(address _newGovernance) external onlyGovernance {
        require(_newGovernance != address(0), "SignerRegistry: zero address");
        emit GovernanceTransferred(governance, _newGovernance);
        governance = _newGovernance;
    }

    // ---- View Functions ----

    function getSigners() external view returns (address[] memory) {
        return signers;
    }

    function signerCount() external view returns (uint256) {
        return signers.length;
    }
}

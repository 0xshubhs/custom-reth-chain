// SPDX-License-Identifier: MIT
pragma solidity ^0.8.28;

/// @title Treasury - Fee distribution for POA chain
/// @notice Governance-controlled treasury that distributes block rewards
///         and transaction fees among signers, development, community, and burn.
///         The EIP-1967 Miner Proxy delegates to this contract.
contract Treasury {
    // ---- State ----
    address public governance;

    // Fee split ratios (basis points, total must be 10000)
    uint256 public signerShare;    // Default: 4000 (40%)
    uint256 public devShare;       // Default: 3000 (30%)
    uint256 public communityShare; // Default: 2000 (20%)
    uint256 public burnShare;      // Default: 1000 (10%)

    // Recipients
    address public devFund;
    address public communityFund;

    // Signer registry (to distribute signer share equally)
    address public signerRegistry;

    uint256 public constant BASIS_POINTS = 10000;

    // ---- Events ----
    event GovernanceTransferred(address indexed previous, address indexed newGovernance);
    event FeeSplitsUpdated(uint256 signer, uint256 dev, uint256 community, uint256 burn);
    event DevFundUpdated(address indexed newDevFund);
    event CommunityFundUpdated(address indexed newCommunityFund);
    event SignerRegistryUpdated(address indexed newRegistry);
    event Distributed(uint256 total, uint256 signerAmount, uint256 devAmount, uint256 communityAmount, uint256 burnAmount);
    event GrantSent(address indexed recipient, uint256 amount, string reason);

    // ---- Modifiers ----
    modifier onlyGovernance() {
        require(msg.sender == governance, "Treasury: not governance");
        _;
    }

    // ---- Constructor ----
    constructor(
        address _governance,
        address _devFund,
        address _communityFund,
        address _signerRegistry
    ) {
        governance = _governance;
        devFund = _devFund;
        communityFund = _communityFund;
        signerRegistry = _signerRegistry;
        signerShare = 4000;
        devShare = 3000;
        communityShare = 2000;
        burnShare = 1000;
    }

    // ---- Receive ETH ----
    receive() external payable {}

    // ---- Distribution ----

    /// @notice Distribute accumulated fees according to the configured splits.
    ///         Can be called by anyone (typically at epoch blocks by the node).
    function distribute() external {
        uint256 total = address(this).balance;
        require(total > 0, "Treasury: nothing to distribute");

        uint256 signerAmount = (total * signerShare) / BASIS_POINTS;
        uint256 devAmount = (total * devShare) / BASIS_POINTS;
        uint256 communityAmount = (total * communityShare) / BASIS_POINTS;
        uint256 burnAmount = total - signerAmount - devAmount - communityAmount;

        // Distribute to dev fund
        if (devAmount > 0 && devFund != address(0)) {
            (bool ok, ) = devFund.call{value: devAmount}("");
            require(ok, "Treasury: dev transfer failed");
        }

        // Distribute to community fund
        if (communityAmount > 0 && communityFund != address(0)) {
            (bool ok, ) = communityFund.call{value: communityAmount}("");
            require(ok, "Treasury: community transfer failed");
        }

        // Burn (send to zero address — effectively locked forever)
        if (burnAmount > 0) {
            (bool ok, ) = address(0xdead).call{value: burnAmount}("");
            require(ok, "Treasury: burn transfer failed");
        }

        // Distribute signer share equally among all signers
        if (signerAmount > 0 && signerRegistry != address(0)) {
            // Read signers from registry
            (bool ok, bytes memory data) = signerRegistry.staticcall(
                abi.encodeWithSignature("getSigners()")
            );
            if (ok && data.length > 0) {
                address[] memory currentSigners = abi.decode(data, (address[]));
                if (currentSigners.length > 0) {
                    uint256 perSigner = signerAmount / currentSigners.length;
                    for (uint256 i = 0; i < currentSigners.length; i++) {
                        if (perSigner > 0) {
                            (bool sent, ) = currentSigners[i].call{value: perSigner}("");
                            // Don't revert if one signer fails to receive
                            if (!sent) continue;
                        }
                    }
                }
            }
        }

        emit Distributed(total, signerAmount, devAmount, communityAmount, burnAmount);
    }

    // ---- Governance Setters ----

    function setFeeSplits(
        uint256 _signer,
        uint256 _dev,
        uint256 _community,
        uint256 _burn
    ) external onlyGovernance {
        require(
            _signer + _dev + _community + _burn == BASIS_POINTS,
            "Treasury: splits must total 10000"
        );
        signerShare = _signer;
        devShare = _dev;
        communityShare = _community;
        burnShare = _burn;
        emit FeeSplitsUpdated(_signer, _dev, _community, _burn);
    }

    function setDevFund(address _devFund) external onlyGovernance {
        devFund = _devFund;
        emit DevFundUpdated(_devFund);
    }

    function setCommunityFund(address _communityFund) external onlyGovernance {
        communityFund = _communityFund;
        emit CommunityFundUpdated(_communityFund);
    }

    function setSignerRegistry(address _signerRegistry) external onlyGovernance {
        signerRegistry = _signerRegistry;
        emit SignerRegistryUpdated(_signerRegistry);
    }

    function transferGovernance(address _newGovernance) external onlyGovernance {
        require(_newGovernance != address(0), "Treasury: zero address");
        emit GovernanceTransferred(governance, _newGovernance);
        governance = _newGovernance;
    }

    /// @notice Send a grant to a specific address (ecosystem funding)
    function sendGrant(address _recipient, uint256 _amount, string calldata _reason)
        external
        onlyGovernance
    {
        require(_recipient != address(0), "Treasury: zero address");
        require(_amount <= address(this).balance, "Treasury: insufficient balance");
        (bool ok, ) = _recipient.call{value: _amount}("");
        require(ok, "Treasury: grant transfer failed");
        emit GrantSent(_recipient, _amount, _reason);
    }
}

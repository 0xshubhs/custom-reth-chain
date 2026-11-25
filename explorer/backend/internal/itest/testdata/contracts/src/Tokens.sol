// SPDX-License-Identifier: MIT
// Test contracts for the explorer engine's anvil integration test. Never
// deployed anywhere but a throwaway anvil.
pragma solidity ^0.8.20;

import {ERC20} from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import {ERC721} from "@openzeppelin/contracts/token/ERC721/ERC721.sol";
import {ERC1155} from "@openzeppelin/contracts/token/ERC1155/ERC1155.sol";

contract TestToken is ERC20 {
    constructor(string memory name_, string memory symbol_, uint256 supply) ERC20(name_, symbol_) {
        _mint(msg.sender, supply);
    }

    function burn(uint256 amount) external {
        _burn(msg.sender, amount);
    }
}

contract TestNFT is ERC721 {
    constructor() ERC721("Test Deed", "DEED") {}

    function mint(address to, uint256 id) external {
        _mint(to, id);
    }

    function burn(uint256 id) external {
        _burn(id);
    }
}

contract TestMulti is ERC1155 {
    constructor() ERC1155("") {}

    function mint(address to, uint256 id, uint256 amount) external {
        _mint(to, id, amount, "");
    }

    function mintBatch(address to, uint256[] calldata ids, uint256[] calldata amounts) external {
        _mintBatch(to, ids, amounts, "");
    }
}

contract Sink {
    constructor() payable {}

    receive() external payable {}
}

contract Doomed {
    constructor() {}
}

// Factory creates contracts from inside a transaction (internal CREATE
// frames), forwards value to one of them, and makes a creation that is
// reverted so the explorer must not record it.
contract Factory {
    event Made(address token, address sink);

    function make(string calldata name_, uint256 supply) external payable returns (address) {
        TestToken tok = new TestToken(name_, "KID", supply);
        tok.transfer(msg.sender, supply);
        Sink sink = new Sink{value: msg.value}();
        try this.makeAndRevert() {} catch {}
        emit Made(address(tok), address(sink));
        return address(tok);
    }

    function makeAndRevert() external {
        new Doomed();
        revert("undone");
    }
}

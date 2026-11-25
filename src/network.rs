//! A real network's identity: `network.toml`, read by `--network <file>`.
//!
//! The built-in genesis configurations (`dev`, and `--production` without a
//! file) use the 20 well-known Hardhat/Anvil development keys as signers and
//! prefunded accounts. Their private keys are public, so anyone can sign
//! blocks and spend those funds. A network that holds real value is started
//! from this file instead; `scripts/setup-network.sh` writes it.
//!
//! The file decides the genesis block. Once the chain has started, changing
//! anything in it changes the genesis hash and the node refuses to open the
//! datadir (reth compares the stored genesis hash with the chain spec), so the
//! file is part of the chain and must be kept with its backups. Signers change
//! later through on-chain governance (SignerRegistry), never by editing it.
//!
//! ```toml
//! chain_id     = 9323310
//! block_period = 2              # seconds
//! gas_limit    = 1_000_000_000
//! epoch        = 30000
//! signers      = ["0x...", "0x..."]
//! governance   = "0x..."        # the chain admin: use a multisig (Safe) address
//! vanity       = "Meowchain"    # up to 32 bytes in the genesis extra data
//! zero_gas     = false
//!
//! [prefund]
//! "0x..." = "1000000eth"
//! ```

use crate::genesis::{dev_accounts, GenesisConfig, GOVERNANCE_SAFE_ADDRESS};
use alloy_primitives::{Address, U256};
use serde::Deserialize;
use std::{collections::BTreeMap, fs, path::Path};

/// Chain ids that belong to well-known public networks. A private network
/// reusing one would let wallets confuse the two and replay transactions.
const RESERVED_CHAIN_IDS: &[(u64, &str)] = &[
    (1, "Ethereum mainnet"),
    (10, "OP Mainnet"),
    (56, "BNB Smart Chain"),
    (100, "Gnosis"),
    (137, "Polygon PoS"),
    (8453, "Base"),
    (42161, "Arbitrum One"),
    (43114, "Avalanche C-Chain"),
    (11155111, "Sepolia"),
    (17000, "Holesky"),
    (560048, "Hoodi"),
    (31337, "Hardhat/Anvil local"),
    (1337, "Ganache/Geth dev"),
];

/// The parsed `network.toml`.
#[derive(Debug, Clone, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct NetworkFile {
    /// EIP-155 chain id.
    pub chain_id: u64,
    /// Block interval in seconds.
    #[serde(default = "default_block_period")]
    pub block_period: u64,
    /// Genesis block gas limit (ChainConfig can change it later on chain).
    #[serde(default = "default_gas_limit")]
    pub gas_limit: u64,
    /// Blocks between signer-list checkpoints.
    #[serde(default = "default_epoch")]
    pub epoch: u64,
    /// The addresses allowed to seal blocks at genesis.
    pub signers: Vec<Address>,
    /// The chain admin (see [`GenesisConfig::governance`]).
    pub governance: Address,
    /// Text placed in the 32-byte vanity of the genesis extra data.
    #[serde(default)]
    pub vanity: Option<String>,
    /// Base fee 0 and gas price 0 accepted.
    #[serde(default)]
    pub zero_gas: bool,
    /// Genesis balances: address to amount (`"1000000eth"`, `"1e24wei"`, or raw wei).
    #[serde(default)]
    pub prefund: BTreeMap<Address, String>,
}

const fn default_block_period() -> u64 {
    2
}
const fn default_gas_limit() -> u64 {
    1_000_000_000
}
const fn default_epoch() -> u64 {
    30_000
}

impl NetworkFile {
    /// Parses a network file's text.
    pub fn parse(text: &str) -> eyre::Result<Self> {
        toml::from_str(text).map_err(|e| eyre::eyre!("network file: {e}"))
    }

    /// Reads and parses a network file.
    pub fn load(path: &Path) -> eyre::Result<Self> {
        let text = fs::read_to_string(path)
            .map_err(|e| eyre::eyre!("cannot read network file {}: {e}", path.display()))?;
        Self::parse(&text).map_err(|e| eyre::eyre!("{}: {e}", path.display()))
    }

    /// Every reason this file must not start a network. Empty means it is fine.
    ///
    /// `allow_dev_keys` lets the public development accounts through, for
    /// rehearsals on a throwaway chain; never set it for a real network.
    pub fn problems(&self, allow_dev_keys: bool) -> Vec<String> {
        let mut out = Vec::new();
        let dev = dev_accounts();

        if self.chain_id == 0 {
            out.push("chain_id must not be 0".into());
        }
        if let Some((_, name)) = RESERVED_CHAIN_IDS
            .iter()
            .find(|(id, _)| *id == self.chain_id)
        {
            out.push(format!(
                "chain_id {} is {name}'s: pick an unused id (check chainlist.org)",
                self.chain_id
            ));
        }
        if self.block_period == 0 {
            out.push("block_period must be at least 1 second".into());
        }
        if self.gas_limit < 5_000_000 {
            out.push(format!(
                "gas_limit {} is too low to deploy contracts",
                self.gas_limit
            ));
        }
        if self.epoch == 0 {
            out.push("epoch must not be 0".into());
        }

        if self.signers.is_empty() {
            out.push("signers is empty: at least one address must seal blocks".into());
        }
        let mut seen = std::collections::BTreeSet::new();
        for s in &self.signers {
            if s.is_zero() {
                out.push("a signer is the zero address".into());
            }
            if !seen.insert(*s) {
                out.push(format!("signer {s} is listed twice"));
            }
            if !allow_dev_keys && dev.contains(s) {
                out.push(format!(
                    "signer {s} is a public development account (its private key is published): \
                     anyone could seal blocks"
                ));
            }
        }

        if self.governance.is_zero() {
            out.push("governance is the zero address".into());
        } else if self.governance == GOVERNANCE_SAFE_ADDRESS {
            out.push(format!(
                "governance {} is the reserved placeholder (no contract there): nobody could \
                 change the chain's settings or signers. Use your Safe multisig's address",
                self.governance
            ));
        } else if !allow_dev_keys && dev.contains(&self.governance) {
            out.push(format!(
                "governance {} is a public development account: anyone could take over the chain",
                self.governance
            ));
        }

        if let Some(v) = &self.vanity {
            if v.len() > 32 {
                out.push(format!("vanity is {} bytes; the limit is 32", v.len()));
            }
        }

        for (addr, amount) in &self.prefund {
            if let Err(e) = parse_balance(amount) {
                out.push(format!("prefund {addr}: {e}"));
            }
            if !allow_dev_keys && dev.contains(addr) {
                out.push(format!(
                    "prefund {addr} is a public development account: anyone could spend it"
                ));
            }
        }
        out
    }

    /// The genesis configuration this file describes. Call [`Self::problems`] first.
    pub fn genesis_config(&self) -> eyre::Result<GenesisConfig> {
        let mut vanity = [0u8; 32];
        if let Some(v) = &self.vanity {
            let bytes = v.as_bytes();
            if bytes.len() > 32 {
                return Err(eyre::eyre!("vanity is longer than 32 bytes"));
            }
            vanity[..bytes.len()].copy_from_slice(bytes);
        }
        let mut prefunded_accounts = BTreeMap::new();
        for (addr, amount) in &self.prefund {
            prefunded_accounts.insert(*addr, parse_balance(amount)?);
        }
        Ok(GenesisConfig {
            chain_id: self.chain_id,
            gas_limit: self.gas_limit,
            prefunded_accounts,
            signers: self.signers.clone(),
            block_period: self.block_period,
            epoch: self.epoch,
            vanity,
            governance: self.governance,
        })
    }
}

/// Parses a balance into wei.
///
/// Accepts `"1000000eth"`, `"1e25wei"`, `"1e25"`, or raw decimal wei. The
/// `eth` suffix multiplies by 10^18; `wei` or no suffix is raw wei.
/// Scientific notation is expanded exactly (no floating point).
pub fn parse_balance(s: &str) -> eyre::Result<U256> {
    let trimmed = s.trim().to_lowercase().replace('_', "");
    let (num_str, multiplier) = if let Some(stripped) = trimmed.strip_suffix("eth") {
        (
            stripped.trim().to_string(),
            U256::from(10u64).pow(U256::from(18u64)),
        )
    } else if let Some(stripped) = trimmed.strip_suffix("wei") {
        (stripped.trim().to_string(), U256::from(1u64))
    } else {
        (trimmed.clone(), U256::from(1u64))
    };

    let expanded = if let Some((mantissa, exp)) = num_str.split_once('e') {
        let exp: u32 = exp
            .parse()
            .map_err(|_| eyre::eyre!("bad exponent in balance: {s}"))?;
        let (int_part, frac_part) = match mantissa.split('.').collect::<Vec<_>>().as_slice() {
            [i] => (i.to_string(), String::new()),
            [i, f] => (i.to_string(), f.to_string()),
            _ => return Err(eyre::eyre!("bad mantissa in balance: {s}")),
        };
        let frac_len = frac_part.len() as u32;
        if exp < frac_len {
            return Err(eyre::eyre!("fractional wei not supported: {s}"));
        }
        format!(
            "{int_part}{frac_part}{}",
            "0".repeat((exp - frac_len) as usize)
        )
    } else {
        num_str
    };

    let base =
        U256::from_str_radix(&expanded, 10).map_err(|e| eyre::eyre!("bad balance '{s}': {e}"))?;
    base.checked_mul(multiplier)
        .ok_or_else(|| eyre::eyre!("balance '{s}' overflows 256 bits"))
}

#[cfg(test)]
mod tests {
    use super::*;
    use alloy_primitives::address;

    const GOOD: &str = r#"
chain_id     = 4242001
block_period = 2
signers      = ["0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222"]
governance   = "0x3333333333333333333333333333333333333333"
vanity       = "TestNet"

[prefund]
"0x4444444444444444444444444444444444444444" = "1000eth"
"#;

    #[test]
    fn a_good_file_parses_and_has_no_problems() {
        let n = NetworkFile::parse(GOOD).unwrap();
        assert_eq!(n.chain_id, 4242001);
        assert_eq!(n.gas_limit, 1_000_000_000);
        assert_eq!(n.epoch, 30_000);
        assert_eq!(n.signers.len(), 2);
        assert!(n.problems(false).is_empty(), "{:?}", n.problems(false));

        let g = n.genesis_config().unwrap();
        assert_eq!(
            g.governance,
            address!("3333333333333333333333333333333333333333")
        );
        assert_eq!(&g.vanity[..7], b"TestNet");
        assert_eq!(
            g.prefunded_accounts[&address!("4444444444444444444444444444444444444444")],
            U256::from(1000u64) * U256::from(10u64).pow(U256::from(18u64))
        );
    }

    #[test]
    fn the_genesis_puts_governance_in_every_admin_slot() {
        let n = NetworkFile::parse(GOOD).unwrap();
        let genesis = crate::genesis::create_genesis(n.genesis_config().unwrap());
        let gov = n.governance;
        let chain_config = &genesis.alloc[&crate::genesis::CHAIN_CONFIG_ADDRESS];
        let slot0 = chain_config.storage.as_ref().unwrap()[&alloy_primitives::B256::ZERO];
        assert_eq!(Address::from_slice(&slot0[12..]), gov);
        assert_eq!(genesis.config.chain_id, 4242001);
    }

    #[test]
    fn unknown_keys_are_rejected() {
        let text = format!("{GOOD}\n");
        let bad = text.replace("vanity", "vanityy");
        assert!(NetworkFile::parse(&bad).is_err());
    }

    #[test]
    fn public_dev_keys_are_refused_unless_allowed() {
        let text = GOOD
            .replace(
                "0x1111111111111111111111111111111111111111",
                "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
            )
            .replace(
                "0x3333333333333333333333333333333333333333",
                "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
            );
        let n = NetworkFile::parse(&text).unwrap();
        let p = n.problems(false);
        assert_eq!(p.len(), 2, "{p:?}");
        assert!(p
            .iter()
            .any(|m| m.contains("signer") && m.contains("public development")));
        assert!(p
            .iter()
            .any(|m| m.contains("governance") && m.contains("public development")));
        assert!(n.problems(true).is_empty());
    }

    #[test]
    fn placeholder_governance_and_reserved_chain_ids_are_refused() {
        let text = GOOD
            .replace("chain_id     = 4242001", "chain_id     = 1")
            .replace(
                "0x3333333333333333333333333333333333333333",
                "0x000000000000000000000000000000006F5AFE00",
            );
        let p = NetworkFile::parse(&text).unwrap().problems(true);
        assert!(p.iter().any(|m| m.contains("Ethereum mainnet")), "{p:?}");
        assert!(
            p.iter().any(|m| m.contains("reserved placeholder")),
            "{p:?}"
        );
    }

    #[test]
    fn empty_and_duplicate_signers_are_refused() {
        let dup = GOOD.replace(
            "0x2222222222222222222222222222222222222222",
            "0x1111111111111111111111111111111111111111",
        );
        assert!(NetworkFile::parse(&dup)
            .unwrap()
            .problems(false)
            .iter()
            .any(|m| m.contains("listed twice")));
        let none = GOOD.replace(
            r#"signers      = ["0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222"]"#,
            "signers = []",
        );
        assert!(NetworkFile::parse(&none)
            .unwrap()
            .problems(false)
            .iter()
            .any(|m| m.contains("signers is empty")));
    }

    #[test]
    fn balances_parse_exactly() {
        let eth = U256::from(10u64).pow(U256::from(18u64));
        assert_eq!(parse_balance("1eth").unwrap(), eth);
        assert_eq!(
            parse_balance("1_000eth").unwrap(),
            U256::from(1000u64) * eth
        );
        assert_eq!(parse_balance("1e18").unwrap(), eth);
        assert_eq!(
            parse_balance("1.5e18wei").unwrap(),
            U256::from(15u64) * eth / U256::from(10u64)
        );
        assert!(parse_balance("1.5e0").is_err());
        assert!(parse_balance("lots").is_err());
        assert!(parse_balance("1e80eth").is_err());
    }
}

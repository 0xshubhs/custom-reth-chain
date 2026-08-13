use alloy_primitives::U256;
use reth_chainspec::{ChainHardforks, ForkCondition, Hardfork};
use reth_ethereum_forks::EthereumHardfork;

/// Creates hardforks configuration that matches Ethereum mainnet.
/// This ensures full smart contract compatibility.
pub fn mainnet_compatible_hardforks() -> ChainHardforks {
    mainnet_compatible_hardforks_with(None)
}

/// Creates the mainnet-compatible hardfork schedule, optionally scheduling the
/// Osaka (Fusaka) fork at a future unix timestamp.
///
/// `osaka_time = None` → Osaka is not scheduled (chain stays on Prague rules).
/// `osaka_time = Some(t)` → Osaka activates for the first block whose timestamp
/// is `>= t`. On a LIVE chain `t` must be in the future and every node must be
/// restarted with the same value before `t` — the schedule is part of the
/// EIP-2124 fork id, so nodes with different schedules refuse to peer.
pub fn mainnet_compatible_hardforks_with(osaka_time: Option<u64>) -> ChainHardforks {
    // Enable all hardforks through Prague at genesis (block 0 / timestamp 0)
    // This gives you the latest Ethereum features immediately
    let mut forks = vec![
        // Block-based hardforks (all at block 0)
        (EthereumHardfork::Frontier.boxed(), ForkCondition::Block(0)),
        (EthereumHardfork::Homestead.boxed(), ForkCondition::Block(0)),
        (EthereumHardfork::Tangerine.boxed(), ForkCondition::Block(0)),
        (
            EthereumHardfork::SpuriousDragon.boxed(),
            ForkCondition::Block(0),
        ),
        (EthereumHardfork::Byzantium.boxed(), ForkCondition::Block(0)),
        (
            EthereumHardfork::Constantinople.boxed(),
            ForkCondition::Block(0),
        ),
        (
            EthereumHardfork::Petersburg.boxed(),
            ForkCondition::Block(0),
        ),
        (EthereumHardfork::Istanbul.boxed(), ForkCondition::Block(0)),
        (EthereumHardfork::Berlin.boxed(), ForkCondition::Block(0)),
        (EthereumHardfork::London.boxed(), ForkCondition::Block(0)),
        // The Merge - we use TTD of 0 since POA doesn't have proof of work
        (
            EthereumHardfork::Paris.boxed(),
            ForkCondition::TTD {
                activation_block_number: 0,
                fork_block: None,
                total_difficulty: U256::ZERO,
            },
        ),
        // Timestamp-based hardforks (all at timestamp 0)
        (
            EthereumHardfork::Shanghai.boxed(),
            ForkCondition::Timestamp(0),
        ),
        (
            EthereumHardfork::Cancun.boxed(),
            ForkCondition::Timestamp(0),
        ),
        (
            EthereumHardfork::Prague.boxed(),
            ForkCondition::Timestamp(0),
        ),
    ];
    if let Some(t) = osaka_time {
        forks.push((EthereumHardfork::Osaka.boxed(), ForkCondition::Timestamp(t)));
    }
    ChainHardforks::new(forks)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_osaka_not_scheduled_by_default() {
        let forks = mainnet_compatible_hardforks();
        assert_eq!(
            forks.fork(EthereumHardfork::Osaka),
            ForkCondition::Never,
            "Osaka must not activate unless explicitly scheduled"
        );
        assert!(forks.fork(EthereumHardfork::Prague).active_at_timestamp(0));
    }

    #[test]
    fn test_osaka_scheduled_at_timestamp() {
        let t = 1_900_000_000; // some future unix time
        let forks = mainnet_compatible_hardforks_with(Some(t));
        let osaka = forks.fork(EthereumHardfork::Osaka);
        assert!(!osaka.active_at_timestamp(t - 1));
        assert!(osaka.active_at_timestamp(t));
        assert!(osaka.active_at_timestamp(t + 1));
    }

    #[test]
    fn test_osaka_schedule_preserves_earlier_forks() {
        let forks = mainnet_compatible_hardforks_with(Some(1_900_000_000));
        assert!(forks.fork(EthereumHardfork::Frontier).active_at_block(0));
        assert!(forks.fork(EthereumHardfork::Cancun).active_at_timestamp(0));
        assert!(forks.fork(EthereumHardfork::Prague).active_at_timestamp(0));
    }
}

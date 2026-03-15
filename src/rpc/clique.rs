//! Clique RPC Namespace
//!
//! Implementation of the standard `clique_*` RPC API that tools like MetaMask
//! and Blockscout expect for Clique POA networks. Provides signer queries,
//! snapshot inspection, and local proposal management.

use alloy_primitives::{Address, B256};
use jsonrpsee::{core::RpcResult, proc_macros::rpc};
use std::collections::HashMap;
use std::sync::{Arc, RwLock};

use super::clique_types::*;
use crate::chainspec::PoaChainSpec;
use crate::signer::SignerManager;

/// The `clique_*` RPC namespace - standard Clique POA API.
///
/// Provides the methods that Ethereum tooling (MetaMask, Blockscout, etc.)
/// uses to interact with Clique POA networks.
#[rpc(server, namespace = "clique")]
pub trait CliqueApi {
    /// Returns the list of authorized signers at the current block.
    #[method(name = "getSigners")]
    async fn get_signers(&self) -> RpcResult<Vec<Address>>;

    /// Returns the list of authorized signers at a specific block hash.
    #[method(name = "getSignersAtHash")]
    async fn get_signers_at_hash(&self, hash: B256) -> RpcResult<Vec<Address>>;

    /// Returns a snapshot of the current clique state.
    #[method(name = "getSnapshot")]
    async fn get_snapshot(&self) -> RpcResult<CliqueSnapshot>;

    /// Returns a snapshot at a specific block hash.
    #[method(name = "getSnapshotAtHash")]
    async fn get_snapshot_at_hash(&self, hash: B256) -> RpcResult<CliqueSnapshot>;

    /// Propose a new signer (authorize=true) or remove an existing one (authorize=false).
    /// This is a local proposal; the vote is included in subsequent blocks signed by us.
    #[method(name = "propose")]
    async fn propose(&self, address: Address, authorize: bool) -> RpcResult<()>;

    /// Remove a previously proposed signer vote.
    #[method(name = "discard")]
    async fn discard(&self, address: Address) -> RpcResult<()>;

    /// Returns the current signing status/activity.
    #[method(name = "status")]
    async fn status(&self) -> RpcResult<CliqueStatus>;

    /// Returns all current proposals.
    #[method(name = "proposals")]
    async fn proposals(&self) -> RpcResult<CliqueProposals>;
}

/// Implementation of the `clique_*` RPC namespace.
pub struct CliqueRpc {
    chain_spec: Arc<PoaChainSpec>,
    /// Signer manager for checking local signer status.
    /// Reserved for future use in status enrichment and historical block lookups.
    #[allow(dead_code)]
    signer_manager: Arc<SignerManager>,
    /// Local proposals: address -> authorize (true=add, false=remove).
    /// Protected by `RwLock` for concurrent access from RPC handlers.
    proposals: Arc<RwLock<HashMap<Address, bool>>>,
}

impl CliqueRpc {
    /// Create a new CliqueRpc instance.
    pub fn new(chain_spec: Arc<PoaChainSpec>, signer_manager: Arc<SignerManager>) -> Self {
        Self {
            chain_spec,
            signer_manager,
            proposals: Arc::new(RwLock::new(HashMap::new())),
        }
    }

    /// Build a snapshot from the current chain state and local proposals.
    ///
    /// Uses `effective_signers()` to respect live on-chain governance changes
    /// (same as consensus and payload builder).
    fn current_snapshot(&self) -> CliqueSnapshot {
        let signers = self.chain_spec.effective_signers();
        let proposals = self.proposals.read().unwrap_or_else(|e| e.into_inner());

        // Build both `votes` and `tally` in a single pass over `proposals` to
        // avoid iterating the map twice.
        let mut votes = Vec::with_capacity(proposals.len());
        let mut tally = HashMap::with_capacity(proposals.len());
        for (addr, auth) in proposals.iter() {
            votes.push(CliqueVote {
                signer: Address::ZERO, // local node proposal
                address: *addr,
                authorize: *auth,
            });
            tally.insert(
                *addr,
                CliqueTally {
                    authorize: *auth,
                    votes: 1,
                },
            );
        }

        CliqueSnapshot {
            number: 0, // would need state provider for actual block number
            hash: B256::ZERO,
            signers,
            votes,
            tally,
        }
    }
}

#[async_trait::async_trait]
impl CliqueApiServer for CliqueRpc {
    async fn get_signers(&self) -> RpcResult<Vec<Address>> {
        Ok(self.chain_spec.effective_signers())
    }

    async fn get_signers_at_hash(&self, _hash: B256) -> RpcResult<Vec<Address>> {
        // For now, return current signers. Full implementation would look up
        // the signer list at the specific block hash via state provider.
        Ok(self.chain_spec.effective_signers())
    }

    async fn get_snapshot(&self) -> RpcResult<CliqueSnapshot> {
        Ok(self.current_snapshot())
    }

    async fn get_snapshot_at_hash(&self, _hash: B256) -> RpcResult<CliqueSnapshot> {
        // For now, return current snapshot. Full implementation would
        // reconstruct the snapshot at the specific block hash.
        Ok(self.current_snapshot())
    }

    async fn propose(&self, address: Address, authorize: bool) -> RpcResult<()> {
        let mut proposals = self.proposals.write().unwrap_or_else(|e| e.into_inner());
        proposals.insert(address, authorize);
        Ok(())
    }

    async fn discard(&self, address: Address) -> RpcResult<()> {
        let mut proposals = self.proposals.write().unwrap_or_else(|e| e.into_inner());
        proposals.remove(&address);
        Ok(())
    }

    async fn status(&self) -> RpcResult<CliqueStatus> {
        let signers = self.chain_spec.effective_signers();
        let mut sealers_activity = HashMap::new();
        for signer in &signers {
            sealers_activity.insert(
                *signer,
                SealerActivity {
                    signed: 0,
                    total: 0,
                },
            );
        }
        Ok(CliqueStatus {
            signer_count: signers.len(),
            num_blocks: 0,
            sealers_activity,
        })
    }

    async fn proposals(&self) -> RpcResult<CliqueProposals> {
        let proposals = self.proposals.read().unwrap_or_else(|e| e.into_inner());
        Ok(CliqueProposals {
            proposals: proposals.clone(),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::chainspec::{PoaChainSpec, PoaConfig};
    use crate::genesis;

    /// Create a dev chain spec with 3 signers for testing.
    fn test_chain_spec() -> Arc<PoaChainSpec> {
        let config = genesis::GenesisConfig::dev();
        let genesis = genesis::create_genesis(config);
        let poa_config = PoaConfig {
            period: 2,
            epoch: 30000,
            signers: genesis::dev_signers(),
        };
        Arc::new(PoaChainSpec::new(genesis, poa_config))
    }

    /// Create a production chain spec with 5 signers.
    fn production_chain_spec() -> Arc<PoaChainSpec> {
        let config = genesis::GenesisConfig::production();
        let genesis = genesis::create_genesis(config);
        let poa_config = PoaConfig {
            period: 12,
            epoch: 30000,
            signers: genesis::dev_accounts().into_iter().take(5).collect(),
        };
        Arc::new(PoaChainSpec::new(genesis, poa_config))
    }

    /// Create a chain spec with no signers (edge case).
    fn empty_signer_chain_spec() -> Arc<PoaChainSpec> {
        let config = genesis::GenesisConfig::dev();
        let genesis = genesis::create_genesis(config);
        let poa_config = PoaConfig {
            period: 2,
            epoch: 30000,
            signers: vec![],
        };
        Arc::new(PoaChainSpec::new(genesis, poa_config))
    }

    fn make_rpc(chain: Arc<PoaChainSpec>) -> CliqueRpc {
        let manager = Arc::new(SignerManager::new());
        CliqueRpc::new(chain, manager)
    }

    // ── getSigners tests ──

    #[tokio::test]
    async fn test_get_signers_returns_authorized_list() {
        let chain = test_chain_spec();
        let rpc = make_rpc(chain.clone());

        let signers = rpc.get_signers().await.unwrap();
        assert_eq!(signers.len(), 3);
        assert_eq!(signers, chain.signers());
    }

    #[tokio::test]
    async fn test_get_signers_production_five_signers() {
        let chain = production_chain_spec();
        let rpc = make_rpc(chain);

        let signers = rpc.get_signers().await.unwrap();
        assert_eq!(signers.len(), 5);
    }

    #[tokio::test]
    async fn test_get_signers_empty() {
        let chain = empty_signer_chain_spec();
        let rpc = make_rpc(chain);

        let signers = rpc.get_signers().await.unwrap();
        assert!(signers.is_empty());
    }

    #[tokio::test]
    async fn test_get_signers_at_hash_returns_current() {
        let chain = test_chain_spec();
        let rpc = make_rpc(chain.clone());

        let signers = rpc.get_signers_at_hash(B256::ZERO).await.unwrap();
        assert_eq!(signers.len(), 3);
        assert_eq!(signers, chain.signers());
    }

    // ── propose / discard lifecycle tests ──

    #[tokio::test]
    async fn test_propose_adds_proposal() {
        let rpc = make_rpc(test_chain_spec());
        let addr = Address::with_last_byte(0x42);

        rpc.propose(addr, true).await.unwrap();

        let proposals = rpc.proposals().await.unwrap();
        assert_eq!(proposals.proposals.len(), 1);
        assert_eq!(proposals.proposals.get(&addr), Some(&true));
    }

    #[tokio::test]
    async fn test_propose_deauthorize() {
        let rpc = make_rpc(test_chain_spec());
        let addr = Address::with_last_byte(0x42);

        rpc.propose(addr, false).await.unwrap();

        let proposals = rpc.proposals().await.unwrap();
        assert_eq!(proposals.proposals.get(&addr), Some(&false));
    }

    #[tokio::test]
    async fn test_propose_overwrite_existing() {
        let rpc = make_rpc(test_chain_spec());
        let addr = Address::with_last_byte(0x42);

        // First propose authorize
        rpc.propose(addr, true).await.unwrap();
        assert_eq!(
            rpc.proposals().await.unwrap().proposals.get(&addr),
            Some(&true)
        );

        // Overwrite with deauthorize
        rpc.propose(addr, false).await.unwrap();
        assert_eq!(
            rpc.proposals().await.unwrap().proposals.get(&addr),
            Some(&false)
        );
        // Still only one proposal
        assert_eq!(rpc.proposals().await.unwrap().proposals.len(), 1);
    }

    #[tokio::test]
    async fn test_discard_removes_proposal() {
        let rpc = make_rpc(test_chain_spec());
        let addr = Address::with_last_byte(0x42);

        rpc.propose(addr, true).await.unwrap();
        assert_eq!(rpc.proposals().await.unwrap().proposals.len(), 1);

        rpc.discard(addr).await.unwrap();
        assert!(rpc.proposals().await.unwrap().proposals.is_empty());
    }

    #[tokio::test]
    async fn test_discard_nonexistent_is_noop() {
        let rpc = make_rpc(test_chain_spec());
        let addr = Address::with_last_byte(0x42);

        // Discard something that was never proposed -- should succeed silently
        rpc.discard(addr).await.unwrap();
        assert!(rpc.proposals().await.unwrap().proposals.is_empty());
    }

    #[tokio::test]
    async fn test_multiple_proposals() {
        let rpc = make_rpc(test_chain_spec());
        let addr1 = Address::with_last_byte(0x01);
        let addr2 = Address::with_last_byte(0x02);
        let addr3 = Address::with_last_byte(0x03);

        rpc.propose(addr1, true).await.unwrap();
        rpc.propose(addr2, false).await.unwrap();
        rpc.propose(addr3, true).await.unwrap();

        let proposals = rpc.proposals().await.unwrap();
        assert_eq!(proposals.proposals.len(), 3);
        assert_eq!(proposals.proposals.get(&addr1), Some(&true));
        assert_eq!(proposals.proposals.get(&addr2), Some(&false));
        assert_eq!(proposals.proposals.get(&addr3), Some(&true));
    }

    // ── snapshot tests ──

    #[tokio::test]
    async fn test_snapshot_contains_signers() {
        let chain = test_chain_spec();
        let rpc = make_rpc(chain.clone());

        let snapshot = rpc.get_snapshot().await.unwrap();
        assert_eq!(snapshot.signers.len(), 3);
        assert_eq!(snapshot.signers, chain.signers());
        assert!(snapshot.votes.is_empty());
        assert!(snapshot.tally.is_empty());
    }

    #[tokio::test]
    async fn test_snapshot_includes_proposals_as_votes() {
        let rpc = make_rpc(test_chain_spec());
        let addr = Address::with_last_byte(0x42);

        rpc.propose(addr, true).await.unwrap();

        let snapshot = rpc.get_snapshot().await.unwrap();
        assert_eq!(snapshot.votes.len(), 1);
        assert_eq!(snapshot.votes[0].address, addr);
        assert!(snapshot.votes[0].authorize);
        assert_eq!(snapshot.votes[0].signer, Address::ZERO);

        // Tally should also reflect the proposal
        assert_eq!(snapshot.tally.len(), 1);
        let tally = snapshot.tally.get(&addr).unwrap();
        assert!(tally.authorize);
        assert_eq!(tally.votes, 1);
    }

    #[tokio::test]
    async fn test_snapshot_at_hash_returns_current() {
        let chain = test_chain_spec();
        let rpc = make_rpc(chain.clone());

        let snapshot = rpc.get_snapshot_at_hash(B256::ZERO).await.unwrap();
        assert_eq!(snapshot.signers.len(), 3);
    }

    #[tokio::test]
    async fn test_snapshot_empty_signers() {
        let rpc = make_rpc(empty_signer_chain_spec());

        let snapshot = rpc.get_snapshot().await.unwrap();
        assert!(snapshot.signers.is_empty());
        assert!(snapshot.votes.is_empty());
        assert!(snapshot.tally.is_empty());
    }

    // ── status tests ──

    #[tokio::test]
    async fn test_status_reports_signer_count() {
        let rpc = make_rpc(test_chain_spec());

        let status = rpc.status().await.unwrap();
        assert_eq!(status.signer_count, 3);
        assert_eq!(status.num_blocks, 0);
        assert_eq!(status.sealers_activity.len(), 3);
    }

    #[tokio::test]
    async fn test_status_empty_signers() {
        let rpc = make_rpc(empty_signer_chain_spec());

        let status = rpc.status().await.unwrap();
        assert_eq!(status.signer_count, 0);
        assert!(status.sealers_activity.is_empty());
    }

    #[tokio::test]
    async fn test_status_activity_initialized_to_zero() {
        let chain = test_chain_spec();
        let rpc = make_rpc(chain.clone());

        let status = rpc.status().await.unwrap();
        for signer in chain.signers() {
            let activity = status.sealers_activity.get(signer).unwrap();
            assert_eq!(activity.signed, 0);
            assert_eq!(activity.total, 0);
        }
    }

    // ── JSON serialization tests ──

    #[test]
    fn test_clique_snapshot_json_serialization() {
        let snapshot = CliqueSnapshot {
            number: 42,
            hash: B256::ZERO,
            signers: vec![Address::with_last_byte(0x01)],
            votes: vec![CliqueVote {
                signer: Address::ZERO,
                address: Address::with_last_byte(0x02),
                authorize: true,
            }],
            tally: HashMap::from([(
                Address::with_last_byte(0x02),
                CliqueTally {
                    authorize: true,
                    votes: 1,
                },
            )]),
        };

        let json = serde_json::to_string(&snapshot).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();

        // Verify camelCase field names
        assert!(parsed.get("number").is_some());
        assert!(parsed.get("hash").is_some());
        assert!(parsed.get("signers").is_some());
        assert!(parsed.get("votes").is_some());
        assert!(parsed.get("tally").is_some());

        // Verify values
        assert_eq!(parsed["number"], 42);
        assert_eq!(parsed["signers"].as_array().unwrap().len(), 1);
    }

    #[test]
    fn test_clique_vote_json_camel_case() {
        let vote = CliqueVote {
            signer: Address::ZERO,
            address: Address::with_last_byte(0x01),
            authorize: false,
        };

        let json = serde_json::to_string(&vote).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();

        assert!(parsed.get("signer").is_some());
        assert!(parsed.get("address").is_some());
        assert!(parsed.get("authorize").is_some());
        assert_eq!(parsed["authorize"], false);
    }

    #[test]
    fn test_clique_status_json_camel_case() {
        let status = CliqueStatus {
            signer_count: 3,
            num_blocks: 100,
            sealers_activity: HashMap::new(),
        };

        let json = serde_json::to_string(&status).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();

        // Verify camelCase
        assert!(parsed.get("signerCount").is_some());
        assert!(parsed.get("numBlocks").is_some());
        assert!(parsed.get("sealersActivity").is_some());

        assert_eq!(parsed["signerCount"], 3);
        assert_eq!(parsed["numBlocks"], 100);
    }

    #[test]
    fn test_sealer_activity_json_camel_case() {
        let activity = SealerActivity {
            signed: 10,
            total: 20,
        };

        let json = serde_json::to_string(&activity).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();

        assert!(parsed.get("signed").is_some());
        assert!(parsed.get("total").is_some());
        assert_eq!(parsed["signed"], 10);
        assert_eq!(parsed["total"], 20);
    }

    #[test]
    fn test_clique_proposals_json_serialization() {
        let mut proposals = HashMap::new();
        proposals.insert(Address::with_last_byte(0x01), true);
        proposals.insert(Address::with_last_byte(0x02), false);

        let resp = CliqueProposals { proposals };
        let json = serde_json::to_string(&resp).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();

        assert!(parsed.get("proposals").is_some());
        assert_eq!(parsed["proposals"].as_object().unwrap().len(), 2);
    }

    #[test]
    fn test_clique_proposals_default_empty() {
        let proposals = CliqueProposals::default();
        assert!(proposals.proposals.is_empty());

        let json = serde_json::to_string(&proposals).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();
        assert_eq!(parsed["proposals"].as_object().unwrap().len(), 0);
    }

    // ── effective_signers (live governance) tests ──

    #[tokio::test]
    async fn test_get_signers_uses_effective_signers() {
        let chain = test_chain_spec();
        // Update live signer cache to simulate on-chain governance change
        let new_signer = Address::with_last_byte(0xFF);
        chain.update_live_signers(vec![new_signer]);

        let rpc = make_rpc(chain);
        let signers = rpc.get_signers().await.unwrap();

        // Should return the live signer list, not the genesis one
        assert_eq!(signers.len(), 1);
        assert_eq!(signers[0], new_signer);
    }

    #[tokio::test]
    async fn test_snapshot_uses_effective_signers() {
        let chain = test_chain_spec();
        let new_signer = Address::with_last_byte(0xAB);
        chain.update_live_signers(vec![new_signer]);

        let rpc = make_rpc(chain);
        let snapshot = rpc.get_snapshot().await.unwrap();

        assert_eq!(snapshot.signers.len(), 1);
        assert_eq!(snapshot.signers[0], new_signer);
    }

    #[tokio::test]
    async fn test_status_uses_effective_signers() {
        let chain = test_chain_spec();
        let s1 = Address::with_last_byte(0x10);
        let s2 = Address::with_last_byte(0x20);
        chain.update_live_signers(vec![s1, s2]);

        let rpc = make_rpc(chain);
        let status = rpc.status().await.unwrap();

        assert_eq!(status.signer_count, 2);
        assert!(status.sealers_activity.contains_key(&s1));
        assert!(status.sealers_activity.contains_key(&s2));
    }

    // ── propose + snapshot interaction ──

    #[tokio::test]
    async fn test_propose_discard_lifecycle_reflected_in_snapshot() {
        let rpc = make_rpc(test_chain_spec());
        let addr1 = Address::with_last_byte(0x01);
        let addr2 = Address::with_last_byte(0x02);

        // Propose two addresses
        rpc.propose(addr1, true).await.unwrap();
        rpc.propose(addr2, false).await.unwrap();

        let snapshot = rpc.get_snapshot().await.unwrap();
        assert_eq!(snapshot.votes.len(), 2);
        assert_eq!(snapshot.tally.len(), 2);

        // Discard one
        rpc.discard(addr1).await.unwrap();

        let snapshot = rpc.get_snapshot().await.unwrap();
        assert_eq!(snapshot.votes.len(), 1);
        assert_eq!(snapshot.tally.len(), 1);
        assert!(snapshot.tally.contains_key(&addr2));
        assert!(!snapshot.tally.contains_key(&addr1));
    }

    // ── signer_manager integration ──

    #[tokio::test]
    async fn test_rpc_with_loaded_signer() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        manager
            .add_signer_from_hex(crate::signer::dev::DEV_PRIVATE_KEYS[0]).unwrap();

        let rpc = CliqueRpc::new(chain, manager);

        // Signers and status should still work
        let signers = rpc.get_signers().await.unwrap();
        assert_eq!(signers.len(), 3);

        let status = rpc.status().await.unwrap();
        assert_eq!(status.signer_count, 3);
    }
}

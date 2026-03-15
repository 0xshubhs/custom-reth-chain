//! Admin RPC Namespace
//!
//! Implements `admin_*` methods for node administration and a `admin_health`
//! endpoint designed for load balancers and monitoring systems.

use crate::chainspec::PoaChainSpec;
use crate::signer::SignerManager;
use jsonrpsee::{core::RpcResult, proc_macros::rpc};
use reth_chainspec::EthChainSpec;
use std::sync::Arc;
use std::time::Instant;
use tokio::sync::RwLock;

use super::admin_types::*;

/// The `admin_*` RPC namespace definition.
#[rpc(server, namespace = "admin")]
pub trait AdminApi {
    /// Returns detailed information about the running node.
    #[method(name = "nodeInfo")]
    async fn node_info(&self) -> RpcResult<AdminNodeInfo>;

    /// Returns a list of connected peers.
    #[method(name = "peers")]
    async fn peers(&self) -> RpcResult<Vec<AdminPeerInfo>>;

    /// Requests adding a new remote peer to the peer list.
    #[method(name = "addPeer")]
    async fn add_peer(&self, enode: String) -> RpcResult<bool>;

    /// Requests removal of a remote peer from the peer list.
    #[method(name = "removePeer")]
    async fn remove_peer(&self, enode: String) -> RpcResult<bool>;

    /// Returns health status for load balancers and monitoring.
    #[method(name = "health")]
    async fn health(&self) -> RpcResult<HealthStatus>;
}

/// Tracks locally managed peer state for the admin namespace.
#[derive(Debug)]
struct PeerState {
    /// Peers added via `admin_addPeer`.
    peers: Vec<AdminPeerInfo>,
}

impl PeerState {
    fn new() -> Self {
        Self { peers: Vec::new() }
    }
}

/// Implementation of the `admin_*` RPC namespace.
#[derive(Debug)]
pub struct AdminRpc {
    /// Chain specification for network/chain info.
    chain_spec: Arc<PoaChainSpec>,
    /// Signer manager for local signer status.
    signer_manager: Arc<SignerManager>,
    /// Timestamp when the node was started.
    start_time: Instant,
    /// Whether the node is in dev mode.
    dev_mode: bool,
    /// P2P listen port.
    p2p_port: u16,
    /// Locally tracked peer state.
    peer_state: RwLock<PeerState>,
}

impl AdminRpc {
    /// Create a new AdminRpc instance.
    pub fn new(
        chain_spec: Arc<PoaChainSpec>,
        signer_manager: Arc<SignerManager>,
        start_time: Instant,
        dev_mode: bool,
        p2p_port: u16,
    ) -> Self {
        Self {
            chain_spec,
            signer_manager,
            start_time,
            dev_mode,
            p2p_port,
            peer_state: RwLock::new(PeerState::new()),
        }
    }

    /// Parse an enode URL and extract the node ID.
    ///
    /// Expected format: `enode://<node-id>@<ip>:<port>`
    fn parse_enode_id(enode: &str) -> Option<String> {
        let stripped = enode.strip_prefix("enode://")?;
        let at_pos = stripped.find('@')?;
        Some(stripped[..at_pos].to_string())
    }

    /// Parse an enode URL and extract the remote address (ip:port).
    fn parse_enode_addr(enode: &str) -> Option<String> {
        let stripped = enode.strip_prefix("enode://")?;
        let at_pos = stripped.find('@')?;
        Some(stripped[at_pos + 1..].to_string())
    }
}

#[async_trait::async_trait]
impl AdminApiServer for AdminRpc {
    async fn node_info(&self) -> RpcResult<AdminNodeInfo> {
        let chain_id = self.chain_spec.inner().chain.id();
        let genesis_hash = format!("{:#x}", self.chain_spec.genesis_hash());
        let poa_config = self.chain_spec.poa_config();

        // Build the placeholder node-id once so it isn't allocated twice.
        let node_id = "0".repeat(128);
        Ok(AdminNodeInfo {
            enode: format!("enode://{}@127.0.0.1:{}", node_id, self.p2p_port),
            id: node_id,
            name: NODE_VERSION.to_string(),
            ip: "127.0.0.1".to_string(),
            ports: AdminPorts {
                discovery: self.p2p_port,
                listener: self.p2p_port,
            },
            listen_addr: format!("[::]:{}", self.p2p_port),
            protocols: AdminProtocols {
                eth: AdminEthProtocol {
                    network: chain_id,
                    difficulty: 1,
                    genesis: genesis_hash.clone(),
                    config: AdminChainConfig {
                        chain_id,
                        clique: AdminCliqueConfig {
                            period: poa_config.period,
                            epoch: poa_config.epoch,
                        },
                    },
                    head: genesis_hash,
                },
            },
        })
    }

    async fn peers(&self) -> RpcResult<Vec<AdminPeerInfo>> {
        let state = self.peer_state.read().await;
        Ok(state.peers.clone())
    }

    async fn add_peer(&self, enode: String) -> RpcResult<bool> {
        // Validate enode format
        let id = match Self::parse_enode_id(&enode) {
            Some(id) => id,
            None => return Ok(false),
        };

        let remote_addr = match Self::parse_enode_addr(&enode) {
            Some(addr) => addr,
            None => return Ok(false),
        };

        let mut state = self.peer_state.write().await;

        // Don't add duplicates
        if state.peers.iter().any(|p| p.enode == enode) {
            return Ok(true);
        }

        let peer = AdminPeerInfo {
            enode: enode.clone(),
            id,
            name: "unknown".to_string(),
            network: AdminPeerNetwork {
                local_address: format!("127.0.0.1:{}", self.p2p_port),
                remote_address: remote_addr,
            },
            protocols: std::collections::HashMap::from([(
                "eth".to_string(),
                self.chain_spec.inner().chain.id().to_string(),
            )]),
        };

        state.peers.push(peer);
        Ok(true)
    }

    async fn remove_peer(&self, enode: String) -> RpcResult<bool> {
        let mut state = self.peer_state.write().await;
        let len_before = state.peers.len();
        state.peers.retain(|p| p.enode != enode);
        Ok(state.peers.len() < len_before)
    }

    async fn health(&self) -> RpcResult<HealthStatus> {
        let local_signers = self.signer_manager.signer_addresses().await;
        let authorized_signers = self.chain_spec.effective_signers();
        let peer_count = self.peer_state.read().await.peers.len();
        let uptime = self.start_time.elapsed().as_secs();

        // A node is an active signer if any of its local signers are in the authorized set.
        let is_signer = local_signers
            .iter()
            .any(|addr| authorized_signers.contains(addr));

        // Health heuristic:
        // - In dev mode, always healthy (auto-mining, no peers needed).
        // - In production, healthy if we have at least one authorized signer loaded
        //   or are connected to at least one peer (i.e. can sync).
        let healthy = if self.dev_mode {
            true
        } else {
            is_signer || peer_count > 0
        };

        Ok(HealthStatus {
            healthy,
            syncing: false,
            block_number: 0, // Would come from chain tip in production wiring
            peer_count,
            signer_count: authorized_signers.len(),
            is_signer,
            uptime_seconds: uptime,
            version: NODE_VERSION.to_string(),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::chainspec::{PoaChainSpec, PoaConfig};
    use crate::genesis;
    use crate::signer::SignerManager;

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

    fn make_rpc(chain: Arc<PoaChainSpec>, manager: Arc<SignerManager>, dev: bool) -> AdminRpc {
        AdminRpc::new(chain, manager, Instant::now(), dev, 30303)
    }

    // --- admin_nodeInfo ---

    #[tokio::test]
    async fn test_admin_node_info_returns_chain_id() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = make_rpc(chain, manager, true);

        let info = rpc.node_info().await.unwrap();
        assert_eq!(info.protocols.eth.network, 9323310);
        assert_eq!(info.protocols.eth.config.chain_id, 9323310);
    }

    #[tokio::test]
    async fn test_admin_node_info_clique_config() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = make_rpc(chain, manager, true);

        let info = rpc.node_info().await.unwrap();
        assert_eq!(info.protocols.eth.config.clique.period, 2);
        assert_eq!(info.protocols.eth.config.clique.epoch, 30000);
    }

    #[tokio::test]
    async fn test_admin_node_info_name_and_ports() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = AdminRpc::new(chain, manager, Instant::now(), true, 31000);

        let info = rpc.node_info().await.unwrap();
        assert_eq!(info.name, NODE_VERSION);
        assert_eq!(info.ports.listener, 31000);
        assert_eq!(info.ports.discovery, 31000);
        assert!(info.listen_addr.contains("31000"));
    }

    #[tokio::test]
    async fn test_admin_node_info_genesis_hash_present() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = make_rpc(chain, manager, true);

        let info = rpc.node_info().await.unwrap();
        // Genesis hash should be a hex string starting with 0x
        assert!(info.protocols.eth.genesis.starts_with("0x"));
        assert_eq!(info.protocols.eth.genesis, info.protocols.eth.head);
    }

    // --- admin_peers ---

    #[tokio::test]
    async fn test_admin_peers_initially_empty() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = make_rpc(chain, manager, true);

        let peers = rpc.peers().await.unwrap();
        assert!(peers.is_empty());
    }

    // --- admin_addPeer / admin_removePeer ---

    #[tokio::test]
    async fn test_admin_add_peer_valid_enode() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = make_rpc(chain, manager, true);

        let enode = format!("enode://{}@192.168.1.1:30303", "ab".repeat(64));
        let result = rpc.add_peer(enode.clone()).await.unwrap();
        assert!(result);

        let peers = rpc.peers().await.unwrap();
        assert_eq!(peers.len(), 1);
        assert_eq!(peers[0].enode, enode);
        assert_eq!(peers[0].network.remote_address, "192.168.1.1:30303");
    }

    #[tokio::test]
    async fn test_admin_add_peer_invalid_enode_returns_false() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = make_rpc(chain, manager, true);

        // Missing enode:// prefix
        assert!(!rpc.add_peer("not-an-enode-url".to_string()).await.unwrap());

        // Missing @ separator
        assert!(!rpc
            .add_peer("enode://abcdef1234".to_string())
            .await
            .unwrap());

        // Peers should still be empty
        let peers = rpc.peers().await.unwrap();
        assert!(peers.is_empty());
    }

    #[tokio::test]
    async fn test_admin_add_peer_duplicate_is_noop() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = make_rpc(chain, manager, true);

        let enode = format!("enode://{}@10.0.0.1:30303", "cc".repeat(64));
        rpc.add_peer(enode.clone()).await.unwrap();
        rpc.add_peer(enode.clone()).await.unwrap();
        rpc.add_peer(enode.clone()).await.unwrap();

        let peers = rpc.peers().await.unwrap();
        assert_eq!(peers.len(), 1);
    }

    #[tokio::test]
    async fn test_admin_remove_peer_existing() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = make_rpc(chain, manager, true);

        let enode = format!("enode://{}@10.0.0.1:30303", "dd".repeat(64));
        rpc.add_peer(enode.clone()).await.unwrap();
        assert_eq!(rpc.peers().await.unwrap().len(), 1);

        let removed = rpc.remove_peer(enode).await.unwrap();
        assert!(removed);
        assert!(rpc.peers().await.unwrap().is_empty());
    }

    #[tokio::test]
    async fn test_admin_remove_peer_nonexistent_returns_false() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = make_rpc(chain, manager, true);

        let removed = rpc
            .remove_peer("enode://nonexistent@1.2.3.4:30303".to_string())
            .await
            .unwrap();
        assert!(!removed);
    }

    #[tokio::test]
    async fn test_admin_add_multiple_peers_then_remove_one() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = make_rpc(chain, manager, true);

        let enode_a = format!("enode://{}@10.0.0.1:30303", "aa".repeat(64));
        let enode_b = format!("enode://{}@10.0.0.2:30303", "bb".repeat(64));
        let enode_c = format!("enode://{}@10.0.0.3:30303", "cc".repeat(64));

        rpc.add_peer(enode_a.clone()).await.unwrap();
        rpc.add_peer(enode_b.clone()).await.unwrap();
        rpc.add_peer(enode_c.clone()).await.unwrap();
        assert_eq!(rpc.peers().await.unwrap().len(), 3);

        rpc.remove_peer(enode_b).await.unwrap();
        let peers = rpc.peers().await.unwrap();
        assert_eq!(peers.len(), 2);

        let enodes: Vec<&str> = peers.iter().map(|p| p.enode.as_str()).collect();
        assert!(enodes.contains(&enode_a.as_str()));
        assert!(enodes.contains(&enode_c.as_str()));
    }

    // --- admin_health ---

    #[tokio::test]
    async fn test_admin_health_dev_mode_always_healthy() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        // No signers loaded, no peers, but dev mode => healthy
        let rpc = make_rpc(chain, manager, true);

        let health = rpc.health().await.unwrap();
        assert!(health.healthy);
        assert!(!health.syncing);
        assert_eq!(health.peer_count, 0);
        assert_eq!(health.version, NODE_VERSION);
    }

    #[tokio::test]
    async fn test_admin_health_production_no_signer_no_peers_unhealthy() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        // Production mode, no signers, no peers => unhealthy
        let rpc = make_rpc(chain, manager, false);

        let health = rpc.health().await.unwrap();
        assert!(!health.healthy);
        assert!(!health.is_signer);
    }

    #[tokio::test]
    async fn test_admin_health_production_with_signer_is_healthy() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        // Add an authorized signer
        manager
            .add_signer_from_hex(crate::signer::dev::DEV_PRIVATE_KEYS[0])
            .await
            .unwrap();

        let rpc = make_rpc(chain, manager, false);
        let health = rpc.health().await.unwrap();
        assert!(health.healthy);
        assert!(health.is_signer);
        assert_eq!(health.signer_count, 3);
    }

    #[tokio::test]
    async fn test_admin_health_production_with_peers_is_healthy() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = make_rpc(chain, manager, false);

        // Add a peer so peer_count > 0
        let enode = format!("enode://{}@10.0.0.1:30303", "aa".repeat(64));
        rpc.add_peer(enode).await.unwrap();

        let health = rpc.health().await.unwrap();
        assert!(health.healthy);
        assert!(!health.is_signer);
        assert_eq!(health.peer_count, 1);
    }

    #[tokio::test]
    async fn test_admin_health_signer_count_reflects_effective_signers() {
        let chain = empty_signer_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = make_rpc(chain, manager, true);

        let health = rpc.health().await.unwrap();
        assert_eq!(health.signer_count, 0);
    }

    #[tokio::test]
    async fn test_admin_health_uptime_increases() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        // Use a start_time slightly in the past
        let start = Instant::now() - std::time::Duration::from_secs(42);
        let rpc = AdminRpc::new(chain, manager, start, true, 30303);

        let health = rpc.health().await.unwrap();
        assert!(health.uptime_seconds >= 42);
    }

    // --- serialization ---

    #[tokio::test]
    async fn test_admin_node_info_json_serialization() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = make_rpc(chain, manager, true);

        let info = rpc.node_info().await.unwrap();
        let json = serde_json::to_string(&info).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();

        // Verify camelCase field names
        assert!(parsed.get("enode").is_some());
        assert!(parsed.get("listenAddr").is_some());
        assert!(parsed["protocols"]["eth"].get("chainId").is_none()); // nested under config
        assert!(parsed["protocols"]["eth"]["config"]
            .get("chainId")
            .is_some());
        assert!(parsed["protocols"]["eth"]["config"]["clique"]
            .get("period")
            .is_some());
    }

    #[tokio::test]
    async fn test_admin_health_json_serialization() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = make_rpc(chain, manager, true);

        let health = rpc.health().await.unwrap();
        let json = serde_json::to_string(&health).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();

        assert!(parsed.get("healthy").is_some());
        assert!(parsed.get("syncing").is_some());
        assert!(parsed.get("blockNumber").is_some());
        assert!(parsed.get("peerCount").is_some());
        assert!(parsed.get("signerCount").is_some());
        assert!(parsed.get("isSigner").is_some());
        assert!(parsed.get("uptimeSeconds").is_some());
        assert!(parsed.get("version").is_some());
    }

    // --- helper parsing ---

    #[test]
    fn test_parse_enode_id() {
        let id = "ab".repeat(64);
        let enode = format!("enode://{}@192.168.1.1:30303", id);
        assert_eq!(AdminRpc::parse_enode_id(&enode), Some(id));
    }

    #[test]
    fn test_parse_enode_addr() {
        let enode = format!("enode://{}@192.168.1.1:30303", "ab".repeat(64));
        assert_eq!(
            AdminRpc::parse_enode_addr(&enode),
            Some("192.168.1.1:30303".to_string())
        );
    }

    #[test]
    fn test_parse_enode_invalid_prefix() {
        assert_eq!(AdminRpc::parse_enode_id("http://foo@bar"), None);
    }

    #[test]
    fn test_parse_enode_missing_at() {
        assert_eq!(AdminRpc::parse_enode_id("enode://no-at-sign"), None);
    }

    #[test]
    fn test_node_version_constant() {
        assert!(NODE_VERSION.starts_with("meowchain/"));
    }
}

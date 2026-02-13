//! Meow RPC Namespace
//!
//! Custom RPC methods for querying chain configuration, signer info,
//! and node status. Registered as the `meow_*` namespace.

use crate::chainspec::PoaChainSpec;
use crate::genesis::{
    CHAIN_CONFIG_ADDRESS, GOVERNANCE_SAFE_ADDRESS, SIGNER_REGISTRY_ADDRESS, TREASURY_ADDRESS,
};
use crate::signer::SignerManager;
use alloy_primitives::Address;
use jsonrpsee::{core::RpcResult, proc_macros::rpc};
use serde::Serialize;
use std::sync::Arc;

/// Response for `meow_chainConfig`
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ChainConfigResponse {
    pub chain_id: u64,
    pub gas_limit: u64,
    pub block_time: u64,
    pub epoch: u64,
    pub signer_count: usize,
    pub governance_safe: Address,
    pub chain_config_contract: Address,
    pub signer_registry_contract: Address,
    pub treasury_contract: Address,
}

/// Response for `meow_nodeInfo`
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct NodeInfoResponse {
    pub chain_id: u64,
    pub dev_mode: bool,
    pub signer_count: usize,
    pub local_signer_count: usize,
    pub local_signers: Vec<Address>,
    pub authorized_signers: Vec<Address>,
}

/// The `meow_*` RPC namespace definition.
#[rpc(server, namespace = "meow")]
pub trait MeowApi {
    /// Returns the current chain configuration parameters.
    #[method(name = "chainConfig")]
    async fn chain_config(&self) -> RpcResult<ChainConfigResponse>;

    /// Returns the list of authorized POA signers.
    #[method(name = "signers")]
    async fn signers(&self) -> RpcResult<Vec<Address>>;

    /// Returns node information including local signer status.
    #[method(name = "nodeInfo")]
    async fn node_info(&self) -> RpcResult<NodeInfoResponse>;
}

/// Implementation of the `meow_*` RPC namespace.
pub struct MeowRpc {
    chain_spec: Arc<PoaChainSpec>,
    signer_manager: Arc<SignerManager>,
    dev_mode: bool,
}

impl MeowRpc {
    /// Create a new MeowRpc instance.
    pub fn new(
        chain_spec: Arc<PoaChainSpec>,
        signer_manager: Arc<SignerManager>,
        dev_mode: bool,
    ) -> Self {
        Self { chain_spec, signer_manager, dev_mode }
    }
}

#[async_trait::async_trait]
impl MeowApiServer for MeowRpc {
    async fn chain_config(&self) -> RpcResult<ChainConfigResponse> {
        Ok(ChainConfigResponse {
            chain_id: self.chain_spec.inner().chain.id(),
            gas_limit: self.chain_spec.inner().genesis().gas_limit,
            block_time: self.chain_spec.block_period(),
            epoch: self.chain_spec.epoch(),
            signer_count: self.chain_spec.signers().len(),
            governance_safe: GOVERNANCE_SAFE_ADDRESS,
            chain_config_contract: CHAIN_CONFIG_ADDRESS,
            signer_registry_contract: SIGNER_REGISTRY_ADDRESS,
            treasury_contract: TREASURY_ADDRESS,
        })
    }

    async fn signers(&self) -> RpcResult<Vec<Address>> {
        Ok(self.chain_spec.signers().to_vec())
    }

    async fn node_info(&self) -> RpcResult<NodeInfoResponse> {
        let local_signers = self.signer_manager.signer_addresses().await;
        let authorized = self.chain_spec.signers();

        Ok(NodeInfoResponse {
            chain_id: self.chain_spec.inner().chain.id(),
            dev_mode: self.dev_mode,
            signer_count: authorized.len(),
            local_signer_count: local_signers.len(),
            local_signers,
            authorized_signers: authorized.to_vec(),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::chainspec::{PoaChainSpec, PoaConfig};
    use crate::genesis;

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

    #[tokio::test]
    async fn test_meow_chain_config() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = MeowRpc::new(chain, manager, true);

        let config = rpc.chain_config().await.unwrap();
        assert_eq!(config.chain_id, 9323310);
        assert_eq!(config.block_time, 2);
        assert_eq!(config.epoch, 30000);
        assert_eq!(config.signer_count, 3);
        assert_eq!(config.governance_safe, GOVERNANCE_SAFE_ADDRESS);
    }

    #[tokio::test]
    async fn test_meow_signers() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = MeowRpc::new(chain.clone(), manager, false);

        let signers = rpc.signers().await.unwrap();
        assert_eq!(signers.len(), 3);
        assert_eq!(signers, chain.signers());
    }

    #[tokio::test]
    async fn test_meow_node_info() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        // Add a signer key
        manager
            .add_signer_from_hex(crate::signer::dev::DEV_PRIVATE_KEYS[0])
            .await
            .unwrap();

        let rpc = MeowRpc::new(chain, manager, true);
        let info = rpc.node_info().await.unwrap();

        assert_eq!(info.chain_id, 9323310);
        assert!(info.dev_mode);
        assert_eq!(info.signer_count, 3);
        assert_eq!(info.local_signer_count, 1);
        assert_eq!(info.local_signers.len(), 1);
        assert_eq!(info.authorized_signers.len(), 3);
    }

    #[tokio::test]
    async fn test_meow_chain_config_production() {
        let chain = production_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = MeowRpc::new(chain, manager, false);

        let config = rpc.chain_config().await.unwrap();
        assert_eq!(config.chain_id, 9323310);
        assert_eq!(config.gas_limit, 60_000_000);
        assert_eq!(config.block_time, 12);
        assert_eq!(config.signer_count, 5);
        assert_eq!(config.epoch, 30000);
    }

    #[tokio::test]
    async fn test_meow_node_info_no_signers() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        // Don't add any signers
        let rpc = MeowRpc::new(chain, manager, false);
        let info = rpc.node_info().await.unwrap();

        assert_eq!(info.local_signer_count, 0);
        assert!(info.local_signers.is_empty());
        assert!(!info.dev_mode);
        assert_eq!(info.signer_count, 3); // Chain still has 3 authorized signers
    }

    #[tokio::test]
    async fn test_meow_node_info_multiple_signers() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        // Add all 3 dev signers
        for key in crate::signer::dev::DEV_PRIVATE_KEYS.iter().take(3) {
            manager.add_signer_from_hex(key).await.unwrap();
        }

        let rpc = MeowRpc::new(chain, manager, true);
        let info = rpc.node_info().await.unwrap();

        assert_eq!(info.local_signer_count, 3);
        assert_eq!(info.local_signers.len(), 3);
        assert_eq!(info.authorized_signers.len(), 3);
    }

    #[tokio::test]
    async fn test_meow_signers_empty() {
        let chain = empty_signer_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = MeowRpc::new(chain, manager, false);

        let signers = rpc.signers().await.unwrap();
        assert!(signers.is_empty());
    }

    #[tokio::test]
    async fn test_meow_chain_config_governance_addresses() {
        let chain = test_chain_spec();
        let manager = Arc::new(SignerManager::new());
        let rpc = MeowRpc::new(chain, manager, true);

        let config = rpc.chain_config().await.unwrap();
        assert_eq!(config.governance_safe, GOVERNANCE_SAFE_ADDRESS);
        assert_eq!(config.chain_config_contract, CHAIN_CONFIG_ADDRESS);
        assert_eq!(config.signer_registry_contract, SIGNER_REGISTRY_ADDRESS);
        assert_eq!(config.treasury_contract, TREASURY_ADDRESS);
    }

    #[test]
    fn test_chain_config_response_json_serialization() {
        let config = ChainConfigResponse {
            chain_id: 9323310,
            gas_limit: 30_000_000,
            block_time: 2,
            epoch: 30000,
            signer_count: 3,
            governance_safe: GOVERNANCE_SAFE_ADDRESS,
            chain_config_contract: CHAIN_CONFIG_ADDRESS,
            signer_registry_contract: SIGNER_REGISTRY_ADDRESS,
            treasury_contract: TREASURY_ADDRESS,
        };

        let json = serde_json::to_string(&config).unwrap();
        let parsed: serde_json::Value = serde_json::from_str(&json).unwrap();

        // Verify camelCase field names
        assert!(parsed.get("chainId").is_some());
        assert!(parsed.get("gasLimit").is_some());
        assert!(parsed.get("blockTime").is_some());
        assert!(parsed.get("signerCount").is_some());
        assert!(parsed.get("governanceSafe").is_some());
        assert!(parsed.get("chainConfigContract").is_some());
        assert!(parsed.get("signerRegistryContract").is_some());
        assert!(parsed.get("treasuryContract").is_some());

        // Verify values
        assert_eq!(parsed["chainId"], 9323310);
        assert_eq!(parsed["gasLimit"], 30_000_000);
        assert_eq!(parsed["blockTime"], 2);
    }
}

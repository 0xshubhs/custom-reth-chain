use alloy_primitives::{Address, Signature, B256};
use alloy_signer::Signer;
use alloy_signer_local::PrivateKeySigner;
use std::collections::HashMap;
use tokio::sync::RwLock;

use super::errors::SignerError;

/// Manages signing keys for POA block production
#[derive(Debug)]
pub struct SignerManager {
    /// Map of address to signer
    signers: RwLock<HashMap<Address, PrivateKeySigner>>,
}

impl SignerManager {
    /// Create a new signer manager
    pub fn new() -> Self {
        Self {
            signers: RwLock::new(HashMap::new()),
        }
    }

    /// Add a signer from a private key hex string
    pub async fn add_signer_from_hex(&self, private_key_hex: &str) -> Result<Address, SignerError> {
        let signer = private_key_hex
            .parse::<PrivateKeySigner>()
            .map_err(|_| SignerError::InvalidPrivateKey)?;

        let address = signer.address();
        self.signers.write().await.insert(address, signer);

        Ok(address)
    }

    /// Add a signer directly
    pub async fn add_signer(&self, signer: PrivateKeySigner) -> Address {
        let address = signer.address();
        self.signers.write().await.insert(address, signer);
        address
    }

    /// Check if we have a signer for the given address
    pub async fn has_signer(&self, address: &Address) -> bool {
        self.signers.read().await.contains_key(address)
    }

    /// Get all registered signer addresses
    pub async fn signer_addresses(&self) -> Vec<Address> {
        self.signers.read().await.keys().copied().collect()
    }

    /// Get the number of registered signers without allocating a Vec.
    pub async fn signer_count(&self) -> usize {
        self.signers.read().await.len()
    }

    /// Check if we have a signer for any address in the given set.
    /// Returns the first matching address, or None.
    /// Avoids allocating a Vec of all signer addresses.
    pub async fn first_signer_in(&self, authorized: &[Address]) -> Option<Address> {
        let signers = self.signers.read().await;
        authorized.iter().find(|a| signers.contains_key(*a)).copied()
    }

    /// Sign a message hash with the specified signer
    pub async fn sign_hash(&self, address: &Address, hash: B256) -> Result<Signature, SignerError> {
        let signers = self.signers.read().await;
        let signer = signers
            .get(address)
            .ok_or(SignerError::NoSignerForAddress(*address))?;

        signer
            .sign_hash(&hash)
            .await
            .map_err(|e| SignerError::SigningFailed(e.to_string()))
    }

    /// Remove a signer
    pub async fn remove_signer(&self, address: &Address) -> bool {
        self.signers.write().await.remove(address).is_some()
    }
}

impl Default for SignerManager {
    fn default() -> Self {
        Self::new()
    }
}

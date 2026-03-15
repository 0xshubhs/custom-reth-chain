use alloy_primitives::{Address, Signature, B256};
use alloy_signer::Signer;
use alloy_signer_local::PrivateKeySigner;
use std::collections::HashMap;
use std::sync::RwLock;

use super::errors::SignerError;

/// Manages signing keys for POA block production
#[derive(Debug)]
pub struct SignerManager {
    /// Map of address to signer
    signers: RwLock<HashMap<Address, PrivateKeySigner>>,
}

impl SignerManager {
    pub fn new() -> Self {
        Self {
            signers: RwLock::new(HashMap::new()),
        }
    }

    pub fn add_signer_from_hex(&self, private_key_hex: &str) -> Result<Address, SignerError> {
        let signer = private_key_hex
            .parse::<PrivateKeySigner>()
            .map_err(|_| SignerError::InvalidPrivateKey)?;
        let address = signer.address();
        self.signers.write().unwrap().insert(address, signer);
        Ok(address)
    }

    pub fn add_signer(&self, signer: PrivateKeySigner) -> Address {
        let address = signer.address();
        self.signers.write().unwrap().insert(address, signer);
        address
    }

    pub fn has_signer(&self, address: &Address) -> bool {
        self.signers.read().unwrap().contains_key(address)
    }

    pub fn signer_addresses(&self) -> Vec<Address> {
        self.signers.read().unwrap().keys().copied().collect()
    }

    pub fn signer_count(&self) -> usize {
        self.signers.read().unwrap().len()
    }

    pub fn first_signer_in(&self, authorized: &[Address]) -> Option<Address> {
        let signers = self.signers.read().unwrap();
        authorized.iter().find(|a| signers.contains_key(*a)).copied()
    }

    /// The only async method: alloy's `Signer` trait requires `.await` for sign_hash.
    /// The lock is released before awaiting (signer is cloned out).
    pub async fn sign_hash(&self, address: &Address, hash: B256) -> Result<Signature, SignerError> {
        let signer = self
            .signers
            .read()
            .unwrap()
            .get(address)
            .cloned()
            .ok_or(SignerError::NoSignerForAddress(*address))?;
        signer
            .sign_hash(&hash)
            .await
            .map_err(|e| SignerError::SigningFailed(e.to_string()))
    }

    pub fn remove_signer(&self, address: &Address) -> bool {
        self.signers.write().unwrap().remove(address).is_some()
    }
}

impl Default for SignerManager {
    fn default() -> Self {
        Self::new()
    }
}

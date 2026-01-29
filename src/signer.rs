//! Block Signer Implementation
//!
//! This module provides utilities for signing POA blocks, including:
//! - Key management for authorized signers
//! - Block sealing (signing)
//! - Signature verification

use alloy_consensus::Header;
use alloy_primitives::{keccak256, Address, Signature, B256};
use alloy_signer::Signer;
use alloy_signer_local::PrivateKeySigner;
use std::collections::HashMap;
use std::sync::Arc;
use thiserror::Error;
use tokio::sync::RwLock;

/// Errors that can occur during signing operations
#[derive(Debug, Error)]
pub enum SignerError {
    /// No signing key available for the specified address
    #[error("No signer available for address {0}")]
    NoSignerForAddress(Address),

    /// Signing operation failed
    #[error("Signing failed: {0}")]
    SigningFailed(String),

    /// Invalid private key format
    #[error("Invalid private key")]
    InvalidPrivateKey,
}

/// Manages signing keys for POA block production
#[derive(Debug)]
pub struct SignerManager {
    /// Map of address to signer
    signers: RwLock<HashMap<Address, PrivateKeySigner>>,
}

impl SignerManager {
    /// Create a new signer manager
    pub fn new() -> Self {
        Self { signers: RwLock::new(HashMap::new()) }
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

    /// Sign a message hash with the specified signer
    pub async fn sign_hash(
        &self,
        address: &Address,
        hash: B256,
    ) -> Result<Signature, SignerError> {
        let signers = self.signers.read().await;
        let signer =
            signers.get(address).ok_or_else(|| SignerError::NoSignerForAddress(*address))?;

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

/// Block sealing utilities for POA
#[derive(Debug)]
pub struct BlockSealer {
    signer_manager: Arc<SignerManager>,
}

impl BlockSealer {
    /// Create a new block sealer
    pub fn new(signer_manager: Arc<SignerManager>) -> Self {
        Self { signer_manager }
    }

    /// Calculate the seal hash for a header (hash without signature)
    pub fn seal_hash(header: &Header) -> B256 {
        // Create a copy with signature stripped from extra data
        let mut header_for_hash = header.clone();

        const EXTRA_SEAL_LENGTH: usize = 65;
        let extra_data = &header.extra_data;
        if extra_data.len() >= EXTRA_SEAL_LENGTH {
            let without_seal = &extra_data[..extra_data.len() - EXTRA_SEAL_LENGTH];
            header_for_hash.extra_data = without_seal.to_vec().into();
        }

        keccak256(alloy_rlp::encode(&header_for_hash))
    }

    /// Seal a block header with a signature
    pub async fn seal_header(
        &self,
        mut header: Header,
        signer_address: &Address,
    ) -> Result<Header, SignerError> {
        // Calculate seal hash
        let seal_hash = Self::seal_hash(&header);

        // Sign the hash
        let signature = self.signer_manager.sign_hash(signer_address, seal_hash).await?;

        // Encode signature as bytes (r, s, v)
        let sig_bytes = signature_to_bytes(&signature);

        // Update extra data with signature
        let mut extra_data = header.extra_data.to_vec();

        // Remove existing signature if present
        const EXTRA_SEAL_LENGTH: usize = 65;
        if extra_data.len() >= EXTRA_SEAL_LENGTH {
            extra_data.truncate(extra_data.len() - EXTRA_SEAL_LENGTH);
        }

        // Append new signature
        extra_data.extend_from_slice(&sig_bytes);
        header.extra_data = extra_data.into();

        Ok(header)
    }

    /// Verify a block's signature
    pub fn verify_signature(header: &Header) -> Result<Address, SignerError> {
        let seal_hash = Self::seal_hash(header);

        let extra_data = &header.extra_data;
        const EXTRA_SEAL_LENGTH: usize = 65;

        if extra_data.len() < EXTRA_SEAL_LENGTH {
            return Err(SignerError::SigningFailed("Extra data too short".into()));
        }

        let sig_bytes = &extra_data[extra_data.len() - EXTRA_SEAL_LENGTH..];
        let signature =
            bytes_to_signature(sig_bytes).map_err(|e| SignerError::SigningFailed(e))?;

        signature
            .recover_address_from_prehash(&seal_hash)
            .map_err(|e| SignerError::SigningFailed(e.to_string()))
    }
}

/// Convert a signature to bytes (r || s || v)
fn signature_to_bytes(sig: &Signature) -> [u8; 65] {
    let mut bytes = [0u8; 65];
    bytes[..32].copy_from_slice(&sig.r().to_be_bytes::<32>());
    bytes[32..64].copy_from_slice(&sig.s().to_be_bytes::<32>());
    bytes[64] = sig.v() as u8;
    bytes
}

/// Convert bytes to a signature
fn bytes_to_signature(bytes: &[u8]) -> Result<Signature, String> {
    if bytes.len() != 65 {
        return Err(format!("Invalid signature length: expected 65, got {}", bytes.len()));
    }

    Signature::try_from(bytes).map_err(|e| format!("Invalid signature: {}", e))
}

/// Development signer setup with known test keys
pub mod dev {
    use super::*;

    /// Private keys for the dev accounts (from "test test..." mnemonic)
    pub const DEV_PRIVATE_KEYS: &[&str] = &[
        "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
        "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d",
        "5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a",
        "7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6",
        "47e179ec197488593b187f80a00eb0da91f1b9d0b13f8733639f19c30a34926a",
        "8b3a350cf5c34c9194ca85829a2df0ec3153be0318b5e2d3348e872092edffba",
        "92db14e403b83dfe3df233f83dfa3a0d7096f21ca9b0d6d6b8d88b2b4ec1564e",
        "4bbbf85ce3377467afe5d46f804f221813b2bb87f24d81f60f1fcdbf7cbf4356",
        "dbda1821b80551c9d65939329250298aa3472ba22feea921c0cf5d620ea67b97",
        "2a871d0798f97d79848a013d4936a73bf4cc922c825d33c1cf7073dff6d409c6",
    ];

    /// Set up the signer manager with dev keys
    pub async fn setup_dev_signers() -> Arc<SignerManager> {
        let manager = Arc::new(SignerManager::new());

        for key in DEV_PRIVATE_KEYS.iter().take(3) {
            // Use first 3 as default signers
            manager
                .add_signer_from_hex(key)
                .await
                .expect("Dev keys should be valid");
        }

        manager
    }

    /// Get the first dev signer for testing
    pub fn first_dev_signer() -> PrivateKeySigner {
        DEV_PRIVATE_KEYS[0]
            .parse()
            .expect("First dev key should be valid")
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn test_signer_manager() {
        let manager = SignerManager::new();

        // Add a dev signer
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0])
            .await
            .unwrap();

        assert!(manager.has_signer(&address).await);
        assert_eq!(manager.signer_addresses().await.len(), 1);
    }

    #[tokio::test]
    async fn test_sign_and_verify() {
        let manager = Arc::new(SignerManager::new());
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0])
            .await
            .unwrap();

        let sealer = BlockSealer::new(manager);

        // Create a test header
        let header = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 12345,
            extra_data: vec![0u8; 32 + 65].into(), // Vanity + space for seal
            ..Default::default()
        };

        // Seal the header
        let sealed = sealer.seal_header(header, &address).await.unwrap();

        // Verify the signature
        let recovered = BlockSealer::verify_signature(&sealed).unwrap();
        assert_eq!(recovered, address);
    }

    #[tokio::test]
    async fn test_dev_signers_setup() {
        let manager = dev::setup_dev_signers().await;
        let addresses = manager.signer_addresses().await;

        assert_eq!(addresses.len(), 3);

        // Verify addresses match expected dev accounts
        let expected_first = crate::genesis::dev_accounts()[0];
        assert!(addresses.contains(&expected_first));
    }
}

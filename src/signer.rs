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

    #[tokio::test]
    async fn test_remove_signer() {
        let manager = SignerManager::new();
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0])
            .await
            .unwrap();

        assert!(manager.has_signer(&address).await);
        assert!(manager.remove_signer(&address).await);
        assert!(!manager.has_signer(&address).await);
        // Removing again should return false
        assert!(!manager.remove_signer(&address).await);
    }

    #[tokio::test]
    async fn test_sign_hash_nonexistent_address() {
        let manager = SignerManager::new();
        let fake_addr: Address = "0x0000000000000000000000000000000000000099".parse().unwrap();
        let hash = B256::ZERO;

        let result = manager.sign_hash(&fake_addr, hash).await;
        assert!(result.is_err());
        match result.unwrap_err() {
            SignerError::NoSignerForAddress(addr) => assert_eq!(addr, fake_addr),
            other => panic!("Expected NoSignerForAddress, got {:?}", other),
        }
    }

    #[tokio::test]
    async fn test_multiple_signers() {
        let manager = SignerManager::new();

        let addr1 = manager.add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0]).await.unwrap();
        let addr2 = manager.add_signer_from_hex(dev::DEV_PRIVATE_KEYS[1]).await.unwrap();
        let addr3 = manager.add_signer_from_hex(dev::DEV_PRIVATE_KEYS[2]).await.unwrap();

        assert_ne!(addr1, addr2);
        assert_ne!(addr2, addr3);
        assert_eq!(manager.signer_addresses().await.len(), 3);
        assert!(manager.has_signer(&addr1).await);
        assert!(manager.has_signer(&addr2).await);
        assert!(manager.has_signer(&addr3).await);
    }

    #[tokio::test]
    async fn test_add_signer_invalid_key() {
        let manager = SignerManager::new();
        let result = manager.add_signer_from_hex("not_a_valid_hex_key").await;
        assert!(result.is_err());
        match result.unwrap_err() {
            SignerError::InvalidPrivateKey => {}
            other => panic!("Expected InvalidPrivateKey, got {:?}", other),
        }
    }

    #[tokio::test]
    async fn test_seal_header_different_signers_produce_different_signatures() {
        let manager = Arc::new(SignerManager::new());
        let addr1 = manager.add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0]).await.unwrap();
        let addr2 = manager.add_signer_from_hex(dev::DEV_PRIVATE_KEYS[1]).await.unwrap();

        let sealer = BlockSealer::new(manager);

        let header = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 12345,
            extra_data: vec![0u8; 32 + 65].into(),
            ..Default::default()
        };

        let sealed1 = sealer.seal_header(header.clone(), &addr1).await.unwrap();
        let sealed2 = sealer.seal_header(header, &addr2).await.unwrap();

        // Different signers should produce different signatures
        assert_ne!(sealed1.extra_data, sealed2.extra_data);

        // But both should verify correctly
        assert_eq!(BlockSealer::verify_signature(&sealed1).unwrap(), addr1);
        assert_eq!(BlockSealer::verify_signature(&sealed2).unwrap(), addr2);
    }

    #[test]
    fn test_verify_signature_short_extra_data() {
        let header = Header {
            extra_data: vec![0u8; 10].into(), // Too short
            ..Default::default()
        };
        let result = BlockSealer::verify_signature(&header);
        assert!(result.is_err());
    }

    #[test]
    fn test_signature_to_bytes_roundtrip() {
        // Create a known signature and round-trip it
        let mut bytes = [0u8; 65];
        bytes[0] = 0x01; // r first byte
        bytes[32] = 0x02; // s first byte
        bytes[64] = 0x00; // v = 0

        let sig = bytes_to_signature(&bytes);
        assert!(sig.is_ok());
        let sig = sig.unwrap();

        let recovered_bytes = signature_to_bytes(&sig);
        assert_eq!(bytes[64], recovered_bytes[64]); // v should match
    }

    #[test]
    fn test_first_dev_signer() {
        let signer = dev::first_dev_signer();
        let expected_addr = crate::genesis::dev_accounts()[0];
        assert_eq!(signer.address(), expected_addr);
    }

    #[tokio::test]
    async fn test_add_signer_directly() {
        let manager = SignerManager::new();
        let signer = dev::first_dev_signer();
        let expected_addr = signer.address();

        let addr = manager.add_signer(signer).await;
        assert_eq!(addr, expected_addr);
        assert!(manager.has_signer(&addr).await);
    }

    #[test]
    fn test_signer_manager_default() {
        let manager = SignerManager::default();
        // Default should be empty
        // Can't check async easily in sync test, but at least it shouldn't panic
        drop(manager);
    }

    #[tokio::test]
    async fn test_concurrent_sign_operations() {
        let manager = Arc::new(SignerManager::new());
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0])
            .await
            .unwrap();

        let mut handles = vec![];
        for i in 0..10u64 {
            let mgr = manager.clone();
            let addr = address;
            handles.push(tokio::spawn(async move {
                let hash = keccak256(i.to_be_bytes());
                mgr.sign_hash(&addr, hash).await.unwrap()
            }));
        }

        let mut results = Vec::new();
        for handle in handles {
            results.push(handle.await.unwrap());
        }

        // All 10 signing operations should succeed
        assert_eq!(results.len(), 10);
        // Different inputs should produce different signatures
        let unique: std::collections::HashSet<_> = results.iter().map(|s| format!("{:?}", s)).collect();
        assert_eq!(unique.len(), 10);
    }

    #[tokio::test]
    async fn test_sign_with_all_dev_signers() {
        let manager = dev::setup_dev_signers().await;
        let addresses = manager.signer_addresses().await;
        let sealer = BlockSealer::new(manager);

        let header = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 12345,
            extra_data: vec![0u8; 32 + 65].into(),
            ..Default::default()
        };

        let mut signatures = vec![];
        for addr in &addresses {
            let signed = sealer.seal_header(header.clone(), addr).await.unwrap();
            let recovered = BlockSealer::verify_signature(&signed).unwrap();
            assert_eq!(recovered, *addr, "Recovered address should match signer");
            signatures.push(signed.extra_data.to_vec());
        }

        // All 3 signers should produce different signatures
        assert_ne!(signatures[0], signatures[1]);
        assert_ne!(signatures[1], signatures[2]);
        assert_ne!(signatures[0], signatures[2]);
    }

    #[test]
    fn test_seal_hash_deterministic() {
        let header = Header {
            number: 42,
            gas_limit: 30_000_000,
            timestamp: 99999,
            extra_data: vec![0u8; 32 + 65].into(),
            ..Default::default()
        };

        let hash1 = BlockSealer::seal_hash(&header);
        let hash2 = BlockSealer::seal_hash(&header);
        let hash3 = BlockSealer::seal_hash(&header);

        assert_eq!(hash1, hash2);
        assert_eq!(hash2, hash3);
    }

    #[test]
    fn test_sign_different_headers_different_hashes() {
        let header1 = Header {
            number: 1,
            extra_data: vec![0u8; 32 + 65].into(),
            ..Default::default()
        };
        let header2 = Header {
            number: 2,
            extra_data: vec![0u8; 32 + 65].into(),
            ..Default::default()
        };
        let header3 = Header {
            number: 3,
            extra_data: vec![0u8; 32 + 65].into(),
            ..Default::default()
        };

        let hash1 = BlockSealer::seal_hash(&header1);
        let hash2 = BlockSealer::seal_hash(&header2);
        let hash3 = BlockSealer::seal_hash(&header3);

        assert_ne!(hash1, hash2);
        assert_ne!(hash2, hash3);
        assert_ne!(hash1, hash3);
    }

    #[tokio::test]
    async fn test_add_all_ten_dev_keys() {
        let manager = SignerManager::new();
        let mut addresses = vec![];

        for key in dev::DEV_PRIVATE_KEYS.iter() {
            let addr = manager.add_signer_from_hex(key).await.unwrap();
            addresses.push(addr);
        }

        assert_eq!(addresses.len(), 10);
        assert_eq!(manager.signer_addresses().await.len(), 10);

        // All addresses should be unique
        let unique: std::collections::HashSet<_> = addresses.iter().collect();
        assert_eq!(unique.len(), 10);
    }

    #[tokio::test]
    async fn test_remove_and_re_add_signer() {
        let manager = SignerManager::new();
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0])
            .await
            .unwrap();

        assert!(manager.has_signer(&address).await);
        assert!(manager.remove_signer(&address).await);
        assert!(!manager.has_signer(&address).await);

        // Re-add the same key
        let re_added = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0])
            .await
            .unwrap();
        assert_eq!(address, re_added);
        assert!(manager.has_signer(&address).await);
    }

    #[tokio::test]
    async fn test_sign_after_remove_fails() {
        let manager = SignerManager::new();
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0])
            .await
            .unwrap();

        // Sign should work
        let hash = B256::ZERO;
        assert!(manager.sign_hash(&address, hash).await.is_ok());

        // Remove the signer
        manager.remove_signer(&address).await;

        // Sign should fail after removal
        let result = manager.sign_hash(&address, hash).await;
        assert!(result.is_err());
        match result.unwrap_err() {
            SignerError::NoSignerForAddress(addr) => assert_eq!(addr, address),
            other => panic!("Expected NoSignerForAddress, got {:?}", other),
        }
    }

    #[tokio::test]
    async fn test_empty_manager_signer_addresses() {
        let manager = SignerManager::new();
        let addresses = manager.signer_addresses().await;
        assert!(addresses.is_empty());
    }
}

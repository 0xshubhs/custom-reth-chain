//! Block Signer Implementation
//!
//! This module provides utilities for signing POA blocks, including:
//! - Key management for authorized signers
//! - Block sealing (signing)
//! - Signature verification

pub mod dev;
pub mod errors;
pub mod manager;
pub mod sealer;

pub use errors::SignerError;
pub use manager::SignerManager;
pub use sealer::{bytes_to_signature, signature_to_bytes, BlockSealer};

#[cfg(test)]
mod tests {
    use super::*;
    use alloy_consensus::Header;
    use alloy_primitives::{keccak256, Address, B256};
    use std::sync::Arc;

    #[tokio::test]
    async fn test_signer_manager() {
        let manager = SignerManager::new();

        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0]).unwrap();

        assert!(manager.has_signer(&address));
        assert_eq!(manager.signer_addresses().len(), 1);
    }

    #[tokio::test]
    async fn test_sign_and_verify() {
        let manager = Arc::new(SignerManager::new());
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0]).unwrap();

        let sealer = BlockSealer::new(manager);

        let header = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 12345,
            extra_data: vec![0u8; 32 + 65].into(),
            ..Default::default()
        };

        let sealed = sealer.seal_header(header, &address).await.unwrap();
        let recovered = BlockSealer::verify_signature(&sealed).unwrap();
        assert_eq!(recovered, address);
    }

    #[tokio::test]
    async fn test_dev_signers_setup() {
        let manager = dev::setup_dev_signers().await;
        let addresses = manager.signer_addresses();

        assert_eq!(addresses.len(), 3);

        let expected_first = crate::genesis::dev_accounts()[0];
        assert!(addresses.contains(&expected_first));
    }

    #[tokio::test]
    async fn test_remove_signer() {
        let manager = SignerManager::new();
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0]).unwrap();

        assert!(manager.has_signer(&address));
        assert!(manager.remove_signer(&address));
        assert!(!manager.has_signer(&address));
        assert!(!manager.remove_signer(&address));
    }

    #[tokio::test]
    async fn test_sign_hash_nonexistent_address() {
        let manager = SignerManager::new();
        let fake_addr: Address = "0x0000000000000000000000000000000000000099"
            .parse()
            .unwrap();
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

        let addr1 = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0]).unwrap();
        let addr2 = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[1]).unwrap();
        let addr3 = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[2]).unwrap();

        assert_ne!(addr1, addr2);
        assert_ne!(addr2, addr3);
        assert_eq!(manager.signer_addresses().len(), 3);
        assert!(manager.has_signer(&addr1));
        assert!(manager.has_signer(&addr2));
        assert!(manager.has_signer(&addr3));
    }

    #[tokio::test]
    async fn test_add_signer_invalid_key() {
        let manager = SignerManager::new();
        let result = manager.add_signer_from_hex("not_a_valid_hex_key");
        assert!(result.is_err());
        match result.unwrap_err() {
            SignerError::InvalidPrivateKey => {}
            other => panic!("Expected InvalidPrivateKey, got {:?}", other),
        }
    }

    #[tokio::test]
    async fn test_seal_header_different_signers_produce_different_signatures() {
        let manager = Arc::new(SignerManager::new());
        let addr1 = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0]).unwrap();
        let addr2 = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[1]).unwrap();

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

        assert_ne!(sealed1.extra_data, sealed2.extra_data);
        assert_eq!(BlockSealer::verify_signature(&sealed1).unwrap(), addr1);
        assert_eq!(BlockSealer::verify_signature(&sealed2).unwrap(), addr2);
    }

    #[test]
    fn test_verify_signature_short_extra_data() {
        let header = Header {
            extra_data: vec![0u8; 10].into(),
            ..Default::default()
        };
        let result = BlockSealer::verify_signature(&header);
        assert!(result.is_err());
    }

    #[test]
    fn test_signature_to_bytes_roundtrip() {
        let mut bytes = [0u8; 65];
        bytes[0] = 0x01;
        bytes[32] = 0x02;
        bytes[64] = 0x00;

        let sig = bytes_to_signature(&bytes);
        assert!(sig.is_ok());
        let sig = sig.unwrap();

        let recovered_bytes = signature_to_bytes(&sig);
        assert_eq!(bytes[64], recovered_bytes[64]);
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

        let addr = manager.add_signer(signer);
        assert_eq!(addr, expected_addr);
        assert!(manager.has_signer(&addr));
    }

    #[test]
    fn test_signer_manager_default() {
        let manager = SignerManager::default();
        drop(manager);
    }

    #[tokio::test]
    async fn test_concurrent_sign_operations() {
        let manager = Arc::new(SignerManager::new());
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0]).unwrap();

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

        assert_eq!(results.len(), 10);
        let unique: std::collections::HashSet<_> =
            results.iter().map(|s| format!("{:?}", s)).collect();
        assert_eq!(unique.len(), 10);
    }

    #[tokio::test]
    async fn test_sign_with_all_dev_signers() {
        let manager = dev::setup_dev_signers().await;
        let addresses = manager.signer_addresses();
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
            let addr = manager.add_signer_from_hex(key).unwrap();
            addresses.push(addr);
        }

        assert_eq!(addresses.len(), 10);
        assert_eq!(manager.signer_addresses().len(), 10);

        let unique: std::collections::HashSet<_> = addresses.iter().collect();
        assert_eq!(unique.len(), 10);
    }

    #[tokio::test]
    async fn test_remove_and_re_add_signer() {
        let manager = SignerManager::new();
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0]).unwrap();

        assert!(manager.has_signer(&address));
        assert!(manager.remove_signer(&address));
        assert!(!manager.has_signer(&address));

        let re_added = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0]).unwrap();
        assert_eq!(address, re_added);
        assert!(manager.has_signer(&address));
    }

    #[tokio::test]
    async fn test_sign_after_remove_fails() {
        let manager = SignerManager::new();
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0]).unwrap();

        let hash = B256::ZERO;
        assert!(manager.sign_hash(&address, hash).await.is_ok());

        manager.remove_signer(&address);

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
        let addresses = manager.signer_addresses();
        assert!(addresses.is_empty());
    }
}

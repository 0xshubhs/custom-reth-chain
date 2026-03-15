use alloy_consensus::Header;
use alloy_primitives::{keccak256, Address, Signature, B256};
use std::sync::Arc;

use super::errors::SignerError;
use super::manager::SignerManager;

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
    #[inline]
    pub fn seal_hash(header: &Header) -> B256 {
        // Clone the header struct, then strip the trailing 65-byte signature from
        // extra_data using Bytes::slice so the truncated view shares the same
        // underlying buffer (arc bump, O(1)) rather than allocating a new Vec.
        let mut header_for_hash = header.clone();

        const EXTRA_SEAL_LENGTH: usize = 65;
        let extra_len = header.extra_data.len();
        if extra_len >= EXTRA_SEAL_LENGTH {
            header_for_hash.extra_data = header.extra_data.slice(..extra_len - EXTRA_SEAL_LENGTH);
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
        let signature = self
            .signer_manager
            .sign_hash(signer_address, seal_hash)
            .await?;

        // Encode signature as bytes (r, s, v)
        let sig_bytes = signature_to_bytes(&signature);

        // Update extra data with signature.
        // Pre-size the Vec to the final length (vanity prefix + 65-byte sig) to
        // avoid a second reallocation from extend_from_slice.
        const EXTRA_SEAL_LENGTH: usize = 65;
        let prefix_len = if header.extra_data.len() >= EXTRA_SEAL_LENGTH {
            header.extra_data.len() - EXTRA_SEAL_LENGTH
        } else {
            header.extra_data.len()
        };
        let mut extra_data = Vec::with_capacity(prefix_len + EXTRA_SEAL_LENGTH);
        extra_data.extend_from_slice(&header.extra_data[..prefix_len]);
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
        let signature = bytes_to_signature(sig_bytes)?;

        signature
            .recover_address_from_prehash(&seal_hash)
            .map_err(|e| SignerError::SigningFailed(e.to_string()))
    }
}

/// Convert a signature to bytes (r || s || v)
#[inline]
pub fn signature_to_bytes(sig: &Signature) -> [u8; 65] {
    let mut bytes = [0u8; 65];
    bytes[..32].copy_from_slice(&sig.r().to_be_bytes::<32>());
    bytes[32..64].copy_from_slice(&sig.s().to_be_bytes::<32>());
    bytes[64] = sig.v() as u8;
    bytes
}

/// Convert bytes to a signature
pub fn bytes_to_signature(bytes: &[u8]) -> Result<Signature, SignerError> {
    if bytes.len() != 65 {
        return Err(SignerError::SigningFailed(format!(
            "Invalid signature length: expected 65, got {}",
            bytes.len()
        )));
    }

    Signature::try_from(bytes)
        .map_err(|e| SignerError::SigningFailed(format!("Invalid signature: {}", e)))
}

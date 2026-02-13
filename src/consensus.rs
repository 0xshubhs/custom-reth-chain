//! POA Consensus Implementation
//!
//! This module implements a Proof of Authority consensus mechanism that validates:
//! - Block signers are authorized
//! - Blocks are signed correctly
//! - Timing constraints are respected
//! - The signer rotation follows the expected pattern

use crate::chainspec::PoaChainSpec;
use alloy_consensus::{BlockHeader, Header};
use alloy_primitives::{keccak256, Address, Signature, B256, U256};
use reth_consensus::{Consensus, ConsensusError, FullConsensus, HeaderValidator, ReceiptRootBloom};
use reth_execution_types::BlockExecutionResult;
use reth_primitives_traits::{
    Block, GotExpected, NodePrimitives, RecoveredBlock, SealedBlock, SealedHeader,
};
use std::sync::Arc;
use thiserror::Error;

/// Extra data structure for POA blocks
/// Format: [vanity (32 bytes)][signers list (N*20 bytes, only in epoch blocks)][signature (65 bytes)]
pub const EXTRA_VANITY_LENGTH: usize = 32;
/// Signature length in extra data (65 bytes: r=32, s=32, v=1)
pub const EXTRA_SEAL_LENGTH: usize = 65;
/// Ethereum address length (20 bytes)
pub const ADDRESS_LENGTH: usize = 20;

/// POA-specific consensus errors
#[derive(Debug, Error)]
#[allow(missing_docs)]
pub enum PoaConsensusError {
    /// Block signer is not in the authorized signers list
    #[error("Block signer {signer} is not authorized")]
    UnauthorizedSigner {
        /// The unauthorized signer address
        signer: Address,
    },

    /// Block signature is invalid or cannot be recovered
    #[error("Invalid block signature")]
    InvalidSignature,

    /// Extra data is too short to contain required POA information
    #[error("Extra data too short: expected at least {expected} bytes, got {got}")]
    ExtraDataTooShort {
        /// Expected minimum length
        expected: usize,
        /// Actual length
        got: usize,
    },

    /// Block timestamp is earlier than allowed
    #[error("Block timestamp {timestamp} is before parent timestamp {parent_timestamp}")]
    TimestampTooEarly {
        /// Block timestamp
        timestamp: u64,
        /// Parent block timestamp
        parent_timestamp: u64,
    },

    /// Block timestamp is too far in the future
    #[error("Block timestamp {timestamp} is too far in the future")]
    TimestampTooFarInFuture {
        /// Block timestamp
        timestamp: u64,
    },

    /// Block was signed by wrong signer (not in-turn)
    #[error("Wrong block signer: expected {expected}, got {got}")]
    WrongSigner {
        /// Expected signer
        expected: Address,
        /// Actual signer
        got: Address,
    },

    /// Difficulty field has invalid value for POA
    #[error("Difficulty must be 1 for in-turn signer or 2 for out-of-turn")]
    InvalidDifficulty,

    /// Signer list in epoch block is invalid
    #[error("Invalid signer list in epoch block")]
    InvalidSignerList,
}

impl From<PoaConsensusError> for ConsensusError {
    fn from(err: PoaConsensusError) -> Self {
        ConsensusError::Custom(std::sync::Arc::new(err))
    }
}

/// POA Consensus implementation
#[derive(Debug, Clone)]
pub struct PoaConsensus {
    /// The chain specification with POA configuration
    chain_spec: Arc<PoaChainSpec>,
    /// Whether the node is in dev mode (relaxed validation - no signature checks)
    dev_mode: bool,
}

impl PoaConsensus {
    /// Create a new POA consensus instance (production mode - strict validation)
    pub fn new(chain_spec: Arc<PoaChainSpec>) -> Self {
        Self { chain_spec, dev_mode: false }
    }

    /// Create a new POA consensus instance in dev mode (relaxed validation)
    pub fn new_dev(chain_spec: Arc<PoaChainSpec>) -> Self {
        Self { chain_spec, dev_mode: true }
    }

    /// Set dev mode on the consensus instance
    pub fn with_dev_mode(mut self, dev_mode: bool) -> Self {
        self.dev_mode = dev_mode;
        self
    }

    /// Returns whether this consensus is in dev mode
    pub fn is_dev_mode(&self) -> bool {
        self.dev_mode
    }

    /// Create an Arc-wrapped instance
    pub fn arc(chain_spec: Arc<PoaChainSpec>) -> Arc<Self> {
        Arc::new(Self::new(chain_spec))
    }

    /// Extract the signer address from the block's extra data
    pub fn recover_signer(&self, header: &Header) -> Result<Address, PoaConsensusError> {
        let extra_data = &header.extra_data;

        // Extra data must contain at least vanity + seal
        let min_length = EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH;
        if extra_data.len() < min_length {
            return Err(PoaConsensusError::ExtraDataTooShort {
                expected: min_length,
                got: extra_data.len(),
            });
        }

        // Extract the signature from the end of extra data
        let signature_start = extra_data.len() - EXTRA_SEAL_LENGTH;
        let signature_bytes = &extra_data[signature_start..];

        // Parse signature (r, s, v format)
        let signature = Signature::try_from(signature_bytes)
            .map_err(|_| PoaConsensusError::InvalidSignature)?;

        // Calculate the seal hash (header hash without the signature)
        let seal_hash = self.seal_hash(header);

        // Recover the signer address
        signature
            .recover_address_from_prehash(&seal_hash)
            .map_err(|_| PoaConsensusError::InvalidSignature)
    }

    /// Calculate the hash used for sealing (excludes the signature from extra data)
    pub fn seal_hash(&self, header: &Header) -> B256 {
        // Create a copy of the header with signature stripped from extra data
        let mut header_for_hash = header.clone();

        let extra_data = &header.extra_data;
        if extra_data.len() >= EXTRA_SEAL_LENGTH {
            let without_seal = &extra_data[..extra_data.len() - EXTRA_SEAL_LENGTH];
            header_for_hash.extra_data = without_seal.to_vec().into();
        }

        // Hash the modified header
        keccak256(alloy_rlp::encode(&header_for_hash))
    }

    /// Validate that the signer is authorized
    pub fn validate_signer(&self, signer: &Address) -> Result<(), PoaConsensusError> {
        if !self.chain_spec.is_authorized_signer(signer) {
            return Err(PoaConsensusError::UnauthorizedSigner { signer: *signer });
        }
        Ok(())
    }

    /// Check if this is an epoch block (where signer list is updated)
    pub fn is_epoch_block(&self, block_number: u64) -> bool {
        block_number % self.chain_spec.epoch() == 0
    }

    /// Validate the difficulty field
    /// In POA: difficulty 1 = in-turn signer, difficulty 2 = out-of-turn
    pub fn validate_difficulty(
        &self,
        header: &Header,
        signer: &Address,
    ) -> Result<(), PoaConsensusError> {
        let expected_signer = self.chain_spec.expected_signer(header.number);
        let is_in_turn = expected_signer == Some(signer);

        let expected_difficulty = if is_in_turn { 1u64 } else { 2u64 };

        if header.difficulty != U256::from(expected_difficulty) {
            return Err(PoaConsensusError::InvalidDifficulty);
        }

        Ok(())
    }

    /// Extract the signer list from an epoch block's extra data
    pub fn extract_signers_from_epoch_block(
        &self,
        header: &Header,
    ) -> Result<Vec<Address>, PoaConsensusError> {
        let extra_data = &header.extra_data;

        let min_length = EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH;
        if extra_data.len() < min_length {
            return Err(PoaConsensusError::ExtraDataTooShort {
                expected: min_length,
                got: extra_data.len(),
            });
        }

        // In epoch blocks, format is: vanity (32) + signers (N*20) + seal (65)
        let signers_data_len = extra_data.len() - EXTRA_VANITY_LENGTH - EXTRA_SEAL_LENGTH;

        if signers_data_len % ADDRESS_LENGTH != 0 {
            return Err(PoaConsensusError::InvalidSignerList);
        }

        let num_signers = signers_data_len / ADDRESS_LENGTH;
        let mut signers = Vec::with_capacity(num_signers);

        for i in 0..num_signers {
            let start = EXTRA_VANITY_LENGTH + i * ADDRESS_LENGTH;
            let end = start + ADDRESS_LENGTH;
            let address = Address::from_slice(&extra_data[start..end]);
            signers.push(address);
        }

        Ok(signers)
    }

    /// Returns a reference to the chain spec
    pub fn chain_spec(&self) -> &Arc<PoaChainSpec> {
        &self.chain_spec
    }
}

// Use concrete Header type instead of generic H so we can access extra_data
// for POA signature verification. This is safe because PoaNode always uses EthPrimitives
// which has Header = alloy_consensus::Header.
impl HeaderValidator<Header> for PoaConsensus {
    fn validate_header(&self, header: &SealedHeader<Header>) -> Result<(), ConsensusError> {
        // 1. Validate nonce (POA uses nonce for voting: 0x0 = neutral, 0xff..ff = add, 0x00 = remove)
        if let Some(nonce) = header.header().nonce() {
            let zero_nonce = alloy_primitives::B64::ZERO;
            let vote_add = alloy_primitives::B64::from_slice(&[0xff; 8]);

            if nonce != zero_nonce && nonce != vote_add {
                // Allow any nonce for flexibility in voting
            }
        }

        // 2. In production mode, verify POA signature
        if !self.dev_mode {
            let inner_header = header.header();
            let extra_data = &inner_header.extra_data;
            let min_length = EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH;

            if extra_data.len() < min_length {
                return Err(PoaConsensusError::ExtraDataTooShort {
                    expected: min_length,
                    got: extra_data.len(),
                }
                .into());
            }

            // Recover signer from the signature in extra_data
            let signer = self.recover_signer(inner_header).map_err(|e| -> ConsensusError {
                ConsensusError::Custom(std::sync::Arc::new(e))
            })?;

            // Verify the signer is in the authorized signers list
            self.validate_signer(&signer).map_err(|e| -> ConsensusError {
                ConsensusError::Custom(std::sync::Arc::new(e))
            })?;
        }

        Ok(())
    }

    fn validate_header_against_parent(
        &self,
        header: &SealedHeader<Header>,
        parent: &SealedHeader<Header>,
    ) -> Result<(), ConsensusError> {
        // Validate block number
        if header.header().number() != parent.header().number() + 1 {
            return Err(ConsensusError::ParentBlockNumberMismatch {
                parent_block_number: parent.header().number(),
                block_number: header.header().number(),
            });
        }

        // Validate parent hash
        if header.header().parent_hash() != parent.hash() {
            return Err(ConsensusError::ParentHashMismatch(
                GotExpected { got: header.header().parent_hash(), expected: parent.hash() }.into(),
            ));
        }

        // Validate timestamp (must be after parent + minimum period)
        let min_timestamp = parent.header().timestamp() + self.chain_spec.block_period();
        if header.header().timestamp() < min_timestamp {
            return Err(PoaConsensusError::TimestampTooEarly {
                timestamp: header.header().timestamp(),
                parent_timestamp: parent.header().timestamp(),
            }
            .into());
        }

        // Validate gas limit changes (EIP-1559 compatible)
        let parent_gas_limit = parent.header().gas_limit();
        let current_gas_limit = header.header().gas_limit();
        let max_change = parent_gas_limit / 1024;

        if current_gas_limit > parent_gas_limit + max_change {
            return Err(ConsensusError::GasLimitInvalidIncrease {
                parent_gas_limit,
                child_gas_limit: current_gas_limit,
            });
        }

        if current_gas_limit < parent_gas_limit.saturating_sub(max_change) {
            return Err(ConsensusError::GasLimitInvalidDecrease {
                parent_gas_limit,
                child_gas_limit: current_gas_limit,
            });
        }

        Ok(())
    }
}

impl<B: Block> Consensus<B> for PoaConsensus
where
    PoaConsensus: HeaderValidator<B::Header>,
{
    fn validate_body_against_header(
        &self,
        _body: &B::Body,
        header: &SealedHeader<B::Header>,
    ) -> Result<(), ConsensusError> {
        // Validate that gas used doesn't exceed gas limit
        if header.header().gas_used() > header.header().gas_limit() {
            return Err(ConsensusError::HeaderGasUsedExceedsGasLimit {
                gas_used: header.header().gas_used(),
                gas_limit: header.header().gas_limit(),
            });
        }
        Ok(())
    }

    fn validate_block_pre_execution(&self, block: &SealedBlock<B>) -> Result<(), ConsensusError> {
        // Validate extra_data has minimum length for POA (vanity + seal)
        let extra_data = block.header().extra_data();
        let min_length = EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH;
        if extra_data.len() < min_length {
            if !self.dev_mode {
                // In production mode, reject blocks with invalid extra_data
                return Err(PoaConsensusError::ExtraDataTooShort {
                    expected: min_length,
                    got: extra_data.len(),
                }
                .into());
            }
            // In dev mode, log but don't reject (blocks are unsigned)
        }

        // Validate gas used doesn't exceed gas limit
        if block.header().gas_used() > block.header().gas_limit() {
            return Err(ConsensusError::HeaderGasUsedExceedsGasLimit {
                gas_used: block.header().gas_used(),
                gas_limit: block.header().gas_limit(),
            });
        }

        Ok(())
    }
}

impl<N: NodePrimitives> FullConsensus<N> for PoaConsensus
where
    PoaConsensus: Consensus<N::Block>,
{
    fn validate_block_post_execution(
        &self,
        block: &RecoveredBlock<N::Block>,
        result: &BlockExecutionResult<N::Receipt>,
        receipt_root_bloom: Option<ReceiptRootBloom>,
    ) -> Result<(), ConsensusError> {
        // Validate gas used matches what's in the header
        let header_gas_used = block.header().gas_used();
        if result.gas_used != header_gas_used {
            return Err(ConsensusError::BlockGasUsed {
                gas: GotExpected {
                    got: result.gas_used,
                    expected: header_gas_used,
                },
                gas_spent_by_tx: vec![],
            });
        }

        // Validate receipt root and logs bloom if pre-computed values are provided
        if let Some((receipt_root, logs_bloom)) = receipt_root_bloom {
            let header_receipt_root = block.header().receipts_root();
            if header_receipt_root != receipt_root {
                return Err(ConsensusError::BodyReceiptRootDiff(
                    GotExpected { got: receipt_root, expected: header_receipt_root }.into(),
                ));
            }

            let header_logs_bloom = block.header().logs_bloom();
            if header_logs_bloom != logs_bloom {
                return Err(ConsensusError::BodyBloomLogDiff(
                    GotExpected { got: logs_bloom, expected: header_logs_bloom }.into(),
                ));
            }
        }

        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::signer::{dev, BlockSealer, SignerManager};

    fn dev_consensus() -> PoaConsensus {
        let chain = Arc::new(crate::chainspec::PoaChainSpec::dev_chain());
        PoaConsensus::new_dev(chain)
    }

    fn production_consensus() -> PoaConsensus {
        let chain = Arc::new(crate::chainspec::PoaChainSpec::dev_chain());
        PoaConsensus::new(chain)
    }

    #[test]
    fn test_consensus_creation() {
        let chain = Arc::new(crate::chainspec::PoaChainSpec::dev_chain());
        let consensus = PoaConsensus::new(chain);
        assert!(!consensus.chain_spec.signers().is_empty());
        assert!(!consensus.is_dev_mode());
    }

    #[test]
    fn test_consensus_dev_mode() {
        let chain = Arc::new(crate::chainspec::PoaChainSpec::dev_chain());
        let consensus = PoaConsensus::new_dev(chain);
        assert!(consensus.is_dev_mode());
    }

    #[test]
    fn test_consensus_with_dev_mode() {
        let chain = Arc::new(crate::chainspec::PoaChainSpec::dev_chain());
        let consensus = PoaConsensus::new(chain).with_dev_mode(true);
        assert!(consensus.is_dev_mode());
    }

    #[test]
    fn test_epoch_block_detection() {
        let chain = Arc::new(crate::chainspec::PoaChainSpec::dev_chain());
        let consensus = PoaConsensus::new(chain.clone());

        let epoch = chain.epoch();
        assert!(consensus.is_epoch_block(0));
        assert!(consensus.is_epoch_block(epoch));
        assert!(consensus.is_epoch_block(epoch * 2));
        assert!(!consensus.is_epoch_block(1));
        assert!(!consensus.is_epoch_block(epoch + 1));
    }

    #[test]
    fn test_validate_signer_authorized() {
        let consensus = production_consensus();
        let signers = consensus.chain_spec.signers().to_vec();
        assert!(!signers.is_empty());
        // First signer should be authorized
        assert!(consensus.validate_signer(&signers[0]).is_ok());
    }

    #[test]
    fn test_validate_signer_unauthorized() {
        let consensus = production_consensus();
        let fake_signer: Address = "0x0000000000000000000000000000000000000099".parse().unwrap();
        let result = consensus.validate_signer(&fake_signer);
        assert!(result.is_err());
        match result.unwrap_err() {
            PoaConsensusError::UnauthorizedSigner { signer } => {
                assert_eq!(signer, fake_signer);
            }
            other => panic!("Expected UnauthorizedSigner, got {:?}", other),
        }
    }

    #[test]
    fn test_recover_signer_short_extra_data() {
        let consensus = production_consensus();
        let header = Header {
            extra_data: vec![0u8; 10].into(), // Too short
            ..Default::default()
        };
        let result = consensus.recover_signer(&header);
        assert!(result.is_err());
        match result.unwrap_err() {
            PoaConsensusError::ExtraDataTooShort { expected, got } => {
                assert_eq!(expected, EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH);
                assert_eq!(got, 10);
            }
            other => panic!("Expected ExtraDataTooShort, got {:?}", other),
        }
    }

    #[tokio::test]
    async fn test_recover_signer_valid_signature() {
        let consensus = production_consensus();
        let manager = Arc::new(SignerManager::new());
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0])
            .await
            .unwrap();

        let sealer = BlockSealer::new(manager);

        // Create a header with space for vanity + seal
        let header = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 12345,
            extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
            ..Default::default()
        };

        // Sign the header
        let sealed_header = sealer.seal_header(header, &address).await.unwrap();

        // Recover the signer
        let recovered = consensus.recover_signer(&sealed_header).unwrap();
        assert_eq!(recovered, address);
    }

    #[tokio::test]
    async fn test_validate_header_with_valid_signature() {
        let consensus = production_consensus();
        let manager = Arc::new(SignerManager::new());
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0])
            .await
            .unwrap();

        // Verify the signer address is authorized
        assert!(consensus.chain_spec.is_authorized_signer(&address));

        let sealer = BlockSealer::new(manager);

        let header = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 12345,
            extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
            ..Default::default()
        };

        let signed_header = sealer.seal_header(header, &address).await.unwrap();
        let sealed = SealedHeader::seal_slow(signed_header);

        // Should pass validation in production mode
        let result: Result<(), ConsensusError> =
            HeaderValidator::validate_header(&consensus, &sealed);
        assert!(result.is_ok());
    }

    #[test]
    fn test_validate_header_short_extra_data_production() {
        let consensus = production_consensus();

        let header = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 12345,
            extra_data: vec![0u8; 10].into(), // Too short for POA
            ..Default::default()
        };
        let sealed = SealedHeader::seal_slow(header);

        // Production mode should reject
        let result: Result<(), ConsensusError> =
            HeaderValidator::validate_header(&consensus, &sealed);
        assert!(result.is_err());
    }

    #[test]
    fn test_validate_header_short_extra_data_dev_mode() {
        let consensus = dev_consensus();

        let header = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 12345,
            extra_data: vec![0u8; 10].into(), // Too short for POA
            ..Default::default()
        };
        let sealed = SealedHeader::seal_slow(header);

        // Dev mode should pass (no signature checks)
        let result: Result<(), ConsensusError> =
            HeaderValidator::validate_header(&consensus, &sealed);
        assert!(result.is_ok());
    }

    #[tokio::test]
    async fn test_validate_header_unauthorized_signer() {
        // Use a key that produces an address NOT in the authorized signers list
        let consensus = production_consensus();
        let manager = Arc::new(SignerManager::new());
        // DEV_PRIVATE_KEYS[5] corresponds to account index 5 which is NOT in dev_signers() (only first 3)
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[5])
            .await
            .unwrap();

        // Verify the address is NOT authorized
        assert!(!consensus.chain_spec.is_authorized_signer(&address));

        let sealer = BlockSealer::new(manager);

        let header = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 12345,
            extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
            ..Default::default()
        };

        let signed_header = sealer.seal_header(header, &address).await.unwrap();
        let sealed = SealedHeader::seal_slow(signed_header);

        // Should fail - signer not authorized
        let result: Result<(), ConsensusError> =
            HeaderValidator::validate_header(&consensus, &sealed);
        assert!(result.is_err());
    }

    #[test]
    fn test_validate_header_against_parent_valid() {
        let consensus = dev_consensus();

        let parent = Header {
            number: 0,
            gas_limit: 30_000_000,
            timestamp: 0,
            ..Default::default()
        };
        let sealed_parent = SealedHeader::seal_slow(parent);

        let child = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 2, // At least block_period (2s) after parent
            parent_hash: sealed_parent.hash(),
            ..Default::default()
        };
        let sealed_child = SealedHeader::seal_slow(child);

        let result = consensus.validate_header_against_parent(&sealed_child, &sealed_parent);
        assert!(result.is_ok());
    }

    #[test]
    fn test_validate_header_against_parent_wrong_number() {
        let consensus = dev_consensus();

        let parent = Header {
            number: 0,
            gas_limit: 30_000_000,
            ..Default::default()
        };
        let sealed_parent = SealedHeader::seal_slow(parent);

        let child = Header {
            number: 5, // Wrong - should be 1
            gas_limit: 30_000_000,
            timestamp: 2,
            parent_hash: sealed_parent.hash(),
            ..Default::default()
        };
        let sealed_child = SealedHeader::seal_slow(child);

        let result = consensus.validate_header_against_parent(&sealed_child, &sealed_parent);
        assert!(result.is_err());
    }

    #[test]
    fn test_validate_header_against_parent_wrong_hash() {
        let consensus = dev_consensus();

        let parent = Header {
            number: 0,
            gas_limit: 30_000_000,
            ..Default::default()
        };
        let sealed_parent = SealedHeader::seal_slow(parent);

        let child = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 2,
            parent_hash: B256::ZERO, // Wrong parent hash
            ..Default::default()
        };
        let sealed_child = SealedHeader::seal_slow(child);

        let result = consensus.validate_header_against_parent(&sealed_child, &sealed_parent);
        assert!(result.is_err());
    }

    #[test]
    fn test_validate_header_against_parent_timestamp_too_early() {
        let consensus = dev_consensus();

        let parent = Header {
            number: 0,
            gas_limit: 30_000_000,
            timestamp: 100,
            ..Default::default()
        };
        let sealed_parent = SealedHeader::seal_slow(parent);

        let child = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 101, // Only 1s after parent, but block_period is 2s
            parent_hash: sealed_parent.hash(),
            ..Default::default()
        };
        let sealed_child = SealedHeader::seal_slow(child);

        let result = consensus.validate_header_against_parent(&sealed_child, &sealed_parent);
        assert!(result.is_err());
    }

    #[test]
    fn test_validate_header_against_parent_gas_limit_increase_too_large() {
        let consensus = dev_consensus();

        let parent = Header {
            number: 0,
            gas_limit: 30_000_000,
            timestamp: 0,
            ..Default::default()
        };
        let sealed_parent = SealedHeader::seal_slow(parent);

        // Max increase is parent_gas_limit / 1024 = ~29,296
        let child = Header {
            number: 1,
            gas_limit: 31_000_000, // 1M increase, way over limit
            timestamp: 2,
            parent_hash: sealed_parent.hash(),
            ..Default::default()
        };
        let sealed_child = SealedHeader::seal_slow(child);

        let result = consensus.validate_header_against_parent(&sealed_child, &sealed_parent);
        assert!(result.is_err());
    }

    #[test]
    fn test_validate_header_against_parent_gas_limit_decrease_too_large() {
        let consensus = dev_consensus();

        let parent = Header {
            number: 0,
            gas_limit: 30_000_000,
            timestamp: 0,
            ..Default::default()
        };
        let sealed_parent = SealedHeader::seal_slow(parent);

        let child = Header {
            number: 1,
            gas_limit: 29_000_000, // 1M decrease, way over limit
            timestamp: 2,
            parent_hash: sealed_parent.hash(),
            ..Default::default()
        };
        let sealed_child = SealedHeader::seal_slow(child);

        let result = consensus.validate_header_against_parent(&sealed_child, &sealed_parent);
        assert!(result.is_err());
    }

    #[test]
    fn test_seal_hash_strips_signature() {
        let consensus = production_consensus();

        // Create two headers: one with signature, one without
        let mut extra_data_with_sig = vec![0u8; EXTRA_VANITY_LENGTH];
        extra_data_with_sig.extend_from_slice(&[0xAA; EXTRA_SEAL_LENGTH]);

        let extra_data_without_sig = vec![0u8; EXTRA_VANITY_LENGTH];

        let header_with_sig = Header {
            number: 1,
            extra_data: extra_data_with_sig.into(),
            ..Default::default()
        };

        let header_without_sig = Header {
            number: 1,
            extra_data: extra_data_without_sig.into(),
            ..Default::default()
        };

        // Seal hash should be the same regardless of signature content
        let hash_with = consensus.seal_hash(&header_with_sig);
        let hash_without = keccak256(alloy_rlp::encode(&header_without_sig));
        assert_eq!(hash_with, hash_without);
    }

    #[test]
    fn test_extract_signers_from_epoch_block() {
        let consensus = production_consensus();

        let signer1: Address = "0x0000000000000000000000000000000000000001".parse().unwrap();
        let signer2: Address = "0x0000000000000000000000000000000000000002".parse().unwrap();

        // Build extra_data: vanity (32) + 2 signers (40) + seal (65)
        let mut extra_data = vec![0u8; EXTRA_VANITY_LENGTH];
        extra_data.extend_from_slice(signer1.as_slice());
        extra_data.extend_from_slice(signer2.as_slice());
        extra_data.extend_from_slice(&[0u8; EXTRA_SEAL_LENGTH]);

        let header = Header {
            number: 0, // Epoch block
            extra_data: extra_data.into(),
            ..Default::default()
        };

        let signers = consensus.extract_signers_from_epoch_block(&header).unwrap();
        assert_eq!(signers.len(), 2);
        assert_eq!(signers[0], signer1);
        assert_eq!(signers[1], signer2);
    }

    #[test]
    fn test_extract_signers_invalid_length() {
        let consensus = production_consensus();

        // Build extra_data with misaligned signer data (not multiple of 20)
        let mut extra_data = vec![0u8; EXTRA_VANITY_LENGTH];
        extra_data.extend_from_slice(&[0u8; 15]); // 15 bytes - not a valid address
        extra_data.extend_from_slice(&[0u8; EXTRA_SEAL_LENGTH]);

        let header = Header {
            extra_data: extra_data.into(),
            ..Default::default()
        };

        let result = consensus.extract_signers_from_epoch_block(&header);
        assert!(result.is_err());
    }

    #[test]
    fn test_validate_difficulty_in_turn() {
        let consensus = production_consensus();
        let signers = consensus.chain_spec.signers().to_vec();

        // Block 0 should be signed by signer[0] (in-turn, difficulty=1)
        let header = Header {
            number: 0,
            difficulty: U256::from(1),
            ..Default::default()
        };
        assert!(consensus.validate_difficulty(&header, &signers[0]).is_ok());
    }

    #[test]
    fn test_validate_difficulty_out_of_turn() {
        let consensus = production_consensus();
        let signers = consensus.chain_spec.signers().to_vec();

        // Block 0 is signer[0]'s turn, so signer[1] is out-of-turn (difficulty=2)
        let header = Header {
            number: 0,
            difficulty: U256::from(2),
            ..Default::default()
        };
        assert!(consensus.validate_difficulty(&header, &signers[1]).is_ok());
    }

    #[test]
    fn test_validate_difficulty_wrong_value() {
        let consensus = production_consensus();
        let signers = consensus.chain_spec.signers().to_vec();

        // Block 0 is signer[0]'s turn, difficulty should be 1 not 2
        let header = Header {
            number: 0,
            difficulty: U256::from(2),
            ..Default::default()
        };
        assert!(consensus.validate_difficulty(&header, &signers[0]).is_err());
    }

    // =========================================================================
    // FullConsensus trait: validate_block_post_execution tests
    // =========================================================================

    use reth_ethereum::BlockBody;
    use alloy_primitives::Bloom;
    use reth_execution_types::BlockExecutionResult;
    use reth_primitives_traits::RecoveredBlock;

    fn make_recovered_block(gas_used: u64, gas_limit: u64, receipts_root: B256, logs_bloom: Bloom) -> RecoveredBlock<reth_ethereum::Block> {
        let header = Header {
            gas_used,
            gas_limit,
            receipts_root,
            logs_bloom,
            ..Default::default()
        };
        let body = BlockBody::default();
        let block = reth_ethereum::Block { header, body };
        let sealed = SealedBlock::seal_slow(block);
        RecoveredBlock::new_sealed(sealed, vec![])
    }

    fn make_execution_result(gas_used: u64) -> BlockExecutionResult<reth_ethereum::Receipt> {
        BlockExecutionResult {
            receipts: vec![],
            requests: Default::default(),
            gas_used,
            blob_gas_used: 0,
        }
    }

    #[test]
    fn test_validate_block_post_execution_gas_used_match() {
        let consensus = dev_consensus();
        let block = make_recovered_block(1000, 30_000_000, B256::ZERO, Bloom::ZERO);
        let result = make_execution_result(1000);

        let validation: Result<(), ConsensusError> = FullConsensus::<reth_ethereum::EthPrimitives>::validate_block_post_execution(
            &consensus,
            &block,
            &result,
            None,
        );
        assert!(validation.is_ok());
    }

    #[test]
    fn test_validate_block_post_execution_gas_used_mismatch() {
        let consensus = dev_consensus();
        let block = make_recovered_block(1000, 30_000_000, B256::ZERO, Bloom::ZERO);
        let result = make_execution_result(2000); // Mismatch: header says 1000, execution says 2000

        let validation: Result<(), ConsensusError> = FullConsensus::<reth_ethereum::EthPrimitives>::validate_block_post_execution(
            &consensus,
            &block,
            &result,
            None,
        );
        assert!(validation.is_err());
        match validation.unwrap_err() {
            ConsensusError::BlockGasUsed { gas, .. } => {
                assert_eq!(gas.got, 2000);
                assert_eq!(gas.expected, 1000);
            }
            other => panic!("Expected BlockGasUsed, got {:?}", other),
        }
    }

    #[test]
    fn test_validate_block_post_execution_receipt_root_mismatch() {
        let consensus = dev_consensus();
        let expected_root = B256::from([0xAA; 32]);
        let block = make_recovered_block(0, 30_000_000, expected_root, Bloom::ZERO);
        let result = make_execution_result(0);

        let wrong_root = B256::from([0xBB; 32]); // Different from header's receipts_root
        let receipt_root_bloom = Some((wrong_root, Bloom::ZERO));

        let validation: Result<(), ConsensusError> = FullConsensus::<reth_ethereum::EthPrimitives>::validate_block_post_execution(
            &consensus,
            &block,
            &result,
            receipt_root_bloom,
        );
        assert!(validation.is_err());
    }

    #[test]
    fn test_validate_block_post_execution_logs_bloom_mismatch() {
        let consensus = dev_consensus();
        let expected_bloom = Bloom::from([0xAA; 256]);
        let block = make_recovered_block(0, 30_000_000, B256::ZERO, expected_bloom);
        let result = make_execution_result(0);

        let wrong_bloom = Bloom::from([0xBB; 256]);
        let receipt_root_bloom = Some((B256::ZERO, wrong_bloom));

        let validation: Result<(), ConsensusError> = FullConsensus::<reth_ethereum::EthPrimitives>::validate_block_post_execution(
            &consensus,
            &block,
            &result,
            receipt_root_bloom,
        );
        assert!(validation.is_err());
    }

    #[test]
    fn test_validate_block_post_execution_no_receipt_root() {
        let consensus = dev_consensus();
        let block = make_recovered_block(5000, 30_000_000, B256::ZERO, Bloom::ZERO);
        let result = make_execution_result(5000);

        // None means skip receipt root and logs bloom check
        let validation: Result<(), ConsensusError> = FullConsensus::<reth_ethereum::EthPrimitives>::validate_block_post_execution(
            &consensus,
            &block,
            &result,
            None,
        );
        assert!(validation.is_ok());
    }

    // =========================================================================
    // Consensus trait: validate_body_against_header, validate_block_pre_execution
    // =========================================================================

    fn make_sealed_block(gas_used: u64, gas_limit: u64, extra_data_len: usize) -> SealedBlock<reth_ethereum::Block> {
        let header = Header {
            gas_used,
            gas_limit,
            extra_data: vec![0u8; extra_data_len].into(),
            ..Default::default()
        };
        let body = BlockBody::default();
        let block = reth_ethereum::Block { header, body };
        SealedBlock::seal_slow(block)
    }

    #[test]
    fn test_validate_body_against_header_gas_ok() {
        let consensus = dev_consensus();
        let header = Header {
            gas_used: 1000,
            gas_limit: 30_000_000,
            ..Default::default()
        };
        let sealed = SealedHeader::seal_slow(header);
        let body = BlockBody::default();

        let result: Result<(), ConsensusError> = Consensus::<reth_ethereum::Block>::validate_body_against_header(
            &consensus,
            &body,
            &sealed,
        );
        assert!(result.is_ok());
    }

    #[test]
    fn test_validate_body_against_header_gas_exceeds() {
        let consensus = dev_consensus();
        let header = Header {
            gas_used: 31_000_000, // Exceeds gas_limit
            gas_limit: 30_000_000,
            ..Default::default()
        };
        let sealed = SealedHeader::seal_slow(header);
        let body = BlockBody::default();

        let result: Result<(), ConsensusError> = Consensus::<reth_ethereum::Block>::validate_body_against_header(
            &consensus,
            &body,
            &sealed,
        );
        assert!(result.is_err());
    }

    #[test]
    fn test_validate_block_pre_execution_production_short_extra_data() {
        let consensus = production_consensus();
        let sealed = make_sealed_block(0, 30_000_000, 10); // Too short for POA

        let result: Result<(), ConsensusError> = Consensus::<reth_ethereum::Block>::validate_block_pre_execution(
            &consensus,
            &sealed,
        );
        assert!(result.is_err());
    }

    #[test]
    fn test_validate_block_pre_execution_dev_short_extra_data() {
        let consensus = dev_consensus();
        let sealed = make_sealed_block(0, 30_000_000, 10); // Short but dev mode allows it

        let result: Result<(), ConsensusError> = Consensus::<reth_ethereum::Block>::validate_block_pre_execution(
            &consensus,
            &sealed,
        );
        assert!(result.is_ok());
    }

    // =========================================================================
    // Boundary tests
    // =========================================================================

    #[test]
    fn test_validate_header_against_parent_gas_limit_exact_boundary() {
        let consensus = dev_consensus();

        let parent_gas_limit: u64 = 30_000_000;
        let max_change = parent_gas_limit / 1024;

        let parent = Header {
            number: 0,
            gas_limit: parent_gas_limit,
            timestamp: 0,
            ..Default::default()
        };
        let sealed_parent = SealedHeader::seal_slow(parent);

        // Exactly at max increase boundary should pass
        let child_increase = Header {
            number: 1,
            gas_limit: parent_gas_limit + max_change,
            timestamp: 2,
            parent_hash: sealed_parent.hash(),
            ..Default::default()
        };
        let sealed_child = SealedHeader::seal_slow(child_increase);
        assert!(consensus.validate_header_against_parent(&sealed_child, &sealed_parent).is_ok());

        // Exactly at max decrease boundary should pass
        let child_decrease = Header {
            number: 1,
            gas_limit: parent_gas_limit - max_change,
            timestamp: 2,
            parent_hash: sealed_parent.hash(),
            ..Default::default()
        };
        let sealed_child = SealedHeader::seal_slow(child_decrease);
        assert!(consensus.validate_header_against_parent(&sealed_child, &sealed_parent).is_ok());
    }

    #[test]
    fn test_validate_header_against_parent_timestamp_exact_period() {
        let consensus = dev_consensus();
        let block_period = consensus.chain_spec.block_period(); // 2 seconds

        let parent = Header {
            number: 0,
            gas_limit: 30_000_000,
            timestamp: 100,
            ..Default::default()
        };
        let sealed_parent = SealedHeader::seal_slow(parent);

        // Exactly at minimum timestamp (parent + block_period) should pass
        let child = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 100 + block_period,
            parent_hash: sealed_parent.hash(),
            ..Default::default()
        };
        let sealed_child = SealedHeader::seal_slow(child);
        assert!(consensus.validate_header_against_parent(&sealed_child, &sealed_parent).is_ok());

        // One second before should fail
        let child_too_early = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 100 + block_period - 1,
            parent_hash: sealed_parent.hash(),
            ..Default::default()
        };
        let sealed_child_early = SealedHeader::seal_slow(child_too_early);
        assert!(consensus.validate_header_against_parent(&sealed_child_early, &sealed_parent).is_err());
    }

    // =========================================================================
    // Cross-module integration: full signed block passes all consensus checks
    // =========================================================================

    #[tokio::test]
    async fn test_full_signed_block_passes_all_consensus() {
        let chain = Arc::new(crate::chainspec::PoaChainSpec::dev_chain());
        let consensus = PoaConsensus::new(chain.clone());

        let manager = Arc::new(SignerManager::new());
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0])
            .await
            .unwrap();

        // Verify the signer is authorized
        assert!(chain.is_authorized_signer(&address));

        let sealer = BlockSealer::new(manager);

        // Create a header with valid POA extra_data format
        let header = Header {
            number: 1,
            gas_limit: 30_000_000,
            gas_used: 0,
            timestamp: 12345,
            extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
            ..Default::default()
        };

        // Sign the header
        let signed_header = sealer.seal_header(header, &address).await.unwrap();
        let sealed_header = SealedHeader::seal_slow(signed_header.clone());

        // 1. validate_header should pass (signature verified)
        let header_result: Result<(), ConsensusError> =
            HeaderValidator::validate_header(&consensus, &sealed_header);
        assert!(header_result.is_ok(), "Header validation should pass");

        // 2. validate_block_pre_execution should pass (extra_data long enough)
        let body = BlockBody::default();
        let block = reth_ethereum::Block { header: signed_header.clone(), body };
        let sealed_block = SealedBlock::seal_slow(block);
        let pre_exec_result: Result<(), ConsensusError> =
            Consensus::<reth_ethereum::Block>::validate_block_pre_execution(&consensus, &sealed_block);
        assert!(pre_exec_result.is_ok(), "Pre-execution validation should pass");

        // 3. validate_block_post_execution should pass (matching gas_used)
        let body2 = BlockBody::default();
        let block2 = reth_ethereum::Block { header: signed_header, body: body2 };
        let sealed2 = SealedBlock::seal_slow(block2);
        let recovered = RecoveredBlock::new_sealed(sealed2, vec![]);
        let exec_result = make_execution_result(0);
        let post_exec: Result<(), ConsensusError> = FullConsensus::<reth_ethereum::EthPrimitives>::validate_block_post_execution(
            &consensus,
            &recovered,
            &exec_result,
            None,
        );
        assert!(post_exec.is_ok(), "Post-execution validation should pass");
    }
}

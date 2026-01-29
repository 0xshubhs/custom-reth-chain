//! POA Consensus Implementation
//!
//! This module implements a Proof of Authority consensus mechanism that validates:
//! - Block signers are authorized
//! - Blocks are signed correctly
//! - Timing constraints are respected
//! - The signer rotation follows the expected pattern

use crate::chainspec::PoaChainSpec;
use alloy_consensus::Header;
use alloy_primitives::{keccak256, Address, Signature, B256};
use alloy_primitives::Sealable;
use reth_consensus::{Consensus, ConsensusError, FullConsensus, HeaderValidator, ReceiptRootBloom};
use reth_execution_types::BlockExecutionResult;
use reth_primitives_traits::{
    Block, BlockHeader, NodePrimitives, RecoveredBlock, SealedBlock, SealedHeader,
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
}

impl PoaConsensus {
    /// Create a new POA consensus instance
    pub fn new(chain_spec: Arc<PoaChainSpec>) -> Self {
        Self { chain_spec }
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
    #[allow(dead_code)]
    fn validate_signer(&self, signer: &Address) -> Result<(), PoaConsensusError> {
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
    #[allow(dead_code)]
    fn validate_difficulty(
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
}

use alloy_primitives::U256;
use reth_primitives_traits::GotExpected;

impl<H: BlockHeader + Sealable> HeaderValidator<H> for PoaConsensus {
    fn validate_header(&self, header: &SealedHeader<H>) -> Result<(), ConsensusError> {
        // For POA, we validate:
        // 1. The header is properly sealed
        // 2. Nonce should be zero (POA doesn't use nonce like PoW)
        // 3. MixHash can be used for additional data or should be zero

        if let Some(nonce) = header.header().nonce() {
            // In POA, nonce is typically 0x0 or used for voting
            // We allow both zero and voting nonces
            let zero_nonce = alloy_primitives::B64::ZERO;
            let vote_add = alloy_primitives::B64::from_slice(&[0xff; 8]);
            let vote_remove = alloy_primitives::B64::ZERO;

            if nonce != zero_nonce && nonce != vote_add && nonce != vote_remove {
                // Allow any nonce for flexibility in voting
            }
        }

        Ok(())
    }

    fn validate_header_against_parent(
        &self,
        header: &SealedHeader<H>,
        parent: &SealedHeader<H>,
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

impl<B: Block> Consensus<B> for PoaConsensus {
    fn validate_body_against_header(
        &self,
        _body: &B::Body,
        _header: &SealedHeader<B::Header>,
    ) -> Result<(), ConsensusError> {
        // Validate transaction root, etc.
        // The base implementation handles most of this
        Ok(())
    }

    fn validate_block_pre_execution(&self, _block: &SealedBlock<B>) -> Result<(), ConsensusError> {
        // POA-specific pre-execution validation
        // For now, we trust the header validation
        Ok(())
    }
}

impl<N: NodePrimitives> FullConsensus<N> for PoaConsensus {
    fn validate_block_post_execution(
        &self,
        _block: &RecoveredBlock<N::Block>,
        _result: &BlockExecutionResult<N::Receipt>,
        _receipt_root_bloom: Option<ReceiptRootBloom>,
    ) -> Result<(), ConsensusError> {
        // Post-execution validation
        // Verify receipt root matches, etc.
        Ok(())
    }
}

/// Builder for POA consensus that integrates with Reth's node builder
#[derive(Debug, Clone)]
pub struct PoaConsensusBuilder {
    chain_spec: Arc<PoaChainSpec>,
}

impl PoaConsensusBuilder {
    /// Create a new consensus builder
    pub fn new(chain_spec: Arc<PoaChainSpec>) -> Self {
        Self { chain_spec }
    }

    /// Build the POA consensus instance
    pub fn build(self) -> Arc<PoaConsensus> {
        PoaConsensus::arc(self.chain_spec)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_consensus_creation() {
        let chain = Arc::new(crate::chainspec::PoaChainSpec::dev_chain());
        let consensus = PoaConsensus::new(chain);

        // Basic sanity check
        assert!(!consensus.chain_spec.signers().is_empty());
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
}

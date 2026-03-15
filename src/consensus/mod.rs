//! POA Consensus Implementation
//!
//! This module implements a Proof of Authority consensus mechanism that validates:
//! - Block signers are authorized
//! - Blocks are signed correctly
//! - Timing constraints are respected
//! - The signer rotation follows the expected pattern

pub mod errors;

pub use crate::constants::{ADDRESS_LENGTH, EXTRA_SEAL_LENGTH, EXTRA_VANITY_LENGTH};
pub use errors::PoaConsensusError;

use crate::chainspec::PoaChainSpec;
use alloy_consensus::{BlockHeader, Header};
use alloy_primitives::{keccak256, Address, Signature, B256, U256};
use reth_consensus::{Consensus, ConsensusError, FullConsensus, HeaderValidator, ReceiptRootBloom};
use reth_execution_types::BlockExecutionResult;
use reth_primitives_traits::{
    Block, GotExpected, NodePrimitives, RecoveredBlock, SealedBlock, SealedHeader,
};
use std::sync::Arc;

// ─── Cold-path error constructors ─────────────────────────────────────────────
//
// These functions are called only on error paths.  Marking them `#[cold]`
// tells the compiler and branch predictor that the enclosing success path is
// the common case, improving instruction-cache locality on the hot path.
// `#[inline(never)]` keeps the error-construction code out of the hot function
// body entirely, shrinking its instruction footprint.

#[cold]
#[inline(never)]
fn cold_extra_data_too_short(expected: usize, got: usize) -> PoaConsensusError {
    PoaConsensusError::ExtraDataTooShort { expected, got }
}

#[cold]
#[inline(never)]
fn cold_invalid_signature() -> PoaConsensusError {
    PoaConsensusError::InvalidSignature
}

#[cold]
#[inline(never)]
fn cold_unauthorized_signer(signer: Address) -> PoaConsensusError {
    PoaConsensusError::UnauthorizedSigner { signer }
}

#[cold]
#[inline(never)]
fn cold_invalid_difficulty() -> PoaConsensusError {
    PoaConsensusError::InvalidDifficulty
}

#[cold]
#[inline(never)]
fn cold_timestamp_too_early(timestamp: u64, parent_timestamp: u64) -> PoaConsensusError {
    PoaConsensusError::TimestampTooEarly { timestamp, parent_timestamp }
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
        Self {
            chain_spec,
            dev_mode: false,
        }
    }

    /// Create a new POA consensus instance in dev mode (relaxed validation)
    pub fn new_dev(chain_spec: Arc<PoaChainSpec>) -> Self {
        Self {
            chain_spec,
            dev_mode: true,
        }
    }

    /// Set dev mode on the consensus instance
    pub fn with_dev_mode(mut self, dev_mode: bool) -> Self {
        self.dev_mode = dev_mode;
        self
    }

    /// Returns whether this consensus is in dev mode
    #[inline]
    pub fn is_dev_mode(&self) -> bool {
        self.dev_mode
    }

    /// Create an Arc-wrapped instance
    pub fn arc(chain_spec: Arc<PoaChainSpec>) -> Arc<Self> {
        Arc::new(Self::new(chain_spec))
    }

    /// Extract the signer address from the block's extra data.
    ///
    /// This is on the hot path for every validated block header.  The two error
    /// branches are annotated `#[cold]` via `cold_err_*` helpers so the compiler
    /// can keep the success path straight-line and avoid branch-prediction pressure.
    #[inline]
    pub fn recover_signer(&self, header: &Header) -> Result<Address, PoaConsensusError> {
        let extra_data = &header.extra_data;

        // Extra data must contain at least vanity + seal
        let min_length = EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH;
        if extra_data.len() < min_length {
            return Err(cold_extra_data_too_short(min_length, extra_data.len()));
        }

        // Extract the signature from the end of extra data
        let signature_start = extra_data.len() - EXTRA_SEAL_LENGTH;
        let signature_bytes = &extra_data[signature_start..];

        // Parse signature (r, s, v format)
        let signature = Signature::try_from(signature_bytes)
            .map_err(|_| cold_invalid_signature())?;

        // Calculate the seal hash (header hash without the signature)
        let seal_hash = self.seal_hash(header);

        // Recover the signer address
        signature
            .recover_address_from_prehash(&seal_hash)
            .map_err(|_| cold_invalid_signature())
    }

    /// Calculate the hash used for sealing (excludes the signature from extra data)
    pub fn seal_hash(&self, header: &Header) -> B256 {
        // Clone the header struct, then replace extra_data using Bytes::slice so
        // the truncated view shares the same underlying buffer (arc bump, O(1))
        // rather than allocating a new Vec via to_vec().
        let mut header_for_hash = header.clone();

        let extra_len = header.extra_data.len();
        if extra_len >= EXTRA_SEAL_LENGTH {
            header_for_hash.extra_data = header.extra_data.slice(..extra_len - EXTRA_SEAL_LENGTH);
        }

        // Hash the modified header
        keccak256(alloy_rlp::encode(&header_for_hash))
    }

    /// Validate that the signer is authorized
    #[inline]
    pub fn validate_signer(&self, signer: &Address) -> Result<(), PoaConsensusError> {
        if !self.chain_spec.is_authorized_signer(signer) {
            return Err(cold_unauthorized_signer(*signer));
        }
        Ok(())
    }

    /// Check if this is an epoch block (where signer list is updated)
    #[inline]
    pub fn is_epoch_block(&self, block_number: u64) -> bool {
        block_number.is_multiple_of(self.chain_spec.epoch())
    }

    /// Validate the difficulty field.
    ///
    /// The Ethereum Engine API (ExecutionPayloadV1) has no difficulty field and alloy
    /// always sets it to U256::ZERO on block deserialization. For Engine API compatibility,
    /// all POA blocks must use difficulty = 0. POA authority is determined by the ECDSA
    /// signature in extra_data, not by difficulty.
    pub fn validate_difficulty(
        &self,
        header: &Header,
        _signer: &Address,
    ) -> Result<(), PoaConsensusError> {
        if header.difficulty != U256::ZERO {
            return Err(cold_invalid_difficulty());
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

        if !signers_data_len.is_multiple_of(ADDRESS_LENGTH) {
            return Err(PoaConsensusError::InvalidSignerList);
        }

        let num_signers = signers_data_len / ADDRESS_LENGTH;
        let mut signers = Vec::with_capacity(num_signers);

        // `chunks_exact` gives the compiler a stronger alignment/stride guarantee
        // than manual index arithmetic, enabling better auto-vectorisation for the
        // address-copy loop.  The slice is already checked to be a multiple of
        // ADDRESS_LENGTH (20 bytes) above, so `remainder()` will always be empty.
        let signers_slice =
            &extra_data[EXTRA_VANITY_LENGTH..EXTRA_VANITY_LENGTH + signers_data_len];
        for chunk in signers_slice.chunks_exact(ADDRESS_LENGTH) {
            signers.push(Address::from_slice(chunk));
        }

        Ok(signers)
    }

    /// Returns a reference to the chain spec
    #[inline]
    pub fn chain_spec(&self) -> &Arc<PoaChainSpec> {
        &self.chain_spec
    }

    // ─── Fork Choice Rule ─────────────────────────────────────────────
    //
    // POA uses difficulty=0 for Engine API compatibility, so we can't use
    // cumulative difficulty for fork choice. Instead, we score chains by
    // counting how many blocks were signed by their in-turn signer.
    // In-turn blocks are preferred because they represent orderly round-robin
    // block production, indicating a healthier chain.

    /// Check if a block was signed by the expected in-turn signer.
    ///
    /// The in-turn signer for block N is `signers[N % signers.len()]`.
    /// Returns `None` if the signer cannot be recovered (dev mode, missing sig).
    #[inline]
    pub fn is_in_turn(&self, header: &Header) -> Option<bool> {
        let expected = self.chain_spec.expected_signer(header.number)?;
        let actual = self.recover_signer(header).ok()?;
        Some(actual == expected)
    }

    /// Score a chain segment by counting in-turn blocks.
    ///
    /// Higher score = more blocks signed by their expected in-turn signer.
    /// This is used for fork choice: the chain with more in-turn blocks is preferred.
    pub fn score_chain(&self, headers: &[Header]) -> u64 {
        headers
            .iter()
            .fold(0u64, |acc, h| acc + self.is_in_turn(h).unwrap_or(false) as u64)
    }

    /// Compare two chain segments for fork choice.
    ///
    /// Returns `std::cmp::Ordering`:
    /// - `Greater` if chain_a is preferred (more in-turn blocks)
    /// - `Less` if chain_b is preferred
    /// - `Equal` if tied (fall back to longest chain)
    ///
    /// When scores are equal, the longer chain wins.
    pub fn compare_chains(&self, chain_a: &[Header], chain_b: &[Header]) -> std::cmp::Ordering {
        let score_a = self.score_chain(chain_a);
        let score_b = self.score_chain(chain_b);
        score_a
            .cmp(&score_b)
            .then_with(|| chain_a.len().cmp(&chain_b.len()))
    }
}

// Use concrete Header type instead of generic H so we can access extra_data
// for POA signature verification. This is safe because PoaNode always uses EthPrimitives
// which has Header = alloy_consensus::Header.
impl HeaderValidator<Header> for PoaConsensus {
    #[inline]
    fn validate_header(&self, header: &SealedHeader<Header>) -> Result<(), ConsensusError> {
        // In production mode, verify POA signature
        if !self.dev_mode {
            // Recover signer from the signature in extra_data
            // (recover_signer validates extra_data length internally)
            let signer = self
                .recover_signer(header.header())
                .map_err(Into::<ConsensusError>::into)?;

            // Verify the signer is in the authorized signers list
            self.validate_signer(&signer)
                .map_err(Into::<ConsensusError>::into)?;
        }

        Ok(())
    }

    #[inline]
    fn validate_header_against_parent(
        &self,
        header: &SealedHeader<Header>,
        parent: &SealedHeader<Header>,
    ) -> Result<(), ConsensusError> {
        let h = header.header();
        let p = parent.header();

        // Validate block number
        if h.number() != p.number() + 1 {
            return Err(ConsensusError::ParentBlockNumberMismatch {
                parent_block_number: p.number(),
                block_number: h.number(),
            });
        }

        // Validate parent hash
        if h.parent_hash() != parent.hash() {
            return Err(ConsensusError::ParentHashMismatch(
                GotExpected {
                    got: h.parent_hash(),
                    expected: parent.hash(),
                }
                .into(),
            ));
        }

        // Validate timestamp (must be after parent + minimum period)
        let min_timestamp = p.timestamp() + self.chain_spec.block_period();
        if h.timestamp() < min_timestamp {
            return Err(cold_timestamp_too_early(h.timestamp(), p.timestamp()).into());
        }

        // Validate gas limit changes (EIP-1559 compatible)
        let parent_gas_limit = p.gas_limit();
        let current_gas_limit = h.gas_limit();
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
        let h = header.header();
        // Validate that gas used doesn't exceed gas limit
        if h.gas_used() > h.gas_limit() {
            return Err(ConsensusError::HeaderGasUsedExceedsGasLimit {
                gas_used: h.gas_used(),
                gas_limit: h.gas_limit(),
            });
        }
        Ok(())
    }

    fn validate_block_pre_execution(&self, block: &SealedBlock<B>) -> Result<(), ConsensusError> {
        // Validate extra_data has minimum length for POA (vanity + seal)
        if !self.dev_mode {
            let extra_data = block.header().extra_data();
            let min_length = EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH;
            if extra_data.len() < min_length {
                return Err(PoaConsensusError::ExtraDataTooShort {
                    expected: min_length,
                    got: extra_data.len(),
                }
                .into());
            }
        }
        // Gas check is already done in validate_body_against_header
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
        let h = block.header();

        // Validate gas used matches what's in the header
        let header_gas_used = h.gas_used();
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
            let header_receipt_root = h.receipts_root();
            if header_receipt_root != receipt_root {
                return Err(ConsensusError::BodyReceiptRootDiff(
                    GotExpected {
                        got: receipt_root,
                        expected: header_receipt_root,
                    }
                    .into(),
                ));
            }

            let header_logs_bloom = h.logs_bloom();
            if header_logs_bloom != logs_bloom {
                return Err(ConsensusError::BodyBloomLogDiff(
                    GotExpected {
                        got: logs_bloom,
                        expected: header_logs_bloom,
                    }
                    .into(),
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
        let fake_signer: Address = "0x0000000000000000000000000000000000000099"
            .parse()
            .unwrap();
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
            timestamp: 100, // Same as parent — too early (block_period is 1s, need > parent)
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

        let signer1: Address = "0x0000000000000000000000000000000000000001"
            .parse()
            .unwrap();
        let signer2: Address = "0x0000000000000000000000000000000000000002"
            .parse()
            .unwrap();

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
    fn test_validate_difficulty_zero() {
        let consensus = production_consensus();
        let signers = consensus.chain_spec.signers().to_vec();

        // All POA blocks must have difficulty = 0 (Engine API compatibility)
        let header = Header {
            number: 0,
            difficulty: U256::ZERO,
            ..Default::default()
        };
        assert!(consensus.validate_difficulty(&header, &signers[0]).is_ok());
    }

    #[test]
    fn test_validate_difficulty_nonzero_rejected() {
        let consensus = production_consensus();
        let signers = consensus.chain_spec.signers().to_vec();

        // Any non-zero difficulty is invalid (Engine API requires 0)
        let header = Header {
            number: 0,
            difficulty: U256::from(1),
            ..Default::default()
        };
        assert!(consensus.validate_difficulty(&header, &signers[0]).is_err());
    }

    // =========================================================================
    // FullConsensus trait: validate_block_post_execution tests
    // =========================================================================

    use alloy_primitives::Bloom;
    use reth_ethereum::BlockBody;
    use reth_execution_types::BlockExecutionResult;
    use reth_primitives_traits::RecoveredBlock;

    fn make_recovered_block(
        gas_used: u64,
        gas_limit: u64,
        receipts_root: B256,
        logs_bloom: Bloom,
    ) -> RecoveredBlock<reth_ethereum::Block> {
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

        let validation: Result<(), ConsensusError> =
            FullConsensus::<reth_ethereum::EthPrimitives>::validate_block_post_execution(
                &consensus, &block, &result, None,
            );
        assert!(validation.is_ok());
    }

    #[test]
    fn test_validate_block_post_execution_gas_used_mismatch() {
        let consensus = dev_consensus();
        let block = make_recovered_block(1000, 30_000_000, B256::ZERO, Bloom::ZERO);
        let result = make_execution_result(2000); // Mismatch: header says 1000, execution says 2000

        let validation: Result<(), ConsensusError> =
            FullConsensus::<reth_ethereum::EthPrimitives>::validate_block_post_execution(
                &consensus, &block, &result, None,
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

        let validation: Result<(), ConsensusError> =
            FullConsensus::<reth_ethereum::EthPrimitives>::validate_block_post_execution(
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

        let validation: Result<(), ConsensusError> =
            FullConsensus::<reth_ethereum::EthPrimitives>::validate_block_post_execution(
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
        let validation: Result<(), ConsensusError> =
            FullConsensus::<reth_ethereum::EthPrimitives>::validate_block_post_execution(
                &consensus, &block, &result, None,
            );
        assert!(validation.is_ok());
    }

    // =========================================================================
    // Consensus trait: validate_body_against_header, validate_block_pre_execution
    // =========================================================================

    fn make_sealed_block(
        gas_used: u64,
        gas_limit: u64,
        extra_data_len: usize,
    ) -> SealedBlock<reth_ethereum::Block> {
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

        let result: Result<(), ConsensusError> =
            Consensus::<reth_ethereum::Block>::validate_body_against_header(
                &consensus, &body, &sealed,
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

        let result: Result<(), ConsensusError> =
            Consensus::<reth_ethereum::Block>::validate_body_against_header(
                &consensus, &body, &sealed,
            );
        assert!(result.is_err());
    }

    #[test]
    fn test_validate_block_pre_execution_production_short_extra_data() {
        let consensus = production_consensus();
        let sealed = make_sealed_block(0, 30_000_000, 10); // Too short for POA

        let result: Result<(), ConsensusError> =
            Consensus::<reth_ethereum::Block>::validate_block_pre_execution(&consensus, &sealed);
        assert!(result.is_err());
    }

    #[test]
    fn test_validate_block_pre_execution_dev_short_extra_data() {
        let consensus = dev_consensus();
        let sealed = make_sealed_block(0, 30_000_000, 10); // Short but dev mode allows it

        let result: Result<(), ConsensusError> =
            Consensus::<reth_ethereum::Block>::validate_block_pre_execution(&consensus, &sealed);
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
        assert!(consensus
            .validate_header_against_parent(&sealed_child, &sealed_parent)
            .is_ok());

        // Exactly at max decrease boundary should pass
        let child_decrease = Header {
            number: 1,
            gas_limit: parent_gas_limit - max_change,
            timestamp: 2,
            parent_hash: sealed_parent.hash(),
            ..Default::default()
        };
        let sealed_child = SealedHeader::seal_slow(child_decrease);
        assert!(consensus
            .validate_header_against_parent(&sealed_child, &sealed_parent)
            .is_ok());
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
        assert!(consensus
            .validate_header_against_parent(&sealed_child, &sealed_parent)
            .is_ok());

        // One second before should fail
        let child_too_early = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 100 + block_period - 1,
            parent_hash: sealed_parent.hash(),
            ..Default::default()
        };
        let sealed_child_early = SealedHeader::seal_slow(child_too_early);
        assert!(consensus
            .validate_header_against_parent(&sealed_child_early, &sealed_parent)
            .is_err());
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
        let block = reth_ethereum::Block {
            header: signed_header.clone(),
            body,
        };
        let sealed_block = SealedBlock::seal_slow(block);
        let pre_exec_result: Result<(), ConsensusError> =
            Consensus::<reth_ethereum::Block>::validate_block_pre_execution(
                &consensus,
                &sealed_block,
            );
        assert!(
            pre_exec_result.is_ok(),
            "Pre-execution validation should pass"
        );

        // 3. validate_block_post_execution should pass (matching gas_used)
        let body2 = BlockBody::default();
        let block2 = reth_ethereum::Block {
            header: signed_header,
            body: body2,
        };
        let sealed2 = SealedBlock::seal_slow(block2);
        let recovered = RecoveredBlock::new_sealed(sealed2, vec![]);
        let exec_result = make_execution_result(0);
        let post_exec: Result<(), ConsensusError> =
            FullConsensus::<reth_ethereum::EthPrimitives>::validate_block_post_execution(
                &consensus,
                &recovered,
                &exec_result,
                None,
            );
        assert!(post_exec.is_ok(), "Post-execution validation should pass");
    }

    // ─── Fork Choice Tests ──────────────────────────────────────────────

    /// Helper: create a signed header for a specific signer at a given block number.
    async fn build_signed_header(block_number: u64, signer_key_index: usize) -> Header {
        let manager = Arc::new(SignerManager::new());
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[signer_key_index])
            .await
            .unwrap();
        let sealer = BlockSealer::new(manager);
        let header = Header {
            number: block_number,
            gas_limit: 30_000_000,
            timestamp: 12345 + block_number * 2,
            extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
            ..Default::default()
        };
        sealer.seal_header(header, &address).await.unwrap()
    }

    #[tokio::test]
    async fn test_is_in_turn_block1() {
        // Dev chain has 3 signers: accounts 0, 1, 2
        // Block 1 → signer index 1 (signers[1 % 3])
        let consensus = production_consensus();

        // Sign block 1 with signer 1 → should be in-turn
        let header = build_signed_header(1, 1).await;
        assert_eq!(consensus.is_in_turn(&header), Some(true));

        // Sign block 1 with signer 0 → should be out-of-turn
        let header_oot = build_signed_header(1, 0).await;
        assert_eq!(consensus.is_in_turn(&header_oot), Some(false));
    }

    #[tokio::test]
    async fn test_is_in_turn_round_robin() {
        let consensus = production_consensus();

        // Block 0 → signer 0, Block 1 → signer 1, Block 2 → signer 2, Block 3 → signer 0
        for block_num in 0u64..6 {
            let expected_signer_idx = (block_num as usize) % 3;
            let header = build_signed_header(block_num, expected_signer_idx).await;
            assert_eq!(
                consensus.is_in_turn(&header),
                Some(true),
                "Block {} should be in-turn for signer {}",
                block_num,
                expected_signer_idx
            );
        }
    }

    #[tokio::test]
    async fn test_score_chain_all_in_turn() {
        let consensus = production_consensus();

        // Build a chain where every block is signed by the in-turn signer
        let mut headers = Vec::new();
        for i in 0u64..6 {
            let signer_idx = (i as usize) % 3;
            headers.push(build_signed_header(i, signer_idx).await);
        }

        assert_eq!(consensus.score_chain(&headers), 6);
    }

    #[tokio::test]
    async fn test_score_chain_all_out_of_turn() {
        let consensus = production_consensus();

        // Build a chain where every block is signed by the WRONG signer
        let mut headers = Vec::new();
        for i in 0u64..6 {
            let wrong_signer_idx = ((i as usize) + 1) % 3; // off by one
            headers.push(build_signed_header(i, wrong_signer_idx).await);
        }

        assert_eq!(consensus.score_chain(&headers), 0);
    }

    #[tokio::test]
    async fn test_score_chain_mixed() {
        let consensus = production_consensus();

        // 3 in-turn + 3 out-of-turn
        let mut headers = Vec::new();
        for i in 0u64..6 {
            if i < 3 {
                let signer_idx = (i as usize) % 3;
                headers.push(build_signed_header(i, signer_idx).await);
            } else {
                let wrong_idx = ((i as usize) + 1) % 3;
                headers.push(build_signed_header(i, wrong_idx).await);
            }
        }

        assert_eq!(consensus.score_chain(&headers), 3);
    }

    #[tokio::test]
    async fn test_compare_chains_in_turn_wins() {
        let consensus = production_consensus();

        // Chain A: all in-turn (score = 3)
        let mut chain_a = Vec::new();
        for i in 0u64..3 {
            let signer_idx = (i as usize) % 3;
            chain_a.push(build_signed_header(i, signer_idx).await);
        }

        // Chain B: all out-of-turn (score = 0)
        let mut chain_b = Vec::new();
        for i in 0u64..3 {
            let wrong_idx = ((i as usize) + 1) % 3;
            chain_b.push(build_signed_header(i, wrong_idx).await);
        }

        assert_eq!(
            consensus.compare_chains(&chain_a, &chain_b),
            std::cmp::Ordering::Greater
        );
    }

    #[tokio::test]
    async fn test_compare_chains_equal_score_longer_wins() {
        let consensus = production_consensus();

        // Both chains: all out-of-turn (score = 0 each)
        let mut chain_a = Vec::new();
        for i in 0u64..4 {
            let wrong_idx = ((i as usize) + 1) % 3;
            chain_a.push(build_signed_header(i, wrong_idx).await);
        }

        let mut chain_b = Vec::new();
        for i in 0u64..3 {
            let wrong_idx = ((i as usize) + 1) % 3;
            chain_b.push(build_signed_header(i, wrong_idx).await);
        }

        // Same score (0), but chain_a is longer → chain_a wins
        assert_eq!(
            consensus.compare_chains(&chain_a, &chain_b),
            std::cmp::Ordering::Greater
        );
    }

    #[tokio::test]
    async fn test_score_chain_empty() {
        let consensus = production_consensus();
        assert_eq!(consensus.score_chain(&[]), 0);
    }

    // ─── State Sync / Chain Validation Tests ─────────────────────────────

    /// Helper: build a chain segment of N signed blocks with proper parent linkage.
    async fn build_chain_segment(
        start_block: u64,
        count: u64,
        parent_hash: B256,
    ) -> Vec<(Header, SealedHeader<Header>)> {
        let mut chain = Vec::new();
        let mut prev_hash = parent_hash;

        for i in 0..count {
            let block_num = start_block + i;
            let signer_idx = (block_num as usize) % 3; // round-robin across 3 signers

            let manager = Arc::new(SignerManager::new());
            let address = manager
                .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[signer_idx])
                .await
                .unwrap();
            let sealer = BlockSealer::new(manager);

            let header = Header {
                number: block_num,
                parent_hash: prev_hash,
                gas_limit: 30_000_000,
                timestamp: 1000 + block_num * 2,
                extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
                ..Default::default()
            };

            let signed = sealer.seal_header(header, &address).await.unwrap();
            let sealed = SealedHeader::seal_slow(signed.clone());

            prev_hash = sealed.hash();
            chain.push((signed, sealed));
        }
        chain
    }

    #[tokio::test]
    async fn test_sync_chain_of_10_blocks() {
        let consensus = production_consensus();
        let chain = build_chain_segment(1, 10, B256::ZERO).await;

        // Validate each block sequentially (simulates full sync)
        for (i, (_, sealed)) in chain.iter().enumerate() {
            // validate_header: checks signature
            let result: Result<(), ConsensusError> =
                HeaderValidator::validate_header(&consensus, sealed);
            assert!(
                result.is_ok(),
                "Block {} header validation failed: {:?}",
                i + 1,
                result
            );

            // validate_header_against_parent: checks parent hash, number, timestamp
            if i > 0 {
                let (_, parent) = &chain[i - 1];
                let result: Result<(), ConsensusError> =
                    HeaderValidator::validate_header_against_parent(&consensus, sealed, parent);
                assert!(
                    result.is_ok(),
                    "Block {} parent validation failed: {:?}",
                    i + 1,
                    result
                );
            }
        }
    }

    #[tokio::test]
    async fn test_sync_rejects_tampered_signature() {
        let consensus = production_consensus();
        let chain = build_chain_segment(1, 3, B256::ZERO).await;

        // Tamper with the signature of block 2 by zeroing it out entirely
        let (mut tampered_header, _) = chain[1].clone();
        let len = tampered_header.extra_data.len();
        let mut extra = tampered_header.extra_data.to_vec();
        // Zero out the entire 65-byte signature
        for byte in extra[len - EXTRA_SEAL_LENGTH..].iter_mut() {
            *byte = 0;
        }
        tampered_header.extra_data = extra.into();

        let sealed_tampered = SealedHeader::seal_slow(tampered_header);
        let result: Result<(), ConsensusError> =
            HeaderValidator::validate_header(&consensus, &sealed_tampered);
        assert!(result.is_err(), "Tampered block should be rejected");
    }

    #[tokio::test]
    async fn test_sync_rejects_wrong_parent_hash() {
        let consensus = production_consensus();
        let chain = build_chain_segment(1, 3, B256::ZERO).await;

        // Create block 2 with wrong parent hash
        let manager = Arc::new(SignerManager::new());
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[1])
            .await
            .unwrap();
        let sealer = BlockSealer::new(manager);

        let bad_header = Header {
            number: 2,
            parent_hash: B256::from([0xAA; 32]), // wrong parent
            gas_limit: 30_000_000,
            timestamp: 1004,
            extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
            ..Default::default()
        };
        let signed = sealer.seal_header(bad_header, &address).await.unwrap();
        let sealed = SealedHeader::seal_slow(signed);

        let (_, parent) = &chain[0];
        let result: Result<(), ConsensusError> =
            HeaderValidator::validate_header_against_parent(&consensus, &sealed, parent);
        assert!(result.is_err(), "Wrong parent hash should be rejected");
    }

    #[tokio::test]
    async fn test_sync_rejects_unauthorized_signer() {
        let consensus = production_consensus();

        // Sign a block with signer index 5 (not in the 3-signer dev chain)
        let manager = Arc::new(SignerManager::new());
        let address = manager
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[5])
            .await
            .unwrap();
        let sealer = BlockSealer::new(manager);

        let header = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 1002,
            extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
            ..Default::default()
        };
        let signed = sealer.seal_header(header, &address).await.unwrap();
        let sealed = SealedHeader::seal_slow(signed);

        let result: Result<(), ConsensusError> =
            HeaderValidator::validate_header(&consensus, &sealed);
        assert!(result.is_err(), "Unauthorized signer should be rejected");
    }

    #[tokio::test]
    async fn test_sync_long_chain_100_blocks() {
        let consensus = production_consensus();
        let chain = build_chain_segment(1, 100, B256::ZERO).await;

        // All 100 blocks should validate
        for (i, (_, sealed)) in chain.iter().enumerate() {
            let result: Result<(), ConsensusError> =
                HeaderValidator::validate_header(&consensus, sealed);
            assert!(result.is_ok(), "Block {} validation failed", i + 1);
        }

        // Verify parent chain linkage
        for i in 1..chain.len() {
            let (_, sealed) = &chain[i];
            let (_, parent) = &chain[i - 1];
            let result: Result<(), ConsensusError> =
                HeaderValidator::validate_header_against_parent(&consensus, sealed, parent);
            assert!(result.is_ok(), "Block {} parent check failed", i + 1);
        }
    }

    // ─── 3-Signer Network Simulation ─────────────────────────────────────

    #[tokio::test]
    async fn test_3_signer_round_robin_production() {
        // Create 3 independent consensus instances (same chain spec, same signers)
        let chain = Arc::new(crate::chainspec::PoaChainSpec::dev_chain());
        let nodes: Vec<PoaConsensus> = (0..3).map(|_| PoaConsensus::new(chain.clone())).collect();

        // Each node has a different signer key
        let mut sealers = Vec::new();
        let mut addresses = Vec::new();
        for i in 0..3 {
            let mgr = Arc::new(SignerManager::new());
            let addr = mgr
                .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[i])
                .await
                .unwrap();
            addresses.push(addr);
            sealers.push(BlockSealer::new(mgr));
        }

        // Build 9 blocks in round-robin (3 full rotations)
        let mut prev_hash = B256::ZERO;
        let mut prev_sealed: Option<SealedHeader<Header>> = None;
        for block_num in 1u64..=9 {
            let signer_idx = (block_num as usize) % 3;

            let header = Header {
                number: block_num,
                parent_hash: prev_hash,
                gas_limit: 30_000_000,
                timestamp: 1000 + block_num * 2,
                extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
                ..Default::default()
            };

            let signed = sealers[signer_idx]
                .seal_header(header, &addresses[signer_idx])
                .await
                .unwrap();
            let sealed = SealedHeader::seal_slow(signed);

            // ALL 3 nodes must validate this block
            for (node_idx, node) in nodes.iter().enumerate() {
                let result: Result<(), ConsensusError> =
                    HeaderValidator::validate_header(node, &sealed);
                assert!(
                    result.is_ok(),
                    "Node {} rejected block {} signed by signer {}: {:?}",
                    node_idx,
                    block_num,
                    signer_idx,
                    result
                );

                if let Some(ref parent) = prev_sealed {
                    let result: Result<(), ConsensusError> =
                        HeaderValidator::validate_header_against_parent(node, &sealed, parent);
                    assert!(
                        result.is_ok(),
                        "Node {} rejected block {} parent chain: {:?}",
                        node_idx,
                        block_num,
                        result
                    );
                }
            }

            prev_hash = sealed.hash();
            prev_sealed = Some(sealed);
        }
    }

    #[tokio::test]
    async fn test_3_signer_out_of_turn_accepted() {
        // When a signer is offline, another signer produces the block out-of-turn
        let chain = Arc::new(crate::chainspec::PoaChainSpec::dev_chain());
        let consensus = PoaConsensus::new(chain);

        // Block 1 should be signer 1's turn, but signer 0 produces it (out-of-turn)
        let header = build_signed_header(1, 0).await;
        let sealed = SealedHeader::seal_slow(header.clone());

        // Out-of-turn blocks should still be accepted
        let result: Result<(), ConsensusError> =
            HeaderValidator::validate_header(&consensus, &sealed);
        assert!(result.is_ok(), "Out-of-turn block should be accepted");

        // But fork choice should prefer in-turn
        let in_turn_header = build_signed_header(1, 1).await;
        assert_eq!(consensus.is_in_turn(&header), Some(false));
        assert_eq!(consensus.is_in_turn(&in_turn_header), Some(true));
    }

    #[tokio::test]
    async fn test_3_signer_unauthorized_signer_rejected() {
        let chain = Arc::new(crate::chainspec::PoaChainSpec::dev_chain());
        let nodes: Vec<PoaConsensus> = (0..3).map(|_| PoaConsensus::new(chain.clone())).collect();

        // Signer 5 is NOT in the 3-signer dev chain
        let mgr = Arc::new(SignerManager::new());
        let addr = mgr
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[5])
            .await
            .unwrap();
        let sealer = BlockSealer::new(mgr);

        let header = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 1002,
            extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
            ..Default::default()
        };
        let signed = sealer.seal_header(header, &addr).await.unwrap();
        let sealed = SealedHeader::seal_slow(signed);

        // ALL 3 nodes must reject this unauthorized block
        for (node_idx, node) in nodes.iter().enumerate() {
            let result: Result<(), ConsensusError> =
                HeaderValidator::validate_header(node, &sealed);
            assert!(
                result.is_err(),
                "Node {} should reject unauthorized signer",
                node_idx
            );
        }
    }

    #[tokio::test]
    async fn test_3_signer_missed_turns_and_catchup() {
        // Simulate: signer 1 is offline for blocks 1-3, signer 0 fills in
        let chain = Arc::new(crate::chainspec::PoaChainSpec::dev_chain());
        let consensus = PoaConsensus::new(chain);

        let mut prev_hash = B256::ZERO;
        let mut headers = Vec::new();

        for block_num in 1u64..=6 {
            // Signer 0 produces ALL blocks (some in-turn, some out-of-turn)
            let header = Header {
                number: block_num,
                parent_hash: prev_hash,
                gas_limit: 30_000_000,
                timestamp: 1000 + block_num * 2,
                extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
                ..Default::default()
            };

            let mgr = Arc::new(SignerManager::new());
            let addr = mgr
                .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0])
                .await
                .unwrap();
            let sealer = BlockSealer::new(mgr);
            let signed = sealer.seal_header(header, &addr).await.unwrap();
            let sealed = SealedHeader::seal_slow(signed.clone());

            // All blocks should be valid (signer 0 is authorized even out-of-turn)
            let result: Result<(), ConsensusError> =
                HeaderValidator::validate_header(&consensus, &sealed);
            assert!(result.is_ok(), "Block {} should be valid", block_num);

            prev_hash = sealed.hash();
            headers.push(signed);
        }

        // Check in-turn vs out-of-turn
        // Dev signers: index 0, 1, 2 — signer 0 is in-turn at block 0, 3, 6, ...
        assert_eq!(consensus.is_in_turn(&headers[2]), Some(true)); // block 3 → idx 0 ✓
        assert_eq!(consensus.is_in_turn(&headers[0]), Some(false)); // block 1 → idx 1 expected, got 0
        assert_eq!(consensus.is_in_turn(&headers[1]), Some(false)); // block 2 → idx 2 expected, got 0
    }

    // ─── Multi-Node Integration Tests ────────────────────────────────────

    /// Helper: create a consensus with a custom signer list
    fn consensus_with_signers(signer_addrs: Vec<Address>) -> PoaConsensus {
        use crate::chainspec::{PoaChainSpec, PoaConfig};
        let genesis = crate::genesis::create_dev_genesis();
        let poa_config = PoaConfig {
            period: 2,
            epoch: 10, // short epoch for testing
            signers: signer_addrs,
        };
        let chain = Arc::new(PoaChainSpec::new(genesis, poa_config));
        PoaConsensus::new(chain)
    }

    /// Helper: derive address from a dev private key index
    async fn dev_address(key_index: usize) -> Address {
        let mgr = Arc::new(SignerManager::new());
        mgr.add_signer_from_hex(dev::DEV_PRIVATE_KEYS[key_index])
            .await
            .unwrap()
    }

    #[tokio::test]
    async fn test_multi_node_5_signer_sequential() {
        // 5-signer production-like setup: signers 0-4
        let mut addrs = Vec::new();
        for i in 0..5 {
            addrs.push(dev_address(i).await);
        }

        let consensus = consensus_with_signers(addrs.clone());

        // Build 15 blocks (3 full rotations)
        let mut prev_hash = B256::ZERO;
        for block_num in 1u64..=15 {
            let signer_idx = (block_num as usize) % 5;
            let mgr = Arc::new(SignerManager::new());
            let addr = mgr
                .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[signer_idx])
                .await
                .unwrap();
            let sealer = BlockSealer::new(mgr);

            let header = Header {
                number: block_num,
                parent_hash: prev_hash,
                gas_limit: 30_000_000,
                timestamp: 1000 + block_num * 2,
                extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
                ..Default::default()
            };
            let signed = sealer.seal_header(header, &addr).await.unwrap();
            let sealed = SealedHeader::seal_slow(signed);

            let result: Result<(), ConsensusError> =
                HeaderValidator::validate_header(&consensus, &sealed);
            assert!(
                result.is_ok(),
                "Block {} by signer {} failed",
                block_num,
                signer_idx
            );

            prev_hash = sealed.hash();
        }
    }

    #[tokio::test]
    async fn test_multi_node_signer_addition_at_epoch() {
        // Start with 3 signers, at epoch block (block 10) expand to 5
        let mut initial_addrs = Vec::new();
        for i in 0..3 {
            initial_addrs.push(dev_address(i).await);
        }

        let consensus = consensus_with_signers(initial_addrs.clone());

        // Before epoch: blocks 1-9, signers 0-2
        for block_num in 1u64..=9 {
            let signer_idx = (block_num as usize) % 3;
            let header = build_signed_header(block_num, signer_idx).await;
            let sealed = SealedHeader::seal_slow(header);
            let result: Result<(), ConsensusError> =
                HeaderValidator::validate_header(&consensus, &sealed);
            assert!(result.is_ok(), "Pre-epoch block {} failed", block_num);
        }

        // Update live signers to include signers 3 and 4 (simulates epoch update)
        let mut expanded = Vec::new();
        for i in 0..5 {
            expanded.push(dev_address(i).await);
        }
        consensus.chain_spec.update_live_signers(expanded);

        // After epoch: signer 3 and 4 should now be accepted
        let header = build_signed_header(10, 3).await;
        let sealed = SealedHeader::seal_slow(header);
        let result: Result<(), ConsensusError> =
            HeaderValidator::validate_header(&consensus, &sealed);
        assert!(
            result.is_ok(),
            "Signer 3 should be accepted after epoch update"
        );

        let header = build_signed_header(11, 4).await;
        let sealed = SealedHeader::seal_slow(header);
        let result: Result<(), ConsensusError> =
            HeaderValidator::validate_header(&consensus, &sealed);
        assert!(
            result.is_ok(),
            "Signer 4 should be accepted after epoch update"
        );
    }

    #[tokio::test]
    async fn test_multi_node_signer_removal_at_epoch() {
        // Start with 5 signers, at epoch block remove signers 3 and 4
        let mut all_addrs = Vec::new();
        for i in 0..5 {
            all_addrs.push(dev_address(i).await);
        }

        let consensus = consensus_with_signers(all_addrs.clone());

        // Pre-epoch: signer 4 produces a block successfully
        let header = build_signed_header(4, 4).await;
        let sealed = SealedHeader::seal_slow(header);
        let result: Result<(), ConsensusError> =
            HeaderValidator::validate_header(&consensus, &sealed);
        assert!(result.is_ok(), "Signer 4 should be valid before removal");

        // Simulate signer removal at epoch: only signers 0-2 remain
        let mut reduced = Vec::new();
        for i in 0..3 {
            reduced.push(dev_address(i).await);
        }
        consensus.chain_spec.update_live_signers(reduced);

        // After epoch: signer 4 should be rejected
        let header = build_signed_header(11, 4).await;
        let sealed = SealedHeader::seal_slow(header);
        let result: Result<(), ConsensusError> =
            HeaderValidator::validate_header(&consensus, &sealed);
        assert!(result.is_err(), "Removed signer 4 should be rejected");
    }

    #[tokio::test]
    async fn test_multi_node_fork_choice_prefers_in_turn() {
        let consensus = production_consensus();

        // Chain A: signer 0 produces all blocks (some in-turn, some out-of-turn)
        let mut chain_a = Vec::new();
        for i in 0u64..6 {
            chain_a.push(build_signed_header(i, 0).await);
        }

        // Chain B: proper round-robin (all in-turn)
        let mut chain_b = Vec::new();
        for i in 0u64..6 {
            let signer_idx = (i as usize) % 3;
            chain_b.push(build_signed_header(i, signer_idx).await);
        }

        // Score: chain_a has 2 in-turn blocks (0, 3), chain_b has 6 in-turn
        let score_a = consensus.score_chain(&chain_a);
        let score_b = consensus.score_chain(&chain_b);
        assert!(score_b > score_a, "Round-robin chain should score higher");

        // Fork choice should prefer chain B
        assert_eq!(
            consensus.compare_chains(&chain_b, &chain_a),
            std::cmp::Ordering::Greater,
            "Round-robin chain should be preferred"
        );
    }

    #[tokio::test]
    async fn test_multi_node_double_sign_detection() {
        // Two different blocks at the same height by the same signer
        let consensus = production_consensus();

        // Block A at height 1 by signer 0
        let header_a = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 1002,
            extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
            ..Default::default()
        };

        // Block B at height 1 by signer 0 (different state root)
        let header_b = Header {
            number: 1,
            gas_limit: 30_000_000,
            timestamp: 1002,
            state_root: B256::from([0x11; 32]),
            extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
            ..Default::default()
        };

        let mgr = Arc::new(SignerManager::new());
        let addr = mgr
            .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0])
            .await
            .unwrap();
        let sealer = BlockSealer::new(mgr);

        let signed_a = sealer.seal_header(header_a, &addr).await.unwrap();
        let signed_b = sealer.seal_header(header_b, &addr).await.unwrap();

        // Both blocks are individually valid
        let sealed_a = SealedHeader::seal_slow(signed_a.clone());
        let sealed_b = SealedHeader::seal_slow(signed_b.clone());

        let result_a: Result<(), ConsensusError> =
            HeaderValidator::validate_header(&consensus, &sealed_a);
        let result_b: Result<(), ConsensusError> =
            HeaderValidator::validate_header(&consensus, &sealed_b);

        assert!(result_a.is_ok());
        assert!(result_b.is_ok());

        // But they have different hashes (different state_root → different seal_hash → different sig)
        assert_ne!(
            sealed_a.hash(),
            sealed_b.hash(),
            "Double-signed blocks at same height should have different hashes"
        );

        // Recover signer from both — same signer produced both (double signing evidence)
        let signer_a = consensus.recover_signer(&signed_a).unwrap();
        let signer_b = consensus.recover_signer(&signed_b).unwrap();
        assert_eq!(signer_a, signer_b, "Same signer produced both blocks");
        assert_eq!(signer_a, addr);
    }

    #[tokio::test]
    async fn test_multi_node_chain_reorganization() {
        let consensus = production_consensus();

        // Common prefix: blocks 1-3 (round-robin)
        let mut common = Vec::new();
        let mut prev_hash = B256::ZERO;
        for i in 1u64..=3 {
            let signer_idx = (i as usize) % 3;
            let mgr = Arc::new(SignerManager::new());
            let addr = mgr
                .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[signer_idx])
                .await
                .unwrap();
            let sealer = BlockSealer::new(mgr);
            let header = Header {
                number: i,
                parent_hash: prev_hash,
                gas_limit: 30_000_000,
                timestamp: 1000 + i * 2,
                extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
                ..Default::default()
            };
            let signed = sealer.seal_header(header, &addr).await.unwrap();
            let sealed = SealedHeader::seal_slow(signed.clone());
            prev_hash = sealed.hash();
            common.push(signed);
        }

        // Fork A: blocks 4-6, all by signer 0 (out-of-turn for 4 and 5)
        let mut fork_a = Vec::new();
        let mut hash_a = prev_hash;
        for i in 4u64..=6 {
            let mgr = Arc::new(SignerManager::new());
            let addr = mgr
                .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[0])
                .await
                .unwrap();
            let sealer = BlockSealer::new(mgr);
            let header = Header {
                number: i,
                parent_hash: hash_a,
                gas_limit: 30_000_000,
                timestamp: 1000 + i * 2,
                extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
                ..Default::default()
            };
            let signed = sealer.seal_header(header, &addr).await.unwrap();
            let sealed = SealedHeader::seal_slow(signed.clone());
            hash_a = sealed.hash();
            fork_a.push(signed);
        }

        // Fork B: blocks 4-6, proper round-robin (all in-turn)
        let mut fork_b = Vec::new();
        let mut hash_b = prev_hash;
        for i in 4u64..=6 {
            let signer_idx = (i as usize) % 3;
            let mgr = Arc::new(SignerManager::new());
            let addr = mgr
                .add_signer_from_hex(dev::DEV_PRIVATE_KEYS[signer_idx])
                .await
                .unwrap();
            let sealer = BlockSealer::new(mgr);
            let header = Header {
                number: i,
                parent_hash: hash_b,
                gas_limit: 30_000_000,
                timestamp: 1000 + i * 2,
                extra_data: vec![0u8; EXTRA_VANITY_LENGTH + EXTRA_SEAL_LENGTH].into(),
                ..Default::default()
            };
            let signed = sealer.seal_header(header, &addr).await.unwrap();
            let sealed = SealedHeader::seal_slow(signed.clone());
            hash_b = sealed.hash();
            fork_b.push(signed);
        }

        // Both forks are valid
        for h in &fork_a {
            let sealed = SealedHeader::seal_slow(h.clone());
            let r: Result<(), ConsensusError> =
                HeaderValidator::validate_header(&consensus, &sealed);
            assert!(r.is_ok(), "Fork A block should be valid");
        }
        for h in &fork_b {
            let sealed = SealedHeader::seal_slow(h.clone());
            let r: Result<(), ConsensusError> =
                HeaderValidator::validate_header(&consensus, &sealed);
            assert!(r.is_ok(), "Fork B block should be valid");
        }

        // Fork choice: B should win (more in-turn blocks)
        let score_a = consensus.score_chain(&fork_a);
        let score_b = consensus.score_chain(&fork_b);
        assert!(
            score_b > score_a,
            "Fork B (round-robin) should score higher than Fork A (single signer)"
        );
    }
}

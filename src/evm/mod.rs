//! Custom EVM configuration for Meowchain (Phase 2).
//!
//! Provides [`PoaEvmFactory`] — a wrapper around Reth's [`EthEvmFactory`] that applies
//! POA-specific EVM overrides before creating each EVM instance:
//!
//! - **Max contract code size** (`limit_contract_code_size`): Lifts EIP-170's 24 KB cap.
//! - **Calldata gas reduction** (Phase 2.12): [`CalldataDiscountInspector`] implements the
//!   discount logic via [`Inspector::initialize_interp`] + `Gas::erase_cost`.
//!   It is a standalone utility that callers wrap explicitly:
//!   `factory.create_evm_with_inspector(db, env, CalldataDiscountInspector::new(my_insp, 4))`
//!   The stored `calldata_gas_per_byte` field on `PoaEvmFactory` is available for a future
//!   custom `BlockExecutorFactory` that pre-processes `TxEnv` gas limits automatically.
//!
//! Also exposes [`PoaExecutorBuilder`] and [`parallel`] (Phase 2 item 13 foundation).
//!
//! # Architecture
//! ```text
//!   PoaNode → PoaExecutorBuilder.build_evm()
//!              → PoaEvmConfig::new(chain_spec, PoaEvmFactory)   [wraps EthEvmConfig]
//!                 ├─ evm_env / next_evm_env / evm_env_for_payload
//!                 │    → patch_cfg (contract size, EIP-7825 cap lift)
//!                 │      (pool-visible: tx pool reads these from evm_env)
//!                 └─ PoaEvmFactory::create_evm(db, env)
//!                      → patch_env (contract size limits)
//!                      → EthEvmFactory::create_evm(db, patched_env)
//! ```

pub mod parallel;

use alloy_evm::{
    eth::{EthEvm, EthEvmContext, EthEvmFactory},
    precompiles::PrecompilesMap,
    revm::{
        context::BlockEnv,
        context_interface::result::{EVMError, HaltReason},
        inspector::NoOpInspector,
        interpreter::{
            CallInput, CallInputs, CallOutcome, CreateInputs, CreateOutcome, Interpreter,
        },
        primitives::hardfork::SpecId,
        Inspector,
    },
    Database, EvmEnv, EvmFactory,
};
use alloy_primitives::{Address, Log, U256};

use alloy_consensus::Header;
use alloy_eips::Decodable2718;
use alloy_evm::eth::spec::EthExecutorSpec;
use alloy_evm::eth::{EthBlockExecutionCtx, EthBlockExecutorFactory};
use alloy_evm::revm::context::{CfgEnv, TxEnv};
use alloy_primitives::Bytes;
use alloy_rpc_types_engine::ExecutionData;
use core::convert::Infallible;
use reth_chainspec::{EthChainSpec, EthereumHardforks};
use reth_ethereum::evm::{EthBlockAssembler, RethReceiptBuilder};
use reth_ethereum::node::api::{FullNodeTypes, NodeTypes};
use reth_ethereum::node::builder::{components::ExecutorBuilder, BuilderContext};
use reth_ethereum::node::EthEvmConfig;
use reth_ethereum::{EthPrimitives, TransactionSigned};
use reth_ethereum_forks::Hardforks;
use reth_evm::{
    ConfigureEngineEvm, ConfigureEvm, EvmEnvFor, ExecutableTxIterator, ExecutionCtxFor,
    NextBlockEnvAttributes,
};
use reth_primitives_traits::{SealedBlock, SealedHeader, SignedTransaction};
use reth_storage_api::errors::any::AnyError;
use std::sync::Arc;

// ─── Calldata gas discount inspector ──────────────────────────────────────────

/// Inspector wrapper that grants a calldata gas discount at the start of each
/// top-level transaction frame.
///
/// Ethereum's intrinsic gas deducts **16 gas per non-zero calldata byte** (EIP-2028)
/// before execution starts.  A POA chain can effectively reduce this by adding back
/// the difference via `Gas::erase_cost` inside [`Inspector::initialize_interp`].
///
/// The discount is applied only **once per EVM instance** (tracked by `discount_applied`).
/// Because reth creates a fresh `EthEvmFactory` call — and therefore a fresh
/// `CalldataDiscountInspector` — for each transaction, the flag resets automatically.
///
/// # Parameters
/// - `calldata_gas_per_byte = 16` (default) → no-op, matches Ethereum mainnet.
/// - `calldata_gas_per_byte = 4`  → discount `12 × non_zero_bytes` gas, making
///   non-zero bytes as cheap as zero bytes.
/// - `calldata_gas_per_byte = 1`  → near-free calldata, maximises throughput.
///
/// # Note on `Clone`
/// This type is intentionally **not** `Clone`.  It is created once per EVM instance
/// (i.e. per transaction) and discarded afterwards; cloning it would be a logic error
/// because the `discount_applied` flag would be copied in a potentially stale state.
#[derive(Debug)]
pub struct CalldataDiscountInspector<I> {
    inner: I,
    /// Pre-computed discount per non-zero byte: `16 - calldata_gas_per_byte`.
    ///
    /// Stored at construction to avoid recomputing the subtraction on every
    /// `discount_for` call and every `initialize_interp` hot-path check.
    /// Zero when `calldata_gas_per_byte == 16` (mainnet, no discount).
    ///
    /// Adjacent to `discount_applied` so both hot-path fields (`discount_per_byte`
    /// and `discount_applied`) are on the same cache line.
    discount_per_byte: u64,
    /// Set to `true` after the discount has been applied for this EVM instance.
    discount_applied: bool,
    /// Replacement cost per non-zero calldata byte (1–16 gas).
    ///
    /// Cold field — only used at construction time.  Placed last to keep the
    /// hot fields (`discount_per_byte`, `discount_applied`) together.
    _calldata_gas_per_byte: u64,
}

impl<I> CalldataDiscountInspector<I> {
    /// Create a new inspector wrapping `inner` with the given calldata gas cost.
    pub fn new(inner: I, calldata_gas_per_byte: u64) -> Self {
        let clamped = calldata_gas_per_byte.clamp(1, 16);
        Self {
            inner,
            discount_per_byte: 16u64 - clamped, // clamped ≤ 16, so no underflow
            discount_applied: false,
            _calldata_gas_per_byte: clamped,
        }
    }

    /// Returns the discount in gas for a given number of non-zero calldata bytes.
    #[inline]
    pub fn discount_for(&self, non_zero_bytes: u64) -> u64 {
        non_zero_bytes * self.discount_per_byte
    }

    /// Consume the wrapper and return the inner inspector.
    #[inline]
    pub fn into_inner(self) -> I {
        self.inner
    }

    /// Borrow the inner inspector.
    #[inline]
    pub fn inner(&self) -> &I {
        &self.inner
    }

    /// Mutably borrow the inner inspector.
    #[inline]
    pub fn inner_mut(&mut self) -> &mut I {
        &mut self.inner
    }
}

impl<CTX, I: Inspector<CTX>> Inspector<CTX> for CalldataDiscountInspector<I> {
    fn initialize_interp(&mut self, interp: &mut Interpreter, context: &mut CTX) {
        // Apply discount once per tx (discount_applied resets when a new EVM is created).
        // Fast path: discount_per_byte == 0 means calldata_gas_per_byte == 16 (mainnet),
        // so nothing to do.  This avoids the branch and byte-counting entirely.
        if !self.discount_applied && self.discount_per_byte > 0 {
            self.discount_applied = true;
            // interp.input is InputsImpl (EthInterpreter default); .input is CallInput.
            let non_zero = match &interp.input.input {
                CallInput::Bytes(bytes) => bytes.iter().filter(|&&b| b != 0).count() as u64,
                CallInput::SharedBuffer(_) => 0, // shared-memory slice: skip (sub-call context)
            };
            // discount_for is now a single multiply; skip erase_cost when result is zero.
            let discount = self.discount_for(non_zero);
            if discount > 0 {
                interp.gas.erase_cost(discount);
            }
        }
        self.inner.initialize_interp(interp, context);
    }

    #[inline]
    fn step(&mut self, interp: &mut Interpreter, context: &mut CTX) {
        self.inner.step(interp, context);
    }

    #[inline]
    fn step_end(&mut self, interp: &mut Interpreter, context: &mut CTX) {
        self.inner.step_end(interp, context);
    }

    #[inline]
    fn log(&mut self, context: &mut CTX, log: Log) {
        self.inner.log(context, log);
    }

    #[inline]
    fn call(&mut self, context: &mut CTX, inputs: &mut CallInputs) -> Option<CallOutcome> {
        self.inner.call(context, inputs)
    }

    #[inline]
    fn call_end(&mut self, context: &mut CTX, inputs: &CallInputs, outcome: &mut CallOutcome) {
        self.inner.call_end(context, inputs, outcome);
    }

    #[inline]
    fn create(&mut self, context: &mut CTX, inputs: &mut CreateInputs) -> Option<CreateOutcome> {
        self.inner.create(context, inputs)
    }

    #[inline]
    fn create_end(
        &mut self,
        context: &mut CTX,
        inputs: &CreateInputs,
        outcome: &mut CreateOutcome,
    ) {
        self.inner.create_end(context, inputs, outcome);
    }

    #[inline]
    fn selfdestruct(&mut self, contract: Address, target: Address, value: U256) {
        self.inner.selfdestruct(contract, target, value);
    }
}

// ─── PoaEvmFactory ────────────────────────────────────────────────────────────

/// POA-customised EVM factory.
///
/// Wraps [`EthEvmFactory`] and injects POA-specific `CfgEnv` overrides:
///
/// 1. `limit_contract_code_size` — lifts EIP-170's 24 KB bytecode cap.
/// 2. Calldata gas discount — wraps every created EVM with
///    [`CalldataDiscountInspector`] so non-zero calldata bytes cost
///    `calldata_gas_per_byte` instead of the Ethereum default of 16.
/// 3. Zero-gas mode — disables base fee validation so `gasPrice: 0` txs execute.
#[derive(Debug, Clone)]
pub struct PoaEvmFactory {
    inner: EthEvmFactory,
    /// Optional override for maximum deployed contract code size.
    ///
    /// `None` → Ethereum default (24,576 bytes, EIP-170).
    /// `Some(n)` → contracts larger than `n` bytes are rejected at deployment.
    pub max_contract_size: Option<usize>,
    /// Gas cost per non-zero calldata byte (1–16).
    ///
    /// Ethereum mainnet: 16.  POA default: 4 (same as zero bytes — effectively
    /// free relative to zero bytes, maximises L2-style throughput).
    pub calldata_gas_per_byte: u64,
    /// When `true`, the genesis sets `base_fee = 0` so that transactions with
    /// `gasPrice: 0` / `maxFeePerGas: 0` are accepted (base fee check passes
    /// trivially since any `max_fee_per_gas >= 0`).
    pub zero_gas: bool,
    /// Pre-computed flag: `true` if `patch_env` has any work to do.
    ///
    /// Allows the hot-path EVM creation calls (`create_evm` / `create_evm_with_inspector`)
    /// to skip the `patch_env` call entirely when all overrides are at their defaults
    /// (`max_contract_size = None`).
    needs_env_patch: bool,
}

impl Default for PoaEvmFactory {
    fn default() -> Self {
        Self {
            inner: EthEvmFactory::default(),
            max_contract_size: None,
            calldata_gas_per_byte: 4, // POA default: reduce calldata cost
            zero_gas: false,
            needs_env_patch: false, // no CfgEnv overrides active by default
        }
    }
}

impl PoaEvmFactory {
    /// Create a factory with custom contract size, calldata gas, and zero-gas overrides.
    ///
    /// `calldata_gas_per_byte` is clamped to `[1, 16]`.
    /// Pass `16` to disable the calldata discount (Ethereum mainnet behaviour).
    /// Pass `zero_gas = true` to disable base fee validation (accept gasPrice=0 txs).
    pub fn new(
        max_contract_size: Option<usize>,
        calldata_gas_per_byte: u64,
        zero_gas: bool,
    ) -> Self {
        let needs_env_patch = max_contract_size.is_some();
        Self {
            inner: EthEvmFactory::default(),
            max_contract_size,
            calldata_gas_per_byte: calldata_gas_per_byte.clamp(1, 16),
            zero_gas,
            needs_env_patch,
        }
    }

    /// Apply POA-specific `CfgEnv` overrides to an [`EvmEnv`] before EVM creation.
    ///
    /// Only called when `needs_env_patch` is `true`; callers must check that flag
    /// before invoking this to avoid the function-call overhead on the hot path.
    #[inline]
    fn patch_env<S, B>(&self, mut env: EvmEnv<S, B>) -> EvmEnv<S, B> {
        if let Some(limit) = self.max_contract_size {
            env.cfg_env.limit_contract_code_size = Some(limit);
            // Also lift the initcode size limit (EIP-3860) proportionally.
            env.cfg_env.limit_contract_initcode_size = Some(limit * 2);
        }
        env
    }

    /// Whether the calldata discount is active (i.e. cheaper than mainnet).
    #[inline]
    pub fn has_calldata_discount(&self) -> bool {
        self.calldata_gas_per_byte < 16
    }
}

impl EvmFactory for PoaEvmFactory {
    // Use the standard inspector passthrough — the `EvmFactory` trait requires
    // `Evm::Inspector == I`, so we cannot transparently wrap `I` with
    // `CalldataDiscountInspector<I>` here.  Use `CalldataDiscountInspector`
    // explicitly when creating an EVM with inspector if the discount is needed.
    type Evm<DB: Database, I: Inspector<Self::Context<DB>>> = EthEvm<DB, I, PrecompilesMap>;
    type Context<DB: Database> = EthEvmContext<DB>;
    type Tx = TxEnv;
    type Error<DBError: core::error::Error + Send + Sync + 'static> = EVMError<DBError>;
    type HaltReason = HaltReason;
    type Spec = SpecId;
    type BlockEnv = BlockEnv;
    type Precompiles = PrecompilesMap;

    fn create_evm<DB: Database>(
        &self,
        db: DB,
        input: EvmEnv<Self::Spec, Self::BlockEnv>,
    ) -> Self::Evm<DB, NoOpInspector> {
        // Skip patch_env entirely when no CfgEnv overrides are active.
        let env = if self.needs_env_patch {
            self.patch_env(input)
        } else {
            input
        };
        self.inner.create_evm(db, env)
    }

    fn create_evm_with_inspector<DB: Database, I: Inspector<Self::Context<DB>>>(
        &self,
        db: DB,
        input: EvmEnv<Self::Spec, Self::BlockEnv>,
        inspector: I,
    ) -> Self::Evm<DB, I> {
        let env = if self.needs_env_patch {
            self.patch_env(input)
        } else {
            input
        };
        self.inner.create_evm_with_inspector(db, env, inspector)
    }
}

// ─── PoaExecutorBuilder ───────────────────────────────────────────────────────

/// Custom executor builder that uses [`PoaEvmFactory`] for EVM creation.
///
/// Plugged into `PoaNode::components_builder` in place of
/// `EthereumExecutorBuilder`.  Passes through `max_contract_size`,
/// `calldata_gas_per_byte`, and `zero_gas` to the factory.
#[derive(Debug, Clone)]
pub struct PoaExecutorBuilder {
    /// Override for maximum deployed contract size.  `None` = Ethereum default.
    pub max_contract_size: Option<usize>,
    /// Gas cost per non-zero calldata byte (1–16). `16` = Ethereum mainnet default.
    pub calldata_gas_per_byte: u64,
    /// Zero-gas mode: disable base fee validation in the EVM.
    pub zero_gas: bool,
}

impl PoaExecutorBuilder {
    /// Create a builder with the given POA EVM settings.
    pub fn new(
        max_contract_size: Option<usize>,
        calldata_gas_per_byte: u64,
        zero_gas: bool,
    ) -> Self {
        Self {
            max_contract_size,
            calldata_gas_per_byte,
            zero_gas,
        }
    }
}

impl<Node> ExecutorBuilder<Node> for PoaExecutorBuilder
where
    Node: FullNodeTypes<
        Types: NodeTypes<
            ChainSpec: Hardforks
                           + EthExecutorSpec
                           + EthereumHardforks
                           + EthChainSpec<Header = Header>,
            Primitives = EthPrimitives,
        >,
    >,
{
    type EVM = PoaEvmConfig<<Node::Types as NodeTypes>::ChainSpec>;

    async fn build_evm(self, ctx: &BuilderContext<Node>) -> eyre::Result<Self::EVM> {
        Ok(PoaEvmConfig::new(
            ctx.chain_spec(),
            PoaEvmFactory::new(
                self.max_contract_size,
                self.calldata_gas_per_byte,
                self.zero_gas,
            ),
        ))
    }
}

// ─── PoaEvmConfig ─────────────────────────────────────────────────────────────

/// POA-customised [`ConfigureEvm`] implementation.
///
/// Wraps [`EthEvmConfig`] (parameterised with [`PoaEvmFactory`]) and patches the
/// `CfgEnv` of every environment it hands out (`evm_env`, `next_evm_env`,
/// `evm_env_for_payload`).  Patching here matters beyond execution: reth's
/// transaction pool reads `max_initcode_size` and `tx_gas_limit_cap` from the
/// tip block's `evm_env`, so overrides applied only inside
/// [`PoaEvmFactory::patch_env`] (at EVM creation) would be invisible to the
/// pool, which would then reject transactions the EVM executes fine.
///
/// Overrides applied:
/// 1. **Contract size** (`--max-contract-size`) — mirrors
///    [`PoaEvmFactory::patch_env`] so pool-side EIP-3860 initcode checks match
///    the EVM.
/// 2. **EIP-7825 neutralised** — `tx_gas_limit_cap = u64::MAX`.  Osaka caps a
///    single transaction at ~16.7M gas on mainnet; this chain runs 300M–1B gas
///    blocks and deploys contracts far past that cap, so the cap is lifted
///    unconditionally (pre-Osaka the effective default is unlimited anyway, so
///    this is a no-op until `--osaka-time` activates).
#[derive(Debug, Clone)]
pub struct PoaEvmConfig<ChainSpec> {
    inner: EthEvmConfig<ChainSpec, PoaEvmFactory>,
    /// Copied from the factory so `patch_cfg` needs no factory access.
    max_contract_size: Option<usize>,
}

impl<ChainSpec> PoaEvmConfig<ChainSpec> {
    /// Create a config from the chain spec and a fully-configured [`PoaEvmFactory`].
    pub fn new(chain_spec: Arc<ChainSpec>, factory: PoaEvmFactory) -> Self {
        let max_contract_size = factory.max_contract_size;
        Self {
            inner: EthEvmConfig::new_with_evm_factory(chain_spec, factory),
            max_contract_size,
        }
    }

    /// Apply the POA `CfgEnv` overrides (see type-level docs).
    fn patch_cfg(&self, cfg: &mut CfgEnv<SpecId>) {
        if let Some(limit) = self.max_contract_size {
            cfg.limit_contract_code_size = Some(limit);
            cfg.limit_contract_initcode_size = Some(limit * 2);
        }
        cfg.tx_gas_limit_cap = Some(u64::MAX);
    }
}

impl<ChainSpec> ConfigureEvm for PoaEvmConfig<ChainSpec>
where
    ChainSpec: EthExecutorSpec + EthChainSpec<Header = Header> + Hardforks + 'static,
{
    type Primitives = EthPrimitives;
    type Error = Infallible;
    type NextBlockEnvCtx = NextBlockEnvAttributes;
    type BlockExecutorFactory =
        EthBlockExecutorFactory<RethReceiptBuilder, Arc<ChainSpec>, PoaEvmFactory>;
    type BlockAssembler = EthBlockAssembler<ChainSpec>;

    fn block_executor_factory(&self) -> &Self::BlockExecutorFactory {
        self.inner.block_executor_factory()
    }

    fn block_assembler(&self) -> &Self::BlockAssembler {
        self.inner.block_assembler()
    }

    fn evm_env(&self, header: &Header) -> Result<EvmEnv<SpecId>, Self::Error> {
        let mut env = self.inner.evm_env(header)?;
        self.patch_cfg(&mut env.cfg_env);
        Ok(env)
    }

    fn next_evm_env(
        &self,
        parent: &Header,
        attributes: &NextBlockEnvAttributes,
    ) -> Result<EvmEnv<SpecId>, Self::Error> {
        let mut env = self.inner.next_evm_env(parent, attributes)?;
        self.patch_cfg(&mut env.cfg_env);
        Ok(env)
    }

    fn context_for_block<'a>(
        &self,
        block: &'a SealedBlock<reth_ethereum::Block>,
    ) -> Result<EthBlockExecutionCtx<'a>, Self::Error> {
        self.inner.context_for_block(block)
    }

    fn context_for_next_block(
        &self,
        parent: &SealedHeader,
        attributes: Self::NextBlockEnvCtx,
    ) -> Result<EthBlockExecutionCtx<'_>, Self::Error> {
        self.inner.context_for_next_block(parent, attributes)
    }
}

impl<ChainSpec> ConfigureEngineEvm<ExecutionData> for PoaEvmConfig<ChainSpec>
where
    ChainSpec: EthExecutorSpec + EthChainSpec<Header = Header> + Hardforks + 'static,
{
    fn evm_env_for_payload(&self, payload: &ExecutionData) -> Result<EvmEnvFor<Self>, Self::Error> {
        let mut env = self.inner.evm_env_for_payload(payload)?;
        self.patch_cfg(&mut env.cfg_env);
        Ok(env)
    }

    fn context_for_payload<'a>(
        &self,
        payload: &'a ExecutionData,
    ) -> Result<ExecutionCtxFor<'a, Self>, Self::Error> {
        self.inner.context_for_payload(payload)
    }

    fn tx_iterator_for_payload(
        &self,
        payload: &ExecutionData,
    ) -> Result<impl ExecutableTxIterator<Self>, Self::Error> {
        // Mirrors EthEvmConfig::tx_iterator_for_payload — the inner method's
        // return type is opaque (`impl ExecutableTxIterator<EthEvmConfig<..>>`),
        // so it cannot be delegated across the wrapper type.
        let txs = payload.payload.transactions().clone();
        let convert = |tx: Bytes| {
            let tx = TransactionSigned::decode_2718_exact(tx.as_ref()).map_err(AnyError::new)?;
            let signer = tx.try_recover().map_err(AnyError::new)?;
            Ok::<_, AnyError>(tx.with_signer(signer))
        };
        Ok((txs, convert))
    }
}

// ─── Tests ────────────────────────────────────────────────────────────────────

#[cfg(test)]
mod bench;

#[cfg(test)]
mod tests {
    use super::*;
    use alloy_evm::EvmEnv;

    fn make_env() -> EvmEnv<SpecId, BlockEnv> {
        EvmEnv::default()
    }

    // ── contract size ──────────────────────────────────────────────────────────

    #[test]
    fn test_poa_evm_factory_no_override_keeps_default() {
        let factory = PoaEvmFactory::new(None, 16, false);
        let patched = factory.patch_env(make_env());
        assert!(patched.cfg_env.limit_contract_code_size.is_none());
    }

    #[test]
    fn test_poa_evm_factory_applies_code_size_limit() {
        let factory = PoaEvmFactory::new(Some(524_288), 16, false); // 512 KB
        let patched = factory.patch_env(make_env());
        assert_eq!(patched.cfg_env.limit_contract_code_size, Some(524_288));
    }

    #[test]
    fn test_poa_evm_factory_sets_initcode_limit_double() {
        let factory = PoaEvmFactory::new(Some(131_072), 16, false); // 128 KB
        let patched = factory.patch_env(make_env());
        assert_eq!(
            patched.cfg_env.limit_contract_initcode_size,
            Some(131_072 * 2)
        );
    }

    #[test]
    fn test_poa_evm_factory_ethereum_default_is_24kb() {
        use alloy_evm::revm::primitives::eip170::MAX_CODE_SIZE;
        assert_eq!(MAX_CODE_SIZE, 24_576);
    }

    // ── calldata gas ───────────────────────────────────────────────────────────

    #[test]
    fn test_calldata_discount_inspector_discount_for_zero_bytes() {
        let inspector = CalldataDiscountInspector::new(NoOpInspector, 4);
        // 0 non-zero bytes → 0 discount
        assert_eq!(inspector.discount_for(0), 0);
    }

    #[test]
    fn test_calldata_discount_inspector_discount_at_4_gas() {
        let inspector = CalldataDiscountInspector::new(NoOpInspector, 4);
        // (16 - 4) * 100 = 1200
        assert_eq!(inspector.discount_for(100), 1200);
    }

    #[test]
    fn test_calldata_discount_inspector_no_discount_at_16_gas() {
        let inspector = CalldataDiscountInspector::new(NoOpInspector, 16);
        // (16 - 16) * 100 = 0
        assert_eq!(inspector.discount_for(100), 0);
    }

    #[test]
    fn test_calldata_discount_inspector_discount_at_1_gas() {
        let inspector = CalldataDiscountInspector::new(NoOpInspector, 1);
        // (16 - 1) * 50 = 750
        assert_eq!(inspector.discount_for(50), 750);
    }

    #[test]
    fn test_calldata_discount_inspector_clamps_cost_to_1() {
        // 0 would be invalid (division by zero risk) — clamp to 1
        // When clamped to 1: discount_per_byte = 16 - 1 = 15
        let inspector = CalldataDiscountInspector::new(NoOpInspector, 0);
        assert_eq!(inspector.discount_for(1), 15);
    }

    #[test]
    fn test_calldata_discount_inspector_clamps_cost_to_16() {
        // When clamped to 16 (mainnet): discount_per_byte = 16 - 16 = 0
        let inspector = CalldataDiscountInspector::new(NoOpInspector, 20);
        assert_eq!(inspector.discount_for(100), 0);
    }

    #[test]
    fn test_poa_evm_factory_default_calldata_gas_is_4() {
        let factory = PoaEvmFactory::default();
        assert_eq!(factory.calldata_gas_per_byte, 4);
        assert!(factory.has_calldata_discount());
    }

    #[test]
    fn test_poa_evm_factory_at_16_no_discount() {
        let factory = PoaEvmFactory::new(None, 16, false);
        assert!(!factory.has_calldata_discount());
    }

    // ── executor builder ───────────────────────────────────────────────────────

    #[test]
    fn test_poa_executor_builder_creation() {
        let builder = PoaExecutorBuilder::new(Some(524_288), 4, false);
        assert_eq!(builder.max_contract_size, Some(524_288));
        assert_eq!(builder.calldata_gas_per_byte, 4);
    }

    #[test]
    fn test_poa_executor_builder_no_override() {
        let builder = PoaExecutorBuilder::new(None, 16, false);
        assert!(builder.max_contract_size.is_none());
        assert_eq!(builder.calldata_gas_per_byte, 16);
    }

    #[test]
    fn test_patch_env_does_not_change_other_fields() {
        let factory = PoaEvmFactory::new(Some(65_536), 4, false);
        let env: EvmEnv = EvmEnv::default();
        let chain_id_before = env.cfg_env.chain_id;
        let patched = factory.patch_env(env);
        assert_eq!(patched.cfg_env.chain_id, chain_id_before);
        assert_eq!(patched.cfg_env.limit_contract_code_size, Some(65_536));
    }

    // ── zero-gas mode ────────────────────────────────────────────────────────
    // Zero-gas mode now works via genesis base_fee=0 rather than CfgEnv.disable_base_fee
    // (which is behind a feature flag in revm 36). The factory stores the flag for
    // genesis configuration; the EVM itself needs no CfgEnv override.

    #[test]
    fn test_zero_gas_flag_stored() {
        let factory = PoaEvmFactory::new(None, 16, true);
        assert!(factory.zero_gas);
    }

    #[test]
    fn test_zero_gas_default_is_false() {
        let factory = PoaEvmFactory::default();
        assert!(!factory.zero_gas);
    }

    #[test]
    fn test_poa_executor_builder_zero_gas() {
        let builder = PoaExecutorBuilder::new(None, 4, true);
        assert!(builder.zero_gas);
    }

    // ── PoaEvmConfig (pool-visible CfgEnv overrides) ──────────────────────────

    fn dev_evm_config(max_contract_size: Option<usize>) -> PoaEvmConfig<reth_chainspec::ChainSpec> {
        let chain = crate::chainspec::PoaChainSpec::dev_chain();
        // The node hands the inner reth ChainSpec to the executor builder
        // (PoaNode's NodeTypes::ChainSpec = ChainSpec) — mirror that here.
        PoaEvmConfig::new(
            Arc::clone(chain.inner()),
            PoaEvmFactory::new(max_contract_size, 4, false),
        )
    }

    #[test]
    fn test_evm_config_lifts_tx_gas_cap() {
        let config = dev_evm_config(None);
        let env = config.evm_env(&Header::default()).unwrap();
        // EIP-7825 neutralised — pool and EVM accept arbitrarily large txs.
        assert_eq!(env.cfg_env.tx_gas_limit_cap, Some(u64::MAX));
    }

    #[test]
    fn test_evm_config_patches_contract_size_in_evm_env() {
        let config = dev_evm_config(Some(524_288));
        let env = config.evm_env(&Header::default()).unwrap();
        assert_eq!(env.cfg_env.limit_contract_code_size, Some(524_288));
        assert_eq!(env.cfg_env.limit_contract_initcode_size, Some(1_048_576));
    }

    #[test]
    fn test_evm_config_no_contract_size_override_by_default() {
        let config = dev_evm_config(None);
        let env = config.evm_env(&Header::default()).unwrap();
        assert!(env.cfg_env.limit_contract_code_size.is_none());
    }

    #[test]
    fn test_scheduled_osaka_selects_osaka_spec_and_keeps_cap_lifted() {
        // End-to-end: --osaka-time flows chainspec → evm_env spec selection,
        // while the EIP-7825 cap stays lifted post-fork.
        let t = 1_900_000_000u64;
        let genesis = crate::genesis::create_dev_genesis();
        let poa_config = crate::chainspec::PoaConfig {
            period: 1,
            epoch: 30000,
            signers: crate::genesis::dev_signers(),
        };
        let chain = crate::chainspec::PoaChainSpec::new_with_forks(genesis, poa_config, Some(t));
        let config = PoaEvmConfig::new(
            Arc::clone(chain.inner()),
            PoaEvmFactory::new(None, 4, false),
        );

        let pre_fork = Header {
            timestamp: t - 1,
            ..Default::default()
        };
        let post_fork = Header {
            timestamp: t,
            ..Default::default()
        };

        let pre_env = config.evm_env(&pre_fork).unwrap();
        assert_eq!(pre_env.cfg_env.spec, SpecId::PRAGUE);

        let post_env = config.evm_env(&post_fork).unwrap();
        assert_eq!(post_env.cfg_env.spec, SpecId::OSAKA);
        // The cap lift is what keeps 300M-gas deploys working after the fork.
        assert_eq!(post_env.cfg_env.tx_gas_limit_cap, Some(u64::MAX));
    }
}

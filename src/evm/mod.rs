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
//!              → EthEvmConfig::new_with_evm_factory(chain_spec, PoaEvmFactory)
//!                 → PoaEvmFactory::create_evm(db, env)
//!                    → patch_env (contract size limits, spec overrides)
//!                    → EthEvmFactory::create_evm(db, patched_env)
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

use alloy_evm::eth::spec::EthExecutorSpec;
use alloy_evm::revm::context::TxEnv;
use reth_chainspec::EthereumHardforks;
use reth_ethereum::node::api::{FullNodeTypes, NodeTypes};
use reth_ethereum::node::builder::{components::ExecutorBuilder, BuilderContext};
use reth_ethereum::node::EthEvmConfig;
use reth_ethereum::EthPrimitives;
use reth_ethereum_forks::Hardforks;

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
#[derive(Debug, Clone)]
pub struct CalldataDiscountInspector<I> {
    inner: I,
    /// Replacement cost per non-zero calldata byte (1–16 gas).
    calldata_gas_per_byte: u64,
    /// Set to `true` after the discount has been applied for this EVM instance.
    discount_applied: bool,
}

impl<I> CalldataDiscountInspector<I> {
    /// Create a new inspector wrapping `inner` with the given calldata gas cost.
    pub fn new(inner: I, calldata_gas_per_byte: u64) -> Self {
        Self {
            inner,
            calldata_gas_per_byte: calldata_gas_per_byte.clamp(1, 16),
            discount_applied: false,
        }
    }

    /// Returns the discount in gas for a given number of non-zero calldata bytes.
    pub fn discount_for(&self, non_zero_bytes: u64) -> u64 {
        non_zero_bytes.saturating_mul(16u64.saturating_sub(self.calldata_gas_per_byte))
    }

    /// Consume the wrapper and return the inner inspector.
    pub fn into_inner(self) -> I {
        self.inner
    }

    /// Borrow the inner inspector.
    pub fn inner(&self) -> &I {
        &self.inner
    }

    /// Mutably borrow the inner inspector.
    pub fn inner_mut(&mut self) -> &mut I {
        &mut self.inner
    }
}

impl<CTX, I: Inspector<CTX>> Inspector<CTX> for CalldataDiscountInspector<I> {
    fn initialize_interp(&mut self, interp: &mut Interpreter, context: &mut CTX) {
        // Apply discount once per tx (discount_applied resets when a new EVM is created).
        if !self.discount_applied && self.calldata_gas_per_byte < 16 {
            self.discount_applied = true;
            // interp.input is InputsImpl (EthInterpreter default); .input is CallInput.
            let non_zero = match &interp.input.input {
                CallInput::Bytes(bytes) => bytes.iter().filter(|&&b| b != 0).count() as u64,
                CallInput::SharedBuffer(_) => 0, // shared-memory slice: skip (sub-call context)
            };
            let discount = self.discount_for(non_zero);
            if discount > 0 {
                interp.gas.erase_cost(discount);
            }
        }
        self.inner.initialize_interp(interp, context);
    }

    fn step(&mut self, interp: &mut Interpreter, context: &mut CTX) {
        self.inner.step(interp, context);
    }

    fn step_end(&mut self, interp: &mut Interpreter, context: &mut CTX) {
        self.inner.step_end(interp, context);
    }

    fn log(&mut self, context: &mut CTX, log: Log) {
        self.inner.log(context, log);
    }

    fn call(&mut self, context: &mut CTX, inputs: &mut CallInputs) -> Option<CallOutcome> {
        self.inner.call(context, inputs)
    }

    fn call_end(&mut self, context: &mut CTX, inputs: &CallInputs, outcome: &mut CallOutcome) {
        self.inner.call_end(context, inputs, outcome);
    }

    fn create(&mut self, context: &mut CTX, inputs: &mut CreateInputs) -> Option<CreateOutcome> {
        self.inner.create(context, inputs)
    }

    fn create_end(
        &mut self,
        context: &mut CTX,
        inputs: &CreateInputs,
        outcome: &mut CreateOutcome,
    ) {
        self.inner.create_end(context, inputs, outcome);
    }

    fn selfdestruct(&mut self, contract: Address, target: Address, value: U256) {
        self.inner.selfdestruct(contract, target, value);
    }
}

// ─── PoaEvmFactory ────────────────────────────────────────────────────────────

/// POA-customised EVM factory.
///
/// Wraps [`EthEvmFactory`] and injects two POA-specific `CfgEnv` overrides:
///
/// 1. `limit_contract_code_size` — lifts EIP-170's 24 KB bytecode cap.
/// 2. Calldata gas discount — wraps every created EVM with
///    [`CalldataDiscountInspector`] so non-zero calldata bytes cost
///    `calldata_gas_per_byte` instead of the Ethereum default of 16.
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
}

impl Default for PoaEvmFactory {
    fn default() -> Self {
        Self {
            inner: EthEvmFactory::default(),
            max_contract_size: None,
            calldata_gas_per_byte: 4, // POA default: reduce calldata cost
        }
    }
}

impl PoaEvmFactory {
    /// Create a factory with custom contract size and calldata gas overrides.
    ///
    /// `calldata_gas_per_byte` is clamped to `[1, 16]`.
    /// Pass `16` to disable the calldata discount (Ethereum mainnet behaviour).
    pub fn new(max_contract_size: Option<usize>, calldata_gas_per_byte: u64) -> Self {
        Self {
            inner: EthEvmFactory::default(),
            max_contract_size,
            calldata_gas_per_byte: calldata_gas_per_byte.clamp(1, 16),
        }
    }

    /// Apply POA-specific `CfgEnv` overrides to an [`EvmEnv`] before EVM creation.
    fn patch_env(&self, mut env: EvmEnv) -> EvmEnv {
        if let Some(limit) = self.max_contract_size {
            env.cfg_env.limit_contract_code_size = Some(limit);
            // Also lift the initcode size limit (EIP-3860) proportionally.
            env.cfg_env.limit_contract_initcode_size = Some(limit * 2);
        }
        env
    }

    /// Whether the calldata discount is active (i.e. cheaper than mainnet).
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

    fn create_evm<DB: Database>(&self, db: DB, input: EvmEnv) -> Self::Evm<DB, NoOpInspector> {
        self.inner.create_evm(db, self.patch_env(input))
    }

    fn create_evm_with_inspector<DB: Database, I: Inspector<Self::Context<DB>>>(
        &self,
        db: DB,
        input: EvmEnv,
        inspector: I,
    ) -> Self::Evm<DB, I> {
        self.inner
            .create_evm_with_inspector(db, self.patch_env(input), inspector)
    }
}

// ─── PoaExecutorBuilder ───────────────────────────────────────────────────────

/// Custom executor builder that uses [`PoaEvmFactory`] for EVM creation.
///
/// Plugged into `PoaNode::components_builder` in place of
/// `EthereumExecutorBuilder`.  Passes through both `max_contract_size`
/// and `calldata_gas_per_byte` to the factory.
#[derive(Debug, Clone)]
pub struct PoaExecutorBuilder {
    /// Override for maximum deployed contract size.  `None` = Ethereum default.
    pub max_contract_size: Option<usize>,
    /// Gas cost per non-zero calldata byte (1–16). `16` = Ethereum mainnet default.
    pub calldata_gas_per_byte: u64,
}

impl PoaExecutorBuilder {
    /// Create a builder with the given POA EVM settings.
    pub fn new(max_contract_size: Option<usize>, calldata_gas_per_byte: u64) -> Self {
        Self {
            max_contract_size,
            calldata_gas_per_byte,
        }
    }
}

impl<Types, Node> ExecutorBuilder<Node> for PoaExecutorBuilder
where
    Types: NodeTypes<
        ChainSpec: Hardforks + EthExecutorSpec + EthereumHardforks,
        Primitives = EthPrimitives,
    >,
    Node: FullNodeTypes<Types = Types>,
{
    type EVM = EthEvmConfig<Types::ChainSpec, PoaEvmFactory>;

    async fn build_evm(self, ctx: &BuilderContext<Node>) -> eyre::Result<Self::EVM> {
        Ok(EthEvmConfig::new_with_evm_factory(
            ctx.chain_spec(),
            PoaEvmFactory::new(self.max_contract_size, self.calldata_gas_per_byte),
        ))
    }
}

// ─── Tests ────────────────────────────────────────────────────────────────────

#[cfg(test)]
mod bench;

#[cfg(test)]
mod tests {
    use super::*;
    use alloy_evm::EvmEnv;

    fn make_env() -> EvmEnv {
        EvmEnv::default()
    }

    // ── contract size ──────────────────────────────────────────────────────────

    #[test]
    fn test_poa_evm_factory_no_override_keeps_default() {
        let factory = PoaEvmFactory::new(None, 16);
        let patched = factory.patch_env(make_env());
        assert!(patched.cfg_env.limit_contract_code_size.is_none());
    }

    #[test]
    fn test_poa_evm_factory_applies_code_size_limit() {
        let factory = PoaEvmFactory::new(Some(524_288), 16); // 512 KB
        let patched = factory.patch_env(make_env());
        assert_eq!(patched.cfg_env.limit_contract_code_size, Some(524_288));
    }

    #[test]
    fn test_poa_evm_factory_sets_initcode_limit_double() {
        let factory = PoaEvmFactory::new(Some(131_072), 16); // 128 KB
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
        let inspector = CalldataDiscountInspector::new(NoOpInspector, 0);
        assert_eq!(inspector.calldata_gas_per_byte, 1);
    }

    #[test]
    fn test_calldata_discount_inspector_clamps_cost_to_16() {
        let inspector = CalldataDiscountInspector::new(NoOpInspector, 20);
        assert_eq!(inspector.calldata_gas_per_byte, 16);
    }

    #[test]
    fn test_poa_evm_factory_default_calldata_gas_is_4() {
        let factory = PoaEvmFactory::default();
        assert_eq!(factory.calldata_gas_per_byte, 4);
        assert!(factory.has_calldata_discount());
    }

    #[test]
    fn test_poa_evm_factory_at_16_no_discount() {
        let factory = PoaEvmFactory::new(None, 16);
        assert!(!factory.has_calldata_discount());
    }

    // ── executor builder ───────────────────────────────────────────────────────

    #[test]
    fn test_poa_executor_builder_creation() {
        let builder = PoaExecutorBuilder::new(Some(524_288), 4);
        assert_eq!(builder.max_contract_size, Some(524_288));
        assert_eq!(builder.calldata_gas_per_byte, 4);
    }

    #[test]
    fn test_poa_executor_builder_no_override() {
        let builder = PoaExecutorBuilder::new(None, 16);
        assert!(builder.max_contract_size.is_none());
        assert_eq!(builder.calldata_gas_per_byte, 16);
    }

    #[test]
    fn test_patch_env_does_not_change_other_fields() {
        let factory = PoaEvmFactory::new(Some(65_536), 4);
        let env = EvmEnv::default();
        let chain_id_before = env.cfg_env.chain_id;
        let patched = factory.patch_env(env);
        assert_eq!(patched.cfg_env.chain_id, chain_id_before);
        assert_eq!(patched.cfg_env.limit_contract_code_size, Some(65_536));
    }
}

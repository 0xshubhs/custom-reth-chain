/**
 * The API contract, copied from docs/explorer-design.md ("API" > "Types").
 * That file is the source of truth: change it there first, then here.
 *
 * Conventions (from the same section):
 *   - wei and token amounts are decimal strings;
 *   - block numbers, counts, gas and indexes are numbers;
 *   - timestamps are ISO 8601 UTC;
 *   - addresses are EIP-55 checksummed, hashes lowercase;
 *   - lists are `?limit=&offset=` and answer a Page<T>;
 *   - errors are `{"message": "..."}` with 400 / 404 / 429 (429 has Retry-After).
 */

export type Wei = string; // decimal string
export type Hash = string; // 0x + 64 hex, lowercase
export type Addr = string; // EIP-55

export interface Page<T> {
  items: T[];
  total: number;
  next_offset: number | null;
}

export type TokenType = "ERC-20" | "ERC-721" | "ERC-1155";

export interface AddressRef {
  hash: Addr;
  name: string | null; // label, verified contract name, or token name
  is_contract: boolean;
  is_verified: boolean;
  token: { name: string | null; symbol: string | null; type: TokenType } | null;
}

export interface Config {
  chain_id: number;
  network_name: string;
  native_symbol: string;
  public_rpc_url: string;
}

export interface Stats extends Config {
  latest_block: number | null; // highest indexed block
  latest_block_time: string | null;
  node_block: number | null; // what the node says, for the lag line
  total_blocks: number;
  total_transactions: number;
  total_addresses: number;
  total_contracts: number;
  total_tokens: number;
  verified_contracts: number;
}

export interface BlockSummary {
  number: number;
  hash: Hash;
  timestamp: string;
  miner: AddressRef;
  tx_count: number;
  gas_used: string;
  gas_limit: string;
  size: number | null;
}

export interface Block extends BlockSummary {
  parent_hash: Hash;
  state_root: Hash;
  transactions_root: Hash;
  receipts_root: Hash;
  base_fee_per_gas: Wei | null;
  extra_data: string;
  nonce: string | null;
  mix_hash: Hash | null;
  confirmations: number;
}

export type TxStatus = "ok" | "failed" | "pending";

export interface TxSummary {
  hash: Hash;
  block_number: number;
  timestamp: string;
  tx_index: number;
  from: AddressRef;
  to: AddressRef | null;
  created_contract: AddressRef | null;
  value: Wei;
  fee: Wei;
  status: TxStatus;
  method: string | null; // decoded name when the ABI is known, else 4-byte id, null for plain transfers
  type: number;
}

export interface DecodedParam {
  name: string;
  type: string;
  value: unknown;
  indexed?: boolean;
}

export interface Tx extends TxSummary {
  block_hash: Hash | null;
  nonce: number;
  gas: number;
  gas_used: number | null;
  gas_price: Wei | null;
  effective_gas_price: Wei | null;
  max_fee_per_gas: Wei | null;
  max_priority_fee_per_gas: Wei | null;
  input: string;
  method_id: string | null;
  decoded_input: { method: string; signature: string; params: DecodedParam[] } | null;
  error: string | null; // revert reason or trace error
  confirmations: number;
  token_transfers_count: number;
  internal_txs_count: number;
  logs_count: number;
  indexed: boolean; // false = read straight from the node, not in the DB yet
}

export interface TokenRef {
  address: Addr;
  name: string | null;
  symbol: string | null;
  decimals: number | null;
  type: TokenType;
}

export interface TokenTransfer {
  tx_hash: Hash;
  block_number: number;
  log_index: number;
  batch_index: number;
  timestamp: string;
  token: TokenRef;
  from: AddressRef;
  to: AddressRef;
  amount: string | null; // raw integer string (ERC-20/1155); null for ERC-721
  token_id: string | null; // ERC-721/1155
  method: string | null; // the tx's method, for context
}

export type CallType = "CALL" | "STATICCALL" | "DELEGATECALL" | "CALLCODE" | "CREATE" | "CREATE2" | "SELFDESTRUCT";

export interface InternalTx {
  tx_hash: Hash;
  block_number: number;
  trace_address: string;
  depth: number;
  call_type: CallType;
  from: AddressRef;
  to: AddressRef | null;
  created_contract: AddressRef | null;
  value: Wei;
  gas: number | null;
  gas_used: number | null;
  error: string | null;
  timestamp: string;
}

export interface Log {
  tx_hash: Hash;
  block_number: number;
  log_index: number;
  address: AddressRef;
  topics: (Hash | null)[];
  data: string;
  decoded: { event: string; signature: string; params: DecodedParam[] } | null;
}

export type ProxyType = "eip1967" | "eip1967-beacon" | "trex" | null;

export interface Token extends TokenRef {
  is_erc3643: boolean;
  total_supply: string | null;
  holders_count: number;
  transfers_count: number;
  first_block: number;
}

export interface AddressDetail {
  hash: Addr;
  name: string | null;
  balance: Wei;
  balance_block: number | null;
  nonce: number | null;
  is_contract: boolean;
  is_verified: boolean;
  creator: AddressRef | null;
  creation_tx: Hash | null;
  creation_block: number | null;
  token: Token | null; // when this address is a token contract
  implementation: AddressRef | null; // proxy target (EIP-1967 or T-REX authority)
  proxy_type: ProxyType;
  tx_count: number;
  token_transfers_count: number;
  internal_txs_count: number;
  token_holdings_count: number;
  first_block: number | null;
  last_block: number | null;
}

export interface TokenHolder {
  address: AddressRef;
  balance: string;
  token_id: string | null;
  share_pct: number | null;
}

export interface TokenHolding {
  token: Token;
  balance: string;
  token_id: string | null;
}

export interface BalancePoint {
  block_number: number;
  timestamp: string;
  balance: Wei;
  delta: Wei;
}

export type MatchType = "full" | "partial" | "similar" | "imported";

export interface SmartContract {
  is_verified: boolean;
  is_fully_verified: boolean;
  is_partially_verified: boolean;
  match_type: MatchType | null;
  name: string | null;
  file_path: string | null;
  compiler_version: string | null;
  language: string | null;
  evm_version: string | null;
  optimization_enabled: boolean | null;
  optimization_runs: number | null;
  via_ir: boolean | null;
  license_type: string | null;
  verified_at: string | null;
  source_address: Addr | null;
  abi: unknown[] | null;
  source_code: string | null; // the main file
  additional_sources: { file_path: string; source_code: string }[];
  constructor_args: string | null;
  deployed_bytecode: string | null;
  creation_bytecode: string | null;
  implementation: AddressRef | null;
  proxy_type: AddressDetail["proxy_type"];
  implementation_abi: unknown[] | null; // so a proxy page can read through to the implementation
}

export interface VerifiedContractRow {
  address: AddressRef;
  name: string;
  compiler_version: string;
  match_type: string;
  verified_at: string;
  tx_count: number;
}

export interface SearchResult {
  type: "block" | "transaction" | "address" | "contract" | "token";
  // block: number + hash; transaction: hash; address/contract/token: address (+ name/symbol)
  number?: number;
  hash?: string;
  address?: Addr;
  name?: string | null;
  symbol?: string | null;
}

export interface VerificationStatus {
  status: "none" | "pending" | "running" | "success" | "failed";
  message: string | null;
  updated_at: string | null;
}

/** `GET /api/v2/transactions/{hash}/raw`: the node's raw JSON. */
export interface RawTx {
  tx: unknown;
  receipt: unknown;
  trace: unknown;
}

/** `POST /api/v2/smart-contracts/{addr}/call`. */
export type CallResponse = { result: string } | { error: string };

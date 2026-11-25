package api

import "encoding/json"

// The JSON shapes below are the TypeScript types of explorer/docs/design.md,
// field for field. A "| null" field is a pointer without omitempty, so it is
// always present.

type AddressRef struct {
	Hash       string    `json:"hash"`
	Name       *string   `json:"name"`
	IsContract bool      `json:"is_contract"`
	IsVerified bool      `json:"is_verified"`
	Token      *RefToken `json:"token"`
}

type RefToken struct {
	Name   *string `json:"name"`
	Symbol *string `json:"symbol"`
	Type   string  `json:"type"`
}

type Stats struct {
	ChainID           uint64  `json:"chain_id"`
	NetworkName       string  `json:"network_name"`
	NativeSymbol      string  `json:"native_symbol"`
	PublicRPCURL      string  `json:"public_rpc_url"`
	LatestBlock       *int64  `json:"latest_block"`
	LatestBlockTime   *string `json:"latest_block_time"`
	NodeBlock         *int64  `json:"node_block"`
	TotalBlocks       int64   `json:"total_blocks"`
	TotalTransactions int64   `json:"total_transactions"`
	TotalAddresses    int64   `json:"total_addresses"`
	TotalContracts    int64   `json:"total_contracts"`
	TotalTokens       int64   `json:"total_tokens"`
	VerifiedContracts int64   `json:"verified_contracts"`
}

type BlockSummary struct {
	Number    int64       `json:"number"`
	Hash      string      `json:"hash"`
	Timestamp string      `json:"timestamp"`
	Miner     *AddressRef `json:"miner"`
	TxCount   int64       `json:"tx_count"`
	GasUsed   string      `json:"gas_used"`
	GasLimit  string      `json:"gas_limit"`
	Size      *int64      `json:"size"`
}

type Block struct {
	BlockSummary
	ParentHash       string  `json:"parent_hash"`
	StateRoot        string  `json:"state_root"`
	TransactionsRoot string  `json:"transactions_root"`
	ReceiptsRoot     string  `json:"receipts_root"`
	BaseFeePerGas    *string `json:"base_fee_per_gas"`
	ExtraData        string  `json:"extra_data"`
	Nonce            *string `json:"nonce"`
	MixHash          *string `json:"mix_hash"`
	Confirmations    int64   `json:"confirmations"`
}

type TxSummary struct {
	Hash            string      `json:"hash"`
	BlockNumber     *int64      `json:"block_number"`
	Timestamp       *string     `json:"timestamp"`
	TxIndex         *int64      `json:"tx_index"`
	From            *AddressRef `json:"from"`
	To              *AddressRef `json:"to"`
	CreatedContract *AddressRef `json:"created_contract"`
	Value           string      `json:"value"`
	Fee             string      `json:"fee"`
	Status          string      `json:"status"`
	Method          *string     `json:"method"`
	Type            int64       `json:"type"`
}

type DecodedParam struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Value   any    `json:"value"`
	Indexed *bool  `json:"indexed,omitempty"`
}

type DecodedInput struct {
	Method    string         `json:"method"`
	Signature string         `json:"signature"`
	Params    []DecodedParam `json:"params"`
}

type DecodedLog struct {
	Event     string         `json:"event"`
	Signature string         `json:"signature"`
	Params    []DecodedParam `json:"params"`
}

type Tx struct {
	TxSummary
	BlockHash            *string       `json:"block_hash"`
	Nonce                int64         `json:"nonce"`
	Gas                  int64         `json:"gas"`
	GasUsed              *int64        `json:"gas_used"`
	GasPrice             *string       `json:"gas_price"`
	EffectiveGasPrice    *string       `json:"effective_gas_price"`
	MaxFeePerGas         *string       `json:"max_fee_per_gas"`
	MaxPriorityFeePerGas *string       `json:"max_priority_fee_per_gas"`
	Input                string        `json:"input"`
	MethodID             *string       `json:"method_id"`
	DecodedInput         *DecodedInput `json:"decoded_input"`
	Error                *string       `json:"error"`
	Confirmations        int64         `json:"confirmations"`
	TokenTransfersCount  int64         `json:"token_transfers_count"`
	InternalTxsCount     int64         `json:"internal_txs_count"`
	LogsCount            int64         `json:"logs_count"`
	Indexed              bool          `json:"indexed"`
}

type TokenRef struct {
	Address  string  `json:"address"`
	Name     *string `json:"name"`
	Symbol   *string `json:"symbol"`
	Decimals *int64  `json:"decimals"`
	Type     string  `json:"type"`
}

type TokenTransfer struct {
	TxHash      string      `json:"tx_hash"`
	BlockNumber int64       `json:"block_number"`
	LogIndex    int64       `json:"log_index"`
	BatchIndex  int64       `json:"batch_index"`
	Timestamp   string      `json:"timestamp"`
	Token       TokenRef    `json:"token"`
	From        *AddressRef `json:"from"`
	To          *AddressRef `json:"to"`
	Amount      *string     `json:"amount"`
	TokenID     *string     `json:"token_id"`
	Method      *string     `json:"method"`
}

type InternalTx struct {
	TxHash          string      `json:"tx_hash"`
	BlockNumber     int64       `json:"block_number"`
	TraceAddress    string      `json:"trace_address"`
	Depth           int64       `json:"depth"`
	CallType        string      `json:"call_type"`
	From            *AddressRef `json:"from"`
	To              *AddressRef `json:"to"`
	CreatedContract *AddressRef `json:"created_contract"`
	Value           string      `json:"value"`
	Gas             *int64      `json:"gas"`
	GasUsed         *int64      `json:"gas_used"`
	Error           *string     `json:"error"`
	Timestamp       string      `json:"timestamp"`
}

type Log struct {
	TxHash      string      `json:"tx_hash"`
	BlockNumber int64       `json:"block_number"`
	LogIndex    int64       `json:"log_index"`
	Address     *AddressRef `json:"address"`
	Topics      []*string   `json:"topics"`
	Data        string      `json:"data"`
	Decoded     *DecodedLog `json:"decoded"`
}

type AddressDetail struct {
	Hash                string      `json:"hash"`
	Name                *string     `json:"name"`
	Balance             string      `json:"balance"`
	BalanceBlock        *int64      `json:"balance_block"`
	Nonce               *int64      `json:"nonce"`
	IsContract          bool        `json:"is_contract"`
	IsVerified          bool        `json:"is_verified"`
	Creator             *AddressRef `json:"creator"`
	CreationTx          *string     `json:"creation_tx"`
	CreationBlock       *int64      `json:"creation_block"`
	Token               *Token      `json:"token"`
	Implementation      *AddressRef `json:"implementation"`
	ProxyType           *string     `json:"proxy_type"`
	TxCount             int64       `json:"tx_count"`
	TokenTransfersCount int64       `json:"token_transfers_count"`
	InternalTxsCount    int64       `json:"internal_txs_count"`
	TokenHoldingsCount  int64       `json:"token_holdings_count"`
	FirstBlock          *int64      `json:"first_block"`
	LastBlock           *int64      `json:"last_block"`
}

type Token struct {
	TokenRef
	IsERC3643      bool    `json:"is_erc3643"`
	TotalSupply    *string `json:"total_supply"`
	HoldersCount   int64   `json:"holders_count"`
	TransfersCount int64   `json:"transfers_count"`
	FirstBlock     int64   `json:"first_block"`
}

type TokenHolder struct {
	Address  *AddressRef `json:"address"`
	Balance  string      `json:"balance"`
	TokenID  *string     `json:"token_id"`
	SharePct *float64    `json:"share_pct"`
}

type TokenHolding struct {
	Token   Token   `json:"token"`
	Balance string  `json:"balance"`
	TokenID *string `json:"token_id"`
}

type BalancePoint struct {
	BlockNumber int64  `json:"block_number"`
	Timestamp   string `json:"timestamp"`
	Balance     string `json:"balance"`
	Delta       string `json:"delta"`
}

type SourceFile struct {
	FilePath   string `json:"file_path"`
	SourceCode string `json:"source_code"`
}

type SmartContract struct {
	IsVerified          bool            `json:"is_verified"`
	IsFullyVerified     bool            `json:"is_fully_verified"`
	IsPartiallyVerified bool            `json:"is_partially_verified"`
	MatchType           *string         `json:"match_type"`
	Name                *string         `json:"name"`
	FilePath            *string         `json:"file_path"`
	CompilerVersion     *string         `json:"compiler_version"`
	Language            *string         `json:"language"`
	EVMVersion          *string         `json:"evm_version"`
	OptimizationEnabled *bool           `json:"optimization_enabled"`
	OptimizationRuns    *int64          `json:"optimization_runs"`
	ViaIR               *bool           `json:"via_ir"`
	LicenseType         *string         `json:"license_type"`
	VerifiedAt          *string         `json:"verified_at"`
	SourceAddress       *string         `json:"source_address"`
	ABI                 json.RawMessage `json:"abi"`
	SourceCode          *string         `json:"source_code"`
	AdditionalSources   []SourceFile    `json:"additional_sources"`
	ConstructorArgs     *string         `json:"constructor_args"`
	DeployedBytecode    *string         `json:"deployed_bytecode"`
	CreationBytecode    *string         `json:"creation_bytecode"`
	Implementation      *AddressRef     `json:"implementation"`
	ProxyType           *string         `json:"proxy_type"`
	ImplementationABI   json.RawMessage `json:"implementation_abi"`
}

type VerifiedContractRow struct {
	Address         *AddressRef `json:"address"`
	Name            string      `json:"name"`
	CompilerVersion string      `json:"compiler_version"`
	MatchType       string      `json:"match_type"`
	VerifiedAt      string      `json:"verified_at"`
	TxCount         int64       `json:"tx_count"`
}

type SearchResult struct {
	Type    string  `json:"type"`
	Number  *int64  `json:"number,omitempty"`
	Hash    *string `json:"hash,omitempty"`
	Address *string `json:"address,omitempty"`
	Name    *string `json:"name,omitempty"`
	Symbol  *string `json:"symbol,omitempty"`
}

type VerificationStatus struct {
	Status    string  `json:"status"`
	Message   *string `json:"message"`
	UpdatedAt *string `json:"updated_at"`
}

// jsonNull is a literal null for json.RawMessage fields.
var jsonNull = json.RawMessage("null")

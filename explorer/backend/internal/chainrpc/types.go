package chainrpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Block is the part of eth_getBlockByNumber(n, true) the explorer models.
// The node's full JSON is kept separately as raw.
type Block struct {
	Number                Quantity          `json:"number"`
	Hash                  string            `json:"hash"`
	ParentHash            string            `json:"parentHash"`
	Miner                 string            `json:"miner"`
	Timestamp             Quantity          `json:"timestamp"`
	GasUsed               Quantity          `json:"gasUsed"`
	GasLimit              Quantity          `json:"gasLimit"`
	BaseFeePerGas         Quantity          `json:"baseFeePerGas"`
	Size                  Quantity          `json:"size"`
	StateRoot             string            `json:"stateRoot"`
	TransactionsRoot      string            `json:"transactionsRoot"`
	ReceiptsRoot          string            `json:"receiptsRoot"`
	LogsBloom             *string           `json:"logsBloom"`
	ExtraData             string            `json:"extraData"`
	Nonce                 *string           `json:"nonce"`
	MixHash               *string           `json:"mixHash"`
	Difficulty            Quantity          `json:"difficulty"`
	WithdrawalsRoot       *string           `json:"withdrawalsRoot"`
	BlobGasUsed           Quantity          `json:"blobGasUsed"`
	ExcessBlobGas         Quantity          `json:"excessBlobGas"`
	ParentBeaconBlockRoot *string           `json:"parentBeaconBlockRoot"`
	RequestsHash          *string           `json:"requestsHash"`
	Transactions          []json.RawMessage `json:"transactions"`
	Withdrawals           []Withdrawal      `json:"withdrawals"`
}

// Withdrawal is one EIP-4895 withdrawal (amount in gwei).
type Withdrawal struct {
	Index          Quantity `json:"index"`
	ValidatorIndex Quantity `json:"validatorIndex"`
	Address        string   `json:"address"`
	Amount         Quantity `json:"amount"`
}

// Transaction is a transaction object as nodes return it.
type Transaction struct {
	Hash                 string   `json:"hash"`
	BlockHash            *string  `json:"blockHash"`
	BlockNumber          Quantity `json:"blockNumber"`
	TransactionIndex     Quantity `json:"transactionIndex"`
	From                 string   `json:"from"`
	To                   *string  `json:"to"`
	Value                Quantity `json:"value"`
	Nonce                Quantity `json:"nonce"`
	Type                 Quantity `json:"type"`
	Gas                  Quantity `json:"gas"`
	GasPrice             Quantity `json:"gasPrice"`
	MaxFeePerGas         Quantity `json:"maxFeePerGas"`
	MaxPriorityFeePerGas Quantity `json:"maxPriorityFeePerGas"`
	Input                string   `json:"input"`
	Data                 string   `json:"data"` // some nodes still say data
}

// InputHex is the call data, whichever field the node used.
func (t *Transaction) InputHex() string {
	if t.Input != "" {
		return strings.ToLower(t.Input)
	}
	if t.Data != "" {
		return strings.ToLower(t.Data)
	}
	return "0x"
}

// Receipt is a transaction receipt.
type Receipt struct {
	TransactionHash   string   `json:"transactionHash"`
	TransactionIndex  Quantity `json:"transactionIndex"`
	BlockHash         string   `json:"blockHash"`
	BlockNumber       Quantity `json:"blockNumber"`
	From              string   `json:"from"`
	To                *string  `json:"to"`
	Status            Quantity `json:"status"`
	GasUsed           Quantity `json:"gasUsed"`
	CumulativeGasUsed Quantity `json:"cumulativeGasUsed"`
	EffectiveGasPrice Quantity `json:"effectiveGasPrice"`
	ContractAddress   *string  `json:"contractAddress"`
	Logs              []Log    `json:"logs"`
}

// Log is one receipt log.
type Log struct {
	Address          string   `json:"address"`
	Topics           []string `json:"topics"`
	Data             string   `json:"data"`
	LogIndex         Quantity `json:"logIndex"`
	TransactionIndex Quantity `json:"transactionIndex"`
	TransactionHash  string   `json:"transactionHash"`
	BlockHash        string   `json:"blockHash"`
	Removed          bool     `json:"removed"`
}

// CallFrame is one frame of a callTracer result.
type CallFrame struct {
	Type         string      `json:"type"`
	From         string      `json:"from"`
	To           string      `json:"to"`
	Value        Quantity    `json:"value"`
	Gas          Quantity    `json:"gas"`
	GasUsed      Quantity    `json:"gasUsed"`
	Input        string      `json:"input"`
	Output       string      `json:"output"`
	Error        string      `json:"error"`
	RevertReason string      `json:"revertReason"`
	Calls        []CallFrame `json:"calls"`
}

// TraceResult is one entry of debug_traceBlockByNumber.
type TraceResult struct {
	TxHash string          `json:"txHash"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

// CallTracer is the tracer config used everywhere.
var CallTracer = map[string]any{"tracer": "callTracer"}

// ChainID asks the node for eth_chainId.
func (c *Client) ChainID(ctx context.Context) (uint64, error) {
	var s string
	if err := c.Call(ctx, &s, "eth_chainId"); err != nil {
		return 0, err
	}
	return ParseUint64(s)
}

// BlockNumber asks the node for its head.
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	var s string
	if err := c.Call(ctx, &s, "eth_blockNumber"); err != nil {
		return 0, err
	}
	return ParseUint64(s)
}

// BlockHashAt returns the hash of block n on the node, "" when the node has
// no such block.
func (c *Client) BlockHashAt(ctx context.Context, n uint64) (string, error) {
	var h struct {
		Hash string `json:"hash"`
	}
	var raw json.RawMessage
	if err := c.Call(ctx, &raw, "eth_getBlockByNumber", Hex64(n), false); err != nil {
		return "", err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return "", fmt.Errorf("block %d: %w", n, err)
	}
	return Lower(h.Hash), nil
}

// BlockHashes returns the node's hashes for the given numbers ("" = absent).
func (c *Client) BlockHashes(ctx context.Context, numbers []uint64) ([]string, error) {
	type hb struct {
		Hash string `json:"hash"`
	}
	elems := make([]BatchElem, len(numbers))
	outs := make([]*hb, len(numbers))
	for i, n := range numbers {
		outs[i] = &hb{}
		elems[i] = BatchElem{Method: "eth_getBlockByNumber", Params: []any{Hex64(n), false}, Result: outs[i]}
	}
	if err := c.BatchCall(ctx, elems); err != nil {
		return nil, err
	}
	res := make([]string, len(numbers))
	for i := range elems {
		if elems[i].Error != nil {
			return nil, elems[i].Error
		}
		res[i] = Lower(outs[i].Hash)
	}
	return res, nil
}

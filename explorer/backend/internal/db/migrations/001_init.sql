-- Explorer: initial schema. See explorer/README.md.
--
-- Conventions: addresses and hashes are lowercase 0x text; wei and token
-- amounts are numeric(78,0); every row that belongs to a block cascades from
-- blocks(number), so rolling a block back is one DELETE. Raw node JSON is kept
-- for blocks, transactions, receipts and traces so nothing the node returned
-- is ever lost, modelled or not.

CREATE TABLE indexer_state (
    id            smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    chain_id      bigint      NOT NULL,
    genesis_hash  text        NOT NULL,
    last_block    bigint      NOT NULL,   -- -1 before block 0 is indexed
    last_hash     text        NOT NULL,   -- '' before block 0 is indexed
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE blocks (
    number                   bigint PRIMARY KEY,
    hash                     text        NOT NULL UNIQUE,
    parent_hash              text        NOT NULL,
    miner                    text        NOT NULL,
    timestamp                timestamptz NOT NULL,
    gas_used                 numeric(78,0) NOT NULL,
    gas_limit                numeric(78,0) NOT NULL,
    base_fee_per_gas         numeric(78,0),
    tx_count                 integer     NOT NULL,
    size                     bigint,
    state_root               text        NOT NULL,
    transactions_root        text        NOT NULL,
    receipts_root            text        NOT NULL,
    logs_bloom               text,
    extra_data               text        NOT NULL DEFAULT '0x',
    nonce                    text,
    mix_hash                 text,
    difficulty               numeric(78,0),
    withdrawals_root         text,
    blob_gas_used            numeric(78,0),
    excess_blob_gas          numeric(78,0),
    parent_beacon_block_root text,
    requests_hash            text,
    raw                      jsonb       NOT NULL  -- header as the node returned it (transactions list replaced by hashes)
);
CREATE INDEX blocks_timestamp_idx ON blocks (timestamp DESC);
CREATE INDEX blocks_miner_idx ON blocks (miner);

CREATE TABLE transactions (
    hash                     text PRIMARY KEY,
    block_number             bigint      NOT NULL REFERENCES blocks(number) ON DELETE CASCADE,
    block_hash               text        NOT NULL,
    tx_index                 integer     NOT NULL,
    timestamp                timestamptz NOT NULL,
    from_addr                text        NOT NULL,
    to_addr                  text,                 -- null for a contract creation
    created_contract         text,                 -- receipt.contractAddress
    value                    numeric(78,0) NOT NULL,
    nonce                    bigint      NOT NULL,
    type                     smallint    NOT NULL,
    gas                      bigint      NOT NULL,
    gas_price                numeric(78,0),
    max_fee_per_gas          numeric(78,0),
    max_priority_fee_per_gas numeric(78,0),
    gas_used                 bigint      NOT NULL,
    cumulative_gas_used      bigint      NOT NULL,
    effective_gas_price      numeric(78,0),
    status                   smallint,             -- 1 ok, 0 failed (receipt.status)
    input                    text        NOT NULL,
    method_id                text,                 -- first 4 bytes of input, null when input is shorter
    error                    text,                 -- root frame error / revert reason when failed
    raw                      jsonb       NOT NULL, -- transaction object
    receipt_raw              jsonb       NOT NULL, -- receipt object
    trace_raw                jsonb,                -- callTracer result for this tx (null only with TRACES=off)
    UNIQUE (block_number, tx_index)
);
CREATE INDEX transactions_from_idx    ON transactions (from_addr, block_number DESC, tx_index DESC);
CREATE INDEX transactions_to_idx      ON transactions (to_addr, block_number DESC, tx_index DESC);
CREATE INDEX transactions_created_idx ON transactions (created_contract) WHERE created_contract IS NOT NULL;
CREATE INDEX transactions_order_idx   ON transactions (block_number DESC, tx_index DESC);

CREATE TABLE logs (
    block_number bigint  NOT NULL REFERENCES blocks(number) ON DELETE CASCADE,
    log_index    integer NOT NULL,
    tx_hash      text    NOT NULL,
    tx_index     integer NOT NULL,
    address      text    NOT NULL,
    topic0       text,
    topic1       text,
    topic2       text,
    topic3       text,
    data         text    NOT NULL,
    PRIMARY KEY (block_number, log_index)
);
CREATE INDEX logs_tx_idx      ON logs (tx_hash);
CREATE INDEX logs_address_idx ON logs (address, block_number DESC, log_index DESC);
CREATE INDEX logs_topic0_idx  ON logs (topic0);

-- Every call frame of every transaction, the root frame included
-- (trace_address '' and depth 0). The site lists depth >= 1 as "internal
-- transactions", like Blockscout; the root is kept so nothing is lost.
CREATE TABLE internal_txs (
    block_number     bigint  NOT NULL REFERENCES blocks(number) ON DELETE CASCADE,
    tx_hash          text    NOT NULL,
    tx_index         integer NOT NULL,
    trace_address    text    NOT NULL,   -- '' root, '0', '0.2', ...
    depth            integer NOT NULL,
    call_type        text    NOT NULL,   -- CALL STATICCALL DELEGATECALL CALLCODE CREATE CREATE2 SELFDESTRUCT
    from_addr        text    NOT NULL,
    to_addr          text,
    value            numeric(78,0) NOT NULL DEFAULT 0,
    gas              bigint,
    gas_used         bigint,
    input            text,
    output           text,
    error            text,
    revert_reason    text,
    created_contract text,               -- CREATE/CREATE2 that succeeded: the new address
    PRIMARY KEY (tx_hash, trace_address)
);
CREATE INDEX internal_txs_block_idx ON internal_txs (block_number);
CREATE INDEX internal_txs_from_idx  ON internal_txs (from_addr, block_number DESC) WHERE depth > 0;
CREATE INDEX internal_txs_to_idx    ON internal_txs (to_addr, block_number DESC) WHERE depth > 0;

-- Runtime code, deduplicated: contracts a factory creates usually run the same code.
CREATE TABLE contract_code (
    code_hash text PRIMARY KEY,   -- keccak256 of the runtime code
    code      text NOT NULL
);

-- Derived: one row per address ever seen anywhere (tx from/to, created,
-- internal from/to, log emitter, token transfer from/to, miner).
CREATE TABLE addresses (
    address                text PRIMARY KEY,
    balance                numeric(78,0) NOT NULL DEFAULT 0,
    balance_block          bigint,
    nonce                  bigint,
    is_contract            boolean NOT NULL DEFAULT false,
    code_hash              text REFERENCES contract_code(code_hash),
    creator                text,
    creation_tx            text,
    creation_block         bigint,
    creation_trace_address text,
    creation_input         text,   -- init code + constructor args (tx input or CREATE frame input)
    first_block            bigint NOT NULL,
    last_block             bigint NOT NULL,
    tx_count               bigint NOT NULL DEFAULT 0
);
CREATE INDEX addresses_code_hash_idx ON addresses (code_hash) WHERE code_hash IS NOT NULL;

CREATE TABLE balance_history (
    address      text   NOT NULL,
    block_number bigint NOT NULL REFERENCES blocks(number) ON DELETE CASCADE,
    balance      numeric(78,0) NOT NULL,
    delta        numeric(78,0) NOT NULL,
    PRIMARY KEY (address, block_number)
);

CREATE TABLE tokens (
    address          text PRIMARY KEY,
    type             text    NOT NULL CHECK (type IN ('ERC-20', 'ERC-721', 'ERC-1155')),
    is_erc3643       boolean NOT NULL DEFAULT false,
    name             text,
    symbol           text,
    decimals         integer,
    total_supply     numeric(78,0),
    holders_count    integer NOT NULL DEFAULT 0,
    transfers_count  bigint  NOT NULL DEFAULT 0,
    first_block      bigint  NOT NULL,
    updated_block    bigint  NOT NULL
);
CREATE INDEX tokens_symbol_idx ON tokens (lower(symbol));
CREATE INDEX tokens_name_idx   ON tokens (lower(name));

CREATE TABLE token_transfers (
    block_number bigint  NOT NULL REFERENCES blocks(number) ON DELETE CASCADE,
    log_index    integer NOT NULL,
    batch_index  integer NOT NULL DEFAULT 0,   -- position inside an ERC-1155 TransferBatch
    tx_hash      text    NOT NULL,
    timestamp    timestamptz NOT NULL,
    token        text    NOT NULL,
    token_type   text    NOT NULL,
    from_addr    text    NOT NULL,
    to_addr      text    NOT NULL,
    operator     text,                          -- ERC-1155
    amount       numeric(78,0),                 -- null for ERC-721
    token_id     numeric(78,0),                 -- null for ERC-20
    PRIMARY KEY (block_number, log_index, batch_index)
);
CREATE INDEX token_transfers_token_idx ON token_transfers (token, block_number DESC, log_index DESC);
CREATE INDEX token_transfers_from_idx  ON token_transfers (from_addr, block_number DESC, log_index DESC);
CREATE INDEX token_transfers_to_idx    ON token_transfers (to_addr, block_number DESC, log_index DESC);
CREATE INDEX token_transfers_tx_idx    ON token_transfers (tx_hash);

-- Current balance per (token, holder[, id]); read from the chain at the block
-- that last touched it. token_id is -1 for fungible ERC-20 balances (a primary
-- key column cannot be null). Zero balances are deleted.
CREATE TABLE token_balances (
    token        text   NOT NULL,
    holder       text   NOT NULL,
    token_id     numeric(78,0) NOT NULL DEFAULT -1,
    balance      numeric(78,0) NOT NULL,
    block_number bigint NOT NULL,
    PRIMARY KEY (token, holder, token_id)
);
CREATE INDEX token_balances_holder_idx ON token_balances (holder);

CREATE TABLE verified_contracts (
    address              text PRIMARY KEY,
    name                 text    NOT NULL,
    file_path            text,
    compiler_version     text    NOT NULL,
    language             text    NOT NULL DEFAULT 'solidity',
    evm_version          text,
    optimization_enabled boolean,
    optimization_runs    integer,
    via_ir               boolean,
    license_type         text,
    match_type           text    NOT NULL CHECK (match_type IN ('full', 'partial', 'similar', 'imported')),
    abi                  jsonb   NOT NULL,
    sources              jsonb   NOT NULL,   -- {"path": "source text", ...}
    compiler_settings    jsonb,
    standard_json_input  jsonb,
    constructor_args     text,
    source_address       text,               -- 'similar': the verified contract this was copied from
    verified_via         text    NOT NULL,   -- 'standard-input' | 'similar' | 'blockscout-import'
    verified_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE verification_jobs (
    id          bigserial PRIMARY KEY,
    address     text        NOT NULL,
    status      text        NOT NULL CHECK (status IN ('pending', 'running', 'success', 'failed')),
    message     text,
    client_ip   text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX verification_jobs_address_idx ON verification_jobs (address, id DESC);

-- Labels: imported from Blockscout's address_names, or set by an operator.
CREATE TABLE address_names (
    address text PRIMARY KEY,
    name    text NOT NULL,
    source  text NOT NULL DEFAULT 'manual'
);

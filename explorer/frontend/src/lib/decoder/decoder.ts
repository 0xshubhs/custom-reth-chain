/**
 * CALLDATA DECODER, ported from ETH.sh (swiss-knife, lib/decoder.ts, MIT; see
 * LICENSE in this folder).
 *
 * The structure is theirs: decodeWithABI, decodeWithSelector (ERC-7821
 * execute, selector lookup, then the "aggressive" top-level-only fallbacks:
 * Safe MultiSend packed bytes, Universal Router path, abi.encode guess,
 * Universal Router commands, function fragment guess, UTF-8 text),
 * decodeRecursive with a depth guard, decodeParamTypes / decodeBytesParam /
 * decodeTupleParam / decodeArrayParam, decodeEventWithABI and decodeEvents.
 *
 * What changed for the the chain explorer:
 *   - No third-party network calls. Selector and event lookups (4byte,
 *     Sourcify) and explorer ABI fetches are replaced by our own API
 *     (./api.ts): /signatures/functions, /signatures/events and
 *     /smart-contracts/{addr} (proxy implementation and EIP-7702 delegate ABIs
 *     followed).
 *   - Results are plain trees (./types.ts) with a `source` on every call:
 *     "abi", "signature", "guess" (abi-guesser and packed formats) or "text".
 *   - Every candidate signature is tried, not only the first.
 *   - An ABI that does not match the calldata falls back to the selector
 *     lookup (theirs returned null when an ABI was given).
 *   - Nested decodes (bytes inside a call) only accept a signature whose
 *     re-encoding equals the bytes exactly, and only dynamic `bytes` are tried
 *     (fixed bytesN values such as ids and hashes are not calldata), so random
 *     bytes are not read as a zero-argument call.
 *   - The `bytes` param of Safe's multiSend(bytes) is decoded as packed
 *     MultiSend transactions (theirs only did that for top-level input).
 *   - A tuple holding one address and a `bytes` field ((to, value, data)
 *     batches, Multicall3 calls) decodes the bytes against that address.
 *   - ERC-7821 executionData with opData ((Call[], bytes)) is accepted too.
 *   - Events without an ABI are decoded from a looked-up signature by trying
 *     which params are indexed.
 *   - A small bundled table of very common signatures (./common.ts) backs
 *     the signature API, so token calls and events decode even without it.
 *   - Added: decodeErrorData for revert data (Error(string), Panic(uint256)
 *     with the panic's meaning, custom errors from the ABI or
 *     /signatures/errors).
 *   - Console logging and the Chakra/ethers result shapes are dropped.
 */
import { guessAbiEncodedData, guessFragment } from "@openchainxyz/abi-guesser";
import {
  AbiCoder,
  ErrorFragment,
  EventFragment,
  Fragment,
  FunctionFragment,
  Indexed,
  Interface,
  ParamType,
  getAddress,
  type InterfaceAbi,
  type LogDescription,
} from "ethers";
import { decodeAbiParameters, decodeFunctionData, hexToBigInt, hexToString, parseAbi, type Hex } from "viem";

import { fetchContractAbi, lookupErrorSignatures, lookupEventSignatures, lookupFunctionSignatures } from "./api";
import { PANIC_CODES } from "./common";
import type { CallKind, DecodeSource, DecodedCall, DecodedEvent, DecodedNode } from "./types";

/* Helpers -------------------------------------------------------------------------- */

const startHexWith0x = (hex: string): Hex => (hex.startsWith("0x") ? hex : `0x${hex}`) as Hex;

const isHex = (s: string) => /^0x([0-9a-fA-F]{2})*$/.test(s);

const coder = AbiCoder.defaultAbiCoder();

/** ABI-encoded parameters are always whole 32-byte words. */
const isWordAligned = (hexNo0x: string) => hexNo0x.length % 64 === 0;

const ifaceCache = new WeakMap<object, Interface | null>();

/**
 * An ethers Interface for an ABI from anywhere (verified source, a pasted
 * string array, a single fragment). Entries ethers cannot parse are skipped
 * rather than failing the whole ABI.
 */
export function toInterface(abi: unknown): Interface | null {
  if (abi === null || abi === undefined) return null;
  if (typeof abi === "object") {
    const hit = ifaceCache.get(abi as object);
    if (hit !== undefined) return hit;
  }
  let iface: Interface | null = null;
  try {
    iface = new Interface(abi as InterfaceAbi);
  } catch {
    const list = Array.isArray(abi) ? abi : typeof abi === "string" ? safeJson(abi) : null;
    if (Array.isArray(list)) {
      const ok: unknown[] = [];
      for (const entry of list) {
        try {
          Fragment.from(entry as never);
          ok.push(entry);
        } catch {
          /* an entry ethers cannot read: skip it */
        }
      }
      try {
        iface = new Interface(ok as InterfaceAbi);
      } catch {
        iface = null;
      }
    }
  }
  if (typeof abi === "object") ifaceCache.set(abi as object, iface);
  return iface;
}

function safeJson(s: string): unknown {
  try {
    return JSON.parse(s);
  } catch {
    return null;
  }
}

/* The parsed (not yet recursively decoded) form ------------------------------------- */

interface BatchTx {
  operation?: string;
  to: string;
  value: string;
  data: string;
}

/** One top-level parse, before params are decoded recursively. */
export interface ParsedCall {
  kind: CallKind;
  source: DecodeSource;
  name: string;
  label: string | null;
  signature: string;
  selector: string | null;
  inputs: readonly ParamType[];
  args: readonly unknown[];
  /** Safe MultiSend and ERC-7821 batches. */
  txs?: BatchTx[];
}

/* PRIMARY DECODING FUNCTIONS ---------------------------------------------------------- */

/**
 * Decodes calldata with a known ABI. Returns null when no function of the ABI
 * matches (or the data does not decode against it).
 */
export function decodeWithABI({
  abi,
  calldata,
  source = "abi",
}: {
  abi: unknown;
  calldata: string;
  source?: DecodeSource;
}): ParsedCall | null {
  const iface = toInterface(abi);
  if (!iface || calldata.length < 10) return null;
  try {
    const tx = iface.parseTransaction({ data: calldata });
    if (!tx) return null;
    return {
      kind: "call",
      source,
      name: tx.name,
      label: null,
      signature: tx.signature,
      selector: tx.selector,
      inputs: tx.fragment.inputs,
      args: tx.args.toArray(false),
    };
  } catch {
    return null;
  }
}

/** Decodes an event log with a known ABI, or null. */
export function decodeEventWithABI({
  abi,
  topics,
  data,
}: {
  abi: unknown;
  topics: string[];
  data: string;
}): LogDescription | null {
  const iface = toInterface(abi);
  if (!iface) return null;
  try {
    return iface.parseLog({ topics, data });
  } catch {
    return null;
  }
}

/**
 * Decodes with the ABI verified for `address` (our API), falling back to the
 * selector lookup when there is none or it does not match (a proxy whose
 * implementation is not verified, a call to a function the ABI lacks).
 */
export async function decodeWithAddress({
  calldata,
  address,
  _depth = 0,
}: {
  calldata: string;
  address: string;
  _depth?: number;
}): Promise<ParsedCall | null> {
  const abi = await fetchContractAbi(address);
  if (abi) {
    const fromAbi = decodeWithABI({ abi, calldata });
    if (fromAbi) return fromAbi;
  }
  return decodeWithSelector({ calldata, _depth });
}

/**
 * Decodes calldata by trying strategies in order of reliability:
 *
 * Always (they need a specific selector):
 *   1. ERC-7821 execute (0xe9ae5c53)
 *   2. selector lookup in our signature API
 * Top level only (they can match arbitrary bytes):
 *   3. Safe MultiSend packed transactions
 *   4. Universal Router path
 *   5. abi.encode guess (abi-guesser)
 *   6. Universal Router commands
 *   7. function fragment guess (abi-guesser)
 *   8. UTF-8 text
 */
export async function decodeWithSelector({
  calldata,
  _depth = 0,
}: {
  calldata: string;
  _depth?: number;
}): Promise<ParsedCall | null> {
  const isNestedDecode = _depth > 0;
  const first = decode7821Execute(calldata);
  if (first) return first;
  const bySelector = await _decodeWithSelector(calldata, isNestedDecode);
  if (bySelector) return bySelector;
  if (isNestedDecode) return null;
  return (
    decodeSafeMultiSendTransactionsParam(calldata, "guess") ??
    decodeUniversalRouterPath(calldata) ??
    decodeABIEncodedData(calldata) ??
    decodeUniversalRouterCommands(calldata) ??
    decodeByGuessingFunctionFragment(calldata) ??
    decodeAsUtf8Text(calldata)
  );
}

/** Selector lookup in our signature API; every candidate signature is tried. */
async function _decodeWithSelector(calldata: string, strict: boolean): Promise<ParsedCall | null> {
  if (calldata.length < 10) return null;
  const selector = calldata.slice(0, 10).toLowerCase();
  // The null selector matches spam signatures; skip it as upstream does.
  if (selector === "0x00000000") return null;
  const signatures = await lookupFunctionSignatures(selector);
  if (!signatures.length) return null;
  const found = decodeAllPossibilities({ functionSignatures: signatures, calldata, strict });
  return found[0] ?? null;
}

/** Tries each signature; with `strict`, only one whose re-encoding equals the calldata. */
function decodeAllPossibilities({
  functionSignatures,
  calldata,
  strict,
}: {
  functionSignatures: string[];
  calldata: string;
  strict: boolean;
}): ParsedCall[] {
  const results: ParsedCall[] = [];
  for (const signature of functionSignatures) {
    try {
      const fragment = FunctionFragment.from(`function ${signature}`);
      const parsed = decodeWithABI({ abi: [fragment], calldata, source: "signature" });
      if (!parsed) continue;
      if (strict) {
        const again = new Interface([fragment]).encodeFunctionData(fragment, parsed.args as unknown[]);
        if (again.toLowerCase() !== calldata.toLowerCase()) continue;
      }
      results.push(parsed);
    } catch {
      /* this signature does not fit; try the next */
    }
  }
  return results;
}

/* SPECIAL FORMAT DECODERS ------------------------------------------------------------- */

const CALL_TUPLE = {
  type: "tuple[]",
  components: [
    { name: "to", type: "address" },
    { name: "value", type: "uint256" },
    { name: "data", type: "bytes" },
  ],
} as const;

/**
 * ERC-7821 execute(bytes32 mode, bytes executionData), selector 0xe9ae5c53.
 * executionData is abi.encode(Call[]) or abi.encode(Call[], bytes opData).
 */
export function decode7821Execute(calldata: string): ParsedCall | null {
  if (calldata.slice(0, 10).toLowerCase() !== "0xe9ae5c53") return null;
  try {
    const decoded = decodeFunctionData({
      abi: parseAbi(["function execute(bytes32 mode, bytes executionData) external"]),
      data: calldata as Hex,
    });
    const executionData = decoded.args[1] as Hex;
    let calls: readonly { to: string; value: bigint; data: string }[];
    try {
      calls = decodeAbiParameters([CALL_TUPLE], executionData)[0];
    } catch {
      calls = decodeAbiParameters([CALL_TUPLE, { name: "opData", type: "bytes" }], executionData)[0];
    }
    return {
      kind: "erc7821Execute",
      source: "signature",
      name: "execute",
      label: "ERC-7821 execute",
      signature: "execute(bytes32,bytes)",
      selector: "0xe9ae5c53",
      inputs: [],
      args: [decoded.args[0]],
      txs: calls.map((c) => ({ to: c.to, value: c.value.toString(), data: c.data })),
    };
  } catch {
    return null;
  }
}

/**
 * Safe MultiSend `transactions` bytes, packed per transaction as
 * operation (1 byte) | to (20) | value (32) | dataLength (32) | data.
 * Every byte must be consumed. Aggressive: top level only, unless the context
 * (a multiSend(bytes) call) says it is MultiSend data.
 */
export function decodeSafeMultiSendTransactionsParam(bytes: string, source: DecodeSource): ParsedCall | null {
  const hex = bytes.startsWith("0x") ? bytes.slice(2) : bytes;
  if (!hex.length || !/^[0-9a-fA-F]*$/.test(hex)) return null;
  const txs: BatchTx[] = [];
  let i = 0;
  try {
    while (i < hex.length) {
      const operation = hex.slice(i, i + 2);
      const toEnd = i + 2 + 40;
      const to = hex.slice(i + 2, toEnd);
      const valueEnd = toEnd + 64;
      const value = hex.slice(toEnd, valueEnd);
      const lenEnd = valueEnd + 64;
      const len = hex.slice(valueEnd, lenEnd);
      if (operation.length < 2 || to.length < 40 || value.length < 64 || len.length < 64) return null;
      const dataLength = Number(hexToBigInt(startHexWith0x(len)));
      if (!Number.isSafeInteger(dataLength)) return null;
      const dataEnd = lenEnd + dataLength * 2;
      if (dataEnd > hex.length) return null;
      txs.push({
        operation,
        to: getAddress(`0x${to}`),
        value: hexToBigInt(startHexWith0x(value)).toString(),
        data: `0x${hex.slice(lenEnd, dataEnd)}`,
      });
      i = dataEnd;
    }
  } catch {
    return null;
  }
  if (i === 0 || i !== hex.length) return null;
  return {
    kind: "safeMultiSend",
    source,
    name: "",
    label: "Safe MultiSend transactions",
    signature: "transactions(tuple(uint8,address,uint256,uint256,bytes)[])",
    selector: null,
    inputs: [],
    args: [],
    txs,
  };
}

/**
 * Abi-guesser on data without a selector (abi.encode output). Aggressive: top
 * level only.
 */
export function decodeABIEncodedData(calldata: string): ParsedCall | null {
  try {
    const guessed = guessAbiEncodedData(calldata);
    if (!guessed || !guessed.length) return null;
    const types = guessed.map((p) => p.format());
    const decoded = coder.decode(types, calldata);
    // Upstream edge case: a guess that returns the input unchanged decoded nothing.
    if (decoded.length === 1 && decoded[0] === calldata) return null;
    const inputs = types.map((t) => ParamType.from(t));
    return {
      kind: "abiEncoded",
      source: "guess",
      name: "",
      label: "ABI-encoded data",
      signature: `abi.encode(${types.join(",")})`,
      selector: null,
      inputs,
      args: decoded.toArray(false),
    };
  } catch {
    return null;
  }
}

/** Abi-guesser on calldata whose selector is unknown. Aggressive: top level only. */
export function decodeByGuessingFunctionFragment(calldata: string): ParsedCall | null {
  if (calldata.length < 10) return null;
  const selector = calldata.slice(0, 10).toLowerCase();
  try {
    const frag = guessFragment(calldata);
    if (!frag) return null;
    const fragment = FunctionFragment.from(frag.format());
    const decoded = coder.decode(fragment.inputs, `0x${calldata.substring(10)}`);
    const types = fragment.inputs.map((p) => p.format());
    return {
      kind: "call",
      source: "guess",
      name: "",
      label: null,
      signature: `${selector}(${types.join(",")})`,
      selector,
      inputs: fragment.inputs,
      args: decoded.toArray(false),
    };
  } catch {
    return null;
  }
}

/** Universal Router path: tokenA (20 bytes) | fee (3) | tokenB (20), exactly. */
export function decodeUniversalRouterPath(calldata: string): ParsedCall | null {
  const path = calldata.startsWith("0x") ? calldata.slice(2) : calldata;
  if (path.length !== 86 || !/^[0-9a-fA-F]+$/.test(path)) return null;
  try {
    const tokenA = getAddress(`0x${path.slice(0, 40)}`);
    const fee = hexToBigInt(startHexWith0x(path.slice(40, 46))).toString();
    const tokenB = getAddress(`0x${path.slice(46, 86)}`);
    return {
      kind: "universalRouterPath",
      source: "guess",
      name: "path",
      label: "Universal Router path",
      signature: "path(address,uint24,address)",
      selector: null,
      inputs: [ParamType.from("address tokenA"), ParamType.from("uint24 fee"), ParamType.from("address tokenB")],
      args: [tokenA, fee, tokenB],
    };
  } catch {
    return null;
  }
}

const UNIVERSAL_ROUTER_COMMANDS: Record<string, string> = {
  "00": "V3_SWAP_EXACT_IN",
  "01": "V3_SWAP_EXACT_OUT",
  "02": "PERMIT2_TRANSFER_FROM",
  "03": "PERMIT2_PERMIT_BATCH",
  "04": "SWEEP",
  "05": "TRANSFER",
  "06": "PAY_PORTION",
  "08": "V2_SWAP_EXACT_IN",
  "09": "V2_SWAP_EXACT_OUT",
  "0a": "PERMIT2_PERMIT",
  "0b": "WRAP_ETH",
  "0c": "UNWRAP_WETH",
  "0d": "PERMIT2_TRANSFER_FROM_BATCH",
  "0e": "BALANCE_CHECK_ERC20",
  "10": "SEAPORT_V1_5",
  "11": "LOOKS_RARE_V2",
  "12": "NFTX",
  "13": "CRYPTOPUNKS",
  "15": "OWNER_CHECK_721",
  "16": "OWNER_CHECK_1155",
  "17": "SWEEP_ERC721",
  "18": "X2Y2_721",
  "19": "SUDOSWAP",
  "1a": "NFT20",
  "1b": "X2Y2_1155",
  "1c": "FOUNDATION",
  "1d": "SWEEP_ERC1155",
  "1e": "ELEMENT_MARKET",
  "20": "SEAPORT_V1_4",
  "21": "EXECUTE_SUB_PLAN",
  "22": "APPROVE_ERC20",
};

/** Universal Router command bytes: every byte must be a known command. */
export function decodeUniversalRouterCommands(calldata: string): ParsedCall | null {
  const bytes = (calldata.startsWith("0x") ? calldata.slice(2) : calldata).toLowerCase();
  if (!bytes.length || bytes.length % 2) return null;
  const commands: string[] = [];
  for (let i = 0; i < bytes.length; i += 2) {
    const c = UNIVERSAL_ROUTER_COMMANDS[bytes.slice(i, i + 2)];
    if (c === undefined) return null;
    commands.push(c);
  }
  return {
    kind: "universalRouterCommands",
    source: "guess",
    name: "commands",
    label: "Universal Router commands",
    signature: "commands(string[])",
    selector: null,
    inputs: [ParamType.from("string[] commands")],
    args: [commands],
  };
}

/** The bytes as UTF-8 text, when at least 80% printable and no NUL bytes. */
export function decodeAsUtf8Text(calldata: string): ParsedCall | null {
  const hexData = startHexWith0x(calldata);
  if (hexData.length < 4 || !isHex(hexData)) return null;
  let text: string;
  try {
    text = hexToString(hexData);
  } catch {
    return null;
  }
  if (!text || text.includes("\0") || text.includes("�")) return null;
  const chars = [...text];
  const printable = chars.filter((ch) => {
    const code = ch.charCodeAt(0);
    return (code >= 32 && code <= 126) || code === 9 || code === 10 || code === 13 || code > 127;
  }).length;
  if (printable / chars.length < 0.8) return null;
  return {
    kind: "text",
    source: "text",
    name: "",
    label: "UTF-8 text message",
    signature: "text(string)",
    selector: null,
    inputs: [ParamType.from("string text")],
    args: [text],
  };
}

/* RECURSIVE DECODING (main entry point) ------------------------------------------------ */

const SAFE_MULTISEND_SELECTOR = "0x8d80ff0a"; // multiSend(bytes)

const BATCH_TUPLE_TYPE = "tuple(address,uint256,bytes)";
const SAFE_TUPLE_TYPE = "tuple(uint8,address,uint256,uint256,bytes)";
const OPERATIONS: Record<string, string> = { "00": "CALL", "01": "DELEGATECALL", "02": "CREATE" };

/**
 * Decodes calldata and, recursively, every dynamic `bytes` param that is
 * itself calldata. `_depth` is 0 at the top; nested bytes are decoded at
 * depth + 1, where the heuristic decoders are not tried.
 */
export async function decodeRecursive({
  calldata,
  address,
  abi,
  _depth = 0,
}: {
  calldata: string;
  address?: string | null;
  abi?: unknown;
  _depth?: number;
}): Promise<DecodedCall | null> {
  if (typeof calldata !== "string") return null;
  const data = startHexWith0x(calldata.trim());
  if (data === "0x" || !isHex(data) || _depth > 8) return null;

  let parsed: ParsedCall | null = null;
  if (abi) {
    parsed = decodeWithABI({ abi, calldata: data }) ?? (await decodeWithSelector({ calldata: data, _depth }));
  } else if (address) {
    parsed = await decodeWithAddress({ calldata: data, address, _depth });
  } else {
    parsed = await decodeWithSelector({ calldata: data, _depth });
  }
  if (!parsed) return null;
  return buildCall(parsed, { address, abi, _depth });
}

async function buildCall(
  parsed: ParsedCall,
  ctx: { address?: string | null; abi?: unknown; _depth: number },
): Promise<DecodedCall> {
  const base = {
    functionName: parsed.name,
    label: parsed.label,
    signature: parsed.signature,
    selector: parsed.selector,
    source: parsed.source,
    kind: parsed.kind,
  };

  // Safe MultiSend and ERC-7821 batches: each inner call decoded against its target.
  if (parsed.txs) {
    const isSafe = parsed.kind === "safeMultiSend";
    const tupleType = isSafe ? SAFE_TUPLE_TYPE : BATCH_TUPLE_TYPE;
    const txNodes = await Promise.all(
      parsed.txs.map(async (tx, i): Promise<DecodedNode> => {
        const children: DecodedNode[] = [];
        if (isSafe) {
          const op = tx.operation ?? "00";
          children.push({
            name: "operation",
            type: "uint8",
            baseType: "uint8",
            value: `${Number.parseInt(op, 16)}${OPERATIONS[op] ? ` (${OPERATIONS[op]})` : ""}`,
          });
        }
        children.push({ name: "to", type: "address", baseType: "address", value: tx.to });
        children.push({ name: "value", type: "uint256", baseType: "uint256", value: tx.value });
        if (isSafe) {
          children.push({
            name: "dataLength",
            type: "uint256",
            baseType: "uint256",
            value: String((tx.data.length - 2) / 2),
          });
        }
        children.push({
          name: "data",
          type: "bytes",
          baseType: "bytes",
          value: tx.data,
          nested: tx.data !== "0x" ? await decodeRecursive({ calldata: tx.data, address: tx.to, _depth: ctx._depth + 1 }) : null,
        });
        return { name: `tx #${i}`, type: tupleType, baseType: "tuple", value: null, children };
      }),
    );
    const params: DecodedNode[] = [];
    if (parsed.kind === "erc7821Execute") {
      params.push({ name: "mode", type: "bytes32", baseType: "bytes32", value: String(parsed.args[0]) });
    }
    params.push({ name: isSafe ? "transactions" : "calls", type: `${tupleType}[]`, baseType: "array", value: null, children: txNodes });
    return { ...base, params };
  }

  const hint = parsed.selector?.toLowerCase() === SAFE_MULTISEND_SELECTOR ? "safeMultiSend" : undefined;
  const params = await Promise.all(
    parsed.inputs.map((input, i) =>
      decodeParamTypes({
        input,
        value: parsed.args[i],
        address: ctx.address,
        // A guessed or looked-up decode must not pass a caller's ABI to nested
        // bytes it was not about.
        abi: parsed.source === "abi" ? ctx.abi : undefined,
        _depth: ctx._depth,
        hint: input.baseType === "bytes" && input.type === "bytes" ? hint : undefined,
      }),
    ),
  );
  return { ...base, params };
}

/* PARAMETER TYPE DECODERS --------------------------------------------------------------- */

type Hint = "safeMultiSend" | undefined;

function leaf(value: unknown): string | boolean {
  if (typeof value === "boolean") return value;
  if (typeof value === "bigint") return value.toString();
  if (typeof value === "string") return value;
  if (typeof value === "number") return String(value);
  return String(value);
}

/** Recursively decodes one param by its type. */
export async function decodeParamTypes({
  input,
  value,
  address,
  abi,
  _depth = 0,
  hint,
  indexed,
}: {
  input: ParamType;
  value: unknown;
  address?: string | null;
  abi?: unknown;
  _depth?: number;
  hint?: Hint;
  indexed?: boolean;
}): Promise<DecodedNode> {
  const node: DecodedNode = { name: input.name ?? "", type: input.type, baseType: input.baseType, value: null };
  if (indexed !== undefined) node.indexed = indexed;

  // An indexed dynamic event value is only its hash.
  if (Indexed.isIndexed(value)) {
    node.value = value.hash ?? null;
    node.hashed = true;
    return node;
  }

  if (input.baseType.includes("int")) {
    try {
      node.value = BigInt(value as bigint).toString();
    } catch {
      node.value = leaf(value);
    }
  } else if (input.baseType === "address") {
    try {
      node.value = getAddress(String(value));
    } catch {
      node.value = leaf(value);
    }
  } else if (input.baseType === "bytes") {
    node.value = String(value);
    node.nested = await decodeBytesParam({ value: String(value), address, abi, _depth, hint });
  } else if (input.baseType.startsWith("bytes")) {
    // Fixed bytesN: ids, hashes, salts. Not decoded as calldata.
    node.value = String(value);
  } else if (input.baseType === "tuple") {
    node.children = await decodeTupleParam({ input, value, address, abi, _depth });
  } else if (input.baseType === "array") {
    node.children = await decodeArrayParam({ input, value, address, abi, _depth });
  } else {
    node.value = leaf(value);
  }
  return node;
}

/** A bytes param, decoded as nested calldata at depth + 1 (or as MultiSend data). */
export async function decodeBytesParam({
  value,
  address,
  abi,
  _depth = 0,
  hint,
}: {
  value: string;
  address?: string | null;
  abi?: unknown;
  _depth?: number;
  hint?: Hint;
}): Promise<DecodedCall | null> {
  if (!value || value === "0x") return null;
  if (hint === "safeMultiSend") {
    const multi = decodeSafeMultiSendTransactionsParam(value, "signature");
    if (multi) return buildCall(multi, { _depth: _depth + 1 });
  }
  const hex = value.startsWith("0x") ? value.slice(2) : value;
  // Calldata is a selector plus whole words; anything else is not decoded here.
  if (hex.length < 8 || !isWordAligned(hex.slice(8))) {
    // ERC-7821 and packed data are the exceptions handled by their own decoders.
    if (hex.slice(0, 8).toLowerCase() !== "e9ae5c53") return null;
  }
  try {
    return await decodeRecursive({ calldata: value, address, abi, _depth: _depth + 1 });
  } catch {
    return null;
  }
}

/** A tuple: each component decoded; bytes next to a single address are decoded against it. */
export async function decodeTupleParam({
  input,
  value,
  address,
  abi,
  _depth = 0,
}: {
  input: ParamType;
  value: unknown;
  address?: string | null;
  abi?: unknown;
  _depth?: number;
}): Promise<DecodedNode[]> {
  const components = input.components ?? [];
  if (!components.length) return [];
  const values = toList(value);
  const addressIdx = components.map((c, i) => (c.baseType === "address" ? i : -1)).filter((i) => i >= 0);
  const hasBytes = components.some((c) => c.baseType === "bytes");
  let target = address;
  let targetAbi = abi;
  if (addressIdx.length === 1 && hasBytes) {
    const a = values[addressIdx[0]];
    if (typeof a === "string" && /^0x[0-9a-fA-F]{40}$/.test(a)) {
      target = a;
      targetAbi = undefined;
    }
  }
  return Promise.all(
    components.map((component, i) =>
      decodeParamTypes({
        input: component,
        value: values[i],
        address: component.baseType === "bytes" ? target : address,
        abi: component.baseType === "bytes" ? targetAbi : abi,
        _depth,
      }),
    ),
  );
}

/** An array: each element decoded with the element type. */
export async function decodeArrayParam({
  input,
  value,
  address,
  abi,
  _depth = 0,
}: {
  input: ParamType;
  value: unknown;
  address?: string | null;
  abi?: unknown;
  _depth?: number;
}): Promise<DecodedNode[]> {
  const list = toList(value);
  const child = input.arrayChildren;
  if (!child || !list.length) return [];
  return Promise.all(list.map((v) => decodeParamTypes({ input: child, value: v, address, abi, _depth })));
}

function toList(value: unknown): unknown[] {
  if (Array.isArray(value)) return value;
  if (value && typeof value === "object" && typeof (value as { toArray?: unknown }).toArray === "function") {
    return (value as { toArray: (deep?: boolean) => unknown[] }).toArray(false);
  }
  return [];
}

/* REVERT DATA (not in upstream) ------------------------------------------------------------ */

/**
 * Decodes revert data: with the given ABI's errors, else the verified ABI of
 * `address`, else a signature from our error-signature API (Error(string) and
 * Panic(uint256) are always known). A signature must re-encode to the data
 * exactly. Panic codes get their meaning as the label.
 */
export async function decodeErrorData({
  data,
  abi,
  address,
}: {
  data: string;
  abi?: unknown;
  address?: string | null;
}): Promise<DecodedCall | null> {
  const hex = startHexWith0x((data ?? "").trim());
  if (hex.length < 10 || !isHex(hex)) return null;
  const selector = hex.slice(0, 10).toLowerCase();

  const fromIface = (iface: Interface | null, source: "abi" | "signature") => {
    if (!iface) return null;
    try {
      const e = iface.parseError(hex);
      if (!e) return null;
      if (source === "signature" && iface.encodeErrorResult(e.fragment, e.args.toArray(false)).toLowerCase() !== hex.toLowerCase()) {
        return null;
      }
      return { e, source };
    } catch {
      return null;
    }
  };

  let hit = abi ? fromIface(toInterface(abi), "abi") : null;
  if (!hit && address) hit = fromIface(toInterface(await fetchContractAbi(address)), "abi");
  if (!hit) {
    for (const sig of await lookupErrorSignatures(selector)) {
      try {
        hit = fromIface(new Interface([ErrorFragment.from(`error ${sig}`)]), "signature");
      } catch {
        hit = null;
      }
      if (hit) break;
    }
  }
  if (!hit) return null;
  const { e, source } = hit;
  const params = await Promise.all(
    e.fragment.inputs.map((input, i) => decodeParamTypes({ input, value: e.args[i], address, _depth: 1 })),
  );
  let label = "Custom error";
  if (e.signature === "Error(string)") label = "Revert reason";
  if (e.signature === "Panic(uint256)") {
    const code = String(params[0]?.value ?? "");
    label = `Panic: ${PANIC_CODES[code] ?? `code ${code}`}`;
  }
  return {
    functionName: e.name,
    label,
    signature: e.signature,
    selector,
    source,
    kind: "error",
    params,
  };
}

/* EVENT LOG DECODING -------------------------------------------------------------------- */

async function describeLog(log: LogDescription, source: "abi" | "signature", ctx: { address?: string | null }): Promise<DecodedEvent> {
  const inputs = log.fragment.inputs;
  const params = await Promise.all(
    inputs.map((input, i) =>
      decodeParamTypes({ input, value: log.args[i], address: ctx.address, _depth: 0, indexed: !!input.indexed }),
    ),
  );
  return {
    eventName: log.name,
    signature: log.signature,
    topic: log.topic ?? null,
    source,
    params,
  };
}

/** Every k-subset of n positions, in order, at most `cap` of them. */
function combinations(n: number, k: number, cap: number): number[][] {
  const out: number[][] = [];
  const pick: number[] = [];
  const walk = (start: number) => {
    if (out.length >= cap) return;
    if (pick.length === k) {
      out.push([...pick]);
      return;
    }
    for (let i = start; i < n; i++) {
      pick.push(i);
      walk(i + 1);
      pick.pop();
    }
  };
  walk(0);
  return out;
}

/**
 * Decodes a log from a text signature ("Transfer(address,address,uint256)")
 * without knowing which params are indexed: every placement of the indexed
 * params is tried, and one whose non-indexed data re-encodes exactly wins.
 */
function decodeEventWithSignature(signature: string, topics: string[], data: string): LogDescription | null {
  let base: EventFragment;
  try {
    base = EventFragment.from(`event ${signature}`);
  } catch {
    return null;
  }
  const n = base.inputs.length;
  const k = topics.length - 1;
  if (k < 0 || k > n || k > 3) return null;
  for (const idx of combinations(n, k, 256)) {
    const set = new Set(idx);
    let frag: EventFragment;
    try {
      frag = EventFragment.from({
        type: "event",
        name: base.name,
        anonymous: false,
        inputs: base.inputs.map((p, i) => ({ ...(JSON.parse(p.format("json")) as object), indexed: set.has(i) })),
      });
    } catch {
      continue;
    }
    try {
      const iface = new Interface([frag]);
      const log = iface.parseLog({ topics, data });
      if (!log) continue;
      // Indexed address topics must be left-padded with zeros.
      const okTopics = frag.inputs
        .filter((p) => p.indexed)
        .every((p, j) => p.baseType !== "address" || /^0x0{24}/i.test(topics[j + 1] ?? ""));
      if (!okTopics) continue;
      const nonIndexed = frag.inputs.filter((p) => !p.indexed);
      const values = frag.inputs.map((_, i) => log.args[i]).filter((_, i) => !frag.inputs[i].indexed);
      const again = coder.encode(nonIndexed, values);
      if (again.toLowerCase() !== (data || "0x").toLowerCase()) continue;
      return log;
    } catch {
      /* this placement does not fit */
    }
  }
  return null;
}

/**
 * Decodes one log: with the given ABI, else the emitter's verified ABI, else a
 * signature looked up by topic0. Null when nothing fits (or no topics).
 */
export async function decodeLogRecursive({
  topics,
  data,
  abi,
  address,
}: {
  topics: (string | null)[];
  data: string;
  abi?: unknown;
  address?: string | null;
}): Promise<DecodedEvent | null> {
  const t = topics.filter((x): x is string => typeof x === "string" && x.length > 0);
  if (!t.length) return null;
  const d = data && data !== "" ? data : "0x";
  if (abi) {
    const log = decodeEventWithABI({ abi, topics: t, data: d });
    if (log) return describeLog(log, "abi", { address });
  }
  if (address) {
    const fetched = await fetchContractAbi(address);
    if (fetched) {
      const log = decodeEventWithABI({ abi: fetched, topics: t, data: d });
      if (log) return describeLog(log, "abi", { address });
    }
  }
  for (const sig of await lookupEventSignatures(t[0])) {
    const log = decodeEventWithSignature(sig, t, d);
    if (log) return describeLog(log, "signature", { address });
  }
  return null;
}

/** Upstream's decodeEvents: every log of one contract, those that decode. */
export async function decodeEvents({
  logs,
  address,
  abi,
}: {
  logs: { topics: (string | null)[]; data: string }[];
  address?: string | null;
  abi?: unknown;
}): Promise<DecodedEvent[]> {
  const out: DecodedEvent[] = [];
  for (const log of logs) {
    const ev = await decodeLogRecursive({ topics: log.topics, data: log.data, abi, address });
    if (ev) out.push(ev);
  }
  return out;
}

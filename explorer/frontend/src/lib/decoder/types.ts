/**
 * Result types of the decoder. These replace swiss-knife's
 * DecodeRecursiveResult / Arg / DecodeEventResult (types.ts in their repo):
 * the same tree (a call, its params, tuples and arrays with children, bytes
 * params holding a nested call), with plain JSON-safe values so React can
 * render them without ethers.
 */

/** Where a decode came from, shown to the reader. */
export type DecodeSource =
  /** A verified ABI (given by the caller, or the contract's from our API). */
  | "abi"
  /** A signature from our signature API, or a standard format (ERC-7821). */
  | "signature"
  /** Heuristics (abi-guesser, packed formats): may be wrong. */
  | "guess"
  /** The bytes read as UTF-8 text. */
  | "text";

/** Special formats the decoder recognises, beyond a plain function call. */
export type CallKind =
  | "call"
  | "erc7821Execute"
  | "safeMultiSend"
  | "universalRouterPath"
  | "universalRouterCommands"
  | "abiEncoded"
  | "text"
  /** Revert data: Error(string), Panic(uint256) or a custom error. */
  | "error";

/** One decoded value: a parameter, a tuple component or an array element. */
export interface DecodedNode {
  /** Parameter name; "" when the ABI has none (guesses never have names). */
  name: string;
  /** Canonical Solidity type, e.g. "uint256", "address[]", "tuple(address,uint256)". */
  type: string;
  /**
   * ethers' base type: "address", "bool", "string", "bytes", "bytes32",
   * "uint256", "int24", "tuple", "array".
   */
  baseType: string;
  /**
   * Leaf value as text: integers as decimal strings, addresses EIP-55,
   * bytes as 0x hex, strings as is, booleans as booleans. null for tuples and
   * arrays (see children).
   */
  value: string | boolean | null;
  /** Tuple components or array elements. */
  children?: DecodedNode[];
  /** A dynamic `bytes` value that is itself calldata, decoded. */
  nested?: DecodedCall | null;
  /** Event params: true when the value came from a topic. */
  indexed?: boolean;
  /**
   * An indexed dynamic value (string, bytes, array, tuple) is stored as the
   * keccak hash of its encoding; `value` is that hash.
   */
  hashed?: boolean;
}

/** A decoded function call (or packed batch). */
export interface DecodedCall {
  /** Function name; "" for anonymous decodes (abi.encode guesses, packed formats). */
  functionName: string;
  /** Human label for special formats, e.g. "Safe MultiSend transactions". */
  label: string | null;
  /** Canonical signature "name(type,...)", or "abi.encode(type,...)" for a guess. */
  signature: string;
  /** "0x12345678", or null when the data has no selector (packed formats, text). */
  selector: string | null;
  source: DecodeSource;
  kind: CallKind;
  params: DecodedNode[];
}

/** A decoded event log. */
export interface DecodedEvent {
  eventName: string;
  /** Canonical "Name(type,...)" (no indexed keywords). */
  signature: string;
  /** topic0, or null for an anonymous event. */
  topic: string | null;
  source: "abi" | "signature";
  params: DecodedNode[];
}

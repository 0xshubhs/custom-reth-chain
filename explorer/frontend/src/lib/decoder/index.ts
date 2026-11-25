/**
 * The explorer's calldata and event decoder, a port of ETH.sh's (swiss-knife)
 * decoder (MIT, see LICENSE here; what changed is listed at the top of
 * decoder.ts). It pulls in ethers and abi-guesser, so import it lazily from
 * UI code: `const { decodeCalldata } = await import("@/lib/decoder")`.
 *
 * Public API
 *
 *   decodeCalldata({ calldata, abi?, address? }): Promise<DecodedCall | null>
 *     Order: the given ABI, else the verified ABI of `address` from our API
 *     (a proxy's implementation ABI first, an EIP-7702 account's delegate),
 *     else a selector lookup in our signature API, else (top level only)
 *     ERC-7821 / Safe MultiSend / Universal Router formats, abi-guesser
 *     guesses (source "guess") and UTF-8 text (source "text"). Dynamic bytes
 *     params that are calldata come back decoded in `node.nested`. Null for
 *     empty input ("0x") or when nothing fits.
 *
 *   decodeLog({ topics, data, abi?, address? }): Promise<DecodedEvent | null>
 *     The given ABI, else the emitter's verified ABI, else an event signature
 *     looked up by topic0 (which params are indexed is worked out).
 *
 *   decodeRevert({ data, abi?, address? }): Promise<DecodedCall | null>
 *     Revert data from a failed call: Error(string) (label "Revert reason"),
 *     Panic(uint256) (label "Panic: <meaning>"), or a custom error from the
 *     ABI, the address's verified ABI, or our error-signature API.
 *     kind is "error"; render it with DecodedCallView.
 *
 *   ethShCalldataUrl(calldata): string
 *     The same calldata in ETH.sh's decoder (an external link). Also in
 *     ./links, which has no ethers import, for eager UI code.
 *
 *   setDecoderApiBase(base): point lookups at another API base (tests).
 *
 * Types: DecodedCall, DecodedEvent, DecodedNode, DecodeSource, CallKind.
 */
import { decodeErrorData, decodeLogRecursive, decodeRecursive } from "./decoder";
import type { DecodedCall, DecodedEvent } from "./types";

export type { CallKind, DecodeSource, DecodedCall, DecodedEvent, DecodedNode } from "./types";
export { setDecoderApiBase, getDecoderApiBase, fetchContractAbi } from "./api";
export { ethShCalldataUrl, looksLikeCalldata } from "./links";
export {
  decodeWithABI,
  decodeWithSelector,
  decodeWithAddress,
  decodeRecursive,
  decodeEvents,
  decodeEventWithABI,
  decodeErrorData,
  toInterface,
} from "./decoder";

export async function decodeCalldata(opts: {
  calldata: string;
  abi?: unknown[] | null;
  address?: string | null;
}): Promise<DecodedCall | null> {
  try {
    return await decodeRecursive({
      calldata: opts.calldata,
      abi: opts.abi && opts.abi.length ? opts.abi : undefined,
      address: opts.address ?? undefined,
    });
  } catch {
    return null;
  }
}

export async function decodeLog(opts: {
  topics: (string | null)[];
  data: string;
  abi?: unknown[] | null;
  address?: string | null;
}): Promise<DecodedEvent | null> {
  try {
    return await decodeLogRecursive({
      topics: opts.topics,
      data: opts.data,
      abi: opts.abi && opts.abi.length ? opts.abi : undefined,
      address: opts.address ?? undefined,
    });
  } catch {
    return null;
  }
}

export async function decodeRevert(opts: {
  data: string;
  abi?: unknown[] | null;
  address?: string | null;
}): Promise<DecodedCall | null> {
  try {
    return await decodeErrorData({
      data: opts.data,
      abi: opts.abi && opts.abi.length ? opts.abi : undefined,
      address: opts.address ?? undefined,
    });
  } catch {
    return null;
  }
}

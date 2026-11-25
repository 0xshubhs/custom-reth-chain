/**
 * Client-side decoding with the ETH.sh decoder port (src/lib/decoder), for
 * what the API could not decode. Loaded lazily, so pages that never need it
 * never download ethers or the ABI guesser.
 */
import type { DecodedParam, Log } from "@/lib/types";

export interface ClientDecodedLog {
  decoded: { event: string; signature: string; params: DecodedParam[] };
  source: "abi" | "signature" | "guess";
}

/** Placeholder until the decoder lands. */
export function ethShCalldataUrl(calldata: string): string {
  return `https://calldata.swiss-knife.xyz/decoder?calldata=${encodeURIComponent(calldata)}`;
}

/** Placeholder until the decoder lands: nothing extra is decoded. */
export function useClientDecodedLog(_log: Log): ClientDecodedLog | null {
  return null;
}

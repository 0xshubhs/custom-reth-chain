/**
 * Small helpers with no ethers import, safe to load with any page.
 */

/** ETH.sh's calldata decoder with this calldata filled in (an external link). */
export function ethShCalldataUrl(calldata: string): string {
  return `https://calldata.eth.sh/decoder?calldata=${encodeURIComponent(calldata.trim())}`;
}

/** A selector plus whole 32-byte words: worth trying as calldata. */
export function looksLikeCalldata(hex: string): boolean {
  const h = hex.startsWith("0x") ? hex.slice(2) : hex;
  return /^[0-9a-fA-F]*$/.test(h) && h.length >= 8 && (h.length - 8) % 64 === 0;
}

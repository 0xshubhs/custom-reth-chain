/**
 * "Add the chain to MetaMask": EIP-3085 wallet_addEthereumChain through the
 * injected provider. No wallet library: this is the only wallet call the
 * explorer makes, and it goes to the extension, not the network, so the CSP
 * needs no allowance for it.
 */
import type { Config } from "./types";

interface Eip1193 {
  request(args: { method: string; params?: unknown[] }): Promise<unknown>;
}

declare global {
  interface Window {
    ethereum?: Eip1193;
  }
}

export type AddNetworkResult =
  | { ok: true }
  | { ok: false; reason: "no-wallet" | "rejected" | "error"; message: string };

export function hasWallet(): boolean {
  return typeof window !== "undefined" && !!window.ethereum?.request;
}

/**
 * The explorer URL handed to the wallet is the page's own origin: /api/v2/config
 * does not carry one (it has chain_id, network_name, native_symbol and
 * public_rpc_url), and the site is always served from the explorer's origin.
 */
export async function addNetwork(cfg: Config): Promise<AddNetworkResult> {
  const eth = typeof window !== "undefined" ? window.ethereum : undefined;
  if (!eth?.request) {
    return { ok: false, reason: "no-wallet", message: "No browser wallet found." };
  }
  const chainId = `0x${cfg.chain_id.toString(16)}`;
  const explorer = (cfg as Config & { explorer_url?: string }).explorer_url || window.location.origin;
  try {
    await eth.request({
      method: "wallet_addEthereumChain",
      params: [
        {
          chainId,
          chainName: cfg.network_name,
          nativeCurrency: { name: cfg.native_symbol, symbol: cfg.native_symbol, decimals: 18 },
          rpcUrls: [cfg.public_rpc_url],
          blockExplorerUrls: [explorer],
        },
      ],
    });
    return { ok: true };
  } catch (e) {
    const err = e as { code?: number; message?: string };
    if (err?.code === 4001) {
      return { ok: false, reason: "rejected", message: "The request was declined in the wallet." };
    }
    return { ok: false, reason: "error", message: err?.message || "The wallet could not add the network." };
  }
}

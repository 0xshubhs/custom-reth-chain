/**
 * The visitor's injected wallet (window.ethereum) for Write Contract, through
 * viem's wallet client and nothing else (no wagmi). Before a write the wallet
 * is moved to the chain (wallet_switchEthereumChain, or
 * wallet_addEthereumChain when it does not know the chain yet). Every request
 * goes to the extension, never to the network from this page, so the CSP
 * needs no allowance.
 */
import { useCallback, useEffect, useState } from "react";
import { createWalletClient, custom, defineChain, type Address, type Hex, type WalletClient } from "viem";

import { useChain } from "@/lib/chain";
import type { Config } from "@/lib/types";

function chainOf(cfg: Config) {
  return defineChain({
    id: cfg.chain_id,
    name: cfg.network_name,
    nativeCurrency: { name: cfg.native_symbol, symbol: cfg.native_symbol, decimals: 18 },
    rpcUrls: { default: { http: [cfg.public_rpc_url] } },
  });
}

function provider() {
  return typeof window !== "undefined" ? window.ethereum : undefined;
}

export function walletError(e: unknown): string {
  const err = e as { code?: number; shortMessage?: string; message?: string; cause?: { code?: number } };
  if (err?.code === 4001 || err?.cause?.code === 4001) return "The request was declined in the wallet.";
  return (err?.shortMessage || err?.message || "The wallet could not complete the request.").split("\n")[0];
}

async function ensureChain(cfg: Config) {
  const eth = provider();
  if (!eth) throw new Error("No browser wallet found.");
  const chainId = `0x${cfg.chain_id.toString(16)}`;
  try {
    await eth.request({ method: "wallet_switchEthereumChain", params: [{ chainId }] });
  } catch (e) {
    const code = (e as { code?: number; data?: { originalError?: { code?: number } } })?.code;
    const nested = (e as { data?: { originalError?: { code?: number } } })?.data?.originalError?.code;
    if (code !== 4902 && nested !== 4902) throw e;
    await eth.request({
      method: "wallet_addEthereumChain",
      params: [
        {
          chainId,
          chainName: cfg.network_name,
          nativeCurrency: { name: cfg.native_symbol, symbol: cfg.native_symbol, decimals: 18 },
          rpcUrls: [cfg.public_rpc_url],
          blockExplorerUrls: [(cfg as Config & { explorer_url?: string }).explorer_url || window.location.origin],
        },
      ],
    });
  }
}

export function useWallet() {
  const cfg = useChain();
  const [account, setAccount] = useState<Address | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const available = !!provider()?.request;

  const client = useCallback((): WalletClient => {
    const eth = provider();
    if (!eth) throw new Error("No browser wallet found.");
    return createWalletClient({ chain: chainOf(cfg), transport: custom(eth as Parameters<typeof custom>[0]) });
  }, [cfg]);

  // Pick up an already-authorised account without prompting.
  useEffect(() => {
    const eth = provider();
    if (!eth) return;
    eth
      .request({ method: "eth_accounts" })
      .then((a) => {
        const list = a as string[];
        if (list?.[0]) setAccount(list[0] as Address);
      })
      .catch(() => undefined);
  }, []);

  const connect = useCallback(async () => {
    setError(null);
    setBusy(true);
    try {
      const [a] = await client().requestAddresses();
      setAccount(a ?? null);
      return a ?? null;
    } catch (e) {
      setError(walletError(e));
      return null;
    } finally {
      setBusy(false);
    }
  }, [client]);

  /** Switch to the chain, then send the call; resolves to the tx hash. */
  const send = useCallback(
    async (to: Address, data: Hex, value: bigint): Promise<Hex> => {
      const from = account ?? (await connect());
      if (!from) throw new Error("Connect a wallet first.");
      await ensureChain(cfg);
      return client().sendTransaction({ account: from, to, data, value, chain: chainOf(cfg) });
    },
    [account, cfg, client, connect],
  );

  return { available, account, connect, send, busy, error };
}

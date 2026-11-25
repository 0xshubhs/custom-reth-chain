import { Link } from "@tanstack/react-router";

import { useChain } from "@/lib/chain";
import { AddNetworkButton } from "./AddNetworkButton";
import { LogoMark, LogoWordmark } from "./BrandLogo";
import { CopyButton } from "./ui";

export function Footer() {
  const cfg = useChain();
  return (
    <footer className="mt-16 border-t border-line bg-card/60">
      <div className="mx-auto grid max-w-[1400px] gap-8 px-4 py-10 sm:px-6 md:grid-cols-[1.4fr_1fr_1.2fr]">
        <div className="min-w-0">
          <div className="flex items-center gap-2.5">
            <LogoMark height={24} width={24} />
            <LogoWordmark height={13} className="w-auto" />
          </div>
          <p className="mt-3 max-w-sm text-sm text-muted">
            The block explorer for the {cfg.network_name} chain: blocks, transactions, tokens and verified contracts,
            straight from the node.
          </p>
        </div>

        <div>
          <h2 className="mb-3 text-xs font-bold uppercase tracking-[0.12em] text-dim">Explore</h2>
          <ul className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm md:grid-cols-1">
            <li>
              <Link to="/blocks" className="text-fg2 hover:text-link">
                Blocks
              </Link>
            </li>
            <li>
              <Link to="/txs" className="text-fg2 hover:text-link">
                Transactions
              </Link>
            </li>
            <li>
              <Link to="/tokens" className="text-fg2 hover:text-link">
                Tokens
              </Link>
            </li>
            <li>
              <Link to="/verified-contracts" className="text-fg2 hover:text-link">
                Verified contracts
              </Link>
            </li>
            <li>
              <Link to="/contract-verification" className="text-fg2 hover:text-link">
                Verify a contract
              </Link>
            </li>
          </ul>
        </div>

        <div className="min-w-0">
          <h2 className="mb-3 text-xs font-bold uppercase tracking-[0.12em] text-dim">Network</h2>
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm">
            <dt className="text-muted">Chain ID</dt>
            <dd className="font-mono text-fg">{cfg.chain_id}</dd>
            <dt className="text-muted">Currency</dt>
            <dd className="text-fg">{cfg.native_symbol}</dd>
            <dt className="text-muted">RPC</dt>
            <dd className="flex min-w-0 items-center gap-1">
              <span className="truncate font-mono text-[13px] text-fg">{cfg.public_rpc_url}</span>
              <CopyButton value={cfg.public_rpc_url} label="Copy RPC URL" />
            </dd>
          </dl>
          <AddNetworkButton className="mt-4" />
        </div>
      </div>
      <div className="border-t border-line">
        <p className="mx-auto max-w-[1400px] px-4 py-4 text-xs text-dim sm:px-6">
          {cfg.network_name} chain explorer. Data is indexed from the chain node; amounts are shown in{" "}
          {cfg.native_symbol} (18 decimals) unless a token says otherwise.
        </p>
      </div>
    </footer>
  );
}

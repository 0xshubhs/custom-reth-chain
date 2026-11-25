/**
 * How chain entities are shown and linked: addresses (with Etherscan's
 * same-address highlight), transactions, blocks, tokens, times and values.
 * Text first: the only glyph is a tiny contract mark.
 */
import { Link } from "@tanstack/react-router";
import { FileText } from "lucide-react";

import { useChain } from "@/lib/chain";
import { cx } from "@/lib/cx";
import {
  formatAmount,
  formatAmountFull,
  formatDateTime,
  formatUtc,
  formatUtcShort,
  sameAddr,
  shortHash,
  timeAgo,
} from "@/lib/format";
import { useNow } from "@/lib/now";
import { useAddressHighlight, useTimeMode } from "@/lib/prefs";
import { NATIVE_DECIMALS } from "@/lib/site";
import type { AddressRef, TokenRef } from "@/lib/types";
import { Tooltip } from "./overlay";
import { CopyButton } from "./ui";

/* Addresses ------------------------------------------------------------------- */

function ContractMark({ verified }: { verified: boolean }) {
  return (
    <span className="inline-flex shrink-0 text-dim" title={verified ? "Contract (verified)" : "Contract"}>
      <FileText className="h-3 w-3" aria-label="Contract" />
    </span>
  );
}

/**
 * An address from the API. Shows the name when there is one, else the
 * shortened hash; hovering it marks every link to the same address on the
 * page. `current` is the page's own address: shown plain, not linked.
 */
export function AddressLink({
  addr,
  current,
  full = false,
  copy = false,
  maxW = "max-w-[170px]",
  icon = true,
  compact = false,
}: {
  addr: AddressRef | null | undefined;
  current?: string;
  full?: boolean;
  copy?: boolean;
  maxW?: string;
  /** The contract mark (EOAs never get one). */
  icon?: boolean;
  /** Shorter hash for tight rows. */
  compact?: boolean;
}) {
  const hl = useAddressHighlight(addr?.hash);
  if (!addr) return <span className="text-dim">None</span>;
  const short = compact ? shortHash(addr.hash, 6, 4) : shortHash(addr.hash, 10, 8);
  const label = addr.name || (full ? addr.hash : short);
  const isSelf = sameAddr(addr.hash, current);
  const title = addr.name ? `${addr.name}\n${addr.hash}` : addr.hash;
  const mono = !addr.name;
  const hover = { onMouseEnter: hl.enter, onMouseLeave: hl.leave };

  if (full && addr.name && !isSelf) {
    return (
      <span className="inline-flex min-w-0 max-w-full flex-wrap items-center gap-x-2 gap-y-0.5 align-middle">
        <span className="inline-flex min-w-0 items-center gap-1">
          {icon && addr.is_contract ? <ContractMark verified={addr.is_verified} /> : null}
          <Link
            to="/address/$addr"
            params={{ addr: addr.hash }}
            title={addr.hash}
            {...hover}
            className={cx("link font-medium", hl.on && "addr-hl")}
          >
            {addr.name}
          </Link>
        </span>
        <span className="inline-flex min-w-0 items-center gap-1">
          <span className="min-w-0 break-all font-mono text-[12.5px] text-muted">({addr.hash})</span>
          {copy ? <CopyButton value={addr.hash} label="Copy address" /> : null}
        </span>
      </span>
    );
  }

  return (
    <span className="inline-flex min-w-0 max-w-full items-center gap-1 align-middle">
      {icon && addr.is_contract ? <ContractMark verified={addr.is_verified} /> : null}
      {isSelf ? (
        <span
          title={title}
          {...hover}
          className={cx("truncate text-fg", mono && "font-mono text-[12.5px]", !full && maxW, hl.on && "addr-hl")}
        >
          {label}
        </span>
      ) : (
        <Link
          to="/address/$addr"
          params={{ addr: addr.hash }}
          title={title}
          {...hover}
          className={cx(
            "link truncate",
            mono && "font-mono text-[12.5px]",
            !full && maxW,
            full && "whitespace-normal break-all",
            hl.on && "addr-hl",
          )}
        >
          {label}
        </Link>
      )}
      {copy ? <CopyButton value={addr.hash} label="Copy address" /> : null}
    </span>
  );
}

/** An address known only by its hash (decoded params, token contracts). */
export function AddressHashLink({ hash, full = false, copy = false }: { hash: string; full?: boolean; copy?: boolean }) {
  const hl = useAddressHighlight(hash);
  return (
    <span className="inline-flex min-w-0 max-w-full items-center gap-1">
      <Link
        to="/address/$addr"
        params={{ addr: hash }}
        title={hash}
        onMouseEnter={hl.enter}
        onMouseLeave={hl.leave}
        className={cx("link font-mono text-[12.5px]", full ? "break-all" : "truncate", hl.on && "addr-hl")}
      >
        {full ? hash : shortHash(hash, 10, 8)}
      </Link>
      {copy ? <CopyButton value={hash} label="Copy address" /> : null}
    </span>
  );
}

/** Kept for call sites; a verified mark is now a "Verified" badge in headings. */
export function VerifiedMark({ verified }: { verified: boolean }) {
  return verified ? <span className="sr-only">(verified)</span> : null;
}

/* Transactions and blocks ------------------------------------------------------------- */

export function TxLink({ hash, full = false, className }: { hash: string; full?: boolean; className?: string }) {
  return (
    <Link
      to="/tx/$hash"
      params={{ hash }}
      title={hash}
      className={cx("link font-mono text-[12.5px]", full && "break-all", className)}
    >
      {full ? hash : `${hash.slice(0, 14)}\u2026`}
    </Link>
  );
}

export function BlockLink({ n, className }: { n: number | string | null | undefined; className?: string }) {
  if (n === null || n === undefined) return <span className="text-dim">(pending)</span>;
  return (
    <Link to="/block/$id" params={{ id: String(n) }} className={cx("link tabular-nums", className)}>
      {String(n)}
    </Link>
  );
}

export function BlockHashLink({ hash, full = false }: { hash: string; full?: boolean }) {
  return (
    <Link to="/block/$id" params={{ id: hash }} title={hash} className={cx("link font-mono text-[12.5px]", full && "break-all")}>
      {full ? hash : shortHash(hash, 10, 6)}
    </Link>
  );
}

/* Tokens ----------------------------------------------------------------------- */

export function tokenLabel(t: Pick<TokenRef, "name" | "symbol" | "address">): string {
  if (t.name && t.symbol) return `${t.name} (${t.symbol})`;
  return t.name || t.symbol || shortHash(t.address, 8, 6);
}

export function TokenLink({
  token,
  maxW = "max-w-[220px]",
  showSymbol = true,
}: {
  token: Pick<TokenRef, "name" | "symbol" | "address">;
  maxW?: string;
  showSymbol?: boolean;
}) {
  return (
    <Link
      to="/token/$addr"
      params={{ addr: token.address }}
      title={`${tokenLabel(token)}\n${token.address}`}
      className={cx("link inline-flex min-w-0 items-center gap-1.5 align-middle", maxW)}
    >
      <TokenAvatar symbol={token.symbol || token.name || "?"} />
      <span className="truncate">
        {showSymbol ? tokenLabel(token) : token.name || token.symbol || shortHash(token.address)}
      </span>
    </Link>
  );
}

/**
 * A two-letter disc (LODHAAMARA -> "LO"), coloured from the symbol, so every
 * token is recognisable at a glance without logos. It stays through the icon
 * cleanup on purpose: it tells you WHICH token, it is not decoration.
 */
export function TokenAvatar({ symbol, size = "sm" }: { symbol: string; size?: "sm" | "lg" }) {
  const letters =
    symbol
      .replace(/[^A-Za-z0-9]/g, "")
      .slice(0, 2)
      .toUpperCase() || "?";
  let h = 0;
  for (const c of symbol) h = (h * 31 + c.charCodeAt(0)) % 360;
  const big = size === "lg";
  return (
    <span
      aria-hidden
      className={cx(
        "inline-flex shrink-0 items-center justify-center rounded-full font-bold text-white",
        big ? "h-10 w-10 text-sm" : "h-5 w-5 text-[9px]",
      )}
      style={{ background: `linear-gradient(135deg, hsl(0 0% ${22 + (h % 24)}%), hsl(0 0% ${12 + (h % 14)}%))` }}
    >
      {letters}
    </span>
  );
}

export function TokenIdLink({ token, id }: { token: string; id: string }) {
  const shown = id.length > 14 ? `${id.slice(0, 6)}…${id.slice(-4)}` : id;
  return (
    <Link to="/token/$addr/instance/$id" params={{ addr: token, id }} title={`Token ID ${id}`} className="link font-mono text-[12.5px]">
      #{shown}
    </Link>
  );
}

/* Time ------------------------------------------------------------------------- */

/** A relative time with the absolute one in a tooltip. */
export function TimeAgo({ iso, className }: { iso: string | null | undefined; className?: string }) {
  const now = useNow();
  if (!iso) return <span className="text-dim">(pending)</span>;
  return (
    <Tooltip content={<span className="whitespace-pre-line">{`${formatDateTime(iso)}\n${formatUtc(iso)}`}</span>}>
      <time dateTime={iso} className={cx("tabular-nums", className)}>
        {timeAgo(iso, now)}
      </time>
    </Tooltip>
  );
}

/** Table cell time that follows the Age / Date Time column toggle. */
export function TimeCell({ iso }: { iso: string | null | undefined }) {
  const { mode } = useTimeMode();
  if (mode === "datetime" && iso) {
    return (
      <time dateTime={iso} className="tabular-nums" title={formatDateTime(iso)}>
        {formatUtcShort(iso)}
      </time>
    );
  }
  return <TimeAgo iso={iso} />;
}

/** The clickable "Age" / "Date Time (UTC)" column header. */
export function TimeHeader() {
  const { mode, toggle } = useTimeMode();
  return (
    <button
      type="button"
      onClick={toggle}
      title={mode === "age" ? "Click to show date and time" : "Click to show age"}
      className="link font-semibold"
    >
      {mode === "age" ? "Age" : "Date Time (UTC)"}
    </button>
  );
}

/** Detail pages: "3 days ago (23 Nov 2026, 15:58:03 GMT+5:30)". */
export function TimeFull({ iso }: { iso: string | null | undefined }) {
  const now = useNow();
  if (!iso) return <span className="text-dim">(pending)</span>;
  return (
    <span className="inline-flex flex-wrap items-baseline gap-x-2">
      <span>{timeAgo(iso, now)}</span>
      <time dateTime={iso} title={formatDateTime(iso)} className="text-muted">
        ({formatUtc(iso)})
      </time>
    </span>
  );
}

/* Values ----------------------------------------------------------------------- */

/** A wei amount in the native coin (ETH, 18 decimals). */
export function NativeValue({
  wei,
  maxFrac = 6,
  full = false,
  symbol = true,
  className,
}: {
  wei: string | null | undefined;
  maxFrac?: number;
  full?: boolean;
  symbol?: boolean;
  className?: string;
}) {
  const { native_symbol } = useChain();
  const exact = formatAmountFull(wei ?? "0", NATIVE_DECIMALS);
  const shown = full ? exact : formatAmount(wei ?? "0", NATIVE_DECIMALS, maxFrac);
  return (
    <span title={`${exact} ${native_symbol}`} className={cx("tabular-nums", full && "[overflow-wrap:anywhere]", className)}>
      {shown}
      {symbol ? <span className="ml-1">{native_symbol}</span> : null}
    </span>
  );
}

/** A token amount scaled by the token's decimals. */
export function TokenValue({
  amount,
  decimals,
  symbol,
  maxFrac = 6,
  full = false,
}: {
  amount: string | null | undefined;
  decimals: number | null | undefined;
  symbol?: string | null;
  maxFrac?: number;
  full?: boolean;
}) {
  const d = decimals ?? 0;
  const exact = formatAmountFull(amount ?? "0", d);
  const shown = full ? exact : formatAmount(amount ?? "0", d, maxFrac);
  return (
    <span title={`${exact}${symbol ? ` ${symbol}` : ""}`} className={cx("tabular-nums", full && "[overflow-wrap:anywhere]")}>
      {shown}
      {symbol ? <span className="ml-1 text-muted">{symbol}</span> : null}
    </span>
  );
}

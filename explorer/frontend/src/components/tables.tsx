/**
 * The list tables, laid out like Etherscan's: an eye to preview a
 * transaction, the method pill, Age / Date Time toggle on the header, IN / OUT
 * / SELF beside the counterparty, and the same-address highlight on hover. On
 * a phone each row becomes a compact card; wide tables never scroll the page.
 */
import type { ReactNode } from "react";
import { ArrowRight } from "lucide-react";

import { cx } from "@/lib/cx";
import { formatInt, formatPct, sameAddr } from "@/lib/format";
import type {
  AddressRef,
  BalancePoint,
  BlockSummary,
  InternalTx,
  Log,
  TokenHolder,
  TokenHolding,
  TokenTransfer,
  TxSummary,
} from "@/lib/types";
import { LogEntry } from "./logs";
import {
  AddressLink,
  BlockLink,
  NativeValue,
  TimeAgo,
  TimeCell,
  TimeHeader,
  TokenIdLink,
  TokenLink,
  TokenValue,
  TxLink,
} from "./links";
import { HelpTip } from "./overlay";
import { TxPreview } from "./previews";
import { Badge, MethodBadge, StatusIcon } from "./ui";

function Table({ head, children, className }: { head: ReactNode; children: ReactNode; className?: string }) {
  return (
    <div className={cx("table-wrap", className)}>
      <table className="data-table">
        <thead>
          <tr>{head}</tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

/** IN / OUT / SELF relative to the address page being viewed. */
function Direction({ from, to, current }: { from: AddressRef | null; to: AddressRef | null; current?: string }) {
  if (!current) return <ArrowRight className="mx-auto h-3.5 w-3.5 text-dim" aria-label="to" />;
  const out = sameAddr(from?.hash, current);
  const inn = sameAddr(to?.hash, current);
  const base = "w-[42px] justify-center";
  if (out && inn)
    return (
      <Badge tone="neutral" className={base}>
        SELF
      </Badge>
    );
  if (out)
    return (
      <Badge tone="warn" className={base}>
        OUT
      </Badge>
    );
  if (inn)
    return (
      <Badge tone="ok" className={base}>
        IN
      </Badge>
    );
  return <ArrowRight className="mx-auto h-3.5 w-3.5 text-dim" aria-label="to" />;
}

function ToCell({
  to,
  created,
  current,
  compact,
}: {
  to: AddressRef | null;
  created: AddressRef | null;
  current?: string;
  compact?: boolean;
}) {
  if (to) return <AddressLink addr={to} current={current} compact={compact} />;
  if (created)
    return (
      <span className="inline-flex min-w-0 items-center gap-1">
        <span className="text-dim">Create:</span>
        <AddressLink addr={created} current={current} compact={compact} icon={false} />
      </span>
    );
  return <span className="text-dim">Contract creation</span>;
}

/** Phone layout: one card per row instead of a wide table. */
function PhoneList({ children }: { children: ReactNode }) {
  return <ul className="divide-y divide-line md:hidden">{children}</ul>;
}

function PhoneRow({ children, className }: { children: ReactNode; className?: string }) {
  return <li className={cx("flex flex-col gap-1.5 px-4 py-3 text-[13px]", className)}>{children}</li>;
}

function Line({ children }: { children: ReactNode }) {
  return <div className="flex min-w-0 items-center justify-between gap-3">{children}</div>;
}

const methodHead = (
  <span className="inline-flex items-center gap-1">
    Method <HelpTip text="The function called by the transaction, decoded from its input data." />
  </span>
);

/* Transactions ------------------------------------------------------------------ */

export function TxTable({ items, current, showBlock = true }: { items: TxSummary[]; current?: string; showBlock?: boolean }) {
  return (
    <>
      <PhoneList>
        {items.map((t) => (
          <PhoneRow key={t.hash}>
            <Line>
              <span className="inline-flex min-w-0 items-center gap-1.5">
                <StatusIcon status={t.status} />
                <TxLink hash={t.hash} />
              </span>
              <MethodBadge method={t.method} />
            </Line>
            <Line>
              <span className="inline-flex min-w-0 items-center gap-1.5">
                <span className="text-dim">From</span>
                <AddressLink addr={t.from} current={current} compact maxW="max-w-[110px]" />
                <span className="text-dim">To</span>
                <ToCell to={t.to} created={t.created_contract} current={current} compact />
              </span>
            </Line>
            <Line>
              <span className="text-muted">
                {showBlock ? (
                  <>
                    Block <BlockLink n={t.block_number} />,{" "}
                  </>
                ) : null}
                <TimeAgo iso={t.timestamp} />
              </span>
              <span className="font-medium text-fg">
                <NativeValue wei={t.value} />
              </span>
            </Line>
          </PhoneRow>
        ))}
      </PhoneList>
      <Table
        className="hidden md:block"
        head={
          <>
            <th className="w-10" aria-label="Preview" />
            <th>Transaction Hash</th>
            <th>{methodHead}</th>
            {showBlock ? <th>Block</th> : null}
            <th>
              <TimeHeader />
            </th>
            <th>From</th>
            <th className="w-14" aria-label="Direction" />
            <th>To</th>
            <th className="num">Amount</th>
            <th className="num">Txn Fee</th>
          </>
        }
      >
        {items.map((t) => (
          <tr key={t.hash}>
            <td className="!pr-0">
              <TxPreview hash={t.hash} />
            </td>
            <td>
              <span className="inline-flex items-center gap-1.5">
                <StatusIcon status={t.status} error={null} />
                <TxLink hash={t.hash} />
              </span>
            </td>
            <td>
              <MethodBadge method={t.method} />
            </td>
            {showBlock ? (
              <td>
                <BlockLink n={t.block_number} />
              </td>
            ) : null}
            <td className="text-fg2">
              <TimeCell iso={t.timestamp} />
            </td>
            <td>
              <AddressLink addr={t.from} current={current} />
            </td>
            <td className="!px-1 text-center">
              <Direction from={t.from} to={t.to ?? t.created_contract} current={current} />
            </td>
            <td>
              <ToCell to={t.to} created={t.created_contract} current={current} />
            </td>
            <td className="num">
              <NativeValue wei={t.value} />
            </td>
            <td className="num text-[12px] text-muted">
              <NativeValue wei={t.fee} maxFrac={8} symbol={false} />
            </td>
          </tr>
        ))}
      </Table>
    </>
  );
}

/* Blocks ---------------------------------------------------------------------- */

export function GasBar({ used, limit }: { used: string; limit: string }) {
  const u = Number(used);
  const l = Number(limit);
  const p = l > 0 ? (u / l) * 100 : 0;
  return (
    <span className="inline-flex flex-col gap-1">
      <span className="tabular-nums">
        {formatInt(used)} <span className="text-muted">({formatPct(p)})</span>
      </span>
      <span className="block h-1 w-24 overflow-hidden rounded-full bg-hover" aria-hidden>
        <span className="block h-full rounded-full bg-gold" style={{ width: `${Math.min(100, Math.max(p > 0 ? 2 : 0, p))}%` }} />
      </span>
    </span>
  );
}

export function BlockTable({ items, fresh }: { items: BlockSummary[]; fresh?: Set<number> }) {
  return (
    <>
      <PhoneList>
        {items.map((b) => (
          <PhoneRow key={b.number} className={cx(fresh?.has(b.number) && "flash-in")}>
            <Line>
              <BlockLink n={b.number} className="font-semibold" />
              <span className="text-muted">
                <TimeAgo iso={b.timestamp} />
              </span>
            </Line>
            <Line>
              <span className="inline-flex min-w-0 items-center gap-1.5 text-muted">
                Validator <AddressLink addr={b.miner} compact maxW="max-w-[150px]" />
              </span>
              <span className="shrink-0 text-fg2">
                {formatInt(b.tx_count)} {b.tx_count === 1 ? "txn" : "txns"}
              </span>
            </Line>
            <Line>
              <span className="text-muted">Gas used</span>
              <span className="tabular-nums text-fg2">
                {formatInt(b.gas_used)} <span className="text-dim">of {formatInt(b.gas_limit)}</span>
              </span>
            </Line>
          </PhoneRow>
        ))}
      </PhoneList>
      <Table
        className="hidden md:block"
        head={
          <>
            <th>Block</th>
            <th>
              <TimeHeader />
            </th>
            <th className="num">Txn</th>
            <th>Validator</th>
            <th>Gas Used</th>
            <th className="num">Gas Limit</th>
            <th className="num">Size</th>
          </>
        }
      >
        {items.map((b) => (
          <tr key={b.number} className={cx(fresh?.has(b.number) && "flash-in")}>
            <td>
              <BlockLink n={b.number} />
            </td>
            <td className="text-fg2">
              <TimeCell iso={b.timestamp} />
            </td>
            <td className="num">{formatInt(b.tx_count)}</td>
            <td>
              <AddressLink addr={b.miner} />
            </td>
            <td>
              <GasBar used={b.gas_used} limit={b.gas_limit} />
            </td>
            <td className="num text-fg2">{formatInt(b.gas_limit)}</td>
            <td className="num text-fg2">{b.size === null ? "" : `${formatInt(b.size)} bytes`}</td>
          </tr>
        ))}
      </Table>
    </>
  );
}

/* Token transfers ------------------------------------------------------------------ */

function TransferAmount({ t }: { t: TokenTransfer }) {
  if (t.token.type === "ERC-721") {
    return t.token_id ? <TokenIdLink token={t.token.address} id={t.token_id} /> : <span>1</span>;
  }
  return (
    <span className="inline-flex items-center justify-end gap-2">
      <TokenValue amount={t.amount} decimals={t.token.decimals} />
      {t.token.type === "ERC-1155" && t.token_id ? <TokenIdLink token={t.token.address} id={t.token_id} /> : null}
    </span>
  );
}

export function TokenTransferTable({
  items,
  current,
  hideToken = false,
}: {
  items: TokenTransfer[];
  current?: string;
  hideToken?: boolean;
}) {
  return (
    <>
      <PhoneList>
        {items.map((t) => (
          <PhoneRow key={`${t.tx_hash}:${t.log_index}:${t.batch_index}`}>
            <Line>
              <TxLink hash={t.tx_hash} />
              <span className="text-muted">
                <TimeAgo iso={t.timestamp} />
              </span>
            </Line>
            <Line>
              <span className="inline-flex min-w-0 items-center gap-1.5">
                <span className="text-dim">From</span>
                <AddressLink addr={t.from} current={current} compact maxW="max-w-[110px]" />
                <span className="text-dim">To</span>
                <AddressLink addr={t.to} current={current} compact maxW="max-w-[110px]" />
              </span>
            </Line>
            <Line>
              {hideToken ? <MethodBadge method={t.method} /> : <TokenLink token={t.token} maxW="max-w-[170px]" />}
              <span className="font-medium text-fg">
                <TransferAmount t={t} />
              </span>
            </Line>
          </PhoneRow>
        ))}
      </PhoneList>
      <Table
        className="hidden md:block"
        head={
          <>
            <th className="w-10" aria-label="Preview" />
            <th>Transaction Hash</th>
            <th>{methodHead}</th>
            <th>Block</th>
            <th>
              <TimeHeader />
            </th>
            <th>From</th>
            <th className="w-14" aria-label="Direction" />
            <th>To</th>
            <th className="num">Amount</th>
            {hideToken ? null : <th>Token</th>}
          </>
        }
      >
        {items.map((t) => (
          <tr key={`${t.tx_hash}:${t.log_index}:${t.batch_index}`}>
            <td className="!pr-0">
              <TxPreview hash={t.tx_hash} />
            </td>
            <td>
              <TxLink hash={t.tx_hash} />
            </td>
            <td>
              <MethodBadge method={t.method} />
            </td>
            <td>
              <BlockLink n={t.block_number} />
            </td>
            <td className="text-fg2">
              <TimeCell iso={t.timestamp} />
            </td>
            <td>
              <AddressLink addr={t.from} current={current} />
            </td>
            <td className="!px-1 text-center">
              <Direction from={t.from} to={t.to} current={current} />
            </td>
            <td>
              <AddressLink addr={t.to} current={current} />
            </td>
            <td className="num">
              <TransferAmount t={t} />
            </td>
            {hideToken ? null : (
              <td>
                <TokenLink token={t.token} />
              </td>
            )}
          </tr>
        ))}
      </Table>
    </>
  );
}

/* Internal transactions -------------------------------------------------------- */

export function InternalTxTable({
  items,
  current,
  showParent = true,
}: {
  items: InternalTx[];
  current?: string;
  showParent?: boolean;
}) {
  return (
    <Table
      head={
        <>
          {showParent ? <th>Parent Transaction Hash</th> : null}
          <th>Block</th>
          <th>
            <TimeHeader />
          </th>
          <th>Type Trace Address</th>
          <th>From</th>
          <th className="w-14" aria-label="Direction" />
          <th>To</th>
          <th className="num">Value</th>
          <th className="num">Gas Used</th>
        </>
      }
    >
      {items.map((t) => (
        <tr key={`${t.tx_hash}:${t.trace_address}`}>
          {showParent ? (
            <td>
              <TxLink hash={t.tx_hash} />
            </td>
          ) : null}
          <td>
            <BlockLink n={t.block_number} />
          </td>
          <td className="text-fg2">
            <TimeCell iso={t.timestamp} />
          </td>
          <td>
            <span className="inline-flex items-center gap-1.5">
              {t.error ? (
                <Badge tone="bad" title={t.error}>
                  failed
                </Badge>
              ) : null}
              <span className="text-[12.5px] text-fg2">{t.call_type.toLowerCase()}</span>
              <span className="font-mono text-[12px] text-dim" title={`Depth ${t.depth}`}>
                _{(t.trace_address || "0").replace(/\./g, "_")}
              </span>
            </span>
          </td>
          <td>
            <AddressLink addr={t.from} current={current} />
          </td>
          <td className="!px-1 text-center">
            <Direction from={t.from} to={t.to ?? t.created_contract} current={current} />
          </td>
          <td>
            <ToCell to={t.to} created={t.created_contract} current={current} />
          </td>
          <td className="num">
            <NativeValue wei={t.value} />
          </td>
          <td className="num text-fg2">{t.gas_used === null ? "" : formatInt(t.gas_used)}</td>
        </tr>
      ))}
    </Table>
  );
}

/* Holders and holdings ------------------------------------------------------------ */

export function HolderTable({
  items,
  decimals,
  symbol,
  offset,
  showId,
  tokenAddress,
}: {
  items: TokenHolder[];
  decimals: number | null;
  symbol: string | null;
  offset: number;
  showId: boolean;
  tokenAddress?: string;
}) {
  return (
    <Table
      head={
        <>
          <th className="w-14">Rank</th>
          <th>Address</th>
          {showId ? <th>Token ID</th> : null}
          <th className="num">Quantity</th>
          <th>Percentage</th>
        </>
      }
    >
      {items.map((h, i) => (
        <tr key={`${h.address.hash}:${h.token_id ?? ""}`}>
          <td className="tabular-nums text-fg2">{offset + i + 1}</td>
          <td>
            <AddressLink addr={h.address} maxW="max-w-[280px]" copy />
          </td>
          {showId ? (
            <td>
              {h.token_id && tokenAddress ? (
                <TokenIdLink token={tokenAddress} id={h.token_id} />
              ) : (
                <span className="font-mono text-[12.5px]">{h.token_id ?? ""}</span>
              )}
            </td>
          ) : null}
          <td className="num">
            <TokenValue amount={h.balance} decimals={decimals} symbol={symbol} maxFrac={4} />
          </td>
          <td>
            {h.share_pct === null ? (
              <span className="text-dim">n/a</span>
            ) : (
              <span className="inline-flex items-center gap-2">
                <span className="tabular-nums text-fg2">{formatPct(h.share_pct, 4)}</span>
                <span className="block h-1.5 w-20 overflow-hidden rounded-full bg-hover" aria-hidden>
                  <span className="block h-full rounded-full bg-gold" style={{ width: `${Math.min(100, Math.max(1, h.share_pct))}%` }} />
                </span>
              </span>
            )}
          </td>
        </tr>
      ))}
    </Table>
  );
}

export function HoldingTable({ items }: { items: TokenHolding[] }) {
  return (
    <Table
      head={
        <>
          <th>Token</th>
          <th>Type</th>
          <th>Token ID</th>
          <th className="num">Quantity</th>
          <th>Contract</th>
        </>
      }
    >
      {items.map((h) => (
        <tr key={`${h.token.address}:${h.token_id ?? ""}`}>
          <td>
            <TokenLink token={h.token} maxW="max-w-[280px]" />
          </td>
          <td>
            <span className="inline-flex gap-1">
              <Badge>{h.token.type}</Badge>
              {h.token.is_erc3643 ? <Badge tone="gold">ERC-3643</Badge> : null}
            </span>
          </td>
          <td>{h.token_id ? <TokenIdLink token={h.token.address} id={h.token_id} /> : <span className="text-dim">n/a</span>}</td>
          <td className="num">
            <TokenValue amount={h.balance} decimals={h.token.decimals} symbol={h.token.symbol} maxFrac={4} />
          </td>
          <td>
            <AddressLink addr={{ hash: h.token.address, name: null, is_contract: true, is_verified: false, token: null }} icon={false} />
          </td>
        </tr>
      ))}
    </Table>
  );
}

export function BalanceTable({ items }: { items: BalancePoint[] }) {
  return (
    <Table
      head={
        <>
          <th>Block</th>
          <th>
            <TimeHeader />
          </th>
          <th className="num">Balance</th>
          <th className="num">Change</th>
        </>
      }
    >
      {items.map((p) => {
        const neg = p.delta.startsWith("-");
        const zero = /^-?0+$/.test(p.delta);
        return (
          <tr key={p.block_number}>
            <td>
              <BlockLink n={p.block_number} />
            </td>
            <td className="text-fg2">
              <TimeCell iso={p.timestamp} />
            </td>
            <td className="num">
              <NativeValue wei={p.balance} />
            </td>
            <td className={cx("num", zero ? "text-dim" : neg ? "text-bad" : "text-ok")}>
              {zero || neg ? "" : "+"}
              <NativeValue wei={p.delta} symbol={false} maxFrac={8} />
            </td>
          </tr>
        );
      })}
    </Table>
  );
}

/* Logs ---------------------------------------------------------------------------- */

export function LogList({ items, showTx = false, current }: { items: Log[]; showTx?: boolean; current?: string }) {
  return (
    <ul className="divide-y divide-line">
      {items.map((l) => (
        <li key={`${l.tx_hash}:${l.log_index}`} className="px-4 py-5 sm:px-5">
          <LogEntry log={l} showTx={showTx} current={current} />
        </li>
      ))}
    </ul>
  );
}

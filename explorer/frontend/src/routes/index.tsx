/**
 * Home, in Etherscan's layout: a search hero, one stats card, then Latest
 * Blocks and Latest Transactions side by side with a "View all" button at the
 * bottom of each. Kept live by the block stream (/api/v2/stream), with a
 * polling fallback.
 */
import type { ReactNode } from "react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { ArrowRight } from "lucide-react";

import { AddNetworkButton } from "@/components/AddNetworkButton";
import { AddressLink, BlockLink, NativeValue, TimeAgo, TxLink } from "@/components/links";
import { SearchBar } from "@/components/SearchBar";
import { TxHistoryChart, useTxHistory } from "@/components/TxHistoryChart";
import { Card, ErrorState, EmptyState, Skeleton, StatusIcon } from "@/components/ui";
import { useApi } from "@/lib/api";
import { useChain } from "@/lib/chain";
import { cx } from "@/lib/cx";
import { formatInt } from "@/lib/format";
import { useFresh } from "@/lib/fresh";
import { pollInterval, useLiveBlocks, type LiveStatus } from "@/lib/live";
import type { BlockSummary, Page, Stats, TxSummary } from "@/lib/types";

export const Route = createFileRoute("/")({
  component: Home,
});

const N = 6;
const BLOCKS_KEY = ["home", "blocks"] as const;
const TXS_KEY = ["home", "txs"] as const;
const STATS_KEY = ["stats"] as const;

function Home() {
  const qc = useQueryClient();
  const status = useLiveBlocks((b) => {
    qc.setQueryData<Page<BlockSummary>>(BLOCKS_KEY, (old) =>
      old
        ? {
            ...old,
            items: [b, ...old.items.filter((x) => x.number !== b.number)].slice(0, N),
            total: Math.max(old.total, b.number + 1),
          }
        : old,
    );
    void qc.invalidateQueries({ queryKey: STATS_KEY });
    if (b.tx_count > 0) void qc.invalidateQueries({ queryKey: TXS_KEY });
  });
  const poll = pollInterval(status);

  const stats = useApi<Stats>(STATS_KEY, "/stats", undefined, { refetchInterval: poll });
  const blocks = useApi<Page<BlockSummary>>(BLOCKS_KEY, "/blocks", { limit: N }, { refetchInterval: poll });
  const txs = useApi<Page<TxSummary>>(TXS_KEY, "/transactions", { limit: N }, { refetchInterval: poll });

  return (
    <>
      <Hero />
      <div className="relative -mt-14 flex flex-col gap-6 sm:-mt-16">
        <StatsCard q={stats} />
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
          <LatestBlocks q={blocks} status={status} />
          <LatestTxs q={txs} />
        </div>
      </div>
    </>
  );
}

/* Hero --------------------------------------------------------------------------- */

function Hero() {
  const cfg = useChain();
  return (
    <section className="relative left-1/2 -mt-5 w-screen -translate-x-1/2 overflow-hidden bg-hero sm:-mt-6">
      <svg aria-hidden className="pointer-events-none absolute inset-0 h-full w-full opacity-[0.06]">
        <defs>
          <pattern id="hero-grid" width="44" height="44" patternUnits="userSpaceOnUse">
            <path d="M44 0H0V44" fill="none" stroke="#8c8c8c" strokeWidth="1" />
          </pattern>
        </defs>
        <rect width="100%" height="100%" fill="url(#hero-grid)" />
      </svg>
      <div className="relative mx-auto max-w-[1400px] px-4 pb-24 pt-9 sm:px-6 sm:pb-28 sm:pt-12">
        <h1 className="text-xl font-semibold text-[var(--hero-text)] sm:text-2xl">The {cfg.network_name} Blockchain Explorer</h1>
        <div className="mt-4 max-w-[780px]">
          <SearchBar size="lg" />
        </div>
        <p className="mt-4 flex flex-wrap items-center gap-x-3 gap-y-2 text-[13px] text-[var(--hero-muted)]">
          <span>
            Chain ID <span className="font-medium text-[var(--hero-text)]">{cfg.chain_id}</span>, currency{" "}
            <span className="font-medium text-[var(--hero-text)]">{cfg.native_symbol}</span>
          </span>
          <AddNetworkButton variant="compact" className="!border-white/15 !bg-white/10 !text-white hover:!bg-white/20" />
        </p>
      </div>
    </section>
  );
}

/* Stats --------------------------------------------------------------------------- */

function StatCell({ label, value, sub, to }: { label: string; value: ReactNode; sub?: ReactNode; to?: "/blocks" | "/txs" | "/verified-contracts" | "/tokens" }) {
  const v = to ? (
    <Link to={to} className="link text-[15px] font-medium text-fg hover:text-link">
      {value}
    </Link>
  ) : (
    <span className="text-[15px] font-medium text-fg">{value}</span>
  );
  return (
    <div className="min-w-0">
      <p className="eyebrow">{label}</p>
      <p className="mt-1 truncate">
        {v}
        {sub ? <span className="ml-1.5 text-[13px] text-muted">{sub}</span> : null}
      </p>
    </div>
  );
}

function StatsCard({ q }: { q: UseQueryResult<Stats, Error> }) {
  const history = useTxHistory();
  const s = q.data;
  if (q.isError && !s) {
    return (
      <Card>
        <ErrorState error={q.error} onRetry={() => q.refetch()} />
      </Card>
    );
  }
  const lag = s && s.node_block !== null && s.latest_block !== null ? Math.max(0, s.node_block - s.latest_block) : null;
  const col = "flex flex-col gap-5 px-5 py-5";
  return (
    <Card className="grid grid-cols-1 divide-y divide-line md:grid-cols-3 md:divide-x md:divide-y-0">
      {!s ? (
        Array.from({ length: 3 }).map((_, i) => (
          <div key={i} className={col}>
            <Skeleton className="h-3 w-24" />
            <Skeleton className="h-4 w-36" />
            <Skeleton className="h-3 w-24" />
            <Skeleton className="h-4 w-28" />
          </div>
        ))
      ) : (
        <>
          <div className={col}>
            <StatCell
              label="Latest block"
              value={s.latest_block === null ? "None yet" : s.latest_block}
              sub={s.latest_block_time ? <TimeAgo iso={s.latest_block_time} /> : undefined}
              to="/blocks"
            />
            <StatCell label="Transactions" value={formatInt(s.total_transactions)} sub={`in ${formatInt(s.total_blocks)} blocks`} to="/txs" />
          </div>
          <div className={col}>
            <StatCell label="Addresses" value={formatInt(s.total_addresses)} />
            <StatCell
              label="Contracts"
              value={formatInt(s.total_contracts)}
              sub={`${formatInt(s.verified_contracts)} verified`}
              to="/verified-contracts"
            />
          </div>
          <div className={col}>
            {history.length >= 2 ? (
              <div className="min-w-0">
                <p className="eyebrow mb-1">Transaction history in 14 days</p>
                <TxHistoryChart points={history} />
              </div>
            ) : (
              <>
                <StatCell label="Tokens" value={formatInt(s.total_tokens)} to="/tokens" />
                <StatCell
                  label="Indexer"
                  value={
                    lag === null ? (
                      <span className="text-dim">Unknown</span>
                    ) : lag === 0 ? (
                      <span className="text-ok">In sync</span>
                    ) : (
                      <span className="text-warn">{formatInt(lag)} blocks behind</span>
                    )
                  }
                  sub={s.node_block !== null ? `node at ${s.node_block}` : undefined}
                />
              </>
            )}
          </div>
        </>
      )}
    </Card>
  );
}

/* Latest lists -------------------------------------------------------------------- */

function LivePill({ status }: { status: LiveStatus }) {
  const label = status === "live" ? "Live" : status === "connecting" ? "Connecting" : "Auto-refresh";
  return (
    <span
      title={
        status === "live"
          ? "New blocks appear as they are indexed."
          : "The live feed is not available right now, so this list refreshes every few seconds."
      }
      className="inline-flex items-center gap-1.5 rounded-full border border-line bg-raised px-2.5 py-1 text-[11.5px] font-semibold text-fg2"
    >
      <span className={cx("h-1.5 w-1.5 rounded-full", status === "live" ? "live-dot bg-ok" : status === "connecting" ? "bg-warn" : "bg-dim")} />
      {label}
    </span>
  );
}

function ListSkeleton() {
  return (
    <ul className="divide-y divide-line" aria-busy="true" aria-label="Loading">
      {Array.from({ length: N }).map((_, i) => (
        <li key={i} className="grid grid-cols-[1fr_1.6fr_auto] items-center gap-4 px-5 py-4">
          <span className="flex flex-col gap-2">
            <Skeleton className="h-3.5 w-20" />
            <Skeleton className="h-3 w-16" />
          </span>
          <span className="flex flex-col gap-2">
            <Skeleton className="h-3.5 w-44" />
            <Skeleton className="h-3 w-24" />
          </span>
          <Skeleton className="h-5 w-16" />
        </li>
      ))}
    </ul>
  );
}

function ViewAll({ to, label }: { to: "/blocks" | "/txs"; label: string }) {
  return (
    <div className="border-t border-line p-3">
      <Link
        to={to}
        className="flex items-center justify-center gap-1.5 rounded-full bg-raised py-2.5 text-[12px] font-semibold uppercase tracking-[0.06em] text-fg2 transition-colors hover:bg-hover hover:text-fg"
      >
        {label} <ArrowRight className="h-3.5 w-3.5" />
      </Link>
    </div>
  );
}

function LatestBlocks({ q, status }: { q: UseQueryResult<Page<BlockSummary>, Error>; status: LiveStatus }) {
  const fresh = useFresh(q.data?.items, (b) => b.number);
  return (
    <Card className="flex flex-col">
      <div className="flex items-center justify-between border-b border-line px-5 py-3.5">
        <h2 className="text-[15px] font-semibold text-fg">Latest Blocks</h2>
        <LivePill status={status} />
      </div>
      {q.isError && !q.data ? (
        <ErrorState error={q.error} onRetry={() => q.refetch()} />
      ) : !q.data ? (
        <ListSkeleton />
      ) : q.data.items.length === 0 ? (
        <EmptyState title="No blocks indexed yet" />
      ) : (
        <ul className="flex-1 divide-y divide-line">
          {q.data.items.map((b) => (
            <li
              key={b.number}
              className={cx(
                "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-5 py-3.5 sm:grid-cols-[110px_minmax(0,1fr)_auto]",
                fresh.has(b.number) && "flash-in",
              )}
            >
              <div className="flex flex-col">
                <BlockLink n={b.number} className="text-[14px]" />
                <span className="text-[12px] text-muted">
                  <TimeAgo iso={b.timestamp} />
                </span>
              </div>
              <div className="hidden min-w-0 flex-col text-[13px] sm:flex">
                <span className="flex min-w-0 items-center gap-1.5">
                  <span className="shrink-0 text-fg2">Validator</span>
                  <AddressLink addr={b.miner} icon={false} compact maxW="max-w-[160px]" />
                </span>
                <span className="text-muted">
                  <Link to="/block/$id" params={{ id: String(b.number) }} search={{ tab: "txs" }} className="link">
                    {formatInt(b.tx_count)} {b.tx_count === 1 ? "txn" : "txns"}
                  </Link>
                </span>
              </div>
              <span className="rounded-full border border-line px-2.5 py-0.5 text-[11.5px] font-medium text-fg2">
                {formatInt(b.gas_used)} gas
              </span>
            </li>
          ))}
        </ul>
      )}
      <ViewAll to="/blocks" label="View all blocks" />
    </Card>
  );
}

function LatestTxs({ q }: { q: UseQueryResult<Page<TxSummary>, Error> }) {
  const fresh = useFresh(q.data?.items, (t) => t.hash);
  return (
    <Card className="flex flex-col">
      <div className="flex items-center justify-between border-b border-line px-5 py-3.5">
        <h2 className="text-[15px] font-semibold text-fg">Latest Transactions</h2>
      </div>
      {q.isError && !q.data ? (
        <ErrorState error={q.error} onRetry={() => q.refetch()} />
      ) : !q.data ? (
        <ListSkeleton />
      ) : q.data.items.length === 0 ? (
        <EmptyState title="No transactions yet" />
      ) : (
        <ul className="flex-1 divide-y divide-line">
          {q.data.items.map((t) => (
            <li
              key={t.hash}
              className={cx(
                "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-5 py-3.5 sm:grid-cols-[150px_minmax(0,1fr)_auto]",
                fresh.has(t.hash) && "flash-in",
              )}
            >
              <div className="flex min-w-0 flex-col">
                <span className="inline-flex items-center gap-1">
                  <StatusIcon status={t.status} />
                  <TxLink hash={t.hash} className="truncate" />
                </span>
                <span className="text-[12px] text-muted">
                  <TimeAgo iso={t.timestamp} />
                </span>
              </div>
              <div className="hidden min-w-0 flex-col text-[13px] sm:flex">
                <span className="flex min-w-0 items-center gap-1.5">
                  <span className="shrink-0 text-fg2">From</span>
                  <AddressLink addr={t.from} icon={false} compact maxW="max-w-[150px]" />
                </span>
                <span className="flex min-w-0 items-center gap-1.5">
                  <span className="shrink-0 text-fg2">To</span>
                  {t.to ? (
                    <AddressLink addr={t.to} compact maxW="max-w-[150px]" />
                  ) : t.created_contract ? (
                    <AddressLink addr={t.created_contract} compact maxW="max-w-[150px]" />
                  ) : (
                    <span className="text-dim">Contract creation</span>
                  )}
                </span>
              </div>
              <span className="rounded-full border border-line px-2.5 py-0.5 text-[11.5px] font-medium text-fg2">
                <NativeValue wei={t.value} maxFrac={4} />
              </span>
            </li>
          ))}
        </ul>
      )}
      <ViewAll to="/txs" label="View all transactions" />
    </Card>
  );
}

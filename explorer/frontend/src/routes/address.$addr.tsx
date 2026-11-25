/**
 * /address/<addr>, in Etherscan's layout: the address with copy and QR, an
 * Overview card (balance, token holdings dropdown) and a More Info card, then
 * pill tabs. Tab ids keep Blockscout's names
 * (?tab=txs|internal_txns|token_transfers|contract|logs|tokens|coin_balance_history);
 * Blockscout's contract sub-tabs (contract_code, read_contract, read_proxy,
 * write_contract, write_proxy, contract_abi) open the contract tab on that view.
 */
import { lazy, Suspense, type ReactNode } from "react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { CheckCircle2, ChevronDown, Filter, X } from "lucide-react";

import { BalanceChart } from "@/components/BalanceChart";
import { AddressInvalid, proxyLabel } from "@/components/common";
import { AddressLink, BlockLink, NativeValue, TokenAvatar, TokenLink, TokenValue, TxLink } from "@/components/links";
import { Popover } from "@/components/overlay";
import { AddressQR } from "@/components/previews";
import { BalanceTable, HoldingTable, InternalTxTable, LogList, TokenTransferTable, TxTable } from "@/components/tables";
import {
  Badge,
  Card,
  CardHeader,
  CopyButton,
  DetailSkeleton,
  EmptyState,
  ErrorState,
  ListSummary,
  Pagination,
  Skeleton,
  Stat,
  TableSkeleton,
  Tabs,
} from "@/components/ui";
import { PAGE_SIZE, useApi, usePage } from "@/lib/api";
import { useChain } from "@/lib/chain";
import { formatInt, isAddress, shortHash } from "@/lib/format";
import { pageParam, strParam } from "@/lib/search";
import { pageHead } from "@/lib/seo";
import type { AddressDetail, BalancePoint, InternalTx, Log, Page, TokenHolding, TokenTransfer, TxSummary } from "@/lib/types";
import { NETWORK_NAME } from "@/lib/site";

const ContractTab = lazy(() => import("@/components/contract/ContractTab"));

interface AddressSearch {
  tab?: string;
  page?: number;
  token?: string;
}

export const Route = createFileRoute("/address/$addr")({
  validateSearch: (s: Record<string, unknown>): AddressSearch => ({
    tab: strParam(s.tab),
    page: pageParam(s.page),
    token: strParam(s.token),
  }),
  head: ({ params }) => pageHead(`Address ${shortHash(params.addr, 8, 6)}`, `${NETWORK_NAME} address ${params.addr}.`),
  component: AddressPage,
});

type TabId = "txs" | "token_transfers" | "internal_txns" | "tokens" | "logs" | "contract" | "coin_balance_history";

const CONTRACT_VIEWS: Record<string, string> = {
  contract: "code",
  contract_code: "code",
  contract_source_code: "code",
  contract_abi: "code",
  read_contract: "read",
  write_contract: "write",
  read_proxy: "read_proxy",
  write_proxy: "write_proxy",
};

function normTab(t: string | undefined): TabId {
  if (t && t in CONTRACT_VIEWS) return "contract";
  switch (t) {
    case "token_transfers":
    case "internal_txns":
    case "tokens":
    case "logs":
    case "coin_balance_history":
      return t;
    case "events":
      return "logs";
    case "internal":
      return "internal_txns";
    default:
      return "txs";
  }
}

function AddressPage() {
  const { addr } = Route.useParams();
  const search = Route.useSearch();
  const navigate = Route.useNavigate();
  const valid = isAddress(addr);
  const q = useApi<AddressDetail>(["address", addr.toLowerCase()], valid ? `/addresses/${addr}` : null);
  const a = q.data;
  const tab = normTab(search.tab);
  const page = search.page ?? 1;

  const setTab = (t: string) => navigate({ search: { tab: t === "txs" ? undefined : t }, resetScroll: false });
  const setPage = (p: number) => navigate({ search: (s) => ({ ...s, page: p > 1 ? p : undefined }) });

  if (!valid) return <AddressInvalid value={addr} />;

  const isContract = a?.is_contract ?? false;
  const shown: TabId = tab === "contract" && a && !isContract ? "txs" : tab;
  const hash = a?.hash ?? addr;

  return (
    <>
      <div className="mb-5 border-b border-line pb-4">
        <h1 className="flex flex-wrap items-center gap-x-2.5 gap-y-1 text-lg font-semibold text-bright sm:text-xl">
          {a?.token ? <TokenAvatar symbol={a.token.symbol || a.token.name || "?"} size="lg" /> : null}
          {a ? (isContract ? "Contract" : "Address") : <Skeleton className="h-6 w-24" />}
          <span className="inline-flex min-w-0 items-center gap-1">
            <span className="min-w-0 break-all font-mono text-[14px] font-normal text-fg sm:text-[15px]">{hash}</span>
            <CopyButton value={hash} label="Copy address" />
            <AddressQR address={hash} />
          </span>
        </h1>
        {a ? (
          <div className="mt-3 flex flex-wrap gap-1.5">
            {a.name ? <Badge>{a.name}</Badge> : null}
            {a.token ? (
              <Badge tone="gold">
                <Link to="/token/$addr" params={{ addr: a.hash }}>
                  Token: {a.token.symbol || a.token.name || "token"}
                </Link>
              </Badge>
            ) : null}
            {a.token?.is_erc3643 ? <Badge tone="gold">ERC-3643</Badge> : null}
            {isContract ? (
              a.is_verified ? (
                <Badge tone="ok">
                  <CheckCircle2 className="h-3 w-3" /> Source code verified
                </Badge>
              ) : (
                <Badge tone="warn">Not verified</Badge>
              )
            ) : null}
            {a.proxy_type ? <Badge>{proxyLabel(a.proxy_type)}</Badge> : null}
          </div>
        ) : null}
      </div>

      {q.isError ? (
        <Card>
          <ErrorState error={q.error} onRetry={() => q.refetch()} />
        </Card>
      ) : (
        <>
          <div className="mb-6 grid grid-cols-1 gap-4 lg:grid-cols-2">
            <Overview a={a} onTab={setTab} />
            <MoreInfo a={a} />
          </div>

          <Tabs
            items={[
              { id: "txs", label: "Transactions", count: a?.tx_count ?? null },
              { id: "internal_txns", label: "Internal Transactions", count: a?.internal_txs_count ?? null },
              { id: "token_transfers", label: "Token Transfers", count: a?.token_transfers_count ?? null },
              {
                id: "contract",
                label: (
                  <span className="inline-flex items-center gap-1">
                    Contract {a?.is_verified ? <CheckCircle2 className="h-3.5 w-3.5 text-ok" aria-label="verified" /> : null}
                  </span>
                ),
                hidden: !isContract,
              },
              { id: "logs", label: "Events", hidden: !isContract },
              { id: "tokens", label: "Token Holdings", count: a?.token_holdings_count ?? null },
              { id: "coin_balance_history", label: "Balance History" },
            ]}
            active={shown}
            onChange={setTab}
          />

          <div key={shown} className="fade-in">
            {shown === "txs" ? (
              <AddrList<TxSummary>
                addr={addr}
                path="transactions"
                page={page}
                onPage={setPage}
                cols={9}
                noun="transactions"
                empty="No transactions sent from or to this address yet."
                render={(items) => <TxTable items={items} current={addr} />}
              />
            ) : shown === "token_transfers" ? (
              <TokenTransfersTab addr={addr} page={page} onPage={setPage} token={search.token} />
            ) : shown === "internal_txns" ? (
              <AddrList<InternalTx>
                addr={addr}
                path="internal-transactions"
                page={page}
                onPage={setPage}
                cols={8}
                noun="internal transactions"
                empty="No internal transactions (calls between contracts) touch this address."
                render={(items) => <InternalTxTable items={items} current={addr} />}
              />
            ) : shown === "tokens" ? (
              <AddrList<TokenHolding>
                addr={addr}
                path="tokens"
                page={page}
                onPage={setPage}
                cols={5}
                noun="token holdings"
                empty="This address holds no tokens."
                render={(items) => <HoldingTable items={items} />}
              />
            ) : shown === "logs" ? (
              <AddrList<Log>
                addr={addr}
                path="logs"
                page={page}
                onPage={setPage}
                cols={4}
                noun="events"
                empty="This contract has not emitted any events."
                render={(items) => <LogList items={items} showTx current={addr} />}
              />
            ) : shown === "contract" ? (
              <Suspense
                fallback={
                  <Card>
                    <DetailSkeleton rows={6} />
                  </Card>
                }
              >
                <ContractTab addr={addr} view={CONTRACT_VIEWS[search.tab ?? "contract"] ?? "code"} />
              </Suspense>
            ) : (
              <BalanceHistory addr={addr} page={page} onPage={setPage} />
            )}
          </div>
        </>
      )}
    </>
  );
}

/* Overview cards ---------------------------------------------------------------------- */

function HoldingsDropdown({ addr, count, onAll }: { addr: string; count: number; onAll: () => void }) {
  return (
    <Popover
      label="Token holdings"
      place="bottom-start"
      triggerClassName="flex w-full max-w-[360px] items-center justify-between gap-2 rounded-xl border border-line bg-card px-3 py-2 text-left text-[13.5px] text-fg transition-colors hover:border-line-strong"
      trigger={
        <>
          <span>
            {formatInt(count)} {count === 1 ? "token" : "tokens"}
          </span>
          <ChevronDown className="h-4 w-4 text-dim" />
        </>
      }
      panelClassName="w-[340px]"
    >
      {(close) => <HoldingsList addr={addr} onAll={() => (close(), onAll())} />}
    </Popover>
  );
}

function HoldingsList({ addr, onAll }: { addr: string; onAll: () => void }) {
  const q = useApi<Page<TokenHolding>>(["addr-holdings-peek", addr.toLowerCase()], `/addresses/${addr}/tokens`, { limit: 10, offset: 0 });
  if (!q.data) {
    return (
      <div className="flex flex-col gap-2 p-4">
        <Skeleton className="h-3.5 w-40" />
        <Skeleton className="h-3.5 w-48" />
        <Skeleton className="h-3.5 w-36" />
      </div>
    );
  }
  return (
    <div className="py-2">
      <p className="eyebrow px-4 py-1.5">Tokens ({formatInt(q.data.total)})</p>
      <ul className="max-h-[320px] overflow-y-auto">
        {q.data.items.map((h) => (
          <li key={`${h.token.address}:${h.token_id ?? ""}`} className="flex items-center justify-between gap-3 px-4 py-2 hover:bg-hover">
            <TokenLink token={h.token} maxW="max-w-[170px]" />
            <span className="text-right text-[12.5px] text-fg2">
              <TokenValue amount={h.balance} decimals={h.token.decimals} symbol={h.token.symbol} maxFrac={4} />
            </span>
          </li>
        ))}
      </ul>
      <div className="border-t border-line px-4 pt-2">
        <button type="button" onClick={onAll} className="link text-[13px] font-medium">
          View all holdings
        </button>
      </div>
    </div>
  );
}

function Overview({ a, onTab }: { a: AddressDetail | undefined; onTab: (t: string) => void }) {
  const { native_symbol } = useChain();
  return (
    <Card>
      <CardHeader title="Overview" />
      {!a ? (
        <DetailSkeleton rows={3} />
      ) : (
        <div className="px-4 pb-3 pt-1 sm:px-5">
          <Stat label={`${native_symbol} balance`}>
            <NativeValue wei={a.balance} full />
            {a.balance_block !== null ? (
              <span className="ml-2 text-xs text-muted">
                as of block <BlockLink n={a.balance_block} />
              </span>
            ) : null}
          </Stat>
          <Stat label="Token holdings">
            {a.token_holdings_count > 0 ? (
              <HoldingsDropdown addr={a.hash} count={a.token_holdings_count} onAll={() => onTab("tokens")} />
            ) : (
              <span className="text-muted">None</span>
            )}
          </Stat>
          {!a.is_contract ? (
            <Stat label="Nonce">
              {a.nonce === null ? <span className="text-muted">No transaction sent</span> : formatInt(a.nonce)}
            </Stat>
          ) : null}
        </div>
      )}
    </Card>
  );
}

function MoreInfo({ a }: { a: AddressDetail | undefined }) {
  const rows: ReactNode[] = [];
  if (a) {
    if (a.is_contract) {
      rows.push(
        <Stat key="creator" label="Contract creator">
          {a.creator ? (
            <span className="flex flex-wrap items-center gap-x-1.5 gap-y-1">
              <AddressLink addr={a.creator} maxW="max-w-[200px]" />
              {a.creation_tx ? (
                <>
                  <span className="text-muted">at txn</span>
                  <TxLink hash={a.creation_tx} />
                </>
              ) : null}
            </span>
          ) : (
            <span className="text-muted">Unknown (created in the genesis block or before indexing)</span>
          )}
        </Stat>,
      );
    }
    if (a.token) {
      rows.push(
        <Stat key="token" label="Token tracker">
          <span className="inline-flex flex-wrap items-center gap-2">
            <TokenLink token={a.token} maxW="max-w-[280px]" />
            <Badge>{a.token.type}</Badge>
          </span>
        </Stat>,
      );
    }
    if (a.implementation) {
      rows.push(
        <Stat key="impl" label="Implementation">
          <span className="flex flex-wrap items-center gap-2">
            <AddressLink addr={a.implementation} maxW="max-w-[240px]" />
            {a.proxy_type ? <Badge>{proxyLabel(a.proxy_type)}</Badge> : null}
          </span>
        </Stat>,
      );
    }
    rows.push(
      <Stat key="seen" label={a.is_contract ? "Activity" : "Transactions"}>
        {a.first_block === null ? (
          <span className="text-muted">No activity indexed</span>
        ) : (
          <span className="flex flex-wrap items-center gap-x-4 gap-y-1">
            <span>
              <span className="text-muted">Latest: </span>block <BlockLink n={a.last_block ?? a.first_block} />
            </span>
            <span>
              <span className="text-muted">First: </span>block <BlockLink n={a.first_block} />
            </span>
          </span>
        )}
      </Stat>,
    );
  }
  return (
    <Card>
      <CardHeader title="More Info" />
      {!a ? <DetailSkeleton rows={3} /> : <div className="px-4 pb-3 pt-1 sm:px-5">{rows}</div>}
    </Card>
  );
}

/* Tab lists ----------------------------------------------------------------------------- */

function AddrList<T>({
  addr,
  path,
  page,
  onPage,
  empty,
  render,
  cols,
  noun,
  params,
  filter,
}: {
  addr: string;
  path: string;
  page: number;
  onPage: (p: number) => void;
  empty: string;
  render: (items: T[]) => ReactNode;
  cols: number;
  noun: string;
  params?: Record<string, string | undefined>;
  filter?: ReactNode;
}) {
  const q = usePage<T>(["addr-list", addr.toLowerCase(), path], `/addresses/${addr}/${path}`, page, params);
  return (
    <Card>
      {q.isError && !q.data ? (
        <ErrorState error={q.error} onRetry={() => q.refetch()} />
      ) : !q.data ? (
        <TableSkeleton rows={6} cols={cols} />
      ) : q.data.items.length === 0 ? (
        <>
          {filter ? <ListSummary right={filter}>No matching {noun}</ListSummary> : null}
          <EmptyState title={page > 1 ? "Nothing on this page" : "Nothing here yet"}>{empty}</EmptyState>
        </>
      ) : (
        <>
          <ListSummary right={filter}>
            {q.data.total > q.data.items.length
              ? `Latest ${formatInt(Math.min(PAGE_SIZE, q.data.items.length))} from a total of ${formatInt(q.data.total)} ${noun}`
              : `A total of ${formatInt(q.data.total)} ${noun} found`}
          </ListSummary>
          {render(q.data.items)}
        </>
      )}
      {q.data ? (
        <Pagination
          page={page}
          limit={PAGE_SIZE}
          total={q.data.total}
          hasNext={q.data.next_offset !== null}
          loading={q.isFetching && q.isPlaceholderData}
          onPage={onPage}
        />
      ) : null}
    </Card>
  );
}

function TokenTransfersTab({ addr, page, onPage, token }: { addr: string; page: number; onPage: (p: number) => void; token?: string }) {
  const filter = token && isAddress(token) ? token : undefined;
  return (
    <AddrList<TokenTransfer>
      addr={addr}
      path="token-transfers"
      page={page}
      onPage={onPage}
      cols={9}
      noun="token transfers"
      params={{ token: filter }}
      filter={
        filter ? (
          <span className="inline-flex items-center gap-2 text-[12.5px]">
            <Filter className="h-3.5 w-3.5 text-dim" />
            <span className="text-muted">Token</span>
            <span className="font-mono text-fg">{shortHash(filter, 10, 8)}</span>
            <Link from={Route.fullPath} search={(s) => ({ ...s, token: undefined, page: undefined })} className="btn btn-sm" aria-label="Clear the token filter">
              <X className="h-3 w-3" /> Clear
            </Link>
          </span>
        ) : undefined
      }
      empty={filter ? "No transfers of this token to or from this address." : "No token transfers to or from this address."}
      render={(items) => <TokenTransferTable items={items} current={addr} />}
    />
  );
}

function BalanceHistory({ addr, page, onPage }: { addr: string; page: number; onPage: (p: number) => void }) {
  // The chart reads the newest 100 points; the table pages through all of them.
  const chart = useApi<{ items: BalancePoint[] }>(["addr-balance-chart", addr.toLowerCase()], `/addresses/${addr}/coin-balance-history`, {
    limit: 100,
    offset: 0,
  });
  return (
    <div className="flex flex-col gap-4">
      {chart.data && chart.data.items.length > 1 ? (
        <Card>
          <CardHeader title="Balance over time" />
          <div className="px-2 pb-3 pt-4 sm:px-4">
            <BalanceChart points={chart.data.items} />
          </div>
        </Card>
      ) : null}
      <AddrList<BalancePoint>
        addr={addr}
        path="coin-balance-history"
        page={page}
        onPage={onPage}
        cols={4}
        noun="balance changes"
        empty="The balance of this address has never changed in an indexed block."
        render={(items) => <BalanceTable items={items} />}
      />
    </div>
  );
}

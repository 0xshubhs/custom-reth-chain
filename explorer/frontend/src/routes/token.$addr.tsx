/** /token/<addr>, tabs: token_transfers (default), holders, contract (Blockscout's names). */
import { lazy, Suspense, type ReactNode } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { CheckCircle2 } from "lucide-react";

import { AddressInvalid } from "@/components/common";
import { AddressHashLink, BlockLink, TokenAvatar, TokenValue } from "@/components/links";
import { HolderTable, TokenTransferTable } from "@/components/tables";
import {
  Badge,
  Card,
  CardHeader,
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
import { formatInt, isAddress, shortHash } from "@/lib/format";
import { tabPageSearch } from "@/lib/search";
import { pageHead } from "@/lib/seo";
import type { SmartContract, Token, TokenHolder, TokenTransfer } from "@/lib/types";
import { NETWORK_NAME } from "@/lib/site";

const ContractTab = lazy(() => import("@/components/contract/ContractTab"));

export const Route = createFileRoute("/token/$addr")({
  validateSearch: tabPageSearch,
  head: ({ params }) => pageHead(`Token ${shortHash(params.addr, 8, 6)}`, `${NETWORK_NAME} token ${params.addr}.`),
  component: TokenPage,
});

type TabId = "token_transfers" | "holders" | "contract";

const CONTRACT_VIEWS: Record<string, string> = {
  contract: "code",
  contract_code: "code",
  read_contract: "read",
  write_contract: "write",
  read_proxy: "read_proxy",
  write_proxy: "write_proxy",
};

function normTab(t: string | undefined): TabId {
  if (t === "holders" || t === "inventory") return "holders";
  if (t && t in CONTRACT_VIEWS) return "contract";
  return "token_transfers";
}

function TokenPage() {
  const { addr } = Route.useParams();
  const { tab, page = 1 } = Route.useSearch();
  const navigate = Route.useNavigate();
  const valid = isAddress(addr);
  const q = useApi<Token>(["token", addr.toLowerCase()], valid ? `/tokens/${addr}` : null);
  const contract = useApi<SmartContract>(["contract", addr.toLowerCase()], valid ? `/smart-contracts/${addr}` : null, undefined, {
    retry: false,
  });
  const t = q.data;
  const active = normTab(tab);
  const setPage = (p: number) => navigate({ search: (s) => ({ ...s, page: p > 1 ? p : undefined }) });

  if (!valid) return <AddressInvalid value={addr} what="token address" />;

  return (
    <>
      <div className="mb-5 border-b border-line pb-4">
        <h1 className="flex flex-wrap items-center gap-x-2.5 gap-y-1 text-lg font-semibold text-bright sm:text-xl">
          {t ? <TokenAvatar symbol={t.symbol || t.name || "?"} size="lg" /> : null}
          Token
          {t ? (
            <span className="font-semibold text-fg">
              {t.name || "Unnamed token"}
              {t.symbol ? <span className="ml-1.5 font-normal text-muted">({t.symbol})</span> : null}
            </span>
          ) : q.isError ? null : (
            <Skeleton className="h-6 w-48" />
          )}
        </h1>
        {t ? (
          <div className="mt-3 flex flex-wrap gap-1.5">
            <Badge>{t.type}</Badge>
            {t.is_erc3643 ? (
              <Badge tone="gold" title="T-REX permissioned token: transfers need verified identities">
                ERC-3643
              </Badge>
            ) : null}
            {contract.data?.is_verified ? <Badge tone="ok">Source code verified</Badge> : null}
          </div>
        ) : null}
      </div>

      {q.isError ? (
        <Card>
          <ErrorState
            error={q.error}
            onRetry={() => q.refetch()}
            notFoundTitle="Token not found"
            notFoundText={
              <>
                No token contract is indexed at <span className="break-all font-mono text-fg">{addr}</span>. It may be
                an ordinary address or contract: open it as an address instead.
                <span className="mt-3 block">
                  <AddressHashLink hash={addr} />
                </span>
              </>
            }
          />
        </Card>
      ) : (
        <>
          <div className="mb-6 grid grid-cols-1 gap-4 lg:grid-cols-2">
            <Card>
              <CardHeader title="Overview" />
              {!t ? (
                <DetailSkeleton rows={3} />
              ) : (
                <div className="px-4 pb-3 pt-1 sm:px-5">
                  <Stat label="Max total supply">
                    {t.total_supply === null ? (
                      <span className="text-dim">Not reported by the contract</span>
                    ) : (
                      <TokenValue amount={t.total_supply} decimals={t.decimals} symbol={t.symbol} full />
                    )}
                  </Stat>
                  <Stat label="Holders">
                    <button type="button" className="link" onClick={() => navigate({ search: { tab: "holders" }, resetScroll: false })}>
                      {formatInt(t.holders_count)}
                    </button>
                  </Stat>
                  <Stat label="Total transfers">{formatInt(t.transfers_count)}</Stat>
                </div>
              )}
            </Card>
            <Card>
              <CardHeader title="Other Info" />
              {!t ? (
                <DetailSkeleton rows={3} />
              ) : (
                <div className="px-4 pb-3 pt-1 sm:px-5">
                  <Stat
                    label={
                      t.decimals !== null ? (
                        <>
                          Token contract (with <span className="text-fg">{t.decimals}</span> decimals)
                        </>
                      ) : (
                        "Token contract"
                      )
                    }
                  >
                    <AddressHashLink hash={t.address} full copy />
                  </Stat>
                  <Stat label="First seen">
                    {t.first_block ? (
                      <span>
                        Block <BlockLink n={t.first_block} />
                      </span>
                    ) : (
                      <span className="text-dim">n/a</span>
                    )}
                  </Stat>
                </div>
              )}
            </Card>
          </div>

          <Tabs
            items={[
              { id: "token_transfers", label: "Transfers", count: t?.transfers_count ?? null },
              { id: "holders", label: "Holders", count: t?.holders_count ?? null },
              {
                id: "contract",
                label: (
                  <span className="inline-flex items-center gap-1">
                    Contract
                    {contract.data?.is_verified ? <CheckCircle2 className="h-3.5 w-3.5 text-ok" aria-label="verified" /> : null}
                  </span>
                ),
              },
            ]}
            active={active}
            onChange={(id) => navigate({ search: { tab: id === "token_transfers" ? undefined : id }, resetScroll: false })}
          />

          <div key={active} className="fade-in">
          {active === "token_transfers" ? (
            <TokenList<TokenTransfer>
              keyName="token-transfers"
              path={`/tokens/${addr}/transfers`}
              page={page}
              onPage={setPage}
              cols={8}
              empty="This token has not moved yet."
              summary={(n) => `A total of ${formatInt(n)} transfers found`}
              render={(items) => <TokenTransferTable items={items} hideToken />}
            />
          ) : active === "holders" ? (
            <TokenList<TokenHolder>
              keyName="token-holders"
              path={`/tokens/${addr}/holders`}
              page={page}
              onPage={setPage}
              cols={4}
              empty="Nobody holds this token right now."
              summary={(n) => `A total of ${formatInt(n)} ${n === 1 ? "holder" : "holders"}`}
              render={(items) => (
                <HolderTable
                  items={items}
                  decimals={t?.decimals ?? null}
                  symbol={t?.symbol ?? null}
                  offset={(page - 1) * PAGE_SIZE}
                  showId={t?.type !== "ERC-20"}
                  tokenAddress={t?.address ?? addr}
                />
              )}
            />
          ) : (
            <Suspense
              fallback={
                <Card>
                  <DetailSkeleton rows={6} />
                </Card>
              }
            >
              <ContractTab addr={addr} view={tab && tab in CONTRACT_VIEWS ? CONTRACT_VIEWS[tab] : "code"} />
            </Suspense>
          )}
          </div>
        </>
      )}
    </>
  );
}

function TokenList<T>({
  keyName,
  path,
  page,
  onPage,
  cols,
  empty,
  summary,
  render,
}: {
  keyName: string;
  path: string;
  page: number;
  onPage: (p: number) => void;
  cols: number;
  empty: string;
  summary: (n: number) => string;
  render: (items: T[]) => ReactNode;
}) {
  const q = usePage<T>([keyName, path.toLowerCase()], path, page);
  return (
    <Card>
      {q.isError && !q.data ? (
        <ErrorState error={q.error} onRetry={() => q.refetch()} />
      ) : !q.data ? (
        <TableSkeleton rows={8} cols={cols} />
      ) : q.data.items.length === 0 ? (
        <EmptyState title="Nothing here yet">{empty}</EmptyState>
      ) : (
        <>
          <ListSummary>{summary(q.data.total)}</ListSummary>
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

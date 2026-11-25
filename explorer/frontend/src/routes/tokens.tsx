import { createFileRoute, Link } from "@tanstack/react-router";
import { Coins } from "lucide-react";

import { ListFilter } from "@/components/ListFilter";
import { AddressHashLink, TokenAvatar, TokenValue } from "@/components/links";
import { Badge, Card, EmptyState, ErrorState, PageTitle, Pagination, TableSkeleton, cx } from "@/components/ui";
import { PAGE_SIZE, usePage } from "@/lib/api";
import { formatInt } from "@/lib/format";
import { pageParam, strParam } from "@/lib/search";
import { pageHead } from "@/lib/seo";
import type { Token, TokenType } from "@/lib/types";
import { NETWORK_NAME } from "@/lib/site";

const TYPES: TokenType[] = ["ERC-20", "ERC-721", "ERC-1155"];

interface TokensSearch {
  q?: string;
  type?: TokenType;
  page?: number;
}

export const Route = createFileRoute("/tokens")({
  validateSearch: (s: Record<string, unknown>): TokensSearch => {
    const type = strParam(s.type)?.toUpperCase();
    return {
      q: strParam(s.q),
      type: TYPES.includes(type as TokenType) ? (type as TokenType) : undefined,
      page: pageParam(s.page),
    };
  },
  head: () =>
    pageHead("Tokens", `Tokens on the ${NETWORK_NAME} chain: ERC-20, ERC-721 and ERC-1155.`),
  component: TokensPage,
});

function TokensPage() {
  const { q: text, type, page = 1 } = Route.useSearch();
  const navigate = Route.useNavigate();
  const q = usePage<Token>(["tokens"], "/tokens", page, { q: text, type });
  const offset = (page - 1) * PAGE_SIZE;

  return (
    <>
      <PageTitle
        title="Tokens"
        icon={<Coins className="h-5 w-5" />}
        subtitle={q.data ? `${formatInt(q.data.total)} tokens, by number of holders.` : "By number of holders."}
      />
      <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <ListFilter
          value={text ?? ""}
          onChange={(v) => navigate({ search: (s) => ({ ...s, q: v || undefined, page: undefined }) })}
          placeholder="Filter by name or symbol"
        />
        <div className="flex w-full items-center gap-1 overflow-x-auto rounded-xl border border-line bg-raised p-1 sm:w-auto">
          {([undefined, ...TYPES] as (TokenType | undefined)[]).map((t) => (
            <button
              key={t ?? "all"}
              type="button"
              onClick={() => navigate({ search: (s) => ({ ...s, type: t, page: undefined }) })}
              className={cx(
                "whitespace-nowrap rounded-lg px-3 py-1.5 text-sm font-semibold transition-colors",
                type === t ? "bg-card text-bright shadow-sm" : "text-muted hover:text-fg",
              )}
            >
              {t ?? "All"}
            </button>
          ))}
        </div>
      </div>
      <Card>
        {q.isError && !q.data ? (
          <ErrorState error={q.error} onRetry={() => q.refetch()} />
        ) : !q.data ? (
          <TableSkeleton rows={10} cols={7} />
        ) : q.data.items.length === 0 ? (
          <EmptyState title="No tokens match">
            {text || type ? "Try a different name, symbol or type." : "No token contract has been indexed yet."}
          </EmptyState>
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th className="w-12">#</th>
                  <th>Token</th>
                  <th>Type</th>
                  <th>Contract</th>
                  <th className="num">Holders</th>
                  <th className="num">Transfers</th>
                  <th className="num">Total supply</th>
                </tr>
              </thead>
              <tbody>
                {q.data.items.map((t, i) => (
                  <tr key={t.address}>
                    <td className="text-muted tabular-nums">{offset + i + 1}</td>
                    <td>
                      <Link
                        to="/token/$addr"
                        params={{ addr: t.address }}
                        className="group flex min-w-0 max-w-[320px] items-center gap-2.5"
                        title={t.address}
                      >
                        <TokenAvatar symbol={t.symbol || t.name || "?"} />
                        <span className="min-w-0">
                          <span className="block truncate font-semibold text-link group-hover:underline">
                            {t.name || "Unnamed token"}
                          </span>
                          {t.symbol ? <span className="block truncate text-xs text-dim">{t.symbol}</span> : null}
                        </span>
                      </Link>
                    </td>
                    <td>
                      <span className="inline-flex gap-1">
                        <Badge>{t.type}</Badge>
                        {t.is_erc3643 ? <Badge tone="gold">ERC-3643</Badge> : null}
                      </span>
                    </td>
                    <td>
                      <AddressHashLink hash={t.address} />
                    </td>
                    <td className="num">{formatInt(t.holders_count)}</td>
                    <td className="num">{formatInt(t.transfers_count)}</td>
                    <td className="num">
                      {t.total_supply === null ? (
                        <span className="text-dim">n/a</span>
                      ) : (
                        <TokenValue amount={t.total_supply} decimals={t.decimals} symbol={t.symbol} maxFrac={2} />
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {q.data ? (
          <Pagination
            page={page}
            limit={PAGE_SIZE}
            total={q.data.total}
            hasNext={q.data.next_offset !== null}
            loading={q.isFetching && q.isPlaceholderData}
            onPage={(p) => navigate({ search: (s) => ({ ...s, page: p > 1 ? p : undefined }) })}
          />
        ) : null}
      </Card>
    </>
  );
}

/** /search-results?q=: everything /api/v2/search found for the query (max 20). */
import { createFileRoute, Link } from "@tanstack/react-router";
import { Search } from "lucide-react";

import { RESULT_LABEL, resultIcon, resultSub, resultTitle } from "@/components/SearchBar";
import { Badge, Card, EmptyState, ErrorState, PageTitle, Skeleton } from "@/components/ui";
import { useApi } from "@/lib/api";
import { ADDRESS_RE, BLOCK_NUM_RE, HASH_RE } from "@/lib/format";
import { strParam } from "@/lib/search";
import { pageHead } from "@/lib/seo";
import type { SearchResult } from "@/lib/types";

export const Route = createFileRoute("/search-results")({
  validateSearch: (s: Record<string, unknown>): { q?: string } => ({ q: strParam(s.q) }),
  head: () => ({ meta: [...pageHead("Search results").meta, { name: "robots", content: "noindex" }] }),
  component: SearchResultsPage,
});

function ResultLink({ r }: { r: SearchResult }) {
  const cls = "flex w-full items-center gap-3 px-4 py-3.5 transition-colors hover:bg-raised sm:px-5";
  const inner = (
    <>
      <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-line bg-raised text-gold">
        {resultIcon(r.type)}
      </span>
      <span className="min-w-0 flex-1">
        <span
          className={
            r.type === "transaction"
              ? "block break-all font-mono text-[13px] text-link"
              : "block truncate font-semibold text-link"
          }
        >
          {resultTitle(r)}
        </span>
        {resultSub(r) ? <span className="block break-all font-mono text-xs text-dim">{resultSub(r)}</span> : null}
      </span>
      <Badge className="shrink-0">{RESULT_LABEL[r.type]}</Badge>
    </>
  );
  switch (r.type) {
    case "block":
      return (
        <Link to="/block/$id" params={{ id: String(r.number ?? r.hash ?? "") }} className={cls}>
          {inner}
        </Link>
      );
    case "transaction":
      return (
        <Link to="/tx/$hash" params={{ hash: r.hash ?? "" }} className={cls}>
          {inner}
        </Link>
      );
    case "token":
      return (
        <Link to="/token/$addr" params={{ addr: r.address ?? "" }} className={cls}>
          {inner}
        </Link>
      );
    default:
      return (
        <Link to="/address/$addr" params={{ addr: r.address ?? "" }} className={cls}>
          {inner}
        </Link>
      );
  }
}

function SearchResultsPage() {
  const { q = "" } = Route.useSearch();
  const query = q.trim();
  const res = useApi<SearchResult[]>(["search", query], query ? "/search" : null, { q: query });

  const hint = BLOCK_NUM_RE.test(query)
    ? "No block with that number is indexed yet."
    : HASH_RE.test(query)
      ? "No transaction or block has that hash in the index. A transaction sent moments ago can take a few seconds to show up."
      : ADDRESS_RE.test(query)
        ? "That address has no activity on the chain yet."
        : "Search by block number, transaction or block hash, address, token name or symbol, or verified contract name.";

  return (
    <>
      <PageTitle
        title="Search results"
        icon={<Search className="h-5 w-5" />}
        subtitle={
          query ? (
            <span>
              For <span className="break-all font-mono text-fg">{query}</span>
              {res.data ? ` (${res.data.length} ${res.data.length === 1 ? "match" : "matches"})` : ""}
            </span>
          ) : (
            "Type something into the search box above."
          )
        }
      />
      <Card>
        {!query ? (
          <EmptyState title="Nothing to search for">{hint}</EmptyState>
        ) : res.isError ? (
          <ErrorState error={res.error} onRetry={() => res.refetch()} />
        ) : !res.data ? (
          <ul className="divide-y divide-line" aria-busy="true">
            {Array.from({ length: 4 }).map((_, i) => (
              <li key={i} className="flex items-center gap-3 px-5 py-3.5">
                <Skeleton className="h-10 w-10 rounded-xl" />
                <span className="flex flex-1 flex-col gap-2">
                  <Skeleton className="h-4 w-1/2" />
                  <Skeleton className="h-3 w-1/3" />
                </span>
              </li>
            ))}
          </ul>
        ) : res.data.length === 0 ? (
          <EmptyState title="No matches" icon={<Search className="h-5 w-5" />}>
            {hint}
            {ADDRESS_RE.test(query) ? (
              <span className="mt-3 block">
                <Link to="/address/$addr" params={{ addr: query }} className="link font-semibold">
                  Open the address page anyway
                </Link>
              </span>
            ) : null}
          </EmptyState>
        ) : (
          <ul className="divide-y divide-line">
            {res.data.map((r, i) => (
              <li key={`${r.type}:${r.hash ?? r.address ?? r.number}:${i}`}>
                <ResultLink r={r} />
              </li>
            ))}
          </ul>
        )}
      </Card>
    </>
  );
}

/**
 * Contract internal transactions across the chain (Etherscan's "View
 * Contract Internal Txns"). Reads `GET /api/v2/internal-transactions`
 * (Page<InternalTx>, newest first); until the engine serves that list the page
 * says so and points to where internal transactions are shown today.
 */
import { createFileRoute, Link } from "@tanstack/react-router";

import { InternalTxTable } from "@/components/tables";
import { Card, EmptyState, ErrorState, ListSummary, PageTitle, Pagination, TableSkeleton } from "@/components/ui";
import { ApiError, PAGE_SIZE, usePage } from "@/lib/api";
import { formatInt } from "@/lib/format";
import { pageSearch } from "@/lib/search";
import { pageHead } from "@/lib/seo";
import type { InternalTx } from "@/lib/types";
import { NETWORK_NAME } from "@/lib/site";

export const Route = createFileRoute("/txs-internal")({
  validateSearch: pageSearch,
  head: () => pageHead("Contract internal transactions", `Internal transactions (calls between contracts) on the ${NETWORK_NAME} chain.`),
  component: InternalTxsPage,
});

function InternalTxsPage() {
  const { page = 1 } = Route.useSearch();
  const navigate = Route.useNavigate();
  const q = usePage<InternalTx>(["internal-txs"], "/internal-transactions", page);
  const missing = q.error instanceof ApiError && q.error.status === 404;

  return (
    <>
      <PageTitle title="Contract Internal Transactions" />
      <Card>
        {missing ? (
          <EmptyState title="Not listed across the chain yet">
            The explorer API does not serve a chain-wide list of internal transactions yet. Every transaction and every
            address shows its own internal transactions: open one from{" "}
            <Link to="/txs" className="link">
              the transactions list
            </Link>
            .
          </EmptyState>
        ) : q.isError && !q.data ? (
          <ErrorState error={q.error} onRetry={() => q.refetch()} />
        ) : !q.data ? (
          <TableSkeleton rows={10} cols={8} />
        ) : q.data.items.length === 0 ? (
          <EmptyState title="No internal transactions yet" />
        ) : (
          <>
            <ListSummary>A total of {formatInt(q.data.total)} internal transactions found</ListSummary>
            <InternalTxTable items={q.data.items} />
          </>
        )}
        {q.data ? (
          <Pagination
            page={page}
            limit={PAGE_SIZE}
            total={q.data.total}
            hasNext={q.data.next_offset !== null}
            loading={q.isFetching && q.isPlaceholderData}
            onPage={(p) => navigate({ search: { page: p > 1 ? p : undefined } })}
          />
        ) : null}
      </Card>
    </>
  );
}

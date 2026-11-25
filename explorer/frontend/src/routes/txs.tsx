import { createFileRoute } from "@tanstack/react-router";

import { TxTable } from "@/components/tables";
import { Card, EmptyState, ErrorState, ListSummary, PageTitle, Pagination, TableSkeleton } from "@/components/ui";
import { PAGE_SIZE, usePage } from "@/lib/api";
import { formatInt } from "@/lib/format";
import { pollInterval, useLiveBlocks } from "@/lib/live";
import { pageSearch } from "@/lib/search";
import { pageHead } from "@/lib/seo";
import type { TxSummary } from "@/lib/types";
import { NETWORK_NAME } from "@/lib/site";

export const Route = createFileRoute("/txs")({
  validateSearch: pageSearch,
  head: () => pageHead("Transactions", `Every transaction on the ${NETWORK_NAME} chain, newest first.`),
  component: TxsPage,
});

function TxsPage() {
  const { page = 1 } = Route.useSearch();
  const navigate = Route.useNavigate();
  const first = page === 1;
  const status = useLiveBlocks((b) => {
    if (b.tx_count > 0) void q.refetch();
  }, first);
  const q = usePage<TxSummary>(["txs"], "/transactions", page, undefined, {
    refetchInterval: first ? pollInterval(status) : false,
  });

  return (
    <>
      <PageTitle title="Transactions" />
      <Card>
        {q.isError && !q.data ? (
          <ErrorState error={q.error} onRetry={() => q.refetch()} />
        ) : !q.data ? (
          <TableSkeleton rows={10} cols={9} />
        ) : q.data.items.length === 0 ? (
          <EmptyState title={page > 1 ? "No transactions on this page" : "No transactions yet"} />
        ) : (
          <>
            <ListSummary>A total of {formatInt(q.data.total)} transactions found</ListSummary>
            <TxTable items={q.data.items} />
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

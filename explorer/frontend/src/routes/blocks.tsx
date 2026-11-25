import { createFileRoute } from "@tanstack/react-router";

import { BlockTable } from "@/components/tables";
import { Card, EmptyState, ErrorState, ListSummary, PageTitle, Pagination, TableSkeleton } from "@/components/ui";
import { PAGE_SIZE, usePage } from "@/lib/api";
import { formatInt } from "@/lib/format";
import { useFresh } from "@/lib/fresh";
import { pollInterval, useLiveBlocks } from "@/lib/live";
import { pageSearch } from "@/lib/search";
import { pageHead } from "@/lib/seo";
import type { BlockSummary } from "@/lib/types";
import { NETWORK_NAME } from "@/lib/site";

export const Route = createFileRoute("/blocks")({
  validateSearch: pageSearch,
  head: () => pageHead("Blocks", `Every block on the ${NETWORK_NAME} chain, newest first.`),
  component: BlocksPage,
});

function BlocksPage() {
  const { page = 1 } = Route.useSearch();
  const navigate = Route.useNavigate();
  const first = page === 1;
  // Page one follows the live feed; older pages do not move.
  const status = useLiveBlocks(() => void q.refetch(), first);
  const q = usePage<BlockSummary>(["blocks"], "/blocks", page, undefined, {
    refetchInterval: first ? pollInterval(status) : false,
  });
  const fresh = useFresh(first ? q.data?.items : undefined, (b) => b.number);

  return (
    <>
      <PageTitle title="Blocks" />
      <Card>
        {q.isError && !q.data ? (
          <ErrorState error={q.error} onRetry={() => q.refetch()} />
        ) : !q.data ? (
          <TableSkeleton rows={10} cols={7} />
        ) : q.data.items.length === 0 ? (
          <EmptyState title={page > 1 ? "No blocks on this page" : "No blocks indexed yet"} />
        ) : (
          <>
            <ListSummary>
              Block #{q.data.items[0].number} to #{q.data.items[q.data.items.length - 1].number} (total of{" "}
              {formatInt(q.data.total)} blocks)
            </ListSummary>
            <BlockTable items={q.data.items} fresh={fresh} />
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

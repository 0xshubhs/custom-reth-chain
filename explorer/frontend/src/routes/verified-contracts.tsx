import { createFileRoute, Link } from "@tanstack/react-router";
import { BadgeCheck, ShieldCheck } from "lucide-react";

import { ListFilter } from "@/components/ListFilter";
import { AddressLink, TimeAgo } from "@/components/links";
import { Badge, Card, EmptyState, ErrorState, PageTitle, Pagination, TableSkeleton } from "@/components/ui";
import { PAGE_SIZE, usePage } from "@/lib/api";
import { formatInt } from "@/lib/format";
import { pageParam, strParam } from "@/lib/search";
import { pageHead } from "@/lib/seo";
import type { VerifiedContractRow } from "@/lib/types";
import { NETWORK_NAME } from "@/lib/site";

interface VcSearch {
  q?: string;
  page?: number;
}

export const Route = createFileRoute("/verified-contracts")({
  validateSearch: (s: Record<string, unknown>): VcSearch => ({ q: strParam(s.q), page: pageParam(s.page) }),
  head: () => pageHead("Verified contracts", `Contracts on the ${NETWORK_NAME} chain with published, verified source code.`),
  component: VerifiedContractsPage,
});

const MATCH_TONE: Record<string, "ok" | "info" | "gold" | "neutral"> = {
  full: "ok",
  partial: "info",
  similar: "gold",
  imported: "neutral",
};

function VerifiedContractsPage() {
  const { q: text, page = 1 } = Route.useSearch();
  const navigate = Route.useNavigate();
  const q = usePage<VerifiedContractRow>(["verified-contracts"], "/smart-contracts", page, { q: text });

  return (
    <>
      <PageTitle
        title="Verified contracts"
        icon={<ShieldCheck className="h-5 w-5" />}
        subtitle={
          q.data
            ? `${formatInt(q.data.total)} contracts with verified source code.`
            : "Contracts with verified source code."
        }
        right={
          <Link to="/contract-verification" className="btn btn-primary">
            <BadgeCheck className="h-4 w-4" /> Verify a contract
          </Link>
        }
      />
      <div className="mb-4">
        <ListFilter
          value={text ?? ""}
          onChange={(v) => navigate({ search: { q: v || undefined } })}
          placeholder="Filter by contract name or address"
        />
      </div>
      <Card>
        {q.isError && !q.data ? (
          <ErrorState error={q.error} onRetry={() => q.refetch()} />
        ) : !q.data ? (
          <TableSkeleton rows={10} cols={6} />
        ) : q.data.items.length === 0 ? (
          <EmptyState title={text ? "No verified contract matches" : "No verified contracts yet"}>
            {text ? "Try another name or address." : "Verify one with its Solidity standard JSON input."}
          </EmptyState>
        ) : (
          <div className="table-wrap">
            <table className="data-table">
              <thead>
                <tr>
                  <th>Contract</th>
                  <th>Name</th>
                  <th>Compiler</th>
                  <th>Match</th>
                  <th className="num">Transactions</th>
                  <th>Verified</th>
                </tr>
              </thead>
              <tbody>
                {q.data.items.map((r) => (
                  <tr key={r.address.hash}>
                    <td>
                      <AddressLink addr={{ ...r.address, name: null }} copy />
                    </td>
                    <td className="font-semibold text-fg">{r.name}</td>
                    <td className="font-mono text-[12.5px] text-muted">{r.compiler_version}</td>
                    <td>
                      <Badge tone={MATCH_TONE[r.match_type] ?? "neutral"}>{r.match_type}</Badge>
                    </td>
                    <td className="num">{formatInt(r.tx_count)}</td>
                    <td className="text-muted">
                      <TimeAgo iso={r.verified_at} />
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

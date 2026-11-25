/** /token/<addr>/instance/<id>: one ERC-721 or ERC-1155 token id, its owner(s) and its transfers. */
import { createFileRoute, Link } from "@tanstack/react-router";
import { Hash } from "lucide-react";

import { AddressInvalid } from "@/components/common";
import { AddressLink, TokenAvatar, TokenLink, TokenValue } from "@/components/links";
import { HolderTable, TokenTransferTable } from "@/components/tables";
import {
  Badge,
  Card,
  CardHeader,
  DetailList,
  DetailRow,
  DetailSkeleton,
  EmptyState,
  ErrorState,
  FullValue,
  PageTitle,
  Pagination,
  Skeleton,
  TableSkeleton,
} from "@/components/ui";
import { PAGE_SIZE, useApi, usePage } from "@/lib/api";
import { formatInt, isAddress } from "@/lib/format";
import { pageSearch } from "@/lib/search";
import { pageHead } from "@/lib/seo";
import type { Page, Token, TokenHolder, TokenTransfer } from "@/lib/types";

export const Route = createFileRoute("/token/$addr_/instance/$id")({
  validateSearch: pageSearch,
  head: ({ params }) => pageHead(`Token #${params.id.length > 20 ? `${params.id.slice(0, 18)}...` : params.id}`),
  component: InstancePage,
});

function InstancePage() {
  const { addr, id } = Route.useParams();
  const { page = 1 } = Route.useSearch();
  const navigate = Route.useNavigate();
  const valid = isAddress(addr);
  const idOk = /^\d{1,80}$/.test(id);
  const token = useApi<Token>(["token", addr.toLowerCase()], valid ? `/tokens/${addr}` : null);
  const owners = useApi<Page<TokenHolder>>(
    ["token-instance-holders", addr.toLowerCase(), id],
    valid && idOk ? `/tokens/${addr}/holders` : null,
    { token_id: id, limit: 25, offset: 0 },
  );
  const transfers = usePage<TokenTransfer>(
    ["token-instance-transfers", addr.toLowerCase(), id],
    valid && idOk ? `/tokens/${addr}/transfers` : null,
    page,
    { token_id: id },
  );
  const t = token.data;

  if (!valid) return <AddressInvalid value={addr} what="token address" />;
  if (!idOk) {
    return (
      <Card>
        <EmptyState title="That is not a token id">A token id is a whole number.</EmptyState>
      </Card>
    );
  }

  const ownerList = owners.data?.items ?? [];
  const single = t?.type === "ERC-721";

  return (
    <>
      <PageTitle
        icon={t ? <TokenAvatar symbol={t.symbol || t.name || "?"} size="lg" /> : <Hash className="h-5 w-5" />}
        title={
          <span className="break-all">
            {t ? `${t.name || "Token"} ` : ""}
            <span className="font-mono" title={id}>
              #{id.length > 16 ? `${id.slice(0, 8)}\u2026${id.slice(-6)}` : id}
            </span>
          </span>
        }
        badges={t ? <Badge tone="info">{t.type}</Badge> : undefined}
        subtitle={
          t ? (
            <span className="inline-flex flex-wrap items-center gap-2">
              Token instance of <TokenLink token={t} maxW="max-w-[280px]" />
            </span>
          ) : token.isError ? undefined : (
            <Skeleton className="h-4 w-60" />
          )
        }
      />

      {token.isError ? (
        <Card>
          <ErrorState error={token.error} onRetry={() => token.refetch()} notFoundTitle="Token not found" />
        </Card>
      ) : (
        <>
          <Card className="mb-6">
            <CardHeader title="Overview" />
            {!t || !owners.data ? (
              <DetailSkeleton rows={4} />
            ) : (
              <DetailList>
                <DetailRow label="Token">
                  <span className="inline-flex flex-wrap items-center gap-2">
                    <TokenLink token={t} maxW="max-w-[280px]" />
                    <Badge>{t.type}</Badge>
                  </span>
                </DetailRow>
                <DetailRow label="Token ID">
                  <FullValue value={id} />
                </DetailRow>
                <DetailRow label={single ? "Owner" : "Holders of this id"}>
                  {ownerList.length === 0 ? (
                    <span className="text-dim">Nobody holds it right now (burned, or never minted)</span>
                  ) : single ? (
                    <AddressLink addr={ownerList[0].address} full copy />
                  ) : (
                    <span>{formatInt(owners.data.total)}</span>
                  )}
                </DetailRow>
                {!single && ownerList.length > 0 ? (
                  <DetailRow label="Supply of this id">
                    <TokenValue
                      amount={ownerList.reduce((s, h) => s + BigInt(h.balance || "0"), 0n).toString()}
                      decimals={0}
                    />
                    {owners.data.total > ownerList.length ? (
                      <span className="ml-2 text-xs text-dim">(top {ownerList.length} holders)</span>
                    ) : null}
                  </DetailRow>
                ) : null}
                <DetailRow label="Contract">
                  <Link
                    to="/address/$addr"
                    params={{ addr: t.address }}
                    className="link break-all font-mono text-[13px]"
                  >
                    {t.address}
                  </Link>
                </DetailRow>
              </DetailList>
            )}
          </Card>

          {!single && ownerList.length > 0 ? (
            <Card className="mb-6">
              <CardHeader title="Holders of this id" />
              <HolderTable items={ownerList} decimals={0} symbol={null} offset={0} showId={false} />
            </Card>
          ) : null}

          <Card>
            <CardHeader title="Transfers of this id" />
            {transfers.isError && !transfers.data ? (
              <ErrorState error={transfers.error} onRetry={() => transfers.refetch()} />
            ) : !transfers.data ? (
              <TableSkeleton rows={4} cols={8} />
            ) : transfers.data.items.length === 0 ? (
              <EmptyState title="No transfers">This token id has not moved.</EmptyState>
            ) : (
              <TokenTransferTable items={transfers.data.items} hideToken />
            )}
            {transfers.data ? (
              <Pagination
                page={page}
                limit={PAGE_SIZE}
                total={transfers.data.total}
                hasNext={transfers.data.next_offset !== null}
                loading={transfers.isFetching && transfers.isPlaceholderData}
                onPage={(p) => navigate({ search: { page: p > 1 ? p : undefined } })}
              />
            ) : null}
          </Card>
        </>
      )}
    </>
  );
}

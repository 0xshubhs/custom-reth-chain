/** /block/<number or hash>, tabs: details (default) and txs (Blockscout's ?tab=txs). */
import { createFileRoute, Link } from "@tanstack/react-router";
import { ChevronLeft, ChevronRight } from "lucide-react";

import { AddressLink, BlockHashLink, NativeValue, TimeFull } from "@/components/links";
import { GasBar, TxTable } from "@/components/tables";
import {
  Badge,
  Card,
  DetailList,
  DetailRow,
  DetailSkeleton,
  EmptyState,
  ErrorState,
  FullValue,
  ListSummary,
  PageTitle,
  Pagination,
  TableSkeleton,
  Tabs,
} from "@/components/ui";
import { PAGE_SIZE, useApi, usePage } from "@/lib/api";
import { formatBytes, formatGwei, formatInt, hexToUtf8 } from "@/lib/format";
import { tabPageSearch } from "@/lib/search";
import { pageHead } from "@/lib/seo";
import type { Block, Stats, TxSummary } from "@/lib/types";
import { NETWORK_NAME } from "@/lib/site";

export const Route = createFileRoute("/block/$id")({
  validateSearch: tabPageSearch,
  head: ({ params }) =>
    pageHead(/^\d+$/.test(params.id) ? `Block #${params.id}` : "Block", `Details of ${NETWORK_NAME} block ${params.id}.`),
  component: BlockPage,
});

function BlockPage() {
  const { id } = Route.useParams();
  const { tab, page = 1 } = Route.useSearch();
  const navigate = Route.useNavigate();
  const q = useApi<Block>(["block", id], `/blocks/${encodeURIComponent(id)}`);
  // The newest indexed block decides whether "next" exists; confirmations is
  // the fallback until the stats answer.
  const stats = useApi<Stats>(["stats"], "/stats");
  const b = q.data;
  const active = tab === "txs" || tab === "transactions" ? "txs" : "details";

  return (
    <>
      <PageTitle
        title={
          <span className="inline-flex items-center gap-2">
            Block <span className="font-normal tabular-nums text-muted">#{b ? b.number : /^\d+$/.test(id) ? id : ""}</span>
          </span>
        }
        right={
          b ? (
            <PrevNext
              n={b.number}
              hasNext={stats.data?.latest_block != null ? b.number < stats.data.latest_block : b.confirmations > 1}
            />
          ) : undefined
        }
      />

      {q.isError ? (
        <Card>
          <ErrorState
            error={q.error}
            onRetry={() => q.refetch()}
            notFoundTitle="Block not found"
            notFoundText={
              <>
                There is no block <span className="break-all font-mono text-fg">{id}</span> in the index. If it was
                sealed moments ago, it may not be indexed yet.
              </>
            }
          />
        </Card>
      ) : (
        <>
          <Tabs
            items={[
              { id: "details", label: "Overview" },
              { id: "txs", label: "Transactions", count: b?.tx_count ?? null },
            ]}
            active={active}
            onChange={(t) => navigate({ search: { tab: t === "details" ? undefined : t }, resetScroll: false })}
          />
          <div key={active} className="fade-in">
          {active === "details" ? (
            <Card>
              {b ? (
                <BlockDetails b={b} onTxs={() => navigate({ search: { tab: "txs" }, resetScroll: false })} />
              ) : (
                <DetailSkeleton rows={12} />
              )}
            </Card>
          ) : (
            <BlockTxs
              n={b?.number ?? null}
              page={page}
              onPage={(p) => navigate({ search: { tab: "txs", page: p > 1 ? p : undefined } })}
            />
          )}
          </div>
        </>
      )}
    </>
  );
}

function PrevNext({ n, hasNext }: { n: number; hasNext: boolean }) {
  return (
    <div className="flex items-center gap-1.5">
      {n > 0 ? (
        <Link to="/block/$id" params={{ id: String(n - 1) }} className="btn btn-sm btn-icon" aria-label="Previous block" title={`Block ${n - 1}`}>
          <ChevronLeft className="h-4 w-4" />
        </Link>
      ) : null}
      {hasNext ? (
        <Link to="/block/$id" params={{ id: String(n + 1) }} className="btn btn-sm btn-icon" aria-label="Next block" title={`Block ${n + 1}`}>
          <ChevronRight className="h-4 w-4" />
        </Link>
      ) : null}
    </div>
  );
}

function BlockDetails({ b, onTxs }: { b: Block; onTxs: () => void }) {
  const extraText = b.extra_data ? hexToUtf8(b.extra_data) : null;
  return (
    <DetailList>
      <DetailRow label="Block Height" hint="Also known as block number: the length of the chain, starting at the genesis block (0).">
        <span className="tabular-nums">{b.number}</span>
        <Badge className="ml-2">
          {formatInt(b.confirmations)} Block {b.confirmations === 1 ? "Confirmation" : "Confirmations"}
        </Badge>
      </DetailRow>
      <DetailRow label="Timestamp" hint="The date and time at which the block was sealed.">
        <TimeFull iso={b.timestamp} />
      </DetailRow>
      <DetailRow label="Transactions" hint="The transactions in this block.">
        {b.tx_count > 0 ? (
          <span>
            <button type="button" onClick={onTxs} className="link font-medium">
              {formatInt(b.tx_count)} {b.tx_count === 1 ? "transaction" : "transactions"}
            </button>{" "}
            in this block
          </span>
        ) : (
          <span className="text-muted">No transactions</span>
        )}
      </DetailRow>
      <DetailRow label="Validator" hint="The signer that sealed this block (proof of authority)." divider>
        <AddressLink addr={b.miner} full copy />
      </DetailRow>
      <DetailRow label="Hash" hint="The hash of the block header." divider>
        <FullValue value={b.hash} />
      </DetailRow>
      <DetailRow label="Parent Hash" hint="The hash of the block before this one.">
        <span className="inline-flex max-w-full items-start gap-1.5">
          <BlockHashLink hash={b.parent_hash} full />
        </span>
      </DetailRow>
      <DetailRow label="Gas Used" hint="Total gas used by all transactions in the block." divider>
        <GasBar used={b.gas_used} limit={b.gas_limit} />
      </DetailRow>
      <DetailRow label="Gas Limit" hint="The most gas the transactions in the block may use.">
        <span className="tabular-nums">{formatInt(b.gas_limit)}</span>
      </DetailRow>
      <DetailRow label="Base Fee Per Gas" hint="The minimum fee per gas for a transaction to be included (EIP-1559).">
        {b.base_fee_per_gas === null ? (
          <span className="text-dim">n/a</span>
        ) : (
          <span className="inline-flex flex-wrap gap-x-2">
            <NativeValue wei={b.base_fee_per_gas} full />
            <span className="text-muted">({formatGwei(b.base_fee_per_gas)})</span>
          </span>
        )}
      </DetailRow>
      <DetailRow label="Size" hint="The block size in bytes.">
        {b.size === null ? <span className="text-dim">n/a</span> : formatBytes(b.size)}
      </DetailRow>
      <DetailRow label="Extra Data" hint="Data the signer put into the block header." divider>
        <span className="flex flex-col gap-1">
          <FullValue value={b.extra_data || "0x"} />
          {extraText ? <span className="text-muted">Text: {extraText}</span> : null}
        </span>
      </DetailRow>
      <DetailRow label="State Root" hint="The root of the state trie after this block.">
        <FullValue value={b.state_root} />
      </DetailRow>
      <DetailRow label="Transactions Root" hint="The root of the transactions trie of this block.">
        <FullValue value={b.transactions_root} />
      </DetailRow>
      <DetailRow label="Receipts Root" hint="The root of the receipts trie of this block.">
        <FullValue value={b.receipts_root} />
      </DetailRow>
      <DetailRow label="Nonce" hint="Block nonce (unused under proof of authority).">
        {b.nonce ? <FullValue value={b.nonce} /> : <span className="text-dim">n/a</span>}
      </DetailRow>
      <DetailRow label="Mix Hash" hint="Mix hash of the block header.">
        {b.mix_hash ? <FullValue value={b.mix_hash} /> : <span className="text-dim">n/a</span>}
      </DetailRow>
    </DetailList>
  );
}

function BlockTxs({ n, page, onPage }: { n: number | null; page: number; onPage: (p: number) => void }) {
  const q = usePage<TxSummary>(["block-txs", n], n === null ? null : `/blocks/${n}/transactions`, page);
  return (
    <Card>
      {q.isError && !q.data ? (
        <ErrorState error={q.error} onRetry={() => q.refetch()} />
      ) : !q.data ? (
        <TableSkeleton rows={5} cols={8} />
      ) : q.data.items.length === 0 ? (
        <EmptyState title="No transactions in this block">
          The chain seals a block when a transaction arrives, but a block can still be empty.
        </EmptyState>
      ) : (
        <>
          <ListSummary>A total of {formatInt(q.data.total)} transactions found</ListSummary>
          <TxTable items={q.data.items} showBlock={false} />
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

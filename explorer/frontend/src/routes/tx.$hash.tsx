/**
 * /tx/<hash>, in Etherscan's layout: Overview (label rows with a "?" help
 * mark, Transaction Action, tokens transferred, value and fees, then "Click to
 * show more" for gas, nonce, position and the input data with a "View input
 * as" switch), Logs, and the token transfer / internal / raw tabs. Tab names
 * stay Blockscout's so old links keep their tab: (none) or index = overview,
 * token_transfers, internal, logs, raw_trace (also raw).
 */
import { useState, type ReactNode } from "react";
import { createFileRoute, Link } from "@tanstack/react-router";
import { ChevronDown, ExternalLink, Minus, Plus, RefreshCw } from "lucide-react";

import { stringify } from "@/components/decoded";
import { InputDecoded } from "@/components/InputDecoded";
import { AddressLink, BlockLink, NativeValue, TimeFull, TokenIdLink, TokenLink, TokenValue } from "@/components/links";
import { Menu } from "@/components/overlay";
import { InternalTxTable, LogList, TokenTransferTable } from "@/components/tables";
import {
  Badge,
  Card,
  CardHeader,
  CopyButton,
  DetailList,
  DetailRow,
  DetailSkeleton,
  EmptyState,
  ErrorState,
  FullValue,
  ListSummary,
  PageTitle,
  Pagination,
  Skeleton,
  StatusBadge,
  TableSkeleton,
  Tabs,
} from "@/components/ui";
import { ethShCalldataUrl } from "@/components/useDecode";
import { PAGE_SIZE, useApi, usePage } from "@/lib/api";
import { formatGwei, formatInt, formatPct, hexToUtf8, shortHash } from "@/lib/format";
import { tabPageSearch } from "@/lib/search";
import { pageHead } from "@/lib/seo";
import type { InternalTx, Log, Page, RawTx, TokenTransfer, Tx } from "@/lib/types";
import { NETWORK_NAME } from "@/lib/site";

export const Route = createFileRoute("/tx/$hash")({
  validateSearch: tabPageSearch,
  head: ({ params }) => pageHead(`Transaction ${shortHash(params.hash, 10, 8)}`, `Details of ${NETWORK_NAME} transaction ${params.hash}.`),
  component: TxPage,
});

const TX_TYPES: Record<number, string> = {
  0: "Legacy",
  1: "EIP-2930",
  2: "EIP-1559",
  3: "EIP-4844",
  4: "EIP-7702",
};

type TabId = "details" | "token_transfers" | "internal" | "logs" | "raw_trace";

function normTab(t: string | undefined): TabId {
  switch (t) {
    case "token_transfers":
    case "internal":
    case "logs":
    case "raw_trace":
      return t;
    case "raw":
      return "raw_trace";
    case "internal_txns":
      return "internal";
    default:
      return "details";
  }
}

function TxPage() {
  const { hash } = Route.useParams();
  const { tab, page = 1 } = Route.useSearch();
  const navigate = Route.useNavigate();
  const active = normTab(tab);

  const q = useApi<Tx>(["tx", hash], `/transactions/${encodeURIComponent(hash)}`, undefined, {
    // A transaction read straight from the node (not indexed yet, or pending)
    // is re-read until the indexer has it.
    refetchInterval: (query) => {
      const d = query.state.data as Tx | undefined;
      return d && (!d.indexed || d.status === "pending") ? 5000 : false;
    },
  });
  const tx = q.data;
  const setTab = (t: string) => navigate({ search: { tab: t === "details" ? undefined : t }, resetScroll: false });
  const setPage = (p: number) => navigate({ search: (s) => ({ ...s, page: p > 1 ? p : undefined }) });

  return (
    <>
      <PageTitle title="Transaction Details" />

      {tx && !tx.indexed ? (
        <div className="fade-in mb-4 flex items-start gap-3 rounded-2xl border border-info/30 bg-info/10 px-4 py-3 text-sm text-fg">
          <RefreshCw className="mt-0.5 h-4 w-4 shrink-0 animate-spin text-info [animation-duration:2.5s]" />
          <p>
            This transaction is not indexed yet: what you see was read straight from the node. Token transfers, internal
            transactions and logs appear once its block is indexed. This page refreshes by itself.
          </p>
        </div>
      ) : null}

      {q.isError ? (
        <Card>
          <ErrorState
            error={q.error}
            onRetry={() => q.refetch()}
            notFoundTitle="Transaction not found"
            notFoundText={
              <>
                Neither the index nor the node knows <span className="break-all font-mono text-fg">{hash}</span>. Check
                the hash, or try again in a few seconds if it was just sent.
              </>
            }
          />
        </Card>
      ) : (
        <>
          <Tabs
            items={[
              { id: "details", label: "Overview" },
              {
                id: "token_transfers",
                label: "Token Transfers",
                count: tx?.indexed ? tx.token_transfers_count : null,
                hidden: !!tx && tx.token_transfers_count === 0 && active !== "token_transfers",
              },
              {
                id: "internal",
                label: "Internal Txns",
                count: tx?.indexed ? tx.internal_txs_count : null,
                hidden: !!tx && tx.internal_txs_count === 0 && active !== "internal",
              },
              { id: "logs", label: "Logs", count: tx?.indexed ? tx.logs_count : null },
              { id: "raw_trace", label: "Raw" },
            ]}
            active={active}
            onChange={setTab}
          />
          <div key={active} className="fade-in">
            {active === "details" ? (
              tx ? (
                <TxOverview tx={tx} onTab={setTab} />
              ) : (
                <Card>
                  <DetailSkeleton rows={12} />
                </Card>
              )
            ) : active === "token_transfers" ? (
              <SubList<TokenTransfer>
                keyName="tx-tt"
                hash={hash}
                path="token-transfers"
                page={page}
                onPage={setPage}
                empty="No token moved in this transaction."
                summary={(n) => `${formatInt(n)} token ${n === 1 ? "transfer" : "transfers"} in this transaction`}
                render={(items) => <TokenTransferTable items={items} />}
              />
            ) : active === "internal" ? (
              <SubList<InternalTx>
                keyName="tx-itx"
                hash={hash}
                path="internal-transactions"
                page={page}
                onPage={setPage}
                empty="This transaction made no internal calls."
                summary={(n) => `The contract call from this transaction made ${formatInt(n)} internal ${n === 1 ? "call" : "calls"}`}
                render={(items) => <InternalTxTable items={items} showParent={false} />}
              />
            ) : active === "logs" ? (
              <SubList<Log>
                keyName="tx-logs"
                hash={hash}
                path="logs"
                page={page}
                onPage={setPage}
                empty="This transaction emitted no events."
                summary={(n) => `Transaction receipt event logs (${formatInt(n)})`}
                skeleton="list"
                render={(items) => <LogList items={items} />}
              />
            ) : (
              <RawTab hash={hash} />
            )}
          </div>
        </>
      )}
    </>
  );
}

/* Overview --------------------------------------------------------------------------- */

function TxAction({ tx }: { tx: Tx }) {
  if (!tx.to && tx.created_contract) {
    return (
      <span className="flex flex-wrap items-center gap-1.5">
        <span className="text-muted">Create contract</span>
        <AddressLink addr={tx.created_contract} />
        <span className="text-muted">by</span>
        <AddressLink addr={tx.from} />
      </span>
    );
  }
  if (!tx.method) {
    return (
      <span className="flex flex-wrap items-center gap-1.5">
        <span className="text-muted">Transfer</span>
        <NativeValue wei={tx.value} />
        <span className="text-muted">to</span>
        <AddressLink addr={tx.to} />
      </span>
    );
  }
  return (
    <span className="flex flex-wrap items-center gap-1.5">
      <span className="text-muted">Call</span>
      <span className="rounded-full border border-line bg-raised px-2 py-0.5 font-mono text-[12px] text-fg">{tx.method}</span>
      <span className="text-muted">function by</span>
      <AddressLink addr={tx.from} />
      <span className="text-muted">on</span>
      <AddressLink addr={tx.to} />
    </span>
  );
}

function TxOverview({ tx, onTab }: { tx: Tx; onTab: (t: string) => void }) {
  const [more, setMore] = useState(false);
  const gasPct = tx.gas_used !== null && tx.gas > 0 ? (tx.gas_used / tx.gas) * 100 : null;
  return (
    <div className="flex flex-col gap-4">
      <Card>
        <DetailList>
          <DetailRow label="Transaction Hash" hint="A transaction hash is a unique 66-character identifier generated whenever a transaction is executed.">
            <FullValue value={tx.hash} />
          </DetailRow>
          <DetailRow label="Status" hint="The status of the transaction.">
            <span className="flex flex-col items-start gap-1.5">
              <StatusBadge status={tx.status} error={tx.error} />
              {tx.error ? (
                <span className="break-words rounded-xl border border-bad/25 bg-bad/10 px-2.5 py-1.5 font-mono text-[12.5px] text-bad">
                  {tx.error}
                </span>
              ) : null}
            </span>
          </DetailRow>
          <DetailRow label="Block" hint="Number of the block in which the transaction is recorded. Block confirmations indicate how many blocks have been added since.">
            {tx.status === "pending" && !tx.block_hash ? (
              <span className="text-warn">Waiting to be sealed in a block</span>
            ) : (
              <span className="inline-flex flex-wrap items-center gap-2">
                <BlockLink n={tx.block_number} />
                <Badge>
                  {formatInt(tx.confirmations)} Block {tx.confirmations === 1 ? "Confirmation" : "Confirmations"}
                </Badge>
              </span>
            )}
          </DetailRow>
          <DetailRow label="Timestamp" hint="The date and time at which the transaction was sealed in a block.">
            <TimeFull iso={tx.timestamp} />
          </DetailRow>

          <DetailRow label="Transaction Action" hint="Highlighted events of the transaction." divider>
            <TxAction tx={tx} />
          </DetailRow>

          <DetailRow label="From" hint="The sending party of the transaction." divider>
            <AddressLink addr={tx.from} full copy />
          </DetailRow>
          <DetailRow
            label={tx.to ? (tx.to.is_contract ? "Interacted With (To)" : "To") : "To"}
            hint="The receiving party of the transaction (could be a contract address)."
          >
            {tx.to ? (
              <AddressLink addr={tx.to} full copy />
            ) : tx.created_contract ? (
              <span className="inline-flex flex-wrap items-center gap-2">
                <span className="text-muted">[Contract</span>
                <AddressLink addr={tx.created_contract} full copy />
                <span className="text-muted">Created]</span>
              </span>
            ) : (
              <span className="text-dim">Contract creation (address not known yet)</span>
            )}
          </DetailRow>

          {tx.token_transfers_count > 0 ? (
            <DetailRow label={`Tokens Transferred (${formatInt(tx.token_transfers_count)})`} hint="Token transfers made by this transaction." divider>
              <TransfersPreview hash={tx.hash} count={tx.token_transfers_count} onAll={() => onTab("token_transfers")} />
            </DetailRow>
          ) : null}

          <DetailRow label="Value" hint="The value being transacted in ETH." divider>
            <NativeValue wei={tx.value} full />
          </DetailRow>
          <DetailRow label="Transaction Fee" hint="Amount paid to the validator for processing the transaction.">
            <NativeValue wei={tx.fee} full />
          </DetailRow>
          <DetailRow label="Gas Price" hint="Cost per unit of gas specified for the transaction, in Gwei.">
            {tx.gas_price === null ? (
              <span className="text-dim">n/a</span>
            ) : (
              <span className="inline-flex flex-wrap gap-x-2">
                <span>{formatGwei(tx.effective_gas_price ?? tx.gas_price)}</span>
                <span className="text-muted">
                  (<NativeValue wei={tx.effective_gas_price ?? tx.gas_price} full />)
                </span>
              </span>
            )}
          </DetailRow>
        </DetailList>
      </Card>

      <Card>
        <DetailList>
          <DetailRow label="More Details" hint="Gas, nonce, position in the block and the input data.">
            <button type="button" onClick={() => setMore((m) => !m)} className="link inline-flex items-center gap-1 font-medium">
              {more ? <Minus className="h-3.5 w-3.5" /> : <Plus className="h-3.5 w-3.5" />}
              {more ? "Click to show less" : "Click to show more"}
            </button>
          </DetailRow>
          {more ? (
            <div className="fade-in">
              <DetailRow label="Gas Limit & Usage by Txn" hint="Maximum amount of gas allocated for the transaction and the amount eventually used." divider>
                <span className="tabular-nums">
                  {formatInt(tx.gas)}
                  <span className="mx-2 text-dim">|</span>
                  {tx.gas_used === null ? <span className="text-dim">not used yet</span> : formatInt(tx.gas_used)}
                  {gasPct !== null ? <span className="ml-1 text-muted">({formatPct(gasPct)})</span> : null}
                </span>
              </DetailRow>
              {tx.max_fee_per_gas !== null || tx.max_priority_fee_per_gas !== null ? (
                <DetailRow label="Gas Fees" hint="Max fee and max priority fee per gas (EIP-1559).">
                  <span className="inline-flex flex-wrap gap-x-4 gap-y-1">
                    {tx.max_fee_per_gas !== null ? (
                      <span>
                        <span className="text-muted">Max: </span>
                        {formatGwei(tx.max_fee_per_gas)}
                      </span>
                    ) : null}
                    {tx.max_priority_fee_per_gas !== null ? (
                      <span>
                        <span className="text-muted">Max Priority: </span>
                        {formatGwei(tx.max_priority_fee_per_gas)}
                      </span>
                    ) : null}
                  </span>
                </DetailRow>
              ) : null}
              <DetailRow label="Other Attributes" hint="Other data related to this transaction.">
                <span className="flex flex-wrap gap-2">
                  <Badge>
                    Txn Type: {tx.type} ({TX_TYPES[tx.type] ?? "other"})
                  </Badge>
                  <Badge>Nonce: {tx.nonce}</Badge>
                  <Badge>Position In Block: {tx.tx_index ?? "?"}</Badge>
                </span>
              </DetailRow>
              <DetailRow label="Input Data" hint="Additional data included for this transaction, commonly the call to a contract function.">
                <InputData tx={tx} />
              </DetailRow>
            </div>
          ) : null}
        </DetailList>
      </Card>
    </div>
  );
}

function TransfersPreview({ hash, count, onAll }: { hash: string; count: number; onAll: () => void }) {
  const q = useApi<Page<TokenTransfer>>(["tx-tt-preview", hash], `/transactions/${hash}/token-transfers`, { limit: 10 });
  if (!q.data) return q.isError ? <span className="text-dim">Could not load the transfers.</span> : <Skeleton className="h-4 w-3/4" />;
  return (
    <ul className="flex flex-col gap-2">
      {q.data.items.map((t) => (
        <li key={`${t.log_index}:${t.batch_index}`} className="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-[13px]">
          <span className="font-medium text-fg2">From</span>
          <AddressLink addr={t.from} compact maxW="max-w-[150px]" />
          <span className="font-medium text-fg2">To</span>
          <AddressLink addr={t.to} compact maxW="max-w-[150px]" />
          <span className="font-medium text-fg2">For</span>
          {t.token.type === "ERC-721" ? (
            t.token_id ? <TokenIdLink token={t.token.address} id={t.token_id} /> : <span>1</span>
          ) : (
            <span className="inline-flex items-center gap-1.5">
              <TokenValue amount={t.amount} decimals={t.token.decimals} />
              {t.token_id ? <TokenIdLink token={t.token.address} id={t.token_id} /> : null}
            </span>
          )}
          <TokenLink token={t.token} maxW="max-w-[260px]" />
        </li>
      ))}
      {count > q.data.items.length ? (
        <li>
          <button type="button" onClick={onAll} className="link text-[13px] font-medium">
            View all {formatInt(count)} transfers
          </button>
        </li>
      ) : null}
    </ul>
  );
}

/** Etherscan's "Original" view: function line, method id, then 32-byte words. */
function originalView(tx: Tx): string {
  const input = tx.input || "0x";
  if (input.length < 10) return input;
  const id = input.slice(0, 10);
  const body = input.slice(10);
  const words: string[] = [];
  for (let i = 0; i < body.length; i += 64) words.push(body.slice(i, i + 64));
  const fn = tx.decoded_input
    ? `Function: ${tx.decoded_input.method}(${tx.decoded_input.params.map((p) => `${p.type} ${p.name}`.trim()).join(", ")})\n\n`
    : "";
  return `${fn}MethodID: ${id}\n${words.map((w, i) => `[${i}]:  ${w}`).join("\n")}`;
}

type View = "original" | "decoded" | "utf8";

function InputData({ tx }: { tx: Tx }) {
  const empty = !tx.input || tx.input === "0x";
  const text = empty ? null : hexToUtf8(tx.input);
  const [view, setView] = useState<View>(empty ? "original" : "decoded");
  if (empty) return <span className="font-mono text-[13px] text-muted">0x</span>;

  const views: { id: View; label: string }[] = [
    { id: "original", label: "Original" },
    { id: "decoded", label: "Default View (decoded)" },
    { id: "utf8", label: "UTF-8" },
  ];
  let body: ReactNode;
  if (view === "decoded") body = <InputDecoded tx={tx} />;
  else if (view === "utf8")
    body = text ? (
      <pre className="code-block max-h-72 whitespace-pre-wrap break-all px-3 py-2">{text}</pre>
    ) : (
      <p className="rounded-xl border border-line bg-raised px-3 py-2 text-[13px] text-muted">The input data is not UTF-8 text.</p>
    );
  else body = <pre className="code-block max-h-72 whitespace-pre-wrap break-all px-3 py-2 text-[12.5px]">{originalView(tx)}</pre>;

  return (
    <div className="flex min-w-0 flex-col gap-2.5">
      {body}
      <div className="flex flex-wrap items-center gap-2">
        <Menu
          label="View input as"
          button={
            <span className="inline-flex items-center gap-1">
              View Input As: {views.find((v) => v.id === view)?.label} <ChevronDown className="h-3.5 w-3.5 opacity-70" />
            </span>
          }
          buttonClassName="btn btn-sm"
          items={views.map((v) => ({ key: v.id, label: v.label, selected: v.id === view, onSelect: () => setView(v.id) }))}
        />
        <CopyButton value={tx.input} label="Copy input data" size="md" className="!rounded-full border border-line !p-1.5" />
        <a href={ethShCalldataUrl(tx.input)} target="_blank" rel="noopener noreferrer" className="btn btn-sm">
          Open in ETH.sh <ExternalLink className="h-3 w-3" />
        </a>
      </div>
    </div>
  );
}

/* Sub lists ----------------------------------------------------------------------------- */

function SubList<T>({
  keyName,
  hash,
  path,
  page,
  onPage,
  empty,
  summary,
  render,
  skeleton = "table",
}: {
  keyName: string;
  hash: string;
  path: string;
  page: number;
  onPage: (p: number) => void;
  empty: string;
  summary: (n: number) => string;
  render: (items: T[]) => ReactNode;
  skeleton?: "table" | "list";
}) {
  const q = usePage<T>([keyName, hash], `/transactions/${encodeURIComponent(hash)}/${path}`, page);
  return (
    <Card>
      {q.isError && !q.data ? (
        <ErrorState error={q.error} onRetry={() => q.refetch()} />
      ) : !q.data ? (
        skeleton === "list" ? (
          <DetailSkeleton rows={6} />
        ) : (
          <TableSkeleton rows={4} cols={8} />
        )
      ) : q.data.items.length === 0 ? (
        <EmptyState title="Nothing here">{empty}</EmptyState>
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

function RawTab({ hash }: { hash: string }) {
  const q = useApi<RawTx>(["tx-raw", hash], `/transactions/${encodeURIComponent(hash)}/raw`);
  if (q.isError) {
    return (
      <Card>
        <ErrorState error={q.error} onRetry={() => q.refetch()} notFoundText="The node's raw data for this transaction is not stored yet." />
      </Card>
    );
  }
  const parts: [string, string, unknown][] = q.data
    ? [
        ["Transaction", "The transaction as the node returned it (eth_getTransactionByHash).", q.data.tx],
        ["Receipt", "The receipt as the node returned it (eth_getTransactionReceipt).", q.data.receipt],
        ["Call trace", "The node's call trace (debug_traceTransaction, callTracer).", q.data.trace],
      ]
    : [];
  return (
    <div className="flex flex-col gap-4">
      {!q.data
        ? Array.from({ length: 2 }).map((_, i) => (
            <Card key={i}>
              <div className="flex flex-col gap-2 p-5">
                <Skeleton className="h-4 w-40" />
                <Skeleton className="h-40 w-full !rounded-xl" />
              </div>
            </Card>
          ))
        : parts.map(([title, sub, value]) => {
            const json = value === null || value === undefined ? null : stringify(value);
            return (
              <Card key={title}>
                <CardHeader title={title} right={json ? <CopyButton value={json} label={`Copy ${title.toLowerCase()} JSON`} size="md" /> : undefined} />
                <div className="px-4 py-4 sm:px-5">
                  <p className="mb-3 text-xs text-muted">{sub}</p>
                  {json ? <pre className="code-block max-h-[520px] px-3 py-2">{json}</pre> : <p className="text-sm text-dim">Not available.</p>}
                </div>
              </Card>
            );
          })}
      <p className="text-xs text-muted">
        The same data as the node's JSON-RPC answers.{" "}
        <Link to="/tx/$hash" params={{ hash }} className="link">
          Back to the overview
        </Link>
      </p>
    </div>
  );
}

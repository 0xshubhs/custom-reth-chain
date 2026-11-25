/**
 * Etherscan's small interactions: the eye that previews a transaction from a
 * list, and the QR code of an address.
 */
import { useMemo } from "react";
import { Link } from "@tanstack/react-router";
import { ArrowRight, Eye, QrCode } from "lucide-react";
import { encode } from "uqr";

import { useApi } from "@/lib/api";
import { formatGwei, formatInt } from "@/lib/format";
import type { Tx } from "@/lib/types";
import { AddressLink, NativeValue } from "./links";
import { Popover } from "./overlay";
import { CopyButton, MethodBadge, Skeleton, StatusBadge } from "./ui";

function TxPreviewBody({ hash }: { hash: string }) {
  const q = useApi<Tx>(["tx", hash], `/transactions/${hash}`);
  const tx = q.data;
  if (q.isError) return <p className="p-4 text-sm text-muted">Could not load this transaction.</p>;
  if (!tx) {
    return (
      <div className="flex w-[300px] flex-col gap-2.5 p-4">
        <Skeleton className="h-3 w-24" />
        <Skeleton className="h-4 w-40" />
        <Skeleton className="h-3 w-56" />
        <Skeleton className="h-3 w-48" />
      </div>
    );
  }
  return (
    <div className="w-[320px] p-4">
      <p className="mb-3 text-[13px] font-semibold text-fg">Additional info</p>
      <dl className="flex flex-col gap-3 text-[13px]">
        <div>
          <dt className="eyebrow mb-1">Status</dt>
          <dd className="flex items-center gap-2">
            <StatusBadge status={tx.status} error={tx.error} />
            {tx.status !== "pending" ? (
              <span className="text-xs text-muted">({formatInt(tx.confirmations)} block confirmations)</span>
            ) : null}
          </dd>
        </div>
        <div>
          <dt className="eyebrow mb-1">Transaction action</dt>
          <dd className="flex min-w-0 flex-wrap items-center gap-1.5">
            <MethodBadge method={tx.method} />
            <span className="text-muted">on</span>
            <AddressLink addr={tx.to ?? tx.created_contract} maxW="max-w-[150px]" />
          </dd>
        </div>
        <div>
          <dt className="eyebrow mb-1">Transaction fee</dt>
          <dd>
            <NativeValue wei={tx.fee} full />
          </dd>
        </div>
        <div>
          <dt className="eyebrow mb-1">Gas info</dt>
          <dd className="tabular-nums">
            {tx.gas_used === null ? "Not used yet" : formatInt(tx.gas_used)} gas used from {formatInt(tx.gas)} limit
            {tx.gas_price ? <span className="block text-xs text-muted">@ {formatGwei(tx.gas_price)}</span> : null}
          </dd>
        </div>
        <div>
          <dt className="eyebrow mb-1">Nonce</dt>
          <dd className="tabular-nums">
            {tx.nonce} <span className="text-xs text-muted">(in the position {tx.tx_index ?? "?"})</span>
          </dd>
        </div>
      </dl>
      <Link
        to="/tx/$hash"
        params={{ hash }}
        className="link mt-4 inline-flex items-center gap-1 text-[13px] font-semibold"
      >
        See more details <ArrowRight className="h-3.5 w-3.5" />
      </Link>
    </div>
  );
}

/** The eye button at the start of a transaction row. */
export function TxPreview({ hash }: { hash: string }) {
  return (
    <Popover
      label="Transaction preview"
      place="bottom-start"
      trigger={<Eye className="h-3.5 w-3.5" />}
      triggerClassName="inline-flex h-7 w-7 items-center justify-center rounded-full border border-line bg-card text-muted transition-colors hover:border-line-strong hover:text-fg active:scale-95"
    >
      <TxPreviewBody hash={hash} />
    </Popover>
  );
}

function QrSvg({ text }: { text: string }) {
  const path = useMemo(() => {
    const { data, size } = encode(text, { ecc: "M", border: 2 });
    let d = "";
    for (let y = 0; y < size; y++) {
      for (let x = 0; x < size; x++) if (data[y][x]) d += `M${x} ${y}h1v1h-1z`;
    }
    return { d, size };
  }, [text]);
  return (
    <svg viewBox={`0 0 ${path.size} ${path.size}`} className="h-52 w-52 rounded-xl bg-white" role="img" aria-label="QR code">
      <path d={path.d} fill="#0b0e11" />
    </svg>
  );
}

/** The QR button next to an address heading. */
export function AddressQR({ address }: { address: string }) {
  return (
    <Popover
      label="Address QR code"
      place="bottom-start"
      trigger={<QrCode className="h-3.5 w-3.5" />}
      triggerClassName="inline-flex shrink-0 items-center justify-center rounded-full p-1 text-dim transition-colors hover:bg-hover hover:text-fg active:scale-95"
    >
      <div className="flex w-[260px] flex-col items-center gap-3 p-4">
        <p className="self-start text-[13px] font-semibold text-fg">Address QR code</p>
        <QrSvg text={address} />
        <span className="flex w-full items-start gap-1">
          <span className="min-w-0 break-all text-center font-mono text-[11.5px] text-muted">{address}</span>
          <CopyButton value={address} label="Copy address" />
        </span>
      </div>
    </Popover>
  );
}

/**
 * Building blocks shared by every page: cards, badges, skeletons, empty and
 * error states, pill tabs, pagination, copy buttons and detail rows. Mostly
 * text, like Etherscan; icons only where they carry meaning (copy, status).
 */
import { useState, type CSSProperties, type ReactNode } from "react";
import {
  AlertTriangle,
  Check,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Clock,
  Copy,
  RotateCw,
  XCircle,
} from "lucide-react";
import { toast } from "sonner";

import { ApiError } from "@/lib/api";
import { cx } from "@/lib/cx";
import { formatInt } from "@/lib/format";
import type { TxStatus } from "@/lib/types";
import { HelpTip } from "./overlay";

export { cx };

/* Card ---------------------------------------------------------------------- */

export function Card({ children, className }: { children: ReactNode; className?: string }) {
  return <section className={cx("card", className)}>{children}</section>;
}

export function CardHeader({
  title,
  right,
  className,
}: {
  title: ReactNode;
  /** Kept for callers from before the icon cleanup; not drawn. */
  icon?: ReactNode;
  right?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cx("flex items-center justify-between gap-3 border-b border-line px-4 py-3.5 sm:px-5", className)}>
      <h2 className="min-w-0 truncate text-[15px] font-semibold text-fg">{title}</h2>
      {right ? <div className="shrink-0">{right}</div> : null}
    </div>
  );
}

/* Page title ------------------------------------------------------------------ */

/** Etherscan's page heading: a title, the subject inline, then a rule. */
export function PageTitle({
  title,
  subtitle,
  right,
  badges,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  /** Kept for callers from before the icon cleanup; not drawn. */
  icon?: ReactNode;
  right?: ReactNode;
  badges?: ReactNode;
}) {
  return (
    <div className="mb-5 border-b border-line pb-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <h1 className="min-w-0 break-words text-lg font-semibold tracking-tight text-bright sm:text-xl">{title}</h1>
            {badges}
          </div>
          {subtitle ? <div className="mt-1.5 min-w-0 text-sm text-muted">{subtitle}</div> : null}
        </div>
        {right ? <div className="shrink-0">{right}</div> : null}
      </div>
    </div>
  );
}

/* Skeleton ---------------------------------------------------------------- */

export function Skeleton({ className, style }: { className?: string; style?: CSSProperties }) {
  return <span aria-hidden className={cx("skeleton", className ?? "h-4 w-full")} style={style} />;
}

export function TableSkeleton({ rows = 8, cols = 6 }: { rows?: number; cols?: number }) {
  return (
    <div className="table-wrap" aria-busy="true" aria-label="Loading">
      <table className="data-table">
        <thead>
          <tr>
            {Array.from({ length: cols }).map((_, i) => (
              <th key={i}>
                <Skeleton className="h-3 w-16" />
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {Array.from({ length: rows }).map((_, r) => (
            <tr key={r}>
              {Array.from({ length: cols }).map((_, c) => (
                <td key={c}>
                  <Skeleton className="h-3.5" style={{ width: `${55 + ((r * 7 + c * 13) % 40)}%`, minWidth: 44 }} />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function DetailSkeleton({ rows = 8 }: { rows?: number }) {
  return (
    <div className="divide-y divide-line" aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex flex-col gap-2 px-4 py-3.5 sm:flex-row sm:items-center sm:px-5">
          <Skeleton className="h-3 w-32 shrink-0 sm:w-52" />
          <Skeleton className="h-3.5" style={{ width: `${40 + ((i * 17) % 50)}%` }} />
        </div>
      ))}
    </div>
  );
}

/* Badges ------------------------------------------------------------------ */

type Tone = "ok" | "bad" | "warn" | "info" | "gold" | "neutral";

const TONES: Record<Tone, string> = {
  ok: "bg-ok/10 text-ok border-ok/30",
  bad: "bg-bad/10 text-bad border-bad/30",
  warn: "bg-warn/10 text-warn border-warn/30",
  info: "bg-info/10 text-info border-info/30",
  gold: "bg-gold/12 text-link border-gold/35",
  neutral: "bg-raised text-fg2 border-line",
};

export function Badge({
  tone = "neutral",
  children,
  className,
  title,
}: {
  tone?: Tone;
  children: ReactNode;
  className?: string;
  title?: string;
}) {
  return (
    <span
      title={title}
      className={cx(
        "inline-flex max-w-full items-center gap-1 whitespace-nowrap rounded-full border px-2 py-0.5 text-[11px] font-semibold leading-4",
        TONES[tone],
        className,
      )}
    >
      {children}
    </span>
  );
}

export function StatusBadge({ status, error }: { status: TxStatus; error?: string | null }) {
  if (status === "ok")
    return (
      <Badge tone="ok">
        <CheckCircle2 className="h-3 w-3" /> Success
      </Badge>
    );
  if (status === "failed")
    return (
      <Badge tone="bad" title={error ?? undefined}>
        <XCircle className="h-3 w-3" /> Failed
      </Badge>
    );
  return (
    <Badge tone="warn">
      <Clock className="h-3 w-3" /> Pending
    </Badge>
  );
}

/** A compact status mark for table rows: nothing for success. */
export function StatusIcon({ status, error }: { status: TxStatus; error?: string | null }) {
  if (status === "failed")
    return (
      <span title={error ? `Failed: ${error}` : "Failed"} className="inline-flex text-bad">
        <XCircle className="h-3.5 w-3.5" aria-label="Failed" />
      </span>
    );
  if (status === "pending")
    return (
      <span title="Pending" className="inline-flex text-warn">
        <Clock className="h-3.5 w-3.5" aria-label="Pending" />
      </span>
    );
  return null;
}

/** Etherscan's method pill. */
export function MethodBadge({ method }: { method: string | null }) {
  const label = method ?? "Transfer";
  return (
    <span
      title={label}
      className="inline-block w-[104px] truncate rounded-full border border-line bg-raised px-2 py-0.5 text-center align-middle text-[11px] font-medium text-fg2"
    >
      {label.startsWith("0x") ? label : label.charAt(0).toUpperCase() + label.slice(1)}
    </span>
  );
}

/* States -------------------------------------------------------------------- */

export function EmptyState({ title, children }: { title: string; children?: ReactNode; icon?: ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-1.5 px-6 py-12 text-center">
      <p className="text-sm font-semibold text-fg">{title}</p>
      {children ? <div className="max-w-md text-sm text-muted">{children}</div> : null}
    </div>
  );
}

export function ErrorState({
  error,
  onRetry,
  notFoundTitle,
  notFoundText,
}: {
  error: unknown;
  onRetry?: () => void;
  notFoundTitle?: string;
  notFoundText?: ReactNode;
}) {
  const api = error instanceof ApiError ? error : null;
  if (api?.notFound) {
    return <EmptyState title={notFoundTitle ?? "Not found"}>{notFoundText ?? api.message}</EmptyState>;
  }
  const limited = api?.status === 429;
  const message = (error as Error)?.message || "Something went wrong.";
  return (
    <div role="alert" className="flex flex-col items-center justify-center gap-1.5 px-6 py-12 text-center">
      <AlertTriangle className="mb-1 h-5 w-5 text-bad" aria-hidden />
      <p className="text-sm font-semibold text-fg">{limited ? "Slow down a little" : "Could not load this"}</p>
      <p className="max-w-md text-sm text-muted">
        {limited
          ? `Too many requests from your connection.${api?.retryAfter ? ` Try again in ${api.retryAfter} seconds.` : " Try again shortly."}`
          : message}
      </p>
      {onRetry ? (
        <button type="button" className="btn btn-sm mt-2" onClick={onRetry}>
          <RotateCw className="h-3.5 w-3.5" /> Try again
        </button>
      ) : null}
    </div>
  );
}

export function Spinner({ className }: { className?: string }) {
  return (
    <span
      aria-hidden
      className={cx("inline-block h-4 w-4 animate-spin rounded-full border-2 border-current border-t-transparent", className)}
    />
  );
}

/* Copy ---------------------------------------------------------------------- */

async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    /* fall through to the textarea route */
  }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}

export function CopyButton({
  value,
  label = "Copy",
  className,
  size = "sm",
}: {
  value: string;
  label?: string;
  className?: string;
  size?: "sm" | "md";
}) {
  const [done, setDone] = useState(false);
  const icon = size === "sm" ? "h-3.5 w-3.5" : "h-4 w-4";
  return (
    <button
      type="button"
      title={done ? "Copied" : label}
      aria-label={label}
      onClick={async (e) => {
        e.preventDefault();
        e.stopPropagation();
        const ok = await copyText(value);
        if (ok) {
          setDone(true);
          toast.success("Copied to clipboard");
          setTimeout(() => setDone(false), 1500);
        } else {
          toast.error("Could not copy. Select the text and copy it by hand.");
        }
      }}
      className={cx(
        "inline-flex shrink-0 items-center justify-center rounded-full p-1 text-dim transition-colors hover:bg-hover hover:text-fg active:scale-95",
        className,
      )}
    >
      {done ? <Check className={cx(icon, "text-ok")} /> : <Copy className={icon} />}
    </button>
  );
}

/* Tabs ---------------------------------------------------------------------- */

export interface TabItem {
  id: string;
  label: ReactNode;
  count?: number | null;
  hidden?: boolean;
}

/**
 * Holds the page at its current height while a new tab's content loads.
 * A tab that is still loading renders short, the document shrinks, and the
 * browser pulls the scroll position back to the top. The hold is released as
 * soon as the page is tall enough again (or after 5 s).
 */
let heightHold: number | undefined;
function holdPageHeight() {
  const root = document.documentElement;
  window.clearTimeout(heightHold);
  root.style.minHeight = `${root.scrollHeight}px`;
  const started = Date.now();
  const release = () => {
    root.style.minHeight = "";
    const fits = root.scrollHeight >= window.scrollY + window.innerHeight;
    if (!fits && Date.now() - started < 5000) {
      root.style.minHeight = `${window.scrollY + window.innerHeight}px`;
      heightHold = window.setTimeout(release, 300);
    }
  };
  heightHold = window.setTimeout(release, 600);
}

/** Etherscan's tab row: pills, the active one filled. */
export function Tabs({ items, active, onChange }: { items: TabItem[]; active: string; onChange: (id: string) => void }) {
  return (
    <div className="-mx-4 mb-4 overflow-x-auto px-4 pb-1 sm:mx-0 sm:px-0">
      <div role="tablist" className="flex w-max gap-2">
        {items
          .filter((t) => !t.hidden)
          .map((t) => {
            const on = t.id === active;
            return (
              <button
                key={t.id}
                type="button"
                role="tab"
                aria-selected={on}
                onClick={() => {
                  if (!on) holdPageHeight();
                  onChange(t.id);
                }}
                className="pill-tab"
              >
                {t.label}
                {t.count !== undefined && t.count !== null ? (
                  <span className={cx("text-[11px] font-semibold tabular-nums", on ? "opacity-80" : "text-dim")}>
                    {t.count > 9999 ? "9,999+" : formatInt(t.count)}
                  </span>
                ) : null}
              </button>
            );
          })}
      </div>
    </div>
  );
}

/* Pagination ------------------------------------------------------------------ */

export function Pagination({
  page,
  limit,
  total,
  hasNext,
  onPage,
  loading,
}: {
  page: number;
  limit: number;
  total: number;
  hasNext: boolean;
  onPage: (p: number) => void;
  loading?: boolean;
}) {
  const last = Math.max(1, Math.ceil(total / limit));
  if (total <= limit && page === 1) return null;
  return (
    <div className="flex flex-wrap items-center justify-end gap-1.5 border-t border-line px-4 py-3 sm:px-5">
      {loading ? <Spinner className="mr-2 h-3.5 w-3.5 text-gold" /> : null}
      <button type="button" className="btn btn-sm" disabled={page <= 1} onClick={() => onPage(1)}>
        First
      </button>
      <button type="button" className="btn btn-sm btn-icon" disabled={page <= 1} onClick={() => onPage(page - 1)} aria-label="Previous page">
        <ChevronLeft className="h-3.5 w-3.5" />
      </button>
      <span className="rounded-full border border-line bg-raised px-3 py-1 text-xs font-medium text-fg2">
        Page {formatInt(page)} of {formatInt(last)}
      </span>
      <button type="button" className="btn btn-sm btn-icon" disabled={!hasNext} onClick={() => onPage(page + 1)} aria-label="Next page">
        <ChevronRight className="h-3.5 w-3.5" />
      </button>
      <button type="button" className="btn btn-sm" disabled={page >= last} onClick={() => onPage(last)}>
        Last
      </button>
    </div>
  );
}

/** "A total of 4,612 transactions found" line above a table, like Etherscan. */
export function ListSummary({ children, right }: { children: ReactNode; right?: ReactNode }) {
  return (
    <div className="flex flex-col gap-2 border-b border-line px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5">
      <p className="text-[13px] text-fg">{children}</p>
      {right ? <div className="shrink-0">{right}</div> : null}
    </div>
  );
}

/* Detail rows --------------------------------------------------------------- */

export function DetailList({ children }: { children: ReactNode }) {
  return <dl className="px-4 py-2 sm:px-5">{children}</dl>;
}

/** A label row with Etherscan's "?" help mark. `divider` draws a rule above. */
export function DetailRow({
  label,
  children,
  hint,
  divider,
}: {
  label: string;
  children: ReactNode;
  hint?: string;
  divider?: boolean;
}) {
  return (
    <div
      className={cx(
        "grid grid-cols-1 gap-1 py-2.5 sm:grid-cols-[minmax(0,260px)_minmax(0,1fr)] sm:gap-4",
        divider && "mt-2 border-t border-line pt-4",
      )}
    >
      <dt className="flex items-center gap-1.5 text-[13.5px] text-muted">
        {hint ? <HelpTip text={hint} /> : null}
        <span>{label}:</span>
      </dt>
      <dd className="min-w-0 text-[13.5px] text-fg">{children}</dd>
    </div>
  );
}

/** A full hash or address that wraps on narrow screens, with a copy button. */
export function FullValue({ value, mono = true, children }: { value: string; mono?: boolean; children?: ReactNode }) {
  return (
    <span className="inline-flex max-w-full items-start gap-1">
      <span className={cx("min-w-0 break-all", mono && "font-mono text-[13px]")}>{children ?? value}</span>
      <CopyButton value={value} className="-mt-0.5" />
    </span>
  );
}

/** Uppercase label + value, for Overview / More Info cards. */
export function Stat({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <div className="min-w-0 py-2.5">
      <div className="eyebrow mb-1">{label}</div>
      <div className="min-w-0 text-[14px] text-fg">{children}</div>
    </div>
  );
}

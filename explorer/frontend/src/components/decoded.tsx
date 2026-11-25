/**
 * Decoded call inputs and event parameters (DecodedParam[] from the API, or
 * viem's decode output in the read-contract form). Laid out as rows that stack
 * on a phone, so long hex values wrap instead of scrolling sideways.
 */
import { isAddress } from "@/lib/format";
import type { DecodedParam } from "@/lib/types";
import { AddressHashLink } from "./links";
import { CopyButton } from "./ui";

/** JSON that keeps bigints exact (as decimal strings). */
export function stringify(v: unknown, space = 2): string {
  return JSON.stringify(v, (_k, x) => (typeof x === "bigint" ? x.toString() : x), space) ?? String(v);
}

export function ParamValue({ type, value }: { type: string; value: unknown }) {
  if (value === null || value === undefined) return <span className="text-dim">null</span>;
  if (type === "address" && typeof value === "string" && isAddress(value)) {
    return <AddressHashLink hash={value} full copy />;
  }
  if (type === "bool" || typeof value === "boolean") {
    return <span className="font-mono">{String(value)}</span>;
  }
  if (typeof value === "string" || typeof value === "number" || typeof value === "bigint") {
    const s = String(value);
    return (
      <span className="flex min-w-0 items-start gap-1">
        <span className="min-w-0 break-all font-mono">{s}</span>
        {s.length > 20 ? <CopyButton value={s} className="-mt-0.5" /> : null}
      </span>
    );
  }
  const json = stringify(value);
  return <pre className="code-block max-h-56 min-w-0 whitespace-pre-wrap break-all px-2.5 py-1.5">{json}</pre>;
}

export function DecodedParams({ params, showIndexed = false }: { params: DecodedParam[]; showIndexed?: boolean }) {
  if (params.length === 0) return <p className="text-[13px] text-dim">No parameters</p>;
  return (
    <div className="overflow-hidden rounded-lg border border-line text-[12.5px]">
      <div className="hidden grid-cols-[160px_120px_minmax(0,1fr)] gap-3 border-b border-line bg-raised px-3 py-2 text-xs font-semibold text-muted sm:grid">
        <span>Name</span>
        <span>Type</span>
        <span>Value</span>
      </div>
      <ul className="divide-y divide-line">
        {params.map((p, i) => (
          <li
            key={`${p.name}:${i}`}
            className="grid grid-cols-1 gap-1 px-3 py-2.5 sm:grid-cols-[160px_120px_minmax(0,1fr)] sm:gap-3"
          >
            <span className="min-w-0 break-all font-mono text-fg">
              {p.name || <span className="text-dim">{i}</span>}
              <span className="ml-2 text-dim sm:hidden">
                {p.type}
                {showIndexed && p.indexed ? ", indexed" : ""}
              </span>
            </span>
            <span className="hidden min-w-0 break-all font-mono text-muted sm:block">
              {p.type}
              {showIndexed && p.indexed ? <span className="block text-[11px] text-dim">indexed</span> : null}
            </span>
            <span className="min-w-0">
              <ParamValue type={p.type} value={p.value} />
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

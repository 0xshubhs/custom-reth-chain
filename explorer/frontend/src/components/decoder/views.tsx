/**
 * Renderers for decoded calls, events and parameter trees, in the spirit of
 * ETH.sh's decodedParams components (AddressParam, UintParam, BytesParam,
 * TupleParam, ArrayParam, StringParam; MIT, see src/lib/decoder/LICENSE):
 * every value readable as text, with small switches to read it another way.
 *
 *   uint/int   raw, or scaled by 10^18 / 10^9 / 10^6, or as a date when it
 *              looks like a unix time
 *   address    link to the address page, copy
 *   bytes      hex, UTF-8 text, a number (32 bytes or less), and the nested
 *              call when the bytes are calldata (decoded on demand)
 *   tuple      its components, expandable
 *   array      its items, expandable, long arrays shortened
 *
 * Only theme tokens are used for colour, so light, dim and dark all work.
 * The decoder itself (ethers, abi-guesser) is imported on demand only.
 */
import { useMemo, useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";

import type { DecodedCall, DecodedEvent, DecodedNode } from "@/lib/decoder/types";
import { looksLikeCalldata } from "@/lib/decoder/links";
import { CopyButton, cx } from "../ui";

/* Small pieces ---------------------------------------------------------------------- */

function Pill({
  on,
  onClick,
  children,
  title,
}: {
  on: boolean;
  onClick: () => void;
  children: ReactNode;
  title?: string;
}) {
  return (
    <button
      type="button"
      title={title}
      onClick={onClick}
      aria-pressed={on}
      className={cx(
        "rounded-full border px-2 py-px text-[11px] font-medium leading-4 transition-colors motion-reduce:transition-none",
        on ? "border-line bg-raised text-fg" : "border-transparent text-dim hover:text-fg2",
      )}
    >
      {children}
    </button>
  );
}

function Tag({ tone = "muted", children, title }: { tone?: "muted" | "warn" | "ok"; children: ReactNode; title?: string }) {
  return (
    <span
      title={title}
      className={cx(
        "inline-flex items-center rounded-full border border-line px-2 py-px text-[11px] font-medium leading-4",
        tone === "warn" ? "text-warn" : tone === "ok" ? "text-ok" : "text-muted",
      )}
    >
      {children}
    </span>
  );
}

function Mono({ children, className }: { children: ReactNode; className?: string }) {
  return <span className={cx("min-w-0 break-all font-mono text-[12.5px] text-fg", className)}>{children}</span>;
}

const ABOUT_SOURCE: Record<string, string> = {
  signature: "Decoded with a known function signature, not with this contract's verified ABI",
  guess: "Types inferred from the data's shape: names are unknown and the guess may be wrong",
  text: "The bytes read as UTF-8 text",
};

function SourceTag({ source }: { source: string }) {
  if (source === "abi") return null;
  const label = source === "guess" ? "Guess" : source === "text" ? "Text" : "From signature";
  return (
    <Tag tone={source === "guess" ? "warn" : "muted"} title={ABOUT_SOURCE[source]}>
      {label}
    </Tag>
  );
}

/* Number formatting (no library: exact bigint arithmetic) ------------------------------ */

function scaled(raw: string, decimals: number): string {
  let v: bigint;
  try {
    v = BigInt(raw);
  } catch {
    return raw;
  }
  const neg = v < 0n;
  const abs = neg ? -v : v;
  const base = 10n ** BigInt(decimals);
  const int = (abs / base).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  const frac = (abs % base).toString().padStart(decimals, "0").replace(/0+$/, "");
  return `${neg ? "-" : ""}${int}${frac ? `.${frac}` : ""}`;
}

/** Seconds between 2001 and 2100, the plausible range of a unix time. */
function asUnixTime(raw: string): number | null {
  try {
    const v = BigInt(raw);
    if (v >= 978307200n && v <= 4102444800n) return Number(v);
  } catch {
    /* not a number */
  }
  return null;
}

function hexToUtf8(hex: string): string | null {
  const h = hex.startsWith("0x") ? hex.slice(2) : hex;
  if (!h || h.length % 2) return null;
  const bytes = new Uint8Array(h.length / 2);
  for (let i = 0; i < bytes.length; i++) bytes[i] = Number.parseInt(h.slice(i * 2, i * 2 + 2), 16);
  try {
    const s = new TextDecoder("utf-8", { fatal: true }).decode(bytes).replace(/\0+$/, "");
    if (!s || /[\u0000-\u0008\u000e-\u001f\u007f]/.test(s)) return null;
    return s;
  } catch {
    return null;
  }
}

/* Leaf values ----------------------------------------------------------------------- */

export function AddressValue({ value }: { value: string }) {
  const ok = /^0x[0-9a-fA-F]{40}$/.test(value);
  return (
    <span className="inline-flex min-w-0 max-w-full items-start gap-1">
      {ok ? (
        <Link to="/address/$addr" params={{ addr: value }} className="min-w-0 break-all font-mono text-[12.5px] text-link hover:underline">
          {value}
        </Link>
      ) : (
        <Mono>{value}</Mono>
      )}
      <CopyButton value={value} label="Copy address" className="-mt-0.5" />
    </span>
  );
}

type Unit = "raw" | "ether" | "gwei" | "e6" | "date";

export function UintValue({ value }: { value: string }) {
  const [unit, setUnit] = useState<Unit>("raw");
  const ts = asUnixTime(value);
  const big = value.replace(/^-/, "").length > 6;
  let shown = value;
  if (unit === "ether") shown = scaled(value, 18);
  else if (unit === "gwei") shown = scaled(value, 9);
  else if (unit === "e6") shown = scaled(value, 6);
  else if (unit === "date" && ts !== null) {
    const d = new Date(ts * 1000);
    shown = `${d.toISOString().replace("T", " ").replace(/\.\d+Z$/, " UTC")} (${d.toLocaleString()})`;
  }
  return (
    <span className="flex min-w-0 flex-col gap-1">
      <span className="inline-flex min-w-0 items-start gap-1">
        <Mono>{shown}</Mono>
        {value.length > 15 ? <CopyButton value={value} label="Copy number" className="-mt-0.5" /> : null}
      </span>
      {big || ts !== null ? (
        <span className="flex flex-wrap items-center gap-0.5" role="group" aria-label="Read the number as">
          <Pill on={unit === "raw"} onClick={() => setUnit("raw")} title="The integer as stored">
            raw
          </Pill>
          {big ? (
            <>
              <Pill on={unit === "ether"} onClick={() => setUnit("ether")} title="Divided by 10^18 (wei to ether, ETH)">
                ether
              </Pill>
              <Pill on={unit === "gwei"} onClick={() => setUnit("gwei")} title="Divided by 10^9">
                gwei
              </Pill>
              <Pill on={unit === "e6"} onClick={() => setUnit("e6")} title="Divided by 10^6">
                10^6
              </Pill>
            </>
          ) : null}
          {ts !== null ? (
            <Pill on={unit === "date"} onClick={() => setUnit("date")} title="Seconds since 1970 as a date">
              date
            </Pill>
          ) : null}
        </span>
      ) : null}
    </span>
  );
}

function LongText({ text, mono = true, limit = 200 }: { text: string; mono?: boolean; limit?: number }) {
  const [all, setAll] = useState(false);
  const cut = !all && text.length > limit;
  return (
    <span className="inline-flex min-w-0 max-w-full flex-col items-start gap-1">
      <span className="inline-flex min-w-0 max-w-full items-start gap-1">
        <span className={cx("min-w-0 break-all text-[12.5px] text-fg", mono && "font-mono", !mono && "whitespace-pre-wrap")}>
          {cut ? `${text.slice(0, limit)}…` : text}
        </span>
        <CopyButton value={text} label="Copy value" className="-mt-0.5" />
      </span>
      {text.length > limit ? (
        <button type="button" onClick={() => setAll((a) => !a)} className="text-[11px] font-medium text-link hover:underline">
          {all ? "Show less" : `Show all (${text.length.toLocaleString()} characters)`}
        </button>
      ) : null}
    </span>
  );
}

type BytesView = "hex" | "text" | "number" | "decoded";

/**
 * A bytes value. With `nested` (already decoded) the decoded call is shown
 * first; otherwise, when the bytes look like calldata, it can be decoded on
 * demand with our decoder.
 */
export function BytesValue({ value, nested }: { value: string; nested?: DecodedCall | null }) {
  const text = useMemo(() => hexToUtf8(value), [value]);
  const byteLen = Math.max(0, (value.length - 2) / 2);
  // undefined: not tried yet, so offer to decode; null: the decoder found nothing.
  const canDecode = nested === undefined && looksLikeCalldata(value);
  const [view, setView] = useState<BytesView>(nested ? "decoded" : "hex");
  const [lazy, setLazy] = useState<DecodedCall | null | "loading" | "none">(nested ?? "none");

  async function decodeNow() {
    setView("decoded");
    if (lazy !== "none") return;
    setLazy("loading");
    try {
      const { decodeCalldata } = await import("@/lib/decoder");
      setLazy((await decodeCalldata({ calldata: value })) ?? null);
    } catch {
      setLazy(null);
    }
  }

  if (value === "0x" || byteLen === 0) return <Mono className="text-dim">0x (empty)</Mono>;

  const decoded = nested ?? (lazy !== "none" && lazy !== "loading" ? lazy : null);
  let number: string | null = null;
  if (byteLen <= 32) {
    try {
      number = BigInt(value).toString();
    } catch {
      number = null;
    }
  }

  return (
    <span className="flex min-w-0 flex-col gap-1.5">
      <span className="flex flex-wrap items-center gap-0.5" role="group" aria-label="Read the bytes as">
        {nested || canDecode ? (
          <Pill on={view === "decoded"} onClick={() => void decodeNow()} title="Decode the bytes as a function call">
            decoded
          </Pill>
        ) : null}
        <Pill on={view === "hex"} onClick={() => setView("hex")}>
          hex
        </Pill>
        {text ? (
          <Pill on={view === "text"} onClick={() => setView("text")}>
            text
          </Pill>
        ) : null}
        {number !== null ? (
          <Pill on={view === "number"} onClick={() => setView("number")}>
            number
          </Pill>
        ) : null}
        <span className="ml-1 text-[11px] text-dim">{byteLen.toLocaleString()} bytes</span>
      </span>
      {view === "decoded" ? (
        lazy === "loading" ? (
          <span className="text-[12px] text-muted">Decoding…</span>
        ) : decoded ? (
          <span className="block min-w-0 rounded-lg border border-line bg-raised/40 p-2.5">
            <DecodedCallView call={decoded} nested />
          </span>
        ) : (
          <span className="text-[12px] text-muted">These bytes do not decode as a known call.</span>
        )
      ) : view === "text" && text ? (
        <LongText text={text} mono={false} />
      ) : view === "number" && number !== null ? (
        <Mono>{number}</Mono>
      ) : (
        <LongText text={value} />
      )}
    </span>
  );
}

/** One leaf value, by its Solidity type. */
export function ParamValueView({
  type,
  value,
  nested,
  hashed,
}: {
  type: string;
  value: unknown;
  nested?: DecodedCall | null;
  hashed?: boolean;
}) {
  if (value === null || value === undefined) return <Mono className="text-dim">null</Mono>;
  const s = typeof value === "bigint" ? value.toString() : typeof value === "object" ? JSON.stringify(value) : String(value);
  if (hashed) {
    return (
      <span className="flex min-w-0 flex-col gap-0.5">
        <LongText text={s} />
        <span className="text-[11px] text-dim">Indexed {type}: the topic holds the hash of the value, not the value.</span>
      </span>
    );
  }
  if (type === "address") return <AddressValue value={s} />;
  if (type === "bool") return <Mono>{s}</Mono>;
  if (/^u?int\d*$/.test(type)) return <UintValue value={s} />;
  if (type === "bytes") return <BytesValue value={s} nested={nested} />;
  if (/^bytes\d+$/.test(type)) {
    // A bytes32 holding a left-padded address reads better as one.
    // (Not when the 20 bytes themselves start with zeros: that is a small number or an id.)
    if (/^0x0{24}[0-9a-fA-F]{40}$/.test(s) && !/^0x0{32}/.test(s)) {
      return (
        <span className="flex min-w-0 flex-col gap-0.5">
          <LongText text={s} />
          <span className="text-[11px] text-dim">
            As an address: <AddressValue value={`0x${s.slice(26)}`} />
          </span>
        </span>
      );
    }
    return <BytesValue value={s} nested={null} />;
  }
  if (type === "string") return <LongText text={s} mono={false} limit={400} />;
  return <LongText text={s} />;
}

/* Trees -------------------------------------------------------------------------------- */

const SHOW_ITEMS = 5;

/** One decoded node: a leaf value, or an expandable tuple / array. */
export function NodeView({ node, label, depth = 0 }: { node: DecodedNode; label?: string; depth?: number }) {
  const isGroup = node.baseType === "tuple" || node.baseType === "array";
  const [open, setOpen] = useState(depth < 2);
  const [all, setAll] = useState(false);
  const name = node.name || label || "";
  const children = node.children ?? [];
  const shown = all ? children : children.slice(0, SHOW_ITEMS);

  const head = (
    <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
      {name ? <span className="font-mono text-[12.5px] font-medium text-fg">{name}</span> : null}
      <span className="break-all font-mono text-[11.5px] text-dim">{node.type}</span>
      {node.indexed ? <Tag>indexed</Tag> : null}
      {isGroup ? (
        <span className="text-[11px] text-dim">
          {node.baseType === "array" ? `${children.length} ${children.length === 1 ? "item" : "items"}` : `${children.length} fields`}
        </span>
      ) : null}
    </span>
  );

  if (!isGroup) {
    return (
      <div className="grid min-w-0 grid-cols-1 gap-1 py-2 @[30rem]:grid-cols-[minmax(0,10rem)_minmax(0,1fr)] @[30rem]:gap-3">
        {head}
        <div className="min-w-0">
          <ParamValueView type={node.type} value={node.value} nested={node.nested} hashed={node.hashed} />
        </div>
      </div>
    );
  }

  return (
    <div className="min-w-0 py-2">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full min-w-0 items-baseline gap-2 text-left"
      >
        <span aria-hidden className={cx("inline-block w-3 shrink-0 text-dim transition-transform motion-reduce:transition-none", open && "rotate-90")}>
          ›
        </span>
        {head}
      </button>
      {open ? (
        <div className="ml-1.5 mt-1 border-l border-line pl-3">
          {children.length === 0 ? <p className="py-1 text-[12px] text-dim">Empty</p> : null}
          <div className="@container divide-y divide-line">
            {shown.map((c, i) => (
              <NodeView
                key={i}
                node={c}
                label={node.baseType === "array" ? `[${i}]` : `${i}`}
                depth={depth + 1}
              />
            ))}
          </div>
          {children.length > SHOW_ITEMS ? (
            <button type="button" onClick={() => setAll((a) => !a)} className="mt-1 text-[11px] font-medium text-link hover:underline">
              {all ? "Show fewer" : `Show all ${children.length}`}
            </button>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

/* Generic params (API DecodedParam values, viem decode output) -------------------------------- */

export interface ParamComponent {
  name?: string;
  type: string;
  components?: readonly ParamComponent[];
}

export interface ParamInput {
  name: string;
  type: string;
  value: unknown;
  components?: readonly ParamComponent[];
  indexed?: boolean;
}

/** "(address,uint256,(bool,bytes))" or "tuple(address,uint256)" -> top-level component types. */
function splitTupleType(type: string): string[] | null {
  const m = /^(?:tuple)?\((.*)\)$/.exec(type);
  if (!m) return null;
  const out: string[] = [];
  let depth = 0;
  let cur = "";
  for (const ch of m[1]) {
    if (ch === "(") depth++;
    if (ch === ")") depth--;
    if (ch === "," && depth === 0) {
      out.push(cur.trim());
      cur = "";
    } else cur += ch;
  }
  if (cur.trim()) out.push(cur.trim());
  return out;
}

function baseTypeOf(type: string): string {
  if (/\[\d*\]$/.test(type)) return "array";
  if (type.startsWith("tuple") || type.startsWith("(")) return "tuple";
  return type;
}

function leafValue(v: unknown): string | boolean | null {
  if (v === null || v === undefined) return null;
  if (typeof v === "boolean") return v;
  if (typeof v === "bigint" || typeof v === "number") return v.toString();
  if (typeof v === "string") return v;
  try {
    return JSON.stringify(v, (_k, x) => (typeof x === "bigint" ? x.toString() : x));
  } catch {
    return String(v);
  }
}

/** Any (type, value) pair into a DecodedNode tree. */
export function toNode(
  name: string,
  type: string,
  value: unknown,
  components?: readonly ParamComponent[],
  indexed?: boolean,
): DecodedNode {
  const arr = /^(.*)\[(\d*)\]$/.exec(type);
  if (arr) {
    const list = Array.isArray(value) ? value : value && typeof value === "object" ? Object.values(value) : [];
    return {
      name,
      type,
      baseType: "array",
      value: null,
      indexed,
      children: list.map((v) => toNode("", arr[1], v, components)),
    };
  }
  if (type.startsWith("tuple") || type.startsWith("(")) {
    const comps: ParamComponent[] = components?.length
      ? [...components]
      : (splitTupleType(type) ?? []).map((t) => ({ type: t }));
    const byIndex = Array.isArray(value);
    const obj = (value ?? {}) as Record<string, unknown>;
    const keys = !byIndex && value && typeof value === "object" ? Object.keys(obj) : [];
    const list: ParamComponent[] =
      comps.length || byIndex ? comps : keys.map((k) => ({ name: k, type: "string" }));
    return {
      name,
      type,
      baseType: "tuple",
      value: null,
      indexed,
      children: list.map((c, i) => {
        const v = byIndex ? (value as unknown[])[i] : c.name && c.name in obj ? obj[c.name] : obj[keys[i]];
        return toNode(c.name ?? "", c.type, v, c.components);
      }),
    };
  }
  return { name, type, baseType: baseTypeOf(type), value: leafValue(value), indexed };
}

/** A list of named, typed values (an API DecodedParam[] or read-contract outputs). */
export function ParamsTable({ params, empty = "No parameters" }: { params: readonly ParamInput[]; empty?: string }) {
  const nodes = useMemo(() => params.map((p) => toNode(p.name, p.type, p.value, p.components, p.indexed)), [params]);
  if (!nodes.length) return <p className="text-[13px] text-dim">{empty}</p>;
  return (
    <div className="@container min-w-0 divide-y divide-line">
      {nodes.map((n, i) => (
        <NodeView key={i} node={n} label={`${i}`} />
      ))}
    </div>
  );
}

/* Calls and events ---------------------------------------------------------------------- */

export function DecodedCallView({ call, nested = false }: { call: DecodedCall; nested?: boolean }) {
  const title = call.functionName || call.label || (call.source === "guess" ? "Unknown function" : "Data");
  return (
    <div className="min-w-0">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <span className={cx("break-all font-mono font-semibold text-fg", nested ? "text-[13px]" : "text-[14px]")}>{title}</span>
        {call.selector ? <Tag>{call.selector}</Tag> : null}
        <SourceTag source={call.source} />
      </div>
      {call.label && call.functionName ? <p className="mt-0.5 text-[12px] text-muted">{call.label}</p> : null}
      <p className="mt-0.5 break-all font-mono text-[11.5px] text-dim">{call.signature}</p>
      {call.params.length ? (
        <div className="@container mt-2 min-w-0 divide-y divide-line">
          {call.params.map((p, i) => (
            <NodeView key={i} node={p} label={`${i}`} />
          ))}
        </div>
      ) : (
        <p className="mt-2 text-[12px] text-dim">No parameters</p>
      )}
    </div>
  );
}

export function DecodedEventView({ event }: { event: DecodedEvent }) {
  return (
    <div className="min-w-0">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <span className="break-all font-mono text-[14px] font-semibold text-fg">{event.eventName}</span>
        <SourceTag source={event.source} />
      </div>
      <p className="mt-0.5 break-all font-mono text-[11.5px] text-dim">{event.signature}</p>
      {event.params.length ? (
        <div className="@container mt-2 min-w-0 divide-y divide-line">
          {event.params.map((p, i) => (
            <NodeView key={i} node={p} label={`${i}`} />
          ))}
        </div>
      ) : (
        <p className="mt-2 text-[12px] text-dim">No parameters</p>
      )}
    </div>
  );
}

/**
 * Read-contract form: one entry per view/pure function of the ABI. Inputs are
 * ABI-encoded with viem in the browser, the engine runs the eth_call
 * (POST /api/v2/smart-contracts/{addr}/call) and the answer is decoded here.
 * The call always goes to the page's address, so reading a proxy through its
 * implementation ABI works the same way.
 */
import { useMemo, useState } from "react";
import { ChevronDown, Play } from "lucide-react";
import { decodeFunctionResult, encodeFunctionData, type Abi, type AbiFunction, type AbiParameter } from "viem";

import { ApiError, apiPost } from "@/lib/api";
import type { CallResponse } from "@/lib/types";
import { ParamValue } from "../decoded";
import { EmptyState, Spinner, cx } from "../ui";

function isRead(f: AbiFunction): boolean {
  return (
    f.stateMutability === "view" || f.stateMutability === "pure" || (f as { constant?: boolean }).constant === true
  );
}

function sig(f: AbiFunction): string {
  return `${f.name}(${f.inputs.map((i) => i.type).join(",")})`;
}

/* Turn what the user typed into the value viem expects for that ABI type. */
function coerce(p: AbiParameter, v: unknown): unknown {
  const t = p.type;
  const arr = t.match(/^(.*)\[(\d*)\]$/);
  if (arr) {
    if (!Array.isArray(v)) throw new Error(`${p.name || t}: expected a JSON array, like ["a","b"]`);
    const inner = { ...p, type: arr[1] } as AbiParameter;
    return v.map((x) => coerce(inner, x));
  }
  if (t === "tuple") {
    const comps = (p as { components?: readonly AbiParameter[] }).components ?? [];
    if (Array.isArray(v)) return comps.map((c, i) => coerce(c, v[i]));
    if (v && typeof v === "object") {
      const o = v as Record<string, unknown>;
      return Object.fromEntries(comps.map((c, i) => [c.name ?? String(i), coerce(c, o[c.name ?? String(i)])]));
    }
    throw new Error(`${p.name || t}: expected a JSON object or array`);
  }
  if (/^u?int\d*$/.test(t)) {
    const s = String(v).trim();
    if (!/^-?\d+$/.test(s)) throw new Error(`${p.name || t}: expected a whole number`);
    return BigInt(s);
  }
  if (t === "bool") {
    if (v === true || v === "true" || v === "1") return true;
    if (v === false || v === "false" || v === "0") return false;
    throw new Error(`${p.name || t}: expected true or false`);
  }
  if (t === "address") {
    const s = String(v).trim();
    if (!/^0x[0-9a-fA-F]{40}$/.test(s)) throw new Error(`${p.name || t}: expected a 0x address (40 hex characters)`);
    return s;
  }
  if (t.startsWith("bytes")) {
    const s = String(v).trim();
    if (!/^0x([0-9a-fA-F]{2})*$/.test(s)) throw new Error(`${p.name || t}: expected 0x-prefixed hex bytes`);
    return s;
  }
  return String(v);
}

function parseInput(p: AbiParameter, raw: string): unknown {
  const t = p.type;
  if (t.endsWith("]") || t === "tuple") {
    let v: unknown;
    try {
      v = JSON.parse(raw);
    } catch {
      throw new Error(`${p.name || t}: not valid JSON`);
    }
    return coerce(p, v);
  }
  return coerce(p, raw);
}

function placeholder(p: AbiParameter): string {
  if (p.type.endsWith("]")) return `${p.name || p.type} (${p.type}, JSON array)`;
  if (p.type === "tuple") return `${p.name || "tuple"} (JSON object)`;
  return `${p.name || "value"} (${p.type})`;
}

type Outcome = { ok: true; values: { name: string; type: string; value: unknown }[] } | { ok: false; error: string };

function FunctionCard({ addr, fn, index }: { addr: string; fn: AbiFunction; index: number }) {
  const [open, setOpen] = useState(false);
  const [inputs, setInputs] = useState<string[]>(() => fn.inputs.map(() => ""));
  const [busy, setBusy] = useState(false);
  const [out, setOut] = useState<Outcome | null>(null);

  async function run() {
    setBusy(true);
    setOut(null);
    try {
      const args = fn.inputs.map((p, i) => parseInput(p, inputs[i]));
      const data = encodeFunctionData({ abi: [fn] as Abi, functionName: fn.name, args } as Parameters<
        typeof encodeFunctionData
      >[0]);
      const res = await apiPost<CallResponse>(`/smart-contracts/${addr}/call`, JSON.stringify({ data }));
      if ("error" in res) {
        setOut({ ok: false, error: res.error || "The call reverted." });
        return;
      }
      const decoded = decodeFunctionResult({
        abi: [fn] as Abi,
        functionName: fn.name,
        data: res.result as `0x${string}`,
      } as Parameters<typeof decodeFunctionResult>[0]);
      const list = fn.outputs.length <= 1 ? [decoded] : (decoded as readonly unknown[]);
      setOut({
        ok: true,
        values: fn.outputs.map((o, i) => ({ name: o.name ?? "", type: o.type, value: list[i] })),
      });
    } catch (e) {
      const msg =
        e instanceof ApiError
          ? e.status === 429
            ? `Too many calls from your connection.${e.retryAfter ? ` Try again in ${e.retryAfter} seconds.` : ""}`
            : e.message
          : (e as Error)?.message?.split("\n")[0] || "The call failed.";
      setOut({ ok: false, error: msg });
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="rounded-xl border border-line bg-card">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left"
      >
        <span className="min-w-0">
          <span className="mr-2 text-xs font-semibold text-dim">{index + 1}.</span>
          <span className="break-all font-mono text-[13px] font-semibold text-fg">{fn.name}</span>
          {fn.inputs.length ? (
            <span className="ml-1 break-all font-mono text-[12px] text-dim">
              ({fn.inputs.map((i) => `${i.type}${i.name ? ` ${i.name}` : ""}`).join(", ")})
            </span>
          ) : null}
        </span>
        <ChevronDown className={cx("h-4 w-4 shrink-0 text-dim transition-transform", open && "rotate-180")} />
      </button>
      {open ? (
        <form
          className="flex flex-col gap-3 border-t border-line px-4 py-4"
          onSubmit={(e) => {
            e.preventDefault();
            void run();
          }}
        >
          {fn.inputs.map((p, i) => (
            <label key={i} className="flex flex-col gap-1">
              <span className="font-mono text-[12px] text-muted">
                {p.name || `arg${i}`} <span className="text-dim">{p.type}</span>
              </span>
              <input
                className="input font-mono !text-[13px]"
                value={inputs[i]}
                placeholder={placeholder(p)}
                spellCheck={false}
                autoComplete="off"
                onChange={(e) => setInputs((v) => v.map((x, j) => (j === i ? e.target.value : x)))}
              />
            </label>
          ))}
          <div className="flex items-center gap-3">
            <button type="submit" className="btn btn-primary btn-sm" disabled={busy}>
              {busy ? <Spinner className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5" />} Query
            </button>
            <span className="text-xs text-dim">
              Returns {fn.outputs.length ? fn.outputs.map((o) => o.type).join(", ") : "nothing"}
            </span>
          </div>
          {out ? (
            out.ok ? (
              <div className="rounded-lg border border-line bg-raised/60 p-3">
                {out.values.length === 0 ? (
                  <p className="text-sm text-muted">The call succeeded and returned nothing.</p>
                ) : (
                  <ul className="flex flex-col gap-2">
                    {out.values.map((v, i) => (
                      <li
                        key={i}
                        className="grid gap-1 text-[13px] sm:grid-cols-[minmax(0,180px)_minmax(0,1fr)] sm:gap-3"
                      >
                        <span className="font-mono text-muted">
                          {v.name || `[${i}]`} <span className="text-dim">{v.type}</span>
                        </span>
                        <span className="min-w-0">
                          <ParamValue type={v.type} value={v.value} />
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            ) : (
              <p role="alert" className="rounded-lg border border-bad/30 bg-bad/10 px-3 py-2 text-[13px] text-bad">
                {out.error}
              </p>
            )
          ) : null}
        </form>
      ) : null}
    </li>
  );
}

export default function ReadContract({ addr, abi }: { addr: string; abi: unknown[] }) {
  const fns = useMemo(() => (abi as Abi).filter((x): x is AbiFunction => x.type === "function").filter(isRead), [abi]);
  const writes = useMemo(
    () => (abi as Abi).filter((x): x is AbiFunction => x.type === "function" && !isRead(x)).length,
    [abi],
  );
  if (fns.length === 0) {
    return <EmptyState title="Nothing to read">This ABI has no view or pure functions.</EmptyState>;
  }
  return (
    <div className="flex flex-col gap-3">
      <p className="text-sm text-muted">
        {fns.length} read {fns.length === 1 ? "function" : "functions"}. Each query is an{" "}
        <span className="font-mono">eth_call</span> at the latest block; nothing is signed or sent.
        {writes
          ? ` ${writes} write ${writes === 1 ? "function needs" : "functions need"} a wallet and are not offered here.`
          : ""}
      </p>
      <ul className="flex flex-col gap-2">
        {fns.map((f, i) => (
          <FunctionCard key={sig(f)} addr={addr} fn={f} index={i} />
        ))}
      </ul>
    </div>
  );
}

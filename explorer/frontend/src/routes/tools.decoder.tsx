/**
 * /tools/decoder: paste calldata (or a log's topics and data) and decode it
 * with the ETH.sh decoder port (src/lib/decoder). A contract address loads its
 * verified ABI from our API; a pasted ABI is used as is; otherwise signatures
 * come from our signature API, and anything still unknown is guessed.
 * Optional ?calldata=, ?address= and ?mode=event prefill the form.
 */
import { useState, type ReactNode } from "react";
import { createFileRoute } from "@tanstack/react-router";

import { DecodedCallView, DecodedEventView } from "@/components/decoder";
import { Card, CardHeader, PageTitle, cx } from "@/components/ui";
import { ethShCalldataUrl } from "@/lib/decoder/links";
import type { DecodedCall, DecodedEvent } from "@/lib/decoder/types";
import { pageHead } from "@/lib/seo";
import { NETWORK_NAME } from "@/lib/site";

type Mode = "calldata" | "event";

interface DecoderSearch {
  calldata?: string;
  address?: string;
  mode?: Mode;
}

const str = (v: unknown, max: number) => (typeof v === "string" && v.trim() ? v.trim().slice(0, max) : undefined);

export const Route = createFileRoute("/tools/decoder")({
  validateSearch: (s: Record<string, unknown>): DecoderSearch => ({
    calldata: str(s.calldata, 200_000),
    address: str(s.address, 42),
    mode: s.mode === "event" ? "event" : undefined,
  }),
  head: () =>
    pageHead(
      "Calldata decoder",
      `Decode transaction input and event logs on the ${NETWORK_NAME} chain: verified ABIs, known signatures, nested calls and batches.`,
    ),
  component: DecoderPage,
});

const HEX = /^0x([0-9a-fA-F]{2})*$/;
const ADDRESS = /^0x[0-9a-fA-F]{40}$/;
const TOPIC = /^0x[0-9a-fA-F]{64}$/;

function parseAbi(text: string): { abi: unknown[] | null; error: string | null } {
  if (!text.trim()) return { abi: null, error: null };
  try {
    const v = JSON.parse(text) as unknown;
    if (Array.isArray(v)) return { abi: v, error: null };
    if (v && typeof v === "object" && Array.isArray((v as { abi?: unknown }).abi)) {
      return { abi: (v as { abi: unknown[] }).abi, error: null };
    }
    return { abi: null, error: "The ABI must be a JSON array (or an object with an \"abi\" array)." };
  } catch {
    // One human-readable signature per line also works: "function transfer(address,uint256)".
    const lines = text
      .split("\n")
      .map((l) => l.trim())
      .filter(Boolean);
    if (lines.length && lines.every((l) => /^(function|event|error|constructor)\s/.test(l))) return { abi: lines, error: null };
    return { abi: null, error: "The ABI is not valid JSON." };
  }
}

const FIELD = "w-full rounded-xl border border-line bg-raised px-3 py-2 font-mono text-[13px] text-fg placeholder:text-dim";

function Field({ label, hint, children, htmlFor }: { label: string; hint?: string; children: ReactNode; htmlFor: string }) {
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={htmlFor} className="text-sm font-semibold text-fg">
        {label}
      </label>
      {children}
      {hint ? <p className="text-xs text-dim">{hint}</p> : null}
    </div>
  );
}

function DecoderPage() {
  const search = Route.useSearch();
  const [mode, setMode] = useState<Mode>(search.mode ?? "calldata");
  const [calldata, setCalldata] = useState(search.calldata ?? "");
  const [topics, setTopics] = useState("");
  const [data, setData] = useState("");
  const [address, setAddress] = useState(search.address ?? "");
  const [abiText, setAbiText] = useState("");
  const [showAbi, setShowAbi] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [call, setCall] = useState<DecodedCall | null>(null);
  const [event, setEvent] = useState<DecodedEvent | null>(null);
  const [nothing, setNothing] = useState(false);

  function reset() {
    setError(null);
    setCall(null);
    setEvent(null);
    setNothing(false);
  }

  async function decode() {
    reset();
    const addr = address.trim();
    if (addr && !ADDRESS.test(addr)) {
      setError("The contract address must be 0x followed by 40 hex characters.");
      return;
    }
    const { abi, error: abiError } = parseAbi(abiText);
    if (abiError) {
      setError(abiError);
      return;
    }
    setBusy(true);
    try {
      const lib = await import("@/lib/decoder");
      if (mode === "calldata") {
        const cd = calldata.trim().replace(/\s+/g, "");
        const hex = cd.startsWith("0x") ? cd : `0x${cd}`;
        if (hex === "0x" || !HEX.test(hex)) {
          setError("Calldata must be hex: 0x followed by an even number of hex characters.");
          return;
        }
        const r = await lib.decodeCalldata({ calldata: hex, abi, address: addr || null });
        if (r) setCall(r);
        else setNothing(true);
      } else {
        const list = topics
          .split(/[\s,]+/)
          .map((t) => t.trim())
          .filter(Boolean);
        if (!list.length || !list.every((t) => TOPIC.test(t))) {
          setError("Topics must be 0x followed by 64 hex characters, one per line; topic 0 is the event's id.");
          return;
        }
        const d = data.trim() || "0x";
        if (!HEX.test(d)) {
          setError("Data must be hex: 0x followed by an even number of hex characters.");
          return;
        }
        const r = await lib.decodeLog({ topics: list, data: d, abi, address: addr || null });
        if (r) setEvent(r);
        else setNothing(true);
      }
    } catch {
      setError("The decoder could not be loaded. Check your connection and try again.");
    } finally {
      setBusy(false);
    }
  }

  const cd = calldata.trim();

  return (
    <>
      <PageTitle
        title="Calldata decoder"
        subtitle="Decode transaction input or an event log. Uses the contract's verified ABI when there is one, then known signatures, then a best-effort guess."
      />

      <div className="mb-4 inline-flex items-center gap-1 rounded-full border border-line bg-raised p-1" role="tablist" aria-label="What to decode">
        {(
          [
            ["calldata", "Calldata"],
            ["event", "Event log"],
          ] as const
        ).map(([m, label]) => (
          <button
            key={m}
            type="button"
            role="tab"
            aria-selected={mode === m}
            onClick={() => {
              setMode(m);
              reset();
            }}
            className={cx(
              "rounded-full px-3.5 py-1.5 text-sm font-semibold transition-colors motion-reduce:transition-none",
              mode === m ? "bg-card text-fg shadow-sm" : "text-muted hover:text-fg",
            )}
          >
            {label}
          </button>
        ))}
      </div>

      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)]">
        <Card>
          <CardHeader title={mode === "calldata" ? "Input" : "Log"} />
          <form
            className="flex flex-col gap-4 p-4 sm:p-5"
            onSubmit={(e) => {
              e.preventDefault();
              void decode();
            }}
          >
            {mode === "calldata" ? (
              <Field label="Calldata" htmlFor="dec-calldata" hint="Transaction input or any ABI-encoded data, 0x...">
                <textarea
                  id="dec-calldata"
                  className={cx(FIELD, "min-h-40 resize-y")}
                  value={calldata}
                  onChange={(e) => setCalldata(e.target.value)}
                  placeholder="0xa9059cbb000000000000000000000000..."
                  spellCheck={false}
                  autoComplete="off"
                />
              </Field>
            ) : (
              <>
                <Field label="Topics" htmlFor="dec-topics" hint="One per line, topic 0 first.">
                  <textarea
                    id="dec-topics"
                    className={cx(FIELD, "min-h-28 resize-y")}
                    value={topics}
                    onChange={(e) => setTopics(e.target.value)}
                    placeholder="0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
                    spellCheck={false}
                    autoComplete="off"
                  />
                </Field>
                <Field label="Data" htmlFor="dec-data">
                  <textarea
                    id="dec-data"
                    className={cx(FIELD, "min-h-24 resize-y")}
                    value={data}
                    onChange={(e) => setData(e.target.value)}
                    placeholder="0x"
                    spellCheck={false}
                    autoComplete="off"
                  />
                </Field>
              </>
            )}

            <Field
              label="Contract address (optional)"
              htmlFor="dec-address"
              hint={mode === "calldata" ? "The contract that was called: its verified ABI is used." : "The contract that emitted the log."}
            >
              <input
                id="dec-address"
                className={FIELD}
                value={address}
                onChange={(e) => setAddress(e.target.value)}
                placeholder="0x..."
                spellCheck={false}
                autoComplete="off"
              />
            </Field>

            <div>
              <button
                type="button"
                onClick={() => setShowAbi((s) => !s)}
                aria-expanded={showAbi}
                className="text-sm font-semibold text-link hover:underline"
              >
                {showAbi ? "Hide ABI" : "Paste an ABI (optional)"}
              </button>
              {showAbi ? (
                <textarea
                  aria-label="ABI"
                  className={cx(FIELD, "mt-2 min-h-28 resize-y")}
                  value={abiText}
                  onChange={(e) => setAbiText(e.target.value)}
                  placeholder={'[{"type":"function","name":"transfer",...}]  or one signature per line: function transfer(address to, uint256 amount)'}
                  spellCheck={false}
                  autoComplete="off"
                />
              ) : null}
            </div>

            {error ? (
              <p role="alert" className="rounded-xl border border-line px-3 py-2 text-[13px] text-bad">
                {error}
              </p>
            ) : null}

            <div className="flex flex-wrap items-center gap-3">
              <button
                type="submit"
                disabled={busy}
                className="btn btn-primary"
              >
                {busy ? "Decoding…" : "Decode"}
              </button>
              {mode === "calldata" && HEX.test(cd.startsWith("0x") ? cd : `0x${cd}`) && cd.length > 2 ? (
                <a
                  href={ethShCalldataUrl(cd.startsWith("0x") ? cd : `0x${cd}`)}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-sm font-semibold text-link hover:underline"
                >
                  Open in ETH.sh {"↗"}
                </a>
              ) : null}
            </div>
          </form>
        </Card>

        <Card>
          <CardHeader title="Result" />
          <div className="p-4 sm:p-5">
            {call ? (
              <DecodedCallView call={call} />
            ) : event ? (
              <DecodedEventView event={event} />
            ) : nothing ? (
              <p className="text-sm text-muted">
                {mode === "calldata"
                  ? "Nothing fits: no verified ABI, no known signature, and the data does not look like ABI-encoded values or text."
                  : "Nothing fits: no verified ABI for the emitter and no known event with this topic 0."}
              </p>
            ) : (
              <p className="text-sm text-dim">The decoded result shows here.</p>
            )}
          </div>
        </Card>
      </div>
    </>
  );
}

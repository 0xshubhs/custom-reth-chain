/**
 * One event log in Etherscan's layout: Address, Name (the event signature
 * with its parameter names), Topics (each indexed parameter decoded next to
 * its raw topic) and Data (decoded values, or the raw hex). Decoding comes
 * from the API (verified ABIs and the common token events); for anything it
 * could not decode, the in-browser decoder (lib/decoder) tries our signature
 * list and then a guess.
 */
import { useState, type ReactNode } from "react";

import { cx } from "@/lib/cx";
import type { DecodedParam, Log } from "@/lib/types";
import { useClientDecodedLog } from "./useDecode";
import { ParamValue } from "./decoded";
import { AddressLink, BlockLink, TxLink } from "./links";
import { Badge, CopyButton } from "./ui";

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-1 gap-1 sm:grid-cols-[90px_minmax(0,1fr)] sm:gap-4">
      <span className="text-[13px] font-medium text-muted">{label}</span>
      <div className="min-w-0 text-[13px]">{children}</div>
    </div>
  );
}

function Signature({ event, params }: { event: string; params: DecodedParam[] }) {
  return (
    <span className="break-words font-mono text-[12.5px]">
      <span className="font-semibold text-fg">{event}</span>
      <span className="text-dim"> (</span>
      {params.map((p, i) => (
        <span key={i}>
          {i ? <span className="text-dim">, </span> : null}
          {p.indexed ? <span className="text-dim">index_topic_{indexOfTopic(params, i)} </span> : null}
          <span className="text-info">{p.type}</span> <span className="text-bad">{p.name || `arg${i}`}</span>
        </span>
      ))}
      <span className="text-dim">)</span>
    </span>
  );
}

function indexOfTopic(params: DecodedParam[], i: number) {
  let n = 0;
  for (let j = 0; j <= i; j++) if (params[j].indexed) n++;
  return n;
}

export function LogEntry({ log, showTx = false, current }: { log: Log; showTx?: boolean; current?: string }) {
  const client = useClientDecodedLog(log);
  const decoded = log.decoded ?? client?.decoded ?? null;
  const guess = !log.decoded && client?.source === "guess";
  const fromSignature = !log.decoded && client?.source === "signature";
  const [dataView, setDataView] = useState<"dec" | "hex">(decoded ? "dec" : "hex");
  const topics = log.topics.filter((t): t is string => t !== null);
  const indexed = decoded ? decoded.params.filter((p) => p.indexed) : [];
  const nonIndexed = decoded ? decoded.params.filter((p) => !p.indexed) : [];

  return (
    <div className="flex gap-4">
      <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-ok/10 text-[13px] font-semibold text-ok">
        {log.log_index}
      </span>
      <div className="flex min-w-0 flex-1 flex-col gap-3">
        <Row label="Address">
          <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <AddressLink addr={log.address} current={current} full copy />
            {showTx ? (
              <span className="inline-flex items-center gap-1.5 text-muted">
                in <TxLink hash={log.tx_hash} /> block <BlockLink n={log.block_number} />
              </span>
            ) : null}
          </span>
        </Row>
        {decoded ? (
          <Row label="Name">
            <span className="flex flex-wrap items-center gap-2">
              <Signature event={decoded.event} params={decoded.params} />
              {guess ? <Badge tone="warn">Guess</Badge> : fromSignature ? <Badge tone="info">From signature</Badge> : null}
            </span>
          </Row>
        ) : null}
        <Row label="Topics">
          <ol className="flex flex-col gap-1.5">
            {topics.map((t, i) => {
              const p = i > 0 ? indexed[i - 1] : null;
              return (
                <li key={i} className="flex min-w-0 flex-wrap items-start gap-x-2 gap-y-1">
                  <span className="mt-px inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-raised px-1.5 text-[11px] font-semibold text-fg2">
                    {i}
                  </span>
                  {p ? (
                    <span className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
                      <span className="font-mono text-[12.5px] text-muted">{p.name || `arg${i}`}</span>
                      <span className="min-w-0">
                        <ParamValue type={p.type} value={p.value} />
                      </span>
                    </span>
                  ) : (
                    <span className="flex min-w-0 flex-1 items-start gap-1">
                      <span className="min-w-0 break-all font-mono text-[12.5px] text-fg2">{t}</span>
                      <CopyButton value={t} />
                    </span>
                  )}
                </li>
              );
            })}
          </ol>
        </Row>
        <Row label="Data">
          <div className="flex flex-col gap-2">
            {decoded && nonIndexed.length ? (
              <div className="inline-flex w-max items-center gap-0.5 rounded-full border border-line bg-raised p-0.5">
                {(["dec", "hex"] as const).map((v) => (
                  <button
                    key={v}
                    type="button"
                    onClick={() => setDataView(v)}
                    className={cx(
                      "rounded-full px-2.5 py-0.5 text-[11.5px] font-semibold transition-colors",
                      dataView === v ? "bg-card text-bright shadow-sm" : "text-muted hover:text-fg",
                    )}
                  >
                    {v === "dec" ? "Dec" : "Hex"}
                  </button>
                ))}
              </div>
            ) : null}
            {decoded && nonIndexed.length && dataView === "dec" ? (
              <ul className="flex flex-col gap-1.5 rounded-xl border border-line bg-raised/50 px-3 py-2">
                {nonIndexed.map((p, i) => (
                  <li key={i} className="flex min-w-0 flex-wrap items-start gap-x-3 gap-y-1">
                    <span className="shrink-0 font-mono text-[12.5px] text-muted">
                      {p.name || `arg${i}`} <span className="text-dim">: </span>
                    </span>
                    <span className="min-w-0 flex-1">
                      <ParamValue type={p.type} value={p.value} />
                    </span>
                  </li>
                ))}
              </ul>
            ) : log.data && log.data !== "0x" ? (
              <span className="flex min-w-0 items-start gap-1">
                <span className="code-block block max-h-40 min-w-0 flex-1 whitespace-pre-wrap break-all px-3 py-2">{log.data}</span>
                <CopyButton value={log.data} />
              </span>
            ) : (
              <span className="font-mono text-dim">0x</span>
            )}
          </div>
        </Row>
      </div>
    </div>
  );
}

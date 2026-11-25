/**
 * "Transaction history in 14 days" on the home stats card, as on Etherscan:
 * one series, a 2px line, recessive axis labels, a crosshair tooltip.
 *
 * The endpoint is the engine's daily transaction counts. Its exact path and
 * shape were not fixed when this was written, so the reader accepts the
 * Blockscout-compatible `{ chart_data: [{ date, transaction_count }] }` and a
 * few close variants; with no data the card shows other stats instead.
 */
import { useEffect, useMemo, useRef, useState } from "react";

import { useApi } from "@/lib/api";
import { formatInt } from "@/lib/format";

export interface DayPoint {
  date: string;
  count: number;
}

function normalise(raw: unknown): DayPoint[] {
  const arr = Array.isArray(raw)
    ? raw
    : raw && typeof raw === "object"
      ? ((raw as Record<string, unknown>).chart_data ?? (raw as Record<string, unknown>).items ?? (raw as Record<string, unknown>).days)
      : null;
  if (!Array.isArray(arr)) return [];
  return arr
    .map((p) => {
      const o = p as Record<string, unknown>;
      const date = String(o.date ?? o.day ?? "");
      const n = Number(o.transaction_count ?? o.tx_count ?? o.transactions ?? o.count ?? o.value);
      return { date, count: Number.isFinite(n) ? n : NaN };
    })
    .filter((p) => p.date && Number.isFinite(p.count))
    .sort((a, b) => a.date.localeCompare(b.date))
    .slice(-14);
}

export function useTxHistory() {
  const q = useApi<unknown>(["chart", "transactions"], "/stats/charts/transactions", undefined, {
    retry: false,
    staleTime: 5 * 60_000,
  });
  return useMemo(() => normalise(q.data), [q.data]);
}

const DAY = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "short", timeZone: "UTC" });
const DAY_LONG = new Intl.DateTimeFormat("en-GB", { weekday: "short", day: "numeric", month: "short", year: "numeric", timeZone: "UTC" });

export function TxHistoryChart({ points }: { points: DayPoint[] }) {
  const box = useRef<HTMLDivElement>(null);
  const [w, setW] = useState(360);
  const [hover, setHover] = useState<number | null>(null);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setW(Math.max(200, Math.floor(e.contentRect.width))));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const H = 96;
  const pad = { l: 40, r: 8, t: 8, b: 20 };
  const max = Math.max(1, ...points.map((p) => p.count));
  const min = Math.min(...points.map((p) => p.count));
  const lo = Math.max(0, Math.floor(min * 0.8));
  const hi = Math.ceil(max * 1.1);
  const iw = w - pad.l - pad.r;
  const ih = H - pad.t - pad.b;
  const x = (i: number) => pad.l + (points.length < 2 ? iw / 2 : (i / (points.length - 1)) * iw);
  const y = (v: number) => pad.t + ih - ((v - lo) / Math.max(1, hi - lo)) * ih;
  const d = points.map((p, i) => `${i ? "L" : "M"}${x(i)},${y(p.count)}`).join("");
  const h = hover === null ? null : points[hover];
  const labels = [0, Math.floor((points.length - 1) / 2), points.length - 1];

  return (
    <div ref={box} className="relative w-full select-none">
      <svg
        width={w}
        height={H}
        role="img"
        aria-label={`Transactions per day over the last ${points.length} days, from ${formatInt(points[0]?.count ?? 0)} to ${formatInt(points[points.length - 1]?.count ?? 0)}.`}
        onPointerMove={(e) => {
          const r = box.current?.getBoundingClientRect();
          if (!r) return;
          const px = e.clientX - r.left;
          let best = 0;
          points.forEach((_, i) => {
            if (Math.abs(x(i) - px) < Math.abs(x(best) - px)) best = i;
          });
          setHover(best);
        }}
        onPointerLeave={() => setHover(null)}
        className="block touch-pan-y"
      >
        {[hi, lo].map((v) => (
          <text key={v} x={pad.l - 8} y={y(v)} dy="0.32em" textAnchor="end" fontSize={10.5} fill="var(--text-dim)">
            {formatInt(v)}
          </text>
        ))}
        <line x1={pad.l} x2={w - pad.r} y1={y(lo)} y2={y(lo)} stroke="var(--border)" />
        <path d={d} fill="none" stroke="var(--chart-line)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
        {labels.map((i) =>
          points[i] ? (
            <text
              key={i}
              x={x(i)}
              y={H - 4}
              textAnchor={i === 0 ? "start" : i === points.length - 1 ? "end" : "middle"}
              fontSize={10.5}
              fill="var(--text-dim)"
            >
              {DAY.format(Date.parse(points[i].date))}
            </text>
          ) : null,
        )}
        {h && hover !== null ? (
          <g pointerEvents="none">
            <line x1={x(hover)} x2={x(hover)} y1={pad.t} y2={pad.t + ih} stroke="var(--text-dim)" />
            <circle cx={x(hover)} cy={y(h.count)} r={4} fill="var(--chart-line)" stroke="var(--card)" strokeWidth={2} />
          </g>
        ) : null}
      </svg>
      {h && hover !== null ? (
        <div
          className="pointer-events-none absolute -top-2 z-10 rounded-lg border border-line bg-card px-2.5 py-1.5 text-xs shadow-[var(--shadow)]"
          style={x(hover) > w / 2 ? { right: w - x(hover) + 8 } : { left: x(hover) + 8 }}
        >
          <p className="font-semibold text-bright">{formatInt(h.count)} transactions</p>
          <p className="text-muted">{DAY_LONG.format(Date.parse(h.date))}</p>
        </div>
      ) : null}
    </div>
  );
}

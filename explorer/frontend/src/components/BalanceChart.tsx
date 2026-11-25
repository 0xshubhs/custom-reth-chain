/**
 * Native balance over time: one series, drawn as a step line (a balance holds
 * its value until the next change), a 10% area wash, hairline grid, an end dot,
 * and a crosshair with a tooltip that snaps to the nearest change. The table
 * under it is the accessible view of the same numbers.
 *
 * Line colour is --chart-line (globals.css): a deeper gold than the brand hue,
 * checked with the dataviz palette validator against both card surfaces.
 */
import { useEffect, useMemo, useRef, useState } from "react";

import { formatAmount, formatDateTime } from "@/lib/format";
import { useChain } from "@/lib/chain";
import type { BalancePoint } from "@/lib/types";

const H = 220;
const PAD = { top: 12, right: 16, bottom: 26, left: 64 };
const WEI = 10n ** 18n;

function big(wei: string): bigint {
  try {
    return BigInt(wei);
  } catch {
    return 0n;
  }
}

/** wei to a plain ETH number, for labels only (exact values stay bigint). */
function toEth(wei: bigint): number {
  return Number(wei / 10n ** 12n) / 1e6;
}

const COMPACT = new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 2 });

function compact(n: number): string {
  if (n === 0) return "0";
  const a = Math.abs(n);
  if (a >= 1e15) return n.toExponential(1);
  if (a >= 1000) return COMPACT.format(n);
  if (a >= 1) return n.toLocaleString("en-US", { maximumFractionDigits: 2 });
  return n.toLocaleString("en-US", { maximumSignificantDigits: 2 });
}

/**
 * The y domain. Zero-based when the balance moves a lot relative to its size
 * (the usual case). When it moves very little relative to a large balance (a
 * funded faucet), a zero base would draw a flat line, so the domain fits the
 * range instead and the ticks read as offsets from the lowest balance.
 */
function domain(vals: bigint[]): { lo: bigint; hi: bigint; relative: boolean } {
  let lo = vals[0];
  let hi = vals[0];
  for (const v of vals) {
    if (v < lo) lo = v;
    if (v > hi) hi = v;
  }
  if (hi === lo) hi = lo + WEI;
  const span = hi - lo;
  if (lo <= span * 2n) return { lo: 0n, hi: hi + hi / 20n, relative: false };
  const pad = span / 10n;
  return { lo: lo - pad, hi: hi + pad, relative: lo > span * 20n };
}

const DATE_FMT = new Intl.DateTimeFormat("en-GB", { day: "numeric", month: "short" });

export function BalanceChart({ points }: { points: BalancePoint[] }) {
  const { native_symbol } = useChain();
  const box = useRef<HTMLDivElement>(null);
  const [w, setW] = useState(640);
  const [hover, setHover] = useState<number | null>(null);

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => setW(Math.max(260, Math.floor(e.contentRect.width))));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const data = useMemo(
    () =>
      [...points]
        .sort((a, b) => a.block_number - b.block_number)
        .map((p) => ({ p, t: Date.parse(p.timestamp), b: big(p.balance) })),
    [points],
  );

  if (data.length < 2) return null;

  const t0 = data[0].t;
  const t1 = Math.max(data[data.length - 1].t, t0 + 1);
  const { lo, hi, relative } = domain(data.map((d) => d.b));
  const iw = w - PAD.left - PAD.right;
  const ih = H - PAD.top - PAD.bottom;
  const x = (t: number) => PAD.left + ((t - t0) / (t1 - t0)) * iw;
  const y = (v: bigint) => PAD.top + ih - (Number(((v - lo) * 100000n) / (hi - lo)) / 100000) * ih;
  const ticks = [0n, 1n, 2n, 3n, 4n].map((i) => lo + ((hi - lo) * i) / 4n);
  const tickLabel = (v: bigint) => (relative ? `+${compact(toEth(v - ticks[0]))}` : compact(toEth(v)));

  let line = `M${x(data[0].t)},${y(data[0].b)}`;
  for (let i = 1; i < data.length; i++) line += `H${x(data[i].t)}V${y(data[i].b)}`;
  const area = `${line}V${y(lo)}H${x(data[0].t)}Z`;

  const xTicks = [0, 0.5, 1].map((f) => t0 + f * (t1 - t0));
  const last = data[data.length - 1];
  const h = hover === null ? null : data[hover];

  function onMove(clientX: number) {
    const el = box.current;
    if (!el) return;
    const px = clientX - el.getBoundingClientRect().left;
    let best = 0;
    let bestD = Infinity;
    data.forEach((d, i) => {
      const dist = Math.abs(x(d.t) - px);
      if (dist < bestD) {
        bestD = dist;
        best = i;
      }
    });
    setHover(best);
  }

  return (
    <div ref={box} className="relative w-full select-none">
      <svg
        width={w}
        height={H}
        role="img"
        aria-label={`Balance in ${native_symbol} over time, ${data.length} changes. The table below lists every change.`}
        tabIndex={0}
        className="block touch-pan-y outline-none"
        onPointerMove={(e) => onMove(e.clientX)}
        onPointerLeave={() => setHover(null)}
        onKeyDown={(e) => {
          if (e.key === "ArrowLeft") setHover((i) => Math.max(0, (i ?? data.length) - 1));
          else if (e.key === "ArrowRight") setHover((i) => Math.min(data.length - 1, (i ?? -1) + 1));
          else if (e.key === "Escape") setHover(null);
        }}
        onBlur={() => setHover(null)}
      >
        {ticks.map((v) => (
          <g key={v.toString()}>
            <line x1={PAD.left} x2={w - PAD.right} y1={y(v)} y2={y(v)} stroke="var(--border)" strokeWidth={1} />
            <text
              x={PAD.left - 8}
              y={y(v)}
              dy="0.32em"
              textAnchor="end"
              fontSize={11}
              fill="var(--text-dim)"
              className="tabular-nums"
            >
              {tickLabel(v)}
            </text>
          </g>
        ))}
        {xTicks.map((t, i) => (
          <text
            key={i}
            x={x(t)}
            y={H - 6}
            textAnchor={i === 0 ? "start" : i === 2 ? "end" : "middle"}
            fontSize={11}
            fill="var(--text-dim)"
          >
            {DATE_FMT.format(t)}
          </text>
        ))}
        <path d={area} fill="var(--chart-line)" opacity={0.1} />
        <path
          d={line}
          fill="none"
          stroke="var(--chart-line)"
          strokeWidth={2}
          strokeLinejoin="round"
          strokeLinecap="round"
        />
        <circle cx={x(last.t)} cy={y(last.b)} r={4} fill="var(--chart-line)" stroke="var(--card)" strokeWidth={2} />
        {h ? (
          <g pointerEvents="none">
            <line x1={x(h.t)} x2={x(h.t)} y1={PAD.top} y2={PAD.top + ih} stroke="var(--text-dim)" strokeWidth={1} />
            <circle cx={x(h.t)} cy={y(h.b)} r={4.5} fill="var(--chart-line)" stroke="var(--card)" strokeWidth={2} />
          </g>
        ) : null}
      </svg>
      {relative ? (
        <p className="mt-1 px-1 text-xs text-dim">
          The axis shows the balance above {formatAmount(ticks[0], 18, 4)} {native_symbol}, so small changes stay
          visible.
        </p>
      ) : null}
      {h ? (
        <div
          className="pointer-events-none absolute top-2 z-10 min-w-[170px] rounded-lg border border-line bg-card px-3 py-2 text-xs shadow-[var(--shadow)]"
          style={x(h.t) > w / 2 ? { right: w - x(h.t) + 10 } : { left: x(h.t) + 10 }}
        >
          <p className="text-sm font-semibold tabular-nums text-bright">
            {formatAmount(h.p.balance, 18, 6)} {native_symbol}
          </p>
          <p className="mt-0.5 flex items-center gap-1.5 text-muted">
            <span className="inline-block h-0.5 w-3 rounded bg-[var(--chart-line)]" aria-hidden />
            Balance at block {h.p.block_number}
          </p>
          <p className="mt-0.5 text-dim">{formatDateTime(h.p.timestamp)}</p>
        </div>
      ) : null}
    </div>
  );
}

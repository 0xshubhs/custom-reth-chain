/**
 * Formatting helpers. No viem here on purpose: this module is on every page,
 * and viem is only loaded by the read-contract form.
 */

const INT_FMT = new Intl.NumberFormat("en-US");

/** 1234567 -> "1,234,567". Accepts numbers, bigints and decimal strings. */
export function formatInt(v: number | bigint | string | null | undefined): string {
  if (v === null || v === undefined || v === "") return "0";
  try {
    return INT_FMT.format(typeof v === "string" ? BigInt(v) : v);
  } catch {
    return String(v);
  }
}

function toBig(v: string | bigint | null | undefined): bigint | null {
  if (v === null || v === undefined || v === "") return null;
  if (typeof v === "bigint") return v;
  try {
    return BigInt(v);
  } catch {
    return null;
  }
}

/** Exact decimal string of `value / 10^decimals`, no trailing zeros. */
export function formatUnitsExact(value: string | bigint, decimals: number): string {
  const v = toBig(value);
  if (v === null) return String(value);
  const neg = v < 0n;
  const abs = neg ? -v : v;
  if (decimals <= 0) return `${neg ? "-" : ""}${abs.toString()}`;
  const base = 10n ** BigInt(decimals);
  const int = abs / base;
  const frac = (abs % base).toString().padStart(decimals, "0").replace(/0+$/, "");
  return `${neg ? "-" : ""}${int.toString()}${frac ? `.${frac}` : ""}`;
}

/**
 * Human amount: thousands separators and at most `maxFrac` decimals
 * (truncated, never rounded up). A non-zero value that truncates to zero
 * shows as "<0.000001" so it is never mistaken for nothing.
 */
export function formatAmount(value: string | bigint | null | undefined, decimals: number, maxFrac = 6): string {
  const v = toBig(value ?? null);
  if (v === null) return "0";
  const neg = v < 0n;
  const abs = neg ? -v : v;
  const exact = formatUnitsExact(abs, decimals);
  const [i, f = ""] = exact.split(".");
  const fracShown = f.slice(0, maxFrac).replace(/0+$/, "");
  const intPart = formatInt(i);
  if (abs !== 0n && i === "0" && fracShown === "") {
    return `${neg ? "-" : ""}<0.${"0".repeat(Math.max(0, maxFrac - 1))}1`;
  }
  return `${neg ? "-" : ""}${intPart}${fracShown ? `.${fracShown}` : ""}`;
}

/** Full-precision amount with separators, for hover titles and detail rows. */
export function formatAmountFull(value: string | bigint | null | undefined, decimals: number): string {
  const v = toBig(value ?? null);
  if (v === null) return "0";
  const exact = formatUnitsExact(v, decimals);
  const neg = exact.startsWith("-");
  const [i, f] = (neg ? exact.slice(1) : exact).split(".");
  return `${neg ? "-" : ""}${formatInt(i)}${f ? `.${f}` : ""}`;
}

/** Gwei for gas prices: "1 Gwei", "0.5 Gwei". */
export function formatGwei(wei: string | null | undefined): string {
  if (wei === null || wei === undefined) return "";
  return `${formatAmountFull(wei, 9)} Gwei`;
}

/** 0x1b950260e8...0cdb style shortening; the full value goes in a title. */
export function shortHash(h: string | null | undefined, head = 6, tail = 4): string {
  if (!h) return "";
  if (h.length <= head + tail + 3) return h;
  return `${h.slice(0, head)}…${h.slice(-tail)}`;
}

export function pct(part: number, whole: number): number {
  if (!whole) return 0;
  return (part / whole) * 100;
}

export function formatPct(p: number | null | undefined, digits = 2): string {
  if (p === null || p === undefined || Number.isNaN(p)) return "";
  if (p > 0 && p < 10 ** -digits) return `<${(10 ** -digits).toFixed(digits)}%`;
  return `${p.toFixed(digits)}%`;
}

export function formatBytes(n: number | null | undefined): string {
  if (n === null || n === undefined) return "";
  if (n < 1024) return `${formatInt(n)} bytes`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(2)} MB`;
}

/* Time ---------------------------------------------------------------------- */

const UNITS: [number, string][] = [
  [365 * 24 * 3600, "year"],
  [30 * 24 * 3600, "month"],
  [7 * 24 * 3600, "week"],
  [24 * 3600, "day"],
  [3600, "hr"],
  [60, "min"],
  [1, "sec"],
];

/** "12 secs ago", "3 hrs ago", "4 days ago". */
export function timeAgo(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  // A block can be newer than the shared clock's last tick, or the server's
  // clock a little ahead of the viewer's: both read as "just now".
  const s = Math.round((now - t) / 1000);
  if (s < 5) return "just now";
  for (const [size, name] of UNITS) {
    if (s >= size) {
      const n = Math.floor(s / size);
      return `${n} ${name}${n === 1 ? "" : "s"} ago`;
    }
  }
  return "just now";
}

const ABS_FMT = new Intl.DateTimeFormat("en-GB", {
  year: "numeric",
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
  timeZoneName: "short",
});

const UTC_FMT = new Intl.DateTimeFormat("en-GB", {
  year: "numeric",
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
  timeZone: "UTC",
});

/** "23 Nov 2026, 15:58:03 GMT+5:30" in the viewer's time zone. */
export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  return ABS_FMT.format(t);
}

/** "23 Nov 2026, 10:28:03 UTC". */
export function formatUtc(iso: string | null | undefined): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  return `${UTC_FMT.format(t)} UTC`;
}

/** "2026-11-23 10:28:03" (UTC), for the Date Time column. */
export function formatUtcShort(iso: string | null | undefined): string {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  return new Date(t).toISOString().slice(0, 19).replace("T", " ");
}

/* Inputs -------------------------------------------------------------------- */

export const ADDRESS_RE = /^0x[0-9a-fA-F]{40}$/;
export const HASH_RE = /^0x[0-9a-fA-F]{64}$/;
export const BLOCK_NUM_RE = /^\d{1,12}$/;

export function isAddress(s: string): boolean {
  return ADDRESS_RE.test(s);
}

export function sameAddr(a: string | null | undefined, b: string | null | undefined): boolean {
  return !!a && !!b && a.toLowerCase() === b.toLowerCase();
}

/** Hex data to UTF-8 text, or null when it is not printable text. */
export function hexToUtf8(hex: string): string | null {
  const h = hex.startsWith("0x") ? hex.slice(2) : hex;
  if (!h || h.length % 2) return null;
  const bytes = new Uint8Array(h.length / 2);
  for (let i = 0; i < bytes.length; i++) bytes[i] = parseInt(h.slice(i * 2, i * 2 + 2), 16);
  try {
    const s = new TextDecoder("utf-8", { fatal: true }).decode(bytes).replace(/\0+$/, "");
    if (!s || /[\u0000-\u0008\u000e-\u001f\u007f]/.test(s)) return null;
    return s;
  } catch {
    return null;
  }
}

/** Uppercase the first letter: "ok" -> "Ok". */
export function cap(s: string): string {
  return s ? s[0].toUpperCase() + s.slice(1) : s;
}

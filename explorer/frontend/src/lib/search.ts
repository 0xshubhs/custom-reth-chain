/**
 * Search-param parsing for validateSearch. Defaults come back as undefined so
 * they drop out of the URL (`?page=1` is never written).
 */

export function pageParam(v: unknown): number | undefined {
  const n = typeof v === "number" ? v : typeof v === "string" ? Number(v) : NaN;
  if (!Number.isFinite(n) || n < 2) return undefined;
  return Math.min(Math.floor(n), 1_000_000);
}

export function strParam(v: unknown): string | undefined {
  if (typeof v === "number") return String(v);
  if (typeof v !== "string") return undefined;
  const s = v.trim();
  return s ? s.slice(0, 200) : undefined;
}

export interface TabPageSearch {
  tab?: string;
  page?: number;
}

export function tabPageSearch(s: Record<string, unknown>): TabPageSearch {
  return { tab: strParam(s.tab), page: pageParam(s.page) };
}

export interface PageSearch {
  page?: number;
}

export function pageSearch(s: Record<string, unknown>): PageSearch {
  return { page: pageParam(s.page) };
}

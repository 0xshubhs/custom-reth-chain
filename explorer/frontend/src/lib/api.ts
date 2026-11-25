/**
 * The explorer API client. Same origin only (`/api/v2`): in production nginx
 * proxies /api/ to the engine, in dev the Vite proxy does. That is why the CSP
 * can say connect-src 'self' and nothing else.
 *
 * Errors follow the contract: `{"message": "..."}` with 400 / 404 / 429, and
 * 429 carries Retry-After.
 */
import { QueryClient, useQuery, type UseQueryOptions } from "@tanstack/react-query";

import type { Config, Page } from "./types";
import { DEFAULT_CHAIN } from "./site";

export const API_BASE = "/api/v2";

export class ApiError extends Error {
  readonly status: number;
  /** Seconds, from Retry-After on a 429. */
  readonly retryAfter: number | null;

  constructor(status: number, message: string, retryAfter: number | null = null) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.retryAfter = retryAfter;
  }

  get notFound() {
    return this.status === 404;
  }
}

async function parseError(res: Response): Promise<ApiError> {
  let message = "";
  try {
    const body = (await res.json()) as { message?: unknown };
    if (body && typeof body.message === "string") message = body.message;
  } catch {
    /* not JSON: nginx error page, engine down, ... */
  }
  const ra = res.headers.get("Retry-After");
  const retryAfter = ra && /^\d+$/.test(ra) ? Number(ra) : null;
  if (!message) {
    if (res.status === 429) message = "Too many requests.";
    else if (res.status === 404) message = "Not found.";
    else if (res.status >= 500) message = "The explorer API is not answering right now.";
    else message = `Request failed (${res.status}).`;
  }
  return new ApiError(res.status, message, retryAfter);
}

type Params = Record<string, string | number | boolean | null | undefined>;

export function apiUrl(path: string, params?: Params): string {
  const qs = new URLSearchParams();
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      if (v !== null && v !== undefined && v !== "") qs.set(k, String(v));
    }
  }
  const q = qs.toString();
  return `${API_BASE}${path}${q ? `?${q}` : ""}`;
}

export async function apiGet<T>(path: string, params?: Params, signal?: AbortSignal): Promise<T> {
  let res: Response;
  try {
    res = await fetch(apiUrl(path, params), { headers: { Accept: "application/json" }, signal });
  } catch (e) {
    if ((e as Error)?.name === "AbortError") throw e;
    throw new ApiError(0, "Could not reach the explorer API. Check your connection and try again.");
  }
  if (!res.ok) throw await parseError(res);
  return (await res.json()) as T;
}

export async function apiPost<T>(path: string, body: BodyInit, json = true): Promise<T> {
  let res: Response;
  try {
    res = await fetch(apiUrl(path), {
      method: "POST",
      headers: json
        ? { "Content-Type": "application/json", Accept: "application/json" }
        : { Accept: "application/json" },
      body,
    });
  } catch {
    throw new ApiError(0, "Could not reach the explorer API. Check your connection and try again.");
  }
  if (!res.ok) throw await parseError(res);
  return (await res.json()) as T;
}

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      refetchOnWindowFocus: false,
      retry: (count, err) => {
        if (err instanceof ApiError && (err.status === 400 || err.status === 404 || err.status === 429)) return false;
        return count < 2;
      },
    },
  },
});

/** A typed GET query. `key` is the cache key; `path`/`params` build the URL. */
export function useApi<T>(
  key: readonly unknown[],
  path: string | null,
  params?: Params,
  opts?: Omit<UseQueryOptions<T, Error, T, readonly unknown[]>, "queryKey" | "queryFn">,
) {
  return useQuery<T, Error, T, readonly unknown[]>({
    ...opts,
    queryKey: key,
    queryFn: ({ signal }) => apiGet<T>(path as string, params, signal),
    enabled: path !== null && opts?.enabled !== false,
  });
}

export const PAGE_SIZE = 25;

/** A paginated list: `page` is 1-based, the API is limit/offset. */
export function usePage<T>(
  key: readonly unknown[],
  path: string | null,
  page: number,
  params?: Params,
  opts?: { refetchInterval?: number | false; limit?: number },
) {
  const limit = opts?.limit ?? PAGE_SIZE;
  const offset = (Math.max(1, page) - 1) * limit;
  const listKey = JSON.stringify([key, path, params ?? null, limit]);
  return useApi<Page<T>>(
    [...key, { ...params, limit, offset }],
    path,
    { ...params, limit, offset },
    {
      // Keep the rows on screen while the NEXT PAGE of the same list loads, and
      // only then. Keeping them across a change of list (another tab, another
      // address, another filter) drew one list's rows with another list's
      // table: Transactions -> Token transfers rendered transactions as token
      // transfers and crashed on `token.type`.
      placeholderData: (previous, previousQuery) =>
        previousQuery && previousQuery.meta?.listKey === listKey ? previous : undefined,
      meta: { listKey },
      refetchInterval: opts?.refetchInterval,
    },
  );
}

/** Network config for the add-network button and labels. Falls back to the built-in defaults. */
export function useConfig(): Config {
  const q = useApi<Config>(["config"], "/config", undefined, { staleTime: Infinity, retry: 1 });
  return q.data ?? DEFAULT_CHAIN;
}

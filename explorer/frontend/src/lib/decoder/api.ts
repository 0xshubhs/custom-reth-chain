/**
 * The decoder's only data sources: OUR explorer API, same origin. These
 * replace swiss-knife's lookups on 4byte.directory, Sourcify's 4byte API and
 * block-explorer ABI fetches: the site makes no third-party request at
 * runtime (CSP connect-src 'self').
 *
 *   GET {base}/signatures/functions?selectors=0x..,0x..  -> { "0x..": ["fn(type,...)"] }
 *   GET {base}/signatures/events?topics=0x..,0x..        -> { "0x..": ["Ev(type,...)"] }
 *   GET {base}/signatures/errors?selectors=0x..,0x..     -> { "0x..": ["Err(type,...)"] }
 *   GET {base}/smart-contracts/{addr}                    -> { abi, implementation_abi, deployed_bytecode, ... }
 *
 * Every answer is cached in memory for the page's lifetime. An error, a 404
 * (the signature endpoints may not exist yet) or a missing key all mean "no
 * result", never an exception. Selector and topic lookups made in the same
 * tick are batched into one request. A small bundled table of very common
 * signatures (./common.ts) is the floor under the API's answers.
 */
import { COMMON_ERRORS, COMMON_EVENTS, COMMON_FUNCTIONS } from "./common";

let apiBase = "/api/v2";

/** Point the decoder at another API base, e.g. "http://127.0.0.1:4000/api/v2" in a test. */
export function setDecoderApiBase(base: string) {
  apiBase = base.replace(/\/+$/, "");
  abiCache.clear();
  sigCache.functions.clear();
  sigCache.events.clear();
  sigCache.errors.clear();
}

export function getDecoderApiBase() {
  return apiBase;
}

async function getJson(url: string): Promise<unknown | null> {
  try {
    const res = await fetch(url, { headers: { Accept: "application/json" } });
    if (!res.ok) return null;
    return await res.json();
  } catch {
    return null;
  }
}

/* Signatures ----------------------------------------------------------------- */

type SigKind = "functions" | "events" | "errors";

const sigCache: Record<SigKind, Map<string, Promise<string[]>>> = {
  functions: new Map(),
  events: new Map(),
  errors: new Map(),
};

/** Keys waiting for the next batched request, per kind. */
const pending: Record<SigKind, Map<string, (v: string[]) => void>> = {
  functions: new Map(),
  events: new Map(),
  errors: new Map(),
};
const scheduled: Record<SigKind, boolean> = { functions: false, events: false, errors: false };

/** Most keys per request, so a URL never gets long enough to be refused. */
const MAX_BATCH = 50;

function flush(kind: SigKind) {
  scheduled[kind] = false;
  const batch = [...pending[kind].entries()];
  pending[kind].clear();
  for (let i = 0; i < batch.length; i += MAX_BATCH) {
    const part = batch.slice(i, i + MAX_BATCH);
    const param = kind === "events" ? "topics" : "selectors";
    const keys = part.map(([k]) => k).join(",");
    void getJson(`${apiBase}/signatures/${kind}?${param}=${keys}`).then((body) => {
      const map = body && typeof body === "object" && !Array.isArray(body) ? (body as Record<string, unknown>) : {};
      for (const [key, resolve] of part) {
        const found = map[key] ?? map[key.toLowerCase()];
        resolve(Array.isArray(found) ? found.filter((s): s is string => typeof s === "string") : []);
      }
    });
  }
}

function lookup(kind: SigKind, key: string): Promise<string[]> {
  const k = key.toLowerCase();
  const hit = sigCache[kind].get(k);
  if (hit) return hit;
  const p = new Promise<string[]>((resolve) => {
    pending[kind].set(k, resolve);
    if (!scheduled[kind]) {
      scheduled[kind] = true;
      setTimeout(() => flush(kind), 0);
    }
  });
  sigCache[kind].set(k, p);
  return p;
}

/** The API's answers first, then the bundled common ones it did not list. */
function withCommon(found: string[], common: string[] | undefined): string[] {
  if (!common) return found;
  return [...found, ...common.filter((c) => !found.includes(c))];
}

/** Text signatures for a 4-byte selector ("0xa9059cbb"), best first. */
export async function lookupFunctionSignatures(selector: string): Promise<string[]> {
  if (!/^0x[0-9a-fA-F]{8}$/.test(selector)) return [];
  return withCommon(await lookup("functions", selector), COMMON_FUNCTIONS[selector.toLowerCase()]);
}

/** Text signatures for an event topic0, best first. */
export async function lookupEventSignatures(topic: string): Promise<string[]> {
  if (!/^0x[0-9a-fA-F]{64}$/.test(topic)) return [];
  return withCommon(await lookup("events", topic), COMMON_EVENTS[topic.toLowerCase()]);
}

/** Text signatures for a custom error (or Error / Panic) selector, best first. */
export async function lookupErrorSignatures(selector: string): Promise<string[]> {
  if (!/^0x[0-9a-fA-F]{8}$/.test(selector)) return [];
  return withCommon(await lookup("errors", selector), COMMON_ERRORS[selector.toLowerCase()]);
}

/* Verified ABIs ------------------------------------------------------------------- */

const abiCache = new Map<string, Promise<unknown[] | null>>();

/** EIP-7702: an account whose code is 0xef0100 + the delegate's address runs the delegate's code. */
const DELEGATION = /^0xef0100([0-9a-fA-F]{40})$/;

interface SmartContractAnswer {
  abi?: unknown;
  implementation_abi?: unknown;
  deployed_bytecode?: unknown;
}

async function fetchAbi(address: string, hops: number): Promise<unknown[] | null> {
  const body = (await getJson(`${apiBase}/smart-contracts/${address}`)) as SmartContractAnswer | null;
  if (!body) return null;
  const impl = Array.isArray(body.implementation_abi) ? body.implementation_abi : [];
  const own = Array.isArray(body.abi) ? body.abi : [];
  // A proxy is called with its implementation's functions; its own (admin)
  // functions are kept too. ethers keeps the first of two equal signatures.
  const merged = [...impl, ...own];
  if (merged.length) return merged;
  const code = typeof body.deployed_bytecode === "string" ? body.deployed_bytecode : "";
  const m = DELEGATION.exec(code);
  if (m && hops < 2) return fetchContractAbi(`0x${m[1]}`, hops + 1);
  return null;
}

/**
 * The verified ABI to decode calls to `address`: the implementation's ABI for
 * a proxy (plus the proxy's own), the delegate's ABI for an EIP-7702 account,
 * null when nothing is verified.
 */
export function fetchContractAbi(address: string, hops = 0): Promise<unknown[] | null> {
  if (!/^0x[0-9a-fA-F]{40}$/.test(address)) return Promise.resolve(null);
  const k = address.toLowerCase();
  const hit = abiCache.get(k);
  if (hit) return hit;
  const p = fetchAbi(address, hops);
  abiCache.set(k, p);
  return p;
}

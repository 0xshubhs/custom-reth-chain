/**
 * Site-wide constants for the router head, robots.txt and the web manifest.
 *
 * `import.meta.env` rather than `process.env`: Vite inlines VITE_* at build
 * time, and Bun maps it onto the environment when
 * scripts/generate-static-meta.mjs imports src/site/* directly.
 */
export const SITE_URL: string = import.meta.env.VITE_SITE_URL || "http://localhost:3000";

/** The chain's name and coin, set at build time (VITE_NETWORK_NAME, VITE_NATIVE_SYMBOL). */
export const NETWORK_NAME: string = import.meta.env.VITE_NETWORK_NAME || "Meowchain";
export const NATIVE_SYMBOL: string = import.meta.env.VITE_NATIVE_SYMBOL || "ETH";

export const SITE_NAME = `${NETWORK_NAME} Explorer`;

/** Appended to per-page titles. */
export const TITLE_SUFFIX = ` | ${SITE_NAME}`;

/** Defaults used until /api/v2/config answers (and if it never does). */
export const DEFAULT_CHAIN = {
  chain_id: Number(import.meta.env.VITE_CHAIN_ID) || 9323310,
  network_name: NETWORK_NAME,
  native_symbol: NATIVE_SYMBOL,
  public_rpc_url: import.meta.env.VITE_CHAIN_RPC || "http://localhost:8548",
};

export const NATIVE_DECIMALS = 18;

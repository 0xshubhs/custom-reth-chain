/**
 * Network config (/api/v2/config) fetched once and handed down by context, so
 * table cells can say "ETH" without each one owning a query.
 */
import { createContext, useContext, type ReactNode } from "react";

import { useConfig } from "./api";
import { DEFAULT_CHAIN } from "./site";
import type { Config } from "./types";

const ChainContext = createContext<Config>(DEFAULT_CHAIN);

export function ChainProvider({ children }: { children: ReactNode }) {
  const cfg = useConfig();
  return <ChainContext.Provider value={cfg}>{children}</ChainContext.Provider>;
}

export function useChain(): Config {
  return useContext(ChainContext);
}

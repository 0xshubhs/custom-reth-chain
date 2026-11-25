/**
 * The search box (header on every page, hero on the home page), Etherscan
 * style: a filter dropdown on the left, suggestions as you type, "/" to focus
 * from anywhere. Enter goes straight to the one obvious match (a block number,
 * an address, a hash with a single hit) or to /search-results.
 */
import { useEffect, useId, useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { ChevronDown, Search, X } from "lucide-react";

import { apiGet } from "@/lib/api";
import { cx } from "@/lib/cx";
import { ADDRESS_RE, BLOCK_NUM_RE, HASH_RE, shortHash } from "@/lib/format";
import type { SearchResult } from "@/lib/types";
import { Menu } from "./overlay";
import { Spinner } from "./ui";
import { NETWORK_NAME } from "@/lib/site";

export const RESULT_LABEL: Record<SearchResult["type"], string> = {
  block: "Block",
  transaction: "Transaction",
  address: "Address",
  contract: "Contract",
  token: "Token",
};

/** Kept for callers; search results are text only now. */
export function resultIcon(_type: SearchResult["type"], _cls?: string) {
  return null;
}

export function resultTitle(r: SearchResult): string {
  switch (r.type) {
    case "block":
      return `Block #${r.number ?? ""}`;
    case "transaction":
      return r.hash ?? "";
    case "token":
      return r.name && r.symbol ? `${r.name} (${r.symbol})` : r.name || r.symbol || r.address || "";
    default:
      return r.name || r.address || "";
  }
}

export function resultSub(r: SearchResult): string | null {
  switch (r.type) {
    case "block":
      return r.hash ?? null;
    case "transaction":
      return null;
    default:
      return r.name || r.symbol ? (r.address ?? null) : null;
  }
}

type Filter = "all" | "address" | "token" | "block" | "transaction" | "contract";

const FILTERS: { id: Filter; label: string }[] = [
  { id: "all", label: "All Filters" },
  { id: "address", label: "Addresses" },
  { id: "token", label: "Tokens" },
  { id: "contract", label: "Contracts" },
  { id: "block", label: "Blocks" },
  { id: "transaction", label: "Transactions" },
];

function matches(f: Filter, r: SearchResult) {
  if (f === "all") return true;
  if (f === "address") return r.type === "address" || r.type === "contract";
  return r.type === f;
}

/** Navigate to a search result. */
export function useGoToResult() {
  const navigate = useNavigate();
  return (r: SearchResult) => {
    switch (r.type) {
      case "block":
        return navigate({ to: "/block/$id", params: { id: String(r.number ?? r.hash ?? "") } });
      case "transaction":
        return navigate({ to: "/tx/$hash", params: { hash: r.hash ?? "" } });
      case "token":
        return navigate({ to: "/token/$addr", params: { addr: r.address ?? "" } });
      default:
        return navigate({ to: "/address/$addr", params: { addr: r.address ?? "" } });
    }
  };
}

/** "/" focuses the visible search box, unless the user is typing elsewhere. */
export function useSlashToSearch() {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "/" || e.metaKey || e.ctrlKey || e.altKey) return;
      const t = e.target as HTMLElement | null;
      if (t && (t.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(t.tagName))) return;
      const inputs = Array.from(document.querySelectorAll<HTMLInputElement>("input[data-global-search]"));
      const visible = inputs.find((i) => i.offsetParent !== null);
      if (!visible) return;
      e.preventDefault();
      visible.focus();
      visible.select();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);
}

export function SearchBar({ size = "md", autoFocus = false }: { size?: "md" | "lg"; autoFocus?: boolean }) {
  const navigate = useNavigate();
  const goTo = useGoToResult();
  const listId = useId();
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const [open, setOpen] = useState(false);
  const [focused, setFocused] = useState(false);
  const [loading, setLoading] = useState(false);
  const [raw, setRaw] = useState<SearchResult[] | null>(null);
  const [active, setActive] = useState(-1);
  const box = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const seq = useRef(0);

  const query = q.trim();
  const results = raw ? raw.filter((r) => matches(filter, r)) : null;

  // Debounced suggestions.
  useEffect(() => {
    if (query.length < 2) {
      setRaw(null);
      setLoading(false);
      return;
    }
    const my = ++seq.current;
    setLoading(true);
    const t = setTimeout(() => {
      apiGet<SearchResult[]>("/search", { q: query })
        .then((r) => {
          if (my === seq.current) {
            setRaw(Array.isArray(r) ? r : []);
            setActive(-1);
          }
        })
        .catch(() => {
          if (my === seq.current) setRaw([]);
        })
        .finally(() => {
          if (my === seq.current) setLoading(false);
        });
    }, 220);
    return () => clearTimeout(t);
  }, [query]);

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (box.current && !box.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, []);

  function finish() {
    setOpen(false);
    setQ("");
    setRaw(null);
    input.current?.blur();
  }

  async function submit() {
    if (!query) return;
    if (active >= 0 && results?.[active]) {
      goTo(results[active]);
      finish();
      return;
    }
    if (BLOCK_NUM_RE.test(query) && (filter === "all" || filter === "block")) {
      navigate({ to: "/block/$id", params: { id: query } });
      finish();
      return;
    }
    if (ADDRESS_RE.test(query) && filter !== "block" && filter !== "transaction") {
      navigate(filter === "token" ? { to: "/token/$addr", params: { addr: query } } : { to: "/address/$addr", params: { addr: query } });
      finish();
      return;
    }
    let r = results;
    if (HASH_RE.test(query) && !r) {
      try {
        r = (await apiGet<SearchResult[]>("/search", { q: query })).filter((x) => matches(filter, x));
      } catch {
        r = null;
      }
    }
    if (r && r.length === 1 && (HASH_RE.test(query) || r[0].type !== "token")) {
      goTo(r[0]);
    } else {
      navigate({ to: "/search-results", search: { q: query } });
    }
    finish();
  }

  const show = open && query.length >= 2;
  const big = size === "lg";

  return (
    <div ref={box} className="relative w-full">
      <form
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
        className={cx(
          "flex items-center rounded-full border border-line bg-card transition-shadow focus-within:border-gold focus-within:shadow-[0_0_0_3px_rgba(128,128,128,0.25)]",
          big ? "p-1.5 shadow-[var(--shadow)]" : "p-1",
        )}
      >
        <Menu
          label="Search filter"
          button={
            <span className="inline-flex items-center gap-1">
              {FILTERS.find((f) => f.id === filter)?.label}
              <ChevronDown className="h-3.5 w-3.5 opacity-70" />
            </span>
          }
          buttonClassName={cx(
            "hidden shrink-0 items-center rounded-full bg-raised font-medium text-fg2 transition-colors hover:bg-hover sm:inline-flex",
            big ? "px-4 py-2 text-sm" : "px-3 py-1 text-[12.5px]",
          )}
          items={FILTERS.map((f) => ({
            key: f.id,
            label: f.label,
            selected: f.id === filter,
            onSelect: () => {
              setFilter(f.id);
              input.current?.focus();
            },
          }))}
        />
        <label className="sr-only" htmlFor={`${listId}-input`}>
          {`Search the ${NETWORK_NAME} chain`}
        </label>
        <input
          ref={input}
          id={`${listId}-input`}
          data-global-search=""
          autoFocus={autoFocus}
          value={q}
          onChange={(e) => {
            setQ(e.target.value);
            setOpen(true);
          }}
          onFocus={() => {
            setFocused(true);
            setOpen(true);
          }}
          onBlur={() => setFocused(false)}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              setOpen(false);
              input.current?.blur();
              return;
            }
            if (!results?.length) return;
            if (e.key === "ArrowDown") {
              e.preventDefault();
              setActive((a) => Math.min(results.length - 1, a + 1));
            } else if (e.key === "ArrowUp") {
              e.preventDefault();
              setActive((a) => Math.max(-1, a - 1));
            }
          }}
          type="search"
          inputMode="search"
          autoComplete="off"
          spellCheck={false}
          placeholder={big ? "Search by Address / Txn Hash / Block / Token / Contract name" : "Search by Address / Txn Hash / Block / Token"}
          role="combobox"
          aria-expanded={show}
          aria-controls={listId}
          aria-activedescendant={active >= 0 ? `${listId}-${active}` : undefined}
          className={cx(
            "min-w-0 flex-1 bg-transparent text-fg outline-none placeholder:text-dim [&::-webkit-search-cancel-button]:hidden",
            big ? "px-4 py-2 text-[15px]" : "px-3 py-1 text-[13px]",
          )}
        />
        {q ? (
          <button
            type="button"
            aria-label="Clear search"
            onClick={() => {
              setQ("");
              input.current?.focus();
            }}
            className="mr-1 rounded-full p-1 text-dim hover:bg-hover hover:text-fg"
          >
            <X className="h-4 w-4" />
          </button>
        ) : !focused ? (
          <kbd
            title="Press / to search"
            className="mr-1 hidden h-6 min-w-6 items-center justify-center rounded-md border border-line bg-raised px-1.5 font-mono text-[11px] text-dim sm:inline-flex"
          >
            /
          </kbd>
        ) : null}
        <button
          type="submit"
          aria-label="Search"
          className={cx(
            "inline-flex shrink-0 items-center justify-center rounded-full bg-gold text-on-gold transition-[transform,background-color] hover:bg-gold-hover active:scale-95",
            big ? "h-10 w-10" : "h-7 w-7",
          )}
        >
          <Search className={big ? "h-4.5 w-4.5" : "h-3.5 w-3.5"} />
        </button>
      </form>

      {show ? (
        <div
          id={listId}
          role="listbox"
          className="pop-in absolute left-0 right-0 top-[calc(100%+6px)] z-50 overflow-hidden rounded-2xl border border-line bg-card shadow-[var(--shadow)]"
        >
          {loading && !raw ? (
            <div className="flex items-center gap-2 px-4 py-3 text-sm text-muted">
              <Spinner className="h-3.5 w-3.5 text-gold" /> Searching
            </div>
          ) : results && results.length === 0 ? (
            <div className="px-4 py-3 text-sm text-muted">
              No {filter === "all" ? "" : `${FILTERS.find((f) => f.id === filter)?.label.toLowerCase()} `}match for{" "}
              <span className="font-mono text-fg">{query.length > 40 ? shortHash(query, 12, 8) : query}</span>. Press
              Enter to search anyway.
            </div>
          ) : (
            <ul className="max-h-[60vh] overflow-y-auto py-1.5">
              {(results ?? []).slice(0, 8).map((r, i) => {
                const sub = resultSub(r);
                return (
                  <li key={`${r.type}:${r.hash ?? r.address ?? r.number}:${i}`}>
                    <button
                      id={`${listId}-${i}`}
                      type="button"
                      role="option"
                      aria-selected={i === active}
                      onMouseEnter={() => setActive(i)}
                      onClick={() => {
                        goTo(r);
                        finish();
                      }}
                      className={cx(
                        "flex w-full items-center gap-3 px-4 py-2 text-left transition-colors",
                        i === active ? "bg-hover" : "hover:bg-hover",
                      )}
                    >
                      <span className="min-w-0 flex-1">
                        <span className={cx("block truncate text-[13.5px] text-fg", r.type === "transaction" && "font-mono text-[12.5px]")}>
                          {resultTitle(r)}
                        </span>
                        {sub ? <span className="block truncate font-mono text-[11.5px] text-dim">{sub}</span> : null}
                      </span>
                      <span className="shrink-0 rounded-full border border-line px-2 py-0.5 text-[10.5px] font-semibold text-muted">
                        {RESULT_LABEL[r.type]}
                      </span>
                    </button>
                  </li>
                );
              })}
              {results && results.length > 8 ? (
                <li className="border-t border-line px-4 py-2 text-xs text-muted">Press Enter to see all {results.length} results.</li>
              ) : null}
            </ul>
          )}
        </div>
      ) : null}
    </div>
  );
}

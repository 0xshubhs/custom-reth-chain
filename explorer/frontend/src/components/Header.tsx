/**
 * Site header in Etherscan's layout: a thin top strip (network line, search
 * on every page but home, theme menu, add-network) and the main bar (logo and
 * the Blockchain / Tokens / Contracts / Tools menus). Phones get the logo,
 * theme and a menu button, with the search on its own row.
 */
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useRouterState } from "@tanstack/react-router";
import { Check, ChevronDown, Menu as MenuIcon, Moon, Sun, SunMoon, X } from "lucide-react";

import { useApi } from "@/lib/api";
import { useChain } from "@/lib/chain";
import { cx } from "@/lib/cx";
import { THEMES, useTheme, type Theme } from "@/lib/theme";
import type { Stats } from "@/lib/types";
import { AddNetworkButton } from "./AddNetworkButton";
import { LogoMark, LogoWordmark } from "./BrandLogo";
import { Menu } from "./overlay";
import { SearchBar } from "./SearchBar";
import { NETWORK_NAME } from "@/lib/site";

type NavTo = "/txs" | "/txs-internal" | "/blocks" | "/tokens" | "/verified-contracts" | "/contract-verification" | "/tools/decoder";
type NavLink = { to: NavTo; label: string; search?: Record<string, string> };

const NAV: { label: string; base: string[]; items: NavLink[] }[] = [
  {
    label: "Blockchain",
    base: ["/txs", "/blocks", "/block", "/tx", "/txs-internal"],
    items: [
      { to: "/txs", label: "Transactions" },
      { to: "/txs-internal", label: "Contract internal transactions" },
      { to: "/blocks", label: "Blocks" },
    ],
  },
  {
    label: "Tokens",
    base: ["/tokens", "/token"],
    items: [
      { to: "/tokens", label: "All tokens" },
      { to: "/tokens", label: "ERC-20 tokens", search: { type: "ERC-20" } },
      { to: "/tokens", label: "ERC-1155 tokens", search: { type: "ERC-1155" } },
      { to: "/tokens", label: "ERC-721 tokens", search: { type: "ERC-721" } },
    ],
  },
  {
    label: "Contracts",
    base: ["/verified-contracts", "/contract-verification"],
    items: [
      { to: "/verified-contracts", label: "Verified contracts" },
      { to: "/contract-verification", label: "Verify and publish" },
    ],
  },
  {
    label: "Tools",
    base: ["/tools"],
    items: [{ to: "/tools/decoder", label: "Calldata decoder" }],
  },
];

function ThemeIcon({ theme, className }: { theme: Theme; className?: string }) {
  if (theme === "light") return <Sun className={className} />;
  if (theme === "dim") return <SunMoon className={className} />;
  return <Moon className={className} />;
}

export function ThemeMenu() {
  const { theme, setTheme } = useTheme();
  return (
    <Menu
      label="Theme"
      place="bottom-end"
      button={<ThemeIcon theme={theme} className="h-4 w-4" />}
      buttonClassName="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-line bg-card text-muted transition-colors hover:border-line-strong hover:text-fg active:scale-95"
      panelClassName="!min-w-[160px]"
      items={THEMES.map((t) => ({
        key: t.id,
        label: (
          <span className="inline-flex items-center gap-2">
            <ThemeIcon theme={t.id} className="h-3.5 w-3.5" />
            {t.label}
          </span>
        ),
        hint: t.id === theme ? <Check className="h-3.5 w-3.5 text-link" /> : undefined,
        selected: t.id === theme,
        onSelect: () => setTheme(t.id),
      }))}
    />
  );
}

function NavDropdown({ label, items, active }: { label: string; items: NavLink[]; active: boolean }) {
  const [open, setOpen] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const box = useRef<HTMLDivElement>(null);
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  useEffect(() => setOpen(false), [pathname]);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (box.current && !box.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);
  const enter = () => {
    if (timer.current) clearTimeout(timer.current);
    setOpen(true);
  };
  const leave = () => {
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setOpen(false), 150);
  };
  return (
    <div ref={box} className="relative" onMouseEnter={enter} onMouseLeave={leave}>
      <button
        type="button"
        aria-haspopup="true"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className={cx(
          "inline-flex items-center gap-1 rounded-full px-3 py-1.5 text-[14px] font-medium transition-colors",
          active ? "text-link" : "text-fg hover:text-link",
        )}
      >
        {label}
        <ChevronDown className={cx("h-3.5 w-3.5 transition-transform", open && "rotate-180")} />
      </button>
      {open ? (
        <div className="pop-in absolute right-0 top-[calc(100%+4px)] z-50 min-w-[240px] rounded-2xl border border-line bg-card p-1.5 shadow-[var(--shadow)]">
          {items.map((it) => (
            <Link
              key={`${it.to}:${it.label}`}
              to={it.to}
              search={(it.search ?? {}) as never}
              className="block rounded-xl px-3 py-2 text-[13.5px] text-fg transition-colors hover:bg-hover hover:text-link"
            >
              {it.label}
            </Link>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function TopStrip({ home }: { home: boolean }) {
  const cfg = useChain();
  const stats = useApi<Stats>(["stats"], "/stats", undefined, { staleTime: 30_000 });
  return (
    <div className="border-b border-line">
      <div className="mx-auto flex h-12 max-w-[1400px] items-center gap-4 px-4 sm:px-6">
        <p className="hidden min-w-0 items-center gap-3 truncate text-[12.5px] text-muted sm:flex">
          <span>
            {cfg.native_symbol} on {cfg.network_name}, chain ID <span className="font-medium text-fg">{cfg.chain_id}</span>
          </span>
          {stats.data?.latest_block != null ? (
            <>
              <span className="text-line-strong">|</span>
              <span>
                Latest block{" "}
                <Link to="/block/$id" params={{ id: String(stats.data.latest_block) }} className="link font-medium">
                  {stats.data.latest_block}
                </Link>
              </span>
            </>
          ) : null}
        </p>
        <div className="ml-auto flex w-full items-center justify-end gap-2 sm:w-auto">
          {!home ? (
            <div className="hidden w-full max-w-[520px] md:block lg:w-[520px]">
              <SearchBar />
            </div>
          ) : null}
          <ThemeMenu />
          <AddNetworkButton variant="compact" label="Add network" className="hidden xl:inline-flex" />
        </div>
      </div>
    </div>
  );
}

function MobileSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="py-1.5">
      <p className="eyebrow px-3 py-1.5">{title}</p>
      {children}
    </div>
  );
}

export function Header() {
  const [open, setOpen] = useState(false);
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const home = pathname === "/";
  useEffect(() => setOpen(false), [pathname]);
  const isActive = (bases: string[]) => bases.some((b) => pathname === b || pathname.startsWith(`${b}/`));

  return (
    <header className="relative z-40 bg-card">
      <div className="hidden sm:block">
        <TopStrip home={home} />
      </div>
      <div className="border-b border-line">
        <div className="mx-auto flex h-16 max-w-[1400px] items-center gap-3 px-4 sm:px-6">
          <Link to="/" aria-label={`${NETWORK_NAME} Explorer home`} className="flex shrink-0 items-center gap-2.5">
            <LogoMark height={28} width={28} />
            <LogoWordmark height={15} className="w-auto" />
            <span className="rounded-full border border-gold/40 bg-gold/10 px-2 py-0.5 text-[10px] font-bold uppercase tracking-[0.12em] text-link">
              Explorer
            </span>
          </Link>

          <nav aria-label="Main" className="ml-auto hidden items-center gap-1 lg:flex">
            <Link
              to="/"
              className={cx("rounded-full px-3 py-1.5 text-[14px] font-medium transition-colors", home ? "text-link" : "text-fg hover:text-link")}
            >
              Home
            </Link>
            {NAV.map((n) => (
              <NavDropdown key={n.label} label={n.label} items={n.items} active={isActive(n.base)} />
            ))}
          </nav>

          <div className="ml-auto flex items-center gap-2 lg:hidden">
            <span className="sm:hidden">
              <ThemeMenu />
            </span>
            <button
              type="button"
              className="inline-flex h-8 w-8 items-center justify-center rounded-full border border-line bg-card text-muted hover:text-fg"
              aria-label={open ? "Close menu" : "Open menu"}
              aria-expanded={open}
              aria-controls="mobile-menu"
              onClick={() => setOpen((o) => !o)}
            >
              {open ? <X className="h-4 w-4" /> : <MenuIcon className="h-4 w-4" />}
            </button>
          </div>
        </div>
        {!home ? (
          <div className="px-4 pb-3 md:hidden">
            <SearchBar />
          </div>
        ) : null}
      </div>

      {open ? (
        <div id="mobile-menu" className="fade-in border-b border-line bg-card lg:hidden">
          <nav aria-label="Main" className="mx-auto max-w-[1400px] px-4 py-2 sm:px-6">
            <Link to="/" className="block rounded-xl px-3 py-2 text-[14px] font-medium text-fg hover:bg-hover">
              Home
            </Link>
            {NAV.map((n) => (
              <MobileSection key={n.label} title={n.label}>
                {n.items.map((it) => (
                  <Link
                    key={`${it.to}:${it.label}`}
                    to={it.to}
                    search={(it.search ?? {}) as never}
                    className="block rounded-xl px-3 py-2 text-[14px] text-fg hover:bg-hover"
                  >
                    {it.label}
                  </Link>
                ))}
              </MobileSection>
            ))}
            <div className="mt-2 border-t border-line pt-3 pb-2">
              <AddNetworkButton className="w-full" />
            </div>
          </nav>
        </div>
      ) : null}
    </header>
  );
}

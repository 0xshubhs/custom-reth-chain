/**
 * Floating UI without a library: tooltips, popovers and dropdown menus. Panels
 * render into document.body with fixed positioning, so a table that scrolls
 * sideways (overflow: auto) never clips them. They close on Escape and on a
 * click outside, and never use browser dialogs.
 */
import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import { CircleHelp } from "lucide-react";

import { cx } from "@/lib/cx";

type Place = "bottom-start" | "bottom-end" | "bottom" | "top";

function usePosition(anchor: RefObject<HTMLElement | null>, panel: RefObject<HTMLElement | null>, open: boolean, place: Place) {
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);
  const update = useCallback(() => {
    const a = anchor.current?.getBoundingClientRect();
    const p = panel.current;
    if (!a || !p) return;
    const pw = p.offsetWidth;
    const ph = p.offsetHeight;
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    const m = 8;
    let left =
      place === "bottom-end" ? a.right - pw : place === "bottom" || place === "top" ? a.left + a.width / 2 - pw / 2 : a.left;
    left = Math.max(m, Math.min(left, vw - pw - m));
    let top = place === "top" ? a.top - ph - 6 : a.bottom + 6;
    if (place !== "top" && top + ph > vh - m && a.top - ph - 6 > m) top = a.top - ph - 6;
    if (place === "top" && top < m) top = a.bottom + 6;
    setPos({ top, left });
  }, [anchor, panel, place]);

  useLayoutEffect(() => {
    if (!open) {
      setPos(null);
      return;
    }
    update();
    window.addEventListener("resize", update);
    window.addEventListener("scroll", update, true);
    return () => {
      window.removeEventListener("resize", update);
      window.removeEventListener("scroll", update, true);
    };
  }, [open, update]);
  return pos;
}

function useDismiss(open: boolean, close: () => void, refs: RefObject<HTMLElement | null>[]) {
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent | TouchEvent) => {
      const t = e.target as Node;
      if (refs.some((r) => r.current?.contains(t))) return;
      close();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close();
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("touchstart", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("touchstart", onDown);
      document.removeEventListener("keydown", onKey);
    };
    // refs are stable objects
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, close]);
}

/* Tooltip ------------------------------------------------------------------- */

export function Tooltip({
  content,
  children,
  place = "top",
  className,
}: {
  content: ReactNode;
  children: ReactNode;
  place?: Place;
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  const anchor = useRef<HTMLSpanElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const id = useId();
  const pos = usePosition(anchor, panel, open, place);
  return (
    <>
      <span
        ref={anchor}
        className={cx("inline-flex", className)}
        onMouseEnter={() => setOpen(true)}
        onMouseLeave={() => setOpen(false)}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        aria-describedby={open ? id : undefined}
      >
        {children}
      </span>
      {open
        ? createPortal(
            <div
              ref={panel}
              id={id}
              role="tooltip"
              style={{ top: pos?.top ?? -9999, left: pos?.left ?? -9999 }}
              className="pop-in pointer-events-none fixed z-[70] max-w-[280px] rounded-lg bg-[var(--tip-bg)] px-2.5 py-1.5 text-xs leading-snug text-[var(--tip-fg)] shadow-[var(--shadow)]"
            >
              {content}
            </div>,
            document.body,
          )
        : null}
    </>
  );
}

/** Etherscan's "?" next to a field label. */
export function HelpTip({ text }: { text: string }) {
  return (
    <Tooltip content={text}>
      <span tabIndex={0} className="inline-flex cursor-help text-dim hover:text-fg" aria-label={text}>
        <CircleHelp className="h-3.5 w-3.5" />
      </span>
    </Tooltip>
  );
}

/* Popover ------------------------------------------------------------------- */

export function Popover({
  trigger,
  children,
  place = "bottom-start",
  label,
  panelClassName,
  triggerClassName,
}: {
  trigger: ReactNode;
  children: ReactNode | ((close: () => void) => ReactNode);
  place?: Place;
  label: string;
  panelClassName?: string;
  triggerClassName?: string;
}) {
  const [open, setOpen] = useState(false);
  const anchor = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const close = useCallback(() => setOpen(false), []);
  useDismiss(open, close, [anchor, panel]);
  const pos = usePosition(anchor, panel, open, place);
  return (
    <>
      <button
        ref={anchor}
        type="button"
        aria-label={label}
        aria-expanded={open}
        aria-haspopup="dialog"
        onClick={(e) => {
          e.stopPropagation();
          setOpen((o) => !o);
        }}
        className={triggerClassName}
      >
        {trigger}
      </button>
      {open
        ? createPortal(
            <div
              ref={panel}
              role="dialog"
              aria-label={label}
              style={{ top: pos?.top ?? -9999, left: pos?.left ?? -9999 }}
              className={cx(
                "pop-in fixed z-[60] rounded-2xl border border-line bg-card text-sm shadow-[var(--shadow)]",
                panelClassName,
              )}
            >
              {typeof children === "function" ? children(close) : children}
            </div>,
            document.body,
          )
        : null}
    </>
  );
}

/* Dropdown menu ------------------------------------------------------------ */

export interface MenuItem {
  key: string;
  label: ReactNode;
  hint?: ReactNode;
  selected?: boolean;
  onSelect: () => void;
}

/**
 * A dropdown that opens on click, and on hover for a mouse (Etherscan's nav
 * menus). Arrow keys move between items.
 */
export function Menu({
  label,
  button,
  items,
  place = "bottom-start",
  hover = false,
  buttonClassName,
  panelClassName,
}: {
  label: string;
  button: ReactNode;
  items: MenuItem[];
  place?: Place;
  hover?: boolean;
  buttonClassName?: string;
  panelClassName?: string;
}) {
  const [open, setOpen] = useState(false);
  const anchor = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const close = useCallback(() => setOpen(false), []);
  useDismiss(open, close, [anchor, panel]);
  const pos = usePosition(anchor, panel, open, place);

  const enter = () => {
    if (!hover) return;
    if (timer.current) clearTimeout(timer.current);
    setOpen(true);
  };
  const leave = () => {
    if (!hover) return;
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setOpen(false), 140);
  };

  const focusItem = (dir: 1 | -1) => {
    const els = Array.from(panel.current?.querySelectorAll<HTMLButtonElement>("[role=menuitem]") ?? []);
    if (!els.length) return;
    const i = els.indexOf(document.activeElement as HTMLButtonElement);
    els[(i + dir + els.length) % els.length]?.focus();
  };

  return (
    <>
      <button
        ref={anchor}
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={label}
        onClick={() => setOpen((o) => !o)}
        onMouseEnter={enter}
        onMouseLeave={leave}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown") {
            e.preventDefault();
            setOpen(true);
            setTimeout(() => focusItem(1), 0);
          }
        }}
        className={buttonClassName}
      >
        {button}
      </button>
      {open
        ? createPortal(
            <div
              ref={panel}
              role="menu"
              aria-label={label}
              onMouseEnter={enter}
              onMouseLeave={leave}
              onKeyDown={(e) => {
                if (e.key === "ArrowDown") {
                  e.preventDefault();
                  focusItem(1);
                } else if (e.key === "ArrowUp") {
                  e.preventDefault();
                  focusItem(-1);
                }
              }}
              style={{ top: pos?.top ?? -9999, left: pos?.left ?? -9999 }}
              className={cx(
                "pop-in fixed z-[60] min-w-[200px] rounded-2xl border border-line bg-card p-1.5 shadow-[var(--shadow)]",
                panelClassName,
              )}
            >
              {items.map((it) => (
                <button
                  key={it.key}
                  type="button"
                  role="menuitem"
                  aria-current={it.selected || undefined}
                  onClick={() => {
                    it.onSelect();
                    setOpen(false);
                  }}
                  className={cx(
                    "flex w-full items-center justify-between gap-4 rounded-xl px-3 py-2 text-left text-[13px] transition-colors",
                    it.selected ? "bg-raised font-semibold text-bright" : "text-fg hover:bg-hover",
                  )}
                >
                  <span>{it.label}</span>
                  {it.hint ? <span className="text-xs text-dim">{it.hint}</span> : null}
                </button>
              ))}
            </div>,
            document.body,
          )
        : null}
    </>
  );
}

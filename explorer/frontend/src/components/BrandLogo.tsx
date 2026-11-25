import type { SVGProps } from "react";
import { NETWORK_NAME } from "@/lib/site";

/**
 * Monochrome mark and wordmark. Both draw in currentColor, so they follow the
 * theme's text colour (black on light, white on dim and dark): this explorer
 * is black and white only. The wordmark is plain text, so changing the chain's
 * name is a text edit, not a new drawing.
 */

/** The wordmark text: the chain name set at build time (VITE_NETWORK_NAME). */
export const BRAND_NAME = NETWORK_NAME;

type LogoProps = Omit<SVGProps<SVGSVGElement>, "viewBox" | "xmlns">;

/** A square block with a notch: a chain block, nothing more. */
export function LogoMark(props: LogoProps) {
  return (
    <svg viewBox="0 0 32 32" xmlns="http://www.w3.org/2000/svg" aria-hidden role="img" fill="currentColor" {...props}>
      <rect x="2" y="2" width="28" height="28" rx="7" />
      <rect x="9" y="9" width="14" height="14" rx="3" fill="var(--card)" />
      <rect x="13" y="13" width="6" height="6" rx="1.5" />
    </svg>
  );
}

export function LogoWordmark({ height = 15, className, style }: LogoProps) {
  const h = typeof height === "number" ? height : Number(height) || 15;
  return (
    <span
      aria-label={BRAND_NAME}
      className={className}
      style={{ fontSize: h * 1.15, lineHeight: 1, fontWeight: 700, letterSpacing: "-0.01em", color: "var(--text-bright)", ...style }}
    >
      {BRAND_NAME}
    </span>
  );
}

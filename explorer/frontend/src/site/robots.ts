/**
 * robots.txt (written to dist/ by scripts/generate-static-meta.mjs).
 *
 * The explorer is public: every block, transaction and address page may be
 * indexed and cited. The API is not a page and is kept out. Known SEO
 * scrapers are blocked to cut load on the per-IP rate limits.
 */
import type { RobotsFile } from "./types";

const SITE_URL = import.meta.env.VITE_SITE_URL || "http://localhost:3000";

export default function robots(): RobotsFile {
  return {
    rules: [
      { userAgent: "*", allow: "/", disallow: "/api/" },
      { userAgent: "AhrefsBot", disallow: "/" },
      { userAgent: "SemrushBot", disallow: "/" },
      { userAgent: "MJ12bot", disallow: "/" },
      { userAgent: "dotbot", disallow: "/" },
    ],
    // No sitemap: pages are one per block/transaction/address and change by the minute.
    host: SITE_URL,
  };
}

/**
 * Writes the static site files a SPA cannot generate per request:
 *
 *     src/site/robots.ts    ->  dist/robots.txt
 *     src/site/manifest.ts  ->  dist/manifest.webmanifest
 *
 * It imports the real modules and serialises what they return, so the files
 * cannot drift from the source. Runs under Bun, which executes TypeScript
 * directly and provides `import.meta.env`.
 *
 * No sitemap: the explorer's pages are one per block, transaction and
 * address, and a list frozen at build time would be stale within minutes.
 *
 * Usage (already wired into `bun run build`):  bun scripts/generate-static-meta.mjs
 */
import { writeFileSync, existsSync, mkdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { pathToFileURL } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const APP_SRC = path.resolve(__dirname, "../src");
const OUT_DIR = path.resolve(__dirname, "../dist");

/** Serialise a RobotsFile (src/site/types.ts) into robots.txt syntax. */
function robotsToText(robots) {
  const lines = [];
  const rules = Array.isArray(robots.rules) ? robots.rules : [robots.rules];

  for (const rule of rules) {
    if (!rule) continue;
    const agents = Array.isArray(rule.userAgent) ? rule.userAgent : [rule.userAgent ?? "*"];
    for (const a of agents) lines.push(`User-agent: ${a}`);

    const allow = rule.allow ? (Array.isArray(rule.allow) ? rule.allow : [rule.allow]) : [];
    for (const p of allow) lines.push(`Allow: ${p}`);

    const disallow = rule.disallow ? (Array.isArray(rule.disallow) ? rule.disallow : [rule.disallow]) : [];
    for (const p of disallow) lines.push(`Disallow: ${p}`);

    if (rule.crawlDelay != null) lines.push(`Crawl-delay: ${rule.crawlDelay}`);
    lines.push("");
  }

  if (robots.host) lines.push(`Host: ${robots.host}`);
  const sitemaps = robots.sitemap
    ? Array.isArray(robots.sitemap) ? robots.sitemap : [robots.sitemap]
    : [];
  for (const s of sitemaps) lines.push(`Sitemap: ${s}`);

  return lines.join("\n").replace(/\n{3,}/g, "\n\n").trim() + "\n";
}

async function loadDefault(relPath) {
  const abs = path.join(APP_SRC, relPath);
  if (!existsSync(abs)) return null;
  const mod = await import(pathToFileURL(abs).href);
  return typeof mod.default === "function" ? await mod.default() : mod.default;
}

if (!existsSync(OUT_DIR)) mkdirSync(OUT_DIR, { recursive: true });

let wrote = 0;

/* robots.txt */
try {
  const robots = await loadDefault("site/robots.ts");
  if (robots) {
    writeFileSync(path.join(OUT_DIR, "robots.txt"), robotsToText(robots));
    console.log("  ok dist/robots.txt");
    wrote++;
  } else {
    console.log("  - src/site/robots.ts not present, skipped");
  }
} catch (err) {
  console.error(`  ! robots.txt failed: ${err.message}`);
  process.exitCode = 1;
}

/* manifest.webmanifest */
try {
  const manifest = await loadDefault("site/manifest.ts");
  if (manifest) {
    writeFileSync(
      path.join(OUT_DIR, "manifest.webmanifest"),
      JSON.stringify(manifest, null, 2) + "\n",
    );
    console.log("  ok dist/manifest.webmanifest");
    wrote++;
  } else {
    console.log("  - src/site/manifest.ts not present, skipped");
  }
} catch (err) {
  console.error(`  ! manifest.webmanifest failed: ${err.message}`);
  process.exitCode = 1;
}

console.log(`  static metadata: ${wrote} file(s) written`);

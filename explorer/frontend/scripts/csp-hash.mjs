/**
 * Last step of `bun run build`: checks dist/index.html and writes
 * Subresource Integrity into it, and keeps the CSP `script-src` line in
 * deploy/nginx.conf free of stale hashes. Same script as the other Meowchain
 * web apps.
 *
 * Inline scripts are not allowed at all. A CSP hash only matches if the
 * browser receives exactly the bytes that were built, and on 2026-11-20 the
 * user app and PA portal received a theme bootstrap whose bytes no longer
 * matched, so the browser blocked it. The theme bootstrap here is
 * public/theme-boot.js, which script-src 'self' allows whatever its bytes are.
 * This script fails the build if an executable inline script shows up in
 * dist/index.html, and strips any stale hash from the config.
 * Data blocks such as <script type="application/ld+json"> are not executed
 * and need no hash, so they do not count.
 *
 * Hashing rule, kept for the stale-hash cleanup: CSP hashes cover the EXACT
 * bytes between <script> and </script> (no trimming, no normalisation). HTML
 * comments are stripped first only so that a literal "<script>" mentioned
 * inside a comment cannot be mistaken for a real one.
 */
import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { createHash } from "node:crypto";
import path from "node:path";
import { fileURLToPath } from "node:url";

const WEB_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const INDEX_HTML = path.join(WEB_DIR, "dist", "index.html");
const NGINX_CONF = path.join(WEB_DIR, "deploy", "nginx.conf");

if (!existsSync(INDEX_HTML)) {
  console.error(`csp-hash: ${path.relative(WEB_DIR, INDEX_HTML)} not found, run vite build first.`);
  process.exit(1);
}
if (!existsSync(NGINX_CONF)) {
  console.error(`csp-hash: ${path.relative(WEB_DIR, NGINX_CONF)} not found.`);
  process.exit(1);
}

const html = readFileSync(INDEX_HTML, "utf8").replace(/<!--[\s\S]*?-->/g, "");
const inline = [...html.matchAll(/<script(?![^>]*\bsrc=)([^>]*)>([\s\S]*?)<\/script>/g)];

// Executable = no type, or a JavaScript/module type. Anything else is data.
const JS_TYPE = /^\s*$|^(?:module|(?:text|application)\/(?:java|ecma)script)$/i;
const executable = inline.filter((m) => {
  const type = m[1].match(/\btype\s*=\s*["']?([^"'\s>]*)/i);
  return JS_TYPE.test(type ? type[1] : "");
});
if (executable.length > 0) {
  console.error(
    `csp-hash: dist/index.html has ${executable.length} inline <script>. Move it into public/ ` +
      `and load it with <script src="/name.js"></script> instead, as the theme bootstrap does. ` +
      `An inline script needs a CSP hash of its exact bytes, and a hash that stops matching ` +
      `the delivered page blocks the script silently.`,
  );
  process.exit(1);
}

// Subresource Integrity
// Every <script src>, <link rel="modulepreload"> and <link rel="stylesheet">
// the shell loads from /assets/ gets integrity="sha384-..." of the file vite
// wrote, plus crossorigin, so a bundle changed anywhere after the build (on the
// server, in a cache or proxy on the way) is refused by the browser instead of
// run. Only /assets/: those names carry a content hash, so one URL always
// means the same bytes. A dist-root file such as /theme-boot.js keeps its name
// from build to build, and a cached copy (the browser's, or Cloudflare's, which
// keeps .js it gets no Cache-Control for) could meet a newer shell holding a
// newer hash; SRI would then block it. Chunks the app imports later are not
// named in the shell and load as before: the entry's integrity covers only the
// entry. Before this was added, Cloudflare was checked to deliver the live
// assets byte for byte as the origin serves them (2026-11-23).
function addIntegrity(source) {
  let tagged = 0;
  const out = source.replace(/<!--[\s\S]*?-->|<(script|link)\b[^>]*>/gi, (tag, name) => {
    if (!name) return tag; // an HTML comment: left exactly as it is
    const isScript = name.toLowerCase() === "script";
    if (!isScript) {
      const rel = tag.match(/\brel\s*=\s*["']?([^"'\s>]+)/i);
      if (!rel || !/^(?:modulepreload|stylesheet)$/i.test(rel[1])) return tag;
    }
    const ref = tag.match(isScript ? /\bsrc\s*=\s*["']([^"']+)["']/i : /\bhref\s*=\s*["']([^"']+)["']/i);
    if (!ref || !ref[1].startsWith("/assets/")) return tag;
    const file = path.join(WEB_DIR, "dist", ref[1].split(/[?#]/)[0]);
    if (!file.startsWith(path.join(WEB_DIR, "dist", "assets") + path.sep) || !existsSync(file)) {
      console.error(`csp-hash: dist/index.html loads ${ref[1]}, which is not a file in dist/assets.`);
      process.exit(1);
    }
    const integrity = `sha384-${createHash("sha384").update(readFileSync(file)).digest("base64")}`;
    tagged++;
    let next = tag.replace(/\s+integrity\s*=\s*(["'])[^"']*\1/i, "");
    if (!/\scrossorigin\b/i.test(next)) next = next.replace(/\s*\/?>$/, (end) => ` crossorigin${end}`);
    return next.replace(/\s*\/?>$/, (end) => ` integrity="${integrity}"${end}`);
  });
  return { out, tagged };
}

const shell = readFileSync(INDEX_HTML, "utf8");
const { out: shellWithSri, tagged } = addIntegrity(shell);
if (tagged === 0 || !/<script\b[^>]*\bsrc="\/assets\/[^"]+"[^>]*\bintegrity="sha384-/.test(shellWithSri)) {
  console.error("csp-hash: found no /assets/ entry script in dist/index.html to add integrity to. Did the vite output change?");
  process.exit(1);
}
if (shellWithSri !== shell) writeFileSync(INDEX_HTML, shellWithSri);
console.log(`csp-hash: integrity on ${tagged} script/stylesheet tag${tagged === 1 ? "" : "s"} in dist/index.html`);

// Nothing executable is inline, so script-src must carry no hash at all.
const hashes = [];

const conf = readFileSync(NGINX_CONF, "utf8");

// The CSP is assembled one directive per `set $csp` line (see the comment in
// deploy/nginx.conf for why it is not one long quoted string), so `script-src`
// owns a whole line, so match it as such.
const SCRIPT_SRC = /^(\s*set \$csp "\$csp; script-src )(.*?)(";)$/m;
const line = conf.match(SCRIPT_SRC);
if (!line) {
  console.error('csp-hash: no `set $csp "$csp; script-src ..."` line in deploy/nginx.conf. Did the CSP layout change?');
  process.exit(1);
}

// Drop whatever hashes are there now, then re-add the current ones in the same
// slot (right after the keyword sources, before any host allowlist) so the diff
// stays readable.
const sources = line[2].split(/\s+/).filter((s) => !s.startsWith("'sha256-"));
const keywordEnd = sources.findIndex((s) => !s.startsWith("'"));
const insertAt = keywordEnd === -1 ? sources.length : keywordEnd;
const rebuilt = [...sources.slice(0, insertAt), ...hashes, ...sources.slice(insertAt)].join(" ");

const next = conf.replace(SCRIPT_SRC, `${line[1]}${rebuilt}${line[3]}`);

if (next === conf) {
  console.log(`csp-hash: up to date (${hashes.length} inline script${hashes.length === 1 ? "" : "s"})`);
} else {
  writeFileSync(NGINX_CONF, next);
  console.log("csp-hash: deploy/nginx.conf updated");
  console.log(`  was: script-src ${line[2]}`);
  console.log(`  now: script-src ${rebuilt}`);
  console.log("  ^ commit this and redeploy the nginx config with the bundle.");
}

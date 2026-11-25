// Theme bootstrap: sets data-theme on <html> before first paint, so there is
// no flash of the wrong theme. index.html loads it as a plain blocking
// <script src> (no module, no defer), which runs before the first paint.
//
// Three themes: "light", "dim", "dark". The saved choice wins; with none (or
// an old "system" value) the page follows the system: light or dark.
//
// It is a file and not an inline <script> on purpose: an inline script is
// only allowed by a CSP hash of its exact bytes, and a hash that stops
// matching blocks it silently. A same-origin file is allowed by
// script-src 'self'. scripts/csp-hash.mjs fails the build if an inline
// script comes back.
(function () {
  var t = null;
  try {
    t = localStorage.getItem("theme");
  } catch (e) {}
  if (t !== "light" && t !== "dim" && t !== "dark") {
    try {
      t = window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
    } catch (e) {
      t = "light";
    }
  }
  document.documentElement.setAttribute("data-theme", t);
})();

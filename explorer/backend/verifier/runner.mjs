// runner.mjs: compile one solc standard JSON input with an official soljson build.
//
//   node runner.mjs <soljson.js>  < input.json  > output.json
//
// The verifier service (cmd/verifier) runs this once per compile, in its own
// process, so a crash, an abort or a runaway build inside the wasm compiler
// never takes the service down, and a timeout is a plain process kill.
//
// Only the raw C export is used (solidity_compile, or compileStandard on
// 0.4.x), through emscripten's cwrap, with no import callback: standard JSON
// carries the content of every source, so the compiler never has to read a
// file. No npm packages: the soljson file is the whole compiler.
//
// stdout carries the compiler's output JSON and nothing else. Anything the
// emscripten runtime prints is sent to stderr, which the service logs.
//
// Exit codes: 0 output written (it may still contain compile errors, which
// are part of the output JSON), 1 the compiler could not be loaded or run,
// 2 bad usage.

import { createRequire } from "node:module";
import path from "node:path";

function fail(code, message) {
  process.stderr.write(`runner: ${message}\n`);
  process.exit(code);
}

const soljsonPath = process.argv[2];
if (!soljsonPath) fail(2, "usage: node runner.mjs <soljson.js> < input.json");

// The emscripten runtime binds its print functions to console.log and
// console.warn when it loads. Point them at stderr before that happens.
const toStderr = (...args) => process.stderr.write(args.join(" ") + "\n");
console.log = toStderr;
console.info = toStderr;
console.warn = toStderr;
console.debug = toStderr;

async function readStdin() {
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  return Buffer.concat(chunks).toString("utf8");
}

function loadCompiler(file) {
  const require = createRequire(import.meta.url);
  let soljson;
  try {
    soljson = require(path.resolve(file));
  } catch (err) {
    fail(1, `cannot load ${file}: ${err && err.message ? err.message : err}`);
  }
  if (!soljson || typeof soljson.cwrap !== "function") {
    fail(1, `${file} is not an emscripten soljson build (no cwrap)`);
  }
  // 0.5.0 and later: solidity_compile(input, readCallback[, context]).
  // Passing 0 for both pointers means "no import callback". 0.5.x takes only
  // two arguments; the extra one is ignored by the wasm call.
  if ("_solidity_compile" in soljson) {
    const compile = soljson.cwrap("solidity_compile", "string", ["string", "number", "number"]);
    return (input) => compile(input, 0, 0);
  }
  // 0.4.11 to 0.4.26: compileStandard(input, readCallback).
  if ("_compileStandard" in soljson) {
    const compile = soljson.cwrap("compileStandard", "string", ["string", "number"]);
    return (input) => compile(input, 0);
  }
  fail(1, "this compiler version has no standard JSON interface (needs 0.4.11 or later)");
  return null;
}

const input = await readStdin();
if (!input.trim()) fail(2, "empty input on stdin");

const compile = loadCompiler(soljsonPath);
let output;
try {
  output = compile(input);
} catch (err) {
  fail(1, `compiler crashed: ${err && err.message ? err.message : err}`);
}
if (typeof output !== "string" || output.length === 0) fail(1, "compiler returned no output");

// On a pipe, stdout writes can be asynchronous: exit only once the whole
// output has been handed to the OS.
process.stdout.write(output, (err) => {
  if (err) fail(1, `writing output: ${err.message}`);
  process.exit(0);
});

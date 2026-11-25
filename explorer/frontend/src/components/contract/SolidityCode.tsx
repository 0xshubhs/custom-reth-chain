/**
 * Solidity source with syntax colours and line numbers, in all three themes.
 *
 * A small tokenizer of our own rather than a highlighting library: no extra
 * bundle weight, no generated HTML strings (every token is a React text node,
 * so a verified file can never inject markup), and nothing the page's strict
 * CSP has to allow. Colours come from the --sol-* tokens in globals.css.
 * Line numbers are CSS counters, so selecting and copying the code copies the
 * code only.
 */
import { useMemo } from "react";

type Cls = "kw" | "type" | "str" | "num" | "com" | "tag" | "def" | "builtin" | "plain";
interface Tok {
  cls: Cls;
  text: string;
}

const KEYWORDS = new Set(
  (
    "pragma solidity import from as abstract contract interface library is using for " +
    "function modifier event error struct enum mapping constructor fallback receive " +
    "returns return if else while do for break continue emit revert require assert " +
    "try catch new delete unchecked assembly type " +
    "public private internal external view pure payable virtual override " +
    "memory storage calldata constant immutable indexed anonymous " +
    "true false this super wei gwei ether seconds minutes hours days weeks"
  ).split(" "),
);
const TYPES = /^(?:u?int(?:8|16|24|32|40|48|56|64|72|80|88|96|104|112|120|128|136|144|152|160|168|176|184|192|200|208|216|224|232|240|248|256)?|address|bool|string|bytes(?:[1-9]|[12]\d|3[0-2])?|fixed|ufixed|var)$/;
const BUILTINS = new Set(
  "msg block tx abi keccak256 sha256 ripemd160 ecrecover addmod mulmod selfdestruct gasleft blockhash".split(" "),
);
/** The name that follows one of these words is a declaration. */
const DECLARES = new Set("contract interface library function modifier event error struct enum".split(" "));

const TOKEN_RE =
  /(\/\/[^\n]*|\/\*[\s\S]*?(?:\*\/|$))|((?:hex|unicode)?"(?:[^"\\\n]|\\.)*"|(?:hex|unicode)?'(?:[^'\\\n]|\\.)*')|(\b0x[0-9a-fA-F_]+\b|\b\d[\d_]*(?:\.\d+)?(?:e-?\d+)?\b)|([A-Za-z_$][A-Za-z0-9_$]*)/g;

function tokenize(src: string): Tok[] {
  const out: Tok[] = [];
  let last = 0;
  let prevWord = "";
  for (const m of src.matchAll(TOKEN_RE)) {
    const i = m.index ?? 0;
    if (i > last) out.push({ cls: "plain", text: src.slice(last, i) });
    const [whole, com, str, num, word] = m;
    if (com !== undefined) {
      // NatSpec tags (@dev, @param, @notice ...) stand out inside comments.
      // Only real tags: "@" right after a space, "*" or "/", then a lowercase word.
      for (const part of com.split(/((?<=[\s*/])@[a-z][a-z-]*(?::[a-z-]+)?)/)) {
        if (part) out.push({ cls: /^@[a-z]/.test(part) ? "tag" : "com", text: part });
      }
    } else if (str !== undefined) {
      out.push({ cls: "str", text: str });
    } else if (num !== undefined) {
      out.push({ cls: "num", text: num });
    } else if (word !== undefined) {
      let cls: Cls = "plain";
      if (DECLARES.has(prevWord)) cls = "def";
      else if (KEYWORDS.has(word)) cls = "kw";
      else if (TYPES.test(word)) cls = "type";
      else if (BUILTINS.has(word)) cls = "builtin";
      out.push({ cls, text: word });
      prevWord = word;
    } else {
      out.push({ cls: "plain", text: whole });
    }
    last = i + whole.length;
  }
  if (last < src.length) out.push({ cls: "plain", text: src.slice(last) });
  return out;
}

/** Splits tokens at newlines so every line renders as its own row. */
function toLines(toks: Tok[]): Tok[][] {
  const lines: Tok[][] = [[]];
  for (const t of toks) {
    const parts = t.text.split("\n");
    parts.forEach((p, k) => {
      if (k > 0) lines.push([]);
      if (p) lines[lines.length - 1].push({ cls: t.cls, text: p });
    });
  }
  return lines;
}

export function SolidityCode({ code, className }: { code: string; className?: string }) {
  const lines = useMemo(() => toLines(tokenize(code)), [code]);
  return (
    <pre className={className}>
      <code className="sol-code">
        {lines.map((line, n) => (
          <span key={n} className="sol-line">
            {line.map((t, k) =>
              t.cls === "plain" ? t.text : (
                <span key={k} className={`sol-${t.cls}`}>
                  {t.text}
                </span>
              ),
            )}
            {"\n"}
          </span>
        ))}
      </code>
    </pre>
  );
}

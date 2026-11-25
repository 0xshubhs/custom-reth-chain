/**
 * The filter box above a list (tokens, verified contracts). It keeps its own
 * text while typing and reports a debounced value, so the URL and the query
 * only change once the user pauses.
 */
import { useEffect, useRef, useState } from "react";
import { Search, X } from "lucide-react";

export function ListFilter({
  value,
  onChange,
  placeholder,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder: string;
}) {
  const [text, setText] = useState(value);
  const cb = useRef(onChange);
  cb.current = onChange;

  // Follow outside changes (back button, a cleared filter).
  useEffect(() => setText(value), [value]);

  useEffect(() => {
    if (text.trim() === value) return;
    const t = setTimeout(() => cb.current(text.trim()), 350);
    return () => clearTimeout(t);
  }, [text, value]);

  return (
    <div className="relative w-full sm:max-w-xs">
      <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-dim" aria-hidden />
      <input
        className="input !pl-9 !pr-9"
        value={text}
        onChange={(e) => setText(e.target.value)}
        placeholder={placeholder}
        aria-label={placeholder}
        spellCheck={false}
        autoComplete="off"
      />
      {text ? (
        <button
          type="button"
          aria-label="Clear filter"
          onClick={() => {
            setText("");
            onChange("");
          }}
          className="absolute right-2 top-1/2 -translate-y-1/2 rounded-md p-1 text-dim hover:bg-hover hover:text-fg"
        >
          <X className="h-4 w-4" />
        </button>
      ) : null}
    </div>
  );
}

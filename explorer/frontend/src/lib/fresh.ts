import { useEffect, useRef, useState } from "react";

/**
 * Keys that appeared in a list since the previous render of it (not on the
 * first load), so new rows can flash in when the live feed adds them.
 */
export function useFresh<T, K extends string | number>(items: T[] | undefined, key: (t: T) => K): Set<K> {
  const seen = useRef<Set<K> | null>(null);
  const [fresh, setFresh] = useState<Set<K>>(() => new Set());
  const keyRef = useRef(key);
  keyRef.current = key;

  useEffect(() => {
    if (!items) return;
    const keys = items.map((t) => keyRef.current(t));
    if (seen.current) {
      const prev = seen.current;
      const f = new Set(keys.filter((k) => !prev.has(k)));
      if (f.size) setFresh(f);
      keys.forEach((k) => prev.add(k));
    } else {
      seen.current = new Set(keys);
    }
  }, [items]);

  return fresh;
}

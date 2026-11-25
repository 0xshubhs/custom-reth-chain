/**
 * The "Default View (decoded)" of a transaction's input data: the API's
 * decoding (verified ABI or the common token methods) as a parameter table,
 * replaced by the in-browser ETH.sh decoder when that can do more (nested
 * calls, batches, our signature list, a labelled guess).
 */
import type { Tx } from "@/lib/types";
import { DecodedParams } from "./decoded";

export function InputDecoded({ tx }: { tx: Tx }) {
  const d = tx.decoded_input;
  if (!d) {
    return (
      <p className="rounded-xl border border-line bg-raised px-3 py-2 text-[13px] text-muted">
        Not decoded: the contract is not verified and the method is not a known signature.
      </p>
    );
  }
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <p className="break-all font-mono text-[12.5px] text-fg2">
        Function: <span className="font-semibold text-fg">{d.signature}</span>
      </p>
      <DecodedParams params={d.params} />
    </div>
  );
}

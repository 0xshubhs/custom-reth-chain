/**
 * "Add the chain to MetaMask". Chain id, name, symbol and RPC come from
 * /api/v2/config (built-in defaults until it answers). Results are shown as
 * toasts, never browser dialogs.
 */
import { useState } from "react";
import { Plus, Wallet } from "lucide-react";
import { toast } from "sonner";

import { useChain } from "@/lib/chain";
import { addNetwork } from "@/lib/wallet";
import { Spinner, cx } from "./ui";

export function AddNetworkButton({
  variant = "default",
  label,
  className,
}: {
  variant?: "default" | "primary" | "compact";
  label?: string;
  className?: string;
}) {
  const cfg = useChain();
  const [busy, setBusy] = useState(false);
  const text = label ?? `Add ${cfg.network_name} to MetaMask`;

  async function onClick() {
    setBusy(true);
    const r = await addNetwork(cfg);
    setBusy(false);
    if (r.ok) {
      toast.success(`${cfg.network_name} is in your wallet`, {
        description: `Chain ID ${cfg.chain_id}, ${cfg.native_symbol}.`,
      });
    } else if (r.reason === "no-wallet") {
      toast.error("No browser wallet found", {
        description: `Install MetaMask (metamask.io), then add ${cfg.network_name} with RPC ${cfg.public_rpc_url} and chain ID ${cfg.chain_id}.`,
        duration: 9000,
      });
    } else if (r.reason === "rejected") {
      toast("Request declined", { description: r.message });
    } else {
      toast.error("Could not add the network", { description: r.message });
    }
  }

  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      title={`Add ${cfg.network_name} (chain ID ${cfg.chain_id}) to your wallet`}
      className={cx("btn", variant === "primary" && "btn-primary", variant === "compact" && "btn-sm", className)}
    >
      {busy ? <Spinner className="h-4 w-4" /> : <Wallet className="h-4 w-4" aria-hidden />}
      <span>{text}</span>
      {variant === "compact" ? null : <Plus className="h-4 w-4 opacity-70" aria-hidden />}
    </button>
  );
}

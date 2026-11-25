/** Small pieces shared by more than one route file. */
import { Wallet } from "lucide-react";

import type { AddressDetail } from "@/lib/types";
import { Card, EmptyState } from "./ui";

export function AddressInvalid({ value, what = "address" }: { value: string; what?: string }) {
  return (
    <Card>
      <EmptyState title={`That is not a valid ${what}`} icon={<Wallet className="h-5 w-5" />}>
        An address is <span className="font-mono">0x</span> followed by 40 hexadecimal characters.{" "}
        <span className="break-all font-mono text-fg">{value}</span> is not one.
      </EmptyState>
    </Card>
  );
}

export function proxyLabel(p: AddressDetail["proxy_type"]): string {
  switch (p) {
    case "eip1967":
      return "EIP-1967 proxy";
    case "eip1967-beacon":
      return "EIP-1967 beacon proxy";
    case "trex":
      return "T-REX proxy";
    default:
      return "Proxy";
  }
}

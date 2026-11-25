import { createFileRoute } from "@tanstack/react-router";
import { BadgeCheck } from "lucide-react";

import { PageTitle } from "@/components/ui";
import { VerifyForm } from "@/components/VerifyForm";
import { strParam } from "@/lib/search";
import { pageHead } from "@/lib/seo";
import { NETWORK_NAME } from "@/lib/site";

export const Route = createFileRoute("/contract-verification")({
  validateSearch: (s: Record<string, unknown>): { address?: string } => ({ address: strParam(s.address) }),
  head: () =>
    pageHead("Verify a contract", `Publish and verify the Solidity source of a contract on the ${NETWORK_NAME} chain.`),
  component: VerifyPage,
});

function VerifyPage() {
  const { address } = Route.useSearch();
  return (
    <>
      <PageTitle
        title="Verify and publish a contract"
        icon={<BadgeCheck className="h-5 w-5" />}
        subtitle="Upload the Solidity standard JSON input the contract was compiled from. The explorer compiles it and compares the result with the code on chain."
      />
      <VerifyForm key={address ?? ""} initialAddress={address} />
    </>
  );
}

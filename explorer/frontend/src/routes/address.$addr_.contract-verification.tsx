import { createFileRoute, Link } from "@tanstack/react-router";
import { BadgeCheck } from "lucide-react";

import { AddressInvalid } from "@/components/common";
import { PageTitle } from "@/components/ui";
import { VerifyForm } from "@/components/VerifyForm";
import { isAddress } from "@/lib/format";
import { pageHead } from "@/lib/seo";
import { NETWORK_NAME } from "@/lib/site";

export const Route = createFileRoute("/address/$addr_/contract-verification")({
  head: () =>
    pageHead("Verify a contract", `Publish and verify the Solidity source of a contract on the ${NETWORK_NAME} chain.`),
  component: VerifyAddressPage,
});

function VerifyAddressPage() {
  const { addr } = Route.useParams();
  if (!isAddress(addr)) return <AddressInvalid value={addr} />;
  return (
    <>
      <PageTitle
        title="Verify and publish a contract"
        icon={<BadgeCheck className="h-5 w-5" />}
        subtitle={
          <span>
            For{" "}
            <Link to="/address/$addr" params={{ addr }} className="link break-all font-mono text-[13px]">
              {addr}
            </Link>
            . Upload the Solidity standard JSON input it was compiled from.
          </span>
        }
      />
      <VerifyForm key={addr} fixedAddress={addr} />
    </>
  );
}

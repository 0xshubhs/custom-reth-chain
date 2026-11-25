/**
 * The contract tab of an address (and of a token): verified source files with
 * copy buttons, compiler settings, the ABI, bytecode, and the read-contract
 * form. Loaded lazily, so viem only downloads when someone opens this tab.
 *
 * A proxy reads through its implementation: the API hands over
 * `implementation_abi`, and calls still go to the proxy's address.
 */
import { useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { ChevronDown, FileCode2, Layers, ShieldAlert, ShieldCheck } from "lucide-react";

import { ApiError, useApi } from "@/lib/api";
import { formatDateTime } from "@/lib/format";
import type { SmartContract } from "@/lib/types";
import { proxyLabel } from "../common";
import { stringify } from "../decoded";
import { AddressHashLink, AddressLink } from "../links";
import { Badge, Card, CardHeader, CopyButton, DetailSkeleton, ErrorState, cx } from "../ui";
import ReadContract from "./ReadContract";
import { SolidityCode } from "./SolidityCode";

type View = "code" | "abi" | "read";

function licenseLabel(l: string | null): string {
  if (!l || l === "none") return "None";
  const map: Record<string, string> = {
    unlicense: "Unlicense",
    mit: "MIT",
    gnu_gpl_v2: "GNU GPLv2",
    gnu_gpl_v3: "GNU GPLv3",
    gnu_lgpl_v2_1: "GNU LGPLv2.1",
    gnu_lgpl_v3: "GNU LGPLv3",
    bsd_2_clause: "BSD 2-clause",
    bsd_3_clause: "BSD 3-clause",
    mpl_2_0: "MPL 2.0",
    osl_3_0: "OSL 3.0",
    apache_2_0: "Apache 2.0",
    gnu_agpl_v3: "GNU AGPLv3",
    bsl_1_1: "BSL 1.1",
  };
  return map[l] ?? l;
}

const MATCH_TEXT: Record<string, string> = {
  full: "Full match: the compiled bytecode, metadata included, equals the code on chain.",
  partial: "Partial match: the bytecode equals the code on chain except for the metadata hash.",
  similar: "Verified through another contract with identical bytecode.",
  imported: "Imported from the previous explorer (Blockscout).",
};

export default function ContractTab({ addr, view: initial }: { addr: string; view: string }) {
  const q = useApi<SmartContract>(["contract", addr.toLowerCase()], `/smart-contracts/${addr}`);
  const [view, setView] = useState<View>(initial === "abi" ? "abi" : initial === "read" ? "read" : "code");
  const c = q.data;

  if (q.isError) {
    // No smart-contract record at all reads the same as "not verified".
    if (q.error instanceof ApiError && q.error.notFound) return <Unverified addr={addr} c={null} />;
    return (
      <Card>
        <ErrorState error={q.error} onRetry={() => q.refetch()} />
      </Card>
    );
  }
  if (!c) {
    return (
      <Card>
        <DetailSkeleton rows={6} />
      </Card>
    );
  }

  if (!c.is_verified) return <Unverified addr={addr} c={c} />;

  const readAbi = c.implementation_abi ?? c.abi;
  return (
    <div className="flex flex-col gap-4">
      {c.implementation ? (
        <div className="flex flex-wrap items-center gap-2 rounded-xl border border-info/30 bg-info/10 px-4 py-3 text-sm">
          <Layers className="h-4 w-4 shrink-0 text-info" />
          <span className="font-semibold text-fg">Proxy of</span>
          <AddressLink addr={c.implementation} maxW="max-w-[260px]" copy />
          {c.proxy_type ? <Badge>{proxyLabel(c.proxy_type)}</Badge> : null}
          <span className="text-muted">
            {c.implementation_abi
              ? "Reads go through this proxy using the implementation's ABI."
              : "The implementation is not verified, so reads use this contract's own ABI."}
          </span>
        </div>
      ) : null}

      <div className="flex w-full items-center gap-1 overflow-x-auto rounded-xl border border-line bg-raised p-1 sm:w-max">
        {(
          [
            ["code", "Code"],
            ["abi", "ABI"],
            ["read", c.implementation && c.implementation_abi ? "Read as proxy" : "Read contract"],
          ] as const
        ).map(([v, label]) => (
          <button
            key={v}
            type="button"
            onClick={() => setView(v)}
            className={cx(
              "whitespace-nowrap rounded-lg px-3 py-1.5 text-sm font-semibold transition-colors",
              view === v ? "bg-card text-bright shadow-sm" : "text-muted hover:text-fg",
            )}
          >
            {label}
          </button>
        ))}
      </div>

      {view === "code" ? (
        <CodeView c={c} />
      ) : view === "abi" ? (
        <AbiView c={c} />
      ) : (
        <Card>
          <div className="p-4 sm:p-5">
            {readAbi ? (
              <ReadContract addr={addr} abi={readAbi} />
            ) : (
              <p className="text-sm text-muted">No ABI is stored for this contract.</p>
            )}
          </div>
        </Card>
      )}
    </div>
  );
}

function Unverified({ addr, c }: { addr: string; c: SmartContract | null }) {
  return (
    <div className="flex flex-col gap-4">
      <Card>
        <div className="flex flex-col items-start gap-4 p-5 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-start gap-3">
            <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-warn/30 bg-warn/10 text-warn">
              <ShieldAlert className="h-5 w-5" />
            </span>
            <div>
              <p className="font-semibold text-fg">The source code of this contract is not verified</p>
              <p className="mt-1 text-sm text-muted">
                Publish its Solidity standard JSON input and the explorer will compile it and compare the result with
                the code on chain. Contracts with identical bytecode are verified at the same time.
              </p>
            </div>
          </div>
          <Link to="/address/$addr/contract-verification" params={{ addr }} className="btn btn-primary shrink-0">
            Verify and publish
          </Link>
        </div>
      </Card>
      {c?.implementation ? (
        <div className="flex flex-wrap items-center gap-2 rounded-xl border border-info/30 bg-info/10 px-4 py-3 text-sm">
          <Layers className="h-4 w-4 shrink-0 text-info" />
          <span className="font-semibold text-fg">Proxy of</span>
          <AddressLink addr={c.implementation} maxW="max-w-[260px]" copy />
        </div>
      ) : null}
      <Bytecode title="Deployed bytecode" value={c?.deployed_bytecode ?? null} open />
      <Bytecode title="Creation bytecode" value={c?.creation_bytecode ?? null} />
    </div>
  );
}

function CodeView({ c }: { c: SmartContract }) {
  const files = [
    ...(c.source_code ? [{ file_path: c.file_path || `${c.name ?? "Contract"}.sol`, source_code: c.source_code }] : []),
    ...c.additional_sources,
  ];
  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader
          title={
            <span className="inline-flex items-center gap-2">
              {c.name ?? "Contract"} <ShieldCheck className="h-4 w-4 text-ok" aria-label="Verified" />
            </span>
          }
          icon={<FileCode2 className="h-4 w-4" />}
          right={
            c.match_type ? (
              <Badge tone={c.match_type === "full" ? "ok" : "info"}>{c.match_type} match</Badge>
            ) : undefined
          }
        />
        <dl className="grid gap-x-6 gap-y-3 px-4 py-4 text-sm sm:grid-cols-2 sm:px-5 lg:grid-cols-3">
          <Info label="Compiler">{c.compiler_version ?? "n/a"}</Info>
          <Info label="Language">{c.language ?? "n/a"}</Info>
          <Info label="EVM version">{c.evm_version ?? "default"}</Info>
          <Info label="Optimization">
            {c.optimization_enabled === null
              ? "n/a"
              : c.optimization_enabled
                ? `Yes, ${c.optimization_runs ?? "?"} ${c.optimization_runs === 1 ? "run" : "runs"}`
                : "No"}
          </Info>
          <Info label="Via IR">{c.via_ir === null ? "n/a" : c.via_ir ? "Yes" : "No"}</Info>
          <Info label="License">{licenseLabel(c.license_type)}</Info>
          <Info label="Verified">{c.verified_at ? formatDateTime(c.verified_at) : "n/a"}</Info>
          <Info label="Main file">
            <span className="break-all font-mono text-[12.5px]">{c.file_path ?? "n/a"}</span>
          </Info>
          {c.source_address ? (
            <Info label="Verified via">
              <AddressHashLink hash={c.source_address} />
            </Info>
          ) : null}
        </dl>
        {c.match_type ? (
          <p className="border-t border-line px-4 py-3 text-xs text-muted sm:px-5">{MATCH_TEXT[c.match_type]}</p>
        ) : null}
      </Card>

      <Card>
        <CardHeader title={`Source code (${files.length} ${files.length === 1 ? "file" : "files"})`} />
        <div className="flex flex-col gap-3 p-3 sm:p-4">
          {files.length === 0 ? (
            <p className="px-1 text-sm text-muted">No source files are stored for this contract.</p>
          ) : (
            files.map((f, i) => (
              <SourceFile
                key={f.file_path}
                path={f.file_path}
                code={f.source_code}
                defaultOpen={i === 0}
                main={i === 0}
              />
            ))
          )}
        </div>
      </Card>

      {c.constructor_args ? (
        <Card>
          <CardHeader
            title="Constructor arguments"
            right={<CopyButton value={c.constructor_args} label="Copy constructor arguments" size="md" />}
          />
          <pre className="code-block m-4 max-h-48 whitespace-pre-wrap break-all px-3 py-2 sm:m-5">
            {c.constructor_args}
          </pre>
        </Card>
      ) : null}

      <Bytecode title="Deployed bytecode" value={c.deployed_bytecode} />
      <Bytecode title="Creation bytecode" value={c.creation_bytecode} />
    </div>
  );
}

function Info({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs font-medium text-muted">{label}</dt>
      <dd className="mt-0.5 min-w-0 text-fg">{children}</dd>
    </div>
  );
}

function SourceFile({
  path,
  code,
  defaultOpen,
  main,
}: {
  path: string;
  code: string;
  defaultOpen: boolean;
  main: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  const lines = code.split("\n").length;
  return (
    <div className="overflow-hidden rounded-xl border border-line">
      <div className="flex items-center justify-between gap-2 bg-raised px-3 py-2">
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          aria-expanded={open}
          className="flex min-w-0 items-center gap-2 text-left"
        >
          <ChevronDown className={cx("h-4 w-4 shrink-0 text-dim transition-transform", !open && "-rotate-90")} />
          <span className="min-w-0 break-all font-mono text-[12.5px] text-fg">{path}</span>
          {main ? <Badge tone="gold">main</Badge> : null}
          <span className="hidden shrink-0 text-xs text-dim sm:inline">{lines} lines</span>
        </button>
        <CopyButton value={code} label={`Copy ${path}`} />
      </div>
      {open ? (
        <SolidityCode
          code={code}
          className="code-block max-h-[520px] !rounded-none !border-0 !border-t !border-line py-2 pr-3"
        />
      ) : null}
    </div>
  );
}

function AbiView({ c }: { c: SmartContract }) {
  const json = c.abi ? stringify(c.abi) : null;
  const impl = c.implementation_abi ? stringify(c.implementation_abi) : null;
  return (
    <div className="flex flex-col gap-4">
      <Card>
        <CardHeader
          title="Contract ABI"
          right={json ? <CopyButton value={json} label="Copy ABI" size="md" /> : undefined}
        />
        <div className="p-4 sm:p-5">
          {json ? (
            <pre className="code-block max-h-[560px] px-3 py-2">{json}</pre>
          ) : (
            <p className="text-sm text-muted">No ABI stored.</p>
          )}
        </div>
      </Card>
      {impl ? (
        <Card>
          <CardHeader
            title="Implementation ABI"
            right={<CopyButton value={impl} label="Copy implementation ABI" size="md" />}
          />
          <div className="p-4 sm:p-5">
            <pre className="code-block max-h-[560px] px-3 py-2">{impl}</pre>
          </div>
        </Card>
      ) : null}
    </div>
  );
}

function Bytecode({ title, value, open: initial = false }: { title: string; value: string | null; open?: boolean }) {
  const [open, setOpen] = useState(initial);
  if (!value || value === "0x") return null;
  return (
    <Card>
      <div className="flex items-center justify-between gap-2 px-4 py-3 sm:px-5">
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          aria-expanded={open}
          className="flex items-center gap-2 text-left"
        >
          <ChevronDown className={cx("h-4 w-4 text-dim transition-transform", !open && "-rotate-90")} />
          <span className="text-[15px] font-semibold text-fg">{title}</span>
          <span className="text-xs text-dim">{Math.max(0, (value.length - 2) / 2).toLocaleString("en-US")} bytes</span>
        </button>
        <CopyButton value={value} label={`Copy ${title.toLowerCase()}`} size="md" />
      </div>
      {open ? (
        <pre className="code-block mx-4 mb-4 max-h-64 whitespace-pre-wrap break-all px-3 py-2 sm:mx-5">{value}</pre>
      ) : null}
    </Card>
  );
}

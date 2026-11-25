/**
 * Contract verification by Solidity standard JSON input.
 *
 *   1. pick the compiler (GET /api/v2/compilers), licence, optional contract
 *      name, and upload the standard JSON (a Foundry/Hardhat build-info file
 *      works too: its `input` is the standard JSON, and its compiler version is
 *      picked for you);
 *   2. multipart POST /api/v2/smart-contracts/{addr}/verification/via/standard-input
 *      with compiler_version, license_type, autodetect_constructor_args,
 *      contract_name and files[0];
 *   3. poll GET .../verification/status until success or failed, and show it.
 */
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { BadgeCheck, CheckCircle2, FileJson, Info, RotateCw, Upload, XCircle } from "lucide-react";

import { ApiError, apiGet, apiPost, useApi } from "@/lib/api";
import { formatBytes, isAddress } from "@/lib/format";
import type { SmartContract, VerificationStatus } from "@/lib/types";
import { Card, CardHeader, Spinner, cx } from "./ui";
import { NETWORK_NAME } from "@/lib/site";

const MAX_BYTES = 20 * 1024 * 1024;
const POLL_MS = 2500;
const GIVE_UP_MS = 20 * 60 * 1000;

export const LICENSES: [string, string][] = [
  ["none", "No license (None)"],
  ["unlicense", "The Unlicense"],
  ["mit", "MIT License"],
  ["gnu_gpl_v2", "GNU General Public License v2.0"],
  ["gnu_gpl_v3", "GNU General Public License v3.0"],
  ["gnu_lgpl_v2_1", "GNU Lesser General Public License v2.1"],
  ["gnu_lgpl_v3", "GNU Lesser General Public License v3.0"],
  ["bsd_2_clause", 'BSD 2-clause "Simplified" license'],
  ["bsd_3_clause", 'BSD 3-clause "New" or "Revised" license'],
  ["mpl_2_0", "Mozilla Public License 2.0"],
  ["osl_3_0", "Open Software License 3.0"],
  ["apache_2_0", "Apache 2.0"],
  ["gnu_agpl_v3", "GNU Affero General Public License"],
  ["bsl_1_1", "Business Source License"],
];

interface Parsed {
  name: string;
  size: number;
  json: string; // the standard JSON that is uploaded
  sources: number;
  language: string;
  optimizer: string;
  viaIR: boolean;
  evm: string | null;
  version: string | null; // from a build-info file
  contracts: string[]; // "path:Name" candidates from settings.compilationTarget, if any
}

function parseFile(name: string, text: string): Parsed {
  let obj: unknown;
  try {
    obj = JSON.parse(text);
  } catch {
    throw new Error("The file is not valid JSON.");
  }
  let input = obj as Record<string, unknown>;
  let version: string | null = null;
  const o = obj as Record<string, unknown>;
  if (o && typeof o === "object" && o.input && typeof o.input === "object") {
    input = o.input as Record<string, unknown>;
    const v = (o.solcLongVersion ?? o.solcVersion) as string | undefined;
    if (typeof v === "string") version = v.startsWith("v") ? v : `v${v}`;
  }
  if (
    !input ||
    typeof input !== "object" ||
    typeof input.language !== "string" ||
    !input.sources ||
    typeof input.sources !== "object"
  ) {
    throw new Error(
      'This is not a Solidity standard JSON input: it needs "language", "sources" and "settings" at the top level.',
    );
  }
  const settings = (input.settings ?? {}) as Record<string, unknown>;
  const opt = (settings.optimizer ?? {}) as { enabled?: boolean; runs?: number };
  const target = (settings.compilationTarget ?? {}) as Record<string, string>;
  const json = input === obj ? text : JSON.stringify(input);
  return {
    name,
    size: new Blob([json]).size,
    json,
    sources: Object.keys(input.sources as object).length,
    language: input.language as string,
    optimizer: opt.enabled ? `on, ${opt.runs ?? 200} runs` : "off",
    viaIR: settings.viaIR === true,
    evm: typeof settings.evmVersion === "string" ? settings.evmVersion : null,
    version,
    contracts: Object.entries(target).map(([p, n]) => `${p}:${n}`),
  };
}

type Phase =
  | { kind: "form" }
  | { kind: "sending" }
  | { kind: "polling"; started: number; status: VerificationStatus | null }
  | { kind: "done"; ok: boolean; message: string | null }
  | { kind: "slow" };

function Field({
  label,
  htmlFor,
  hint,
  error,
  children,
}: {
  label: string;
  htmlFor: string;
  hint?: ReactNode;
  error?: string | null;
  children: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={htmlFor} className="text-sm font-semibold text-fg">
        {label}
      </label>
      {children}
      {error ? (
        <p id={`${htmlFor}-error`} className="text-xs font-medium text-bad">
          {error}
        </p>
      ) : hint ? (
        <p className="text-xs text-dim">{hint}</p>
      ) : null}
    </div>
  );
}

export function VerifyForm({ fixedAddress, initialAddress }: { fixedAddress?: string; initialAddress?: string }) {
  const qc = useQueryClient();
  const [address, setAddress] = useState(fixedAddress ?? initialAddress ?? "");
  const [version, setVersion] = useState("");
  const [license, setLicense] = useState("none");
  const [contractName, setContractName] = useState("");
  const [autodetect, setAutodetect] = useState(true);
  const [file, setFile] = useState<Parsed | null>(null);
  const [fileError, setFileError] = useState<string | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [phase, setPhase] = useState<Phase>({ kind: "form" });
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const [now, setNow] = useState(Date.now());

  const compilers = useApi<{ versions: string[] }>(["compilers"], "/compilers", undefined, { staleTime: Infinity });
  const versions = useMemo(() => compilers.data?.versions ?? [], [compilers.data]);

  const addrOk = isAddress(address.trim());
  const existing = useApi<SmartContract>(
    ["contract", address.trim().toLowerCase()],
    addrOk ? `/smart-contracts/${address.trim()}` : null,
    undefined,
    {
      retry: false,
    },
  );

  // A build-info file names its compiler: pick it when the list has it.
  useEffect(() => {
    if (file?.version && versions.includes(file.version)) setVersion(file.version);
  }, [file, versions]);

  // Poll the status while a job runs.
  useEffect(() => {
    if (phase.kind !== "polling") return;
    let stop = false;
    const tick = setInterval(() => setNow(Date.now()), 1000);
    const addr = address.trim();
    const loop = async () => {
      while (!stop) {
        await new Promise((r) => setTimeout(r, POLL_MS));
        if (stop) return;
        try {
          const s = await apiGet<VerificationStatus>(`/smart-contracts/${addr}/verification/status`);
          if (stop) return;
          if (s.status === "success" || s.status === "failed") {
            setPhase({ kind: "done", ok: s.status === "success", message: s.message });
            if (s.status === "success") {
              void qc.invalidateQueries({ queryKey: ["contract", addr.toLowerCase()] });
              void qc.invalidateQueries({ queryKey: ["address", addr.toLowerCase()] });
            }
            return;
          }
          setPhase((p) => (p.kind === "polling" ? { ...p, status: s } : p));
        } catch (e) {
          // A 429 or a dropped connection: keep polling, a little slower.
          if (e instanceof ApiError && e.retryAfter) await new Promise((r) => setTimeout(r, e.retryAfter! * 1000));
        }
        if (Date.now() - (phase.kind === "polling" ? phase.started : 0) > GIVE_UP_MS) {
          setPhase({ kind: "slow" });
          return;
        }
      }
    };
    void loop();
    return () => {
      stop = true;
      clearInterval(tick);
    };
    // Runs once per job: the loop reads `phase` and `address` as they were when it started.
  }, [phase.kind]);

  async function onFile(f: File | undefined) {
    setFileError(null);
    setFile(null);
    if (!f) return;
    if (f.size > MAX_BYTES) {
      setFileError(`The file is ${formatBytes(f.size)}; the limit is ${formatBytes(MAX_BYTES)}.`);
      return;
    }
    try {
      setFile(parseFile(f.name, await f.text()));
      setErrors((e) => ({ ...e, file: "" }));
    } catch (e) {
      setFileError((e as Error).message);
    }
  }

  async function submit() {
    const errs: Record<string, string> = {};
    const addr = address.trim();
    if (!isAddress(addr)) errs.address = "Enter the contract address: 0x followed by 40 hex characters.";
    if (!version) errs.version = "Choose the compiler version the contract was built with.";
    if (!file) errs.file = "Upload the standard JSON input file.";
    if (contractName && !/^[\w./@-]+(\.sol)?(:[A-Za-z_$][\w$]*)?$/.test(contractName.trim()))
      errs.name = "Use Name or path/File.sol:Name.";
    setErrors(errs);
    setSubmitError(null);
    if (Object.keys(errs).length || !file) {
      // Take the user to the first field that needs fixing.
      const first = errs.address ? "vf-address" : errs.version ? "vf-version" : errs.name ? "vf-name" : null;
      if (first) document.getElementById(first)?.focus();
      else fileInput.current?.closest("div")?.scrollIntoView({ behavior: "smooth", block: "center" });
      return;
    }

    const fd = new FormData();
    fd.set("compiler_version", version);
    fd.set("license_type", license);
    fd.set("autodetect_constructor_args", autodetect ? "true" : "false");
    if (contractName.trim()) fd.set("contract_name", contractName.trim());
    fd.set(
      "files[0]",
      new Blob([file.json], { type: "application/json" }),
      file.name.endsWith(".json") ? file.name : "input.json",
    );

    setPhase({ kind: "sending" });
    try {
      await apiPost<{ message: string }>(`/smart-contracts/${addr}/verification/via/standard-input`, fd, false);
      setPhase({ kind: "polling", started: Date.now(), status: null });
      setNow(Date.now());
    } catch (e) {
      const msg =
        e instanceof ApiError && e.status === 429
          ? `Too many verification attempts from your connection.${e.retryAfter ? ` Try again in ${Math.ceil(e.retryAfter / 60)} minutes.` : ""}`
          : (e as Error).message;
      setSubmitError(msg);
      setPhase({ kind: "form" });
    }
  }

  const addr = address.trim();

  if (phase.kind === "polling" || phase.kind === "sending" || phase.kind === "done" || phase.kind === "slow") {
    const elapsed = phase.kind === "polling" ? Math.max(0, Math.floor((now - phase.started) / 1000)) : 0;
    const label =
      phase.kind === "sending"
        ? "Uploading"
        : phase.kind === "polling"
          ? phase.status?.status === "running"
            ? "Compiling and comparing bytecode"
            : "Waiting for the verifier"
          : null;
    return (
      <Card>
        <div className="flex flex-col items-center gap-3 px-6 py-12 text-center">
          {phase.kind === "done" ? (
            phase.ok ? (
              <>
                <span className="flex h-14 w-14 items-center justify-center rounded-2xl border border-ok/30 bg-ok/10 text-ok">
                  <CheckCircle2 className="h-7 w-7" />
                </span>
                <h2 className="text-xl font-bold text-bright">Contract verified</h2>
                <p className="max-w-lg text-sm text-muted">
                  {phase.message ||
                    "The compiled bytecode matches the code on chain. The source is now public on the contract page."}
                </p>
                <Link
                  to="/address/$addr"
                  params={{ addr }}
                  search={{ tab: "contract" }}
                  className="btn btn-primary mt-2"
                >
                  <BadgeCheck className="h-4 w-4" /> View the verified contract
                </Link>
              </>
            ) : (
              <>
                <span className="flex h-14 w-14 items-center justify-center rounded-2xl border border-bad/30 bg-bad/10 text-bad">
                  <XCircle className="h-7 w-7" />
                </span>
                <h2 className="text-xl font-bold text-bright">Verification failed</h2>
                <p className="max-w-lg break-words rounded-lg border border-bad/25 bg-bad/10 px-3 py-2 font-mono text-[13px] text-bad">
                  {phase.message || "The verifier did not say why."}
                </p>
                <p className="max-w-lg text-sm text-muted">
                  Check the compiler version, that the file is the exact input the contract was built from (optimizer
                  runs, viaIR, EVM version and remappings all change the bytecode), and the contract name.
                </p>
                <button type="button" className="btn mt-2" onClick={() => setPhase({ kind: "form" })}>
                  <RotateCw className="h-4 w-4" /> Change the inputs and try again
                </button>
              </>
            )
          ) : phase.kind === "slow" ? (
            <>
              <Info className="h-8 w-8 text-info" />
              <h2 className="text-xl font-bold text-bright">Still working</h2>
              <p className="max-w-lg text-sm text-muted">
                This verification is taking unusually long (via-IR builds can). It keeps running on the server: check
                the contract page later.
              </p>
              <Link to="/address/$addr" params={{ addr }} search={{ tab: "contract" }} className="btn mt-2">
                Open the contract page
              </Link>
            </>
          ) : (
            <>
              <Spinner className="h-8 w-8 text-gold" />
              <h2 className="text-lg font-bold text-bright">{label}</h2>
              <p className="max-w-lg text-sm text-muted">
                The verifier compiles your input with {version || "the chosen compiler"} and compares the result with
                the code at <span className="break-all font-mono text-fg">{addr}</span>. Small contracts take seconds;
                large via-IR builds can take several minutes. You can leave this page open.
              </p>
              {phase.kind === "polling" ? (
                <p className="text-xs tabular-nums text-dim">
                  {Math.floor(elapsed / 60)}:{String(elapsed % 60).padStart(2, "0")} elapsed
                  {phase.status?.message ? `, ${phase.status.message}` : ""}
                </p>
              ) : null}
            </>
          )}
        </div>
      </Card>
    );
  }

  return (
    <form
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
      className="grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_340px]"
    >
      <Card>
        <CardHeader title="Contract and compiler" />
        <div className="flex flex-col gap-5 p-4 sm:p-5">
          <Field
            label="Contract address"
            htmlFor="vf-address"
            error={errors.address}
            hint={fixedAddress ? undefined : `The address the contract is deployed at on ${NETWORK_NAME}.`}
          >
            <input
              id="vf-address"
              className={cx("input font-mono", fixedAddress && "cursor-default !bg-transparent text-muted")}
              value={address}
              readOnly={!!fixedAddress}
              onChange={(e) => setAddress(e.target.value)}
              placeholder="0x..."
              spellCheck={false}
              autoComplete="off"
              aria-invalid={!!errors.address}
              aria-describedby={errors.address ? "vf-address-error" : undefined}
            />
          </Field>
          {addrOk && existing.data?.is_verified ? (
            <p className="-mt-2 flex items-start gap-2 rounded-lg border border-ok/30 bg-ok/10 px-3 py-2 text-[13px] text-fg">
              <BadgeCheck className="mt-0.5 h-4 w-4 shrink-0 text-ok" />
              <span>
                This contract is already verified{existing.data.name ? ` as ${existing.data.name}` : ""}.{" "}
                <Link to="/address/$addr" params={{ addr }} search={{ tab: "contract" }} className="link font-semibold">
                  See its source
                </Link>
                .
              </span>
            </p>
          ) : null}

          <Field
            label="Compiler version"
            htmlFor="vf-version"
            error={errors.version}
            hint={
              compilers.isError
                ? "The compiler list could not be loaded. Try reloading the page."
                : "Release builds of solc. Must be the exact version the contract was compiled with."
            }
          >
            <select
              id="vf-version"
              className="input"
              value={version}
              onChange={(e) => setVersion(e.target.value)}
              disabled={!versions.length}
              aria-invalid={!!errors.version}
            >
              <option value="">{compilers.isLoading ? "Loading compilers..." : "Choose a version"}</option>
              {versions.map((v) => (
                <option key={v} value={v}>
                  {v}
                </option>
              ))}
            </select>
          </Field>

          <Field
            label="Contract name (optional)"
            htmlFor="vf-name"
            error={errors.name}
            hint={
              file?.contracts.length
                ? `The input names ${file.contracts.join(", ")}.`
                : "Name or path/File.sol:Name. Leave empty to let the verifier try every contract in the input."
            }
          >
            <input
              id="vf-name"
              className="input font-mono"
              value={contractName}
              onChange={(e) => setContractName(e.target.value)}
              placeholder="src/Token.sol:Token"
              spellCheck={false}
              autoComplete="off"
              aria-invalid={!!errors.name}
            />
          </Field>

          <Field label="License" htmlFor="vf-license">
            <select id="vf-license" className="input" value={license} onChange={(e) => setLicense(e.target.value)}>
              {LICENSES.map(([v, l]) => (
                <option key={v} value={v}>
                  {l}
                </option>
              ))}
            </select>
          </Field>

          <label className="flex cursor-pointer items-start gap-3 rounded-lg border border-line bg-raised px-3 py-2.5">
            <input
              type="checkbox"
              className="mt-0.5 h-4 w-4 accent-[var(--gold)]"
              checked={autodetect}
              onChange={(e) => setAutodetect(e.target.checked)}
            />
            <span className="text-sm">
              <span className="font-semibold text-fg">Detect constructor arguments</span>
              <span className="block text-xs text-muted">
                Read them from the creation transaction: whatever follows the compiled init code.
              </span>
            </span>
          </label>
        </div>
      </Card>

      <div className="flex flex-col gap-6">
        <Card>
          <CardHeader title="Standard JSON input" icon={<FileJson className="h-4 w-4" />} />
          <div className="flex flex-col gap-3 p-4 sm:p-5">
            <div
              onDragOver={(e) => {
                e.preventDefault();
                setDragging(true);
              }}
              onDragLeave={() => setDragging(false)}
              onDrop={(e) => {
                e.preventDefault();
                setDragging(false);
                void onFile(e.dataTransfer.files?.[0]);
              }}
              className={cx(
                "flex flex-col items-center gap-2 rounded-xl border-2 border-dashed px-4 py-6 text-center transition-colors",
                dragging ? "border-gold bg-gold/5" : errors.file || fileError ? "border-bad/50" : "border-line",
              )}
            >
              <Upload className="h-6 w-6 text-dim" />
              <p className="text-sm text-fg2">Drop the .json file here, or</p>
              <button type="button" className="btn btn-sm" onClick={() => fileInput.current?.click()}>
                Choose a file
              </button>
              <input
                ref={fileInput}
                type="file"
                accept=".json,application/json"
                className="sr-only"
                aria-label="Standard JSON input file"
                onChange={(e) => void onFile(e.target.files?.[0])}
              />
              <p className="text-xs text-dim">Up to 20 MB. A Foundry or Hardhat build-info file also works.</p>
            </div>
            {fileError || errors.file ? (
              <p className="text-xs font-medium text-bad">{fileError || errors.file}</p>
            ) : null}
            {file ? (
              <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 rounded-lg border border-line bg-raised px-3 py-2.5 text-[13px]">
                <dt className="text-muted">File</dt>
                <dd className="truncate font-mono text-fg" title={file.name}>
                  {file.name}
                </dd>
                <dt className="text-muted">Size</dt>
                <dd className="text-fg">{formatBytes(file.size)}</dd>
                <dt className="text-muted">Sources</dt>
                <dd className="text-fg">
                  {file.sources} files ({file.language})
                </dd>
                <dt className="text-muted">Optimizer</dt>
                <dd className="text-fg">{file.optimizer}</dd>
                <dt className="text-muted">Via IR</dt>
                <dd className="text-fg">{file.viaIR ? "yes" : "no"}</dd>
                {file.evm ? (
                  <>
                    <dt className="text-muted">EVM</dt>
                    <dd className="text-fg">{file.evm}</dd>
                  </>
                ) : null}
                {file.version ? (
                  <>
                    <dt className="text-muted">Built with</dt>
                    <dd className="font-mono text-fg">{file.version}</dd>
                  </>
                ) : null}
              </dl>
            ) : null}
          </div>
        </Card>

        <Card>
          <div className="flex flex-col gap-3 p-4 sm:p-5">
            {submitError ? (
              <p role="alert" className="rounded-lg border border-bad/30 bg-bad/10 px-3 py-2 text-[13px] text-bad">
                {submitError}
              </p>
            ) : null}
            <button type="submit" className="btn btn-primary w-full">
              <BadgeCheck className="h-4 w-4" /> Verify and publish
            </button>
            <p className="text-xs text-dim">
              With Foundry: <span className="font-mono">forge verify-contract --show-standard-json-input</span> writes
              this file. Every contract with the same runtime bytecode is verified along with this one.
            </p>
          </div>
        </Card>
      </div>
    </form>
  );
}

# syntax=docker/dockerfile:1.7
# ^ must stay the first line. A parser directive after a comment, a
#   blank line or an instruction is silently ignored, and the cache
#   mounts below would then fail to parse. It is what makes
#   `RUN --mount=type=cache` available.
# =============================================================================
# Explorer contract verifier: Go service, node, and the solc builds it runs
# =============================================================================
# The build context is the explorer's Go module, so paths here are relative to
# explorer/backend/ (explorer/compose.yaml builds it):
#
#   docker build -f explorer/backend/verifier.Dockerfile explorer/backend
#
# Why node at runtime: the verifier compiles with the official solc
# emscripten-wasm32 build (verifier/runner.mjs loads it). That is one code
# path on every machine; solc has no native arm64 build before 0.8.31, and
# the server may be arm64.
#
# Why the compilers are baked in: the explorer runs with no dependency on
# outside services (SOLC_DOWNLOAD=on fetches other versions, sha256-checked). Every compiler listed in
# verifier/compilers.tsv is downloaded HERE, at build time, and checked
# against the sha256 pinned in that file; a mismatch fails the build. At
# runtime SOLC_DOWNLOAD defaults to off and the service makes no network
# request at all. Adding a version is one reviewed line in compilers.tsv and a
# rebuild.
# =============================================================================

# ---------------------------------------------------------------- build ----
FROM golang:1.26.11-alpine AS build

# git: some modules resolve over it. The rest of the toolchain is in the image.
RUN apk add --no-cache git ca-certificates

WORKDIR /src

# go.mod/go.sum first so `go mod download` is cached against dependency
# changes rather than against every source edit.
COPY go.mod go.sum ./

# The module cache is a cache mount rather than a layer so it survives a
# go.sum change and is shared with the other engine builds on this host.
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    go mod download

COPY . ./

# CGO off gives a static binary that needs no libc from the runtime image.
# -trimpath keeps build paths out of panics; -s -w drops the symbol table and
# DWARF. The compile cache is mounted so only changed packages rebuild; -o
# writes to /out, a real layer, so the binary that ships is unaffected.
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/verifier ./cmd/verifier/

# ----------------------------------------------------------------- solc ----
# Same base as the runtime, so the smoke test below runs the compilers under
# the very node that will run them in production.
FROM node:22-alpine AS solc

# The only place compilers come from. Overridable for a mirror, but the
# sha256 pins in compilers.tsv still decide what is accepted.
ARG SOLC_BIN_BASE=https://binaries.soliditylang.org

WORKDIR /solc
COPY verifier/compilers.tsv ./
COPY verifier/runner.mjs /tmp/runner.mjs

# For each "version file sha256" line: refuse a file name with a path in it,
# download, check the sha256 (sha256sum -c exits non-zero on a mismatch,
# which fails this RUN and so the build), then compile an empty contract with
# it through runner.mjs. That last step catches a compiler that downloads
# fine but cannot run here, before the image ships rather than at the first
# verification.
RUN set -eu; \
    count=0; \
    grep -Ev '^[[:space:]]*(#|$)' compilers.tsv > /tmp/pins; \
    while read -r version file sha; do \
      case "$file" in \
        */*|.*|"") echo "compilers.tsv: bad file name '$file'" >&2; exit 1 ;; \
      esac; \
      echo "fetching $version"; \
      wget -q -O "$file" "$SOLC_BIN_BASE/emscripten-wasm32/$file"; \
      echo "${sha#0x}  $file" | sha256sum -c -; \
      echo '{"language":"Solidity","sources":{"t.sol":{"content":"contract T {}"}},"settings":{"outputSelection":{"*":{"*":["evm.deployedBytecode.object"]}}}}' \
        | node /tmp/runner.mjs "/solc/$file" > /tmp/out.json; \
      grep -q '"object":"60' /tmp/out.json || { echo "$version did not compile the smoke test:" >&2; cat /tmp/out.json >&2; exit 1; }; \
      count=$((count + 1)); \
    done < /tmp/pins; \
    [ "$count" -gt 0 ] || { echo "compilers.tsv lists no compiler" >&2; exit 1; }; \
    chmod 0444 /solc/*

# -------------------------------------------------------------- runtime ----
FROM node:22-alpine AS runtime

# npm, npx, corepack and yarn are never used here; removing them takes a
# package manager (and its own dependency tree) out of the image. node itself
# does not need them. Then the service user, and the download cache, which is
# only written when SOLC_DOWNLOAD=on (mount a volume there in that case).
# The user gets no home directory (-H): /app is then created by WORKDIR as
# root, so the service cannot rename or replace runner.mjs or the compiler
# directory. The cache is the only place it can write.
# wget comes from busybox and is what the healthcheck uses.
RUN rm -rf /usr/local/lib/node_modules /usr/local/bin/npm /usr/local/bin/npx \
           /usr/local/bin/corepack /usr/local/bin/yarn /usr/local/bin/yarnpkg /opt/yarn-* && \
    adduser -D -H -u 10001 -h /app verifier && \
    mkdir -p /var/cache/solc && chown 10001:10001 /var/cache/solc

WORKDIR /app
COPY --from=build /out/verifier /app/verifier
COPY verifier/runner.mjs /app/runner.mjs
# Root-owned and read-only to the service user: the bundled compilers cannot
# be swapped from inside the container. The service also re-checks each
# file's sha256 against compilers.tsv before every use.
COPY --from=solc /solc/ /app/solc/

# Never run as root. 8080 needs no privilege.
USER 10001:10001

ENV HOST=0.0.0.0 \
    PORT=8080 \
    SOLC_DOWNLOAD=off \
    SOLC_BUNDLED_DIR=/app/solc \
    COMPILER_CACHE_DIR=/var/cache/solc \
    NODE_BIN=node \
    RUNNER_PATH=/app/runner.mjs
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null || exit 1

ENTRYPOINT ["/app/verifier"]

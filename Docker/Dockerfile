# Multi-stage build: compile Rust binary then create minimal runtime image
FROM rust:slim-bookworm AS builder

# Install build dependencies
# clang/libclang are required by bindgen, which reth's libmdbx-sys build script
# runs to generate the MDBX bindings. Without them the build fails deep in a
# dependency with "Unable to find libclang", which reads like a Rust problem and
# is actually a missing apt package.
RUN apt-get update && apt-get install -y \
    pkg-config \
    libssl-dev \
    clang \
    libclang-dev \
    git \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# Copy manifests first to cache dependency downloads on Cargo.lock changes
COPY Cargo.toml Cargo.lock ./

# Copy bytecodes (required by include_bytes! macros in source)
COPY src/bytecodes ./src/bytecodes

# Create a minimal lib.rs and main.rs that satisfies the compiler
# for dependency caching (no include_bytes!, no complex macros)
RUN mkdir -p src && \
    echo 'pub fn dummy() {}' > src/lib.rs && \
    echo 'fn main() {}' > src/main.rs && \
    cargo fetch --locked 2>/dev/null || true

# Now copy the real source and build
COPY src ./src
RUN touch src/lib.rs src/main.rs && cargo build --release

# ── Runtime image ────────────────────────────────────────────────────────────
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y \
    ca-certificates \
    libssl3 \
    curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /build/target/release/example-custom-poa-node /usr/local/bin/meowchain

RUN mkdir -p /app/data

EXPOSE 8545 8546 30303 30303/udp 9001

ENV RUST_LOG=info
ENV RUST_BACKTRACE=1

CMD ["meowchain", "--datadir", "/app/data", "--http-addr", "0.0.0.0", "--http-port", "8545", "--ws-addr", "0.0.0.0", "--ws-port", "8546"]

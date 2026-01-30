# Rust POA Node Dockerfile
FROM rust:1.83-bookworm AS builder

# Install build dependencies
RUN apt-get update && apt-get install -y \
    build-essential \
    clang \
    libclang-dev \
    pkg-config \   
    libssl-dev \
    git \
    && rm -rf /var/lib/apt/lists/*

# Install just command runner
RUN cargo install just

WORKDIR /app

# Copy the entire project
COPY Cargo.toml Cargo.lock* ./
COPY src ./src
COPY Justfile ./
COPY sample-genesis.json ./

# Build the project in release mode
RUN cargo build --release -p example-custom-poa-node

# Runtime stage
FROM debian:bookworm-slim

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates \
    libssl3 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy the built binary
COPY --from=builder /app/target/release/example-custom-poa-node /usr/local/bin/poa-node
COPY --from=builder /app/sample-genesis.json ./

# Create data directory
RUN mkdir -p /app/data

# Expose RPC ports
EXPOSE 8545 8546 30303 30303/udp 9001

# Set environment variables
ENV RUST_LOG=info
ENV RUST_BACKTRACE=1

# Run the POA node
CMD ["poa-node", "--datadir", "/app/data", "--http", "--http.addr", "0.0.0.0", "--http.port", "8545", "--http.api", "eth,net,web3,txpool,debug,trace", "--ws", "--ws.addr", "0.0.0.0", "--ws.port", "8546"]

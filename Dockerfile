# Rust POA Node Dockerfile
# Uses pre-built binary from local build
FROM ubuntu:24.04

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates \
    libssl3 \
    curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy the pre-built binary from local target/release
COPY target/release/example-custom-poa-node /usr/local/bin/poa-node
COPY sample-genesis.json ./

# Create data directory
RUN mkdir -p /app/data

# Expose RPC ports
EXPOSE 8545 8546 30303 30303/udp 9001

# Set environment variables
ENV RUST_LOG=info
ENV RUST_BACKTRACE=1

# Run the POA node
CMD ["poa-node", "--datadir", "/app/data", "--http", "--http.addr", "0.0.0.0", "--http.port", "8545", "--http.api", "eth,net,web3,txpool,debug,trace", "--ws", "--ws.addr", "0.0.0.0", "--ws.port", "8546"]

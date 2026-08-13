# Meowchain Justfile - builds are deterministic (reth pinned by rev in Cargo.toml,
# versions locked in Cargo.lock). Nothing here runs `cargo update`; upgrading reth
# is a deliberate act via `just upgrade-reth <rev>`.

# Default: release build from the committed lockfile
default: build

# Release build (locked deps — reproduces the deployed binary)
build:
    cargo build --release --locked

# Alias kept for muscle memory; same as build
build-fast:
    cargo build --release --locked

# Debug build
build-debug:
    cargo build --locked

# Deliberately upgrade reth: pins Cargo.toml to REV, refreshes the lockfile, builds.
# After this, test the new binary against a COPY of a production datadir before
# deploying — reth storage-format changes can make old datadirs unreadable.
upgrade-reth REV:
    sed -i.bak 's|rev = "[0-9a-f]\{40\}"|rev = "{{REV}}"|g' Cargo.toml && rm -f Cargo.toml.bak
    cargo update
    cargo build --release

# Run all tests
test:
    cargo test --locked

# Alias kept for muscle memory; same as test
test-fast:
    cargo test --locked

# Dev mode: build + run
dev:
    RUST_LOG=info cargo run --release --locked

# Run in production mode
run-production:
    cargo run --release --locked -- --production --block-time 12

# Run with custom args
run-custom *ARGS:
    cargo run --release --locked -- {{ARGS}}

# Build Docker image
docker:
    cargo build --release --locked
    docker build -t meowchain .

# Clean build artifacts
clean:
    cargo clean

# Check compilation without building
check:
    cargo check --locked

# Format code
fmt:
    cargo fmt

# Run clippy lints
lint:
    cargo clippy --locked -- -D warnings

# Regenerate sample-genesis.json
genesis:
    cargo test test_regenerate_sample_genesis

# Update Rust toolchain only (does NOT touch dependency versions)
update-toolchain:
    rustup update stable

# syntax=docker/dockerfile:1.7
# =============================================================================
# Public RPC gateway (explorer/rpc-gateway)
# =============================================================================
# Build context: explorer/rpc-gateway (compose.yaml at the repo root builds it).
# The public entrance to the chain: forwards only eth_*/net_*/web3_*, refuses
# node signing, caps batches and unfiltered eth_getLogs, answers CORS for the
# allowed sites. Put it behind your public RPC name; it hands calls to the
# node on the docker network.
# =============================================================================

FROM golang:1.26.11-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked go mod download
COPY *.go ./
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/rpc-gateway .

FROM alpine:3.20 AS runtime
# wget (busybox) is what the healthcheck uses.
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 -h /app gateway
COPY --from=build /out/rpc-gateway /app/rpc-gateway
USER 10001:10001
# Inside the container it must listen on all interfaces so the published port
# reaches it; compose.yaml publishes it on 127.0.0.1 by default.
ENV RPC_GATEWAY_LISTEN=0.0.0.0:8548 \
    RPC_GATEWAY_UPSTREAM=http://chain:8545 \
    RPC_GATEWAY_UPSTREAM_WS=ws://chain:8546
EXPOSE 8548
HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- --header 'Content-Type: application/json' \
        --post-data '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}' \
        http://127.0.0.1:8548/ | grep -q '"result"' || exit 1
ENTRYPOINT ["/app/rpc-gateway"]

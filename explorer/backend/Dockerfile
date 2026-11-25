# syntax=docker/dockerfile:1.7
# Explorer backend: one static Go binary, `explorer`, run as the API
# (`explorer api`, the default) or the indexer (`explorer indexer`).
# Build context: explorer/backend.

FROM golang:1.26.11-alpine AS build
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked go mod download
COPY . ./
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/explorer ./cmd/explorer/

FROM alpine:3.20
# ca-certificates: TLS to a remote RPC. tzdata: readable log timestamps.
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 -h /app explorer
WORKDIR /app
COPY --from=build /out/explorer /app/explorer
USER 10001:10001
ENV HOST=0.0.0.0 PORT=8080
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 \
    CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null 2>&1 || exit 1
ENTRYPOINT ["/app/explorer"]

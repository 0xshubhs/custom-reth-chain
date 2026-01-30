# Scoutup Dockerfile - Blockscout Explorer Orchestrator
FROM golang:1.23-bookworm AS builder

WORKDIR /app

# Copy Go module files first for caching
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the rest of the source code
COPY . .

# Build the Go binary
RUN CGO_ENABLED=0 GOOS=linux go build -o scoutup .

# Runtime stage - needs Docker CLI to orchestrate blockscout containers
FROM docker:24-cli

# Install required dependencies
RUN apk add --no-cache \
    ca-certificates \
    bash \
    curl

WORKDIR /app

# Copy the built binary
COPY --from=builder /app/scoutup /usr/local/bin/scoutup

# Copy embedded files (docker-compose templates, env files)
COPY --from=builder /app/blockscout/embed /app/blockscout/embed

# Expose ports for frontend and backend
EXPOSE 3000 4000

# Default command - run scoutup pointing to poa-node
ENTRYPOINT ["scoutup"]

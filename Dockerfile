# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git ca-certificates

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o hawser ./cmd/hawser

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    docker-cli \
    docker-cli-compose

# Copy binary from builder
COPY --from=builder /app/hawser /usr/local/bin/hawser

# Create non-root user (optional, but requires docker group)
# RUN addgroup -S hawser && adduser -S hawser -G hawser

# Environment variables with defaults
ENV PORT=2375 \
    DOCKER_SOCKET=/var/run/docker.sock \
    HEARTBEAT_INTERVAL=30 \
    REQUEST_TIMEOUT=30 \
    RECONNECT_DELAY=1 \
    MAX_RECONNECT_DELAY=60

# Expose default port
EXPOSE 2375

# Health check
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:${PORT}/_hawser/health || exit 1

# Run as root to access Docker socket (can be changed with --user flag)
ENTRYPOINT ["/usr/local/bin/hawser"]

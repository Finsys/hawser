# Runtime image - uses pre-built binary from goreleaser
FROM alpine:3.19

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    docker-cli \
    docker-cli-compose

# Copy pre-built binary (provided by goreleaser)
COPY hawser /usr/local/bin/hawser

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

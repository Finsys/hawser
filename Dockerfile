# syntax=docker/dockerfile:1.4
# =============================================================================
# Hawser Docker Image - Security-Hardened Build
# =============================================================================
# This Dockerfile builds a custom Wolfi OS from scratch using apko, ensuring:
# - Full transparency (no dependency on pre-built Chainguard images)
# - Reproducible builds from open-source Wolfi packages
# - Minimal attack surface with only required packages
# =============================================================================

# -----------------------------------------------------------------------------
# Stage 1: OS Generator (Alpine + apko tool)
# -----------------------------------------------------------------------------
# We use Alpine because it has a shell. This lets us download and run apko
# to build our custom Wolfi OS from scratch using open-source packages.
FROM alpine:3.21 AS os-builder

ARG TARGETARCH

WORKDIR /work

# Install apko tool (latest stable release)
# apko is the tool Chainguard uses to build their images - we use it directly
ARG APKO_VERSION=0.30.34
RUN apk add --no-cache curl \
    && ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "arm64" || echo "amd64") \
    && curl -sL "https://github.com/chainguard-dev/apko/releases/download/v${APKO_VERSION}/apko_${APKO_VERSION}_linux_${ARCH}.tar.gz" \
       | tar -xz --strip-components=1 -C /usr/local/bin \
    && chmod +x /usr/local/bin/apko

# Generate apko.yaml for current target architecture only
# We build single-arch to avoid multi-arch layer confusion in extraction
# Note: no wget/curl - the HEALTHCHECK uses the built-in "hawser healthcheck"
RUN APKO_ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "aarch64" || echo "x86_64") \
    && printf '%s\n' \
    "contents:" \
    "  repositories:" \
    "    - https://packages.wolfi.dev/os" \
    "  keyring:" \
    "    - https://packages.wolfi.dev/os/wolfi-signing.rsa.pub" \
    "  packages:" \
    "    - wolfi-base" \
    "    - ca-certificates" \
    "    - busybox" \
    "    - docker-cli" \
    "    - docker-compose=5.5.0-r0" \
    "    - docker-cli-buildx" \
    "entrypoint:" \
    "  command: /bin/sh -l" \
    "archs:" \
    "  - ${APKO_ARCH}" \
    > apko.yaml

# Build the OS tarball and extract rootfs
# apko creates an OCI tarball - we need to extract the actual filesystem layer
RUN apko build apko.yaml hawser-base:latest output.tar \
    && mkdir -p rootfs \
    && tar -xf output.tar \
    && LAYER=$(tar -tf output.tar | grep '.tar.gz$' | head -1) \
    && tar -xzf "$LAYER" -C rootfs

# -----------------------------------------------------------------------------
# Stage 2: Final Image (Scratch + Custom Wolfi OS)
# -----------------------------------------------------------------------------
FROM scratch

# Install our custom-built Wolfi OS (now we have /bin/sh!)
COPY --from=os-builder /work/rootfs/ /

WORKDIR /app

# Set up environment variables
ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
    SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt \
    PORT=2376 \
    DOCKER_SOCKET=/var/run/docker.sock \
    STACKS_DIR=/data/stacks \
    HEARTBEAT_INTERVAL=30 \
    REQUEST_TIMEOUT=30 \
    RECONNECT_DELAY=1 \
    MAX_RECONNECT_DELAY=60

# Create docker compose plugin symlink and data directory.
# Use ln -sf because newer docker-compose packages already ship this symlink.
RUN mkdir -p /usr/libexec/docker/cli-plugins \
    && ln -sf /usr/bin/docker-compose /usr/libexec/docker/cli-plugins/docker-compose \
    && mkdir -p /data/stacks

# Declare as volume to ensure writability even with --read-only
VOLUME /data/stacks

# Copy pre-built binary (provided by goreleaser)
COPY --chmod=0755 hawser /usr/local/bin/hawser

# Expose default port
EXPOSE 2376

# Health check - uses the built-in subcommand (auto-detects TLS mode)
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/hawser", "healthcheck"]

# Run as root to access Docker socket (can be changed with --user flag)
ENTRYPOINT ["/usr/local/bin/hawser"]

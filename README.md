# Hawser

<p align="center">
  <img src="logo/hawser.png" alt="Hawser Logo" width="200">
</p>

[![GitHub Release](https://img.shields.io/github/v/release/Finsys/hawser?style=flat-square&logo=github)](https://github.com/Finsys/hawser/releases/latest)
[![Build](https://img.shields.io/github/actions/workflow/status/Finsys/hawser/build.yml?branch=main&style=flat-square&logo=github&label=build)](https://github.com/Finsys/hawser/actions/workflows/build.yml)
[![Release](https://img.shields.io/github/actions/workflow/status/Finsys/hawser/release.yml?style=flat-square&logo=github&label=release)](https://github.com/Finsys/hawser/actions/workflows/release.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/Finsys/hawser?style=flat-square&logo=go)](https://go.dev/)
[![Docker Image](https://img.shields.io/badge/docker-ghcr.io%2Ffinsys%2Fhawser-blue?style=flat-square&logo=docker)](https://github.com/Finsys/hawser/pkgs/container/hawser)
[![License](https://img.shields.io/github/license/Finsys/hawser?style=flat-square)](LICENSE)

Remote Docker agent for [Dockhand](https://dockhand.pro) - manage Docker hosts anywhere.

## Overview

Hawser is a lightweight Go agent that enables Dockhand to manage Docker hosts in various network configurations. It supports two operational modes:

- **Standard Mode**: Agent listens for incoming connections (ideal for LAN/homelab with static IPs)
- **Edge Mode**: Agent initiates outbound WebSocket connection to Dockhand (ideal for VPS, NAT, dynamic IP)

## Quick Start

### Binary

Download the latest release from [GitHub Releases](https://github.com/Finsys/hawser/releases).

**Standard Mode:**

```bash
hawser --port 2376
```

**Standard Mode with Token Authentication** (optional):

```bash
TOKEN=your-secret-token hawser --port 2376
```

**Standard Mode with TLS** (optional):

```bash
TLS_CERT=/path/to/server.crt TLS_KEY=/path/to/server.key hawser --port 2376
```

**Standard Mode with TLS and Token** (recommended for production):

```bash
TLS_CERT=/path/to/server.crt TLS_KEY=/path/to/server.key TOKEN=your-secret-token hawser --port 2376
```

**Edge Mode:**

```bash
hawser --server wss://your-dockhand.example.com/api/hawser/connect --token your-token
```

**Edge Mode with Self-Signed Certificate:**

```bash
CA_CERT=/path/to/dockhand-ca.crt hawser --server wss://your-dockhand.example.com/api/hawser/connect --token your-token
```

**Edge Mode with TLS Skip Verify** (insecure, for testing):

```bash
TLS_SKIP_VERIFY=true hawser --server wss://your-dockhand.example.com/api/hawser/connect --token your-token
```

### Systemd Service

#### Quick Install

1. Download and install the binary:

```bash
curl -fsSL https://raw.githubusercontent.com/Finsys/hawser/main/scripts/install.sh | bash
```

2. Configure the service:

```bash
sudo nano /etc/hawser/config
```

Example config for **Standard Mode**:

```bash
# Standard mode - listen for connections
PORT=2376
# Optional: require token authentication
TOKEN=your-secret-token
```

Example config for **Edge Mode**:

```bash
# Edge mode - connect to Dockhand server
DOCKHAND_SERVER_URL=wss://your-dockhand.example.com/api/hawser/connect
TOKEN=your-agent-token
```

3. Start the service:

```bash
sudo systemctl enable --now hawser
```

#### Full Systemd Service File

If you prefer to set up the systemd service manually, here's the complete service file:

**`/etc/systemd/system/hawser.service`**

```ini
[Unit]
Description=Hawser - Remote Docker Agent for Dockhand
Documentation=https://github.com/Finsys/hawser
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/hawser
Restart=always
RestartSec=10
EnvironmentFile=/etc/hawser/config

# Security hardening
NoNewPrivileges=false
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/run/docker.sock /data/stacks

[Install]
WantedBy=multi-user.target
```

**`/etc/hawser/config`** (Standard Mode example):

```bash
# Hawser Configuration
# See https://github.com/Finsys/hawser for documentation

# Standard Mode
PORT=2376

# Docker socket path
DOCKER_SOCKET=/var/run/docker.sock

# Agent identification (optional)
# AGENT_NAME=my-server

# Token authentication (optional)
# TOKEN=your-secret-token

# TLS configuration (optional)
# TLS_CERT=/etc/hawser/server.crt
# TLS_KEY=/etc/hawser/server.key
```

**`/etc/hawser/config`** (Edge Mode example):

```bash
# Hawser Configuration
# See https://github.com/Finsys/hawser for documentation

# Edge Mode - connect to Dockhand server
DOCKHAND_SERVER_URL=wss://your-dockhand.example.com/api/hawser/connect
TOKEN=your-agent-token

# Docker socket path
DOCKER_SOCKET=/var/run/docker.sock

# Agent identification (optional)
# AGENT_NAME=my-server

# TLS configuration for self-signed Dockhand (optional)
# CA_CERT=/etc/hawser/dockhand-ca.crt
# TLS_SKIP_VERIFY=false

# Connection settings (optional)
# HEARTBEAT_INTERVAL=30
# RECONNECT_DELAY=1
# MAX_RECONNECT_DELAY=60
# WELCOME_TIMEOUT=30
```

**Manual installation steps:**

```bash
# 1. Download binary
curl -fsSL https://github.com/Finsys/hawser/releases/latest/download/hawser_linux_amd64.tar.gz | tar xz
sudo install -m 755 hawser /usr/local/bin/hawser

# 2. Create config directory
sudo mkdir -p /etc/hawser

# 3. Create config file (edit with your settings)
sudo tee /etc/hawser/config << 'EOF'
PORT=2376
DOCKER_SOCKET=/var/run/docker.sock
EOF

# 4. Create systemd service file
sudo tee /etc/systemd/system/hawser.service << 'EOF'
[Unit]
Description=Hawser - Remote Docker Agent for Dockhand
Documentation=https://github.com/Finsys/hawser
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/hawser
Restart=always
RestartSec=10
EnvironmentFile=/etc/hawser/config

NoNewPrivileges=false
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/run/docker.sock /data/stacks

[Install]
WantedBy=multi-user.target
EOF

# 5. Enable and start the service
sudo systemctl daemon-reload
sudo systemctl enable --now hawser

# 6. Check status
sudo systemctl status hawser
sudo journalctl -u hawser -f
```

### Docker

> **Image variants:** `ghcr.io/finsys/hawser:latest` is the full image and bundles the `docker-buildx`
> plugin, so stacks can build images on the host (`docker compose up --build`).
> `ghcr.io/finsys/hawser:latest-slim` drops the buildx plugin (~65 MB smaller) — use it when the agent
> only deploys pre-built images. Deploying from an image works on both; building from source does not work
> on the slim image. Both are multi-arch (armv7 is shared between the two tags).

> **Important:** If your compose stacks use relative file bind mounts (e.g., `./config.conf:/app/config.conf`),
> you **must** use a host path bind mount for `STACKS_DIR` — not a named volume. The path inside the container
> must match the host path, because Docker daemon resolves bind mount sources on the host filesystem.
>
> Example: `-v /opt/hawser-stacks:/opt/hawser-stacks -e STACKS_DIR=/opt/hawser-stacks`
>
> If your stacks only use named volumes or absolute paths, a named volume (`-v hawser_stacks:/data/stacks`) works fine.

**Standard Mode** - Agent listens for connections:

```bash
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /opt/hawser-stacks:/opt/hawser-stacks \
  -e STACKS_DIR=/opt/hawser-stacks \
  -p 2376:2376 \
  ghcr.io/finsys/hawser:latest
```

**Standard Mode with Token Authentication** (optional):

```bash
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /opt/hawser-stacks:/opt/hawser-stacks \
  -e STACKS_DIR=/opt/hawser-stacks \
  -p 2376:2376 \
  -e TOKEN=your-secret-token \
  ghcr.io/finsys/hawser:latest
```

**Standard Mode with TLS** (optional):

```bash
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /opt/hawser-stacks:/opt/hawser-stacks \
  -e STACKS_DIR=/opt/hawser-stacks \
  -v /path/to/certs:/certs:ro \
  -p 2376:2376 \
  -e TLS_CERT=/certs/server.crt \
  -e TLS_KEY=/certs/server.key \
  ghcr.io/finsys/hawser:latest
```

**Standard Mode with TLS and Token** (recommended for production):

```bash
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /opt/hawser-stacks:/opt/hawser-stacks \
  -e STACKS_DIR=/opt/hawser-stacks \
  -v /path/to/certs:/certs:ro \
  -p 2376:2376 \
  -e TLS_CERT=/certs/server.crt \
  -e TLS_KEY=/certs/server.key \
  -e TOKEN=your-secret-token \
  ghcr.io/finsys/hawser:latest
```

**Edge Mode** - Agent connects to Dockhand:

```bash
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /opt/hawser-stacks:/opt/hawser-stacks \
  -e STACKS_DIR=/opt/hawser-stacks \
  -e DOCKHAND_SERVER_URL=wss://your-dockhand.example.com/api/hawser/connect \
  -e TOKEN=your-agent-token \
  ghcr.io/finsys/hawser:latest
```

**Edge Mode with Self-Signed Certificate:**

```bash
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /opt/hawser-stacks:/opt/hawser-stacks \
  -e STACKS_DIR=/opt/hawser-stacks \
  -v /path/to/dockhand-ca.crt:/certs/ca.crt:ro \
  -e DOCKHAND_SERVER_URL=wss://your-dockhand.example.com/api/hawser/connect \
  -e TOKEN=your-agent-token \
  -e CA_CERT=/certs/ca.crt \
  ghcr.io/finsys/hawser:latest
```

### Building Docker image locally

For local development or custom builds, use the multi-stage `Dockerfile.dev` which builds from source:

```bash
# Clone repository
git clone https://github.com/Finsys/hawser.git
cd hawser

# Build from source (recommended for local development)
docker build -f Dockerfile.dev -t hawser:local .

# Run locally built image - Standard mode
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /opt/hawser-stacks:/opt/hawser-stacks \
  -e STACKS_DIR=/opt/hawser-stacks \
  -p 2376:2376 \
  hawser:local

# Run locally built image - Edge mode
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /opt/hawser-stacks:/opt/hawser-stacks \
  -e STACKS_DIR=/opt/hawser-stacks \
  -e DOCKHAND_SERVER_URL=wss://your-dockhand.example.com/api/hawser/connect \
  -e TOKEN=your-agent-token \
  hawser:local
```

**Note**: The default `Dockerfile` is used by GoReleaser for release builds and expects a pre-built binary. Use `Dockerfile.dev` for building from source. Both accept `--build-arg INCLUDE_BUILDX=false` to produce the slim (no buildx plugin) variant.

### Multi-architecture builds

The official images on `ghcr.io/finsys/hawser` are multi-arch (amd64 + arm64). For local multi-arch builds:

```bash
# Create a builder (first time only)
docker buildx create --name mybuilder --use

# Build for multiple platforms
docker buildx build -f Dockerfile.dev \
  --platform linux/amd64,linux/arm64 \
  -t hawser:local \
  --load .
```

### Docker health check

The Hawser Docker image includes a built-in health check that verifies Docker connectivity. This works in **both Standard and Edge modes**.

**How it works:**

- The container's `HEALTHCHECK` runs `hawser healthcheck`, which probes the `/_hawser/health` endpoint every 30 seconds (https with verification disabled when `TLS_CERT` is set). No `wget`/`curl` is shipped in the image.
- Both modes expose a minimal HTTP server on the configured port (default: 2376) for health checks
- The health check verifies that Hawser can communicate with the Docker daemon

**Health check response:**

```bash
# Standard mode
curl http://localhost:2376/_hawser/health
{"status":"healthy"}

# Edge mode (includes connection status)
curl http://localhost:2376/_hawser/health
{"status":"healthy","mode":"edge","connected":true}
```

**Container status:**

```bash
# Check container health status
docker inspect --format='{{.State.Health.Status}}' hawser
# healthy

# View health check logs
docker inspect --format='{{json .State.Health}}' hawser | jq
```

**Custom health check (optional):**

If you need custom health check settings, you can override the built-in health check:

```bash
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e DOCKHAND_SERVER_URL=wss://your-dockhand.example.com/api/hawser/connect \
  -e TOKEN=your-agent-token \
  --health-cmd="/usr/local/bin/hawser healthcheck" \
  --health-interval=30s \
  --health-timeout=5s \
  --health-retries=3 \
  --health-start-period=10s \
  ghcr.io/finsys/hawser:latest
```

## Configuration

Hawser is configured via environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `DOCKHAND_SERVER_URL` | WebSocket URL for Edge mode | - |
| `TOKEN` | Authentication token | - |
| `CA_CERT` | Path to CA certificate for Edge mode (self-signed Dockhand) | - |
| `TLS_SKIP_VERIFY` | Skip TLS verification for Edge mode (insecure) | `false` |
| `PORT` | HTTP server port (Standard mode) | `2376` |
| `TLS_CERT` | Path to TLS certificate (Standard mode server cert) | - |
| `TLS_KEY` | Path to TLS private key (Standard mode server key) | - |
| `BIND_ADDRESS` | Address to bind to (use `127.0.0.1` to restrict to localhost) | `0.0.0.0` |
| `DOCKER_SOCKET` | Docker socket path | `/var/run/docker.sock` |
| `STACKS_DIR` | Directory for compose stack files (requires Dockhand 1.0.5+). Use a host path bind mount with matching paths if stacks use relative file bind mounts. | `/data/stacks` |
| `AGENT_ID` | Unique agent identifier | Auto-generated UUID |
| `AGENT_NAME` | Human-readable agent name | Hostname |
| `HEARTBEAT_INTERVAL` | Heartbeat interval in seconds | `30` |
| `REQUEST_TIMEOUT` | Request timeout in seconds | `30` |
| `RECONNECT_DELAY` | Initial reconnect delay (Edge mode) | `1` |
| `MAX_RECONNECT_DELAY` | Maximum reconnect delay | `60` |
| `WELCOME_TIMEOUT` | Timeout in seconds waiting for welcome after hello (Edge mode) | `30` |
| `LOG_LEVEL` | Logging level: `debug`, `info`, `warn`, `error` | `info` |
| `SKIP_DF_COLLECTION` | Skip disk usage collection (see below) | - |

### Mode Detection

Hawser automatically detects the operational mode:

- If `DOCKHAND_SERVER_URL` and `TOKEN` are set → **Edge Mode**
- Otherwise → **Standard Mode**

### Log Levels

The `LOG_LEVEL` environment variable controls verbosity:

| Level | Description |
|-------|-------------|
| `debug` | All messages including Docker API calls (method, path, status codes) |
| `info` | Standard operational messages (connections, startup, shutdown) |
| `warn` | Warnings only |
| `error` | Errors only |

**Example: Debug mode**

```bash
# Binary
LOG_LEVEL=debug hawser --port 2376

# Docker
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -p 2376:2376 \
  -e LOG_LEVEL=debug \
  ghcr.io/finsys/hawser:latest
```

Debug mode logs all Docker API requests, which is useful for troubleshooting connectivity issues.

## Features

### Docker API Proxy

Hawser provides full access to the Docker API:

- Container management (create, start, stop, remove)
- Image operations (pull, list, remove)
- Volume and network management
- Log streaming
- Interactive exec sessions

### Docker Compose Support

Hawser includes Docker Compose support for stack operations:

- `up` - Deploy stack
- `down` - Remove stack
- `pull` - Pull images
- `ps` - List services
- `logs` - View logs

> Building images on the host (`docker compose up --build`) requires the `docker-buildx` plugin, which
> is present in the full image only — not in the `-slim` image.

### Host Metrics

Hawser collects and reports host metrics:

- CPU usage (per-core and total)
- Memory (total, used, available)
- Disk usage (Docker data directory)
- Network I/O statistics

Metrics are sent every 30 seconds in Edge mode.

#### Disabling Disk Usage Collection

On some systems, particularly NAS devices (Synology, QNAP, TrueNAS) or hosts with many mounted volumes, the disk usage collection can cause performance issues. The `statfs` system call used to check disk space can be slow when there are many mounted filesystems or network mounts.

To disable disk usage collection, set the `SKIP_DF_COLLECTION` environment variable to any non-empty value:

```bash
# Binary
SKIP_DF_COLLECTION=1 hawser --port 2376

# Docker
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -e SKIP_DF_COLLECTION=1 \
  ghcr.io/finsys/hawser:latest

# Systemd config (/etc/hawser/config)
SKIP_DF_COLLECTION=1
```

When disabled, disk metrics will show as 0 in Dockhand (displayed as "N/A").

### Reliability

- **Auto-reconnect**: Edge mode automatically reconnects with exponential backoff and ±25% random jitter to prevent thundering herds when multiple agents reconnect simultaneously (e.g., after a Dockhand restart)
- **Heartbeat**: Regular keepalive messages maintain connection health
- **Graceful shutdown**: Clean shutdown on SIGTERM/SIGINT

### Docker API Version Compatibility

Hawser automatically negotiates the Docker API version with the daemon. When running Docker Compose operations, Hawser sets the `DOCKER_API_VERSION` environment variable to match the daemon's reported API version. This ensures compatibility when the Docker CLI version differs from the daemon version - for example, when using an older Docker CLI with a newer Docker daemon that requires a higher minimum API version.

## API Endpoints

### Standard Mode

In Standard mode, Hawser proxies all Docker API endpoints plus:

| Endpoint | Description |
|----------|-------------|
| `/_hawser/health` | Health check (no auth required) |
| `/_hawser/info` | Agent information |

### Health Check

```bash
# Standard mode
curl http://localhost:2376/_hawser/health
# {"status":"healthy"}

# Edge mode (includes WebSocket connection status)
curl http://localhost:2376/_hawser/health
# {"status":"healthy","mode":"edge","connected":true}
```

## Security Considerations

1. **Docker Socket Access**: Hawser requires access to the Docker socket, which provides full control over Docker. Run with appropriate access controls.

2. **Network Security**:
   - Standard mode: Use TLS and/or token authentication
   - Edge mode: Use WSS (TLS-encrypted WebSocket)

3. **Token Security**: Tokens are plain-text strings — any printable ASCII works. There are no format restrictions (hex, base64, etc.).

   **Recommended**: Use Dockhand's built-in token generator (when adding new Environment in Settings → Environments). It generates a cryptographically secure 32-byte base64url token and only shows it once.

   **Manual generation** (if not using Dockhand's UI):
   ```bash
   # Option 1: openssl (recommended, 32 bytes / 43 characters)
   openssl rand -base64 32

   # Option 2: pwgen (20+ characters recommended)
   pwgen -s 32 1

   # Option 3: /dev/urandom
   head -c 32 /dev/urandom | base64
   ```

   Use at least 24 characters. Tokens are hashed with Argon2id on the Dockhand side, so length and randomness matter more than character set.

## Building from Source

```bash
# Clone repository
git clone https://github.com/Finsys/hawser.git
cd hawser

# Build
go build -o hawser ./cmd/hawser

# Run
./hawser --port 2376
```

## Docker Build

```bash
docker build -t hawser .
```

## Contributing

Contributions are welcome! Please read the contributing guidelines before submitting a pull request.

## License

MIT License - see [LICENSE](LICENSE) for details.

## Related

- [Dockhand](https://dockhand.pro) - Modern Docker management application
- [Docker Engine API](https://docs.docker.com/engine/api/) - Docker API documentation

---

<p align="center">
  Made with ❤️ and mass amounts of ☕ by Finsys for <a href="https://dockhand.pro">Dockhand</a>
</p>

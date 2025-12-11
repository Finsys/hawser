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
ReadWritePaths=/var/run/docker.sock

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

# Connection settings (optional)
# HEARTBEAT_INTERVAL=30
# RECONNECT_DELAY=1
# MAX_RECONNECT_DELAY=60
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
ReadWritePaths=/var/run/docker.sock

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

**Standard Mode** - Agent listens for connections:

```bash
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -p 2376:2376 \
  ghcr.io/finsys/hawser:latest
```

**Standard Mode with Token Authentication** (optional):

```bash
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -p 2376:2376 \
  -e TOKEN=your-secret-token \
  ghcr.io/finsys/hawser:latest
```

**Standard Mode with TLS** (optional):

```bash
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
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
  -e DOCKHAND_SERVER_URL=wss://your-dockhand.example.com/api/hawser/connect \
  -e TOKEN=your-agent-token \
  ghcr.io/finsys/hawser:latest
```

## Configuration

Hawser is configured via environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `DOCKHAND_SERVER_URL` | WebSocket URL for Edge mode | - |
| `TOKEN` | Authentication token | - |
| `PORT` | HTTP server port (Standard mode) | `2376` |
| `TLS_CERT` | Path to TLS certificate | - |
| `TLS_KEY` | Path to TLS private key | - |
| `DOCKER_SOCKET` | Docker socket path | `/var/run/docker.sock` |
| `AGENT_ID` | Unique agent identifier | Auto-generated UUID |
| `AGENT_NAME` | Human-readable agent name | Hostname |
| `HEARTBEAT_INTERVAL` | Heartbeat interval in seconds | `30` |
| `REQUEST_TIMEOUT` | Request timeout in seconds | `30` |
| `RECONNECT_DELAY` | Initial reconnect delay (Edge mode) | `1` |
| `MAX_RECONNECT_DELAY` | Maximum reconnect delay | `60` |

### Mode Detection

Hawser automatically detects the operational mode:

- If `DOCKHAND_SERVER_URL` and `TOKEN` are set → **Edge Mode**
- Otherwise → **Standard Mode**

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

### Host Metrics

Hawser collects and reports host metrics:

- CPU usage (per-core and total)
- Memory (total, used, available)
- Disk usage (Docker data directory)
- Network I/O statistics

Metrics are sent every 30 seconds in Edge mode.

### Reliability

- **Auto-reconnect**: Edge mode automatically reconnects with exponential backoff
- **Heartbeat**: Regular keepalive messages maintain connection health
- **Graceful shutdown**: Clean shutdown on SIGTERM/SIGINT

## API Endpoints

### Standard Mode

In Standard mode, Hawser proxies all Docker API endpoints plus:

| Endpoint | Description |
|----------|-------------|
| `/_hawser/health` | Health check (no auth required) |
| `/_hawser/info` | Agent information |

### Health Check

```bash
curl http://localhost:2376/_hawser/health
# {"status":"healthy"}
```

## Security Considerations

1. **Docker Socket Access**: Hawser requires access to the Docker socket, which provides full control over Docker. Run with appropriate access controls.

2. **Network Security**:
   - Standard mode: Use TLS and/or token authentication
   - Edge mode: Use WSS (TLS-encrypted WebSocket)

3. **Token Security**: Tokens should be strong, randomly generated strings. In Dockhand, tokens are shown only once when generated.

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

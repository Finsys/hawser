# Hawser

Remote Docker agent for [Dockhand](https://dockhand.io) - manage Docker hosts anywhere.

## Overview

Hawser is a lightweight Go agent that enables Dockhand to manage Docker hosts in various network configurations. It supports two operational modes:

- **Standard Mode**: Agent listens for incoming connections (ideal for LAN/homelab with static IPs)
- **Edge Mode**: Agent initiates outbound WebSocket connection to Dockhand (ideal for VPS, NAT, dynamic IP)

## Quick Start

### Docker (Recommended)

**Standard Mode** - Agent listens for connections:

```bash
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -p 2375:2375 \
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

### Binary

Download the latest release from [GitHub Releases](https://github.com/Finsys/hawser/releases).

**Standard Mode:**

```bash
hawser --port 2375
```

**Edge Mode:**

```bash
hawser --server wss://your-dockhand.example.com/api/hawser/connect --token your-token
```

### Systemd Service

1. Download and install the binary:

```bash
curl -fsSL https://raw.githubusercontent.com/Finsys/hawser/main/scripts/install.sh | bash
```

2. Configure the service:

```bash
sudo nano /etc/hawser/config
```

3. Start the service:

```bash
sudo systemctl enable --now hawser
```

## Configuration

Hawser is configured via environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `DOCKHAND_SERVER_URL` | WebSocket URL for Edge mode | - |
| `TOKEN` | Authentication token | - |
| `PORT` | HTTP server port (Standard mode) | `2375` |
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

### TLS Configuration (Standard Mode)

To enable HTTPS:

```bash
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v /path/to/certs:/certs:ro \
  -p 2376:2375 \
  -e TLS_CERT=/certs/server.crt \
  -e TLS_KEY=/certs/server.key \
  ghcr.io/finsys/hawser:latest
```

### Token Authentication (Standard Mode)

To require token authentication:

```bash
docker run -d \
  --name hawser \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -p 2375:2375 \
  -e TOKEN=your-secret-token \
  ghcr.io/finsys/hawser:latest
```

Clients must include the token in requests:

```bash
curl -H "X-Hawser-Token: your-secret-token" http://localhost:2375/containers/json
```

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
curl http://localhost:2375/_hawser/health
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
./hawser --port 2375
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

- [Dockhand](https://dockhand.io) - Modern Docker management application
- [Docker Engine API](https://docs.docker.com/engine/api/) - Docker API documentation

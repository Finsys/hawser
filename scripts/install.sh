#!/bin/bash
set -e

# Hawser Installation Script
# Usage: curl -fsSL https://raw.githubusercontent.com/Finsys/hawser/main/scripts/install.sh | bash

VERSION="${HAWSER_VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
CONFIG_DIR="/etc/hawser"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64)
        ARCH="amd64"
        ;;
    aarch64|arm64)
        ARCH="arm64"
        ;;
    armv7l|armv7|arm)
        ARCH="arm"
        ;;
    *)
        echo "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

echo "Installing Hawser for ${OS}/${ARCH}..."

# Determine download URL
if [ "$VERSION" = "latest" ]; then
    # Fetch the latest release version from GitHub API
    LATEST_VERSION=$(curl -fsSL "https://api.github.com/repos/Finsys/hawser/releases/latest" | grep '"tag_name"' | sed -E 's/.*"tag_name": "v?([^"]+)".*/\1/')
    if [ -z "$LATEST_VERSION" ]; then
        echo "Error: Could not determine latest version"
        exit 1
    fi
    echo "Latest version: $LATEST_VERSION"
    DOWNLOAD_URL="https://github.com/Finsys/hawser/releases/download/v${LATEST_VERSION}/hawser_${LATEST_VERSION}_${OS}_${ARCH}.tar.gz"
else
    # Remove 'v' prefix if present
    VERSION_NUM="${VERSION#v}"
    DOWNLOAD_URL="https://github.com/Finsys/hawser/releases/download/v${VERSION_NUM}/hawser_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
fi

# Create temporary directory
TMP_DIR=$(mktemp -d)
trap "rm -rf $TMP_DIR" EXIT

# Download and extract
echo "Downloading from $DOWNLOAD_URL..."
curl -fsSL "$DOWNLOAD_URL" -o "$TMP_DIR/hawser.tar.gz"
tar -xzf "$TMP_DIR/hawser.tar.gz" -C "$TMP_DIR"

# Install binary
echo "Installing binary to $INSTALL_DIR..."
sudo mkdir -p "$INSTALL_DIR"
sudo install -m 755 "$TMP_DIR/hawser" "$INSTALL_DIR/hawser"

# Create config directory
echo "Creating config directory..."
sudo mkdir -p "$CONFIG_DIR"

# Stacks directory: STACKS_DIR from the environment, else from an existing config, else the default
STACKS_DIR="${STACKS_DIR:-$(sudo sed -n 's/^STACKS_DIR=//p' "$CONFIG_DIR/config" 2>/dev/null | tail -1)}"
STACKS_DIR="${STACKS_DIR%\"}"; STACKS_DIR="${STACKS_DIR#\"}"; STACKS_DIR="${STACKS_DIR%\'}"; STACKS_DIR="${STACKS_DIR#\'}"
STACKS_DIR="${STACKS_DIR:-/data/stacks}"
echo "Creating stacks directory $STACKS_DIR..."
sudo mkdir -p "$STACKS_DIR"

# Create default config file if it doesn't exist
if [ ! -f "$CONFIG_DIR/config" ]; then
    echo "Creating default config file..."
    sudo tee "$CONFIG_DIR/config" > /dev/null << 'EOF'
# Hawser Configuration
# See https://github.com/Finsys/hawser for documentation

# Docker socket path
DOCKER_SOCKET=/var/run/docker.sock

#################### Standard Mode (comment out for Edge mode) ####################
PORT=2376

# REQUIRED: Standard mode listens on all interfaces and proxies to the Docker
# socket, so a token is required to start. Set the same token in Dockhand when
# adding this environment. (For a local-only agent instead, leave TOKEN unset
# and add BIND_ADDRESS=127.0.0.1.)
TOKEN=change-me-before-starting

# TLS configuration (optional, Standard mode only)
# TLS_CERT=/etc/hawser/server.crt
# TLS_KEY=/etc/hawser/server.key

################# Edge Mode (uncomment and configure for Edge mode) ###############
# DOCKHAND_SERVER_URL=wss://your-dockhand.example.com/api/hawser/connect
# TOKEN=your-agent-token-taken-from-dockhand

# TLS configuration for self-signed Dockhand (optional, Edge mode only)
# CA_CERT=/etc/hawser/dockhand-ca.crt
# TLS_SKIP_VERIFY=false

# Agent identification (optional)
# AGENT_NAME=my-server

# Edge mode only listens for its health check (on PORT, default 2376).
# Restrict it to localhost so the host has no externally-reachable surface:
# BIND_ADDRESS=127.0.0.1
EOF
    if [ "$STACKS_DIR" != "/data/stacks" ]; then
        echo "STACKS_DIR=$STACKS_DIR" | sudo tee -a "$CONFIG_DIR/config" > /dev/null
    fi
fi

# The config can hold a TOKEN, so it must not be world-readable.
sudo chmod 600 "$CONFIG_DIR/config"

# Install systemd service if systemd is available
if command -v systemctl &> /dev/null; then
    echo "Installing systemd service..."
    # The service may only write to the stacks directory it was installed with; after
    # changing STACKS_DIR, run this installer again or add the path with `systemctl edit hawser`.
    sudo tee /etc/systemd/system/hawser.service > /dev/null << EOF
[Unit]
Description=Hawser - Remote Docker Agent for Dockhand
Documentation=https://github.com/Finsys/hawser
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
ExecStart=${INSTALL_DIR}/hawser
Restart=always
RestartSec=10
EnvironmentFile=/etc/hawser/config

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=hawser

# Security hardening (relaxed for Docker access)
NoNewPrivileges=false
ProtectSystem=strict
ProtectHome=true
# docker login, compose and buildx need a writable config dir and temp dir, which the
# two lines above take away. /run/hawser is tmpfs: registry logins never reach the disk.
RuntimeDirectory=hawser
Environment=DOCKER_CONFIG=/run/hawser TMPDIR=/run/hawser
# The stacks directory must match STACKS_DIR in /etc/hawser/config.
ReadWritePaths=/var/run/docker.sock -${STACKS_DIR}

# Resource limits
LimitNOFILE=65535
LimitNPROC=4096

[Install]
WantedBy=multi-user.target
EOF

    echo "Reloading systemd..."
    sudo systemctl daemon-reload
    INIT_SYSTEM="systemd"
elif command -v rc-service &> /dev/null; then
    # OpenRC (Alpine Linux)
    echo "Installing OpenRC service..."

    # Create wrapper script that sources config and runs hawser
    sudo tee /usr/local/bin/hawser-wrapper > /dev/null << EOF
#!/bin/sh
# Wrapper script for hawser that loads config file
if [ -f /etc/hawser/config ]; then
    set -a  # Automatically export all variables
    . /etc/hawser/config
    set +a
fi
exec "${INSTALL_DIR}/hawser" "\$@"
EOF
    sudo chmod +x /usr/local/bin/hawser-wrapper

    # Create init script that uses the wrapper
    sudo tee /etc/init.d/hawser > /dev/null << 'EOF'
#!/sbin/openrc-run

name="hawser"
description="Hawser - Remote Docker Agent for Dockhand"
command="/usr/local/bin/hawser-wrapper"
command_background="yes"
pidfile="/run/${RC_SVCNAME}.pid"
start_stop_daemon_args="--stdout /var/log/hawser.log --stderr /var/log/hawser.log"

depend() {
    need net docker
    after docker
}
EOF
    sudo chmod +x /etc/init.d/hawser
    INIT_SYSTEM="openrc"
fi

echo ""
echo "Hawser installed successfully!"
echo ""
echo "Configuration: $CONFIG_DIR/config"
echo ""

if [ "$INIT_SYSTEM" = "systemd" ]; then
    echo "Service management:"
    echo "  sudo systemctl start hawser    # Start the service"
    echo "  sudo systemctl stop hawser     # Stop the service"
    echo "  sudo systemctl status hawser   # Check service status"
    echo "  sudo systemctl enable hawser   # Enable on boot"
    echo "  sudo journalctl -u hawser -f   # View logs"
    echo ""
elif [ "$INIT_SYSTEM" = "openrc" ]; then
    echo "Service management:"
    echo "  sudo rc-service hawser start   # Start the service"
    echo "  sudo rc-service hawser stop    # Stop the service"
    echo "  sudo rc-service hawser status  # Check service status"
    echo "  sudo rc-update add hawser      # Enable on boot"
    echo "  tail -f /var/log/hawser.log    # View logs"
    echo ""
fi

echo "Manual run (for testing):"
echo "  Standard mode: PORT=2376 TOKEN=secret hawser"
echo "  Edge mode:     DOCKHAND_SERVER_URL=wss://... TOKEN=your-token hawser"

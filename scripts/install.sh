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
sudo install -m 755 "$TMP_DIR/hawser" "$INSTALL_DIR/hawser"

# Create config directory
echo "Creating config directory..."
sudo mkdir -p "$CONFIG_DIR"

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

# TLS configuration (optional, Standard mode only)
# TLS_CERT=/etc/hawser/server.crt
# TLS_KEY=/etc/hawser/server.key

# Token authentication (optional)
# TOKEN=your-secret-token

################# Edge Mode (uncomment and configure for Edge mode) ###############
# DOCKHAND_SERVER_URL=wss://your-dockhand.example.com/api/hawser/connect
# TOKEN=your-agent-token-taken-from-dockhand

# Agent identification (optional)
# AGENT_NAME=my-server
EOF
fi

# Install systemd service if systemd is available
if command -v systemctl &> /dev/null; then
    echo "Installing systemd service..."
    sudo tee /etc/systemd/system/hawser.service > /dev/null << 'EOF'
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
EOF

    echo "Reloading systemd..."
    sudo systemctl daemon-reload
    INIT_SYSTEM="systemd"
elif command -v rc-service &> /dev/null; then
    # OpenRC (Alpine Linux)
    echo "Installing OpenRC service..."
    sudo tee /etc/init.d/hawser > /dev/null << 'EOF'
#!/sbin/openrc-run

name="hawser"
description="Hawser - Remote Docker Agent for Dockhand"
command="/usr/local/bin/hawser"
command_background="yes"
pidfile="/run/${RC_SVCNAME}.pid"
start_stop_daemon_args="--stdout /var/log/hawser.log --stderr /var/log/hawser.log"

depend() {
    need net docker
    after docker
}

start_pre() {
    if [ -f /etc/hawser/config ]; then
        . /etc/hawser/config
        export DOCKER_SOCKET PORT TLS_CERT TLS_KEY TOKEN DOCKHAND_SERVER_URL AGENT_NAME
    fi
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
echo "  Standard mode: hawser --port 2376"
echo "  Edge mode:     hawser --server wss://... --token your-token"

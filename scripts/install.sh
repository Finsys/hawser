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

# Standard Mode (comment out for Edge mode)
PORT=2376

# Edge Mode (uncomment and configure for Edge mode)
# DOCKHAND_SERVER_URL=wss://your-dockhand.example.com/api/hawser/connect
# TOKEN=your-agent-token

# Docker socket path
DOCKER_SOCKET=/var/run/docker.sock

# Agent identification (optional)
# AGENT_NAME=my-server

# TLS configuration (optional, Standard mode only)
# TLS_CERT=/etc/hawser/server.crt
# TLS_KEY=/etc/hawser/server.key

# Token authentication (optional, Standard mode only)
# TOKEN=your-secret-token
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

    echo ""
    echo "Systemd service installed. To start Hawser:"
    echo "  1. Edit /etc/hawser/config with your settings"
    echo "  2. sudo systemctl enable --now hawser"
    echo "  3. sudo systemctl status hawser"
fi

echo ""
echo "Hawser installed successfully!"
echo ""
echo "Quick start:"
echo "  Standard mode: hawser --port 2375"
echo "  Edge mode:     hawser --server wss://... --token your-token"
echo ""
echo "Configuration file: $CONFIG_DIR/config"

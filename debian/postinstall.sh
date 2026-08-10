#!/bin/sh
set -e

[ "$1" = "configure" ] || exit 0

. /usr/share/debconf/confmodule

CONFIG_FILE=/etc/hawser/config
CONFIG_DIR=/etc/hawser
SERVICE=hawser
SERVICE_USER=hawser

# Create group and user
if ! getent group "$SERVICE_USER" >/dev/null; then
	echo "Creating $SERVICE_USER group"
	addgroup --quiet --system "$SERVICE_USER"
fi

if ! getent passwd "$SERVICE_USER" >/dev/null; then
	echo "Creating $SERVICE_USER user"
	adduser --quiet --system "$SERVICE_USER" \
		--ingroup "$SERVICE_USER" \
		--no-create-home \
		--home /nonexistent \
		--gecos "System user for $SERVICE"
fi

# Add user to Docker group so it can use the socket
adduser "$SERVICE_USER" docker || true

# Create config file if it doesn't already exist
if [ ! -f "$CONFIG_FILE" ]; then
  install -d -m 0750 -o root -g "$SERVICE_USER" "$CONFIG_DIR"
  {
    echo "# Hawser Configuration"
    echo "# See https://github.com/Finsys/hawser for documentation" 
    echo 
    echo "DOCKER_SOCKET=/run/docker.sock" 
  } > "$CONFIG_FILE"
	chmod 0600 "$CONFIG_FILE"
	chown "$SERVICE_USER":"$SERVICE_USER" "$CONFIG_FILE"
fi;

# Add missing configs
if ! grep -q "^BIND_ADDRESS=" "$CONFIG_FILE"; then
  db_get hawser/bind_address
  echo "BIND_ADDRESS=$RET" >> "$CONFIG_FILE"
fi;

if ! grep -q "^PORT=" "$CONFIG_FILE"; then
  db_get hawser/port
  echo "PORT=$RET" >> "$CONFIG_FILE"
fi;

if ! grep -q "^TOKEN=" "$CONFIG_FILE"; then
  db_get hawser/token
  echo "TOKEN=$RET" >> "$CONFIG_FILE"
fi;

deb-systemd-helper enable "$SERVICE".service
systemctl daemon-reload
deb-systemd-invoke start "$SERVICE".service || echo "could not start $SERVICE.service!"

#!/bin/sh
set -e

SERVICE=hawser
SERVICE_USER=hawser
CONFIG_DIR=/etc/hawser
CONFIG_FILE=$CONFIG_DIR/config
STATE_DIR=/var/lib/hawser

case "$1" in
  configure)
    ;;
  abort-upgrade | abort-deconfigure | abort-remove)
    # Nothing to configure; just bring the service back below.
    ;;
  *)
    exit 0
    ;;
esac

incomplete=
if [ "$1" = "configure" ]; then
  . /usr/share/debconf/confmodule

  # Create group and user
  if ! getent group "$SERVICE_USER" >/dev/null; then
    echo "Creating $SERVICE_USER group"
    addgroup --quiet --system "$SERVICE_USER"
  fi

  if ! getent passwd "$SERVICE_USER" >/dev/null; then
    echo "Creating $SERVICE_USER user"
    # The home directory is created by the unit's StateDirectory=.
    adduser --quiet --system "$SERVICE_USER" \
      --ingroup "$SERVICE_USER" \
      --no-create-home \
      --home "$STATE_DIR" \
      --gecos "System user for $SERVICE"
  fi

  # Add user to Docker group so it can use the socket
  if getent group docker >/dev/null &&
    ! id -nG "$SERVICE_USER" | tr ' ' '\n' | grep -qx docker; then
    adduser --quiet "$SERVICE_USER" docker
  fi

  # Write the config from the debconf answers on first installation only. After
  # that it belongs to the admin, and upgrades leave it alone.
  if [ ! -f "$CONFIG_FILE" ]; then
    # Quote values for systemd's EnvironmentFile syntax, in which a backslash
    # inside double quotes escapes only \, ", ` and $.
    write_setting() {
      escaped=$(printf '%s' "$2" | sed 's/[\\"`$]/\\&/g')
      printf '%s="%s"\n' "$1" "$escaped"
    }

    db_get hawser/mode
    mode=$RET
    case "$mode" in
      standard)
        db_get hawser/bind_address
        bind_address=$RET
        db_get hawser/port
        port=$RET
        db_get hawser/token
        token=$RET
        ;;
      edge)
        db_get hawser/server_url
        server_url=$RET
        db_get hawser/edge_token
        token=$RET
        ;;
    esac
    db_get hawser/agent_name
    agent_name=$RET

    install -d -m 0750 -o root -g "$SERVICE_USER" "$CONFIG_DIR"
    umask 077
    {
      cat <<'EOF'
# Hawser Configuration
# See https://github.com/Finsys/hawser for documentation
#
# Run "service hawser restart" after editing this file. Files referenced
# below must be readable by the hawser user.

DOCKER_SOCKET=/run/docker.sock
STACKS_DIR=/var/lib/hawser/stacks

# Log level: debug, info, warn or error
# LOG_LEVEL=info

# TLS configuration (optional, Standard mode only; both required together)
# TLS_CERT=/etc/hawser/server.crt
# TLS_KEY=/etc/hawser/server.key

# TLS configuration for self-signed Dockhand (optional, Edge mode only)
# CA_CERT=/etc/hawser/dockhand-ca.crt
# TLS_SKIP_VERIFY=false

EOF
      case "$mode" in
        standard)
          if [ -n "$bind_address" ]; then
            write_setting BIND_ADDRESS "$bind_address"
          fi
          write_setting PORT "$port"
          ;;
        edge)
          # Written even if empty, to show where it goes.
          write_setting DOCKHAND_SERVER_URL "$server_url"
          # Edge mode only listens for health checks, so keep that local.
          write_setting BIND_ADDRESS 127.0.0.1
          ;;
      esac
      if [ -n "$token" ] || [ "$mode" = "edge" ]; then
        write_setting TOKEN "$token"
      fi
      if [ -n "$agent_name" ]; then
        write_setting AGENT_NAME "$agent_name"
      fi
      # Give the agent a stable identity; Hawser otherwise picks a new one on
      # every start.
      write_setting AGENT_ID "$(cat /proc/sys/kernel/random/uuid)"
    } >"$CONFIG_FILE.dpkg-new"
    chmod 0600 "$CONFIG_FILE.dpkg-new"
    chown "$SERVICE_USER":"$SERVICE_USER" "$CONFIG_FILE.dpkg-new"
    mv "$CONFIG_FILE.dpkg-new" "$CONFIG_FILE"

    # config.sh can't re-prompt with a noninteractive frontend, so edge settings
    # may be incomplete. Hawser can't run like that: with no server URL it
    # would fall back to standard mode.
    if [ "$mode" = "edge" ]; then
      case "$server_url" in
        ws://?* | wss://?*) ;;
        *)
          echo "Warning: no valid Dockhand server URL is set." >&2
          incomplete=1
          ;;
      esac
      if [ -z "$token" ]; then
        echo "Warning: no Dockhand Edge token is set." >&2
        incomplete=1
      fi
    else
      echo "Hawser's authentication token is stored in $CONFIG_FILE; use it when adding this host in Dockhand."
    fi
  fi

  # Release debconf before starting the daemon.
  db_stop
fi

# Based on debhelper's systemd snippets.
deb-systemd-helper unmask "$SERVICE".service >/dev/null || true

if [ -n "$incomplete" ]; then
  # Record the unit without enabling it, like dh_installsystemd --no-enable.
  # was-enabled then returns false, so upgrades don't enable it either.
  deb-systemd-helper update-state "$SERVICE".service >/dev/null || true
  echo "Not enabling $SERVICE. Set DOCKHAND_SERVER_URL and TOKEN in $CONFIG_FILE, then run 'systemctl enable --now $SERVICE'." >&2
  exit 0
fi

# was-enabled defaults to true, so new installations run enable. Units the
# admin disabled stay disabled.
if deb-systemd-helper --quiet was-enabled "$SERVICE".service; then
  deb-systemd-helper enable "$SERVICE".service >/dev/null || true
else
  deb-systemd-helper update-state "$SERVICE".service >/dev/null || true
fi

if [ -d /run/systemd/system ]; then
  systemctl --system daemon-reload >/dev/null || true
  if [ -n "$2" ]; then
    action=restart
  else
    action=start
  fi
  deb-systemd-invoke "$action" "$SERVICE".service >/dev/null ||
    echo "Could not $action $SERVICE.service; check 'journalctl -u $SERVICE'." >&2
fi

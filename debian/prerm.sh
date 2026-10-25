#!/bin/sh
set -e

SERVICE=hawser

# On upgrade the service keeps running and postinst restarts it.
if [ "$1" = "remove" ] && [ -d /run/systemd/system ]; then
  deb-systemd-invoke stop "$SERVICE".service >/dev/null || true
fi

#!/bin/sh
set -e

SERVICE=hawser

# Based on debhelper's systemd snippets.
if [ "$1" = "remove" ] && [ -d /run/systemd/system ]; then
  systemctl --system daemon-reload >/dev/null || true
fi

if [ "$1" = "remove" ] && command -v deb-systemd-helper >/dev/null; then
  deb-systemd-helper mask "$SERVICE".service >/dev/null || true
fi

if [ "$1" = "purge" ]; then
  if command -v deb-systemd-helper >/dev/null; then
    deb-systemd-helper purge "$SERVICE".service >/dev/null || true
    deb-systemd-helper unmask "$SERVICE".service >/dev/null || true
  fi

  # debconf may already have been removed.
  if [ -e /usr/share/debconf/confmodule ]; then
    . /usr/share/debconf/confmodule
    db_purge
  fi

  rm -f /etc/hawser/config
  rmdir /etc/hawser 2>/dev/null || true
  # Remove Docker CLI state (including registry credentials), but keep stack
  # files: they're the user's, and running containers may still use them.
  if [ -d /var/lib/hawser ]; then
    find /var/lib/hawser -mindepth 1 -maxdepth 1 ! -name stacks -exec rm -rf {} +
    if ! rmdir /var/lib/hawser/stacks 2>/dev/null && [ -d /var/lib/hawser/stacks ]; then
      echo "Keeping stack files in /var/lib/hawser/stacks."
    fi
    rmdir /var/lib/hawser 2>/dev/null || true
  fi
fi

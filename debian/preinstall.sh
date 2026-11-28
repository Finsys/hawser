#!/bin/sh
set -e

case "$1" in
  install | upgrade)
    # The package's systemd unit is installed at `/usr/lib/systemd/system/hawser.service`.
    # The presence of a unit in `/etc` means that Hawser was previously manually installed.
    # Symlink to /dev/null is a masked service and should not block installation.
    unit=/etc/systemd/system/hawser.service
    if { [ -e "$unit" ] || [ -L "$unit" ]; } &&
      [ "$(readlink "$unit")" != /dev/null ]; then
      echo "Cannot install Hawser: $unit already exists." >&2
      echo "Migrate the existing installation before installing the Debian package:" >&2
      echo "https://github.com/Finsys/hawser#migrating-from-installsh-or-a-manual-systemd-installation" >&2
      exit 1
    fi
    ;;
esac

#!/bin/sh
set -e

if [ "$1" = "purge" ]; then
	. /usr/share/debconf/confmodule
	db_purge
	rm -f /etc/hawser/config
	rmdir /etc/hawser 2>/dev/null || true
fi

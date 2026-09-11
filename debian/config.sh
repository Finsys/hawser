#!/bin/sh
set -e

. /usr/share/debconf/confmodule
db_version 2.0

db_input high hawser/bind_address || true
db_input high hawser/port || true
db_input high hawser/token || true
db_go

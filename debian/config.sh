#!/bin/sh
set -e

. /usr/share/debconf/confmodule
db_version 2.0

CONFIG_FILE=/etc/hawser/config

# postinst only writes the config file on first installation, and it's left
# for the admin to edit after that, so there's nothing to ask.
if [ -f "$CONFIG_FILE" ]; then
  exit 0
fi

is_port() {
  case "$1" in
    '' | *[!0-9]* | ??????*) return 1 ;;
  esac
  [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
}

is_ws_url() {
  case "$1" in
    ws://?* | wss://?*) return 0 ;;
  esac
  return 1
}

is_nonempty() {
  [ -n "$1" ]
}

# Asks a question until the validator accepts the answer, showing the error
# template after each invalid one. If the question isn't shown (noninteractive
# frontend, or already answered), the answer is kept and postinst warns.
ask() {
  question=$1
  validator=$2
  error=$3
  while :; do
    rc=0
    db_input high "$question" || rc=$?
    db_go
    db_get "$question"
    if "$validator" "$RET" || [ "$rc" -eq 30 ]; then
      return 0
    fi
    db_input critical "$error" || true
    db_fset "$question" seen false
  done
}

db_input high hawser/mode || true
db_go
db_get hawser/mode

case "$RET" in
  standard)
    db_input high hawser/bind_address || true
    db_input high hawser/token || true
    ask hawser/port is_port hawser/invalid_port
    db_get hawser/token
    if [ -z "$RET" ]; then
      token=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
      db_set hawser/token "$token"
    fi
    ;;
  edge)
    ask hawser/server_url is_ws_url hawser/invalid_server_url
    ask hawser/edge_token is_nonempty hawser/missing_edge_token
    ;;
  *)
    echo "Unknown Hawser mode: $RET" >&2
    exit 1
    ;;
esac

db_input medium hawser/agent_name || true
db_go

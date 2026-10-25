# Changelog

## Unreleased

- Expose every environment variable Hawser supports in `/etc/hawser.env`:
  `hawser_token_file`, `hawser_agent_id`, `hawser_bind_address`,
  `hawser_allow_insecure_no_auth`, `hawser_docker_socket`, `hawser_docker_host`,
  `hawser_stacks_dir`, `hawser_heartbeat_interval`, `hawser_request_timeout`,
  `hawser_compose_timeout`, `hawser_reconnect_delay`,
  `hawser_max_reconnect_delay`, `hawser_welcome_timeout`,
  `hawser_max_message_size_mb`
- `hawser_token` is no longer required when `hawser_token_file` is set
- `TLS_CERT`/`TLS_KEY` are now emitted as a pair (Hawser requires both)

## 1.0.0 (2026-05-17)

- Initial release
- Standard and Edge mode support
- Auto-detect architecture (amd64/arm64/arm)
- SHA256 checksum verification
- Systemd service with security hardening
- Idempotent install (skips if version matches)
- Uninstall task included

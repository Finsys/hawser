# Ansible Role: Hawser

Installs and configures [Hawser](https://github.com/Finsys/hawser) — the remote Docker agent for [Dockhand](https://dockhand.io).

## Requirements

- Target host must have Docker installed and running
- Target host must have `systemd`
- Ansible 2.14+

## Installation

```bash
# From Galaxy (when published)
ansible-galaxy install finsys.hawser

# From Git
ansible-galaxy install git+https://github.com/Finsys/hawser.git,,hawser
```

Or clone into your `roles/` directory.

## Role variables

All variables are optional except `hawser_token`.

| Variable | Default | Description |
|----------|---------|-------------|
| `hawser_version` | `latest` | Version to install (e.g. `v0.2.42`). `latest` resolves from GitHub. |
| `hawser_mode` | `standard` | `standard` (Hawser listens) or `edge` (Hawser connects outbound) |
| `hawser_token` | **required** | Authentication token |
| `hawser_port` | `2376` | Listen port (standard mode) |
| `hawser_agent_name` | `{{ inventory_hostname }}` | Agent display name in Dockhand |
| `hawser_dockhand_url` | — | Dockhand WebSocket URL (edge mode, **required**) |
| `hawser_log_level` | `info` | `debug`, `info`, `warn`, `error` |
| `hawser_tls_cert` | — | TLS certificate path (standard mode) |
| `hawser_tls_key` | — | TLS key path (standard mode) |
| `hawser_tls_skip_verify` | `false` | Skip TLS verification (edge mode) |
| `hawser_ca_cert` | — | CA cert path for self-signed Dockhand (edge mode) |
| `hawser_stacks_dir` | `/data/stacks` | Stack files directory; created if missing and the only writable path the unit allows |
| `hawser_install_dir` | `/usr/local/bin` | Binary install path |
| `hawser_user` | `root` | Service user |
| `hawser_group` | `docker` | Service group (needs Docker socket access) |

## Usage

### Standard mode

Hawser listens on a port. Configure the environment in Dockhand as "Hawser Standard" and point it to the host.

```yaml
# playbook.yml
- hosts: docker_hosts
  become: true
  roles:
    - role: hawser
      hawser_token: "my-secret-token"
```

```yaml
# inventory.yml
all:
  hosts:
    docker-1:
      ansible_host: 192.168.1.10
    docker-2:
      ansible_host: 192.168.1.11
      hawser_port: 2377
      hawser_token: "different-token"
```

```bash
ansible-playbook -i inventory.yml playbook.yml
```

### Edge mode

Hawser connects outbound to Dockhand via WebSocket. No inbound ports needed on the Docker host.

```yaml
- hosts: edge_hosts
  become: true
  roles:
    - role: hawser
      hawser_mode: edge
      hawser_dockhand_url: "wss://dockhand.example.com/api/hawser/connect"
      hawser_token: "edge-token"
```

### Standard mode with TLS

```yaml
- hosts: tls_hosts
  become: true
  roles:
    - role: hawser
      hawser_token: "my-secret"
      hawser_tls_cert: "/etc/hawser/cert.pem"
      hawser_tls_key: "/etc/hawser/key.pem"
```

### Mixed inventory

```yaml
# inventory.yml
all:
  children:
    standard_hosts:
      vars:
        hawser_mode: standard
        hawser_token: "lan-token"
      hosts:
        docker-lan-1:
          ansible_host: 192.168.1.10
        docker-lan-2:
          ansible_host: 192.168.1.11

    edge_hosts:
      vars:
        hawser_mode: edge
        hawser_dockhand_url: "wss://dockhand.example.com/api/hawser/connect"
      hosts:
        docker-vps:
          ansible_host: 203.0.113.10
          hawser_token: "vps-edge-token"
        docker-cloud:
          ansible_host: 198.51.100.5
          hawser_token: "cloud-edge-token"
```

## Uninstalling

```yaml
# uninstall.yml
- hosts: docker_hosts
  become: true
  tasks:
    - ansible.builtin.import_role:
        name: hawser
        tasks_from: uninstall
```

```bash
ansible-playbook -i inventory.yml uninstall.yml
```

## What the role does

1. **Validates** configuration (token required, edge needs URL, port in range, TLS cert/key paired)
2. **Downloads** the correct binary for the target architecture (amd64/arm64/arm) from GitHub releases
3. **Verifies** SHA256 checksum against the release checksums
4. **Installs** the binary to `/usr/local/bin/hawser`
5. **Creates** `/etc/hawser.env` (mode 0600) with the token and configuration
6. **Deploys** a systemd unit with security hardening (`NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`, `ReadOnlyPaths`)
7. **Enables and starts** the service
8. **Verifies** the service is active

On re-run, skips download if the correct version is already installed (idempotent).

## License

MIT

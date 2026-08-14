#!/usr/bin/env bash
set -u

usage() {
  printf 'Usage: sudo bash deploy/diagnose.sh server|agent\n'
}

[[ "${EUID}" -eq 0 ]] || {
  printf 'Run this diagnostic with sudo.\n' >&2
  exit 1
}

component="${1:-}"
case "${component}" in
  server)
    service_name="hyfleet-server"
    binary_path="/usr/local/bin/hyfleet-server"
    config_path="/etc/hyfleet/server.yaml"
    unit_path="/etc/systemd/system/hyfleet-server.service"
    ;;
  agent)
    service_name="hyfleet-agent"
    binary_path="/usr/local/bin/hyfleet-agent"
    config_path="/etc/hyfleet/agent.yaml"
    unit_path="/etc/systemd/system/hyfleet-agent.service"
    ;;
  *)
    usage
    exit 2
    ;;
esac

printf '=== Platform ===\n'
uname -a
printf 'Architecture: %s\n' "$(uname -m)"
timedatectl status 2>/dev/null || true

printf '\n=== Binary ===\n'
if [[ -e "${binary_path}" ]]; then
  if command -v file >/dev/null 2>&1; then
    file "${binary_path}"
  fi
  stat -c '%A %U:%G %s bytes %n' "${binary_path}" 2>/dev/null || true
  "${binary_path}" -version 2>&1 || true
else
  printf 'Missing: %s\n' "${binary_path}"
fi

printf '\n=== Paths ===\n'
namei -l "${config_path}" 2>&1 || true
[[ "${component}" == server ]] && namei -l /var/lib/hyfleet 2>&1 || true
[[ "${component}" == agent ]] && namei -l /var/lib/hyfleet-agent 2>&1 || true

if [[ "${component}" == agent ]]; then
  printf '\n=== Adapter configuration ===\n'
  adapter_type="$(awk '$1 == "adapter_type:" { print $2; exit }' "${config_path}" 2>/dev/null)"
  printf 'Adapter: %s\n' "${adapter_type:-unknown}"
  if [[ "${adapter_type}" == "s_ui" ]]; then
    if grep -Eq '^s_ui_api_url:[[:space:]]+http://(127\.0\.0\.1|\[::1\]):[0-9]{1,5}/.*apiv2/?$' "${config_path}" 2>/dev/null; then
      printf 'S-UI loopback API configured: yes\n'
    else
      printf 'S-UI loopback API configured: no\n'
    fi
    if grep -Eq '^HYFLEET_SUI_TOKEN=[^[:space:]]+$' /etc/hyfleet/agent.env 2>/dev/null; then
      printf 'S-UI API token configured: yes\n'
    else
      printf 'S-UI API token configured: no\n'
    fi
    stat -c 'Environment file: %A %U:%G %n' /etc/hyfleet/agent.env 2>/dev/null || true
  fi

  printf '\n=== Operations helper ===\n'
  if [[ -x /usr/local/libexec/hyfleet-agent-ops ]]; then
    stat -c '%A %U:%G %s bytes %n' /usr/local/libexec/hyfleet-agent-ops 2>/dev/null || true
    /usr/local/libexec/hyfleet-agent-ops -version 2>&1 || true
    /usr/local/libexec/hyfleet-agent-ops -config "${config_path}" -check-config 2>&1 || true
  else
    printf 'Missing: /usr/local/libexec/hyfleet-agent-ops\n'
  fi
  namei -l /var/lib/hyfleet-backups 2>&1 || true
  namei -l /var/lib/hyfleet-agent-ops 2>&1 || true
fi

printf '\n=== Unit verification ===\n'
if [[ "${component}" == agent ]]; then
  systemd-analyze verify "${unit_path}" \
    /etc/systemd/system/hyfleet-agent-ops.socket \
    /etc/systemd/system/hyfleet-agent-ops@.service 2>&1 || true
else
  systemd-analyze verify "${unit_path}" 2>&1 || true
fi

printf '\n=== Service status ===\n'
systemctl status "${service_name}" --no-pager --full 2>&1 || true
if [[ "${component}" == agent ]]; then
  systemctl status hyfleet-agent-ops.socket --no-pager --full 2>&1 || true
fi

printf '\n=== Current-boot journal (last 100 lines) ===\n'
journalctl -u "${service_name}" -b -n 100 --no-pager 2>&1 || true

if [[ "${component}" == server ]]; then
  printf '\n=== Loopback health ===\n'
  listen_address="$(awk '$1 == "listen:" { print $2; exit }' "${config_path}" 2>/dev/null)"
  if [[ "${listen_address}" =~ ^127\.0\.0\.1:[0-9]{1,5}$ ]]; then
    curl --fail --silent --show-error "http://${listen_address}/healthz" 2>&1 || true
  else
    printf 'Could not determine a loopback listen address from %s.\n' "${config_path}"
  fi
  printf '\n'
fi

printf '\nThis report does not print PolyFleet environment files or Agent state credentials.\n'

#!/usr/bin/env bash
set -Eeuo pipefail

config_path="/etc/hyfleet/agent.yaml"
stats_env_path="/etc/hyfleet/hy2-stats.env"
temporary_dir=""

fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [[ -n "${temporary_dir}" && -d "${temporary_dir}" ]]; then
    rm -rf -- "${temporary_dir}"
  fi
}
trap cleanup EXIT
trap 'printf "ERROR: Hysteria integration failed at line %s.\n" "$LINENO" >&2' ERR

[[ "${EUID}" -eq 0 ]] || fail "run this command with sudo"
for command_name in getent install openssl systemctl; do
  command -v "${command_name}" >/dev/null 2>&1 || fail "required command is missing: ${command_name}"
done
[[ -x /usr/local/bin/hyfleet-agent ]] || fail "PolyFleet Agent is not installed"
[[ -f "${config_path}" ]] || fail "missing ${config_path}"
getent group hyfleet-agent >/dev/null 2>&1 || fail "hyfleet-agent group does not exist"

temporary_dir="$(mktemp -d)"
if [[ ! -f "${stats_env_path}" ]]; then
  stats_secret="$(openssl rand -hex 32)"
  printf 'HYFLEET_HY2_STATS_SECRET=%s\n' "${stats_secret}" > "${temporary_dir}/hy2-stats.env"
  install -o root -g hyfleet-agent -m 0640 \
    "${temporary_dir}/hy2-stats.env" "${stats_env_path}"
  unset stats_secret
fi

/usr/local/bin/hyfleet-agent \
  -config "${config_path}" \
  -configure-hysteria-stats \
  -stats-env-file "${stats_env_path}"

systemctl restart hyfleet-agent
for _ in {1..20}; do
  if systemctl is-active --quiet hyfleet-agent; then
    printf 'Hysteria HTTP authentication and traffic statistics are active.\n'
    exit 0
  fi
  sleep 1
done
systemctl status hyfleet-agent --no-pager --full || true
fail "PolyFleet Agent did not remain active after enabling traffic statistics"

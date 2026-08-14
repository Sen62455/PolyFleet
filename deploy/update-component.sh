#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
bundle_dir="$(cd -- "${script_dir}/.." && pwd)"
component="${1:-}"
backup_root="/var/lib/hyfleet-updates"
backup_dir=""
committed=false
data_backed_up=false
had_ops_binary=false
had_ops_socket=false
had_ops_service=false
ops_socket_was_active=false
is_reality=false
had_reality_binary=false
had_reality_unit=false
reality_was_active=false
reality_api_secret=""
temporary_environment=""
reality_curl_config=""

reality_service_unit="hyfleet-sing-box-reality.service"
reality_target_unit="/etc/systemd/system/${reality_service_unit}"
reality_binary="/usr/bin/sing-box"
reality_core_config="/etc/sing-box/hyfleet-reality.json"
reality_identity="/var/lib/hyfleet-agent-ops/reality-hyfleet-sing-box-reality.json"
reality_applied="/var/lib/hyfleet-agent-ops/reality-hyfleet-sing-box-reality-applied.json"
reality_api_url="http://127.0.0.1:18083"
reality_sing_box_version="1.13.18-hyfleet-utls1.8.7-api2"
reality_sing_box_legacy_version="1.13.18-hyfleet-utls1.8.7"
reality_sing_box_api1_version="1.13.18-hyfleet-utls1.8.7-api1"

fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

restore_optional_agent_file() {
  local snapshot_name="$1"
  local target_path="$2"
  local owner="$3"
  local mode="$4"
  if [[ -f "${backup_dir}/${snapshot_name}" ]]; then
    install -o "${owner}" -g hyfleet-agent -m "${mode}" \
      "${backup_dir}/${snapshot_name}" "${target_path}"
  else
    rm -f -- "${target_path}"
  fi
}

restore_optional_reality_file() {
  local snapshot_name="$1"
  local target_path="$2"
  local owner="$3"
  local group="$4"
  local mode="$5"
  if [[ -f "${backup_dir}/${snapshot_name}" ]]; then
    install -o "${owner}" -g "${group}" -m "${mode}" \
      "${backup_dir}/${snapshot_name}" "${target_path}"
  else
    rm -f -- "${target_path}"
  fi
}

secure_regular_file() {
  local path="$1"
  local owner_group="$2"
  local mode="$3"
  [[ -f "${path}" && ! -L "${path}" ]] || fail "${path} must be a regular file, not a symbolic link"
  [[ "$(stat -c '%u:%g %a' "${path}")" == "${owner_group} ${mode}" ]] ||
    fail "${path} has invalid ownership or permissions"
}

case "${component}" in
  server)
    binary_name="hyfleet-server"
    service_name="hyfleet-server.service"
    service_user="hyfleet"
    service_group="hyfleet"
    config_path="/etc/hyfleet/server.yaml"
    ;;
  agent)
    binary_name="hyfleet-agent"
    service_name="hyfleet-agent.service"
    service_user="hyfleet-agent"
    service_group="hyfleet-agent"
    config_path="/etc/hyfleet/agent.yaml"
    ;;
  *)
    fail "usage: sudo bash deploy/update-component.sh server|agent"
    ;;
esac

source_binary="${bundle_dir}/bin/${binary_name}"
source_unit="${script_dir}/systemd/${service_name}"
target_binary="/usr/local/bin/${binary_name}"
target_unit="/etc/systemd/system/${service_name}"

if [[ "${component}" == "agent" ]]; then
  ops_source_binary="${bundle_dir}/bin/hyfleet-agent-ops"
  ops_source_socket="${script_dir}/systemd/hyfleet-agent-ops.socket"
  ops_source_service="${script_dir}/systemd/hyfleet-agent-ops@.service"
  ops_target_binary="/usr/local/libexec/hyfleet-agent-ops"
  ops_target_socket="/etc/systemd/system/hyfleet-agent-ops.socket"
  ops_target_service="/etc/systemd/system/hyfleet-agent-ops@.service"
fi

rollback() {
  local status="$1"
  local rollback_listen=""
  local rollback_ready=false
  if [[ "${committed}" != true && -n "${backup_dir}" && -d "${backup_dir}" ]]; then
    set +e
    [[ -z "${temporary_environment}" ]] || rm -f -- "${temporary_environment}"
    [[ -z "${reality_curl_config}" ]] || rm -f -- "${reality_curl_config}"
    printf 'Update failed; restoring %s.\n' "${component}" >&2
    systemctl stop "${service_name}"
    if [[ "${component}" == "agent" && "${is_reality}" == true ]]; then
      systemctl stop "${reality_service_unit}"
    fi
    install -o root -g root -m 0755 "${backup_dir}/${binary_name}" "${target_binary}"
    install -o root -g root -m 0644 "${backup_dir}/${service_name}" "${target_unit}"
    if [[ "${component}" == "agent" ]]; then
      if [[ "${had_ops_binary}" == true ]]; then
        install -o root -g root -m 0755 "${backup_dir}/hyfleet-agent-ops" "${ops_target_binary}"
      else
        rm -f -- "${ops_target_binary}"
      fi
      if [[ "${had_ops_socket}" == true ]]; then
        install -o root -g root -m 0644 "${backup_dir}/hyfleet-agent-ops.socket" "${ops_target_socket}"
      else
        systemctl disable --now hyfleet-agent-ops.socket
        rm -f -- "${ops_target_socket}"
      fi
      if [[ "${had_ops_service}" == true ]]; then
        install -o root -g root -m 0644 "${backup_dir}/hyfleet-agent-ops@.service" "${ops_target_service}"
      else
        rm -f -- "${ops_target_service}"
      fi
      if [[ "${is_reality}" == true ]]; then
        if [[ "${had_reality_binary}" == true ]]; then
          install -o root -g root -m 0755 "${backup_dir}/sing-box" "${reality_binary}"
        else
          rm -f -- "${reality_binary}"
        fi
        if [[ "${had_reality_unit}" == true ]]; then
          install -o root -g root -m 0644 \
            "${backup_dir}/${reality_service_unit}" "${reality_target_unit}"
        else
          systemctl disable --now "${reality_service_unit}"
          rm -f -- "${reality_target_unit}"
        fi
      fi
    fi
    if [[ "${data_backed_up}" == true && "${component}" == "server" ]]; then
      install -o hyfleet -g hyfleet -m 0600 "${backup_dir}/server.db" /var/lib/hyfleet/server.db
      install -o hyfleet -g hyfleet -m 0600 "${backup_dir}/master.key" /var/lib/hyfleet/master.key
      install -o root -g hyfleet -m 0640 "${backup_dir}/server.yaml" /etc/hyfleet/server.yaml
      if [[ -f "${backup_dir}/server.db-wal" ]]; then
        install -o hyfleet -g hyfleet -m 0600 \
          "${backup_dir}/server.db-wal" /var/lib/hyfleet/server.db-wal
      else
        rm -f -- /var/lib/hyfleet/server.db-wal
      fi
      rm -f -- /var/lib/hyfleet/server.db-shm
    elif [[ "${data_backed_up}" == true ]]; then
      install -o root -g hyfleet-agent -m 0640 "${backup_dir}/agent.yaml" /etc/hyfleet/agent.yaml
      restore_optional_agent_file agent.env /etc/hyfleet/agent.env root 0640
      restore_optional_agent_file hy2-stats.env /etc/hyfleet/hy2-stats.env root 0640
      restore_optional_agent_file \
        agent-state.json /var/lib/hyfleet-agent/agent-state.json hyfleet-agent 0600
      restore_optional_agent_file agent.db /var/lib/hyfleet-agent/agent.db hyfleet-agent 0600
      restore_optional_agent_file agent.db-wal /var/lib/hyfleet-agent/agent.db-wal hyfleet-agent 0600
      restore_optional_agent_file auth-cache.json /var/lib/hyfleet-agent/auth-cache.json hyfleet-agent 0600
      rm -f -- /var/lib/hyfleet-agent/agent.db-shm
      if [[ "${is_reality}" == true ]]; then
        restore_optional_reality_file reality-config.json "${reality_core_config}" root hyfleet-singbox 0640
        restore_optional_reality_file reality-identity.json "${reality_identity}" root root 0600
        restore_optional_reality_file reality-applied.json "${reality_applied}" root root 0600
      fi
    fi
    systemctl daemon-reload
    if [[ "${component}" == "agent" && "${ops_socket_was_active}" == true ]]; then
      systemctl restart hyfleet-agent-ops.socket
    fi
    if [[ "${component}" == "agent" && "${is_reality}" == true &&
      "${reality_was_active}" == true ]]; then
      systemctl restart "${reality_service_unit}"
    fi
    systemctl restart "${service_name}"
    if [[ "${component}" == "server" ]]; then
      rollback_listen="$(awk '$1 == "listen:" { print $2; exit }' /etc/hyfleet/server.yaml)"
    fi
    for _ in {1..20}; do
      if systemctl is-active --quiet "${service_name}"; then
        if [[ "${component}" != "server" ]]; then
          rollback_ready=true
          break
        fi
        if [[ "${rollback_listen}" =~ ^127\.0\.0\.1:[0-9]{1,5}$ ]] &&
          curl --fail --silent --show-error "http://${rollback_listen}/healthz" >/dev/null 2>&1; then
          rollback_ready=true
          break
        fi
      fi
      sleep 1
    done
    if [[ "${component}" == "agent" && "${rollback_ready}" == true ]]; then
      sleep 3
      systemctl is-active --quiet "${service_name}" || rollback_ready=false
      if [[ "${is_reality}" == true && "${reality_was_active}" == true ]]; then
        systemctl is-active --quiet "${reality_service_unit}" || rollback_ready=false
      fi
    fi
    if [[ "${rollback_ready}" == true ]]; then
      printf '%s rollback completed and passed its health check.\n' "${component}" >&2
    else
      printf 'ERROR: %s rollback did not pass its health check.\n' "${component}" >&2
      systemctl status "${service_name}" --no-pager --full >&2 || true
      status=1
    fi
  fi
  exit "${status}"
}
trap 'rollback $?' EXIT

[[ "${EUID}" -eq 0 ]] || fail "run this updater with sudo"
for command_name in \
  awk basename curl find getent grep id install mktemp od runuser sed sha256sum sleep stat \
  systemctl systemd-analyze tr uname; do
  command -v "${command_name}" >/dev/null 2>&1 || fail "required command is missing: ${command_name}"
done
[[ -f "${source_binary}" && ! -L "${source_binary}" &&
  -f "${source_unit}" && ! -L "${source_unit}" ]] || fail "release bundle is incomplete"
[[ -x "${target_binary}" && ! -L "${target_binary}" &&
  -f "${target_unit}" && ! -L "${target_unit}" &&
  -f "${config_path}" && ! -L "${config_path}" ]] ||
  fail "${component} is not installed or uses unsafe managed paths; use the initial installer first"
if [[ "${component}" == "agent" ]]; then
  [[ -f "${ops_source_binary}" && ! -L "${ops_source_binary}" &&
    -f "${ops_source_socket}" && ! -L "${ops_source_socket}" &&
    -f "${ops_source_service}" && ! -L "${ops_source_service}" ]] ||
    fail "release bundle has no operations helper"
  configured_adapter="$(awk '$1 == "adapter_type:" { print $2; exit }' "${config_path}")"
  [[ "${configured_adapter}" =~ ^(native_hysteria2|standalone_sing_box|s_ui|sing_box_vless_reality)$ ]] ||
    fail "Agent adapter_type is invalid"
  if [[ "${configured_adapter}" == "sing_box_vless_reality" ]]; then
    is_reality=true
    reality_source_binary="${bundle_dir}/bin/sing-box-reality"
    reality_source_unit="${script_dir}/systemd/${reality_service_unit}"
    reality_checksums="${script_dir}/sing-box-reality.sha256"
    [[ -f "${reality_source_binary}" && ! -L "${reality_source_binary}" &&
      -f "${reality_source_unit}" && ! -L "${reality_source_unit}" &&
      -f "${reality_checksums}" && ! -L "${reality_checksums}" ]] ||
      fail "release bundle has no complete Reality data plane"
  fi
fi

elf_magic="$(od -An -t x1 -N 4 "${source_binary}" | tr -d '[:space:]')"
[[ "${elf_magic}" == "7f454c46" ]] || fail "${binary_name} is not a Linux ELF binary"
elf_machine="$(od -An -t x1 -j 18 -N 2 "${source_binary}" | tr -d '[:space:]')"
case "$(uname -m)" in
  x86_64) expected_machine="3e00"; architecture="amd64" ;;
  aarch64|arm64) expected_machine="b700"; architecture="arm64" ;;
  *) fail "unsupported host architecture: $(uname -m)" ;;
esac
[[ "${elf_machine}" == "${expected_machine}" ]] || fail "binary architecture does not match this host"
if [[ "${component}" == "agent" ]]; then
  ops_elf_magic="$(od -An -t x1 -N 4 "${ops_source_binary}" | tr -d '[:space:]')"
  [[ "${ops_elf_magic}" == "7f454c46" ]] || fail "hyfleet-agent-ops is not a Linux ELF binary"
  ops_elf_machine="$(od -An -t x1 -j 18 -N 2 "${ops_source_binary}" | tr -d '[:space:]')"
  [[ "${ops_elf_machine}" == "${expected_machine}" ]] ||
    fail "hyfleet-agent-ops architecture does not match this host"
  if [[ "${is_reality}" == true ]]; then
    reality_artifact="sing-box-${reality_sing_box_version}-linux-${architecture}"
    case "${architecture}" in
      amd64)
        legacy_reality_hash="759f7a7acfdd32517851ec3b68fb19bc211a41c5d40b2610b7693b2a41b55f33"
        api1_reality_hash="a99679a7ebc4e4f4b21af5aa5db23eb3149c3abfdbde516d97718ce3920586d7"
        ;;
      arm64)
        legacy_reality_hash="2483f6f8c8f2ad91db7278ed09b4c0f505074f39ab9a3d4843b87cf93261498f"
        api1_reality_hash="52f3c8a71317b51996c3d0f3a42f3ffdea747352a8655d848415bad3c8253f0c"
        ;;
    esac
    expected_reality_hash="$(awk -v artifact="${reality_artifact}" '
      length($1) == 64 && $1 ~ /^[0-9a-f]+$/ && $2 == artifact && NF == 2 {
        hash=$1; matches++
      }
      END { if (matches == 1) print hash }
    ' "${reality_checksums}")"
    [[ -n "${expected_reality_hash}" ]] ||
      fail "Reality checksum manifest has no unique entry for ${reality_artifact}"
    [[ -x "${reality_source_binary}" &&
      "$(od -An -t x1 -N 4 "${reality_source_binary}" | tr -d '[:space:]')" == "7f454c46" &&
      "$(od -An -t x1 -j 18 -N 2 "${reality_source_binary}" | tr -d '[:space:]')" == "${expected_machine}" ]] ||
      fail "bundled Reality binary is not the expected Linux ELF architecture"
    [[ -z "$(find "${reality_source_binary}" -maxdepth 0 -perm /022 -print -quit)" ]] ||
      fail "bundled Reality binary must not be writable by group or other"
    [[ "$(sha256sum "${reality_source_binary}" | awk '{print $1}')" == "${expected_reality_hash}" ]] ||
      fail "bundled Reality binary checksum does not match the pinned build"
    reality_version_line="$("${reality_source_binary}" version | sed -n '1p')"
    [[ "${reality_version_line}" == "sing-box version ${reality_sing_box_version}" ]] ||
      fail "bundled Reality binary reports an unexpected version"
    [[ "$(grep -c '^service_unit:' "${config_path}")" -eq 1 &&
      "$(awk '$1 == "service_unit:" { print $2; exit }' "${config_path}")" == "${reality_service_unit}" &&
      "$(grep -c '^core_config_path:' "${config_path}")" -eq 1 &&
      "$(awk '$1 == "core_config_path:" { print $2; exit }' "${config_path}")" == "${reality_core_config}" &&
      "$(grep -c '^sing_box_binary_path:' "${config_path}")" -eq 1 &&
      "$(awk '$1 == "sing_box_binary_path:" { print $2; exit }' "${config_path}")" == "${reality_binary}" &&
      "$(grep -c '^reality_identity_path:' "${config_path}")" -eq 1 &&
      "$(awk '$1 == "reality_identity_path:" { print $2; exit }' "${config_path}")" == "${reality_identity}" ]] ||
      fail "Reality Agent configuration uses unsupported managed paths"
    if grep -q '^reality_api_url:' "${config_path}"; then
      [[ "$(grep -c '^reality_api_url:' "${config_path}")" -eq 1 &&
        "$(awk '$1 == "reality_api_url:" { print $2; exit }' "${config_path}")" == "${reality_api_url}" ]] ||
        fail "Reality API URL is duplicated or outside the fixed loopback origin"
    fi
    if grep -q '^reality_api_secret_env:' "${config_path}"; then
      [[ "$(grep -c '^reality_api_secret_env:' "${config_path}")" -eq 1 &&
        "$(awk '$1 == "reality_api_secret_env:" { print $2; exit }' "${config_path}")" == "HYFLEET_REALITY_API_SECRET" ]] ||
        fail "Reality API secret environment name is duplicated or unsupported"
    fi

    [[ -d /etc/sing-box && ! -L /etc/sing-box ]] ||
      fail "/etc/sing-box must be a directory, not a symbolic link"
    reality_group_id="$(getent group hyfleet-singbox | awk -F: '{ print $3; exit }')"
    reality_user_id="$(id -u hyfleet-singbox 2>/dev/null || true)"
    [[ -n "${reality_group_id}" && -n "${reality_user_id}" ]] ||
      fail "the managed hyfleet-singbox service identity is missing"
    [[ "$(stat -c '%u:%g %a' /etc/sing-box)" == "0:${reality_group_id} 750" ]] ||
      fail "/etc/sing-box has invalid ownership or permissions"
    [[ -d /var/lib/hyfleet-singbox && ! -L /var/lib/hyfleet-singbox &&
      "$(stat -c '%u:%g %a' /var/lib/hyfleet-singbox)" == "${reality_user_id}:${reality_group_id} 750" ]] ||
      fail "/var/lib/hyfleet-singbox has invalid ownership or permissions"
    [[ -d /var/lib/hyfleet-agent-ops && ! -L /var/lib/hyfleet-agent-ops &&
      "$(stat -c '%u:%g %a' /var/lib/hyfleet-agent-ops)" == "0:0 700" ]] ||
      fail "/var/lib/hyfleet-agent-ops has invalid ownership or permissions"
    [[ -d /var/lib/hyfleet-backups && ! -L /var/lib/hyfleet-backups &&
      "$(stat -c '%u:%g %a' /var/lib/hyfleet-backups)" == "0:0 700" ]] ||
      fail "/var/lib/hyfleet-backups has invalid ownership or permissions"
    runuser -u hyfleet-singbox -- test -x /etc/sing-box ||
      fail "/etc/sing-box is not traversable by hyfleet-singbox"
    secure_regular_file "${reality_binary}" "0:0" 755
    installed_reality_hash="$(sha256sum "${reality_binary}" | awk '{print $1}')"
    case "${installed_reality_hash}" in
      "${expected_reality_hash}") expected_installed_reality_version="${reality_sing_box_version}" ;;
      "${legacy_reality_hash}") expected_installed_reality_version="${reality_sing_box_legacy_version}" ;;
      "${api1_reality_hash}") expected_installed_reality_version="${reality_sing_box_api1_version}" ;;
      *) fail "installed Reality binary checksum is not an approved upgrade input" ;;
    esac
    reality_installed_version_line="$("${reality_binary}" version | sed -n '1p')"
    [[ "${reality_installed_version_line}" == "sing-box version ${expected_installed_reality_version}" ]] ||
      fail "installed Reality binary version does not match its approved checksum"
    secure_regular_file "${reality_target_unit}" "0:0" 644
    secure_regular_file "${reality_core_config}" "0:${reality_group_id}" 640
    secure_regular_file "${reality_identity}" "0:0" 600
    secure_regular_file "${reality_applied}" "0:0" 600

    environment_path="/etc/hyfleet/agent.env"
    agent_group_id="$(getent group hyfleet-agent | awk -F: '{ print $3; exit }')"
    [[ -n "${agent_group_id}" ]] || fail "the managed hyfleet-agent group is missing"
    if [[ -e "${environment_path}" || -L "${environment_path}" ]]; then
      secure_regular_file "${environment_path}" "0:${agent_group_id}" 640
      reality_secret_count="$(awk -F= '$1 == "HYFLEET_REALITY_API_SECRET" { count++ } END { print count+0 }' "${environment_path}")"
      [[ "${reality_secret_count}" -le 1 ]] ||
        fail "${environment_path} contains duplicate Reality API secret entries"
      reality_api_secret="$(awk -F= '$1 == "HYFLEET_REALITY_API_SECRET" { sub(/^[^=]*=/, ""); print; exit }' "${environment_path}")"
    fi
    if [[ -z "${reality_api_secret}" ]]; then
      reality_api_secret="$(od -An -N32 -tx1 /dev/urandom | tr -d '[:space:]')"
    fi
    [[ "${reality_api_secret}" =~ ^[A-Za-z0-9_-]{43,128}$ ]] ||
      fail "invalid Reality API secret in ${environment_path}"
    temporary_environment="$(mktemp /etc/hyfleet/.agent.env.update.XXXXXXXX)"
    chmod 0600 "${temporary_environment}"
    if [[ -f "${environment_path}" ]]; then
      awk -F= '$1 != "HYFLEET_REALITY_API_SECRET" { print }' "${environment_path}" > "${temporary_environment}"
    fi
    printf 'HYFLEET_REALITY_API_SECRET=%s\n' "${reality_api_secret}" >> "${temporary_environment}"
  fi
fi

install -d -o root -g root -m 0700 "${backup_root}"
backup_dir="$(mktemp -d "${backup_root}/${component}.XXXXXXXX")"
[[ "${backup_dir}" == "${backup_root}/"* && "${backup_dir}" != "${backup_root}" ]] ||
  fail "invalid update backup directory"
install -o root -g root -m 0755 "${target_binary}" "${backup_dir}/${binary_name}"
install -o root -g root -m 0644 "${target_unit}" "${backup_dir}/${service_name}"
if [[ "${component}" == "agent" ]]; then
  if [[ -x "${ops_target_binary}" && ! -L "${ops_target_binary}" ]]; then
    had_ops_binary=true
    install -o root -g root -m 0755 "${ops_target_binary}" "${backup_dir}/hyfleet-agent-ops"
  fi
  if [[ -f "${ops_target_socket}" && ! -L "${ops_target_socket}" ]]; then
    had_ops_socket=true
    install -o root -g root -m 0644 "${ops_target_socket}" "${backup_dir}/hyfleet-agent-ops.socket"
  fi
  if [[ -f "${ops_target_service}" && ! -L "${ops_target_service}" ]]; then
    had_ops_service=true
    install -o root -g root -m 0644 "${ops_target_service}" "${backup_dir}/hyfleet-agent-ops@.service"
  fi
  if systemctl is-active --quiet hyfleet-agent-ops.socket; then
    ops_socket_was_active=true
  fi
  if [[ "${is_reality}" == true ]]; then
    reality_was_active=false
    if systemctl is-active --quiet "${reality_service_unit}"; then
      reality_was_active=true
    fi
    had_reality_binary=true
    had_reality_unit=true
    install -o root -g root -m 0755 "${reality_binary}" "${backup_dir}/sing-box"
    install -o root -g root -m 0644 "${reality_target_unit}" "${backup_dir}/${reality_service_unit}"
    install -o root -g root -m 0600 "${reality_core_config}" "${backup_dir}/reality-config.json"
    install -o root -g root -m 0600 "${reality_identity}" "${backup_dir}/reality-identity.json"
    install -o root -g root -m 0600 "${reality_applied}" "${backup_dir}/reality-applied.json"
  fi
fi

if [[ "${component}" == "agent" && "${is_reality}" == true ]]; then
  systemctl stop "${service_name}"
  systemctl stop "${reality_service_unit}"
else
  systemctl stop "${service_name}"
fi
if [[ "${component}" == "server" ]]; then
  [[ -f /var/lib/hyfleet/server.db && ! -L /var/lib/hyfleet/server.db &&
    -f /var/lib/hyfleet/master.key && ! -L /var/lib/hyfleet/master.key ]] ||
    fail "server database or master key is missing or unsafe"
  install -o root -g root -m 0600 /var/lib/hyfleet/server.db "${backup_dir}/server.db"
  if [[ -f /var/lib/hyfleet/server.db-wal && ! -L /var/lib/hyfleet/server.db-wal ]]; then
    install -o root -g root -m 0600 /var/lib/hyfleet/server.db-wal "${backup_dir}/server.db-wal"
  fi
  install -o root -g root -m 0600 /var/lib/hyfleet/master.key "${backup_dir}/master.key"
  install -o root -g root -m 0600 /etc/hyfleet/server.yaml "${backup_dir}/server.yaml"
else
  install -o root -g root -m 0600 /etc/hyfleet/agent.yaml "${backup_dir}/agent.yaml"
  for optional_path in \
    /etc/hyfleet/agent.env \
    /etc/hyfleet/hy2-stats.env \
    /var/lib/hyfleet-agent/agent-state.json \
    /var/lib/hyfleet-agent/agent.db \
    /var/lib/hyfleet-agent/agent.db-wal \
    /var/lib/hyfleet-agent/auth-cache.json; do
    if [[ -f "${optional_path}" && ! -L "${optional_path}" ]]; then
      install -o root -g root -m 0600 "${optional_path}" "${backup_dir}/$(basename -- "${optional_path}")"
    fi
  done
fi
data_backed_up=true

install -o root -g root -m 0755 "${source_binary}" "${target_binary}"
install -o root -g root -m 0644 "${source_unit}" "${target_unit}"
if [[ "${component}" == "agent" ]]; then
  install -d -o root -g root -m 0755 /usr/local/libexec
  install -d -o root -g root -m 0700 /var/lib/hyfleet-backups /var/lib/hyfleet-agent-ops
  install -o root -g root -m 0755 "${ops_source_binary}" "${ops_target_binary}"
  install -o root -g root -m 0644 "${ops_source_socket}" "${ops_target_socket}"
  install -o root -g root -m 0644 "${ops_source_service}" "${ops_target_service}"
  if [[ "${is_reality}" == true ]]; then
    install -o root -g root -m 0755 "${reality_source_binary}" "${reality_binary}"
    install -o root -g root -m 0644 "${reality_source_unit}" "${reality_target_unit}"
    install -o root -g hyfleet-agent -m 0640 "${temporary_environment}" /etc/hyfleet/agent.env
    rm -f -- "${temporary_environment}"
    temporary_environment=""
  fi
fi
"${target_binary}" -version
runuser -u "${service_user}" -g "${service_group}" -- \
  "${target_binary}" -config "${config_path}" -check-config
if [[ "${component}" == "agent" ]]; then
  "${ops_target_binary}" -version
  "${ops_target_binary}" -config "${config_path}" -check-config
  configured_adapter="$(awk '$1 == "adapter_type:" { print $2; exit }' "${config_path}")"
  if [[ "${configured_adapter}" == "s_ui" ]]; then
    grep -Eq '^HYFLEET_SUI_TOKEN=[^[:space:]]+$' /etc/hyfleet/agent.env ||
      fail "S-UI Agent requires HYFLEET_SUI_TOKEN in /etc/hyfleet/agent.env"
  elif [[ "${is_reality}" == true ]]; then
    secure_regular_file "${reality_binary}" "0:0" 755
    [[ "$(sha256sum "${reality_binary}" | awk '{print $1}')" == "${expected_reality_hash}" ]] ||
      fail "installed Reality binary checksum changed during update"
    [[ "$("${reality_binary}" version | sed -n '1p')" == "sing-box version ${reality_sing_box_version}" ]] ||
      fail "installed Reality binary version changed during update"
    grep -Eq '^HYFLEET_REALITY_API_SECRET=[A-Za-z0-9_-]{43,128}$' /etc/hyfleet/agent.env ||
      fail "Reality Agent requires a restricted API secret"
  fi
fi

systemctl daemon-reload
if [[ "${component}" == "agent" ]]; then
  units_to_verify=("${target_unit}" "${ops_target_socket}" "${ops_target_service}")
  if [[ "${is_reality}" == true ]]; then
    units_to_verify+=("${reality_target_unit}")
  fi
  systemd-analyze verify "${units_to_verify[@]}"
  systemctl enable --now hyfleet-agent-ops.socket
else
  systemd-analyze verify "${target_unit}"
fi

if [[ "${component}" == "agent" && "${is_reality}" == true ]]; then
  systemctl enable "${reality_service_unit}"
  systemctl restart "${reality_service_unit}"
  reality_active=false
  for _ in {1..20}; do
    if systemctl is-active --quiet "${reality_service_unit}"; then
      reality_active=true
      break
    fi
    sleep 1
  done
  [[ "${reality_active}" == true ]] || fail "${reality_service_unit} did not become active"
else
  systemctl restart "${service_name}"
fi
if [[ "${component}" == "agent" && "${is_reality}" == true ]]; then
  systemctl restart "${service_name}"
fi

active=false
for _ in {1..20}; do
  if systemctl is-active --quiet "${service_name}"; then
    active=true
    break
  fi
  sleep 1
done
[[ "${active}" == true ]] || fail "${service_name} did not become active"

if [[ "${component}" == "server" ]]; then
  listen_address="$(awk '$1 == "listen:" { print $2; exit }' "${config_path}")"
  [[ "${listen_address}" =~ ^127\.0\.0\.1:[0-9]{1,5}$ ]] || fail "invalid server listen address"
  healthy=false
  for _ in {1..20}; do
    if curl --fail --silent --show-error "http://${listen_address}/healthz" >/dev/null 2>&1; then
      healthy=true
      break
    fi
    sleep 1
  done
  [[ "${healthy}" == true ]] || fail "PolyFleet Server health check failed"
elif [[ "${is_reality}" == true ]]; then
  reality_listen_port="$(sed -nE 's/^[[:space:]]*"listen_port"[[:space:]]*:[[:space:]]*([0-9]+),?[[:space:]]*$/\1/p' "${reality_core_config}")"
  [[ "$(printf '%s\n' "${reality_listen_port}" | grep -c '^[0-9][0-9]*$')" -eq 1 ]] ||
    fail "managed Reality configuration has no unique listener port"
  [[ "${reality_listen_port}" =~ ^[0-9]+$ && "${reality_listen_port}" -ge 1 &&
    "${reality_listen_port}" -le 65535 ]] || fail "managed Reality listener port is invalid"
  reality_curl_config="$(mktemp)"
  chmod 0600 "${reality_curl_config}"
  printf 'header = "Authorization: Bearer %s"\n' "${reality_api_secret}" > "${reality_curl_config}"
  api_healthy=false
  listener_healthy=false
  for _ in {1..30}; do
    if curl --fail --silent --show-error --config "${reality_curl_config}" \
      "${reality_api_url}/hyfleet/v1/users" >/dev/null 2>&1; then
      api_healthy=true
    fi
    if (echo > "/dev/tcp/127.0.0.1/${reality_listen_port}") >/dev/null 2>&1; then
      listener_healthy=true
    fi
    if [[ "${api_healthy}" == true && "${listener_healthy}" == true ]]; then
      break
    fi
    sleep 1
  done
  rm -f -- "${reality_curl_config}"
  reality_curl_config=""
  unset reality_api_secret
  [[ "${api_healthy}" == true ]] || fail "Reality user control API health check failed"
  [[ "${listener_healthy}" == true ]] || fail "Reality TCP listener health check failed"
  systemctl is-active --quiet "${reality_service_unit}" || fail "Reality service did not remain active"
  systemctl is-active --quiet "${service_name}" || fail "PolyFleet Agent did not remain active"
else
  sleep 3
  systemctl is-active --quiet "${service_name}" || fail "PolyFleet Agent did not remain active"
fi

committed=true
printf '%s update completed. Rollback snapshot: %s\n' "${component}" "${backup_dir}"

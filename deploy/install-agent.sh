#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
bundle_dir="$(cd -- "${script_dir}/.." && pwd)"
source_binary="${bundle_dir}/bin/hyfleet-agent"
source_ops_binary="${bundle_dir}/bin/hyfleet-agent-ops"
source_reality_binary="${bundle_dir}/bin/sing-box-reality"
source_unit="${script_dir}/systemd/hyfleet-agent.service"
source_ops_socket="${script_dir}/systemd/hyfleet-agent-ops.socket"
source_ops_service="${script_dir}/systemd/hyfleet-agent-ops@.service"
source_reality_unit="${script_dir}/systemd/hyfleet-sing-box-reality.service"
source_reality_checksums="${script_dir}/sing-box-reality.sha256"

reality_service_unit="hyfleet-sing-box-reality.service"
reality_core_config="/etc/sing-box/hyfleet-reality.json"
reality_binary="/usr/bin/sing-box"
reality_identity="/var/lib/hyfleet-agent-ops/reality-hyfleet-sing-box-reality.json"
reality_applied="/var/lib/hyfleet-agent-ops/reality-hyfleet-sing-box-reality-applied.json"
reality_sing_box_version="1.13.18-hyfleet-utls1.8.7-api2"

server_url=""
node_name=""
adapter_type=""
service_unit=""
core_name=""
core_config_path=""
s_ui_api_url=""
replace_config=false
temporary_dir=""

usage() {
  cat <<'EOF'
Usage:
  sudo bash deploy/install-agent.sh \
    --server-url https://panel.example.com \
    --node-name edge-node-a \
    --adapter native-hysteria2

Adapters:
  native-hysteria2    Hysteria2 systemd service
  standalone-sing-box
  s-ui
  vless-reality       Managed VLESS/TCP/Reality using the pinned compatibility build

Options:
  --server-url URL    PolyFleet HTTPS origin (required initially)
  --node-name NAME    Local node label using letters, numbers, dot, dash, underscore
  --adapter TYPE      One adapter from the list above
  --service-unit UNIT Override the adapter's default systemd unit
  --core-config-path PATH
                       Config file or directory inside the adapter's /etc directory
  --s-ui-api-url URL  Local S-UI HTTP API ending in /apiv2 (S-UI only)
  --replace-config    Replace /etc/hyfleet/agent.yaml with generated settings
  -h, --help          Show this help

The vless-reality adapter uses fixed service, config, binary, and identity
paths. --service-unit and --core-config-path may only repeat those fixed values.
The installer does not fetch sing-box separately. The release contains the pinned
1.13.18-hyfleet-utls1.8.7-api2 build and its checksum manifest.
EOF
}

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
trap 'printf "ERROR: installation failed at line %s.\n" "$LINENO" >&2' ERR

while (($# > 0)); do
  case "$1" in
    --server-url)
      (($# >= 2)) || fail "--server-url requires a value"
      server_url="$2"
      shift 2
      ;;
    --node-name)
      (($# >= 2)) || fail "--node-name requires a value"
      node_name="$2"
      shift 2
      ;;
    --adapter)
      (($# >= 2)) || fail "--adapter requires a value"
      adapter_type="$2"
      shift 2
      ;;
    --service-unit)
      (($# >= 2)) || fail "--service-unit requires a value"
      service_unit="$2"
      shift 2
      ;;
    --core-config-path)
      (($# >= 2)) || fail "--core-config-path requires a value"
      core_config_path="$2"
      shift 2
      ;;
    --s-ui-api-url)
      (($# >= 2)) || fail "--s-ui-api-url requires a value"
      s_ui_api_url="$2"
      shift 2
      ;;
    --replace-config)
      replace_config=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

[[ "${EUID}" -eq 0 ]] || fail "run this installer with sudo"

for command_name in \
  awk chmod chown curl find getent grep groupadd install journalctl mkdir mktemp od runuser sed sha256sum stat \
  systemctl systemd-analyze tr uname useradd; do
  command -v "${command_name}" >/dev/null 2>&1 || fail "required command is missing: ${command_name}"
done

[[ -f "${source_binary}" ]] || fail "missing ${source_binary}; extract the complete release archive"
[[ -f "${source_ops_binary}" ]] || fail "missing ${source_ops_binary}; extract the complete release archive"
[[ -f "${source_unit}" ]] || fail "missing ${source_unit}; extract the complete release archive"
[[ -f "${source_ops_socket}" ]] || fail "missing ${source_ops_socket}; extract the complete release archive"
[[ -f "${source_ops_service}" ]] || fail "missing ${source_ops_service}; extract the complete release archive"

elf_magic="$(od -An -t x1 -N 4 "${source_binary}" | tr -d '[:space:]')"
[[ "${elf_magic}" == "7f454c46" ]] || fail "hyfleet-agent is not a Linux ELF binary"
elf_machine="$(od -An -t x1 -j 18 -N 2 "${source_binary}" | tr -d '[:space:]')"
case "$(uname -m)" in
  x86_64)
    architecture="amd64"
    expected_machine="3e00"
    ;;
  aarch64|arm64)
    architecture="arm64"
    expected_machine="b700"
    ;;
  *) fail "unsupported host architecture: $(uname -m)" ;;
esac
[[ "${elf_machine}" == "${expected_machine}" ]] ||
  fail "hyfleet-agent architecture does not match host $(uname -m)"
ops_elf_magic="$(od -An -t x1 -N 4 "${source_ops_binary}" | tr -d '[:space:]')"
[[ "${ops_elf_magic}" == "7f454c46" ]] || fail "hyfleet-agent-ops is not a Linux ELF binary"
ops_elf_machine="$(od -An -t x1 -j 18 -N 2 "${source_ops_binary}" | tr -d '[:space:]')"
[[ "${ops_elf_machine}" == "${expected_machine}" ]] ||
  fail "hyfleet-agent-ops architecture does not match host $(uname -m)"

validate_reality_binary() {
  local binary_path="$1"
  local require_root_owner="$2"
  local actual_hash expected_hash first_line manifest_matches reality_artifact
  reality_artifact="sing-box-${reality_sing_box_version}-linux-${architecture}"
  [[ -f "${source_reality_checksums}" && ! -L "${source_reality_checksums}" ]] ||
    fail "missing checksum manifest ${source_reality_checksums}; extract the complete release archive"
  [[ -f "${binary_path}" && -x "${binary_path}" && ! -L "${binary_path}" ]] ||
    fail "vless-reality requires a regular executable at ${binary_path}"
  if [[ "${require_root_owner}" == true ]]; then
    [[ "$(stat -c '%u:%g' "${binary_path}")" == "0:0" ]] ||
      fail "${binary_path} must be owned by root:root"
  fi
  [[ -z "$(find "${binary_path}" -maxdepth 0 -perm /022 -print -quit)" ]] ||
    fail "${binary_path} must not be writable by group or other"
  [[ "$(od -An -t x1 -N 4 "${binary_path}" | tr -d '[:space:]')" == "7f454c46" ]] ||
    fail "${binary_path} is not a Linux ELF binary"
  [[ "$(od -An -t x1 -j 18 -N 2 "${binary_path}" | tr -d '[:space:]')" == "${expected_machine}" ]] ||
    fail "${binary_path} architecture does not match host $(uname -m)"
  manifest_matches="$(awk -v artifact="${reality_artifact}" '
    length($1) == 64 && $1 ~ /^[0-9a-f]+$/ && $2 == artifact && NF == 2 {
      hash=$1
      matches++
    }
    END {
      if (matches == 1) print hash
    }
  ' "${source_reality_checksums}")"
  expected_hash="${manifest_matches}"
  [[ -n "${expected_hash}" ]] ||
    fail "checksum manifest has no unique entry for ${reality_artifact}"
  actual_hash="$(sha256sum "${binary_path}" | awk '{print $1}')"
  [[ "${actual_hash}" == "${expected_hash}" ]] ||
    fail "${binary_path} checksum does not match the pinned PolyFleet ${architecture} build"
  first_line="$("${binary_path}" version | sed -n '1p')"
  [[ "${first_line}" == "sing-box version ${reality_sing_box_version}" ]] ||
    fail "vless-reality requires sing-box ${reality_sing_box_version}; found ${first_line:-no version output}"
}

if [[ "${adapter_type}" == "vless-reality" ]]; then
  [[ -z "${service_unit}" || "${service_unit}" == "${reality_service_unit}" ]] ||
    fail "vless-reality requires service unit ${reality_service_unit}"
  [[ -z "${core_config_path}" || "${core_config_path}" == "${reality_core_config}" ]] ||
    fail "vless-reality requires core config ${reality_core_config}"
  [[ -z "${s_ui_api_url}" ]] || fail "--s-ui-api-url is not supported for vless-reality"
fi

config_path="/etc/hyfleet/agent.yaml"
state_path="/var/lib/hyfleet-agent/agent-state.json"
agent_already_enrolled=false
if [[ -f "${state_path}" && ! -L "${state_path}" ]] &&
  grep -Eq '"node_credential"[[:space:]]*:[[:space:]]*"[^\"]+' "${state_path}"; then
  agent_already_enrolled=true
fi
if [[ "${replace_config}" == true && "${agent_already_enrolled}" == true ]]; then
  fail "Agent is already enrolled; refusing to replace its identity configuration"
fi
if [[ ! -f "${config_path}" || "${replace_config}" == true ]]; then
  [[ "${server_url}" =~ ^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$ ]] ||
    fail "--server-url must be an HTTPS origin without a path"
  [[ "${node_name}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$ ]] ||
    fail "--node-name contains unsupported characters"

  case "${adapter_type}" in
    native-hysteria2)
      adapter_type="native_hysteria2"
      core_name="hysteria"
      : "${core_config_path:=/etc/hysteria/config.yaml}"
      : "${service_unit:=hysteria-server.service}"
      ;;
    standalone-sing-box)
      adapter_type="standalone_sing_box"
      core_name="sing-box"
      : "${core_config_path:=/etc/sing-box/config.json}"
      : "${service_unit:=sing-box.service}"
      ;;
    s-ui)
      adapter_type="s_ui"
      core_name="sing-box"
      [[ -z "${core_config_path}" ]] || fail "--core-config-path is not supported for S-UI"
      : "${service_unit:=s-ui.service}"
      : "${s_ui_api_url:=http://127.0.0.1:2095/app/apiv2}"
      [[ "${s_ui_api_url}" =~ ^http://127\.0\.0\.1:[0-9]{1,5}(/[A-Za-z0-9._~-]+)*/apiv2/?$ ]] ||
        fail "--s-ui-api-url must use 127.0.0.1, include a port, and end with /apiv2"
      ;;
    vless-reality)
      adapter_type="sing_box_vless_reality"
      core_name="sing-box"
      [[ -z "${s_ui_api_url}" ]] || fail "--s-ui-api-url is not supported for vless-reality"
      [[ -z "${service_unit}" || "${service_unit}" == "${reality_service_unit}" ]] ||
        fail "vless-reality requires service unit ${reality_service_unit}"
      [[ -z "${core_config_path}" || "${core_config_path}" == "${reality_core_config}" ]] ||
        fail "vless-reality requires core config ${reality_core_config}"
      service_unit="${reality_service_unit}"
      core_config_path="${reality_core_config}"
      ;;
    *)
      fail "unsupported --adapter value: ${adapter_type}"
      ;;
  esac
  [[ "${service_unit}" =~ ^[A-Za-z0-9][A-Za-z0-9_.@:-]*$ ]] || fail "invalid systemd service unit"
  if [[ -n "${core_config_path}" ]]; then
    [[ ${#core_config_path} -le 256 && "${core_config_path}" =~ ^/etc/[A-Za-z0-9._/-]+$ &&
      "${core_config_path}" != *"//"* && "${core_config_path}" != *"/./"* &&
      "${core_config_path}" != *"/../"* && "${core_config_path}" != */. &&
      "${core_config_path}" != */.. && "${core_config_path}" != */ ]] ||
      fail "--core-config-path must be a normalized path below /etc without a trailing slash"
    if [[ "${adapter_type}" == "native_hysteria2" ]]; then
      [[ "${core_config_path}" == /etc/hysteria/* ]] ||
        fail "native Hysteria2 config must be below /etc/hysteria"
    elif [[ "${adapter_type}" == "standalone_sing_box" ]]; then
      [[ "${core_config_path}" == /etc/sing-box/* ]] ||
        fail "standalone sing-box config must be below /etc/sing-box"
    fi
    if [[ "${adapter_type}" == "sing_box_vless_reality" ]]; then
      [[ "${core_config_path}" == "${reality_core_config}" ]] ||
        fail "vless-reality requires core config ${reality_core_config}"
      [[ ! -L /etc/sing-box ]] || fail "/etc/sing-box must not be a symbolic link"
      if [[ -e "${core_config_path}" || -L "${core_config_path}" ]]; then
        [[ ! -L "${core_config_path}" && -f "${core_config_path}" ]] ||
          fail "${core_config_path} must be a regular file, not a symbolic link"
      fi
    else
      [[ ! -L "${core_config_path}" && ( -f "${core_config_path}" || -d "${core_config_path}" ) ]] ||
        fail "--core-config-path must identify an existing regular file or directory, not a symlink"
    fi
  fi
elif [[ -n "${server_url}${node_name}${adapter_type}${service_unit}${core_config_path}${s_ui_api_url}" ]]; then
  printf 'Keeping existing %s; supplied configuration options were not applied.\n' "${config_path}"
fi

getent group hyfleet-agent >/dev/null 2>&1 || groupadd --system hyfleet-agent
if ! id -u hyfleet-agent >/dev/null 2>&1; then
  useradd --system --gid hyfleet-agent --home-dir /var/lib/hyfleet-agent \
    --shell /usr/sbin/nologin hyfleet-agent
fi

install -d -o root -g root -m 0755 /etc/hyfleet
install -d -o hyfleet-agent -g hyfleet-agent -m 0700 /var/lib/hyfleet-agent
install -d -o root -g root -m 0755 /usr/local/libexec
install -d -o root -g root -m 0700 /var/lib/hyfleet-backups /var/lib/hyfleet-agent-ops
install -o root -g root -m 0755 "${source_binary}" /usr/local/bin/hyfleet-agent
install -o root -g root -m 0755 "${source_ops_binary}" /usr/local/libexec/hyfleet-agent-ops
/usr/local/bin/hyfleet-agent -version
/usr/local/libexec/hyfleet-agent-ops -version

temporary_dir="$(mktemp -d)"
if [[ ! -f "${config_path}" || "${replace_config}" == true ]]; then
  cat > "${temporary_dir}/agent.yaml" <<EOF
server_url: ${server_url}
node_name: ${node_name}
adapter_type: ${adapter_type}
core_name: ${core_name}
service_unit: ${service_unit}
operations_socket_path: /run/hyfleet-agent-ops.sock
state_path: /var/lib/hyfleet-agent/agent-state.json
auth_listen: 127.0.0.1:18081
auth_path: /hysteria/auth
auth_cache_path: /var/lib/hyfleet-agent/auth-cache.json
traffic_stats_url: http://127.0.0.1:18082
traffic_stats_secret_env: HYFLEET_HY2_STATS_SECRET
traffic_database_path: /var/lib/hyfleet-agent/agent.db
local_database_path: /var/lib/hyfleet-agent/agent.db
heartbeat_every: 15s
telemetry_every: 60s
desired_every: 10s
traffic_every: 30s
EOF
  if [[ -n "${core_config_path}" ]]; then
    printf 'core_config_path: %s\n' "${core_config_path}" >> "${temporary_dir}/agent.yaml"
  fi
  if [[ "${adapter_type}" == "s_ui" ]]; then
    cat >> "${temporary_dir}/agent.yaml" <<EOF
s_ui_api_url: ${s_ui_api_url}
s_ui_token_env: HYFLEET_SUI_TOKEN
EOF
  fi
  if [[ "${adapter_type}" == "sing_box_vless_reality" ]]; then
    cat >> "${temporary_dir}/agent.yaml" <<EOF
sing_box_binary_path: ${reality_binary}
reality_identity_path: ${reality_identity}
reality_api_url: http://127.0.0.1:18083
reality_api_secret_env: HYFLEET_REALITY_API_SECRET
EOF
  fi
  install -o root -g hyfleet-agent -m 0640 "${temporary_dir}/agent.yaml" "${config_path}"
fi

if grep -q 'hyfleet\.example\.com' "${config_path}"; then
  fail "${config_path} still contains the example hostname; rerun with --replace-config"
fi

configured_adapter="$(awk '$1 == "adapter_type:" { print $2; exit }' "${config_path}")"
[[ "${configured_adapter}" =~ ^(native_hysteria2|standalone_sing_box|s_ui|sing_box_vless_reality)$ ]] ||
  fail "agent adapter_type is invalid"
if [[ "${configured_adapter}" == "sing_box_vless_reality" ]]; then
  [[ -f "${source_reality_unit}" ]] ||
    fail "missing ${source_reality_unit}; extract the complete release archive"
  [[ "$(awk '$1 == "service_unit:" { print $2; exit }' "${config_path}")" == "${reality_service_unit}" ]] ||
    fail "vless-reality requires service unit ${reality_service_unit}"
  [[ "$(awk '$1 == "core_config_path:" { print $2; exit }' "${config_path}")" == "${reality_core_config}" ]] ||
    fail "vless-reality requires core config ${reality_core_config}"
  [[ "$(awk '$1 == "sing_box_binary_path:" { print $2; exit }' "${config_path}")" == "${reality_binary}" ]] ||
    fail "vless-reality requires binary ${reality_binary}"
  [[ "$(awk '$1 == "reality_identity_path:" { print $2; exit }' "${config_path}")" == "${reality_identity}" ]] ||
    fail "vless-reality requires identity path ${reality_identity}"
  [[ -f "${source_reality_binary}" ]] ||
    fail "missing ${source_reality_binary}; extract the complete release archive"
  validate_reality_binary "${source_reality_binary}" false
  getent group hyfleet-singbox >/dev/null 2>&1 || groupadd --system hyfleet-singbox
  if ! id -u hyfleet-singbox >/dev/null 2>&1; then
    useradd --system --gid hyfleet-singbox --home-dir /var/lib/hyfleet-singbox \
      --shell /usr/sbin/nologin hyfleet-singbox
  fi
  reality_user_id="$(id -u hyfleet-singbox)"
  reality_group_id="$(getent group hyfleet-singbox | awk -F: '{ print $3; exit }')"
  [[ -n "${reality_user_id}" && -n "${reality_group_id}" ]] ||
    fail "could not resolve the hyfleet-singbox service identity"
  if [[ "${agent_already_enrolled}" == true ]]; then
    agent_user_id="$(id -u hyfleet-agent)"
    agent_group_id="$(getent group hyfleet-agent | awk -F: '{ print $3; exit }')"
    [[ -n "${agent_user_id}" && -n "${agent_group_id}" &&
      "$(stat -c '%u:%g %a' "${state_path}")" == "${agent_user_id}:${agent_group_id} 600" ]] ||
      fail "enrolled Agent state has invalid ownership or permissions"
  fi
  if mkdir -m 0750 /etc/sing-box 2>/dev/null; then
    reality_config_dir_identity="$(stat -c '%d:%i' /etc/sing-box)"
    chown root:hyfleet-singbox /etc/sing-box
    chmod 0750 /etc/sing-box
    [[ "$(stat -c '%d:%i' /etc/sing-box)" == "${reality_config_dir_identity}" ]] ||
      fail "/etc/sing-box changed while securing the newly created directory"
  else
    [[ -d /etc/sing-box && ! -L /etc/sing-box ]] ||
      fail "/etc/sing-box must be a directory, not a symbolic link"
  fi
  [[ "$(stat -c '%u:%g' /etc/sing-box)" == "0:${reality_group_id}" ]] ||
    fail "existing /etc/sing-box must already be owned by root:hyfleet-singbox"
  [[ -z "$(find /etc/sing-box -maxdepth 0 -perm /022 -print -quit)" ]] ||
    fail "existing /etc/sing-box must not be writable by group or other"
  runuser -u hyfleet-singbox -- test -x /etc/sing-box ||
    fail "existing /etc/sing-box is not traversable by hyfleet-singbox"
  if [[ -e /var/lib/hyfleet-singbox || -L /var/lib/hyfleet-singbox ]]; then
    [[ -d /var/lib/hyfleet-singbox && ! -L /var/lib/hyfleet-singbox ]] ||
      fail "/var/lib/hyfleet-singbox must be a directory, not a symbolic link"
    [[ "$(stat -c '%u:%g %a' /var/lib/hyfleet-singbox)" == \
      "${reality_user_id}:${reality_group_id} 750" ]] ||
      fail "existing /var/lib/hyfleet-singbox has invalid ownership or permissions"
  else
    install -d -o hyfleet-singbox -g hyfleet-singbox -m 0750 /var/lib/hyfleet-singbox
  fi
  if [[ -e "${reality_core_config}" || -L "${reality_core_config}" ]]; then
    [[ -f "${reality_core_config}" && ! -L "${reality_core_config}" ]] ||
      fail "${reality_core_config} must be a regular file, not a symbolic link"
    [[ "${agent_already_enrolled}" == true ]] ||
      fail "refusing to adopt existing unmanaged Reality configuration ${reality_core_config}"
    for managed_state in "${reality_identity}" "${reality_applied}"; do
      [[ -f "${managed_state}" && ! -L "${managed_state}" ]] ||
        fail "refusing to adopt Reality configuration without managed local state"
      [[ "$(stat -c '%u:%g %a' "${managed_state}")" == "0:0 600" ]] ||
        fail "managed Reality state has invalid ownership or permissions"
    done
    [[ "$(stat -c '%u:%g %a' "${reality_core_config}")" == "0:${reality_group_id} 640" ]] ||
      fail "managed Reality configuration has invalid ownership or permissions"
  fi
  if [[ -e "${reality_binary}" || -L "${reality_binary}" ]]; then
    validate_reality_binary "${reality_binary}" true
  else
    install -o root -g root -m 0755 "${source_reality_binary}" "${reality_binary}"
    validate_reality_binary "${reality_binary}" true
  fi
fi

install -o root -g root -m 0644 "${source_unit}" \
  /etc/systemd/system/hyfleet-agent.service
install -o root -g root -m 0644 "${source_ops_socket}" \
  /etc/systemd/system/hyfleet-agent-ops.socket
install -o root -g root -m 0644 "${source_ops_service}" \
  /etc/systemd/system/hyfleet-agent-ops@.service
if [[ "${configured_adapter}" == "sing_box_vless_reality" ]]; then
  install -o root -g root -m 0644 "${source_reality_unit}" \
    "/etc/systemd/system/${reality_service_unit}"
fi

runuser -u hyfleet-agent -g hyfleet-agent -- /usr/local/bin/hyfleet-agent \
  -config "${config_path}" -check-config
/usr/local/libexec/hyfleet-agent-ops -config "${config_path}" -check-config

configured_server="$(awk '$1 == "server_url:" { print $2; exit }' "${config_path}")"
[[ "${configured_server}" =~ ^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$ ]] ||
  fail "agent server_url must be a simple HTTPS origin"
curl --fail --silent --show-error "${configured_server}/healthz" >/dev/null ||
  fail "cannot reach ${configured_server}/healthz with trusted TLS"

environment_path="/etc/hyfleet/agent.env"
sui_token=""
if [[ "${configured_adapter}" == "s_ui" ]]; then
  if [[ -f "${environment_path}" ]]; then
    sui_token_count="$(awk -F= '$1 == "HYFLEET_SUI_TOKEN" { count++ } END { print count+0 }' "${environment_path}")"
    [[ "${sui_token_count}" -le 1 ]] || fail "${environment_path} contains duplicate S-UI token entries"
    sui_token="$(awk -F= '$1 == "HYFLEET_SUI_TOKEN" { sub(/^[^=]*=/, ""); print; exit }' "${environment_path}")"
  fi
  if [[ -z "${sui_token}" ]]; then
    printf 'Paste the local S-UI API token, then press Enter: ' > /dev/tty
    IFS= read -r -s sui_token < /dev/tty
    printf '\n' > /dev/tty
  fi
  [[ "${sui_token}" =~ ^[A-Za-z0-9._~+/=:@%-]{1,1024}$ ]] || fail "invalid S-UI API token"
fi

reality_api_secret=""
if [[ "${configured_adapter}" == "sing_box_vless_reality" ]]; then
  if [[ -e "${environment_path}" || -L "${environment_path}" ]]; then
    [[ -f "${environment_path}" && ! -L "${environment_path}" ]] ||
      fail "${environment_path} must be a regular file, not a symbolic link"
    agent_group_id="$(getent group hyfleet-agent | awk -F: '{ print $3; exit }')"
    [[ "$(stat -c '%u:%g %a' "${environment_path}")" == "0:${agent_group_id} 640" ]] ||
      fail "${environment_path} has invalid ownership or permissions"
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
fi

write_agent_environment() {
  local enrollment_token_value="${1:-}"
  : > "${temporary_dir}/agent.env"
  if [[ -n "${sui_token}" ]]; then
    printf 'HYFLEET_SUI_TOKEN=%s\n' "${sui_token}" >> "${temporary_dir}/agent.env"
  fi
  if [[ -n "${reality_api_secret}" ]]; then
    printf 'HYFLEET_REALITY_API_SECRET=%s\n' "${reality_api_secret}" >> "${temporary_dir}/agent.env"
  fi
  if [[ -n "${enrollment_token_value}" ]]; then
    printf 'HYFLEET_ENROLLMENT_TOKEN=%s\n' "${enrollment_token_value}" >> "${temporary_dir}/agent.env"
  fi
  if [[ -s "${temporary_dir}/agent.env" ]]; then
    install -o root -g hyfleet-agent -m 0640 "${temporary_dir}/agent.env" "${environment_path}"
  else
    rm -f -- "${environment_path}"
  fi
}

has_credential=false
if [[ "${agent_already_enrolled}" == true ]]; then
  has_credential=true
fi

if [[ "${has_credential}" != true ]]; then
  printf 'Paste the unexpired one-time enrollment token, then press Enter: ' > /dev/tty
  IFS= read -r -s enrollment_token < /dev/tty
  printf '\n' > /dev/tty
  [[ -n "${enrollment_token}" && ${#enrollment_token} -le 256 ]] || fail "invalid enrollment token"
  write_agent_environment "${enrollment_token}"
  unset enrollment_token
else
  write_agent_environment
fi

systemctl daemon-reload
units_to_verify=(
  /etc/systemd/system/hyfleet-agent.service
  /etc/systemd/system/hyfleet-agent-ops.socket
  /etc/systemd/system/hyfleet-agent-ops@.service
)
if [[ "${configured_adapter}" == "sing_box_vless_reality" ]]; then
  units_to_verify+=("/etc/systemd/system/${reality_service_unit}")
fi
systemd-analyze verify "${units_to_verify[@]}"
systemctl enable --now hyfleet-agent-ops.socket
if [[ "${configured_adapter}" == "sing_box_vless_reality" ]]; then
  systemctl enable "${reality_service_unit}"
fi
systemctl enable hyfleet-agent
systemctl restart hyfleet-agent

if [[ "${has_credential}" != true ]]; then
  for _ in {1..30}; do
    if [[ -f "${state_path}" ]] &&
      grep -Eq '"node_credential"[[:space:]]*:[[:space:]]*"[^\"]+' "${state_path}"; then
      has_credential=true
      break
    fi
    sleep 1
  done
fi

if [[ "${has_credential}" != true ]]; then
  systemctl stop hyfleet-agent || true
  journalctl -u hyfleet-agent -b -n 80 --no-pager || true
  fail "Agent enrollment did not complete; the service was stopped and the token file was retained"
fi

write_agent_environment
unset sui_token
unset reality_api_secret
systemctl restart hyfleet-agent
for _ in {1..10}; do
  if systemctl is-active --quiet hyfleet-agent; then
    break
  fi
  sleep 1
done
systemctl is-active --quiet hyfleet-agent || {
  journalctl -u hyfleet-agent -b -n 80 --no-pager || true
  fail "Agent did not remain active after removing the one-time enrollment token"
}

printf 'PolyFleet Agent is enrolled and active. The one-time enrollment token was removed.\n'
if [[ "${configured_adapter}" == "s_ui" ]]; then
  printf 'The local S-UI API token remains in %s with restricted permissions.\n' "${environment_path}"
fi
if [[ "${configured_adapter}" == "sing_box_vless_reality" ]]; then
  printf '%s is enabled and will start after the first valid Reality desired state.\n' \
    "${reality_service_unit}"
fi
printf 'Confirm that the node becomes online in the PolyFleet dashboard.\n'

#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
bundle_dir="$(cd -- "${script_dir}/.." && pwd)"
source_binary="${bundle_dir}/bin/hyfleet-server"
source_config="${bundle_dir}/configs/server.example.yaml"
source_unit="${script_dir}/systemd/hyfleet-server.service"

public_url=""
listen_address="127.0.0.1:8080"
replace_config=false
temporary_dir=""

usage() {
  cat <<'EOF'
Usage:
  sudo bash deploy/install-server.sh --public-url https://panel.example.com

Options:
  --public-url URL       Dedicated HTTPS origin for PolyFleet (required initially)
  --listen ADDRESS      Loopback listen address (default: 127.0.0.1:8080)
  --replace-config      Replace /etc/hyfleet/server.yaml with generated settings
  -h, --help            Show this help
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
    --public-url)
      (($# >= 2)) || fail "--public-url requires a value"
      public_url="$2"
      shift 2
      ;;
    --listen)
      (($# >= 2)) || fail "--listen requires a value"
      listen_address="$2"
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
  awk curl getent grep groupadd head install journalctl mktemp od openssl runuser sed \
  systemctl systemd-analyze tr uname useradd; do
  command -v "${command_name}" >/dev/null 2>&1 || fail "required command is missing: ${command_name}"
done

[[ -f "${source_binary}" ]] || fail "missing ${source_binary}; extract the complete release archive"
[[ -f "${source_config}" ]] || fail "missing ${source_config}; extract the complete release archive"
[[ -f "${source_unit}" ]] || fail "missing ${source_unit}; extract the complete release archive"

elf_magic="$(od -An -t x1 -N 4 "${source_binary}" | tr -d '[:space:]')"
[[ "${elf_magic}" == "7f454c46" ]] || fail "hyfleet-server is not a Linux ELF binary"
elf_machine="$(od -An -t x1 -j 18 -N 2 "${source_binary}" | tr -d '[:space:]')"
case "$(uname -m)" in
  x86_64) expected_machine="3e00" ;;
  aarch64|arm64) expected_machine="b700" ;;
  *) fail "unsupported host architecture: $(uname -m)" ;;
esac
[[ "${elf_machine}" == "${expected_machine}" ]] ||
  fail "hyfleet-server architecture does not match host $(uname -m)"

config_path="/etc/hyfleet/server.yaml"
if [[ ! -f "${config_path}" || "${replace_config}" == true ]]; then
  [[ "${public_url}" =~ ^https://[A-Za-z0-9.-]+(:[0-9]{1,5})?$ ]] ||
    fail "--public-url must be a dedicated HTTPS origin without a path"
  [[ "${listen_address}" =~ ^127\.0\.0\.1:[0-9]{1,5}$ ]] ||
    fail "--listen must use 127.0.0.1 and a TCP port"
  listen_port="${listen_address##*:}"
  ((10#${listen_port} >= 1 && 10#${listen_port} <= 65535)) || fail "listen port is invalid"
elif [[ -n "${public_url}" ]]; then
  printf 'Keeping existing %s; --public-url was not applied.\n' "${config_path}"
fi

getent group hyfleet >/dev/null 2>&1 || groupadd --system hyfleet
if ! id -u hyfleet >/dev/null 2>&1; then
  useradd --system --gid hyfleet --home-dir /var/lib/hyfleet \
    --shell /usr/sbin/nologin hyfleet
fi

install -d -o root -g root -m 0755 /etc/hyfleet
install -d -o hyfleet -g hyfleet -m 0700 /var/lib/hyfleet
install -o root -g root -m 0755 "${source_binary}" /usr/local/bin/hyfleet-server
/usr/local/bin/hyfleet-server -version

temporary_dir="$(mktemp -d)"
if [[ ! -f "${config_path}" || "${replace_config}" == true ]]; then
  sed \
    -e "s|^listen:.*|listen: ${listen_address}|" \
    -e "s|^public_url:.*|public_url: ${public_url}|" \
    "${source_config}" > "${temporary_dir}/server.yaml"
  install -o root -g hyfleet -m 0640 "${temporary_dir}/server.yaml" "${config_path}"
fi

if grep -q 'hyfleet\.example\.com' "${config_path}"; then
  fail "${config_path} still contains the example hostname; rerun with --replace-config"
fi

install -o root -g root -m 0644 "${source_unit}" \
  /etc/systemd/system/hyfleet-server.service

runuser -u hyfleet -g hyfleet -- /usr/local/bin/hyfleet-server \
  -config "${config_path}" -check-config

environment_path="/etc/hyfleet/server.env"
bootstrap_token=""
if [[ -f "${environment_path}" ]]; then
  bootstrap_token="$(sed -n 's/^HYFLEET_BOOTSTRAP_TOKEN=//p' "${environment_path}" | head -n 1)"
fi
if [[ -z "${bootstrap_token}" || "${bootstrap_token}" == "replace-before-starting" ]]; then
  bootstrap_token="$(openssl rand -hex 32)"
  printf 'HYFLEET_BOOTSTRAP_TOKEN=%s\n' "${bootstrap_token}" > "${temporary_dir}/server.env"
  install -o root -g hyfleet -m 0640 "${temporary_dir}/server.env" "${environment_path}"
fi

systemctl daemon-reload
systemd-analyze verify /etc/systemd/system/hyfleet-server.service
systemctl enable hyfleet-server
systemctl restart hyfleet-server

health_listen="$(awk '$1 == "listen:" { print $2; exit }' "${config_path}")"
[[ "${health_listen}" =~ ^127\.0\.0\.1:[0-9]{1,5}$ ]] ||
  fail "server listen address must remain on 127.0.0.1"

healthy=false
for _ in {1..20}; do
  if curl --fail --silent --show-error "http://${health_listen}/healthz" >/dev/null 2>&1; then
    healthy=true
    break
  fi
  sleep 1
done
if [[ "${healthy}" != true ]]; then
  systemctl status hyfleet-server --no-pager --full || true
  journalctl -u hyfleet-server -b -n 80 --no-pager || true
  fail "PolyFleet Server did not pass its local health check"
fi

setup_status="$(curl --fail --silent --show-error "http://${health_listen}/api/v1/setup/status")"
if grep -Eq '"setup_required"[[:space:]]*:[[:space:]]*false' <<<"${setup_status}"; then
  rm -f -- "${environment_path}"
  systemctl restart hyfleet-server
  printf 'PolyFleet Server is healthy; an administrator already exists.\n'
else
  printf '\nPolyFleet Server is healthy. Bootstrap token (do not share):\n%s\n\n' "${bootstrap_token}"
  printf 'Open the configured HTTPS URL after the reverse proxy is ready.\n'
  printf 'After creating the administrator, run:\n'
  printf '  sudo rm -f %s && sudo systemctl restart hyfleet-server\n' "${environment_path}"
fi

printf 'Local health check: http://%s/healthz\n' "${health_listen}"

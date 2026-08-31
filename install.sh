#!/usr/bin/env bash
set -Eeuo pipefail

repository="Sen62455/PolyFleet"
version="v1.3.0"
temporary_dir=""
max_archive_bytes=$((64 * 1024 * 1024))
max_checksum_bytes=1024
max_archive_entries=256

usage() {
  cat <<'EOF'
Usage:
  sudo bash install.sh server [--version v1.3.0] --public-url https://panel.example.com
  sudo bash install.sh agent [--version v1.3.0] --server-url https://panel.example.com \
    --node-name NODE \
    --adapter native-hysteria2|standalone-sing-box|s-ui|vless-reality [options]

This bootstrap supports systemd-based Debian and Ubuntu on amd64 or arm64. It
downloads a GitHub Release, verifies both checksum layers, then runs the bundled
native installer. Agent enrollment remains interactive so its one-time token is
not written to shell history.

The vless-reality adapter uses the pinned compatibility sing-box build shipped
inside the PolyFleet release. The native installer verifies and installs that
exact build as /usr/bin/sing-box for a clean managed Reality node.
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
trap 'printf "ERROR: bootstrap installation failed at line %s.\n" "$LINENO" >&2' ERR

component="${1:-}"
case "${component}" in
  server|agent)
    shift
    ;;
  -h|--help|"")
    usage
    exit 0
    ;;
  *)
    fail "first argument must be server or agent"
    ;;
esac

installer_arguments=()
while (($# > 0)); do
  case "$1" in
    --version)
      (($# >= 2)) || fail "--version requires a value"
      version="$2"
      shift 2
      ;;
    *)
      installer_arguments+=("$1")
      shift
      ;;
  esac
done

[[ "${EUID}" -eq 0 ]] || fail "run this bootstrap with sudo"
[[ "${version}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([._-][0-9A-Za-z.-]+)?$ ]] || fail "invalid release version"
[[ -r /etc/os-release ]] || fail "cannot identify this Linux distribution"
os_id="$(awk -F= '$1 == "ID" { gsub(/\"/, "", $2); print tolower($2); exit }' /etc/os-release)"
os_like="$(awk -F= '$1 == "ID_LIKE" { gsub(/\"/, "", $2); print tolower($2); exit }' /etc/os-release)"
if [[ "${os_id}" != "debian" && "${os_id}" != "ubuntu" && " ${os_like} " != *" debian "* ]]; then
  fail "supported distributions are Debian and Ubuntu"
fi
command -v apt-get >/dev/null 2>&1 || fail "apt-get is required on a clean host"
[[ -d /run/systemd/system ]] || fail "native installation requires a host booted with systemd"

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends \
  ca-certificates coreutils curl iputils-ping openssl passwd tar util-linux

case "$(uname -m)" in
  x86_64)
    architecture="amd64"
    ;;
  aarch64|arm64)
    architecture="arm64"
    ;;
  *)
    fail "unsupported host architecture: $(uname -m)"
    ;;
esac

package_name="polyfleet-${version}-linux-${architecture}"
archive_name="${package_name}.tar.gz"
checksum_name="${archive_name}.sha256"
release_url="https://github.com/${repository}/releases/download/${version}"
temporary_dir="$(mktemp -d)"
curl --fail --location --proto '=https' --tlsv1.2 --output "${temporary_dir}/${archive_name}" \
  "${release_url}/${archive_name}"
curl --fail --location --proto '=https' --tlsv1.2 --output "${temporary_dir}/${checksum_name}" \
  "${release_url}/${checksum_name}"

archive_size="$(stat -c '%s' "${temporary_dir}/${archive_name}")"
checksum_size="$(stat -c '%s' "${temporary_dir}/${checksum_name}")"
[[ "${archive_size}" =~ ^[0-9]+$ && "${archive_size}" -ge 1 &&
  "${archive_size}" -le "${max_archive_bytes}" ]] || fail "release archive size is invalid"
[[ "${checksum_size}" =~ ^[0-9]+$ && "${checksum_size}" -ge 1 &&
  "${checksum_size}" -le "${max_checksum_bytes}" ]] || fail "release checksum size is invalid"

read -r expected_hash checksum_file_name checksum_extra < "${temporary_dir}/${checksum_name}" ||
  fail "release checksum file could not be read"
checksum_file_name="${checksum_file_name#\*}"
[[ -z "${checksum_extra:-}" && "${expected_hash}" =~ ^[0-9a-fA-F]{64}$ &&
  "${checksum_file_name}" == "${archive_name}" ]] || fail "release checksum file has an invalid format"
expected_hash="${expected_hash,,}"
actual_hash="$(sha256sum "${temporary_dir}/${archive_name}" | awk '{print $1}')"
[[ "${actual_hash}" == "${expected_hash}" ]] || fail "release archive checksum mismatch"

mapfile -t archive_entries < <(tar -tzf "${temporary_dir}/${archive_name}")
mapfile -t archive_listing < <(LC_ALL=C tar -tvzf "${temporary_dir}/${archive_name}")
[[ ${#archive_entries[@]} -ge 1 && ${#archive_entries[@]} -le ${max_archive_entries} &&
  ${#archive_entries[@]} -eq ${#archive_listing[@]} ]] || fail "release archive entry count is invalid"
for index in "${!archive_entries[@]}"; do
  archive_entry="${archive_entries[${index}]%/}"
  entry_type="${archive_listing[${index}]:0:1}"
  [[ "${entry_type}" == "-" || "${entry_type}" == "d" ]] ||
    fail "release archive contains a link or special file"
  [[ "${archive_entry}" =~ ^[A-Za-z0-9._@/-]+$ &&
    ( "${archive_entry}" == "${package_name}" || "${archive_entry}" == "${package_name}/"* ) &&
    "${archive_entry}" != *"//"* && "${archive_entry}" != *"/./"* &&
    "${archive_entry}" != *"/../"* && "${archive_entry}" != */. &&
    "${archive_entry}" != */.. ]] || fail "release archive contains an unsafe path"
done

tar --no-same-owner --no-same-permissions -xzf \
  "${temporary_dir}/${archive_name}" -C "${temporary_dir}"
cd "${temporary_dir}/${package_name}"
sha256sum -c SHA256SUMS
bash -n deploy/*.sh

case "${component}" in
  server)
    bash deploy/install-server.sh "${installer_arguments[@]}"
    ;;
  agent)
    bash deploy/install-agent.sh "${installer_arguments[@]}"
    ;;
esac

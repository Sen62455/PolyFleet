#!/usr/bin/env bash
set -Eeuo pipefail

output_dir="/var/backups/hyfleet"
temporary_dir=""

usage() {
  cat <<'EOF'
Usage:
  sudo bash deploy/backup-server.sh [--output-dir /var/backups/hyfleet]

Creates a consistent server archive and a separate master-key file. Store the
two files separately or encrypt the directory before copying it off the VPS.
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
trap 'printf "ERROR: server backup failed at line %s.\n" "$LINENO" >&2' ERR

while (($# > 0)); do
  case "$1" in
    --output-dir)
      (($# >= 2)) || fail "--output-dir requires a value"
      output_dir="$2"
      shift 2
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

[[ "${EUID}" -eq 0 ]] || fail "run this backup with sudo"
[[ ${#output_dir} -le 256 && "${output_dir}" =~ ^/[A-Za-z0-9._/-]+$ &&
  "${output_dir}" != *"//"* && "${output_dir}" != *"/./"* &&
  "${output_dir}" != *"/../"* && "${output_dir}" != */. &&
  "${output_dir}" != */.. ]] || fail "--output-dir must be a normalized absolute path"
for command_name in awk chmod date install mktemp runuser sha256sum stat tar tr; do
  command -v "${command_name}" >/dev/null 2>&1 || fail "required command is missing: ${command_name}"
done

binary_path="/usr/local/bin/hyfleet-server"
config_path="/etc/hyfleet/server.yaml"
database_path="/var/lib/hyfleet/server.db"
master_key_path="/var/lib/hyfleet/master.key"
[[ -x "${binary_path}" && -f "${config_path}" && -f "${database_path}" && -f "${master_key_path}" ]] ||
  fail "PolyFleet Server is not installed with the compatible filesystem layout"
[[ ! -L "${config_path}" && ! -L "${database_path}" && ! -L "${master_key_path}" ]] ||
  fail "server backup paths cannot be symbolic links"
[[ "$(stat -c '%s' "${master_key_path}")" == "32" ]] || fail "master key must contain exactly 32 bytes"

install -d -o root -g root -m 0700 "${output_dir}"
temporary_dir="$(mktemp -d)"
chmod 0711 "${temporary_dir}"
staging_dir="${temporary_dir}/archive"
database_dir="${temporary_dir}/database"
install -d -o root -g root -m 0700 "${staging_dir}"
install -d -o hyfleet -g hyfleet -m 0700 "${database_dir}"

runuser -u hyfleet -g hyfleet -- "${binary_path}" \
  -config "${config_path}" -backup-database "${database_dir}/server.db"
install -o root -g root -m 0600 "${database_dir}/server.db" "${staging_dir}/server.db"
install -o root -g root -m 0640 "${config_path}" "${staging_dir}/server.yaml"

database_hash="$(sha256sum "${staging_dir}/server.db" | awk '{print $1}')"
config_hash="$(sha256sum "${staging_dir}/server.yaml" | awk '{print $1}')"
master_key_hash="$(sha256sum "${master_key_path}" | awk '{print $1}')"
created_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
version_line="$(${binary_path} -version | tr -d '\r\n')"
[[ "${database_hash}${config_hash}${master_key_hash}" =~ ^[0-9a-f]{192}$ ]] ||
  fail "could not calculate backup hashes"
[[ ${#version_line} -ge 1 && ${#version_line} -le 256 ]] || fail "installed version output is invalid"

cat > "${staging_dir}/manifest" <<EOF
format_version=1
component=hyfleet-server
created_at=${created_at}
version=${version_line}
database_sha256=${database_hash}
config_sha256=${config_hash}
master_key_sha256=${master_key_hash}
EOF
chmod 0600 "${staging_dir}/manifest"

timestamp="$(date -u +%Y%m%dT%H%M%SZ)"
archive_name="hyfleet-server-backup-${timestamp}.tar.gz"
checksum_name="${archive_name}.sha256"
key_name="hyfleet-server-master-key-${timestamp}.key"
key_checksum_name="${key_name}.sha256"
for candidate in "${archive_name}" "${checksum_name}" "${key_name}" "${key_checksum_name}"; do
  [[ ! -e "${output_dir}/${candidate}" ]] || fail "backup output already exists: ${output_dir}/${candidate}"
done

tar -czf "${temporary_dir}/${archive_name}" -C "${staging_dir}" manifest server.db server.yaml
archive_hash="$(sha256sum "${temporary_dir}/${archive_name}" | awk '{print $1}')"
printf '%s  %s\n' "${archive_hash}" "${archive_name}" > "${temporary_dir}/${checksum_name}"
install -o root -g root -m 0600 "${master_key_path}" "${temporary_dir}/${key_name}"
printf '%s  %s\n' "${master_key_hash}" "${key_name}" > "${temporary_dir}/${key_checksum_name}"

install -o root -g root -m 0600 "${temporary_dir}/${archive_name}" "${output_dir}/${archive_name}"
install -o root -g root -m 0600 "${temporary_dir}/${checksum_name}" "${output_dir}/${checksum_name}"
install -o root -g root -m 0600 "${temporary_dir}/${key_name}" "${output_dir}/${key_name}"
install -o root -g root -m 0600 "${temporary_dir}/${key_checksum_name}" "${output_dir}/${key_checksum_name}"

printf 'Server backup created:\n  %s\n  %s\n' \
  "${output_dir}/${archive_name}" "${output_dir}/${checksum_name}"
printf 'Separate master key created:\n  %s\n  %s\n' \
  "${output_dir}/${key_name}" "${output_dir}/${key_checksum_name}"
printf 'Keep the archive and master key in separately protected off-VPS storage.\n'

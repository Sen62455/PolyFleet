#!/usr/bin/env bash
set -Eeuo pipefail

archive_path=""
checksum_path=""
master_key_path=""
master_key_checksum_path=""
temporary_dir=""
rollback_dir=""
committed=false

usage() {
  cat <<'EOF'
Usage:
  sudo bash deploy/restore-server.sh \
    --archive /path/hyfleet-server-backup-TIME.tar.gz \
    --checksum /path/hyfleet-server-backup-TIME.tar.gz.sha256 \
    --master-key /path/hyfleet-server-master-key-TIME.key \
    --master-key-checksum /path/hyfleet-server-master-key-TIME.key.sha256

Install the same or a newer PolyFleet release first. Restore is supported only for
the standard native layout under /etc/hyfleet and /var/lib/hyfleet.
EOF
}

fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

checksum_value() {
  local checksum_file="$1"
  local expected_name="$2"
  local hash_value file_name extra
  read -r hash_value file_name extra < "${checksum_file}" || return 1
  [[ -z "${extra:-}" && "${file_name}" == "${expected_name}" && "${hash_value}" =~ ^[0-9a-fA-F]{64}$ ]] ||
    return 1
  printf '%s' "${hash_value,,}"
}

manifest_value() {
  local key="$1"
  local manifest="$2"
  local count value
  count="$(awk -F= -v wanted="${key}" '$1 == wanted { count++ } END { print count+0 }' "${manifest}")"
  [[ "${count}" == "1" ]] || return 1
  value="$(awk -F= -v wanted="${key}" '$1 == wanted { sub(/^[^=]*=/, ""); print; exit }' "${manifest}")"
  [[ -n "${value}" ]] || return 1
  printf '%s' "${value}"
}

rollback() {
  local status="$1"
  if [[ "${committed}" != true && -n "${rollback_dir}" && -d "${rollback_dir}" ]]; then
    set +e
    printf 'Restore failed; restoring the pre-restore server state.\n' >&2
    systemctl stop hyfleet-server.service
    install -o hyfleet -g hyfleet -m 0600 "${rollback_dir}/server.db" /var/lib/hyfleet/server.db
    install -o hyfleet -g hyfleet -m 0600 "${rollback_dir}/master.key" /var/lib/hyfleet/master.key
    install -o root -g hyfleet -m 0640 "${rollback_dir}/server.yaml" /etc/hyfleet/server.yaml
    rm -f -- /var/lib/hyfleet/server.db-wal /var/lib/hyfleet/server.db-shm
    systemctl start hyfleet-server.service
  fi
  if [[ -n "${temporary_dir}" && -d "${temporary_dir}" ]]; then
    rm -rf -- "${temporary_dir}"
  fi
  exit "${status}"
}
trap 'rollback $?' EXIT

while (($# > 0)); do
  case "$1" in
    --archive)
      (($# >= 2)) || fail "--archive requires a value"
      archive_path="$2"
      shift 2
      ;;
    --checksum)
      (($# >= 2)) || fail "--checksum requires a value"
      checksum_path="$2"
      shift 2
      ;;
    --master-key)
      (($# >= 2)) || fail "--master-key requires a value"
      master_key_path="$2"
      shift 2
      ;;
    --master-key-checksum)
      (($# >= 2)) || fail "--master-key-checksum requires a value"
      master_key_checksum_path="$2"
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

[[ "${EUID}" -eq 0 ]] || fail "run this restore with sudo"
for command_name in awk basename curl grep install mktemp sha256sum stat systemctl tar; do
  command -v "${command_name}" >/dev/null 2>&1 || fail "required command is missing: ${command_name}"
done
for required_path in "${archive_path}" "${checksum_path}" "${master_key_path}" "${master_key_checksum_path}"; do
  [[ -n "${required_path}" && -f "${required_path}" && ! -L "${required_path}" ]] ||
    fail "every restore input must be an existing regular file, not a symlink"
done
[[ -x /usr/local/bin/hyfleet-server && -f /etc/hyfleet/server.yaml ]] ||
  fail "install PolyFleet Server before restoring a backup"

archive_name="$(basename -- "${archive_path}")"
key_name="$(basename -- "${master_key_path}")"
expected_archive_hash="$(checksum_value "${checksum_path}" "${archive_name}")" ||
  fail "archive checksum file has an invalid format"
expected_key_hash="$(checksum_value "${master_key_checksum_path}" "${key_name}")" ||
  fail "master-key checksum file has an invalid format"
actual_archive_hash="$(sha256sum "${archive_path}" | awk '{print $1}')"
actual_key_hash="$(sha256sum "${master_key_path}" | awk '{print $1}')"
[[ "${actual_archive_hash}" == "${expected_archive_hash}" ]] || fail "server archive checksum mismatch"
[[ "${actual_key_hash}" == "${expected_key_hash}" ]] || fail "master-key checksum mismatch"
[[ "$(stat -c '%s' "${master_key_path}")" == "32" ]] || fail "master key must contain exactly 32 bytes"

mapfile -t archive_entries < <(tar -tzf "${archive_path}")
[[ ${#archive_entries[@]} -eq 3 ]] || fail "server archive must contain exactly three files"
for expected_entry in manifest server.db server.yaml; do
  matches=0
  for archive_entry in "${archive_entries[@]}"; do
    [[ "${archive_entry}" == "${expected_entry}" ]] && matches=$((matches + 1))
  done
  [[ ${matches} -eq 1 ]] || fail "server archive entry is missing or duplicated: ${expected_entry}"
done

temporary_dir="$(mktemp -d)"
tar --no-same-owner --no-same-permissions -xzf "${archive_path}" -C "${temporary_dir}"
manifest_path="${temporary_dir}/manifest"
[[ "$(manifest_value format_version "${manifest_path}")" == "1" ]] || fail "unsupported backup format"
[[ "$(manifest_value component "${manifest_path}")" == "hyfleet-server" ]] || fail "backup component is invalid"
manifest_database_hash="$(manifest_value database_sha256 "${manifest_path}")" || fail "database hash is missing"
manifest_config_hash="$(manifest_value config_sha256 "${manifest_path}")" || fail "config hash is missing"
manifest_key_hash="$(manifest_value master_key_sha256 "${manifest_path}")" || fail "master-key hash is missing"
[[ "${manifest_database_hash}${manifest_config_hash}${manifest_key_hash}" =~ ^[0-9a-f]{192}$ ]] ||
  fail "backup manifest contains an invalid hash"
[[ "$(sha256sum "${temporary_dir}/server.db" | awk '{print $1}')" == "${manifest_database_hash}" ]] ||
  fail "database hash does not match the backup manifest"
[[ "$(sha256sum "${temporary_dir}/server.yaml" | awk '{print $1}')" == "${manifest_config_hash}" ]] ||
  fail "configuration hash does not match the backup manifest"
[[ "${actual_key_hash}" == "${manifest_key_hash}" ]] || fail "master key does not match the backup manifest"

grep -Eq '^database_path:[[:space:]]+/var/lib/hyfleet/server\.db[[:space:]]*$' "${temporary_dir}/server.yaml" ||
  fail "restored configuration must use /var/lib/hyfleet/server.db"
grep -Eq '^master_key_file:[[:space:]]+/var/lib/hyfleet/master\.key[[:space:]]*$' "${temporary_dir}/server.yaml" ||
  fail "restored configuration must use /var/lib/hyfleet/master.key"
listen_address="$(awk '$1 == "listen:" { print $2; exit }' "${temporary_dir}/server.yaml")"
[[ "${listen_address}" =~ ^127\.0\.0\.1:[0-9]{1,5}$ ]] || fail "restored listen address is invalid"
/usr/local/bin/hyfleet-server -config "${temporary_dir}/server.yaml" -check-config
/usr/local/bin/hyfleet-server -check-database "${temporary_dir}/server.db"

install -d -o root -g root -m 0700 /var/lib/hyfleet-restores
rollback_dir="$(mktemp -d /var/lib/hyfleet-restores/server.XXXXXXXX)"
/usr/local/bin/hyfleet-server -config /etc/hyfleet/server.yaml \
  -backup-database "${rollback_dir}/server.db"
install -o root -g root -m 0600 /var/lib/hyfleet/master.key "${rollback_dir}/master.key"
install -o root -g root -m 0600 /etc/hyfleet/server.yaml "${rollback_dir}/server.yaml"

systemctl stop hyfleet-server.service
install -o hyfleet -g hyfleet -m 0600 "${temporary_dir}/server.db" /var/lib/hyfleet/server.db
install -o hyfleet -g hyfleet -m 0600 "${master_key_path}" /var/lib/hyfleet/master.key
install -o root -g hyfleet -m 0640 "${temporary_dir}/server.yaml" /etc/hyfleet/server.yaml
rm -f -- /var/lib/hyfleet/server.db-wal /var/lib/hyfleet/server.db-shm
systemctl start hyfleet-server.service

healthy=false
for _ in {1..20}; do
  if curl --fail --silent --show-error "http://${listen_address}/healthz" >/dev/null 2>&1; then
    healthy=true
    break
  fi
  sleep 1
done
[[ "${healthy}" == true ]] || fail "restored PolyFleet Server did not pass its health check"

committed=true
rm -rf -- "${temporary_dir}"
temporary_dir=""
printf 'Server restore completed. Pre-restore rollback data remains at %s\n' "${rollback_dir}"

#!/usr/bin/env bash
set -Eeuo pipefail

base_image="${1:-}"
bundle_dir="${2:-}"
[[ -n "${base_image}" && -d "${bundle_dir}" ]] || {
  printf 'Usage: bash tests/install-e2e.sh BASE_IMAGE BUNDLE_DIRECTORY\n' >&2
  exit 2
}
for command_name in docker realpath; do
  command -v "${command_name}" >/dev/null 2>&1 || {
    printf 'Missing command: %s\n' "${command_name}" >&2
    exit 1
  }
done

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repository_root="$(cd -- "${script_dir}/.." && pwd)"
bundle_dir="$(realpath -- "${bundle_dir}")"
suffix="$(printf '%s' "${base_image}" | tr -c 'A-Za-z0-9' '-' | tr '[:upper:]' '[:lower:]')"
image_name="hyfleet-systemd-e2e:${suffix}"
container_name="hyfleet-systemd-e2e-${suffix}-${RANDOM}"

cleanup() {
  docker rm -f "${container_name}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker build \
  --build-arg "BASE_IMAGE=${base_image}" \
  --tag "${image_name}" \
  --file "${repository_root}/tests/systemd/Dockerfile" \
  "${repository_root}/tests/systemd"
docker run --detach \
  --name "${container_name}" \
  --privileged \
  --cgroupns=host \
  --tmpfs /run \
  --tmpfs /run/lock \
  --volume /sys/fs/cgroup:/sys/fs/cgroup:rw \
  "${image_name}"

ready=false
for _ in {1..30}; do
  state="$(docker exec "${container_name}" systemctl is-system-running 2>/dev/null || true)"
  if [[ "${state}" == "running" || "${state}" == "degraded" ]]; then
    ready=true
    break
  fi
  sleep 1
done
[[ "${ready}" == true ]] || {
  docker logs "${container_name}"
  exit 1
}

docker exec "${container_name}" mkdir -p /opt/hyfleet
docker cp "${bundle_dir}/." "${container_name}:/opt/hyfleet/"
docker exec "${container_name}" bash -lc '
  set -Eeuo pipefail
  cd /opt/hyfleet
  bash deploy/install-server.sh --public-url https://panel.example.com >/dev/null
  systemctl is-active --quiet hyfleet-server.service || {
    systemctl status hyfleet-server.service --no-pager --full >&2 || true
    exit 1
  }
  curl --fail --silent --show-error http://127.0.0.1:8080/healthz >/dev/null
  printf "Server installation passed.\n"
'

docker exec "${container_name}" bash -lc '
  set -Eeuo pipefail
  cd /opt/hyfleet
  grep -qx "ProtectProc=default" deploy/systemd/hyfleet-agent.service
  mkdir -p /etc/sing-box/conf /var/lib/hyfleet-agent-ops
  printf "%s\n" "{\"log\":{\"level\":\"info\"}}" > /etc/sing-box/conf/00_log.json
  printf "%s\n" "{\"inbounds\":[{\"type\":\"hysteria2\"}]}" > /etc/sing-box/conf/12_hysteria2_inbounds.json
  groupadd --system hyfleet-agent
  cat > /etc/hyfleet/agent.yaml <<EOF
server_url: https://panel.example.com
node_name: e2e-sing-box
adapter_type: standalone_sing_box
core_name: sing-box
service_unit: sing-box.service
core_config_path: /etc/sing-box/conf
operations_socket_path: /run/hyfleet-agent-ops.sock
state_path: /var/lib/hyfleet-agent/agent-state.json
local_database_path: /var/lib/hyfleet-agent/agent.db
heartbeat_every: 15s
telemetry_every: 60s
desired_every: 10s
EOF
  request="{\"operation\":{\"id\":\"8bf7ac09-4750-481b-a007-5c2296373fd4\",\"sequence\":1,\"type\":\"backup_config\",\"attempt\":1}}"
  if printf "%s" "${request}" | bin/hyfleet-agent-ops -config /etc/hyfleet/agent.yaml \
    >/tmp/direct-helper.out 2>/tmp/direct-helper.err; then
    printf "Operations helper unexpectedly accepted a non-socket stdin.\n" >&2
    exit 1
  fi
  grep -q "operations helper socket input" /tmp/direct-helper.err
  install -m 0755 bin/hyfleet-agent-ops /usr/local/libexec/hyfleet-agent-ops
  install -m 0644 deploy/systemd/hyfleet-agent-ops.socket /etc/systemd/system/hyfleet-agent-ops.socket
  install -m 0644 deploy/systemd/hyfleet-agent-ops@.service /etc/systemd/system/hyfleet-agent-ops@.service
  systemctl daemon-reload
  systemctl enable --now hyfleet-agent-ops.socket
  systemctl is-active --quiet hyfleet-agent-ops.socket
  [[ -S /run/hyfleet-agent-ops.sock ]]
  result="$(printf "%s" "${request}" | socat -t 10 - UNIX-CONNECT:/run/hyfleet-agent-ops.sock)"
  grep -q "\"status\":\"succeeded\"" <<<"${result}" || {
    printf "Agent configuration backup failed: %s\n" "${result}" >&2
    exit 1
  }
  backup_path="$(find /var/lib/hyfleet-backups -maxdepth 1 -type f -name "*-conf.tar.gz" -print -quit)"
  [[ -n "${backup_path}" ]] || {
    printf "Agent configuration backup archive was not created.\n" >&2
    exit 1
  }
  printf "Agent directory backup passed.\n"
'

docker exec "${container_name}" bash -lc '
  set -Eeuo pipefail
  cd /opt/hyfleet
  bash deploy/backup-server.sh --output-dir /var/backups/hyfleet
  bash deploy/update-component.sh server
  systemctl is-active --quiet hyfleet-server.service
  cp deploy/systemd/hyfleet-server.service /tmp/hyfleet-server.service
  sed -i "s#^ExecStart=.*#ExecStart=/bin/false#" deploy/systemd/hyfleet-server.service
  if bash deploy/update-component.sh server; then
    printf "Expected deliberately broken Server update to fail.\n" >&2
    exit 1
  fi
  mv /tmp/hyfleet-server.service deploy/systemd/hyfleet-server.service
  systemctl is-active --quiet hyfleet-server.service
  curl --fail --silent http://127.0.0.1:8080/healthz >/dev/null
  archive=(/var/backups/hyfleet/hyfleet-server-backup-*.tar.gz)
  checksum=(/var/backups/hyfleet/hyfleet-server-backup-*.tar.gz.sha256)
  key=(/var/backups/hyfleet/hyfleet-server-master-key-*.key)
  key_checksum=(/var/backups/hyfleet/hyfleet-server-master-key-*.key.sha256)
  [[ ${#archive[@]} -eq 1 && ${#checksum[@]} -eq 1 && ${#key[@]} -eq 1 && ${#key_checksum[@]} -eq 1 ]]
  bash deploy/restore-server.sh \
    --archive "${archive[0]}" \
    --checksum "${checksum[0]}" \
    --master-key "${key[0]}" \
    --master-key-checksum "${key_checksum[0]}"
  systemctl is-active --quiet hyfleet-server.service
  curl --fail --silent http://127.0.0.1:8080/healthz >/dev/null
'

#!/usr/bin/env bash
set -Eeuo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repository_root="$(cd -- "${script_dir}/.." && pwd)"
manifest_path="${repository_root}/deploy/sing-box-reality.sha256"

sing_box_repository="https://github.com/SagerNet/sing-box.git"
sing_box_commit="45ca32dcb966f07f97fc888fe8586e359dbe8405"
sing_box_version="1.13.18-hyfleet-utls1.8.7-api2"
go_toolchain="go1.26.5"
utls_version="v1.8.7"
utls_sum="h1:Cp+yWkNTFkSihETgGWq34hlVFds5HpYWVOR1xovUVTs="
utls_go_mod_sum="h1:kncGGVhFaoGn5M3pFe3SXhZCzsbCJayNOH4UEqTKTko="

output_dir="${repository_root}/.codex-lab-build/sing-box-reality"
temporary_dir=""

usage() {
  cat <<'EOF'
Usage: bash scripts/build-sing-box-reality.sh [--output DIR]

Builds the two PolyFleet Reality sing-box compatibility artifacts from the pinned upstream
commit, dependency graph, Go toolchain, and linker flags. The resulting hashes
must match deploy/sing-box-reality.sha256. Existing output files are replaced
only after both artifacts pass all checks.
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
trap 'printf "ERROR: sing-box build failed at line %s.\n" "$LINENO" >&2' ERR

while (($# > 0)); do
  case "$1" in
    --output)
      (($# >= 2)) || fail "--output requires a directory"
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

for command_name in awk git go install mkdir mktemp od sed sha256sum tee tr; do
  command -v "${command_name}" >/dev/null 2>&1 || fail "required command is missing: ${command_name}"
done
[[ -f "${manifest_path}" && ! -L "${manifest_path}" ]] ||
  fail "checksum manifest is missing or is a symbolic link: ${manifest_path}"

export GOENV=off
export GOEXPERIMENT=
export GOFLAGS=
export GOTOOLCHAIN=local
export GOWORK=off
[[ "$(go env GOVERSION)" == "${go_toolchain}" ]] ||
  fail "this build requires ${go_toolchain}; found $(go env GOVERSION)"

temporary_dir="$(mktemp -d)"
source_dir="${temporary_dir}/source"
stage_dir="${temporary_dir}/stage"
export GOCACHE="${temporary_dir}/go-build-cache"
export GOMODCACHE="${temporary_dir}/go/pkg/mod"
export GOPATH="${temporary_dir}/go"
mkdir -p -- "${source_dir}" "${stage_dir}" "${GOCACHE}" "${GOMODCACHE}"

git -C "${source_dir}" init --quiet
git -C "${source_dir}" remote add origin "${sing_box_repository}"
git -C "${source_dir}" fetch --quiet --depth=1 origin "${sing_box_commit}"
git -C "${source_dir}" checkout --quiet --detach FETCH_HEAD
[[ "$(git -C "${source_dir}" rev-parse HEAD)" == "${sing_box_commit}" ]] ||
  fail "sing-box checkout does not match the pinned commit"

(
  cd -- "${source_dir}"
  go mod edit -require="github.com/metacubex/utls@${utls_version}"
  go mod download "github.com/metacubex/utls@${utls_version}"

  git apply --unidiff-zero --whitespace=error-all \
    "${repository_root}/deploy/sing-box-hyfleet-api.patch"

  [[ "$(git diff --numstat -- go.mod)" == $'1\t1\tgo.mod' &&
    "$(git diff --numstat -- go.sum)" == $'2\t0\tgo.sum' ]] ||
    fail "the dependency update does not match the reviewed go.mod/go.sum line counts"
  changed_file_count=0
  while IFS= read -r status_line; do
    case "${status_line}" in
      " M go.mod"|" M go.sum"|" M option/experimental.go"|\
      " M experimental/clashapi/server.go"|\
      " M experimental/clashapi/trafficontrol/manager.go"|\
      " M experimental/clashapi/trafficontrol/tracker.go"|\
      "?? experimental/clashapi/hyfleet.go")
        ((changed_file_count += 1))
        ;;
      *)
        fail "the sing-box source tree contains an unexpected change: ${status_line}"
        ;;
    esac
  done < <(git status --porcelain=v1 --untracked-files=all)
  [[ "${changed_file_count}" -eq 7 ]] ||
    fail "the sing-box source tree does not contain exactly the reviewed changes"

  selected_utls="$(go list -mod=readonly -m \
    -f '{{.Path}} {{.Version}} {{.Sum}} {{.GoModSum}}' github.com/metacubex/utls)"
  [[ "${selected_utls}" == "github.com/metacubex/utls ${utls_version} ${utls_sum} ${utls_go_mod_sum}" ]] ||
    fail "the selected uTLS module does not match the reviewed dependency"
  go mod verify

  for architecture in amd64 arm64; do
    artifact="sing-box-${sing_box_version}-linux-${architecture}"
    architecture_env=("GOARCH=${architecture}")
    case "${architecture}" in
      amd64)
        architecture_env+=("GOAMD64=v1")
        expected_machine="3e00"
        ;;
      arm64)
        architecture_env+=("GOARM64=v8.0")
        expected_machine="b700"
        ;;
    esac
    env \
      CGO_ENABLED=0 \
      GOOS=linux \
      "${architecture_env[@]}" \
      LC_ALL=C \
      TZ=UTC \
      go build \
        -p 1 \
        -mod=readonly \
        -trimpath \
        -buildvcs=false \
        -tags with_utls,with_clash_api \
        -ldflags "-s -w -buildid= -X github.com/sagernet/sing-box/constant.Version=${sing_box_version}" \
        -o "${stage_dir}/${artifact}" \
        ./cmd/sing-box

    elf_magic="$(od -An -t x1 -N 4 "${stage_dir}/${artifact}" | tr -d '[:space:]')"
    elf_machine="$(od -An -t x1 -j 18 -N 2 "${stage_dir}/${artifact}" | tr -d '[:space:]')"
    [[ "${elf_magic}" == "7f454c46" && "${elf_machine}" == "${expected_machine}" ]] ||
      fail "${artifact} is not the expected Linux ELF architecture"

    build_info="$(go version -m "${stage_dir}/${artifact}")"
    [[ "${build_info}" == *$'\tpath\tgithub.com/sagernet/sing-box/cmd/sing-box'* ]] ||
      fail "${artifact} has an unexpected main package"
    [[ "${build_info}" == *$'\tmod\tgithub.com/sagernet/sing-box\t(devel)'* ]] ||
      fail "${artifact} has an unexpected main module"
    [[ "${build_info}" == *$'\tdep\tgithub.com/metacubex/utls\tv1.8.7\th1:Cp+yWkNTFkSihETgGWq34hlVFds5HpYWVOR1xovUVTs='* ]] ||
      fail "${artifact} does not contain the reviewed uTLS dependency"
    [[ "${build_info}" == *$'\tbuild\tCGO_ENABLED=0'* &&
      "${build_info}" == *$'\tbuild\tGOOS=linux'* &&
      "${build_info}" == *$'\tbuild\tGOARCH='"${architecture}"* ]] ||
      fail "${artifact} has unexpected Go build settings"
    [[ "${build_info}" != *$'\tbuild\tvcs.'* ]] ||
      fail "${artifact} unexpectedly contains variable VCS build metadata"

    if [[ "$(go env GOHOSTOS)" == "linux" && "$(go env GOHOSTARCH)" == "${architecture}" ]]; then
      first_line="$("${stage_dir}/${artifact}" version | sed -n '1p')"
      [[ "${first_line}" == "sing-box version ${sing_box_version}" ]] ||
        fail "${artifact} reports an unexpected version"
    fi
  done
  go mod verify
)

actual_checksums="${temporary_dir}/actual.sha256"
: > "${actual_checksums}"
for architecture in amd64 arm64; do
  artifact="sing-box-${sing_box_version}-linux-${architecture}"
  actual_hash="$(sha256sum "${stage_dir}/${artifact}" | awk '{print $1}')"
  printf '%s  %s\n' "${actual_hash}" "${artifact}" | tee -a "${actual_checksums}"
done

for architecture in amd64 arm64; do
  artifact="sing-box-${sing_box_version}-linux-${architecture}"
  expected_hash="$(awk -v artifact="${artifact}" '
    length($1) == 64 && $1 ~ /^[0-9a-f]+$/ && $2 == artifact && NF == 2 {
      hash=$1
      matches++
    }
    END { if (matches == 1) print hash }
  ' "${manifest_path}")"
  [[ -n "${expected_hash}" ]] || fail "manifest has no unique checksum for ${artifact}"
  actual_hash="$(awk -v artifact="${artifact}" '$2 == artifact { print $1 }' "${actual_checksums}")"
  [[ "${actual_hash}" == "${expected_hash}" ]] ||
    fail "checksum mismatch for ${artifact}: expected ${expected_hash}, got ${actual_hash}"
done

mkdir -p -- "${output_dir}"
for architecture in amd64 arm64; do
  artifact="sing-box-${sing_box_version}-linux-${architecture}"
  install -m 0755 "${stage_dir}/${artifact}" "${output_dir}/${artifact}"
  sha256sum "${output_dir}/${artifact}"
done

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const realityBuildVersion = "1.13.18-hyfleet-utls1.8.7-api2"

func TestSingBoxRealitySupplyChainContract(t *testing.T) {
	repositoryRoot := filepath.Clean("..")
	read := func(path string) string {
		t.Helper()
		content, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		return string(content)
	}

	buildScript := read("scripts/build-sing-box-reality.sh")
	apiPatch := read("deploy/sing-box-hyfleet-api.patch")
	installer := read("deploy/install-agent.sh")
	updater := read("deploy/update-component.sh")
	helper := read("internal/nodeops/reality.go")
	releaseBuilder := read("scripts/build-release.ps1")
	service := read("deploy/systemd/hyfleet-sing-box-reality.service")

	for _, required := range []string{
		`sing_box_commit="45ca32dcb966f07f97fc888fe8586e359dbe8405"`,
		`go_toolchain="go1.26.5"`,
		`utls_version="v1.8.7"`,
		`-buildvcs=false`,
		`-tags with_utls`,
		`-p 1`,
		`CGO_ENABLED=0`,
		`GOOS=linux`,
		`GOMODCACHE=`,
		`GOCACHE=`,
		`chmod -R u+w`,
		`go mod verify`,
		`git apply --unidiff-zero --whitespace=error-all`,
	} {
		if !strings.Contains(buildScript, required) {
			t.Errorf("build script is missing %q", required)
		}
	}
	if strings.Contains(buildScript, "git clone --branch") || strings.Contains(buildScript, "git checkout v1.13.18") {
		t.Fatal("build script trusts a movable tag instead of the pinned commit")
	}
	if !strings.Contains(apiPatch, `if !options.HyFleetOnly {`) ||
		strings.Contains(apiPatch, "allowedOrigins = nil") {
		t.Fatal("compatibility API patch does not disable CORS fail closed")
	}
	for name, content := range map[string]string{
		"build script": buildScript,
		"installer":    installer,
		"updater":      updater,
		"helper":       helper,
	} {
		if !strings.Contains(content, realityBuildVersion) {
			t.Errorf("%s does not pin %s", name, realityBuildVersion)
		}
	}
	if !strings.Contains(installer, "sha256sum") || !strings.Contains(installer, "source_reality_checksums") {
		t.Fatal("installer does not validate the bundled Reality checksum manifest")
	}
	for _, required := range []string{
		`configured_adapter="$(awk '$1 == "adapter_type:" { print $2; exit }' "${config_path}")"`,
		`reality_source_binary="${bundle_dir}/bin/sing-box-reality"`,
		`"$(sha256sum "${reality_binary}" | awk '{print $1}')" == "${expected_reality_hash}"`,
		`reality_api_secret="$(od -An -N32 -tx1 /dev/urandom | tr -d '[:space:]')"`,
		`awk -F= '$1 != "HYFLEET_REALITY_API_SECRET" { print }'`,
		`systemctl stop "${service_name}"`,
		`systemctl stop "${reality_service_unit}"`,
		`systemctl restart "${reality_service_unit}"`,
		`systemctl restart "${service_name}"`,
		`"${reality_api_url}/hyfleet/v1/users"`,
		`/dev/tcp/127.0.0.1/${reality_listen_port}`,
		`restore_optional_reality_file reality-config.json`,
		`restore_optional_agent_file agent.env`,
	} {
		if !strings.Contains(updater, required) {
			t.Errorf("Reality updater is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`printf '%s\n' "${reality_api_secret}"`,
		`curl --header "Authorization: Bearer ${reality_api_secret}"`,
	} {
		if strings.Contains(updater, forbidden) {
			t.Errorf("Reality updater exposes its API secret through %q", forbidden)
		}
	}
	for _, digest := range []string{
		"17b2fac82abaaf51c50632f21bb64412afe899868c3c44500c3274d189134928",
		"46e52d1ccde00ef5cde7415fb01c1b103720d394d858b559ab297f07a16bbd8c",
	} {
		if !strings.Contains(helper, digest) {
			t.Errorf("helper does not pin Reality binary digest %s", digest)
		}
	}
	for _, upgradeInput := range []string{
		`reality_sing_box_legacy_version="1.13.18-hyfleet-utls1.8.7"`,
		`reality_sing_box_api1_version="1.13.18-hyfleet-utls1.8.7-api1"`,
		"759f7a7acfdd32517851ec3b68fb19bc211a41c5d40b2610b7693b2a41b55f33",
		"2483f6f8c8f2ad91db7278ed09b4c0f505074f39ab9a3d4843b87cf93261498f",
		"a99679a7ebc4e4f4b21af5aa5db23eb3149c3abfdbde516d97718ce3920586d7",
		"52f3c8a71317b51996c3d0f3a42f3ffdea747352a8655d848415bad3c8253f0c",
		`fail "installed Reality binary checksum is not an approved upgrade input"`,
		`fail "installed Reality binary version does not match its approved checksum"`,
		`fail "installed Reality binary version changed during update"`,
	} {
		if !strings.Contains(updater, upgradeInput) {
			t.Errorf("Reality updater is missing approved legacy input guard %q", upgradeInput)
		}
	}
	if !strings.Contains(installer, `-perm /022`) {
		t.Fatal("installer does not reject a group- or world-writable Reality binary")
	}
	if !strings.Contains(installer, "refusing to adopt existing unmanaged Reality configuration") ||
		!strings.Contains(installer, `"${reality_identity}" "${reality_applied}"`) {
		t.Fatal("installer does not reject an existing Reality config without managed local state")
	}
	if strings.Contains(installer, `chown root:hyfleet-singbox "${reality_core_config}"`) ||
		strings.Contains(installer, `chmod 0640 "${reality_core_config}"`) {
		t.Fatal("installer silently normalizes and adopts an existing Reality configuration")
	}
	for _, required := range []string{
		`if mkdir -m 0750 /etc/sing-box 2>/dev/null; then`,
		`reality_config_dir_identity="$(stat -c '%d:%i' /etc/sing-box)"`,
		`"$(stat -c '%u:%g' /etc/sing-box)" == "0:${reality_group_id}"`,
		`find /etc/sing-box -maxdepth 0 -perm /022`,
		`runuser -u hyfleet-singbox -- test -x /etc/sing-box`,
	} {
		if !strings.Contains(installer, required) {
			t.Errorf("installer does not preserve an existing shared config directory; missing %q", required)
		}
	}
	if strings.Contains(installer, "install -d -o root -g hyfleet-singbox -m 0750 /etc/sing-box") {
		t.Fatal("installer must use atomic mkdir to distinguish a new config directory")
	}
	if !strings.Contains(releaseBuilder, `"sing-box-reality.sha256"`) {
		t.Fatal("release builder does not bundle the Reality checksum manifest")
	}
	if !strings.Contains(service, "ExecStart=/usr/bin/sing-box") ||
		!strings.Contains(service, "ExecStartPre=/usr/bin/test ! -L /usr/bin/sing-box") {
		t.Fatal("Reality service does not retain the fixed non-symlink binary contract")
	}
}

func TestSingBoxRealityManifest(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "deploy", "sing-box-reality.sha256"))
	if err != nil {
		t.Fatalf("ReadFile(manifest) error = %v", err)
	}
	linePattern := regexp.MustCompile(`^[0-9a-f]{64}  sing-box-` + regexp.QuoteMeta(realityBuildVersion) + `-linux-(amd64|arm64)$`)
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		line = strings.TrimSuffix(line, "\r")
		matches := linePattern.FindStringSubmatch(line)
		if matches == nil || seen[matches[1]] {
			t.Fatalf("invalid or duplicate manifest line: %q", line)
		}
		if strings.HasPrefix(line, strings.Repeat("0", 64)) {
			t.Fatalf("manifest retains a placeholder checksum: %q", line)
		}
		seen[matches[1]] = true
	}
	if !seen["amd64"] || !seen["arm64"] || len(seen) != 2 {
		t.Fatalf("manifest architectures = %v, want amd64 and arm64", seen)
	}
}

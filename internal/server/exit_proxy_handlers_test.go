package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Sen62455/PolyFleet/internal/store"
)

func TestExitProxyCatalogLandingGuideAndNodeAssignment(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)

	created := app.request(t, http.MethodPost, "/api/v1/exit-proxies", map[string]any{
		"name": "UK landing", "kind": "landing_vps", "protocol": "socks5",
		"host": "203.0.113.9", "port": 1080, "udp": true,
	}, app.csrf, "http://hyfleet.test")
	requireStatus(t, created, http.StatusCreated)
	var proxy exitProxyResponse
	decodeResponse(t, created, &proxy)
	if proxy.ID == "" || !proxy.PasswordSet || proxy.Username != "polyfleet" {
		t.Fatalf("created proxy = %#v", proxy)
	}
	if strings.Contains(created.Body.String(), `"password":`) {
		t.Fatalf("create payload should not include password field: %s", created.Body.String())
	}

	guide := app.request(t, http.MethodGet, "/api/v1/exit-proxies/"+proxy.ID+"/landing-guide", nil, "", "http://hyfleet.test")
	requireStatus(t, guide, http.StatusOK)
	var landing landingGuideResponse
	decodeResponse(t, guide, &landing)
	if landing.Username != "polyfleet" || len(landing.Steps) < 4 ||
		!strings.Contains(landing.Steps[3].Copy, "polyfleet-landing.service") ||
		!strings.Contains(landing.Steps[1].Copy, `"listen_port": 1080`) {
		t.Fatalf("landing guide = %#v", landing)
	}

	nodeResp := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "LA entry", "adapter_type": "native_hysteria2",
		"public_host": "hy2.example.com", "public_port": 443,
		"exit_proxy_id": proxy.ID,
	}, app.csrf, "http://hyfleet.test")
	requireStatus(t, nodeResp, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, nodeResp, &node)
	if node.ExitProxyID != proxy.ID || node.ExitProxy == nil || node.ExitProxy.Name != "UK landing" {
		t.Fatalf("node exit assignment = %#v", node)
	}

	listed := app.request(t, http.MethodGet, "/api/v1/exit-proxies", nil, "", "http://hyfleet.test")
	requireStatus(t, listed, http.StatusOK)
	var catalog struct {
		ExitProxies []exitProxyResponse `json:"exit_proxies"`
	}
	decodeResponse(t, listed, &catalog)
	if len(catalog.ExitProxies) != 1 || len(catalog.ExitProxies[0].Assignments) != 1 ||
		catalog.ExitProxies[0].Assignments[0].NodeName != "LA entry" {
		encoded, _ := json.Marshal(catalog)
		t.Fatalf("catalog = %s", encoded)
	}

	cleared := app.request(t, http.MethodPut, "/api/v1/exit-proxies/"+proxy.ID+"/nodes", map[string]any{
		"node_ids": []string{},
	}, app.csrf, "http://hyfleet.test")
	requireStatus(t, cleared, http.StatusOK)
	decodeResponse(t, cleared, &proxy)
	if len(proxy.Assignments) != 0 {
		t.Fatalf("cleared assignments = %#v", proxy)
	}
}

func TestLandingGuideCommandsAndCredentials(t *testing.T) {
	proxy := store.ExitProxy{Name: "Landing", Host: "2001:db8::2", Port: 1080, Protocol: store.ExitProxyProtocolSOCKS5}
	password := "p'\"$`\\:\n复杂"
	guide := buildLandingGuide(proxy, password)
	if len(guide.Steps) != 8 || !strings.Contains(guide.Steps[2].Copy, "exit 1") || strings.Contains(guide.Steps[2].Copy, "nft -f") {
		t.Fatal("unbound guide must stop before exposing proxy")
	}
	for _, step := range guide.Steps {
		if step.Where == "" || step.Expected == "" {
			t.Fatalf("missing novice instructions: %s", step.Title)
		}
	}
	if !strings.Contains(guide.Steps[0].Copy, "sha256sum -c") || !strings.Contains(guide.Steps[3].Copy, "Requires=polyfleet-landing-firewall.service") {
		t.Fatal("missing install verification or firewall dependency")
	}
	var config struct {
		Inbounds []struct{ Users []struct{ Password string } }
	}
	if err := json.Unmarshal([]byte(landingSingBoxConfig(proxy, "polyfleet", password)), &config); err != nil {
		t.Fatal(err)
	}
	if config.Inbounds[0].Users[0].Password != password {
		t.Fatal("credentials altered in generated JSON")
	}
	verify := guide.Steps[5].Copy
	if !strings.Contains(verify, "socks5h://[2001:db8::2]:1080") || !strings.Contains(verify, "<<'POLYFLEET_CURL'") || strings.Contains(verify, "p'\"$`\\:\n") {
		t.Fatal("unsafe curl credential quoting")
	}
	firewall := landingFirewallScript(1080, []string{"entry.example.com", "2001:db8::1"})
	for _, required := range []string{"socket.getaddrinfo", "ip6", "ip_local_port_range", "nft', '-c'", "systemctl enable --now polyfleet-landing-firewall"} {
		if !strings.Contains(firewall, required) {
			t.Fatalf("firewall missing %s", required)
		}
	}
	if strings.Contains(firewall, "flush ruleset") {
		t.Fatal("must preserve other firewall rules")
	}
}

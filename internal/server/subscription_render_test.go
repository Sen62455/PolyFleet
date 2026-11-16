package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/Sen62455/PolyFleet/internal/store"
	"go.yaml.in/yaml/v3"
)

func TestServerSideExitExportsOnlyEntryAndIncludesHandshakeTiming(t *testing.T) {
	s := store.Subscription{Endpoints: []store.SubscriptionEndpoint{{
		NodeName: "entry", Protocol: "hysteria2", PublicHost: "entry.example.com", PublicPort: 443, Credential: "user-secret",
		Exit: &store.SubscriptionExitProxy{Name: "landing", Host: "private-landing.example.com", Port: 1080, Password: "never-export-this", ServerSide: true},
	}}}
	for _, format := range []string{"clash", "sing-box", "uri", "base64"} {
		r, err := renderSubscription(format, s)
		if err != nil {
			t.Fatal(err)
		}
		body := r.Body
		if format == "base64" {
			body, err = base64.StdEncoding.DecodeString(string(body))
			if err != nil {
				t.Fatal(err)
			}
		}
		if bytes.Contains(body, []byte("never-export-this")) || bytes.Contains(body, []byte("private-landing")) || bytes.Contains(body, []byte("dialer-proxy")) || bytes.Contains(body, []byte("detour")) {
			t.Fatalf("%s exported a client chain: %s", format, body)
		}
		if format == "clash" {
			var v struct {
				Unified bool              `yaml:"unified-delay"`
				Proxies []map[string]any  `yaml:"proxies"`
				Groups  []clashProxyGroup `yaml:"proxy-groups"`
			}
			if err := yaml.Unmarshal(body, &v); err != nil {
				t.Fatal(err)
			}
			if v.Unified || len(v.Proxies) != 1 || v.Proxies[0]["name"] != "entry · landing" || v.Proxies[0]["type"] != "hysteria2" || v.Groups[0].Proxies[1] != "entry · landing" {
				t.Fatalf("invalid server-forwarded config: %s", body)
			}
		}
	}
	if s.Endpoints[0].Exit == nil || s.Endpoints[0].NodeName != "entry" {
		t.Fatal("renderer mutated caller subscription")
	}
}

func TestSubscriptionRenderersEscapeStructuredValues(t *testing.T) {
	certificateFingerprint := strings.TrimSuffix(strings.Repeat("AB:", 32), ":")
	publicKeyPin := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	subscription := store.Subscription{Endpoints: []store.SubscriptionEndpoint{
		{
			NodeID: "node-one", NodeName: "IPv6 / Tokyo #1", PublicHost: "2001:db8::1",
			PublicPort: 8443, SNI: "edge.example.com", TLSInsecure: true,
			TLSCertFingerprint: certificateFingerprint, TLSPublicKeySHA256: publicKeyPin,
			Credential: "user:p@ss/?# value",
		},
	}}

	uriDocument, err := renderSubscription("uri", subscription)
	if err != nil {
		t.Fatalf("renderSubscription(uri) error = %v", err)
	}
	parsed, err := url.Parse(string(uriDocument.Body))
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if parsed.Scheme != "hysteria2" || parsed.Hostname() != "2001:db8::1" || parsed.Port() != "8443" ||
		parsed.User.Username() != "user:p@ss/?# value" || parsed.Fragment != "IPv6 / Tokyo #1" ||
		parsed.Query().Get("sni") != "edge.example.com" || parsed.Query().Get("insecure") != "1" ||
		parsed.Query().Get("pinSHA256") != certificateFingerprint {
		t.Fatalf("unexpected Hysteria2 URI: %s", uriDocument.Body)
	}

	encoded, err := renderSubscription("base64", subscription)
	if err != nil {
		t.Fatalf("renderSubscription(base64) error = %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(encoded.Body))
	if err != nil || string(decoded) != string(uriDocument.Body) {
		t.Fatalf("base64 decoded = %q, error = %v", decoded, err)
	}

	clash, err := renderSubscription("clash", subscription)
	if err != nil {
		t.Fatalf("renderSubscription(clash) error = %v", err)
	}
	var clashValue struct {
		Mode          string `yaml:"mode"`
		IPv6          bool   `yaml:"ipv6"`
		TCPConcurrent bool   `yaml:"tcp-concurrent"`
		DNS           struct {
			Enable                bool                `yaml:"enable"`
			EnhancedMode          string              `yaml:"enhanced-mode"`
			Nameserver            []string            `yaml:"nameserver"`
			NameserverPolicy      map[string][]string `yaml:"nameserver-policy"`
			ProxyServerNameserver []string            `yaml:"proxy-server-nameserver"`
		} `yaml:"dns"`
		Sniffer struct {
			Enable bool `yaml:"enable"`
		} `yaml:"sniffer"`
		Tun struct {
			Enable    bool     `yaml:"enable"`
			Stack     string   `yaml:"stack"`
			DNSHijack []string `yaml:"dns-hijack"`
		} `yaml:"tun"`
		Proxies     []map[string]any `yaml:"proxies"`
		ProxyGroups []struct {
			Name    string   `yaml:"name"`
			Type    string   `yaml:"type"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(clash.Body, &clashValue); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v; body = %s", err, clash.Body)
	}
	if len(clashValue.Proxies) != 1 || clashValue.Proxies[0]["password"] != "user:p@ss/?# value" ||
		clashValue.Proxies[0]["server"] != "2001:db8::1" || clashValue.Proxies[0]["skip-cert-verify"] != true ||
		clashValue.Proxies[0]["fingerprint"] != certificateFingerprint || clashValue.Proxies[0]["udp"] != true {
		t.Fatalf("unexpected Clash subscription: %#v", clashValue)
	}
	rules := strings.Join(clashValue.Rules, "\n")
	if clashValue.Mode != "rule" || clashValue.IPv6 || !clashValue.TCPConcurrent ||
		!clashValue.DNS.Enable || clashValue.DNS.EnhancedMode != "fake-ip" || len(clashValue.DNS.Nameserver) != 2 ||
		!strings.HasSuffix(clashValue.DNS.Nameserver[0], "#PolyFleet") ||
		!strings.HasSuffix(clashValue.DNS.Nameserver[1], "#PolyFleet") ||
		len(clashValue.DNS.NameserverPolicy["+.cn"]) != 2 ||
		len(clashValue.DNS.NameserverPolicy["+.qq.com"]) != 2 ||
		len(clashValue.DNS.ProxyServerNameserver) != 2 ||
		!clashValue.Sniffer.Enable || clashValue.Tun.Enable || clashValue.Tun.Stack != "mixed" ||
		len(clashValue.Tun.DNSHijack) != 2 ||
		len(clashValue.ProxyGroups) != 2 || clashValue.ProxyGroups[0].Name != "PolyFleet" ||
		clashValue.ProxyGroups[0].Type != "select" || len(clashValue.ProxyGroups[0].Proxies) != 3 ||
		clashValue.ProxyGroups[0].Proxies[0] != "自动选择" ||
		clashValue.ProxyGroups[0].Proxies[1] != "IPv6 / Tokyo #1" ||
		clashValue.ProxyGroups[0].Proxies[2] != "DIRECT" ||
		clashValue.ProxyGroups[1].Name != "自动选择" || clashValue.ProxyGroups[1].Type != "url-test" ||
		len(clashValue.Rules) < 3 || clashValue.Rules[0] != "DOMAIN,localhost,DIRECT" ||
		!strings.Contains(rules, "DOMAIN-SUFFIX,cn,DIRECT") ||
		!strings.Contains(rules, "DOMAIN-SUFFIX,qq.com,DIRECT") ||
		strings.Contains(rules, "GEOSITE,") || strings.Contains(rules, "GEOIP,") ||
		strings.Contains(string(clash.Body), "fallback:") ||
		strings.Contains(rules, "198.18.0.0/16") ||
		clashValue.Rules[len(clashValue.Rules)-1] != "MATCH,PolyFleet" {
		t.Fatalf("Clash rule-mode configuration is incomplete: %#v", clashValue)
	}

	singBox, err := renderSubscription("sing-box", subscription)
	if err != nil {
		t.Fatalf("renderSubscription(sing-box) error = %v", err)
	}
	var singBoxValue struct {
		Outbounds []struct {
			Tag      string `json:"tag"`
			Password string `json:"password"`
			TLS      struct {
				Enabled                    bool     `json:"enabled"`
				ServerName                 string   `json:"server_name"`
				Insecure                   bool     `json:"insecure"`
				CertificatePublicKeySHA256 []string `json:"certificate_public_key_sha256"`
			} `json:"tls"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(singBox.Body, &singBoxValue); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body = %s", err, singBox.Body)
	}
	if len(singBoxValue.Outbounds) != 1 || singBoxValue.Outbounds[0].Tag != "IPv6 / Tokyo #1" ||
		singBoxValue.Outbounds[0].Password != "user:p@ss/?# value" ||
		!singBoxValue.Outbounds[0].TLS.Enabled || !singBoxValue.Outbounds[0].TLS.Insecure ||
		singBoxValue.Outbounds[0].TLS.ServerName != "edge.example.com" ||
		len(singBoxValue.Outbounds[0].TLS.CertificatePublicKeySHA256) != 1 ||
		singBoxValue.Outbounds[0].TLS.CertificatePublicKeySHA256[0] != publicKeyPin {
		t.Fatalf("unexpected sing-box subscription: %#v", singBoxValue)
	}
}

func TestClashDomesticRoutingCoversMediaAndKeepsDNSAligned(t *testing.T) {
	rendered, err := renderSubscription("clash", store.Subscription{})
	if err != nil {
		t.Fatal(err)
	}
	var config clashSubscription
	if err := yaml.Unmarshal(rendered.Body, &config); err != nil {
		t.Fatal(err)
	}
	ruleIndex := make(map[string]int)
	for i, rule := range config.Rules {
		ruleIndex[rule] = i
	}
	for _, suffix := range []string{
		"douyinvod.com", "douyincdn.com", "douyinpic.com", "douyinstatic.com",
		"douyinliving.com", "idouyinvod.com", "idouyinliving.com",
		"bytegoofy.com", "bytedance.net", "volces.com",
		"126.net", "127.net", "cloudflare-cn.com",
	} {
		index, ok := ruleIndex["DOMAIN-SUFFIX,"+suffix+",DIRECT"]
		if !ok || index >= ruleIndex["RULE-SET,polyfleet-cn-domain,DIRECT"] {
			t.Errorf("%s must have an offline direct rule before downloaded providers", suffix)
		}
		if got := config.DNS.NameserverPolicy["+."+suffix]; strings.Join(got, ",") != strings.Join(config.DNS.DirectNameserver, ",") {
			t.Errorf("%s DNS must use the domestic direct resolvers", suffix)
		}
	}
	for _, suffix := range []string{"tiktok.com", "tiktokcdn.com", "byteoversea.com", "cloudflare.com", "chatgpt.com"} {
		if _, ok := ruleIndex["DOMAIN-SUFFIX,"+suffix+",DIRECT"]; ok {
			t.Errorf("international service %s must not be in the domestic fallback", suffix)
		}
	}
	if len(config.RuleProviders) != 2 || len(config.DNS.DirectNameserver) != 2 || !config.DNS.DirectNameserverFollowPolicy {
		t.Fatal("missing China rule providers or direct DNS policy")
	}
	for name, behavior := range map[string]string{"polyfleet-cn-domain": "domain", "polyfleet-cn-ip": "ipcidr"} {
		provider, ok := config.RuleProviders[name]
		parsed, err := url.Parse(provider.URL)
		if !ok || err != nil || parsed.Scheme != "https" || parsed.Host != "raw.githubusercontent.com" ||
			provider.Type != "http" || provider.Behavior != behavior || provider.Format != "mrs" ||
			provider.Proxy != "PolyFleet" || provider.Interval != 86400 || provider.Path == "" {
			t.Errorf("invalid updateable %s provider: %#v", name, provider)
		}
	}
	if strings.Join(config.DNS.NameserverPolicy["rule-set:polyfleet-cn-domain"], ",") != strings.Join(config.DNS.DirectNameserver, ",") {
		t.Fatal("downloaded domestic domains must use the same DNS as offline domains")
	}
	end := config.Rules[len(config.Rules)-3:]
	if strings.Join(end, "\n") != "RULE-SET,polyfleet-cn-domain,DIRECT\nRULE-SET,polyfleet-cn-ip,DIRECT\nMATCH,PolyFleet" {
		t.Fatalf("invalid domain/IP/final rule ordering: %v", end)
	}
}

func TestSubscriptionRenderersChainExitProxiesThroughEntryNodes(t *testing.T) {
	subscription := store.Subscription{Endpoints: []store.SubscriptionEndpoint{
		{
			NodeID: "node-hy2", NodeName: "LA entry", Protocol: "hysteria2",
			PublicHost: "hy2.example.com", PublicPort: 443, SNI: "hy2.example.com",
			Credential: "hy2-password",
			Exit: &store.SubscriptionExitProxy{
				Name: "UK landing", Kind: store.ExitProxyKindLandingVPS,
				Protocol: store.ExitProxyProtocolSOCKS5, Host: "203.0.113.9", Port: 1080,
				Username: "polyfleet", Password: "landing-secret", UDP: true,
			},
		},
		{
			NodeID: "node-direct", NodeName: "Direct node", Protocol: "hysteria2",
			PublicHost: "direct.example.com", PublicPort: 443, Credential: "direct-password",
		},
	}}
	clash, err := renderSubscription("clash", subscription)
	if err != nil {
		t.Fatalf("renderSubscription(clash) error = %v", err)
	}
	var clashValue struct {
		Proxies     []map[string]any `yaml:"proxies"`
		ProxyGroups []struct {
			Name    string   `yaml:"name"`
			Proxies []string `yaml:"proxies"`
		} `yaml:"proxy-groups"`
	}
	if err := yaml.Unmarshal(clash.Body, &clashValue); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v; body = %s", err, clash.Body)
	}
	if len(clashValue.Proxies) != 3 {
		t.Fatalf("proxies = %#v", clashValue.Proxies)
	}
	var foundChain bool
	for _, proxy := range clashValue.Proxies {
		if proxy["name"] == "LA entry · UK landing" {
			foundChain = true
			if proxy["type"] != "socks5" || proxy["server"] != "203.0.113.9" ||
				proxy["dialer-proxy"] != "LA entry" || proxy["password"] != "landing-secret" {
				t.Fatalf("chained proxy = %#v", proxy)
			}
		}
	}
	if !foundChain || clashValue.ProxyGroups[0].Proxies[1] != "LA entry · UK landing" ||
		clashValue.ProxyGroups[0].Proxies[2] != "Direct node" {
		t.Fatalf("clash groups = %#v", clashValue.ProxyGroups)
	}

	singBox, err := renderSubscription("sing-box", subscription)
	if err != nil {
		t.Fatalf("renderSubscription(sing-box) error = %v", err)
	}
	var singBoxValue struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(singBox.Body, &singBoxValue); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body = %s", err, singBox.Body)
	}
	if len(singBoxValue.Outbounds) != 3 ||
		singBoxValue.Outbounds[0]["tag"] != "LA entry · UK landing" ||
		singBoxValue.Outbounds[0]["detour"] != "LA entry" ||
		singBoxValue.Outbounds[0]["type"] != "socks" {
		t.Fatalf("sing-box outbounds = %#v", singBoxValue.Outbounds)
	}
}

func TestSubscriptionRenderersProduceValidEmptyDocuments(t *testing.T) {
	empty := store.Subscription{Endpoints: []store.SubscriptionEndpoint{}}
	for _, format := range []string{"uri", "base64"} {
		rendered, err := renderSubscription(format, empty)
		if err != nil || len(rendered.Body) != 0 {
			t.Fatalf("renderSubscription(%s) = %q, error = %v", format, rendered.Body, err)
		}
	}
	clash, err := renderSubscription("clash", empty)
	if err != nil || !strings.Contains(string(clash.Body), "proxies: []") ||
		!strings.Contains(string(clash.Body), "- DIRECT") ||
		!strings.Contains(string(clash.Body), "- MATCH,PolyFleet") {
		t.Fatalf("empty Clash = %q, error = %v", clash.Body, err)
	}
	singBox, err := renderSubscription("sing-box", empty)
	if err != nil || !strings.Contains(string(singBox.Body), `"outbounds": []`) {
		t.Fatalf("empty sing-box = %q, error = %v", singBox.Body, err)
	}
}

func TestSubscriptionRenderersSupportMixedHysteria2AndVLESSReality(t *testing.T) {
	subscription := store.Subscription{Endpoints: []store.SubscriptionEndpoint{
		{
			NodeID: "node-hy2", NodeName: "Hysteria node", Protocol: "hysteria2",
			PublicHost: "hy2.example.com", PublicPort: 443, SNI: "hy2.example.com",
			Credential: "hy2-password",
		},
		{
			NodeID: "node-vless", NodeName: "Reality / Tokyo #1", Protocol: "vless",
			PublicHost: "2001:db8::2", PublicPort: 24443, SNI: "www.microsoft.com",
			Credential: "9c33cc92-54e0-427e-8214-a45e62c05e11",
			Flow:       "xtls-rprx-vision", Network: "tcp",
			RealityPublicKey: "RAjjVUXRxxNkVHMbFpJTPIq8V8kV9cYvf3qj6M7iCUQ",
			RealityShortID:   "25f5c1b8109c9f62",
		},
	}}

	uriDocument, err := renderSubscription("uri", subscription)
	if err != nil {
		t.Fatalf("renderSubscription(uri) error = %v", err)
	}
	lines := strings.Split(string(uriDocument.Body), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "hysteria2://") ||
		!strings.HasPrefix(lines[1], "vless://") {
		t.Fatalf("mixed URI document = %q", uriDocument.Body)
	}
	vlessURI, err := url.Parse(lines[1])
	if err != nil {
		t.Fatalf("url.Parse(VLESS) error = %v", err)
	}
	query := vlessURI.Query()
	if vlessURI.Hostname() != "2001:db8::2" || vlessURI.Port() != "24443" ||
		vlessURI.User.Username() != "9c33cc92-54e0-427e-8214-a45e62c05e11" ||
		vlessURI.Fragment != "Reality / Tokyo #1" || query.Get("encryption") != "none" ||
		query.Get("flow") != "xtls-rprx-vision" || query.Get("security") != "reality" ||
		query.Get("sni") != "www.microsoft.com" ||
		query.Get("pbk") != "RAjjVUXRxxNkVHMbFpJTPIq8V8kV9cYvf3qj6M7iCUQ" ||
		query.Get("sid") != "25f5c1b8109c9f62" || query.Get("type") != "tcp" {
		t.Fatalf("unexpected VLESS URI: %s", lines[1])
	}

	encoded, err := renderSubscription("base64", subscription)
	if err != nil {
		t.Fatalf("renderSubscription(base64) error = %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(encoded.Body))
	if err != nil || string(decoded) != string(uriDocument.Body) {
		t.Fatalf("mixed base64 decoded = %q, error = %v", decoded, err)
	}

	clash, err := renderSubscription("clash", subscription)
	if err != nil {
		t.Fatalf("renderSubscription(clash) error = %v", err)
	}
	var clashValue struct {
		Proxies []map[string]any `yaml:"proxies"`
	}
	if err := yaml.Unmarshal(clash.Body, &clashValue); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v; body = %s", err, clash.Body)
	}
	if len(clashValue.Proxies) != 2 || clashValue.Proxies[0]["type"] != "hysteria2" ||
		clashValue.Proxies[0]["udp"] != true ||
		clashValue.Proxies[1]["type"] != "vless" ||
		clashValue.Proxies[1]["uuid"] != "9c33cc92-54e0-427e-8214-a45e62c05e11" ||
		clashValue.Proxies[1]["network"] != "tcp" || clashValue.Proxies[1]["tls"] != true ||
		clashValue.Proxies[1]["flow"] != "xtls-rprx-vision" ||
		clashValue.Proxies[1]["client-fingerprint"] != "chrome" ||
		clashValue.Proxies[1]["udp"] != true || clashValue.Proxies[1]["packet-encoding"] != "xudp" {
		t.Fatalf("unexpected mixed Clash subscription: %#v", clashValue.Proxies)
	}
	realityOptions, ok := clashValue.Proxies[1]["reality-opts"].(map[string]any)
	if !ok || realityOptions["public-key"] != "RAjjVUXRxxNkVHMbFpJTPIq8V8kV9cYvf3qj6M7iCUQ" ||
		realityOptions["short-id"] != "25f5c1b8109c9f62" {
		t.Fatalf("unexpected Clash Reality options: %#v", clashValue.Proxies[1]["reality-opts"])
	}

	singBox, err := renderSubscription("sing-box", subscription)
	if err != nil {
		t.Fatalf("renderSubscription(sing-box) error = %v", err)
	}
	var singBoxValue struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(singBox.Body, &singBoxValue); err != nil {
		t.Fatalf("json.Unmarshal() error = %v; body = %s", err, singBox.Body)
	}
	if len(singBoxValue.Outbounds) != 2 || singBoxValue.Outbounds[0]["type"] != "hysteria2" ||
		singBoxValue.Outbounds[1]["type"] != "vless" ||
		singBoxValue.Outbounds[1]["uuid"] != "9c33cc92-54e0-427e-8214-a45e62c05e11" ||
		singBoxValue.Outbounds[1]["flow"] != "xtls-rprx-vision" ||
		singBoxValue.Outbounds[1]["network"] != "tcp" {
		t.Fatalf("unexpected mixed sing-box subscription: %#v", singBoxValue.Outbounds)
	}
	vlessTLS, ok := singBoxValue.Outbounds[1]["tls"].(map[string]any)
	if !ok || vlessTLS["enabled"] != true || vlessTLS["server_name"] != "www.microsoft.com" {
		t.Fatalf("unexpected sing-box VLESS TLS: %#v", singBoxValue.Outbounds[1]["tls"])
	}
	reality, ok := vlessTLS["reality"].(map[string]any)
	if !ok || reality["enabled"] != true ||
		reality["public_key"] != "RAjjVUXRxxNkVHMbFpJTPIq8V8kV9cYvf3qj6M7iCUQ" ||
		reality["short_id"] != "25f5c1b8109c9f62" {
		t.Fatalf("unexpected sing-box Reality TLS: %#v", vlessTLS["reality"])
	}
	utls, ok := vlessTLS["utls"].(map[string]any)
	if !ok || utls["enabled"] != true || utls["fingerprint"] != "chrome" {
		t.Fatalf("unexpected sing-box uTLS: %#v", vlessTLS["utls"])
	}
}

func TestSubscriptionRenderersRejectUnknownProtocol(t *testing.T) {
	subscription := store.Subscription{Endpoints: []store.SubscriptionEndpoint{{
		NodeName: "unknown", Protocol: "future-protocol",
	}}}
	for _, format := range []string{"uri", "base64", "clash", "sing-box"} {
		if _, err := renderSubscription(format, subscription); err != store.ErrUnsupported {
			t.Fatalf("renderSubscription(%s) error = %v, want ErrUnsupported", format, err)
		}
	}
}

package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/Sen62455/PolyFleet/internal/store"
	"go.yaml.in/yaml/v3"
)

type renderedSubscription struct {
	ContentType string
	Body        []byte
}

type clashSubscription struct {
	AllowLAN      bool              `yaml:"allow-lan"`
	Mode          string            `yaml:"mode"`
	LogLevel      string            `yaml:"log-level"`
	IPv6          bool              `yaml:"ipv6"`
	UnifiedDelay  bool              `yaml:"unified-delay"`
	TCPConcurrent bool              `yaml:"tcp-concurrent"`
	Profile       clashProfile      `yaml:"profile"`
	DNS           clashDNS          `yaml:"dns"`
	Sniffer       clashSniffer      `yaml:"sniffer"`
	Tun           clashTun          `yaml:"tun"`
	Proxies       []any             `yaml:"proxies"`
	ProxyGroups   []clashProxyGroup `yaml:"proxy-groups"`
	Rules         []string          `yaml:"rules"`
}

type clashProxyGroup struct {
	Name      string   `yaml:"name"`
	Type      string   `yaml:"type"`
	Proxies   []string `yaml:"proxies"`
	URL       string   `yaml:"url,omitempty"`
	Interval  int      `yaml:"interval,omitempty"`
	Tolerance int      `yaml:"tolerance,omitempty"`
	Lazy      bool     `yaml:"lazy,omitempty"`
}

type clashProfile struct {
	StoreSelected bool `yaml:"store-selected"`
	StoreFakeIP   bool `yaml:"store-fake-ip"`
}

type clashDNS struct {
	Enable                bool                `yaml:"enable"`
	IPv6                  bool                `yaml:"ipv6"`
	EnhancedMode          string              `yaml:"enhanced-mode"`
	FakeIPRange           string              `yaml:"fake-ip-range"`
	FakeIPFilterMode      string              `yaml:"fake-ip-filter-mode"`
	FakeIPFilter          []string            `yaml:"fake-ip-filter"`
	DefaultNameserver     []string            `yaml:"default-nameserver"`
	Nameserver            []string            `yaml:"nameserver"`
	NameserverPolicy      map[string][]string `yaml:"nameserver-policy"`
	ProxyServerNameserver []string            `yaml:"proxy-server-nameserver"`
}

type clashSniffer struct {
	Enable          bool                `yaml:"enable"`
	ForceDNSMapping bool                `yaml:"force-dns-mapping"`
	ParsePureIP     bool                `yaml:"parse-pure-ip"`
	Sniff           clashSniffProtocols `yaml:"sniff"`
	SkipDomain      []string            `yaml:"skip-domain"`
}

type clashSniffProtocols struct {
	HTTP clashSniffProtocol `yaml:"HTTP"`
	TLS  clashSniffProtocol `yaml:"TLS"`
	QUIC clashSniffProtocol `yaml:"QUIC"`
}

type clashSniffProtocol struct {
	Ports               []any `yaml:"ports"`
	OverrideDestination bool  `yaml:"override-destination,omitempty"`
}

type clashTun struct {
	Enable              bool     `yaml:"enable"`
	Stack               string   `yaml:"stack"`
	DNSHijack           []string `yaml:"dns-hijack"`
	AutoRoute           bool     `yaml:"auto-route"`
	AutoRedirect        bool     `yaml:"auto-redirect"`
	AutoDetectInterface bool     `yaml:"auto-detect-interface"`
	StrictRoute         bool     `yaml:"strict-route"`
}

type clashHysteria2Proxy struct {
	Name           string `yaml:"name"`
	Type           string `yaml:"type"`
	Server         string `yaml:"server"`
	Port           int    `yaml:"port"`
	Password       string `yaml:"password"`
	SNI            string `yaml:"sni,omitempty"`
	SkipCertVerify bool   `yaml:"skip-cert-verify"`
	Fingerprint    string `yaml:"fingerprint,omitempty"`
	UDP            bool   `yaml:"udp"`
}

type clashVLESSRealityProxy struct {
	Name              string                   `yaml:"name"`
	Type              string                   `yaml:"type"`
	Server            string                   `yaml:"server"`
	Port              int                      `yaml:"port"`
	UUID              string                   `yaml:"uuid"`
	Network           string                   `yaml:"network"`
	TLS               bool                     `yaml:"tls"`
	ServerName        string                   `yaml:"servername"`
	Flow              string                   `yaml:"flow"`
	ClientFingerprint string                   `yaml:"client-fingerprint"`
	RealityOptions    clashVLESSRealityOptions `yaml:"reality-opts"`
	UDP               bool                     `yaml:"udp"`
	PacketEncoding    string                   `yaml:"packet-encoding"`
}

type clashVLESSRealityOptions struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id"`
}

type singBoxSubscription struct {
	Outbounds []any `json:"outbounds"`
}

type singBoxHysteria2Outbound struct {
	Type       string         `json:"type"`
	Tag        string         `json:"tag"`
	Server     string         `json:"server"`
	ServerPort int            `json:"server_port"`
	Password   string         `json:"password"`
	TLS        singBoxTLSData `json:"tls"`
}

type singBoxTLSData struct {
	Enabled                    bool     `json:"enabled"`
	ServerName                 string   `json:"server_name,omitempty"`
	Insecure                   bool     `json:"insecure"`
	CertificatePublicKeySHA256 []string `json:"certificate_public_key_sha256,omitempty"`
}

type singBoxVLESSRealityOutbound struct {
	Type       string                 `json:"type"`
	Tag        string                 `json:"tag"`
	Server     string                 `json:"server"`
	ServerPort int                    `json:"server_port"`
	UUID       string                 `json:"uuid"`
	Flow       string                 `json:"flow"`
	Network    string                 `json:"network"`
	TLS        singBoxVLESSRealityTLS `json:"tls"`
}

type singBoxVLESSRealityTLS struct {
	Enabled    bool                    `json:"enabled"`
	ServerName string                  `json:"server_name"`
	UTLS       singBoxUTLS             `json:"utls"`
	Reality    singBoxRealityClientTLS `json:"reality"`
}

type singBoxUTLS struct {
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint"`
}

type singBoxRealityClientTLS struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key"`
	ShortID   string `json:"short_id"`
}

var defaultClashDomesticDomainSuffixes = []string{
	"cn",
	"10010.com", "10086.cn", "12306.cn", "126.com", "163.com", "189.cn",
	"360.cn", "360.com",
	"alicdn.com", "alipay.com", "aliyun.com", "amap.com",
	"baidu.com", "bdimg.com", "bilibili.com", "bilivideo.com",
	"bytedance.com", "byteimg.com", "douyin.com", "iesdouyin.com", "amemv.com",
	"snssdk.com", "pstatp.com", "ixigua.com",
	"hicloud.com", "huawei.com", "jd.com",
	"360buyimg.com", "iqiyi.com", "xiaohongshu.com",
	"meituan.com", "dianping.com", "mi.com", "miui.com", "xiaomi.com",
	"netease.com", "qq.com", "qpic.cn", "gtimg.com", "tencent.com",
	"tenpay.com", "weixin.qq.com", "wechat.com", "taobao.com", "tmall.com", "toutiao.com",
	"youku.com", "ykimg.com", "zhihu.com", "zhimg.com",
}

func defaultClashRules() []string {
	rules := []string{
		"DOMAIN,localhost,DIRECT",
		"DOMAIN-SUFFIX,local,DIRECT",
		"DOMAIN-SUFFIX,lan,DIRECT",
		"DOMAIN-SUFFIX,home.arpa,DIRECT",
		"IP-CIDR,0.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,100.64.0.0/10,DIRECT,no-resolve",
		"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,169.254.0.0/16,DIRECT,no-resolve",
		"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
		"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
		"IP-CIDR6,::1/128,DIRECT,no-resolve",
		"IP-CIDR6,fc00::/7,DIRECT,no-resolve",
		"IP-CIDR6,fe80::/10,DIRECT,no-resolve",
	}
	for _, suffix := range defaultClashDomesticDomainSuffixes {
		rules = append(rules, "DOMAIN-SUFFIX,"+suffix+",DIRECT")
	}
	return append(rules, "MATCH,PolyFleet")
}

func defaultClashDNS() clashDNS {
	domesticNameservers := []string{
		"https://dns.alidns.com/dns-query",
		"https://doh.pub/dns-query",
	}
	nameserverPolicy := map[string][]string{
		"+.home.arpa": {"system"},
		"+.lan":       {"system"},
		"+.local":     {"system"},
	}
	for _, suffix := range defaultClashDomesticDomainSuffixes {
		nameserverPolicy["+."+suffix] = domesticNameservers
	}

	return clashDNS{
		Enable: true, IPv6: false, EnhancedMode: "fake-ip",
		FakeIPRange: "198.18.0.1/16", FakeIPFilterMode: "blacklist",
		FakeIPFilter: []string{
			"+.lan", "+.local", "localhost.ptlogin2.qq.com", "Mijia Cloud",
			"time.*.com", "ntp.*.com", "+.market.xiaomi.com", "+.push.apple.com",
		},
		DefaultNameserver: []string{"223.5.5.5", "119.29.29.29"},
		Nameserver: []string{
			"https://1.1.1.1/dns-query#PolyFleet",
			"https://8.8.8.8/dns-query#PolyFleet",
		},
		NameserverPolicy:      nameserverPolicy,
		ProxyServerNameserver: domesticNameservers,
	}
}

func defaultClashSniffer() clashSniffer {
	return clashSniffer{
		Enable: true, ForceDNSMapping: true, ParsePureIP: true,
		Sniff: clashSniffProtocols{
			HTTP: clashSniffProtocol{Ports: []any{80, "8080-8880"}, OverrideDestination: true},
			TLS:  clashSniffProtocol{Ports: []any{443, 8443}},
			QUIC: clashSniffProtocol{Ports: []any{443, 8443}},
		},
		SkipDomain: []string{"Mijia Cloud", "+.push.apple.com"},
	}
}

func defaultClashTun() clashTun {
	return clashTun{
		Enable: false, Stack: "mixed", DNSHijack: []string{"any:53", "tcp://any:53"},
		AutoRoute: true, AutoRedirect: true, AutoDetectInterface: true, StrictRoute: false,
	}
}

func renderSubscription(format string, subscription store.Subscription) (renderedSubscription, error) {
	switch format {
	case "uri":
		uris, err := subscriptionURIs(subscription)
		if err != nil {
			return renderedSubscription{}, err
		}
		return renderedSubscription{
			ContentType: "text/plain; charset=utf-8",
			Body:        []byte(strings.Join(uris, "\n")),
		}, nil
	case "base64":
		uris, err := subscriptionURIs(subscription)
		if err != nil {
			return renderedSubscription{}, err
		}
		plain := strings.Join(uris, "\n")
		return renderedSubscription{
			ContentType: "text/plain; charset=utf-8",
			Body:        []byte(base64.StdEncoding.EncodeToString([]byte(plain))),
		}, nil
	case "clash":
		proxies := make([]any, 0, len(subscription.Endpoints))
		proxyNames := make([]string, 0, len(subscription.Endpoints))
		for _, endpoint := range subscription.Endpoints {
			switch endpointProtocol(endpoint) {
			case "hysteria2":
				proxies = append(proxies, clashHysteria2Proxy{
					Name: endpoint.NodeName, Type: "hysteria2", Server: endpoint.PublicHost,
					Port: endpoint.PublicPort, Password: endpoint.Credential, SNI: endpoint.SNI,
					SkipCertVerify: endpoint.TLSInsecure, Fingerprint: endpoint.TLSCertFingerprint,
					UDP: true,
				})
			case "vless":
				proxies = append(proxies, clashVLESSRealityProxy{
					Name: endpoint.NodeName, Type: "vless", Server: endpoint.PublicHost,
					Port: endpoint.PublicPort, UUID: endpoint.Credential,
					Network: realityNetwork(endpoint), TLS: true, ServerName: endpoint.SNI,
					Flow: realityFlow(endpoint), ClientFingerprint: "chrome",
					RealityOptions: clashVLESSRealityOptions{
						PublicKey: endpoint.RealityPublicKey, ShortID: endpoint.RealityShortID,
					},
					UDP: true, PacketEncoding: "xudp",
				})
			default:
				return renderedSubscription{}, store.ErrUnsupported
			}
			proxyNames = append(proxyNames, endpoint.NodeName)
		}
		proxyGroups := []clashProxyGroup{}
		if len(proxyNames) == 0 {
			proxyGroups = append(proxyGroups, clashProxyGroup{
				Name: "PolyFleet", Type: "select", Proxies: []string{"DIRECT"},
			})
		} else {
			selection := append([]string{"自动选择"}, proxyNames...)
			selection = append(selection, "DIRECT")
			proxyGroups = append(proxyGroups,
				clashProxyGroup{Name: "PolyFleet", Type: "select", Proxies: selection},
				clashProxyGroup{
					Name: "自动选择", Type: "url-test", Proxies: proxyNames,
					URL: "https://www.gstatic.com/generate_204", Interval: 300,
					Tolerance: 80, Lazy: true,
				},
			)
		}
		body, err := yaml.Marshal(clashSubscription{
			AllowLAN: false, Mode: "rule", LogLevel: "warning", IPv6: false,
			UnifiedDelay: true, TCPConcurrent: true,
			Profile: clashProfile{StoreSelected: true, StoreFakeIP: true},
			DNS:     defaultClashDNS(), Sniffer: defaultClashSniffer(), Tun: defaultClashTun(),
			Proxies: proxies, ProxyGroups: proxyGroups, Rules: defaultClashRules(),
		})
		if err != nil {
			return renderedSubscription{}, fmt.Errorf("encode Clash subscription: %w", err)
		}
		return renderedSubscription{ContentType: "application/yaml; charset=utf-8", Body: body}, nil
	case "sing-box":
		outbounds := make([]any, 0, len(subscription.Endpoints))
		for _, endpoint := range subscription.Endpoints {
			switch endpointProtocol(endpoint) {
			case "hysteria2":
				outbounds = append(outbounds, singBoxHysteria2Outbound{
					Type: "hysteria2", Tag: endpoint.NodeName, Server: endpoint.PublicHost,
					ServerPort: endpoint.PublicPort, Password: endpoint.Credential,
					TLS: singBoxTLSData{
						Enabled: true, ServerName: endpoint.SNI, Insecure: endpoint.TLSInsecure,
						CertificatePublicKeySHA256: optionalStringSlice(endpoint.TLSPublicKeySHA256),
					},
				})
			case "vless":
				outbounds = append(outbounds, singBoxVLESSRealityOutbound{
					Type: "vless", Tag: endpoint.NodeName, Server: endpoint.PublicHost,
					ServerPort: endpoint.PublicPort, UUID: endpoint.Credential,
					Flow: realityFlow(endpoint), Network: realityNetwork(endpoint),
					TLS: singBoxVLESSRealityTLS{
						Enabled: true, ServerName: endpoint.SNI,
						UTLS: singBoxUTLS{Enabled: true, Fingerprint: "chrome"},
						Reality: singBoxRealityClientTLS{
							Enabled: true, PublicKey: endpoint.RealityPublicKey,
							ShortID: endpoint.RealityShortID,
						},
					},
				})
			default:
				return renderedSubscription{}, store.ErrUnsupported
			}
		}
		body, err := json.MarshalIndent(singBoxSubscription{Outbounds: outbounds}, "", "  ")
		if err != nil {
			return renderedSubscription{}, fmt.Errorf("encode sing-box subscription: %w", err)
		}
		body = append(body, '\n')
		return renderedSubscription{ContentType: "application/json; charset=utf-8", Body: body}, nil
	default:
		return renderedSubscription{}, store.ErrUnsupported
	}
}

func subscriptionURIs(subscription store.Subscription) ([]string, error) {
	result := make([]string, 0, len(subscription.Endpoints))
	for _, endpoint := range subscription.Endpoints {
		switch endpointProtocol(endpoint) {
		case "hysteria2":
			result = append(result, hysteria2URI(endpoint))
		case "vless":
			result = append(result, vlessRealityURI(endpoint))
		default:
			return nil, store.ErrUnsupported
		}
	}
	return result, nil
}

func hysteria2URI(endpoint store.SubscriptionEndpoint) string {
	address := net.JoinHostPort(endpoint.PublicHost, strconv.Itoa(endpoint.PublicPort))
	value := &url.URL{
		Scheme:   "hysteria2",
		User:     url.User(endpoint.Credential),
		Host:     address,
		Path:     "/",
		Fragment: endpoint.NodeName,
	}
	query := url.Values{}
	if endpoint.SNI != "" {
		query.Set("sni", endpoint.SNI)
	}
	if endpoint.TLSInsecure {
		query.Set("insecure", "1")
	}
	if endpoint.TLSCertFingerprint != "" {
		query.Set("pinSHA256", endpoint.TLSCertFingerprint)
	}
	value.RawQuery = query.Encode()
	return value.String()
}

func vlessRealityURI(endpoint store.SubscriptionEndpoint) string {
	address := net.JoinHostPort(endpoint.PublicHost, strconv.Itoa(endpoint.PublicPort))
	value := &url.URL{
		Scheme: "vless", User: url.User(endpoint.Credential), Host: address,
		Fragment: endpoint.NodeName,
	}
	query := url.Values{}
	query.Set("encryption", "none")
	query.Set("flow", realityFlow(endpoint))
	query.Set("security", "reality")
	query.Set("sni", endpoint.SNI)
	query.Set("pbk", endpoint.RealityPublicKey)
	query.Set("sid", endpoint.RealityShortID)
	query.Set("type", realityNetwork(endpoint))
	value.RawQuery = query.Encode()
	return value.String()
}

func endpointProtocol(endpoint store.SubscriptionEndpoint) string {
	if endpoint.Protocol == "" {
		return "hysteria2"
	}
	return endpoint.Protocol
}

func realityFlow(endpoint store.SubscriptionEndpoint) string {
	if endpoint.Flow == "" {
		return "xtls-rprx-vision"
	}
	return endpoint.Flow
}

func realityNetwork(endpoint store.SubscriptionEndpoint) string {
	if endpoint.Network == "" {
		return "tcp"
	}
	return endpoint.Network
}

func optionalStringSlice(value string) []string {
	if value == "" {
		return nil
	}
	return []string{value}
}

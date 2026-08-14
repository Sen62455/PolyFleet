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
	Proxies     []any             `yaml:"proxies"`
	ProxyGroups []clashProxyGroup `yaml:"proxy-groups"`
	Rules       []string          `yaml:"rules"`
}

type clashProxyGroup struct {
	Name    string   `yaml:"name"`
	Type    string   `yaml:"type"`
	Proxies []string `yaml:"proxies"`
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

func defaultClashRules() []string {
	return []string{
		"DOMAIN-SUFFIX,cn,DIRECT",
		"DOMAIN-KEYWORD,-cn,DIRECT",
		"DOMAIN-SUFFIX,qq.com,DIRECT",
		"DOMAIN-SUFFIX,weixin.qq.com,DIRECT",
		"DOMAIN-SUFFIX,tenpay.com,DIRECT",
		"DOMAIN-SUFFIX,baidu.com,DIRECT",
		"DOMAIN-SUFFIX,bilibili.com,DIRECT",
		"DOMAIN-SUFFIX,bilivideo.com,DIRECT",
		"DOMAIN-SUFFIX,douyin.com,DIRECT",
		"DOMAIN-SUFFIX,iesdouyin.com,DIRECT",
		"DOMAIN-SUFFIX,amemv.com,DIRECT",
		"DOMAIN-SUFFIX,snssdk.com,DIRECT",
		"DOMAIN-SUFFIX,toutiao.com,DIRECT",
		"DOMAIN-SUFFIX,bytedance.com,DIRECT",
		"DOMAIN-SUFFIX,pstatp.com,DIRECT",
		"DOMAIN-SUFFIX,ixigua.com,DIRECT",
		"DOMAIN-SUFFIX,zhihu.com,DIRECT",
		"DOMAIN-SUFFIX,aliyun.com,DIRECT",
		"DOMAIN-SUFFIX,taobao.com,DIRECT",
		"DOMAIN-SUFFIX,tmall.com,DIRECT",
		"DOMAIN-SUFFIX,jd.com,DIRECT",
		"DOMAIN-SUFFIX,360buyimg.com,DIRECT",
		"DOMAIN-SUFFIX,163.com,DIRECT",
		"DOMAIN-SUFFIX,126.com,DIRECT",
		"DOMAIN-SUFFIX,netease.com,DIRECT",
		"DOMAIN-SUFFIX,mi.com,DIRECT",
		"DOMAIN-SUFFIX,xiaomi.com,DIRECT",
		"DOMAIN-SUFFIX,xiaohongshu.com,DIRECT",
		"DOMAIN-SUFFIX,iqiyi.com,DIRECT",
		"DOMAIN-SUFFIX,youku.com,DIRECT",
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
		"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
		"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
		"GEOIP,CN,DIRECT",
		"MATCH,PolyFleet",
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
				})
			default:
				return renderedSubscription{}, store.ErrUnsupported
			}
			proxyNames = append(proxyNames, endpoint.NodeName)
		}
		if len(proxyNames) == 0 {
			proxyNames = append(proxyNames, "DIRECT")
		}
		body, err := yaml.Marshal(clashSubscription{
			Proxies: proxies,
			ProxyGroups: []clashProxyGroup{{
				Name: "PolyFleet", Type: "select", Proxies: proxyNames,
			}},
			Rules: defaultClashRules(),
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

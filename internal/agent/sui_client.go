package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
)

const maxSUIResponseBytes = 2 * 1024 * 1024

var suiVersionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$`)

var errSUIClientNotFound = errors.New("S-UI client not found")

type suiAPIError struct {
	code string
}

func (err suiAPIError) Error() string { return err.code }

type suiClient struct {
	baseURL string
	token   string
	http    *http.Client
}

type suiEnvelope struct {
	Success bool            `json:"success"`
	Object  json.RawMessage `json:"obj"`
}

type suiProbeObject struct {
	System struct {
		AppVersion string `json:"appVersion"`
	} `json:"sys"`
	SingBox struct {
		Running bool `json:"running"`
	} `json:"sbd"`
}

type suiInbound struct {
	ID         int64  `json:"id"`
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
}

type suiClientSummary struct {
	ID       int64   `json:"id"`
	Enable   bool    `json:"enable"`
	Name     string  `json:"name"`
	Inbounds []int64 `json:"inbounds"`
	Volume   int64   `json:"volume"`
	Expiry   int64   `json:"expiry"`
	Down     int64   `json:"down"`
	Up       int64   `json:"up"`
	Desc     string  `json:"desc"`
	Group    string  `json:"group"`
	OnlineAt int64   `json:"onlineAt"`
}

type suiClientDetail struct {
	ID         int64           `json:"id,omitempty"`
	Enable     bool            `json:"enable"`
	Name       string          `json:"name"`
	Config     json.RawMessage `json:"config"`
	Inbounds   []int64         `json:"inbounds"`
	Links      json.RawMessage `json:"links"`
	Volume     int64           `json:"volume"`
	Expiry     int64           `json:"expiry"`
	Down       int64           `json:"down"`
	Up         int64           `json:"up"`
	Desc       string          `json:"desc"`
	Group      string          `json:"group"`
	Remark     string          `json:"remark"`
	DelayStart bool            `json:"delayStart"`
	AutoReset  bool            `json:"autoReset"`
	ResetDays  int             `json:"resetDays"`
	NextReset  int64           `json:"nextReset"`
	TotalUp    int64           `json:"totalUp"`
	TotalDown  int64           `json:"totalDown"`
	CreatedAt  int64           `json:"createdAt"`
	OnlineAt   int64           `json:"onlineAt"`
}

type suiDiscovery struct {
	Inbounds []suiInbound
	Clients  []suiClientSummary
	Online   map[string]struct{}
}

func newSUIClient(baseURL, token string) *suiClient {
	return &suiClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (client *suiClient) probe(ctx context.Context, now time.Time) (protocol.AdapterInfo, protocol.CoreInfo) {
	probedAt := now.UTC()
	adapter := protocol.AdapterInfo{
		Name: "s_ui", Status: "unavailable", ErrorCode: "sui_api_unavailable",
		LastProbedAt: &probedAt,
	}
	core := protocol.CoreInfo{Name: "sing-box"}
	if client.token == "" {
		adapter.Status = "not_configured"
		adapter.ErrorCode = "sui_token_not_configured"
		return adapter, core
	}
	var object suiProbeObject
	if err := client.get(ctx, "status", url.Values{"r": {"sys,sbd"}}, &object); err != nil {
		adapter.ErrorCode = suiErrorCode(err)
		return adapter, core
	}
	adapter.Version = strings.TrimSpace(object.System.AppVersion)
	core.Running = object.SingBox.Running
	if !supportedSUIVersion(adapter.Version) {
		adapter.Status = "incompatible"
		adapter.ErrorCode = "sui_version_unsupported"
		return adapter, core
	}
	adapter.Status = "compatible"
	adapter.ErrorCode = ""
	return adapter, core
}

func supportedSUIVersion(version string) bool {
	match := suiVersionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if len(match) != 4 {
		return false
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	return major == 1 && minor == 5 && patch >= 3
}

func (client *suiClient) discover(ctx context.Context) (suiDiscovery, error) {
	var inboundObject struct {
		Inbounds []suiInbound `json:"inbounds"`
	}
	if err := client.get(ctx, "inbounds", nil, &inboundObject); err != nil {
		return suiDiscovery{}, err
	}
	var clientObject struct {
		Clients []suiClientSummary `json:"clients"`
	}
	if err := client.get(ctx, "clients", nil, &clientObject); err != nil {
		return suiDiscovery{}, err
	}
	var onlineObject struct {
		User []string `json:"user"`
	}
	if err := client.get(ctx, "onlines", nil, &onlineObject); err != nil {
		return suiDiscovery{}, err
	}
	online := make(map[string]struct{}, len(onlineObject.User))
	for _, name := range onlineObject.User {
		online[name] = struct{}{}
	}
	inbounds := make([]suiInbound, 0, len(inboundObject.Inbounds))
	hysteriaInboundIDs := make(map[int64]struct{})
	for _, inbound := range inboundObject.Inbounds {
		if inbound.ID > 0 && inbound.Type == "hysteria2" {
			inbounds = append(inbounds, inbound)
			hysteriaInboundIDs[inbound.ID] = struct{}{}
		}
	}
	clients := make([]suiClientSummary, 0, len(clientObject.Clients))
	for _, candidate := range clientObject.Clients {
		for _, inboundID := range candidate.Inbounds {
			if _, ok := hysteriaInboundIDs[inboundID]; ok {
				clients = append(clients, candidate)
				break
			}
		}
	}
	sort.Slice(inbounds, func(left, right int) bool { return inbounds[left].ID < inbounds[right].ID })
	sort.Slice(clients, func(left, right int) bool {
		return clients[left].ID < clients[right].ID
	})
	return suiDiscovery{Inbounds: inbounds, Clients: clients, Online: online}, nil
}

func (client *suiClient) getClient(ctx context.Context, id int64) (suiClientDetail, error) {
	var object struct {
		Clients []suiClientDetail `json:"clients"`
	}
	if err := client.get(ctx, "clients", url.Values{"id": {strconv.FormatInt(id, 10)}}, &object); err != nil {
		return suiClientDetail{}, err
	}
	if len(object.Clients) != 1 || object.Clients[0].ID != id {
		return suiClientDetail{}, errSUIClientNotFound
	}
	return object.Clients[0], nil
}

func (client *suiClient) saveClient(ctx context.Context, action string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode S-UI client: %w", err)
	}
	form := url.Values{
		"object": {"clients"},
		"action": {action},
		"data":   {string(encoded)},
	}
	return client.request(ctx, http.MethodPost, "save", nil, form, nil)
}

func (client *suiClient) get(ctx context.Context, action string, query url.Values, destination any) error {
	return client.request(ctx, http.MethodGet, action, query, nil, destination)
}

func (client *suiClient) request(
	ctx context.Context,
	method, action string,
	query, form url.Values,
	destination any,
) error {
	endpoint, err := url.Parse(client.baseURL + "/" + action)
	if err != nil {
		return suiAPIError{code: "sui_url_invalid"}
	}
	if query != nil {
		endpoint.RawQuery = query.Encode()
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return suiAPIError{code: "sui_request_invalid"}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Token", client.token)
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return suiAPIError{code: "sui_api_unavailable"}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxSUIResponseBytes+1))
		return suiAPIError{code: "sui_http_error"}
	}
	limited := io.LimitReader(response.Body, maxSUIResponseBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return suiAPIError{code: "sui_response_invalid"}
	}
	if len(payload) > maxSUIResponseBytes {
		return suiAPIError{code: "sui_response_too_large"}
	}
	var envelope suiEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil || !envelope.Success {
		return suiAPIError{code: "sui_api_rejected"}
	}
	if destination != nil {
		if len(envelope.Object) == 0 || bytes.Equal(envelope.Object, []byte("null")) ||
			json.Unmarshal(envelope.Object, destination) != nil {
			return suiAPIError{code: "sui_response_invalid"}
		}
	}
	return nil
}

func suiErrorCode(err error) string {
	var reconcileError suiReconcileError
	if errors.As(err, &reconcileError) {
		return reconcileError.code
	}
	var apiError suiAPIError
	if errors.As(err, &apiError) {
		return apiError.code
	}
	if errors.Is(err, errSUIClientNotFound) {
		return "sui_client_not_found"
	}
	return "sui_adapter_failed"
}

func suiCredentialFingerprint(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return "fp_" + base64.RawURLEncoding.EncodeToString(digest[:6])
}

func suiClientCredential(client suiClientDetail) (string, error) {
	var configs map[string]json.RawMessage
	if err := json.Unmarshal(client.Config, &configs); err != nil {
		return "", errors.New("S-UI client config is invalid")
	}
	raw, ok := configs["hysteria2"]
	if !ok {
		return "", errors.New("S-UI client has no Hysteria2 credential")
	}
	var credential struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(raw, &credential); err != nil || credential.Password == "" {
		return "", errors.New("S-UI Hysteria2 credential is invalid")
	}
	return credential.Password, nil
}

func setSUIClientCredential(client *suiClientDetail, name, secret string) error {
	var configs map[string]json.RawMessage
	if len(client.Config) == 0 || bytes.Equal(client.Config, []byte("null")) {
		configs = make(map[string]json.RawMessage)
	} else if err := json.Unmarshal(client.Config, &configs); err != nil {
		return errors.New("S-UI client config is invalid")
	}
	fields := make(map[string]json.RawMessage)
	if raw, ok := configs["hysteria2"]; ok {
		if err := json.Unmarshal(raw, &fields); err != nil {
			return errors.New("S-UI Hysteria2 config is invalid")
		}
	}
	nameJSON, _ := json.Marshal(name)
	passwordJSON, _ := json.Marshal(secret)
	fields["name"] = nameJSON
	fields["password"] = passwordJSON
	hysteria, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	configs["hysteria2"] = hysteria
	client.Config, err = json.Marshal(configs)
	return err
}

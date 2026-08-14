package server

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/store"
)

func TestUnifiedSubscriptionAPIAndCredentialRotation(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	publicKeyPin := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))

	createdNode := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "subscription-node", "provider": "Test", "region": "Tokyo",
		"adapter_type": "native_hysteria2", "public_host": "hy2.example.com",
		"public_port": 8443, "sni": "edge.example.com", "tls_insecure": false,
		"tls_cert_fingerprint":  strings.Repeat("ab", 32),
		"tls_public_key_sha256": publicKeyPin,
	}, app.csrf, "http://hyfleet.test")
	requireStatus(t, createdNode, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, createdNode, &node)
	if node.PublicHost != "hy2.example.com" || node.PublicPort != 8443 || node.SNI != "edge.example.com" ||
		node.TLSCertFingerprint != strings.TrimSuffix(strings.Repeat("AB:", 32), ":") ||
		node.TLSPublicKeySHA256 != publicKeyPin {
		t.Fatalf("unexpected subscription endpoint: %#v", node)
	}

	createdUser := app.request(t, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "subscription-user", "enabled": true, "node_ids": []string{node.ID},
	}, app.csrf, "http://hyfleet.test")
	requireStatus(t, createdUser, http.StatusCreated)
	var userPayload struct {
		User        userResponse         `json:"user"`
		Credentials []credentialResponse `json:"credentials"`
	}
	decodeResponse(t, createdUser, &userPayload)
	firstCredential := userPayload.Credentials[0].Credential
	ackServerNode(t, app.store, node.ID, time.Now().UTC())

	withoutCSRF := app.request(t, http.MethodPost,
		"/api/v1/users/"+userPayload.User.ID+"/subscription-tokens", map[string]any{
			"name": "phone",
		}, "", "")
	requireStatus(t, withoutCSRF, http.StatusForbidden)
	createdToken := app.request(t, http.MethodPost,
		"/api/v1/users/"+userPayload.User.ID+"/subscription-tokens", map[string]any{
			"name": "phone", "allowed_formats": []string{"uri", "base64", "clash", "sing-box"},
		}, app.csrf, "http://hyfleet.test")
	requireStatus(t, createdToken, http.StatusCreated)
	requireCredentialNoStore(t, createdToken)
	var issued issuedSubscriptionTokenResponse
	decodeResponse(t, createdToken, &issued)
	if !strings.HasPrefix(issued.Token, "hys_") || issued.URLs.URI == "" ||
		issued.URLs.Base64 == "" || issued.URLs.Clash == "" || issued.URLs.SingBox == "" {
		t.Fatalf("unexpected issued subscription: %#v", issued)
	}

	listed := app.request(t, http.MethodGet,
		"/api/v1/users/"+userPayload.User.ID+"/subscription-tokens", nil, "", "")
	requireStatus(t, listed, http.StatusOK)
	if strings.Contains(listed.Body.String(), issued.Token) || !strings.Contains(listed.Body.String(), issued.Subscription.TokenPrefix) {
		t.Fatalf("token listing leaked or omitted metadata: %s", listed.Body.String())
	}

	uriURL, err := url.Parse(issued.URLs.URI)
	if err != nil {
		t.Fatalf("url.Parse(subscription URI URL) error = %v", err)
	}
	uriResponse := app.request(t, http.MethodGet, uriURL.RequestURI(), nil, "", "")
	requireStatus(t, uriResponse, http.StatusOK)
	if uriResponse.Header().Get("Cache-Control") != "no-store, max-age=0" ||
		uriResponse.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("subscription cache headers = %q, %q",
			uriResponse.Header().Get("Cache-Control"), uriResponse.Header().Get("Pragma"))
	}
	parsedURI, err := url.Parse(uriResponse.Body.String())
	if err != nil || parsedURI.User.Username() != firstCredential || parsedURI.Hostname() != "hy2.example.com" {
		t.Fatalf("unexpected Hysteria2 URI = %q, error = %v", uriResponse.Body.String(), err)
	}
	base64URL, _ := url.Parse(issued.URLs.Base64)
	base64Response := app.request(t, http.MethodGet, base64URL.RequestURI(), nil, "", "")
	requireStatus(t, base64Response, http.StatusOK)
	decoded, err := base64.StdEncoding.DecodeString(base64Response.Body.String())
	if err != nil || string(decoded) != uriResponse.Body.String() {
		t.Fatalf("base64 subscription decoded = %q, error = %v", decoded, err)
	}

	rotatedCredential := app.request(t, http.MethodPost,
		"/api/v1/users/"+userPayload.User.ID+"/assignments/"+node.ID+"/rotate-credential",
		map[string]any{}, app.csrf, "http://hyfleet.test")
	requireStatus(t, rotatedCredential, http.StatusAccepted)
	requireCredentialNoStore(t, rotatedCredential)
	var rotation struct {
		Credential credentialResponse `json:"credential"`
	}
	decodeResponse(t, rotatedCredential, &rotation)
	if rotation.Credential.Credential == "" || rotation.Credential.Credential == firstCredential {
		t.Fatalf("unexpected rotated credential: %#v", rotation.Credential)
	}
	pendingSubscription := app.request(t, http.MethodGet, uriURL.RequestURI(), nil, "", "")
	requireStatus(t, pendingSubscription, http.StatusOK)
	if pendingSubscription.Body.Len() != 0 {
		t.Fatalf("pending credential was published: %s", pendingSubscription.Body.String())
	}
	ackServerNode(t, app.store, node.ID, time.Now().UTC().Add(time.Second))
	appliedSubscription := app.request(t, http.MethodGet, uriURL.RequestURI(), nil, "", "")
	requireStatus(t, appliedSubscription, http.StatusOK)
	parsedURI, err = url.Parse(appliedSubscription.Body.String())
	if err != nil || parsedURI.User.Username() != rotation.Credential.Credential {
		t.Fatalf("rotated subscription URI = %q, error = %v", appliedSubscription.Body.String(), err)
	}

	rotatedTokenResponse := app.request(t, http.MethodPost,
		"/api/v1/users/"+userPayload.User.ID+"/subscription-tokens/"+
			issued.Subscription.ID+"/rotate", map[string]any{}, app.csrf, "http://hyfleet.test")
	requireStatus(t, rotatedTokenResponse, http.StatusOK)
	var rotatedToken issuedSubscriptionTokenResponse
	decodeResponse(t, rotatedTokenResponse, &rotatedToken)
	if rotatedToken.Token == issued.Token || rotatedToken.Token == "" {
		t.Fatalf("subscription token was not rotated: %#v", rotatedToken)
	}
	oldToken := app.request(t, http.MethodGet, uriURL.RequestURI(), nil, "", "")
	requireStatus(t, oldToken, http.StatusNotFound)
	rotatedURIURL, _ := url.Parse(rotatedToken.URLs.URI)
	newToken := app.request(t, http.MethodGet, rotatedURIURL.RequestURI(), nil, "", "")
	requireStatus(t, newToken, http.StatusOK)

	revoked := app.request(t, http.MethodDelete,
		"/api/v1/users/"+userPayload.User.ID+"/subscription-tokens/"+issued.Subscription.ID,
		nil, app.csrf, "http://hyfleet.test")
	requireStatus(t, revoked, http.StatusNoContent)
	revokedAgain := app.request(t, http.MethodDelete,
		"/api/v1/users/"+userPayload.User.ID+"/subscription-tokens/"+issued.Subscription.ID,
		nil, app.csrf, "http://hyfleet.test")
	requireStatus(t, revokedAgain, http.StatusNoContent)
	revokedFetch := app.request(t, http.MethodGet, rotatedURIURL.RequestURI(), nil, "", "")
	requireStatus(t, revokedFetch, http.StatusNotFound)

	if strings.Contains(app.logs.String(), issued.Token) || strings.Contains(app.logs.String(), rotatedToken.Token) {
		t.Fatalf("subscription token leaked into logs: %s", app.logs.String())
	}
	if !strings.Contains(app.logs.String(), "path=/sub/[redacted]/uri") {
		t.Fatalf("redacted subscription path missing from logs: %s", app.logs.String())
	}
}

func TestSubscriptionTokenValidationAndInvalidEndpoint(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	invalidNode := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "invalid-endpoint", "adapter_type": "native_hysteria2",
		"public_host": "https://hy2.example.com:443", "public_port": 0,
	}, app.csrf, "http://hyfleet.test")
	requireStatus(t, invalidNode, http.StatusUnprocessableEntity)
	invalidFingerprint := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "invalid-fingerprint", "adapter_type": "native_hysteria2",
		"tls_cert_fingerprint": "not-a-sha256-fingerprint",
	}, app.csrf, "http://hyfleet.test")
	requireStatus(t, invalidFingerprint, http.StatusUnprocessableEntity)
	invalidPublicKeyPin := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "invalid-public-key-pin", "adapter_type": "native_hysteria2",
		"tls_public_key_sha256": base64.StdEncoding.EncodeToString([]byte("too short")),
	}, app.csrf, "http://hyfleet.test")
	requireStatus(t, invalidPublicKeyPin, http.StatusUnprocessableEntity)

	missingToken := app.request(t, http.MethodGet,
		"/sub/hys_abcdefghijklmnopqrstuvwxyz0123456789/uri", nil, "", "")
	requireStatus(t, missingToken, http.StatusNotFound)
	if missingToken.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("missing subscription cache control = %q", missingToken.Header().Get("Cache-Control"))
	}
}

func ackServerNode(t *testing.T, database *store.Store, nodeID string, now time.Time) {
	t.Helper()
	node, err := database.GetNode(t.Context(), nodeID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	envelope, err := database.GetDesiredSnapshot(t.Context(), nodeID, node.DesiredVersion)
	if err != nil {
		t.Fatalf("GetDesiredSnapshot() error = %v", err)
	}
	hash, err := base64.RawURLEncoding.DecodeString(envelope.SHA256)
	if err != nil {
		t.Fatalf("decode desired hash: %v", err)
	}
	if err := database.AcknowledgeDesired(t.Context(), store.AgentIdentity{
		NodeID: nodeID, AdapterType: "native_hysteria2", Enabled: true,
	}, node.DesiredVersion, hash, "applied", "", "", now); err != nil {
		t.Fatalf("AcknowledgeDesired() error = %v", err)
	}
}

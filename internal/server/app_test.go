package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/config"
	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/Sen62455/PolyFleet/internal/store"
)

const testPassword = "correct horse battery staple"

type testApp struct {
	handler http.Handler
	store   *store.Store
	cookie  *http.Cookie
	csrf    string
	logs    *bytes.Buffer
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hyfleet.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	cfg := config.Server{
		PublicURL:          "http://hyfleet.test",
		BootstrapToken:     "test-bootstrap-token-with-enough-entropy",
		SessionLifetime:    12 * time.Hour,
		SessionIdleTimeout: 30 * time.Minute,
		StaleAfter:         45 * time.Second,
		OfflineAfter:       90 * time.Second,
	}
	logs := &bytes.Buffer{}
	application, err := New(cfg, database, bytes.Repeat([]byte{0x42}, 32), slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	handler, err := application.Handler()
	if err != nil {
		t.Fatalf("Handler() error = %v", err)
	}
	return &testApp{handler: handler, store: database, logs: logs}
}

func (app *testApp) request(t *testing.T, method, path string, body any, csrf, origin string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, "http://hyfleet.test"+path, reader)
	request.RemoteAddr = "192.0.2.20:40200"
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if app.cookie != nil {
		request.AddCookie(app.cookie)
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	app.handler.ServeHTTP(response, request)
	return response
}

func (app *testApp) bootstrap(t *testing.T) {
	t.Helper()
	response := app.request(t, http.MethodPost, "/api/v1/setup/bootstrap", map[string]any{
		"bootstrap_token": "test-bootstrap-token-with-enough-entropy",
		"username":        "admin",
		"password":        testPassword,
	}, "", "http://hyfleet.test")
	requireStatus(t, response, http.StatusOK)
	var session sessionResponse
	decodeResponse(t, response, &session)
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("bootstrap cookies = %d, want 1", len(cookies))
	}
	app.cookie = cookies[0]
	app.csrf = session.CSRFToken
}

func requireStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, want, response.Body.String())
	}
}

func requireCredentialNoStore(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Header().Get("Cache-Control") != "no-store" ||
		response.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("credential response cache headers = %q, %q",
			response.Header().Get("Cache-Control"), response.Header().Get("Pragma"))
	}
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), destination); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, response.Body.String())
	}
}

func TestAdminAndNodeLifecycle(t *testing.T) {
	app := newTestApp(t)
	status := app.request(t, http.MethodGet, "/api/v1/setup/status", nil, "", "")
	requireStatus(t, status, http.StatusOK)
	var setup map[string]bool
	decodeResponse(t, status, &setup)
	if !setup["setup_required"] || !setup["bootstrap_token_configured"] {
		t.Fatalf("unexpected setup status: %#v", setup)
	}

	rejected := app.request(t, http.MethodPost, "/api/v1/setup/bootstrap", map[string]any{
		"bootstrap_token": "test-bootstrap-token-with-enough-entropy",
		"username":        "admin", "password": testPassword,
	}, "", "https://attacker.example")
	requireStatus(t, rejected, http.StatusForbidden)

	app.bootstrap(t)
	if !app.cookie.HttpOnly || app.cookie.SameSite != http.SameSiteStrictMode || app.cookie.Secure {
		t.Fatalf("unexpected session cookie attributes: %#v", app.cookie)
	}
	second := app.request(t, http.MethodPost, "/api/v1/setup/bootstrap", map[string]any{
		"bootstrap_token": "test-bootstrap-token-with-enough-entropy",
		"username":        "other", "password": testPassword,
	}, "", "")
	requireStatus(t, second, http.StatusConflict)

	withoutCSRF := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "LisaHost", "provider": "Lisa", "region": "US",
		"adapter_type": "native_hysteria2",
	}, "", "")
	requireStatus(t, withoutCSRF, http.StatusForbidden)

	created := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "LisaHost", "provider": "Lisa", "region": "US",
		"adapter_type": "native_hysteria2",
	}, app.csrf, "http://hyfleet.test")
	requireStatus(t, created, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, created, &node)
	if node.Name != "LisaHost" || node.Status != "pending" || node.DesiredVersion != 1 || !node.Enabled {
		t.Fatalf("unexpected node: %#v", node)
	}

	duplicate := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "lisahost", "provider": "Other", "region": "US",
		"adapter_type": "native_hysteria2",
	}, app.csrf, "")
	requireStatus(t, duplicate, http.StatusConflict)

	updated := app.request(t, http.MethodPut, "/api/v1/nodes/"+node.ID, map[string]any{
		"name": "LisaHost", "provider": "Lisa", "region": "Los Angeles",
		"adapter_type": "native_hysteria2", "enabled": false,
	}, app.csrf, "")
	requireStatus(t, updated, http.StatusOK)
	decodeResponse(t, updated, &node)
	if node.Enabled || node.Status != "disabled" || node.DesiredVersion != 2 {
		t.Fatalf("unexpected updated node: %#v", node)
	}

	token := app.request(t, http.MethodPost, "/api/v1/nodes/"+node.ID+"/enrollment-token", map[string]any{}, app.csrf, "")
	requireStatus(t, token, http.StatusCreated)
	var enrollment map[string]any
	decodeResponse(t, token, &enrollment)
	if enrollment["enrollment_token"] == "" {
		t.Fatal("enrollment token is empty")
	}

	archived := app.request(t, http.MethodDelete, "/api/v1/nodes/"+node.ID, nil, app.csrf, "")
	requireStatus(t, archived, http.StatusNoContent)
	listed := app.request(t, http.MethodGet, "/api/v1/nodes", nil, "", "")
	requireStatus(t, listed, http.StatusOK)
	var list struct {
		Nodes []nodeResponse `json:"nodes"`
	}
	decodeResponse(t, listed, &list)
	if len(list.Nodes) != 0 {
		t.Fatalf("nodes after archive = %d, want 0", len(list.Nodes))
	}
	recreated := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "lisahost", "provider": "Replacement", "region": "Hong Kong",
		"adapter_type": "s_ui",
	}, app.csrf, "")
	requireStatus(t, recreated, http.StatusCreated)
	var replacement nodeResponse
	decodeResponse(t, recreated, &replacement)
	if replacement.ID == node.ID || replacement.Name != "lisahost" || replacement.AdapterType != "s_ui" {
		t.Fatalf("unexpected replacement node: %#v", replacement)
	}

	logout := app.request(t, http.MethodPost, "/api/v1/auth/logout", map[string]any{}, app.csrf, "")
	requireStatus(t, logout, http.StatusNoContent)
	session := app.request(t, http.MethodGet, "/api/v1/auth/session", nil, "", "")
	requireStatus(t, session, http.StatusUnauthorized)
	login := app.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
		"username": "ADMIN", "password": testPassword,
	}, "", "http://hyfleet.test")
	requireStatus(t, login, http.StatusOK)
}

func TestVLESSRealityNodeAPIUsesTypedBoundedSettings(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)

	validInput := map[string]any{
		"name": "Reality Lab", "provider": "LisaHost", "region": "Los Angeles",
		"adapter_type": "sing_box_vless_reality",
		"public_host":  "reality.example.com", "public_port": 24443,
		"sni": "www.microsoft.com", "tls_insecure": false,
		"tls_cert_fingerprint": "", "tls_public_key_sha256": "",
		"reality": map[string]any{
			"handshake_server": "www.microsoft.com", "handshake_port": 443,
		},
	}
	canonicalInput := map[string]any{}
	for key, value := range validInput {
		canonicalInput[key] = value
	}
	canonicalInput["name"] = "Reality Canonical DNS"
	canonicalInput["sni"] = "WWW.MICROSOFT.COM"
	canonicalInput["reality"] = map[string]any{
		"handshake_server": "WWW.MICROSOFT.COM", "handshake_port": 443,
	}
	canonicalResponse := app.request(t, http.MethodPost, "/api/v1/nodes", canonicalInput, app.csrf, "")
	requireStatus(t, canonicalResponse, http.StatusCreated)
	var canonicalNode nodeResponse
	decodeResponse(t, canonicalResponse, &canonicalNode)
	if canonicalNode.SNI != "www.microsoft.com" || canonicalNode.Reality == nil ||
		canonicalNode.Reality.HandshakeServer != "www.microsoft.com" {
		t.Fatalf("Reality DNS values were not canonicalized: %#v", canonicalNode)
	}
	created := app.request(t, http.MethodPost, "/api/v1/nodes", validInput, app.csrf, "")
	requireStatus(t, created, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, created, &node)
	if node.AdapterType != store.AdapterSingBoxVLESSReality || node.PublicPort != 24443 ||
		node.SNI != "www.microsoft.com" || node.Reality == nil ||
		node.Reality.HandshakeServer != "www.microsoft.com" ||
		node.Reality.HandshakePort != 443 || node.Reality.KeyGeneration != 1 ||
		node.Reality.PublicKey != "" || node.Reality.ShortID != "" ||
		node.Reality.MaterialAppliedVersion != 0 {
		t.Fatalf("unexpected Reality node: %#v", node)
	}
	envelope, err := app.store.GetDesiredSnapshot(context.Background(), node.ID, 1)
	if err != nil {
		t.Fatalf("GetDesiredSnapshot() error = %v", err)
	}
	if envelope.Snapshot.SchemaVersion != 2 || envelope.Snapshot.VLESSReality == nil ||
		envelope.Snapshot.VLESSReality.Network != "tcp" ||
		envelope.Snapshot.VLESSReality.Flow != "xtls-rprx-vision" ||
		envelope.Snapshot.VLESSReality.KeyGeneration != 1 {
		t.Fatalf("unexpected Reality desired snapshot: %#v", envelope.Snapshot)
	}
	canonical, err := json.Marshal(envelope.Snapshot)
	if err != nil {
		t.Fatalf("json.Marshal(snapshot) error = %v", err)
	}
	for _, forbidden := range []string{"private_key", "public_key", "short_id", "uuid"} {
		if strings.Contains(string(canonical), forbidden) {
			t.Fatalf("Reality desired snapshot leaked %q: %s", forbidden, canonical)
		}
	}

	invalidCases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "missing public host", mutate: func(input map[string]any) { input["public_host"] = "" }},
		{name: "IP SNI", mutate: func(input map[string]any) { input["sni"] = "192.0.2.1" }},
		{name: "single-label SNI", mutate: func(input map[string]any) { input["sni"] = "localhost" }},
		{name: "trailing-dot SNI", mutate: func(input map[string]any) { input["sni"] = "www.microsoft.com." }},
		{name: "single-label handshake", mutate: func(input map[string]any) {
			input["reality"] = map[string]any{"handshake_server": "localhost", "handshake_port": 443}
		}},
		{name: "non-443 handshake", mutate: func(input map[string]any) {
			input["reality"] = map[string]any{"handshake_server": "www.microsoft.com", "handshake_port": 8443}
		}},
		{name: "traditional TLS override", mutate: func(input map[string]any) { input["tls_insecure"] = true }},
	}
	for _, test := range invalidCases {
		t.Run(test.name, func(t *testing.T) {
			input := map[string]any{}
			for key, value := range validInput {
				input[key] = value
			}
			input["name"] = "Invalid " + test.name
			test.mutate(input)
			response := app.request(t, http.MethodPost, "/api/v1/nodes", input, app.csrf, "")
			requireStatus(t, response, http.StatusUnprocessableEntity)
		})
	}

	nonRealityWithSettings := map[string]any{
		"name": "Bad native", "adapter_type": "native_hysteria2",
		"reality": map[string]any{"handshake_server": "www.microsoft.com", "handshake_port": 443},
	}
	response := app.request(t, http.MethodPost, "/api/v1/nodes", nonRealityWithSettings, app.csrf, "")
	requireStatus(t, response, http.StatusUnprocessableEntity)
}

func TestVLESSRealityIdentityRotationAPIIsExplicitAndConcurrencyBound(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	created := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "Reality Rotation", "adapter_type": "sing_box_vless_reality",
		"public_host": "reality.example.com", "public_port": 24443,
		"sni": "www.microsoft.com", "tls_insecure": false,
		"tls_cert_fingerprint": "", "tls_public_key_sha256": "",
		"reality": map[string]any{
			"handshake_server": "www.microsoft.com", "handshake_port": 443,
		},
	}, app.csrf, "")
	requireStatus(t, created, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, created, &node)
	if _, err := app.store.DB().Exec(`
		UPDATE nodes SET agent_installation_id = ?, applied_version = desired_version
		WHERE id = ?
	`, cryptoutil.NewID(), node.ID); err != nil {
		t.Fatalf("bind Reality test Agent: %v", err)
	}
	if _, err := app.store.DB().Exec(`
		UPDATE node_vless_reality
		SET applied_key_generation = desired_key_generation,
		    public_key = ?, short_id = ?, material_applied_version = 1
		WHERE node_id = ?
	`, "RAjjVUXRxxNkVHMbFpJTPIq8V8kV9cYvf3qj6M7iCUQ", "0123456789abcdef", node.ID); err != nil {
		t.Fatalf("seed applied Reality identity: %v", err)
	}
	path := "/api/v1/nodes/" + node.ID + "/reality/rotate-identity"
	body := map[string]any{"expected_key_generation": 1, "expected_desired_version": 1}

	withoutCSRF := app.request(t, http.MethodPost, path, body, "", "")
	requireStatus(t, withoutCSRF, http.StatusForbidden)
	rotated := app.request(t, http.MethodPost, path, body, app.csrf, "")
	requireStatus(t, rotated, http.StatusAccepted)
	var result nodeResponse
	decodeResponse(t, rotated, &result)
	if result.DesiredVersion != 2 || result.AppliedVersion != 1 || result.Status != "pending" ||
		result.Reality == nil || result.Reality.KeyGeneration != 2 ||
		result.Reality.AppliedKeyGeneration != 1 || result.Reality.PublicKey == "" {
		t.Fatalf("rotated Reality node = %#v", result)
	}
	envelope, err := app.store.GetDesiredSnapshot(context.Background(), node.ID, 2)
	if err != nil || envelope.Snapshot.VLESSReality == nil ||
		envelope.Snapshot.VLESSReality.KeyGeneration != 2 {
		t.Fatalf("rotated Reality snapshot = (%#v, %v)", envelope, err)
	}
	replayed := app.request(t, http.MethodPost, path, body, app.csrf, "")
	requireStatus(t, replayed, http.StatusConflict)
	var replayError protocol.ErrorResponse
	decodeResponse(t, replayed, &replayError)
	if replayError.Error.Code != "reality_identity_rotation_conflict" {
		t.Fatalf("rotation replay error = %#v", replayError)
	}

	native := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "Native Rotation", "adapter_type": "native_hysteria2",
	}, app.csrf, "")
	requireStatus(t, native, http.StatusCreated)
	var nativeNode nodeResponse
	decodeResponse(t, native, &nativeNode)
	unsupported := app.request(t, http.MethodPost,
		"/api/v1/nodes/"+nativeNode.ID+"/reality/rotate-identity", body, app.csrf, "")
	requireStatus(t, unsupported, http.StatusUnprocessableEntity)
	var unsupportedError protocol.ErrorResponse
	decodeResponse(t, unsupported, &unsupportedError)
	if unsupportedError.Error.Code != "reality_identity_rotation_unsupported" {
		t.Fatalf("unsupported rotation error = %#v", unsupportedError)
	}
}

func TestAgentEnrollmentHeartbeatAndDesiredState(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	created := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "test-node", "provider": "local", "region": "test",
		"adapter_type": "native_hysteria2",
	}, app.csrf, "")
	requireStatus(t, created, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, created, &node)
	tokenResponse := app.request(t, http.MethodPost, "/api/v1/nodes/"+node.ID+"/enrollment-token", map[string]any{}, app.csrf, "")
	requireStatus(t, tokenResponse, http.StatusCreated)
	var token struct {
		EnrollmentToken string `json:"enrollment_token"`
	}
	decodeResponse(t, tokenResponse, &token)

	installationID := cryptoutil.NewID()
	requestID := cryptoutil.NewID()
	enrollBody := protocol.EnrollRequest{
		EnrollmentToken: token.EnrollmentToken,
		InstallationID:  installationID,
		RequestID:       requestID,
		AgentVersion:    "v0.1.0-test", OS: "linux", OSVersion: "24.04", Architecture: "amd64",
		Capabilities: []string{"host_metrics", "read_only_foundation"},
		Adapter:      protocol.EnrollmentAdapter{Type: "native_hysteria2", CoreName: "hysteria"},
	}
	enrolled := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/enroll", enrollBody, "", requestID)
	requireStatus(t, enrolled, http.StatusOK)
	var credentials protocol.EnrollResponse
	decodeResponse(t, enrolled, &credentials)
	if credentials.NodeID != node.ID || credentials.NodeCredential == "" || credentials.Protocol != protocol.MajorVersion {
		t.Fatalf("unexpected enrollment: %#v", credentials)
	}

	replay := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/enroll", enrollBody, "", requestID)
	requireStatus(t, replay, http.StatusOK)
	var replayed protocol.EnrollResponse
	decodeResponse(t, replay, &replayed)
	if replayed.NodeCredential != credentials.NodeCredential {
		t.Fatal("enrollment retry did not replay the original credential")
	}

	conflictingID := cryptoutil.NewID()
	conflicting := enrollBody
	conflicting.RequestID = conflictingID
	conflict := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/enroll", conflicting, "", conflictingID)
	requireStatus(t, conflict, http.StatusConflict)

	now := time.Now().UTC().Truncate(time.Second)
	heartbeat := protocol.HeartbeatRequest{
		InstallationID: installationID,
		AppliedVersion: 0,
		Agent:          protocol.AgentInfo{Version: "v0.1.0-test", Protocol: protocol.MajorVersion},
		Core:           protocol.CoreInfo{Name: "hysteria", Version: "v2.12.0", Running: true},
		Host: protocol.HostMetrics{
			UptimeSeconds: 300, CPUPercent: 12.5,
			MemoryUsedBytes: 256 << 20, MemoryTotalBytes: 1024 << 20,
			DiskUsedBytes: 3 << 30, DiskTotalBytes: 20 << 30,
			NetworkRXBPS: 1200, NetworkTXBPS: 800, Load1: 0.1, Load5: 0.2, Load15: 0.3,
		},
		SampledAt: now,
	}
	beat := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/heartbeat", heartbeat, credentials.NodeCredential, cryptoutil.NewID())
	requireStatus(t, beat, http.StatusOK)
	var beatResult protocol.HeartbeatResponse
	decodeResponse(t, beat, &beatResult)
	if beatResult.DesiredVersion != 1 {
		t.Fatalf("desired version = %d, want 1", beatResult.DesiredVersion)
	}
	storedNode, err := app.store.GetNode(context.Background(), node.ID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if storedNode.Status != "online" || !storedNode.CoreRunning || storedNode.MemoryUsedBytes != 256<<20 {
		t.Fatalf("heartbeat was not persisted: %#v", storedNode)
	}
	adapterChange := app.request(t, http.MethodPut, "/api/v1/nodes/"+node.ID, map[string]any{
		"name": "test-node", "provider": "local", "region": "test",
		"adapter_type": "s_ui", "enabled": true,
	}, app.csrf, "")
	requireStatus(t, adapterChange, http.StatusConflict)

	clearedReplay := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/enroll", enrollBody, "", requestID)
	requireStatus(t, clearedReplay, http.StatusConflict)

	desired := agentRequest(t, app.handler, http.MethodGet, "/agent/v1/desired?after=0", nil, credentials.NodeCredential, cryptoutil.NewID())
	requireStatus(t, desired, http.StatusOK)
	var envelope protocol.DesiredEnvelope
	decodeResponse(t, desired, &envelope)
	if envelope.Snapshot.NodeID != node.ID || envelope.Snapshot.Version != 1 || len(envelope.Snapshot.Users) != 0 {
		t.Fatalf("unexpected desired state: %#v", envelope)
	}

	ack := protocol.DesiredAckRequest{Status: "applied", SnapshotHash: envelope.SHA256, Adapter: "native_hysteria2"}
	ackResponse := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/desired/1/ack", ack, credentials.NodeCredential, cryptoutil.NewID())
	requireStatus(t, ackResponse, http.StatusNoContent)
	storedNode, err = app.store.GetNode(context.Background(), node.ID)
	if err != nil || storedNode.AppliedVersion != 1 || storedNode.LastAppliedAt == nil {
		t.Fatalf("desired acknowledgement not persisted: node=%#v err=%v", storedNode, err)
	}
	upToDate := agentRequest(t, app.handler, http.MethodGet, "/agent/v1/desired?after=1", nil, credentials.NodeCredential, cryptoutil.NewID())
	requireStatus(t, upToDate, http.StatusNoContent)

	var samples int
	if err := app.store.DB().QueryRow("SELECT COUNT(*) FROM node_metric_samples WHERE node_id = ?", node.ID).Scan(&samples); err != nil || samples != 1 {
		t.Fatalf("metric sample count = %d, err = %v", samples, err)
	}
}

func TestVLESSRealityHeartbeatPersistsTypedProbeState(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	created := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "Reality heartbeat", "adapter_type": store.AdapterSingBoxVLESSReality,
		"public_host": "reality.example.com", "public_port": 24443,
		"sni": "www.microsoft.com",
		"reality": map[string]any{
			"handshake_server": "www.microsoft.com", "handshake_port": 443,
		},
	}, app.csrf, "")
	requireStatus(t, created, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, created, &node)
	tokenResponse := app.request(t, http.MethodPost,
		"/api/v1/nodes/"+node.ID+"/enrollment-token", map[string]any{}, app.csrf, "")
	requireStatus(t, tokenResponse, http.StatusCreated)
	var token struct {
		EnrollmentToken string `json:"enrollment_token"`
	}
	decodeResponse(t, tokenResponse, &token)

	installationID := cryptoutil.NewID()
	requestID := cryptoutil.NewID()
	enrolled := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/enroll", protocol.EnrollRequest{
		EnrollmentToken: token.EnrollmentToken, InstallationID: installationID,
		RequestID: requestID, AgentVersion: "v0.1.0-reality-test", OS: "linux",
		OSVersion: "24.04", Architecture: "amd64",
		Capabilities: []string{
			"desired_state_v2", "credential_material_v1", "sing_box_vless_reality",
			"reality_user_control_v1",
		},
		Adapter: protocol.EnrollmentAdapter{
			Type: store.AdapterSingBoxVLESSReality, CoreName: "sing-box",
		},
	}, "", requestID)
	requireStatus(t, enrolled, http.StatusOK)
	var credentials protocol.EnrollResponse
	decodeResponse(t, enrolled, &credentials)

	probedAt := time.Now().UTC().Truncate(time.Second)
	heartbeat := protocol.HeartbeatRequest{
		InstallationID: installationID,
		Agent: protocol.AgentInfo{
			Version: "v0.1.0-reality-test", Protocol: protocol.MajorVersion,
		},
		Core: protocol.CoreInfo{Name: "sing-box", Version: "1.13.18", Running: true},
		Adapter: protocol.AdapterInfo{
			Name: store.AdapterSingBoxVLESSReality, Version: "1.13.18", Status: "compatible",
			LastProbedAt: &probedAt,
		},
		Host:      protocol.HostMetrics{MemoryTotalBytes: 1, DiskTotalBytes: 1},
		SampledAt: probedAt,
	}
	beat := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/heartbeat",
		heartbeat, credentials.NodeCredential, cryptoutil.NewID())
	requireStatus(t, beat, http.StatusOK)
	storedNode, err := app.store.GetNode(context.Background(), node.ID)
	if err != nil {
		t.Fatalf("GetNode() error = %v", err)
	}
	if storedNode.AdapterStatus != "compatible" || storedNode.AdapterVersion != "1.13.18" ||
		storedNode.AdapterErrorCode != "" || storedNode.AdapterLastProbedAt == nil ||
		!storedNode.AdapterLastProbedAt.Equal(probedAt) || storedNode.CoreName != "sing-box" ||
		storedNode.CoreVersion != "1.13.18" || !storedNode.CoreRunning ||
		storedNode.Status != "online" {
		t.Fatalf("Reality heartbeat probe was not persisted: %#v", storedNode)
	}

	heartbeat.Core.Running = false
	heartbeat.Adapter.Status = "incompatible"
	heartbeat.Adapter.ErrorCode = "reality_binary_incompatible"
	heartbeat.SampledAt = probedAt.Add(time.Second)
	secondBeat := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/heartbeat",
		heartbeat, credentials.NodeCredential, cryptoutil.NewID())
	requireStatus(t, secondBeat, http.StatusOK)
	storedNode, err = app.store.GetNode(context.Background(), node.ID)
	if err != nil || storedNode.AdapterStatus != "incompatible" ||
		storedNode.AdapterErrorCode != "reality_binary_incompatible" || storedNode.CoreRunning ||
		storedNode.Status != "degraded" {
		t.Fatalf("incompatible Reality probe was not persisted: %#v, %v", storedNode, err)
	}
}

func TestAgentTrafficAndOnlineEndpoints(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	createdNode := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "usage-node", "provider": "local", "region": "test",
		"adapter_type": "native_hysteria2",
	}, app.csrf, "")
	requireStatus(t, createdNode, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, createdNode, &node)

	tokenResponse := app.request(t, http.MethodPost, "/api/v1/nodes/"+node.ID+"/enrollment-token", map[string]any{}, app.csrf, "")
	requireStatus(t, tokenResponse, http.StatusCreated)
	var token struct {
		EnrollmentToken string `json:"enrollment_token"`
	}
	decodeResponse(t, tokenResponse, &token)
	installationID := cryptoutil.NewID()
	enrollmentRequestID := cryptoutil.NewID()
	enrolled := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/enroll", protocol.EnrollRequest{
		EnrollmentToken: token.EnrollmentToken,
		InstallationID:  installationID,
		RequestID:       enrollmentRequestID,
		AgentVersion:    "v0.3.0-test",
		OS:              "linux",
		OSVersion:       "24.04",
		Architecture:    "amd64",
		Capabilities:    []string{"traffic_outbox", "online_users", "kick_user"},
		Adapter:         protocol.EnrollmentAdapter{Type: "native_hysteria2", CoreName: "hysteria"},
	}, "", enrollmentRequestID)
	requireStatus(t, enrolled, http.StatusOK)
	var enrollment protocol.EnrollResponse
	decodeResponse(t, enrolled, &enrollment)

	createdUser := app.request(t, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "usage-user", "enabled": true, "node_ids": []string{node.ID},
	}, app.csrf, "")
	requireStatus(t, createdUser, http.StatusCreated)
	var userPayload struct {
		User userResponse `json:"user"`
	}
	decodeResponse(t, createdUser, &userPayload)
	userID := userPayload.User.ID
	now := time.Now().UTC().Truncate(time.Millisecond)
	batch := protocol.TrafficBatch{
		ID:             cryptoutil.NewID(),
		InstallationID: installationID,
		SourceEpoch:    cryptoutil.NewID(),
		Sequence:       1,
		SampledAt:      now,
		Items: []protocol.TrafficDelta{{
			UserID: userID, UploadBytes: 256, DownloadBytes: 512,
		}},
	}
	trafficBody := protocol.TrafficBatchesRequest{Batches: []protocol.TrafficBatch{batch}}
	traffic := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/traffic-batches",
		trafficBody, enrollment.NodeCredential, cryptoutil.NewID())
	requireStatus(t, traffic, http.StatusOK)
	var trafficResult protocol.TrafficBatchesResponse
	decodeResponse(t, traffic, &trafficResult)
	if len(trafficResult.Results) != 1 || trafficResult.Results[0].Status != "accepted" {
		t.Fatalf("traffic result = %#v, want accepted", trafficResult.Results)
	}

	duplicate := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/traffic-batches",
		trafficBody, enrollment.NodeCredential, cryptoutil.NewID())
	requireStatus(t, duplicate, http.StatusOK)
	decodeResponse(t, duplicate, &trafficResult)
	if len(trafficResult.Results) != 1 || trafficResult.Results[0].Status != "duplicate" {
		t.Fatalf("duplicate traffic result = %#v, want duplicate", trafficResult.Results)
	}

	online := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/online-snapshot", protocol.OnlineSnapshotRequest{
		SnapshotID: installationID, InstallationID: installationID, SampledAt: now,
		Users: []protocol.OnlineUser{{UserID: userID, Connections: 3}},
	}, enrollment.NodeCredential, cryptoutil.NewID())
	requireStatus(t, online, http.StatusOK)
	var onlineResult protocol.OnlineSnapshotResponse
	decodeResponse(t, online, &onlineResult)
	if !onlineResult.Accepted {
		t.Fatal("online snapshot was not accepted")
	}

	user, err := app.store.GetUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if user.TrafficUploadBytes != 256 || user.TrafficDownloadBytes != 512 ||
		len(user.Assignments) != 1 || user.Assignments[0].OnlineConnections != 3 {
		t.Fatalf("usage state = %#v", user)
	}
}

func agentRequest(t *testing.T, handler http.Handler, method, path string, body any, credential, requestID string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, "http://hyfleet.test"+path, reader)
	request.RemoteAddr = "198.51.100.10:31200"
	request.Header.Set("X-HyFleet-Protocol", strconv.Itoa(protocol.MajorVersion))
	request.Header.Set("X-Request-ID", requestID)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestLoginRateLimit(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	for attempt := 1; attempt <= 9; attempt++ {
		response := app.request(t, http.MethodPost, "/api/v1/auth/login", map[string]any{
			"username": "admin", "password": "definitely wrong",
		}, "", "")
		want := http.StatusUnauthorized
		if attempt == 9 {
			want = http.StatusTooManyRequests
		}
		requireStatus(t, response, want)
	}
}

func TestUserLifecycleAPI(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)

	createNode := func(name, adapter string) nodeResponse {
		t.Helper()
		response := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
			"name": name, "provider": "Test", "region": "Test",
			"adapter_type": adapter,
		}, app.csrf, "http://hyfleet.test")
		requireStatus(t, response, http.StatusCreated)
		var node nodeResponse
		decodeResponse(t, response, &node)
		return node
	}
	nativeOne := createNode("native-one", "native_hysteria2")
	nativeTwo := createNode("native-two", "native_hysteria2")
	unsupportedNode := createNode("standalone", "standalone_sing_box")

	withoutCSRF := app.request(t, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "alice", "enabled": true,
	}, "", "")
	requireStatus(t, withoutCSRF, http.StatusForbidden)

	created := app.request(t, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "alice", "display_name": "Alice", "notes": "first user",
		"enabled": true, "traffic_limit_bytes": 1000, "node_ids": []string{nativeOne.ID},
	}, app.csrf, "http://hyfleet.test")
	requireStatus(t, created, http.StatusCreated)
	requireCredentialNoStore(t, created)
	var createdPayload struct {
		User        userResponse         `json:"user"`
		Credentials []credentialResponse `json:"credentials"`
	}
	decodeResponse(t, created, &createdPayload)
	if createdPayload.User.Username != "alice" || createdPayload.User.Status != "active" ||
		createdPayload.User.TrafficLimitBytes != 1000 || len(createdPayload.User.Assignments) != 1 ||
		len(createdPayload.Credentials) != 1 {
		t.Fatalf("unexpected created user: %#v", createdPayload)
	}
	firstSecret := createdPayload.Credentials[0].Credential
	if firstSecret == "" || createdPayload.Credentials[0].NodeID != nativeOne.ID {
		t.Fatalf("unexpected created credential: %#v", createdPayload.Credentials[0])
	}

	duplicate := app.request(t, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "ALICE", "enabled": true,
	}, app.csrf, "")
	requireStatus(t, duplicate, http.StatusConflict)

	listed := app.request(t, http.MethodGet, "/api/v1/users", nil, "", "")
	requireStatus(t, listed, http.StatusOK)
	listedBody := listed.Body.String()
	for _, forbidden := range []string{"verifier_sha256", "secret_ciphertext", firstSecret} {
		if strings.Contains(listedBody, forbidden) {
			t.Fatalf("user list leaked %q: %s", forbidden, listedBody)
		}
	}
	var list struct {
		Users []userResponse `json:"users"`
	}
	decodeResponse(t, listed, &list)
	if len(list.Users) != 1 || list.Users[0].ID != createdPayload.User.ID {
		t.Fatalf("unexpected users list: %#v", list.Users)
	}

	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	updated := app.request(t, http.MethodPut, "/api/v1/users/"+createdPayload.User.ID, map[string]any{
		"username": "alice", "display_name": "Alice Updated", "notes": "updated",
		"enabled": false, "expires_at": expiresAt,
	}, app.csrf, "")
	requireStatus(t, updated, http.StatusOK)
	var updatedUser userResponse
	decodeResponse(t, updated, &updatedUser)
	if updatedUser.Enabled || updatedUser.Status != "disabled" || updatedUser.ExpiresAt == nil {
		t.Fatalf("unexpected updated user: %#v", updatedUser)
	}
	if updatedUser.TrafficLimitBytes != 1000 {
		t.Fatalf("omitted traffic limit was not preserved: %#v", updatedUser)
	}
	invalidLimit := app.request(t, http.MethodPut, "/api/v1/users/"+createdPayload.User.ID, map[string]any{
		"username": "alice", "display_name": "Alice Updated", "notes": "updated",
		"enabled": false, "expires_at": expiresAt, "traffic_limit_bytes": -1,
	}, app.csrf, "")
	requireStatus(t, invalidLimit, http.StatusUnprocessableEntity)

	assigned := app.request(t, http.MethodPost,
		"/api/v1/users/"+createdPayload.User.ID+"/assignments", map[string]any{
			"node_id": nativeTwo.ID, "traffic_limit_bytes": 300,
		}, app.csrf, "")
	requireStatus(t, assigned, http.StatusCreated)
	requireCredentialNoStore(t, assigned)
	var assignedPayload struct {
		User       userResponse       `json:"user"`
		Credential credentialResponse `json:"credential"`
	}
	decodeResponse(t, assigned, &assignedPayload)
	if assignedPayload.Credential.Credential == "" ||
		assignedPayload.Credential.Credential == firstSecret ||
		len(assignedPayload.User.Assignments) != 2 || assignedPayload.Credential.NodeID != nativeTwo.ID {
		t.Fatalf("unexpected second assignment: %#v", assignedPayload)
	}

	unsupported := app.request(t, http.MethodPost,
		"/api/v1/users/"+createdPayload.User.ID+"/assignments", map[string]any{
			"node_id": unsupportedNode.ID,
		}, app.csrf, "")
	requireStatus(t, unsupported, http.StatusUnprocessableEntity)

	disabled := app.request(t, http.MethodPut,
		"/api/v1/users/"+createdPayload.User.ID+"/assignments/"+nativeTwo.ID,
		map[string]any{"enabled": false, "traffic_limit_bytes": 250}, app.csrf, "")
	requireStatus(t, disabled, http.StatusOK)
	var disabledUser userResponse
	decodeResponse(t, disabled, &disabledUser)
	if len(disabledUser.Assignments) != 2 || disabledUser.Assignments[1].Enabled ||
		disabledUser.Assignments[1].TrafficLimitBytes != 250 {
		t.Fatalf("assignment was not disabled: %#v", disabledUser.Assignments)
	}

	kicked := app.request(t, http.MethodPost, "/api/v1/users/"+createdPayload.User.ID+"/kick",
		map[string]any{}, app.csrf, "")
	requireStatus(t, kicked, http.StatusAccepted)
	var kickResult struct {
		RequestedNodes int `json:"requested_nodes"`
	}
	decodeResponse(t, kicked, &kickResult)
	if kickResult.RequestedNodes != 2 {
		t.Fatalf("requested kick nodes = %d, want 2", kickResult.RequestedNodes)
	}

	revealPath := "/api/v1/users/" + createdPayload.User.ID + "/assignments/" + nativeOne.ID + "/credential"
	revealWithoutCSRF := app.request(t, http.MethodPost, revealPath, map[string]any{}, "", "")
	requireStatus(t, revealWithoutCSRF, http.StatusForbidden)
	revealed := app.request(t, http.MethodPost, revealPath, map[string]any{}, app.csrf, "")
	requireStatus(t, revealed, http.StatusOK)
	requireCredentialNoStore(t, revealed)
	var revealedCredential credentialResponse
	decodeResponse(t, revealed, &revealedCredential)
	if revealedCredential.Credential != firstSecret {
		t.Fatalf("revealed credential = %q, want original", revealedCredential.Credential)
	}

	unassigned := app.request(t, http.MethodDelete,
		"/api/v1/users/"+createdPayload.User.ID+"/assignments/"+nativeTwo.ID,
		nil, app.csrf, "")
	requireStatus(t, unassigned, http.StatusNoContent)

	archived := app.request(t, http.MethodDelete, "/api/v1/users/"+createdPayload.User.ID, nil, app.csrf, "")
	requireStatus(t, archived, http.StatusNoContent)
	notFound := app.request(t, http.MethodGet, "/api/v1/users/"+createdPayload.User.ID, nil, "", "")
	requireStatus(t, notFound, http.StatusNotFound)
	listed = app.request(t, http.MethodGet, "/api/v1/users", nil, "", "")
	requireStatus(t, listed, http.StatusOK)
	decodeResponse(t, listed, &list)
	if len(list.Users) != 0 {
		t.Fatalf("users after archive = %d, want 0", len(list.Users))
	}
}

package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

func TestSUIAdapterAPIAndCredentialMaterialContract(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	created := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "sui-node", "provider": "local", "region": "test", "adapter_type": "s_ui",
		"public_host": "sui.example.test", "public_port": 443,
	}, app.csrf, "")
	requireStatus(t, created, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, created, &node)
	installationID, agentCredential := enrollSUIAgentForTest(t, app, node.ID)

	probedAt := time.Now().UTC().Truncate(time.Millisecond)
	report := protocol.SUIReportRequest{
		InstallationID: installationID,
		Adapter: protocol.AdapterInfo{
			Name: "s_ui", Version: "v1.5.3", Status: "compatible", LastProbedAt: &probedAt,
		},
		Core: protocol.CoreInfo{Name: "sing-box", Running: true},
		Inbounds: []protocol.SUIDiscoveredInbound{{
			RemoteID: 7, Tag: "hy2-in", Type: "hysteria2", Listen: "::", ListenPort: 443,
		}},
		Clients: []protocol.SUIDiscoveredClient{{
			RemoteID: 41, Name: "existing-client", Enabled: true,
			InboundIDs: []int64{7}, UploadBytes: 10, DownloadBytes: 20,
		}},
		SampledAt: probedAt,
	}
	reported := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/s-ui-report",
		report, agentCredential, cryptoutil.NewID())
	requireStatus(t, reported, http.StatusOK)

	stateResponse := app.request(t, http.MethodGet, "/api/v1/nodes/"+node.ID+"/s-ui", nil, "", "")
	requireStatus(t, stateResponse, http.StatusOK)
	if strings.Contains(stateResponse.Body.String(), "password") ||
		strings.Contains(stateResponse.Body.String(), "config") {
		t.Fatalf("S-UI discovery response exposed a secret-bearing field: %s", stateResponse.Body)
	}
	var state suiStateResponse
	decodeResponse(t, stateResponse, &state)
	if state.AdapterStatus != "compatible" || state.AdapterVersion != "v1.5.3" ||
		len(state.Inbounds) != 1 || len(state.Clients) != 1 {
		t.Fatalf("S-UI state = %#v", state)
	}

	invalidTarget := app.request(t, http.MethodPut, "/api/v1/nodes/"+node.ID+"/s-ui/targets",
		map[string]any{"inbound_ids": []int64{999}}, app.csrf, "")
	requireStatus(t, invalidTarget, http.StatusConflict)
	target := app.request(t, http.MethodPut, "/api/v1/nodes/"+node.ID+"/s-ui/targets",
		map[string]any{"inbound_ids": []int64{7}}, app.csrf, "")
	requireStatus(t, target, http.StatusOK)

	createdUser := app.request(t, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "mapped-user", "enabled": true,
	}, app.csrf, "")
	requireStatus(t, createdUser, http.StatusCreated)
	var userPayload struct {
		User userResponse `json:"user"`
	}
	decodeResponse(t, createdUser, &userPayload)
	imported := app.request(t, http.MethodPost,
		"/api/v1/nodes/"+node.ID+"/s-ui/clients/41/import",
		map[string]any{"user_id": userPayload.User.ID}, app.csrf, "")
	requireStatus(t, imported, http.StatusCreated)
	var importedUser userResponse
	decodeResponse(t, imported, &importedUser)
	if len(importedUser.Assignments) != 1 ||
		importedUser.Assignments[0].ManagementMode != "read_only" ||
		importedUser.Assignments[0].RemoteClientID != 41 ||
		importedUser.Assignments[0].SubscriptionEligible ||
		importedUser.Assignments[0].SubscriptionReason != "read_only_requires_adoption" {
		t.Fatalf("imported user = %#v", importedUser)
	}
	readOnlyLimit := app.request(t, http.MethodPut,
		"/api/v1/users/"+userPayload.User.ID+"/assignments/"+node.ID,
		map[string]any{"traffic_limit_bytes": int64(5 << 30)}, app.csrf, "")
	requireStatus(t, readOnlyLimit, http.StatusConflict)

	desired := agentRequest(t, app.handler, http.MethodGet, "/agent/v1/desired?after=0",
		nil, agentCredential, cryptoutil.NewID())
	requireStatus(t, desired, http.StatusOK)
	var readOnlyEnvelope protocol.DesiredEnvelope
	decodeResponse(t, desired, &readOnlyEnvelope)
	adoptTooSoon := app.request(t, http.MethodPost,
		"/api/v1/nodes/"+node.ID+"/s-ui/clients/41/adopt",
		map[string]any{"confirm_name": "existing-client"}, app.csrf, "")
	requireStatus(t, adoptTooSoon, http.StatusConflict)
	ack := agentRequest(t, app.handler, http.MethodPost,
		"/agent/v1/desired/"+jsonNumber(readOnlyEnvelope.Snapshot.Version)+"/ack",
		protocol.DesiredAckRequest{
			Status: "applied", SnapshotHash: readOnlyEnvelope.SHA256, Adapter: "s_ui",
		}, agentCredential, cryptoutil.NewID())
	requireStatus(t, ack, http.StatusNoContent)

	wrongConfirmation := app.request(t, http.MethodPost,
		"/api/v1/nodes/"+node.ID+"/s-ui/clients/41/adopt",
		map[string]any{"confirm_name": "wrong-name"}, app.csrf, "")
	requireStatus(t, wrongConfirmation, http.StatusConflict)
	adopted := app.request(t, http.MethodPost,
		"/api/v1/nodes/"+node.ID+"/s-ui/clients/41/adopt",
		map[string]any{"confirm_name": "existing-client"}, app.csrf, "")
	requireStatus(t, adopted, http.StatusAccepted)

	managedDesired := agentRequest(t, app.handler, http.MethodGet,
		"/agent/v1/desired?after="+jsonNumber(readOnlyEnvelope.Snapshot.Version),
		nil, agentCredential, cryptoutil.NewID())
	requireStatus(t, managedDesired, http.StatusOK)
	var managedEnvelope protocol.DesiredEnvelope
	decodeResponse(t, managedDesired, &managedEnvelope)
	if len(managedEnvelope.Snapshot.Users) != 1 ||
		managedEnvelope.Snapshot.Users[0].ManagementMode != "managed" ||
		managedEnvelope.Snapshot.Users[0].Credential.VerifierSHA256 != "" {
		t.Fatalf("managed S-UI desired state = %#v", managedEnvelope.Snapshot)
	}
	credentialRef := managedEnvelope.Snapshot.Users[0].Credential.Ref
	material := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/credential-material",
		protocol.CredentialMaterialRequest{
			CredentialRef: credentialRef, DesiredVersion: managedEnvelope.Snapshot.Version,
			SnapshotSHA256: managedEnvelope.SHA256,
		}, agentCredential, cryptoutil.NewID())
	requireStatus(t, material, http.StatusOK)
	requireCredentialNoStore(t, material)
	var materialPayload protocol.CredentialMaterialResponse
	decodeResponse(t, material, &materialPayload)
	if materialPayload.CredentialRef != credentialRef || materialPayload.Secret == "" {
		t.Fatalf("credential material response = %#v", materialPayload)
	}

	stale := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/credential-material",
		protocol.CredentialMaterialRequest{
			CredentialRef: credentialRef, DesiredVersion: readOnlyEnvelope.Snapshot.Version,
			SnapshotSHA256: readOnlyEnvelope.SHA256,
		}, agentCredential, cryptoutil.NewID())
	requireStatus(t, stale, http.StatusForbidden)
	requireCredentialNoStore(t, stale)
	if strings.Contains(stale.Body.String(), materialPayload.Secret) {
		t.Fatal("denied credential response leaked plaintext")
	}

	managedAck := agentRequest(t, app.handler, http.MethodPost,
		"/agent/v1/desired/"+jsonNumber(managedEnvelope.Snapshot.Version)+"/ack",
		protocol.DesiredAckRequest{
			Status: "applied", SnapshotHash: managedEnvelope.SHA256, Adapter: "s_ui",
		}, agentCredential, cryptoutil.NewID())
	requireStatus(t, managedAck, http.StatusNoContent)
	managedUserResponse := app.request(t, http.MethodGet,
		"/api/v1/users/"+userPayload.User.ID, nil, "", "")
	requireStatus(t, managedUserResponse, http.StatusOK)
	var managedUser userResponse
	decodeResponse(t, managedUserResponse, &managedUser)
	if len(managedUser.Assignments) != 1 || !managedUser.Assignments[0].SubscriptionEligible ||
		managedUser.Assignments[0].SubscriptionReason != "" {
		t.Fatalf("managed assignment did not enter subscription: %#v", managedUser.Assignments)
	}

	updatedLimit := app.request(t, http.MethodPut,
		"/api/v1/users/"+userPayload.User.ID+"/assignments/"+node.ID,
		map[string]any{"traffic_limit_bytes": int64(5 << 30)}, app.csrf, "")
	requireStatus(t, updatedLimit, http.StatusOK)
	quotaDesired := agentRequest(t, app.handler, http.MethodGet,
		"/agent/v1/desired?after="+jsonNumber(managedEnvelope.Snapshot.Version),
		nil, agentCredential, cryptoutil.NewID())
	requireStatus(t, quotaDesired, http.StatusOK)
	var quotaEnvelope protocol.DesiredEnvelope
	decodeResponse(t, quotaDesired, &quotaEnvelope)
	quotaAck := agentRequest(t, app.handler, http.MethodPost,
		"/agent/v1/desired/"+jsonNumber(quotaEnvelope.Snapshot.Version)+"/ack",
		protocol.DesiredAckRequest{
			Status: "applied", SnapshotHash: quotaEnvelope.SHA256, Adapter: "s_ui",
		}, agentCredential, cryptoutil.NewID())
	requireStatus(t, quotaAck, http.StatusNoContent)

	tokenResponse := app.request(t, http.MethodPost,
		"/api/v1/users/"+userPayload.User.ID+"/subscription-tokens",
		map[string]any{"name": "clash", "allowed_formats": []string{"clash"}}, app.csrf, "")
	requireStatus(t, tokenResponse, http.StatusCreated)
	var issued issuedSubscriptionTokenResponse
	decodeResponse(t, tokenResponse, &issued)
	clashURL, err := url.Parse(issued.URLs.Clash)
	if err != nil {
		t.Fatalf("parse Clash subscription URL: %v", err)
	}
	clash := app.request(t, http.MethodGet, clashURL.RequestURI(), nil, "", "")
	requireStatus(t, clash, http.StatusOK)
	if !strings.Contains(clash.Body.String(), "name: sui-node") {
		t.Fatalf("managed S-UI node missing from Clash subscription: %s", clash.Body.String())
	}
}

func TestValidSUIReportRejectsFutureTimestamps(t *testing.T) {
	now := time.Now().UTC()
	probedAt := now
	report := protocol.SUIReportRequest{
		InstallationID: cryptoutil.NewID(),
		Adapter: protocol.AdapterInfo{
			Name: "s_ui", Version: "v1.5.3", Status: "compatible", LastProbedAt: &probedAt,
		},
		Core:      protocol.CoreInfo{Name: "sing-box", Running: true},
		SampledAt: now,
	}
	if !validSUIReport(report, now) {
		t.Fatal("current S-UI report should be valid")
	}
	futureSample := now.Add(11 * time.Minute)
	report.SampledAt = futureSample
	if validSUIReport(report, now) {
		t.Fatal("future S-UI sample should be rejected")
	}
	report.SampledAt = now
	report.Adapter.LastProbedAt = &futureSample
	if validSUIReport(report, now) {
		t.Fatal("future S-UI probe should be rejected")
	}
}

func enrollSUIAgentForTest(t *testing.T, app *testApp, nodeID string) (string, string) {
	t.Helper()
	tokenResponse := app.request(t, http.MethodPost,
		"/api/v1/nodes/"+nodeID+"/enrollment-token", map[string]any{}, app.csrf, "")
	requireStatus(t, tokenResponse, http.StatusCreated)
	var token struct {
		EnrollmentToken string `json:"enrollment_token"`
	}
	decodeResponse(t, tokenResponse, &token)
	installationID := cryptoutil.NewID()
	requestID := cryptoutil.NewID()
	enrolled := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/enroll", protocol.EnrollRequest{
		EnrollmentToken: token.EnrollmentToken, InstallationID: installationID,
		RequestID: requestID, AgentVersion: "v0.5.0-test", OS: "linux",
		OSVersion: "24.04", Architecture: "amd64",
		Capabilities: []string{"sui_apiv2_v1", "host_metrics"},
		Adapter:      protocol.EnrollmentAdapter{Type: "s_ui", CoreName: "sing-box"},
	}, "", requestID)
	requireStatus(t, enrolled, http.StatusOK)
	var enrollment protocol.EnrollResponse
	decodeResponse(t, enrolled, &enrollment)
	return installationID, enrollment.NodeCredential
}

func jsonNumber(value int64) string {
	data, _ := json.Marshal(value)
	return string(data)
}

package server

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/Sen62455/PolyFleet/internal/store"
)

func TestVLESSRealityReenrollmentInvalidatesInstallationAppliedState(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	createdNode := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "Reality material bearer", "adapter_type": store.AdapterSingBoxVLESSReality,
		"public_host": "reality.example.com", "public_port": 24443,
		"sni": "www.microsoft.com",
		"reality": map[string]any{
			"handshake_server": "www.microsoft.com", "handshake_port": 443,
		},
	}, app.csrf, "")
	requireStatus(t, createdNode, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, createdNode, &node)

	createdUser := app.request(t, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "reality-material-bearer", "enabled": true,
		"node_ids": []string{node.ID},
	}, app.csrf, "")
	requireStatus(t, createdUser, http.StatusCreated)
	var userPayload struct {
		User userResponse `json:"user"`
	}
	decodeResponse(t, createdUser, &userPayload)

	enroll := func() (protocol.EnrollResponse, string) {
		t.Helper()
		tokenResponse := app.request(t, http.MethodPost,
			"/api/v1/nodes/"+node.ID+"/enrollment-token", map[string]any{}, app.csrf, "")
		requireStatus(t, tokenResponse, http.StatusCreated)
		var token struct {
			EnrollmentToken string `json:"enrollment_token"`
		}
		decodeResponse(t, tokenResponse, &token)
		installationID := cryptoutil.NewID()
		requestID := cryptoutil.NewID()
		response := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/enroll",
			protocol.EnrollRequest{
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
		requireStatus(t, response, http.StatusOK)
		var enrollment protocol.EnrollResponse
		decodeResponse(t, response, &enrollment)
		return enrollment, installationID
	}
	heartbeat := func(enrollment protocol.EnrollResponse, installationID string, appliedVersion int64) {
		t.Helper()
		probedAt := time.Now().UTC().Truncate(time.Millisecond)
		response := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/heartbeat",
			protocol.HeartbeatRequest{
				InstallationID: installationID, AppliedVersion: appliedVersion,
				Agent: protocol.AgentInfo{
					Version: "v0.1.0-reality-test", Protocol: protocol.MajorVersion,
				},
				Core: protocol.CoreInfo{Name: "sing-box", Version: "1.13.18", Running: true},
				Adapter: protocol.AdapterInfo{
					Name: store.AdapterSingBoxVLESSReality, Version: "1.13.18",
					Status: "compatible", LastProbedAt: &probedAt,
				},
				Host:      protocol.HostMetrics{MemoryTotalBytes: 1, DiskTotalBytes: 1},
				SampledAt: probedAt,
			}, enrollment.NodeCredential, cryptoutil.NewID())
		requireStatus(t, response, http.StatusOK)
	}

	first, firstInstallationID := enroll()
	desiredResponse := agentRequest(t, app.handler, http.MethodGet,
		"/agent/v1/desired?after=0", nil, first.NodeCredential, cryptoutil.NewID())
	requireStatus(t, desiredResponse, http.StatusOK)
	var desired protocol.DesiredEnvelope
	decodeResponse(t, desiredResponse, &desired)
	if len(desired.Snapshot.Users) != 1 {
		t.Fatalf("Reality desired users = %d, want 1", len(desired.Snapshot.Users))
	}
	materialRequest := protocol.CredentialMaterialRequest{
		CredentialRef:  desired.Snapshot.Users[0].Credential.Ref,
		DesiredVersion: desired.Snapshot.Version, SnapshotSHA256: desired.SHA256,
	}
	initialMaterial := agentRequest(t, app.handler, http.MethodPost,
		"/agent/v1/credential-material", materialRequest, first.NodeCredential, cryptoutil.NewID())
	requireStatus(t, initialMaterial, http.StatusOK)
	requireCredentialNoStore(t, initialMaterial)
	var initialPayload protocol.CredentialMaterialResponse
	decodeResponse(t, initialMaterial, &initialPayload)
	if initialPayload.Secret == "" {
		t.Fatal("initial credential material is empty")
	}
	appliedMaterial := &protocol.AppliedRealityMaterial{
		KeyGeneration: 1,
		PublicKey:     base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x71}, 32)),
		ShortID:       "0123456789abcdef",
	}
	ack := protocol.DesiredAckRequest{
		Status: "applied", SnapshotHash: desired.SHA256,
		Adapter: store.AdapterSingBoxVLESSReality, Reality: appliedMaterial,
	}
	ackResponse := agentRequest(t, app.handler, http.MethodPost,
		"/agent/v1/desired/"+strconv.FormatInt(desired.Snapshot.Version, 10)+"/ack",
		ack, first.NodeCredential, cryptoutil.NewID())
	requireStatus(t, ackResponse, http.StatusNoContent)
	heartbeat(first, firstInstallationID, desired.Snapshot.Version)

	issued, err := app.store.CreateSubscriptionToken(t.Context(), store.NewSubscriptionToken{
		ID: cryptoutil.NewID(), UserID: userPayload.User.ID, Name: "re-enrollment",
		AllowedFormats: []string{"uri"}, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CreateSubscriptionToken() error = %v", err)
	}
	subscriptionPath := "/sub/" + issued.Secret + "/uri"
	initialSubscription := app.request(t, http.MethodGet, subscriptionPath, nil, "", "")
	requireStatus(t, initialSubscription, http.StatusOK)
	if !strings.Contains(initialSubscription.Body.String(), appliedMaterial.PublicKey) {
		t.Fatalf("initial Reality subscription missing applied public key: %s", initialSubscription.Body.String())
	}

	second, secondInstallationID := enroll()
	if second.NodeCredential == first.NodeCredential {
		t.Fatal("re-enrollment reused the previous Agent credential")
	}
	reenrolledNode, err := app.store.GetNode(t.Context(), node.ID)
	if err != nil {
		t.Fatalf("GetNode(re-enrolled) error = %v", err)
	}
	if reenrolledNode.DesiredVersion != desired.Snapshot.Version ||
		reenrolledNode.AppliedVersion != 0 || reenrolledNode.Status != "pending" ||
		reenrolledNode.AdapterStatus != "unknown" || reenrolledNode.CoreRunning ||
		reenrolledNode.VLESSReality == nil ||
		reenrolledNode.VLESSReality.PublicKey != appliedMaterial.PublicKey ||
		reenrolledNode.VLESSReality.ShortID != appliedMaterial.ShortID ||
		reenrolledNode.VLESSReality.AppliedKeyGeneration != 0 ||
		reenrolledNode.VLESSReality.MaterialAppliedVersion != 0 {
		t.Fatalf("re-enrollment did not invalidate installation state: %#v", reenrolledNode)
	}
	reenrolledUser, err := app.store.GetUser(t.Context(), userPayload.User.ID)
	if err != nil || len(reenrolledUser.Assignments) != 1 {
		t.Fatalf("GetUser(re-enrolled) = %#v, %v", reenrolledUser, err)
	}
	assignment := reenrolledUser.Assignments[0]
	if assignment.DesiredVersion != desired.Snapshot.Version ||
		assignment.AppliedVersion != 0 || assignment.AppliedCredentialID != "" ||
		assignment.State != "pending" || assignment.SubscriptionEligible {
		t.Fatalf("re-enrollment did not invalidate assignment state: %#v", assignment)
	}
	oldBearer := agentRequest(t, app.handler, http.MethodPost,
		"/agent/v1/credential-material", materialRequest, first.NodeCredential, cryptoutil.NewID())
	requireStatus(t, oldBearer, http.StatusUnauthorized)
	if strings.Contains(oldBearer.Body.String(), initialPayload.Secret) {
		t.Fatal("old bearer denial leaked credential material")
	}

	newBearer := agentRequest(t, app.handler, http.MethodPost,
		"/agent/v1/credential-material", materialRequest, second.NodeCredential, cryptoutil.NewID())
	requireStatus(t, newBearer, http.StatusOK)
	requireCredentialNoStore(t, newBearer)
	var newPayload protocol.CredentialMaterialResponse
	decodeResponse(t, newBearer, &newPayload)
	if newPayload.CredentialRef != initialPayload.CredentialRef ||
		newPayload.Secret != initialPayload.Secret {
		t.Fatalf("re-enrolled material response changed: %#v", newPayload)
	}

	hash, err := base64.RawURLEncoding.DecodeString(desired.SHA256)
	if err != nil {
		t.Fatalf("decode desired hash: %v", err)
	}
	if err := app.store.AcknowledgeDesiredWithMaterial(t.Context(), store.AgentIdentity{
		NodeID: node.ID, InstallationID: firstInstallationID,
		AdapterType: store.AdapterSingBoxVLESSReality, Enabled: true,
	}, desired.Snapshot.Version, hash, "applied", "", "", appliedMaterial,
		time.Now().UTC()); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("old installation acknowledgement error = %v", err)
	}
	heartbeat(second, secondInstallationID, 0)
	withheld := app.request(t, http.MethodGet, subscriptionPath, nil, "", "")
	requireStatus(t, withheld, http.StatusOK)
	if withheld.Body.Len() != 0 {
		t.Fatalf("re-enrolled node was published before exact ACK: %s", withheld.Body.String())
	}

	newAck := agentRequest(t, app.handler, http.MethodPost,
		"/agent/v1/desired/"+strconv.FormatInt(desired.Snapshot.Version, 10)+"/ack",
		ack, second.NodeCredential, cryptoutil.NewID())
	requireStatus(t, newAck, http.StatusNoContent)
	restored := app.request(t, http.MethodGet, subscriptionPath, nil, "", "")
	requireStatus(t, restored, http.StatusOK)
	if !strings.Contains(restored.Body.String(), appliedMaterial.PublicKey) {
		t.Fatalf("subscription was not restored after exact ACK: %s", restored.Body.String())
	}
}

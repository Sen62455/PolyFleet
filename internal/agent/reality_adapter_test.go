package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/config"
	"github.com/Sen62455/PolyFleet/internal/nodeops"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

func TestRealityDesiredFetchesEligibleMaterialAndAcknowledgesPublicIdentity(t *testing.T) {
	nodeID := uuid.NewString()
	activeUserID := uuid.NewString()
	disabledUserID := uuid.NewString()
	activeCredentialRef := uuid.NewString()
	disabledCredentialRef := uuid.NewString()
	activeSecret := uuid.NewString()
	publicMaterial := protocol.AppliedRealityMaterial{
		KeyGeneration: 1,
		PublicKey:     base64.RawURLEncoding.EncodeToString(bytesFilled(32, 7)),
		ShortID:       "0123456789abcdef",
	}
	snapshot := protocol.DesiredSnapshot{
		SchemaVersion: 2,
		NodeID:        nodeID,
		Version:       1,
		Adapter:       "sing_box_vless_reality",
		Users: []protocol.DesiredUser{
			{
				ID: activeUserID, Username: "active", Enabled: true, QuotaState: "unlimited",
				ExpiresAt: timePointer(time.Now().UTC().Add(-24 * time.Hour)),
				Credential: protocol.DesiredCredential{
					Ref: activeCredentialRef, Fingerprint: vlessFingerprint(activeSecret), Protocol: "vless",
				},
			},
			{
				ID: disabledUserID, Username: "disabled", Enabled: false, QuotaState: "unlimited",
				ExpiresAt: timePointer(time.Now().UTC().Add(24 * time.Hour)),
				Credential: protocol.DesiredCredential{
					Ref: disabledCredentialRef, Fingerprint: vlessFingerprint(uuid.NewString()), Protocol: "vless",
				},
			},
		},
		VLESSReality: &protocol.DesiredVLESSReality{
			ListenPort: 24443, ServerName: "www.microsoft.com",
			HandshakeServer: "www.microsoft.com", HandshakeServerPort: 443,
			Flow: "xtls-rprx-vision", Network: "tcp", KeyGeneration: 1,
		},
		GeneratedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	envelope := envelopeFor(snapshot)
	var materialRequests atomic.Int32
	acknowledgements := make(chan protocol.DesiredAckRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/agent/v1/desired":
			writeAgentJSON(response, envelope)
		case "/agent/v1/credential-material":
			materialRequests.Add(1)
			var input protocol.CredentialMaterialRequest
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil ||
				input.CredentialRef != activeCredentialRef || input.DesiredVersion != 1 ||
				input.SnapshotSHA256 != envelope.SHA256 {
				http.Error(response, "invalid material request", http.StatusBadRequest)
				return
			}
			writeAgentJSON(response, protocol.CredentialMaterialResponse{
				CredentialRef: activeCredentialRef, Secret: activeSecret,
			})
		case "/agent/v1/desired/1/ack":
			var acknowledgement protocol.DesiredAckRequest
			if err := json.NewDecoder(request.Body).Decode(&acknowledgement); err != nil {
				http.Error(response, "invalid acknowledgement", http.StatusBadRequest)
				return
			}
			acknowledgements <- acknowledgement
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runner := newEnrolledRealityAgent(t, server.URL, nodeID)
	runner.realityApplyExecutor = func(_ context.Context, request nodeops.RealityApplyRequest) (nodeops.HelperResponse, error) {
		if request.NodeID != nodeID || request.Version != 1 || request.SnapshotSHA256 != envelope.SHA256 ||
			request.Settings.ListenPort != 24443 || len(request.Users) != 1 ||
			request.Users[0].UserID != activeUserID || request.Users[0].UUID != activeSecret {
			t.Fatalf("unexpected Reality helper request: %#v", request)
		}
		return nodeops.HelperResponse{
			Status: "succeeded", AppliedVersion: 1, SnapshotSHA256: envelope.SHA256,
			Reality: &publicMaterial,
		}, nil
	}

	if err := runner.pollDesired(context.Background()); err != nil {
		t.Fatalf("pollDesired() error = %v", err)
	}
	if got := materialRequests.Load(); got != 1 {
		t.Fatalf("credential material requests = %d, want 1", got)
	}
	acknowledgement := <-acknowledgements
	if acknowledgement.Status != "applied" || acknowledgement.Reality == nil ||
		*acknowledgement.Reality != publicMaterial {
		t.Fatalf("Reality acknowledgement = %#v", acknowledgement)
	}
	if runner.state.AppliedVersion != 1 || runner.state.PendingAckVersion != 0 ||
		runner.state.PendingAckReality != nil {
		t.Fatalf("Reality state after acknowledgement = %#v", runner.state)
	}
}

func TestRealityCredentialMismatchFailsWithoutAdvancingAppliedVersion(t *testing.T) {
	nodeID := uuid.NewString()
	credentialRef := uuid.NewString()
	snapshot := protocol.DesiredSnapshot{
		SchemaVersion: 2, NodeID: nodeID, Version: 1, Adapter: "sing_box_vless_reality",
		Users: []protocol.DesiredUser{{
			ID: uuid.NewString(), Username: "invalid", Enabled: true, QuotaState: "unlimited",
			Credential: protocol.DesiredCredential{
				Ref: credentialRef, Fingerprint: vlessFingerprint(uuid.NewString()), Protocol: "vless",
			},
		}},
		VLESSReality: &protocol.DesiredVLESSReality{
			ListenPort: 24443, ServerName: "www.microsoft.com",
			HandshakeServer: "www.microsoft.com", HandshakeServerPort: 443,
			Flow: "xtls-rprx-vision", Network: "tcp", KeyGeneration: 1,
		},
		GeneratedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	envelope := envelopeFor(snapshot)
	acknowledgements := make(chan protocol.DesiredAckRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/agent/v1/desired":
			writeAgentJSON(response, envelope)
		case "/agent/v1/credential-material":
			writeAgentJSON(response, protocol.CredentialMaterialResponse{
				CredentialRef: credentialRef, Secret: uuid.NewString(),
			})
		case "/agent/v1/desired/1/ack":
			var acknowledgement protocol.DesiredAckRequest
			_ = json.NewDecoder(request.Body).Decode(&acknowledgement)
			acknowledgements <- acknowledgement
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runner := newEnrolledRealityAgent(t, server.URL, nodeID)
	runner.realityApplyExecutor = func(context.Context, nodeops.RealityApplyRequest) (nodeops.HelperResponse, error) {
		t.Fatal("helper called for invalid credential material")
		return nodeops.HelperResponse{}, nil
	}
	if err := runner.pollDesired(context.Background()); err != nil {
		t.Fatalf("pollDesired() error = %v", err)
	}
	acknowledgement := <-acknowledgements
	if acknowledgement.Status != "failed" || acknowledgement.ErrorCode != "reality_credential_invalid" ||
		acknowledgement.Reality != nil {
		t.Fatalf("failed Reality acknowledgement = %#v", acknowledgement)
	}
	if runner.state.AppliedVersion != 0 || runner.state.PendingAckVersion != 0 {
		t.Fatalf("invalid Reality desired advanced state: %#v", runner.state)
	}
}

func TestRealityStateWriteFailureDoesNotAdvanceInMemoryVersion(t *testing.T) {
	nodeID := uuid.NewString()
	runner := newEnrolledRealityAgent(t, "http://127.0.0.1", nodeID)
	previous := runner.state
	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("blocked"), 0o600); err != nil {
		t.Fatalf("WriteFile(blocking state parent) error = %v", err)
	}
	runner.config.StatePath = filepath.Join(blockingFile, "agent-state.json")
	material := protocol.AppliedRealityMaterial{
		KeyGeneration: 1,
		PublicKey:     base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32)),
		ShortID:       "0011223344556677",
	}
	runner.realityApplyExecutor = func(
		_ context.Context,
		request nodeops.RealityApplyRequest,
	) (nodeops.HelperResponse, error) {
		return nodeops.HelperResponse{
			Status:         "succeeded",
			AppliedVersion: request.Version,
			SnapshotSHA256: request.SnapshotSHA256,
			Reality:        &material,
		}, nil
	}
	envelope := protocol.DesiredEnvelope{
		Snapshot: protocol.DesiredSnapshot{
			SchemaVersion: 2,
			NodeID:        nodeID,
			Version:       1,
			Adapter:       "sing_box_vless_reality",
			VLESSReality: &protocol.DesiredVLESSReality{
				ListenPort: 18443, ServerName: "www.example.com",
				HandshakeServer: "www.example.com", HandshakeServerPort: 443,
				Flow: "xtls-rprx-vision", Network: "tcp", KeyGeneration: 1,
			},
		},
		SHA256: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)),
	}

	if _, err := runner.applyAndPersistVLESSRealityDesired(t.Context(), envelope); err == nil {
		t.Fatal("applyAndPersistVLESSRealityDesired() unexpectedly persisted state")
	}
	if !reflect.DeepEqual(runner.state, previous) {
		t.Fatalf("state write failure advanced in-memory state: got %#v, want %#v", runner.state, previous)
	}
}

func TestRealityPendingAckClearFailureKeepsInMemoryRetry(t *testing.T) {
	runner := newEnrolledRealityAgent(t, "http://127.0.0.1", uuid.NewString())
	runner.state.PendingAckVersion = 7
	runner.state.PendingAckHash = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{6}, 32))
	runner.state.PendingAckReality = &protocol.AppliedRealityMaterial{
		KeyGeneration: 2,
		PublicKey:     base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
		ShortID:       "8899aabbccddeeff",
	}
	previous := runner.state
	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("blocked"), 0o600); err != nil {
		t.Fatalf("WriteFile(blocking state parent) error = %v", err)
	}
	runner.config.StatePath = filepath.Join(blockingFile, "agent-state.json")

	if err := runner.clearPendingAck(); err == nil {
		t.Fatal("clearPendingAck() unexpectedly persisted state")
	}
	if !reflect.DeepEqual(runner.state, previous) {
		t.Fatalf("failed ACK clear lost in-memory retry: got %#v, want %#v", runner.state, previous)
	}
}

func TestRealityApplyAndNodeOperationAreSerialized(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		firstReality bool
	}{
		{name: "operation first"},
		{name: "Reality apply first", firstReality: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			nodeID := uuid.NewString()
			runner := newEnrolledRealityAgent(t, "http://127.0.0.1", nodeID)
			envelope := envelopeFor(protocol.DesiredSnapshot{
				SchemaVersion: 2, NodeID: nodeID, Version: 1,
				Adapter: "sing_box_vless_reality",
				VLESSReality: &protocol.DesiredVLESSReality{
					ListenPort: 24443, ServerName: "www.cloudflare.com",
					HandshakeServer: "www.cloudflare.com", HandshakeServerPort: 443,
					Flow: "xtls-rprx-vision", Network: "tcp", KeyGeneration: 1,
				},
				GeneratedAt: time.Now().UTC().Truncate(time.Millisecond),
			})
			material := protocol.AppliedRealityMaterial{
				KeyGeneration: 1,
				PublicKey:     base64.RawURLEncoding.EncodeToString(bytesFilled(32, 9)),
				ShortID:       "0123456789abcdef",
			}
			realityStarted := make(chan struct{})
			releaseReality := make(chan struct{})
			operationStarted := make(chan struct{})
			releaseOperation := make(chan struct{})
			runner.realityApplyExecutor = func(
				context.Context, nodeops.RealityApplyRequest,
			) (nodeops.HelperResponse, error) {
				close(realityStarted)
				<-releaseReality
				return nodeops.HelperResponse{
					Status: "succeeded", AppliedVersion: 1,
					SnapshotSHA256: envelope.SHA256, Reality: &material,
				}, nil
			}
			runner.operationExecutor = func(
				context.Context, protocol.NodeOperation,
			) protocol.OperationResultRequest {
				close(operationStarted)
				<-releaseOperation
				return protocol.OperationResultRequest{Status: "succeeded", CompletedAt: time.Now().UTC()}
			}

			realityDone := make(chan error, 1)
			operationDone := make(chan struct{})
			startReality := func() {
				go func() {
					_, err := runner.applyAndPersistVLESSRealityDesired(t.Context(), envelope)
					realityDone <- err
				}()
			}
			startOperation := func() {
				go func() {
					runner.executeNodeOperation(t.Context(), protocol.NodeOperation{
						ID: uuid.NewString(), Sequence: 1, Type: "restart_core", Attempt: 1,
					})
					close(operationDone)
				}()
			}

			if testCase.firstReality {
				startReality()
				waitForSignal(t, realityStarted, "Reality apply did not start")
				startOperation()
				assertNoSignal(t, operationStarted, "operation overlapped Reality apply")
				close(releaseReality)
				if err := <-realityDone; err != nil {
					t.Fatalf("Reality apply error = %v", err)
				}
				waitForSignal(t, operationStarted, "operation did not resume")
				close(releaseOperation)
				<-operationDone
			} else {
				startOperation()
				waitForSignal(t, operationStarted, "operation did not start")
				startReality()
				assertNoSignal(t, realityStarted, "Reality apply overlapped operation")
				close(releaseOperation)
				<-operationDone
				waitForSignal(t, realityStarted, "Reality apply did not resume")
				close(releaseReality)
				if err := <-realityDone; err != nil {
					t.Fatalf("Reality apply error = %v", err)
				}
			}
		})
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal(failure)
	}
}

func assertNoSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
		t.Fatal(failure)
	case <-time.After(75 * time.Millisecond):
	}
}

func TestRealityStalePendingAckIsDiscardedForNewerDesiredVersion(t *testing.T) {
	nodeID := uuid.NewString()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/agent/v1/desired/1/ack":
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(response).Encode(protocol.ErrorResponse{Error: protocol.APIError{
				Code: "desired_version_conflict", Message: "desired state changed",
			}})
		case request.Method == http.MethodGet && request.URL.Path == "/agent/v1/desired":
			if request.URL.Query().Get("after") != "1" {
				http.Error(response, "invalid after version", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(response).Encode(protocol.DesiredEnvelope{Snapshot: protocol.DesiredSnapshot{
				SchemaVersion: 2, NodeID: nodeID, Version: 2, Adapter: "sing_box_vless_reality",
			}})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runner := newEnrolledRealityAgent(t, server.URL, nodeID)
	runner.state.AppliedVersion = 1
	runner.state.PendingAckVersion = 1
	runner.state.PendingAckHash = base64.RawURLEncoding.EncodeToString(bytesFilled(32, 1))
	runner.state.PendingAckReality = &protocol.AppliedRealityMaterial{
		KeyGeneration: 1,
		PublicKey:     base64.RawURLEncoding.EncodeToString(bytesFilled(32, 2)),
		ShortID:       "0123456789abcdef",
	}
	if err := SaveState(runner.config.StatePath, runner.state); err != nil {
		t.Fatalf("SaveState(pending) error = %v", err)
	}

	if err := runner.sendPendingAck(context.Background()); err != nil {
		t.Fatalf("sendPendingAck() error = %v", err)
	}
	if runner.state.AppliedVersion != 1 || runner.state.PendingAckVersion != 0 ||
		runner.state.PendingAckHash != "" || runner.state.PendingAckReality != nil {
		t.Fatalf("state after stale acknowledgement = %#v", runner.state)
	}
	reloaded, err := LoadState(runner.config.StatePath)
	if err != nil || reloaded.PendingAckVersion != 0 || reloaded.PendingAckReality != nil {
		t.Fatalf("persisted state after stale acknowledgement = %#v, %v", reloaded, err)
	}
}

func TestRealityPendingAckConflictIsRetainedWithoutNewerDesiredVersion(t *testing.T) {
	tests := []struct {
		name            string
		confirmResponse func(http.ResponseWriter)
	}{
		{
			name: "same version",
			confirmResponse: func(response http.ResponseWriter) {
				response.WriteHeader(http.StatusNoContent)
			},
		},
		{
			name: "server failure",
			confirmResponse: func(response http.ResponseWriter) {
				http.Error(response, "unavailable", http.StatusServiceUnavailable)
			},
		},
		{
			name: "network failure",
			confirmResponse: func(http.ResponseWriter) {
				panic(http.ErrAbortHandler)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			nodeID := uuid.NewString()
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				switch {
				case request.Method == http.MethodPost && request.URL.Path == "/agent/v1/desired/1/ack":
					response.Header().Set("Content-Type", "application/json")
					response.WriteHeader(http.StatusConflict)
					_ = json.NewEncoder(response).Encode(protocol.ErrorResponse{Error: protocol.APIError{
						Code: "desired_version_conflict", Message: "desired state changed",
					}})
				case request.Method == http.MethodGet && request.URL.Path == "/agent/v1/desired":
					if request.URL.Query().Get("after") != "1" {
						http.Error(response, "invalid after version", http.StatusBadRequest)
						return
					}
					test.confirmResponse(response)
				default:
					http.NotFound(response, request)
				}
			}))
			defer server.Close()

			runner := newEnrolledRealityAgent(t, server.URL, nodeID)
			runner.state.AppliedVersion = 1
			runner.state.PendingAckVersion = 1
			runner.state.PendingAckHash = base64.RawURLEncoding.EncodeToString(bytesFilled(32, 1))
			runner.state.PendingAckReality = &protocol.AppliedRealityMaterial{
				KeyGeneration: 1,
				PublicKey:     base64.RawURLEncoding.EncodeToString(bytesFilled(32, 2)),
				ShortID:       "0123456789abcdef",
			}
			if err := SaveState(runner.config.StatePath, runner.state); err != nil {
				t.Fatalf("SaveState(pending) error = %v", err)
			}

			if err := runner.sendPendingAck(context.Background()); err == nil {
				t.Fatal("sendPendingAck() succeeded without confirming a newer desired version")
			}
			assertRealityPendingAck(t, runner.state)
			reloaded, err := LoadState(runner.config.StatePath)
			if err != nil {
				t.Fatalf("LoadState() error = %v", err)
			}
			assertRealityPendingAck(t, reloaded)
		})
	}
}

func TestRealityProbeMapsHelperStates(t *testing.T) {
	now := time.Date(2026, 8, 12, 14, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name        string
		response    nodeops.HelperResponse
		err         error
		status      string
		errorCode   string
		coreRunning bool
	}{
		{
			name: "compatible and active",
			response: nodeops.HelperResponse{Status: "succeeded", RealityProbe: &nodeops.RealityProbeResult{
				AdapterStatus: "compatible", AdapterVersion: "1.13.18", CoreVersion: "1.13.18",
				CoreRunning: true, ProbedAt: now,
			}},
			status: "compatible", coreRunning: true,
		},
		{
			name: "compatible but inactive",
			response: nodeops.HelperResponse{Status: "succeeded", RealityProbe: &nodeops.RealityProbeResult{
				AdapterStatus: "compatible", AdapterVersion: "1.13.18", CoreVersion: "1.13.18",
				ProbedAt: now,
			}},
			status: "compatible",
		},
		{
			name: "incompatible binary",
			response: nodeops.HelperResponse{Status: "succeeded", RealityProbe: &nodeops.RealityProbeResult{
				AdapterStatus: "incompatible", AdapterErrorCode: "reality_binary_incompatible",
				ProbedAt: now,
			}},
			status: "incompatible", errorCode: "reality_binary_incompatible",
		},
		{
			name: "helper unavailable", err: errors.New("helper unavailable"),
			status: "unavailable", errorCode: "reality_probe_unavailable",
		},
		{
			name: "invalid helper response",
			response: nodeops.HelperResponse{Status: "succeeded", RealityProbe: &nodeops.RealityProbeResult{
				AdapterStatus: "compatible", CoreRunning: true, ProbedAt: now,
			}},
			status: "unavailable", errorCode: "reality_probe_invalid_response",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runner := newEnrolledRealityAgent(t, "http://127.0.0.1:1", uuid.NewString())
			runner.realityProbeExecutor = func(
				context.Context, nodeops.RealityProbeRequest,
			) (nodeops.HelperResponse, error) {
				return testCase.response, testCase.err
			}
			adapter, core, _ := runner.probeVLESSReality(t.Context(), now)
			if adapter.Name != "sing_box_vless_reality" || adapter.Status != testCase.status ||
				adapter.ErrorCode != testCase.errorCode || adapter.LastProbedAt == nil ||
				!adapter.LastProbedAt.Equal(now) || core.Name != "sing-box" ||
				core.Running != testCase.coreRunning {
				t.Fatalf("mapped Reality probe = adapter %#v core %#v", adapter, core)
			}
		})
	}
}

func TestRealityHeartbeatUsesTypedProbeAndReleasesDataPlaneLockBeforeHTTP(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	heartbeats := make(chan protocol.HeartbeatRequest, 1)
	var runner *Agent
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/agent/v1/heartbeat" {
			http.NotFound(response, request)
			return
		}
		if !runner.dataPlaneMu.TryLock() {
			http.Error(response, "data-plane lock held across heartbeat", http.StatusConflict)
			return
		}
		runner.dataPlaneMu.Unlock()
		var heartbeat protocol.HeartbeatRequest
		if err := json.NewDecoder(request.Body).Decode(&heartbeat); err != nil {
			http.Error(response, "decode", http.StatusBadRequest)
			return
		}
		heartbeats <- heartbeat
		writeAgentJSON(response, protocol.HeartbeatResponse{ServerTime: time.Now().UTC()})
	}))
	defer server.Close()

	runner = newEnrolledRealityAgent(t, server.URL, uuid.NewString())
	runner.collector = fixedCollector{}
	probeCalls := atomic.Int32{}
	runner.realityProbeExecutor = func(
		context.Context, nodeops.RealityProbeRequest,
	) (nodeops.HelperResponse, error) {
		probeCalls.Add(1)
		return nodeops.HelperResponse{Status: "succeeded", RealityProbe: &nodeops.RealityProbeResult{
			AdapterStatus: "compatible", AdapterVersion: "1.13.18", CoreVersion: "1.13.18",
			CoreRunning: true, ProbedAt: now,
		}}, nil
	}
	if _, _, err := runner.heartbeat(t.Context()); err != nil {
		t.Fatalf("heartbeat() error = %v", err)
	}
	heartbeat := <-heartbeats
	if probeCalls.Load() != 1 || heartbeat.Adapter.Status != "compatible" ||
		heartbeat.Adapter.Version != "1.13.18" || heartbeat.Adapter.LastProbedAt == nil ||
		heartbeat.Core.Name != "sing-box" || heartbeat.Core.Version != "1.13.18" ||
		!heartbeat.Core.Running {
		t.Fatalf("Reality heartbeat = %#v; probe calls = %d", heartbeat, probeCalls.Load())
	}
}

func TestRealityOperationBetweenProbeAndHeartbeatPreventsPendingAck(t *testing.T) {
	nodeID := uuid.NewString()
	heartbeatReceived := make(chan struct{}, 1)
	releaseHeartbeat := make(chan struct{})
	heartbeatCalls := atomic.Int32{}
	ackCalls := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/agent/v1/heartbeat":
			if heartbeatCalls.Add(1) == 1 {
				select {
				case heartbeatReceived <- struct{}{}:
				default:
				}
				<-releaseHeartbeat
			}
			writeAgentJSON(response, protocol.HeartbeatResponse{ServerTime: time.Now().UTC()})
		case "/agent/v1/desired/7/ack":
			ackCalls.Add(1)
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runner := newEnrolledRealityAgent(t, server.URL, nodeID)
	runner.collector = fixedCollector{}
	runner.state.AppliedVersion = 7
	runner.state.PendingAckVersion = 7
	runner.state.PendingAckHash = base64.RawURLEncoding.EncodeToString(bytesFilled(32, 6))
	runner.state.PendingAckReality = &protocol.AppliedRealityMaterial{
		KeyGeneration: 2,
		PublicKey:     base64.RawURLEncoding.EncodeToString(bytesFilled(32, 8)),
		ShortID:       "fedcba9876543210",
	}
	if err := SaveState(runner.config.StatePath, runner.state); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	runner.realityProbeExecutor = func(
		context.Context, nodeops.RealityProbeRequest,
	) (nodeops.HelperResponse, error) {
		now := time.Now().UTC()
		return nodeops.HelperResponse{Status: "succeeded", RealityProbe: &nodeops.RealityProbeResult{
			AdapterStatus: "compatible", AdapterVersion: "1.13.18", CoreVersion: "1.13.18",
			CoreRunning: true, ProbedAt: now,
		}}, nil
	}
	runner.operationExecutor = func(
		context.Context, protocol.NodeOperation,
	) protocol.OperationResultRequest {
		return protocol.OperationResultRequest{Status: "failed", CompletedAt: time.Now().UTC()}
	}

	type heartbeatResult struct {
		revision uint64
		err      error
	}
	heartbeatDone := make(chan heartbeatResult, 1)
	go func() {
		_, revision, err := runner.heartbeat(t.Context())
		heartbeatDone <- heartbeatResult{revision: revision, err: err}
	}()
	waitForSignal(t, heartbeatReceived, "heartbeat was not sent after the Reality probe")
	runner.executeNodeOperation(t.Context(), protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 1, Type: "restart_core", Attempt: 1,
	})
	close(releaseHeartbeat)
	heartbeat := <-heartbeatDone
	if heartbeat.err != nil {
		t.Fatalf("heartbeat() error = %v", heartbeat.err)
	}
	if err := runner.sendPendingRealityAck(t.Context(), heartbeat.revision); !errors.Is(err, errRealityDataPlaneChanged) {
		t.Fatalf("stale sendPendingRealityAck() error = %v", err)
	}
	if ackCalls.Load() != 0 || runner.state.PendingAckVersion != 7 {
		t.Fatalf("stale pending acknowledgement was sent or cleared: calls=%d state=%#v",
			ackCalls.Load(), runner.state)
	}

	_, revision, err := runner.heartbeat(t.Context())
	if err != nil {
		t.Fatalf("second heartbeat() error = %v", err)
	}
	if err := runner.sendPendingRealityAck(t.Context(), revision); err != nil {
		t.Fatalf("fresh sendPendingRealityAck() error = %v", err)
	}
	if ackCalls.Load() != 1 || runner.state.PendingAckVersion != 0 ||
		runner.state.PendingAckReality != nil {
		t.Fatalf("fresh pending acknowledgement was not cleared: calls=%d state=%#v",
			ackCalls.Load(), runner.state)
	}
}

func TestRealityRunRetriesPersistedAckOnlyAfterSuccessfulHeartbeat(t *testing.T) {
	nodeID := uuid.NewString()
	publicMaterial := protocol.AppliedRealityMaterial{
		KeyGeneration: 2,
		PublicKey:     base64.RawURLEncoding.EncodeToString(bytesFilled(32, 8)),
		ShortID:       "fedcba9876543210",
	}
	var sequenceMu sync.Mutex
	sequence := make([]string, 0, 4)
	var heartbeatCalls atomic.Int32
	var acknowledgementSeen atomic.Bool
	acknowledgementPersisted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/agent/v1/heartbeat":
			var heartbeat protocol.HeartbeatRequest
			if err := json.NewDecoder(request.Body).Decode(&heartbeat); err != nil {
				http.Error(response, "invalid heartbeat", http.StatusBadRequest)
				return
			}
			if heartbeat.Core.Running {
				http.Error(response, "test probe must report the core down", http.StatusBadRequest)
				return
			}
			call := heartbeatCalls.Add(1)
			sequenceMu.Lock()
			if call == 1 {
				sequence = append(sequence, "heartbeat_failed")
			} else {
				sequence = append(sequence, "heartbeat_succeeded")
			}
			sequenceMu.Unlock()
			if call == 1 {
				http.Error(response, "retry", http.StatusServiceUnavailable)
				return
			}
			writeAgentJSON(response, protocol.HeartbeatResponse{ServerTime: time.Now().UTC()})
		case "/agent/v1/desired/7/ack":
			sequenceMu.Lock()
			sequence = append(sequence, "acknowledged")
			sequenceMu.Unlock()
			acknowledgementSeen.Store(true)
			response.WriteHeader(http.StatusNoContent)
		case "/agent/v1/desired":
			if request.URL.Query().Get("after") != "7" {
				http.Error(response, "invalid desired version", http.StatusBadRequest)
				return
			}
			sequenceMu.Lock()
			sequence = append(sequence, "desired_polled")
			sequenceMu.Unlock()
			if acknowledgementSeen.Load() {
				select {
				case acknowledgementPersisted <- struct{}{}:
				default:
				}
			}
			response.WriteHeader(http.StatusNoContent)
		case "/agent/v1/telemetry":
			writeAgentJSON(response, protocol.TelemetrySnapshotResponse{Accepted: true})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	runner := newEnrolledRealityAgent(t, server.URL, nodeID)
	runner.collector = fixedCollector{}
	runner.config.HeartbeatEvery = 25 * time.Millisecond
	runner.config.DesiredEvery = 5 * time.Millisecond
	runner.config.TelemetryEvery = time.Hour
	runner.config.TrafficEvery = time.Hour
	runner.state.AppliedVersion = 7
	runner.state.AppliedSnapshotHash = base64.RawURLEncoding.EncodeToString(bytesFilled(32, 6))
	runner.state.PendingAckVersion = 7
	runner.state.PendingAckHash = runner.state.AppliedSnapshotHash
	runner.state.PendingAckReality = &publicMaterial
	if err := SaveState(runner.config.StatePath, runner.state); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	runner.realityProbeExecutor = func(
		_ context.Context,
		_ nodeops.RealityProbeRequest,
	) (nodeops.HelperResponse, error) {
		now := time.Now().UTC()
		return nodeops.HelperResponse{
			Status: "succeeded", CompletedAt: now,
			RealityProbe: &nodeops.RealityProbeResult{
				AdapterStatus: "compatible", AdapterVersion: "1.13.18-hyfleet-utls1.8.7",
				CoreVersion: "1.13.18-hyfleet-utls1.8.7", CoreRunning: false, ProbedAt: now,
			},
		}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	select {
	case <-acknowledgementPersisted:
	case <-time.After(2 * time.Second):
		cancel()
		<-done
		t.Fatal("pending acknowledgement was not persisted after a successful heartbeat")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Agent.Run() error = %v", err)
	}
	persisted, err := LoadState(runner.config.StatePath)
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if persisted.PendingAckVersion != 0 || persisted.PendingAckReality != nil {
		t.Fatalf("pending acknowledgement was not persisted as cleared: %#v", persisted)
	}

	sequenceMu.Lock()
	gotSequence := append([]string(nil), sequence...)
	sequenceMu.Unlock()
	if len(gotSequence) < 4 || gotSequence[0] != "heartbeat_failed" {
		t.Fatalf("heartbeat and acknowledgement sequence = %#v", gotSequence)
	}
	sawDesiredPoll := false
	sawSuccessfulHeartbeat := false
	for _, event := range gotSequence[1:] {
		switch event {
		case "desired_polled":
			if !sawSuccessfulHeartbeat {
				sawDesiredPoll = true
			}
		case "heartbeat_succeeded":
			if !sawDesiredPoll {
				t.Fatalf("desired polling was blocked by the pending acknowledgement: %#v", gotSequence)
			}
			sawSuccessfulHeartbeat = true
		case "acknowledged":
			if !sawSuccessfulHeartbeat {
				t.Fatalf("acknowledgement preceded a successful heartbeat: %#v", gotSequence)
			}
		default:
			t.Fatalf("unexpected startup event %q in %#v", event, gotSequence)
		}
	}
	if runner.state.PendingAckVersion != 0 || runner.state.PendingAckReality != nil {
		t.Fatalf("pending acknowledgement was not cleared: %#v", runner.state)
	}
}

func assertRealityPendingAck(t *testing.T, state State) {
	t.Helper()
	if state.PendingAckVersion != 1 || state.PendingAckHash == "" || state.PendingAckReality == nil {
		t.Fatalf("pending acknowledgement was not retained: %#v", state)
	}
}

func newEnrolledRealityAgent(t *testing.T, serverURL, nodeID string) *Agent {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	if err := SaveState(statePath, State{
		InstallationID: uuid.NewString(), NodeID: nodeID,
		NodeCredential: "hya_" + nodeID + ".reality-test",
	}); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	runner, err := New(config.Agent{
		ServerURL: serverURL, StatePath: statePath, AdapterType: "sing_box_vless_reality",
		CoreName: "sing-box", ServiceUnit: "hyfleet-sing-box-reality.service", AllowHTTP: true,
	}, testLogger())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = runner.Close() })
	return runner
}

func vlessFingerprint(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return "fp_" + base64.RawURLEncoding.EncodeToString(digest[:6])
}

func bytesFilled(length int, value byte) []byte {
	result := make([]byte, length)
	for index := range result {
		result[index] = value
	}
	return result
}

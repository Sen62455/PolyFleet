package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/config"
	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/google/uuid"
)

func TestOperationResultOutboxSurvivesControlPlaneDisconnect(t *testing.T) {
	local, err := openLocalStore(t.Context(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("openLocalStore() error = %v", err)
	}
	t.Cleanup(func() { _ = local.Close() })
	operation := protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 1, Type: "tail_core_log", MaxLines: 100,
		Attempt: 1, CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	}
	var acceptResults atomic.Bool
	var executions atomic.Int32
	var received protocol.OperationResultRequest
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-agent-credential" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/agent/v1/operations":
			operations := []protocol.NodeOperation{}
			if request.URL.Query().Get("after") == "0" {
				operations = append(operations, operation)
			}
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(protocol.NodeOperationsResponse{
				Operations: operations, ServerTime: time.Now().UTC(),
			})
		case request.Method == http.MethodPost &&
			request.URL.Path == "/agent/v1/operations/"+operation.ID+"/result":
			if !acceptResults.Load() {
				response.Header().Set("Content-Type", "application/json")
				response.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(response).Encode(protocol.ErrorResponse{
					Error: protocol.APIError{Code: "control_plane_unavailable", Message: "offline"},
				})
				return
			}
			if err := json.NewDecoder(request.Body).Decode(&received); err != nil {
				t.Errorf("decode operation result: %v", err)
			}
			response.WriteHeader(http.StatusNoContent)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	runner := &Agent{
		config: config.Agent{ServerURL: server.URL}, client: server.Client(), localStore: local,
		state: State{NodeCredential: "test-agent-credential"},
		operationExecutor: func(_ context.Context, operation protocol.NodeOperation) protocol.OperationResultRequest {
			executions.Add(1)
			return protocol.OperationResultRequest{
				Sequence: operation.Sequence, Status: "succeeded",
				Output:      "password=must-not-leave-agent\ncompleted",
				CompletedAt: time.Now().UTC(),
			}
		},
	}
	if err := runner.runOperationCycle(t.Context()); err == nil {
		t.Fatal("runOperationCycle() succeeded while result endpoint was unavailable")
	}
	if executions.Load() != 1 {
		t.Fatalf("operation executions = %d, want 1", executions.Load())
	}
	pending, err := local.listPendingOperationResults(t.Context(), 20)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending operation results = %#v, error = %v", pending, err)
	}

	acceptResults.Store(true)
	if err := runner.runOperationCycle(t.Context()); err != nil {
		t.Fatalf("runOperationCycle(reconnected) error = %v", err)
	}
	if executions.Load() != 1 {
		t.Fatalf("operation re-executed after reconnect: %d", executions.Load())
	}
	pending, err = local.listPendingOperationResults(t.Context(), 20)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending results after reconnect = %#v, error = %v", pending, err)
	}
	if received.Sequence != 1 || received.Status != "succeeded" ||
		received.Output == "" || received.Output == "password=must-not-leave-agent\ncompleted" {
		t.Fatalf("reported operation result was not sanitized: %#v", received)
	}
}

func TestLatestDesiredStateAndAcknowledgementCatchUpAfterReconnect(t *testing.T) {
	nodeID := uuid.NewString()
	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	if err := SaveState(statePath, State{
		InstallationID: uuid.NewString(), NodeID: nodeID,
		NodeCredential: "test-agent-credential",
	}); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	snapshot := protocol.DesiredSnapshot{
		SchemaVersion: 1, NodeID: nodeID, Version: 7, Adapter: "native_hysteria2",
		Users: []protocol.DesiredUser{}, GeneratedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	envelope := envelopeFor(snapshot)
	var acceptAcknowledgement atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/agent/v1/desired":
			_ = json.NewEncoder(response).Encode(envelope)
		case request.Method == http.MethodPost &&
			request.URL.Path == "/agent/v1/desired/7/ack":
			if !acceptAcknowledgement.Load() {
				response.Header().Set("Content-Type", "application/json")
				response.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(response).Encode(protocol.ErrorResponse{
					Error: protocol.APIError{Code: "control_plane_unavailable"},
				})
				return
			}
			response.WriteHeader(http.StatusNoContent)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	cfg := config.Agent{
		ServerURL: server.URL, StatePath: statePath, AdapterType: "native_hysteria2",
	}
	first, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("New(first) error = %v", err)
	}
	if err := first.pollDesired(t.Context()); err == nil {
		t.Fatal("pollDesired() succeeded while acknowledgement endpoint was unavailable")
	}
	if first.state.AppliedVersion != 7 || first.state.PendingAckVersion != 7 {
		t.Fatalf("state after disconnected apply = %#v", first.state)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first) error = %v", err)
	}

	acceptAcknowledgement.Store(true)
	second, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("New(second) error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err := second.sendPendingAck(t.Context()); err != nil {
		t.Fatalf("sendPendingAck(reconnected) error = %v", err)
	}
	if second.state.AppliedVersion != 7 || second.state.PendingAckVersion != 0 {
		t.Fatalf("state after acknowledgement catch-up = %#v", second.state)
	}
}

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/config"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

type fixedCollector struct{}

func (fixedCollector) Facts() HostFacts {
	return HostFacts{OS: "linux", OSVersion: "24.04", Architecture: "amd64"}
}

func (fixedCollector) Sample(context.Context) (protocol.HostMetrics, error) {
	return protocol.HostMetrics{
		UptimeSeconds: 120, CPUPercent: 5,
		MemoryUsedBytes: 128 << 20, MemoryTotalBytes: 1024 << 20,
		DiskUsedBytes: 2 << 30, DiskTotalBytes: 20 << 30,
	}, nil
}

func (fixedCollector) ServiceRunning(context.Context, string) bool { return true }

type failingCollector struct{}

func (failingCollector) Facts() HostFacts {
	return HostFacts{
		OS: "linux", OSVersion: "24.04", Architecture: "amd64",
		Hostname: "metrics-unavailable", KernelVersion: "test-kernel", CPUCores: 2,
	}
}

func (failingCollector) Sample(context.Context) (protocol.HostMetrics, error) {
	return protocol.HostMetrics{}, errors.New("host metrics unavailable")
}

func (failingCollector) ServiceRunning(context.Context, string) bool { return true }

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestAgentRestartReusesEnrollment(t *testing.T) {
	nodeID := uuid.NewString()
	credential := "hya_" + nodeID + ".test-secret"
	var mu sync.Mutex
	enrollments := 0
	heartbeats := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-HyFleet-Protocol") != "1" {
			http.Error(response, "protocol", http.StatusUpgradeRequired)
			return
		}
		switch request.URL.Path {
		case "/agent/v1/enroll":
			mu.Lock()
			enrollments++
			mu.Unlock()
			writeAgentJSON(response, protocol.EnrollResponse{
				NodeID: nodeID, NodeCredential: credential, Protocol: protocol.MajorVersion,
				Polling:    protocol.PollingPolicy{HeartbeatSeconds: 15, DesiredSeconds: 10},
				ServerTime: time.Now().UTC(),
			})
		case "/agent/v1/heartbeat":
			if request.Header.Get("Authorization") != "Bearer "+credential {
				http.Error(response, "auth", http.StatusUnauthorized)
				return
			}
			mu.Lock()
			heartbeats++
			mu.Unlock()
			writeAgentJSON(response, protocol.HeartbeatResponse{ServerTime: time.Now().UTC()})
		case "/agent/v1/desired":
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	cfg := config.Agent{
		ServerURL: server.URL, EnrollmentToken: "one-time-token", StatePath: statePath,
		AdapterType: "native_hysteria2", CoreName: "hysteria", ServiceUnit: "hysteria-server.service",
		HeartbeatEvery: 20 * time.Millisecond, DesiredEvery: 20 * time.Millisecond, AllowHTTP: true,
	}
	first, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("New(first) error = %v", err)
	}
	first.collector = fixedCollector{}
	runAgentUntilHeartbeat(t, first, func() int {
		mu.Lock()
		defer mu.Unlock()
		return heartbeats
	}, 1)

	cfg.EnrollmentToken = ""
	second, err := New(cfg, testLogger())
	if err != nil {
		t.Fatalf("New(second) error = %v", err)
	}
	second.collector = fixedCollector{}
	runAgentUntilHeartbeat(t, second, func() int {
		mu.Lock()
		defer mu.Unlock()
		return heartbeats
	}, 2)

	mu.Lock()
	defer mu.Unlock()
	if enrollments != 1 {
		t.Fatalf("enrollments = %d, want 1 after restart", enrollments)
	}
}

func runAgentUntilHeartbeat(t *testing.T, runner *Agent, count func() int, want int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for count() < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if count() < want {
		cancel()
		<-done
		t.Fatalf("heartbeat count did not reach %d", want)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Agent.Run() error = %v", err)
	}
}

func TestHeartbeatContinuesWhenHostMetricsAreUnavailable(t *testing.T) {
	nodeID := uuid.NewString()
	installationID := uuid.NewString()
	credential := "hya_" + nodeID + ".metrics-fallback"
	heartbeats := make(chan protocol.HeartbeatRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/agent/v1/heartbeat" {
			http.NotFound(response, request)
			return
		}
		var heartbeat protocol.HeartbeatRequest
		if err := json.NewDecoder(request.Body).Decode(&heartbeat); err != nil {
			http.Error(response, "decode", http.StatusBadRequest)
			return
		}
		heartbeats <- heartbeat
		writeAgentJSON(response, protocol.HeartbeatResponse{ServerTime: time.Now().UTC()})
	}))
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	if err := SaveState(statePath, State{
		InstallationID: installationID,
		NodeID:         nodeID,
		NodeCredential: credential,
	}); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	runner, err := New(config.Agent{
		ServerURL: server.URL, StatePath: statePath, AdapterType: "standalone_sing_box",
		CoreName: "sing-box", ServiceUnit: "sing-box.service", AllowHTTP: true,
	}, testLogger())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = runner.Close() })
	runner.collector = failingCollector{}

	if _, _, err := runner.heartbeat(context.Background()); err != nil {
		t.Fatalf("heartbeat() error = %v", err)
	}
	heartbeat := <-heartbeats
	if heartbeat.InstallationID != installationID {
		t.Fatalf("heartbeat installation ID = %q, want %q", heartbeat.InstallationID, installationID)
	}
	if heartbeat.Host.Hostname != "metrics-unavailable" ||
		heartbeat.Host.KernelVersion != "test-kernel" || heartbeat.Host.CPUCores != 2 {
		t.Fatalf("fallback host facts = %#v", heartbeat.Host)
	}
}

func TestHeartbeatsContinueWhileNodeOperationIsBlocked(t *testing.T) {
	nodeID := uuid.NewString()
	credential := "hya_" + nodeID + ".operation-isolation"
	operation := protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 1, Type: "restart_core", Attempt: 1,
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
	}
	var heartbeats atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/agent/v1/enroll":
			writeAgentJSON(response, protocol.EnrollResponse{
				NodeID: nodeID, NodeCredential: credential, Protocol: protocol.MajorVersion,
				Polling:    protocol.PollingPolicy{HeartbeatSeconds: 15, DesiredSeconds: 10},
				ServerTime: time.Now().UTC(),
			})
		case "/agent/v1/heartbeat":
			heartbeats.Add(1)
			writeAgentJSON(response, protocol.HeartbeatResponse{ServerTime: time.Now().UTC()})
		case "/agent/v1/desired":
			response.WriteHeader(http.StatusNoContent)
		case "/agent/v1/operations":
			writeAgentJSON(response, protocol.NodeOperationsResponse{
				Operations: []protocol.NodeOperation{operation}, ServerTime: time.Now().UTC(),
			})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	runner, err := New(config.Agent{
		ServerURL: server.URL, EnrollmentToken: "one-time-token", StatePath: statePath,
		AdapterType: "native_hysteria2", CoreName: "hysteria",
		ServiceUnit: "hysteria-server.service", HeartbeatEvery: 20 * time.Millisecond,
		DesiredEvery: 20 * time.Millisecond, TrafficEvery: time.Hour, AllowHTTP: true,
	}, testLogger())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runner.collector = fixedCollector{}
	operationStarted := make(chan struct{})
	var started sync.Once
	runner.operationExecutor = func(ctx context.Context, operation protocol.NodeOperation) protocol.OperationResultRequest {
		started.Do(func() { close(operationStarted) })
		<-ctx.Done()
		return protocol.OperationResultRequest{
			Sequence: operation.Sequence, Status: "failed", ErrorCode: "operation_cancelled",
			ErrorMessage: "operation cancelled with Agent shutdown", CompletedAt: time.Now().UTC(),
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	select {
	case <-operationStarted:
	case <-time.After(2 * time.Second):
		cancel()
		<-done
		t.Fatal("node operation did not start")
	}
	deadline := time.Now().Add(2 * time.Second)
	for heartbeats.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := heartbeats.Load(); got < 3 {
		cancel()
		<-done
		t.Fatalf("heartbeats while operation was blocked = %d, want at least 3", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Agent.Run() error = %v", err)
	}
}

func TestRealityUsageCycleDoesNotCallUnsupportedEndpoints(t *testing.T) {
	nodeID := uuid.NewString()
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(response, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	if err := SaveState(statePath, State{
		InstallationID: uuid.NewString(),
		NodeID:         nodeID,
		NodeCredential: "hya_" + nodeID + ".reality-usage",
	}); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	runner, err := New(config.Agent{
		ServerURL: server.URL, StatePath: statePath,
		AdapterType: "sing_box_vless_reality", CoreName: "sing-box",
		ServiceUnit: "hyfleet-sing-box-reality.service", AllowHTTP: true,
	}, testLogger())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = runner.Close() })

	if err := runner.runUsageCycle(context.Background()); err != nil {
		t.Fatalf("runUsageCycle() error = %v", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("unsupported Reality usage requests = %d, want 0", got)
	}
}

func TestNativeDesiredStatePersistsAuthenticationCache(t *testing.T) {
	nodeID := uuid.NewString()
	installationID := uuid.NewString()
	credential := "hya_" + nodeID + ".test-secret"
	var mu sync.Mutex
	var desired protocol.DesiredEnvelope
	var acknowledgements []protocol.DesiredAckRequest
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/agent/v1/desired":
			mu.Lock()
			current := desired
			mu.Unlock()
			writeAgentJSON(response, current)
		case request.Method == http.MethodPost:
			var acknowledgement protocol.DesiredAckRequest
			if err := json.NewDecoder(request.Body).Decode(&acknowledgement); err != nil {
				http.Error(response, "decode", http.StatusBadRequest)
				return
			}
			mu.Lock()
			acknowledgements = append(acknowledgements, acknowledgement)
			mu.Unlock()
			response.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "agent-state.json")
	if err := SaveState(statePath, State{
		InstallationID: installationID,
		NodeID:         nodeID,
		NodeCredential: credential,
	}); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	runner, err := New(config.Agent{
		ServerURL: server.URL, StatePath: statePath, AdapterType: "native_hysteria2",
	}, testLogger())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = runner.Close() })

	emptySnapshot := protocol.DesiredSnapshot{
		SchemaVersion: 1, NodeID: nodeID, Version: 1, Adapter: "native_hysteria2",
		Users: []protocol.DesiredUser{}, GeneratedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	mu.Lock()
	desired = envelopeFor(emptySnapshot)
	mu.Unlock()
	if err := runner.pollDesired(context.Background()); err != nil {
		t.Fatalf("pollDesired(empty) error = %v", err)
	}
	if runner.state.AppliedVersion != 1 || runner.state.PendingAckVersion != 0 {
		t.Fatalf("state after empty desired = %#v", runner.state)
	}

	userSecret := "generated-high-entropy-user-secret"
	verifier := sha256.Sum256([]byte(userSecret))
	userID := uuid.NewString()
	userSnapshot := protocol.DesiredSnapshot{
		SchemaVersion: 1, NodeID: nodeID, Version: 2, Adapter: "native_hysteria2",
		Users: []protocol.DesiredUser{{
			ID: userID, Username: "phase-two-user", Enabled: true, QuotaState: "unlimited",
			Credential: protocol.DesiredCredential{
				Ref: uuid.NewString(), Fingerprint: "fp_test",
				VerifierSHA256: base64.RawURLEncoding.EncodeToString(verifier[:]),
			},
		}},
		GeneratedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	mu.Lock()
	desired = envelopeFor(userSnapshot)
	mu.Unlock()
	if err := runner.pollDesired(context.Background()); err != nil {
		t.Fatalf("pollDesired(user) error = %v", err)
	}
	if runner.state.AppliedVersion != 2 {
		t.Fatalf("Agent applied version = %d, want 2", runner.state.AppliedVersion)
	}
	if authenticatedID, ok := runner.authCache.Authenticate(userSecret, time.Now().UTC()); !ok || authenticatedID != userID {
		t.Fatalf("cached authentication = (%q, %v), want (%q, true)", authenticatedID, ok, userID)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(acknowledgements) != 2 || acknowledgements[0].Status != "applied" ||
		acknowledgements[1].Status != "applied" {
		t.Fatalf("unexpected acknowledgements: %#v", acknowledgements)
	}
}

func envelopeFor(snapshot protocol.DesiredSnapshot) protocol.DesiredEnvelope {
	canonical, _ := json.Marshal(snapshot)
	hash := sha256.Sum256(canonical)
	return protocol.DesiredEnvelope{
		Snapshot:  snapshot,
		SHA256:    base64.RawURLEncoding.EncodeToString(hash[:]),
		CreatedAt: snapshot.GeneratedAt,
	}
}

func writeAgentJSON(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(value)
}

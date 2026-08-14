package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/config"
	"github.com/Sen62455/PolyFleet/internal/nodeops"
	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/google/uuid"
)

func TestRealityStatsClientUsesBearerAuthAndAcceptsStrictUsers(t *testing.T) {
	userID := uuid.NewString()
	epoch := uuid.NewString()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/hyfleet/v1/users" {
			http.NotFound(response, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer test-reality-secret" ||
			request.Header.Get("Accept") != "application/json" {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(response, `{"epoch":%q,"users":[{"user":%q,"upload":12,"download":34,"connections":2}]}`,
			epoch, userID)
	}))
	t.Cleanup(server.Close)

	client := newRealityStatsClient(server.URL, "test-reality-secret")
	gotEpoch, users, err := client.users(t.Context())
	if err != nil || gotEpoch != epoch || len(users) != 1 || users[0].User != userID || users[0].Upload != 12 ||
		users[0].Download != 34 || users[0].Connections != 2 {
		t.Fatalf("users() = %#v, error = %v", users, err)
	}
}

func TestRealityStatsClientRejectsInvalidResponses(t *testing.T) {
	canonical := uuid.NewString()
	epoch := uuid.NewString()
	upper := strings.ToUpper(canonical)
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "non-200", statusCode: http.StatusUnauthorized, body: `{}`},
		{name: "missing epoch", statusCode: http.StatusOK, body: `{"users":[]}`},
		{name: "invalid epoch", statusCode: http.StatusOK, body: `{"epoch":"not-a-uuid","users":[]}`},
		{name: "unknown field", statusCode: http.StatusOK, body: fmt.Sprintf(`{"epoch":%q,"users":[],"extra":true}`, epoch)},
		{name: "trailing JSON", statusCode: http.StatusOK, body: fmt.Sprintf(`{"epoch":%q,"users":[]} {}`, epoch)},
		{name: "oversized", statusCode: http.StatusOK, body: fmt.Sprintf(`{"epoch":%q,"users":[]}`, epoch) + strings.Repeat(" ", maxStatsResponseBytes)},
		{name: "duplicate UUID", statusCode: http.StatusOK, body: fmt.Sprintf(
			`{"epoch":%q,"users":[{"user":%q,"upload":0,"download":0,"connections":0},{"user":%q,"upload":0,"download":0,"connections":0}]}`,
			epoch, canonical, canonical,
		)},
		{name: "noncanonical UUID", statusCode: http.StatusOK, body: fmt.Sprintf(
			`{"epoch":%q,"users":[{"user":%q,"upload":0,"download":0,"connections":0}]}`, epoch, upper,
		)},
		{name: "negative upload", statusCode: http.StatusOK, body: fmt.Sprintf(
			`{"epoch":%q,"users":[{"user":%q,"upload":-1,"download":0,"connections":0}]}`, epoch, canonical,
		)},
		{name: "negative download", statusCode: http.StatusOK, body: fmt.Sprintf(
			`{"epoch":%q,"users":[{"user":%q,"upload":0,"download":-1,"connections":0}]}`, epoch, canonical,
		)},
		{name: "negative connections", statusCode: http.StatusOK, body: fmt.Sprintf(
			`{"epoch":%q,"users":[{"user":%q,"upload":0,"download":0,"connections":-1}]}`, epoch, canonical,
		)},
		{name: "oversized connections", statusCode: http.StatusOK, body: fmt.Sprintf(
			`{"epoch":%q,"users":[{"user":%q,"upload":0,"download":0,"connections":%d}]}`,
			epoch, canonical, maxOnlineConnections+1,
		)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.WriteHeader(test.statusCode)
				_, _ = response.Write([]byte(test.body))
			}))
			t.Cleanup(server.Close)
			if _, _, err := newRealityStatsClient(server.URL, "secret").users(t.Context()); err == nil {
				t.Fatal("users() accepted an invalid Reality API response")
			}
		})
	}
}

func TestRealityKickPersistsPartialSuccessAndRetriesOnlyRemainingTarget(t *testing.T) {
	ctx := context.Background()
	local, err := openLocalStore(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("openLocalStore() error = %v", err)
	}
	t.Cleanup(func() { _ = local.Close() })
	firstID := "00000000-0000-4000-8000-000000000001"
	secondID := "00000000-0000-4000-8000-000000000002"
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := local.queueKicks(ctx, []protocol.DesiredKick{
		{UserID: firstID, Generation: 1},
		{UserID: secondID, Generation: 1},
	}, now); err != nil {
		t.Fatalf("queueKicks() error = %v", err)
	}

	var mu sync.Mutex
	failSecond := true
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete || request.Header.Get("Authorization") != "Bearer secret" {
			http.Error(response, "invalid request", http.StatusBadRequest)
			return
		}
		userID, err := url.PathUnescape(strings.TrimPrefix(request.URL.Path, "/hyfleet/v1/users/"))
		if err != nil {
			http.Error(response, "invalid path", http.StatusBadRequest)
			return
		}
		mu.Lock()
		calls[userID]++
		shouldFail := userID == secondID && failSecond
		mu.Unlock()
		if shouldFail {
			http.Error(response, "temporary failure", http.StatusServiceUnavailable)
			return
		}
		_, _ = response.Write([]byte(`{"closed":1}`))
	}))
	t.Cleanup(server.Close)
	runner := &Agent{
		localStore:         local,
		realityStatsClient: newRealityStatsClient(server.URL, "secret"),
	}
	if err := runner.executeRealityPendingKicks(ctx); err == nil {
		t.Fatal("executeRealityPendingKicks() succeeded despite the second target failure")
	}
	pending, err := local.listPendingKicks(ctx, 10)
	if err != nil || len(pending) != 1 || pending[0].UserID != secondID {
		t.Fatalf("pending kicks after partial failure = %#v, error = %v", pending, err)
	}
	mu.Lock()
	failSecond = false
	mu.Unlock()
	if err := runner.executeRealityPendingKicks(ctx); err != nil {
		t.Fatalf("executeRealityPendingKicks(retry) error = %v", err)
	}
	pending, err = local.listPendingKicks(ctx, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending kicks after retry = %#v, error = %v", pending, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls[firstID] != 1 || calls[secondID] != 2 {
		t.Fatalf("kick calls = %#v, want first=1 second=2", calls)
	}
}

func TestRealityRestartSamplesTrafficBeforeCallingHelper(t *testing.T) {
	ctx := context.Background()
	local, err := openLocalStore(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("openLocalStore() error = %v", err)
	}
	t.Cleanup(func() { _ = local.Close() })
	userID := uuid.NewString()
	counterEpoch := uuid.NewString()
	installationID := uuid.NewString()
	if _, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		userID: {TX: 10, RX: 20},
	}, time.Now().UTC(), counterEpoch); err != nil {
		t.Fatalf("prime Reality traffic baseline: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/hyfleet/v1/users" {
			http.NotFound(response, request)
			return
		}
		fmt.Fprintf(response, `{"epoch":%q,"users":[{"user":%q,"upload":15,"download":27,"connections":1}]}`,
			counterEpoch, userID)
	}))
	t.Cleanup(server.Close)
	helperCalled := false
	runner := &Agent{
		config: config.Agent{AdapterType: "sing_box_vless_reality"},
		state:  State{InstallationID: installationID}, localStore: local,
		realityStatsClient: newRealityStatsClient(server.URL, "secret"),
		operationExecutor: func(_ context.Context, _ protocol.NodeOperation) protocol.OperationResultRequest {
			helperCalled = true
			batches, listErr := local.listTrafficOutbox(ctx, 10)
			if listErr != nil || len(batches) != 1 || len(batches[0].Items) != 1 ||
				batches[0].Items[0].UserID != userID || batches[0].Items[0].UploadBytes != 5 ||
				batches[0].Items[0].DownloadBytes != 7 {
				t.Fatalf("pre-restart traffic batches = %#v, error = %v", batches, listErr)
			}
			return protocol.OperationResultRequest{Status: "succeeded", CompletedAt: time.Now().UTC()}
		},
	}
	result := runner.executeNodeOperation(ctx, protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 1, Type: "restart_core", Attempt: 1,
	})
	if !helperCalled || result.Status != "succeeded" {
		t.Fatalf("Reality restart result = %#v; helper called = %v", result, helperCalled)
	}
}

func TestRealityRestartContinuesWhenFinalTrafficSampleFails(t *testing.T) {
	local, err := openLocalStore(t.Context(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("openLocalStore() error = %v", err)
	}
	t.Cleanup(func() { _ = local.Close() })
	helperCalled := false
	runner := &Agent{
		config: config.Agent{AdapterType: "sing_box_vless_reality"},
		logger: testLogger(), state: State{InstallationID: uuid.NewString()}, localStore: local,
		realityStatsClient: newRealityStatsClient("http://127.0.0.1:1", "secret"),
		operationExecutor: func(context.Context, protocol.NodeOperation) protocol.OperationResultRequest {
			helperCalled = true
			return protocol.OperationResultRequest{Status: "succeeded", CompletedAt: time.Now().UTC()}
		},
	}
	result := runner.executeNodeOperation(t.Context(), protocol.NodeOperation{
		ID: uuid.NewString(), Sequence: 1, Type: "restart_core", Attempt: 1,
	})
	if !helperCalled || result.Status != "succeeded" {
		t.Fatalf("failed pre-restart sample result = %#v; helper called = %v", result, helperCalled)
	}
}

func TestRealityRunTakesFinalTrafficSampleOnShutdown(t *testing.T) {
	nodeID := uuid.NewString()
	userID := uuid.NewString()
	counterEpoch := uuid.NewString()
	usageRequests := make(chan int, 4)
	controller := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/agent/v1/traffic-batches":
			writeAgentJSON(response, protocol.TrafficBatchesResponse{Results: []protocol.TrafficBatchResult{}})
		case "/agent/v1/online":
			response.WriteHeader(http.StatusNoContent)
		case "/agent/v1/heartbeat":
			writeAgentJSON(response, protocol.HeartbeatResponse{ServerTime: time.Now().UTC()})
		case "/agent/v1/telemetry":
			writeAgentJSON(response, protocol.TelemetrySnapshotResponse{Accepted: true})
		case "/agent/v1/desired":
			response.WriteHeader(http.StatusNoContent)
		case "/agent/v1/operations":
			writeAgentJSON(response, protocol.NodeOperationsResponse{Operations: []protocol.NodeOperation{}})
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(controller.Close)
	stats := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requestNumber := len(usageRequests) + 1
		usageRequests <- requestNumber
		fmt.Fprintf(response, `{"epoch":%q,"users":[{"user":%q,"upload":%d,"download":%d,"connections":0}]}`,
			counterEpoch, userID, requestNumber*10, requestNumber*20)
	}))
	t.Cleanup(stats.Close)
	runner := newEnrolledRealityAgent(t, controller.URL, nodeID)
	runner.config.RealityAPISecret = "secret"
	runner.realityStatsClient = newRealityStatsClient(stats.URL, "secret")
	runner.realityProbeExecutor = func(context.Context, nodeops.RealityProbeRequest) (nodeops.HelperResponse, error) {
		return nodeops.HelperResponse{Status: "succeeded", RealityProbe: &nodeops.RealityProbeResult{
			AdapterStatus: "compatible", AdapterVersion: "test", CoreVersion: "test",
			CoreRunning: true, ProbedAt: time.Now().UTC(),
		}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	select {
	case <-usageRequests:
	case <-time.After(2 * time.Second):
		t.Fatal("initial Reality usage sample did not run")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Agent.Run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Agent.Run() did not stop")
	}
	if got := len(usageRequests); got != 1 {
		t.Fatalf("additional Reality usage samples = %d, want final shutdown sample", got)
	}
}

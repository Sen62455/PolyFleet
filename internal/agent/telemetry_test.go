package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/config"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

type fixedTelemetryCollector struct {
	snapshot protocol.TelemetrySnapshotRequest
}

func (collector fixedTelemetryCollector) SampleTelemetry(context.Context) protocol.TelemetrySnapshotRequest {
	return collector.snapshot
}

func TestTelemetryUnsupportedEndpointDoesNotFailAgent(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		http.NotFound(response, request)
	}))
	defer server.Close()
	runner := &Agent{
		config: config.Agent{ServerURL: server.URL}, client: server.Client(),
		state: State{InstallationID: uuid.NewString(), NodeCredential: "test-credential"},
		telemetryCollector: fixedTelemetryCollector{snapshot: protocol.TelemetrySnapshotRequest{
			SampledAt: time.Now().UTC(), ProcessesAvailable: true, ServicesAvailable: true,
		}},
	}
	if err := runner.reportTelemetry(t.Context()); err != nil {
		t.Fatalf("reportTelemetry() against old Server error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("telemetry requests = %d, want 1", requests)
	}
}

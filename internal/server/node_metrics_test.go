package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

func TestNodeMetricsAPIReportsCurrentFactsAndBoundedHistory(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	created := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "monitored-node", "adapter_type": "native_hysteria2",
	}, app.csrf, "")
	requireStatus(t, created, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, created, &node)
	installationID, credential := enrollOperationAgentForTest(t, app, node.ID)

	now := time.Now().UTC().Truncate(time.Minute)
	for index, sampledAt := range []time.Time{now.Add(-2 * time.Minute), now} {
		heartbeat := protocol.HeartbeatRequest{
			InstallationID: installationID,
			Agent:          protocol.AgentInfo{Version: "v1.1.0-test", Protocol: protocol.MajorVersion},
			Core:           protocol.CoreInfo{Name: "hysteria", Version: "v2-test", Running: true},
			Host: protocol.HostMetrics{
				Hostname: "monitor-host", KernelVersion: "6.8.0-test", CPUCores: 2,
				UptimeSeconds: int64(600 + index), CPUPercent: float64(10 + index),
				MemoryUsedBytes: 256, MemoryTotalBytes: 1024,
				SwapUsedBytes: 64, SwapTotalBytes: 256,
				DiskUsedBytes: 512, DiskTotalBytes: 2048,
				DiskReadBytesPerSecond: 32, DiskWriteBytesPerSecond: 16,
				NetworkRXBPS: 8000, NetworkTXBPS: 4000,
				NetworkRXBytesTotal: 12000, NetworkTXBytesTotal: 6000,
				Load1: 0.2, Load5: 0.1, Load15: 0.05,
			},
			SampledAt: sampledAt,
		}
		response := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/heartbeat",
			heartbeat, credential, cryptoutil.NewID())
		requireStatus(t, response, http.StatusOK)
	}

	current := app.request(t, http.MethodGet, "/api/v1/nodes/"+node.ID, nil, "", "")
	requireStatus(t, current, http.StatusOK)
	decodeResponse(t, current, &node)
	if node.Hostname != "monitor-host" || node.KernelVersion != "6.8.0-test" ||
		node.CPUCores != 2 || node.SwapUsedBytes != 64 ||
		node.DiskReadBytesPerSecond != 32 || node.NetworkRXBytesTotal != 12000 {
		t.Fatalf("current monitored node = %#v", node)
	}

	history := app.request(t, http.MethodGet,
		"/api/v1/nodes/"+node.ID+"/metrics?range=1h", nil, "", "")
	requireStatus(t, history, http.StatusOK)
	var series struct {
		Range       string               `json:"range"`
		StepSeconds int64                `json:"step_seconds"`
		Samples     []nodeMetricResponse `json:"samples"`
	}
	decodeResponse(t, history, &series)
	if series.Range != "1h" || series.StepSeconds != 60 || len(series.Samples) != 2 {
		t.Fatalf("metric series = %#v", series)
	}
	if series.Samples[1].CPUPercent != 11 || series.Samples[1].SwapTotalBytes != 256 {
		t.Fatalf("latest metric sample = %#v", series.Samples[1])
	}

	invalid := app.request(t, http.MethodGet,
		"/api/v1/nodes/"+node.ID+"/metrics?range=2h", nil, "", "")
	requireStatus(t, invalid, http.StatusUnprocessableEntity)
	missing := app.request(t, http.MethodGet,
		"/api/v1/nodes/"+cryptoutil.NewID()+"/metrics?range=1h", nil, "", "")
	requireStatus(t, missing, http.StatusNotFound)
}

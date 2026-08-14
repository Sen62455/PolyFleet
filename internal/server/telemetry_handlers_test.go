package server

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

func TestNodeTelemetryLatestSnapshotAndFailureRetention(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	created := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "telemetry-node", "adapter_type": "native_hysteria2",
	}, app.csrf, "")
	requireStatus(t, created, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, created, &node)

	empty := app.request(t, http.MethodGet, "/api/v1/nodes/"+node.ID+"/telemetry", nil, "", "")
	requireStatus(t, empty, http.StatusOK)
	var view nodeTelemetryResponse
	decodeResponse(t, empty, &view)
	if view.Supported || view.SampledAt != nil || view.Processes == nil || view.Services == nil {
		t.Fatalf("empty telemetry = %#v", view)
	}

	installationID, credential := enrollOperationAgentForTest(t, app, node.ID)
	base := time.Now().UTC().Truncate(time.Millisecond)
	first := protocol.TelemetrySnapshotRequest{
		InstallationID: installationID, SampledAt: base,
		ProcessesAvailable: true, ProcessesTotal: 1,
		Processes: []protocol.ProcessTelemetry{{
			PID: 10, Name: "hysteria", Unit: "hysteria-server.service",
			CPUPercent: 2, RSSBytes: 1024, UptimeSeconds: 60,
		}},
		ServicesAvailable: true, ServicesTotal: 1,
		Services: []protocol.ServiceTelemetry{{
			Unit: "hysteria-server.service", Description: "Hysteria",
			ActiveState: "active", SubState: "running", CPUPercent: 2,
			CPUPeakPercent: 3, MemoryBytes: 1024, MemoryPeakBytes: 2048,
			Tasks: 2, MainPID: 10,
		}},
	}
	recorded := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/telemetry",
		first, credential, cryptoutil.NewID())
	requireStatus(t, recorded, http.StatusOK)

	partialFailure := protocol.TelemetrySnapshotRequest{
		InstallationID: installationID, SampledAt: base.Add(time.Second),
		ProcessesErrorCode: "process_collection_failed",
		ServicesAvailable:  true, ServicesTotal: 1,
		Services: []protocol.ServiceTelemetry{{
			Unit: "hysteria-server.service", ActiveState: "active", SubState: "running",
			CPUPercent: 1, CPUPeakPercent: 3, MemoryBytes: 2048,
		}},
	}
	recorded = agentRequest(t, app.handler, http.MethodPost, "/agent/v1/telemetry",
		partialFailure, credential, cryptoutil.NewID())
	requireStatus(t, recorded, http.StatusOK)

	stale := first
	stale.SampledAt = base.Add(-time.Minute)
	stale.Processes[0].Name = "stale-process"
	recorded = agentRequest(t, app.handler, http.MethodPost, "/agent/v1/telemetry",
		stale, credential, cryptoutil.NewID())
	requireStatus(t, recorded, http.StatusOK)

	current := app.request(t, http.MethodGet, "/api/v1/nodes/"+node.ID+"/telemetry", nil, "", "")
	requireStatus(t, current, http.StatusOK)
	decodeResponse(t, current, &view)
	if !view.Supported || view.ProcessesAvailable || view.ProcessesErrorCode != "process_collection_failed" ||
		len(view.Processes) != 1 || view.Processes[0].Name != "hysteria" ||
		view.ProcessesSampledAt == nil || !view.ProcessesSampledAt.Equal(base) ||
		!view.ServicesAvailable || len(view.Services) != 1 || view.Services[0].MemoryBytes != 2048 ||
		view.ServicesSampledAt == nil || !view.ServicesSampledAt.Equal(base.Add(time.Second)) {
		t.Fatalf("retained telemetry = %#v", view)
	}

	wrongInstallation := partialFailure
	wrongInstallation.InstallationID = uuid.NewString()
	rejected := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/telemetry",
		wrongInstallation, credential, cryptoutil.NewID())
	requireStatus(t, rejected, http.StatusConflict)

	missing := app.request(t, http.MethodGet, "/api/v1/nodes/"+uuid.NewString()+"/telemetry", nil, "", "")
	requireStatus(t, missing, http.StatusNotFound)
}

func TestTelemetryValidationRejectsUnboundedOrInvalidData(t *testing.T) {
	now := time.Now().UTC()
	valid := protocol.TelemetrySnapshotRequest{
		InstallationID: uuid.NewString(), SampledAt: now,
		ProcessesAvailable: true, ServicesAvailable: true,
	}
	if !validTelemetrySnapshot(valid, now) {
		t.Fatal("validTelemetrySnapshot() rejected an empty available snapshot")
	}
	invalid := valid
	invalid.Processes = make([]protocol.ProcessTelemetry, protocol.MaxTelemetryProcesses+1)
	invalid.ProcessesTotal = len(invalid.Processes)
	for index := range invalid.Processes {
		invalid.Processes[index] = protocol.ProcessTelemetry{PID: index + 1, Name: "process"}
	}
	if validTelemetrySnapshot(invalid, now) {
		t.Fatal("validTelemetrySnapshot() accepted too many processes")
	}
	invalid = valid
	invalid.ServicesErrorCode = "raw error text"
	invalid.ServicesAvailable = false
	if validTelemetrySnapshot(invalid, now) {
		t.Fatal("validTelemetrySnapshot() accepted an unsafe error code")
	}
}

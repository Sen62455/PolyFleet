package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

func TestNodePingOperationValidatesAndDeliversTarget(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	created := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "ping-node", "adapter_type": "native_hysteria2",
	}, app.csrf, "")
	requireStatus(t, created, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, created, &node)
	_, credential := enrollOperationAgentForTest(t, app, node.ID)

	invalid := app.request(t, http.MethodPost, "/api/v1/nodes/"+node.ID+"/operations",
		map[string]any{"type": "ping", "target": "example.com"}, app.csrf, "")
	requireStatus(t, invalid, http.StatusUnprocessableEntity)
	queued := app.request(t, http.MethodPost, "/api/v1/nodes/"+node.ID+"/operations",
		map[string]any{"type": "ping", "target": "42.49.64.154"}, app.csrf, "")
	requireStatus(t, queued, http.StatusCreated)
	var operation nodeOperationResponse
	decodeResponse(t, queued, &operation)
	if operation.Type != "ping" || operation.Target != "42.49.64.154" {
		t.Fatalf("queued ping operation = %#v", operation)
	}

	poll := agentRequest(t, app.handler, http.MethodGet,
		"/agent/v1/operations?after=0", nil, credential, cryptoutil.NewID())
	requireStatus(t, poll, http.StatusOK)
	var pending protocol.NodeOperationsResponse
	decodeResponse(t, poll, &pending)
	if len(pending.Operations) != 1 || pending.Operations[0].Target != "42.49.64.154" {
		t.Fatalf("pending ping operations = %#v", pending.Operations)
	}
}

func TestNodeOperationAPIQueuesReportsRetriesAndAlerts(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	created := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "managed-operation-node", "adapter_type": "native_hysteria2",
	}, app.csrf, "")
	requireStatus(t, created, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, created, &node)
	beforeEnrollment := app.request(t, http.MethodPost,
		"/api/v1/nodes/"+node.ID+"/operations",
		map[string]any{"type": "probe_core"}, app.csrf, "")
	requireStatus(t, beforeEnrollment, http.StatusConflict)
	installationID, credential := enrollOperationAgentForTest(t, app, node.ID)

	invalid := app.request(t, http.MethodPost,
		"/api/v1/nodes/"+node.ID+"/operations",
		map[string]any{"type": "run_shell"}, app.csrf, "")
	requireStatus(t, invalid, http.StatusUnprocessableEntity)
	heartbeat := protocol.HeartbeatRequest{
		InstallationID: installationID,
		Agent:          protocol.AgentInfo{Version: "v0.6.0-test", Protocol: protocol.MajorVersion},
		Core:           protocol.CoreInfo{Name: "hysteria", Running: true},
		Host:           protocol.HostMetrics{MemoryTotalBytes: 1, DiskTotalBytes: 1},
		SampledAt:      time.Now().UTC(),
	}
	beat := agentRequest(t, app.handler, http.MethodPost, "/agent/v1/heartbeat",
		heartbeat, credential, cryptoutil.NewID())
	requireStatus(t, beat, http.StatusOK)
	createdOperation := app.request(t, http.MethodPost,
		"/api/v1/nodes/"+node.ID+"/operations",
		map[string]any{"type": "tail_core_log", "max_lines": 20}, app.csrf, "")
	requireStatus(t, createdOperation, http.StatusCreated)
	var operation nodeOperationResponse
	decodeResponse(t, createdOperation, &operation)
	if operation.Sequence != 1 || operation.Status != "queued" || operation.MaxLines != 20 {
		t.Fatalf("created operation = %#v", operation)
	}

	polled := agentRequest(t, app.handler, http.MethodGet,
		"/agent/v1/operations?after=0", nil, credential, cryptoutil.NewID())
	requireStatus(t, polled, http.StatusOK)
	var pending protocol.NodeOperationsResponse
	decodeResponse(t, polled, &pending)
	if len(pending.Operations) != 1 || pending.Operations[0].ID != operation.ID {
		t.Fatalf("pending operations = %#v", pending)
	}
	result := protocol.OperationResultRequest{
		Sequence: operation.Sequence, Status: "failed",
		Output:    "authorization: Bearer must-not-be-stored\nlog line",
		ErrorCode: "core_log_failed", ErrorMessage: "journal unavailable",
		CompletedAt: time.Now().UTC(),
	}
	reported := agentRequest(t, app.handler, http.MethodPost,
		"/agent/v1/operations/"+operation.ID+"/result",
		result, credential, cryptoutil.NewID())
	requireStatus(t, reported, http.StatusNoContent)
	duplicate := agentRequest(t, app.handler, http.MethodPost,
		"/agent/v1/operations/"+operation.ID+"/result",
		result, credential, cryptoutil.NewID())
	requireStatus(t, duplicate, http.StatusNoContent)

	listed := app.request(t, http.MethodGet,
		"/api/v1/nodes/"+node.ID+"/operations", nil, "", "")
	requireStatus(t, listed, http.StatusOK)
	if strings.Contains(listed.Body.String(), "must-not-be-stored") ||
		!strings.Contains(listed.Body.String(), "[REDACTED]") {
		t.Fatalf("operation log was not redacted: %s", listed.Body.String())
	}
	retried := app.request(t, http.MethodPost,
		"/api/v1/nodes/"+node.ID+"/operations/"+operation.ID+"/retry",
		map[string]any{}, app.csrf, "")
	requireStatus(t, retried, http.StatusCreated)
	var retry nodeOperationResponse
	decodeResponse(t, retried, &retry)
	if retry.Sequence != 2 || retry.Attempt != 2 || retry.RetryOf != operation.ID {
		t.Fatalf("retry operation = %#v", retry)
	}
	global := app.request(t, http.MethodGet,
		"/api/v1/operations?node_id="+node.ID+"&status=failed&limit=10&offset=0", nil, "", "")
	requireStatus(t, global, http.StatusOK)
	var globalPage struct {
		Operations []nodeOperationResponse `json:"operations"`
		Total      int                     `json:"total"`
		Limit      int                     `json:"limit"`
		Offset     int                     `json:"offset"`
	}
	decodeResponse(t, global, &globalPage)
	if globalPage.Total != 1 || globalPage.Limit != 10 || globalPage.Offset != 0 ||
		len(globalPage.Operations) != 1 || globalPage.Operations[0].ID != operation.ID ||
		globalPage.Operations[0].RequestedBy != "admin" {
		t.Fatalf("global operation page = %#v", globalPage)
	}
	invalidFilter := app.request(t, http.MethodGet,
		"/api/v1/operations?status=unknown", nil, "", "")
	requireStatus(t, invalidFilter, http.StatusUnprocessableEntity)

	alerts := app.request(t, http.MethodGet, "/api/v1/alerts", nil, "", "")
	requireStatus(t, alerts, http.StatusOK)
	var alertList struct {
		Alerts []alertResponse `json:"alerts"`
	}
	decodeResponse(t, alerts, &alertList)
	if len(alertList.Alerts) != 1 || alertList.Alerts[0].Type != "operation_failed" {
		t.Fatalf("operation alerts = %#v", alertList.Alerts)
	}
	acknowledged := app.request(t, http.MethodPost,
		"/api/v1/alerts/"+alertList.Alerts[0].ID+"/acknowledge",
		map[string]any{}, app.csrf, "")
	requireStatus(t, acknowledged, http.StatusOK)

}

func enrollOperationAgentForTest(t *testing.T, app *testApp, nodeID string) (string, string) {
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
		RequestID: requestID, AgentVersion: "v0.6.0-test", OS: "linux",
		OSVersion: "24.04", Architecture: "amd64",
		Capabilities: []string{"node_operations_v1", "operation_result_outbox_v1"},
		Adapter:      protocol.EnrollmentAdapter{Type: "native_hysteria2", CoreName: "hysteria"},
	}, "", requestID)
	requireStatus(t, enrolled, http.StatusOK)
	var enrollment protocol.EnrollResponse
	decodeResponse(t, enrolled, &enrollment)
	return installationID, enrollment.NodeCredential
}

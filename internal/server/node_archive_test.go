package server

import (
	"net/http"
	"testing"

	"github.com/Sen62455/PolyFleet/internal/protocol"
)

func TestNodeArchiveDistinguishesPendingRemovalAndAllowsExplicitForce(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	created := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "offline-archive-node", "adapter_type": "native_hysteria2",
	}, app.csrf, "")
	requireStatus(t, created, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, created, &node)
	_, _ = enrollOperationAgentForTest(t, app, node.ID)

	disabled := app.request(t, http.MethodPut, "/api/v1/nodes/"+node.ID, map[string]any{
		"name": node.Name, "adapter_type": node.AdapterType, "enabled": false,
	}, app.csrf, "")
	requireStatus(t, disabled, http.StatusOK)

	blocked := app.request(t, http.MethodDelete, "/api/v1/nodes/"+node.ID, nil, app.csrf, "")
	requireStatus(t, blocked, http.StatusConflict)
	var blockedError protocol.ErrorResponse
	decodeResponse(t, blocked, &blockedError)
	if blockedError.Error.Code != "node_pending_removals" {
		t.Fatalf("archive error = %#v", blockedError)
	}

	forced := app.request(
		t, http.MethodDelete, "/api/v1/nodes/"+node.ID+"?force=true", nil, app.csrf, "",
	)
	requireStatus(t, forced, http.StatusNoContent)
}

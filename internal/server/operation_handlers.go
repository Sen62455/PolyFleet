package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/nodeops"
	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/Sen62455/PolyFleet/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type nodeOperationRequest struct {
	Type     string `json:"type"`
	MaxLines int    `json:"max_lines"`
	Target   string `json:"target"`
}

type nodeOperationResponse struct {
	ID           string     `json:"id"`
	NodeID       string     `json:"node_id"`
	NodeName     string     `json:"node_name"`
	Sequence     int64      `json:"sequence"`
	Type         string     `json:"type"`
	Status       string     `json:"status"`
	RetryOf      string     `json:"retry_of,omitempty"`
	Attempt      int        `json:"attempt"`
	MaxLines     int        `json:"max_lines"`
	Target       string     `json:"target"`
	Output       string     `json:"output"`
	ErrorCode    string     `json:"error_code"`
	ErrorMessage string     `json:"error_message"`
	RolledBack   bool       `json:"rolled_back"`
	RequestedBy  string     `json:"requested_by"`
	ExpiresAt    time.Time  `json:"expires_at"`
	StartedAt    *time.Time `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func (a *App) handleListOperations(response http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	limit := boundedQueryLimit(query.Get("limit"), 20, 100)
	offset, err := strconv.Atoi(query.Get("offset"))
	if query.Get("offset") == "" {
		offset = 0
	} else if err != nil || offset < 0 || offset > 1_000_000 {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "offset must be a non-negative integer")
		return
	}
	page, err := a.store.ListOperations(request.Context(), store.OperationFilter{
		NodeID: strings.TrimSpace(query.Get("node_id")), Type: strings.TrimSpace(query.Get("type")),
		Status: strings.TrimSpace(query.Get("status")), Limit: limit, Offset: offset,
	})
	if errors.Is(err, store.ErrUnsupported) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "operation filters are invalid")
		return
	}
	if err != nil {
		a.logger.Error("list operations failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "operations_read_failed", "could not read operations")
		return
	}
	result := make([]nodeOperationResponse, 0, len(page.Operations))
	for _, operation := range page.Operations {
		result = append(result, presentNodeOperation(operation))
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"operations": result, "total": page.Total, "limit": limit, "offset": offset,
	})
}

type configBackupResponse struct {
	ID          string    `json:"id"`
	NodeID      string    `json:"node_id"`
	NodeName    string    `json:"node_name"`
	OperationID string    `json:"operation_id"`
	LocalPath   string    `json:"local_path"`
	SHA256      string    `json:"sha256"`
	SizeBytes   int64     `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

type alertResponse struct {
	ID              string     `json:"id"`
	NodeID          string     `json:"node_id"`
	NodeName        string     `json:"node_name"`
	Type            string     `json:"type"`
	Severity        string     `json:"severity"`
	Status          string     `json:"status"`
	Message         string     `json:"message"`
	OccurrenceCount int        `json:"occurrence_count"`
	FirstSeenAt     time.Time  `json:"first_seen_at"`
	LastSeenAt      time.Time  `json:"last_seen_at"`
	AcknowledgedBy  string     `json:"acknowledged_by,omitempty"`
	AcknowledgedAt  *time.Time `json:"acknowledged_at"`
	ResolvedAt      *time.Time `json:"resolved_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func (a *App) handleListNodeOperations(response http.ResponseWriter, request *http.Request) {
	limit := boundedQueryLimit(request.URL.Query().Get("limit"), 50, 100)
	operations, err := a.store.ListNodeOperations(
		request.Context(), chi.URLParam(request, "nodeID"), limit,
	)
	if a.writeOperationStoreError(response, request, err) {
		return
	}
	result := make([]nodeOperationResponse, 0, len(operations))
	for _, operation := range operations {
		result = append(result, presentNodeOperation(operation))
	}
	writeJSON(response, http.StatusOK, map[string]any{"operations": result})
}

func (a *App) handleCreateNodeOperation(response http.ResponseWriter, request *http.Request) {
	var input nodeOperationRequest
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid operation request")
		return
	}
	input.Type = strings.TrimSpace(input.Type)
	session := sessionFromContext(request.Context())
	operation, err := a.store.CreateTargetedNodeOperation(
		request.Context(), chi.URLParam(request, "nodeID"), input.Type,
		input.MaxLines, input.Target, session.AdminID, time.Now().UTC(),
	)
	if a.writeOperationStoreError(response, request, err) {
		return
	}
	writeJSON(response, http.StatusCreated, presentNodeOperation(operation))
}

func (a *App) handleRetryNodeOperation(response http.ResponseWriter, request *http.Request) {
	session := sessionFromContext(request.Context())
	operation, err := a.store.RetryNodeOperation(
		request.Context(), chi.URLParam(request, "nodeID"),
		chi.URLParam(request, "operationID"), session.AdminID, time.Now().UTC(),
	)
	if a.writeOperationStoreError(response, request, err) {
		return
	}
	writeJSON(response, http.StatusCreated, presentNodeOperation(operation))
}

func (a *App) handleRetryNodeSync(response http.ResponseWriter, request *http.Request) {
	node, err := a.store.RetryNodeSync(
		request.Context(), chi.URLParam(request, "nodeID"), time.Now().UTC(),
	)
	if a.writeOperationStoreError(response, request, err) {
		return
	}
	writeJSON(response, http.StatusOK, a.presentNode(node, time.Now().UTC()))
}

func (a *App) handleListConfigBackups(response http.ResponseWriter, request *http.Request) {
	limit := boundedQueryLimit(request.URL.Query().Get("limit"), 50, 100)
	backups, err := a.store.ListConfigBackups(
		request.Context(), chi.URLParam(request, "nodeID"), limit,
	)
	if a.writeOperationStoreError(response, request, err) {
		return
	}
	result := make([]configBackupResponse, 0, len(backups))
	for _, backup := range backups {
		result = append(result, configBackupResponse{
			ID: backup.ID, NodeID: backup.NodeID, NodeName: backup.NodeName,
			OperationID: backup.OperationID, LocalPath: backup.LocalPath,
			SHA256: backup.SHA256, SizeBytes: backup.SizeBytes, CreatedAt: backup.CreatedAt,
		})
	}
	writeJSON(response, http.StatusOK, map[string]any{"backups": result})
}

func (a *App) handleListAlerts(response http.ResponseWriter, request *http.Request) {
	now := time.Now().UTC()
	if err := a.store.ReconcileAlerts(
		request.Context(), now, a.config.OfflineAfter, 5*time.Minute,
	); err != nil {
		a.logger.Error("reconcile alerts failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "alerts_reconcile_failed", "could not reconcile alerts")
		return
	}
	status := strings.TrimSpace(request.URL.Query().Get("status"))
	limit := boundedQueryLimit(request.URL.Query().Get("limit"), 100, 200)
	offset := queryOffset(request.URL.Query().Get("offset"))
	alerts, total, err := a.store.ListAlertsPage(request.Context(), store.AlertFilter{
		Status: status,
		NodeID: strings.TrimSpace(request.URL.Query().Get("node_id")),
		Type:   strings.TrimSpace(request.URL.Query().Get("type")),
		Limit:  limit,
		Offset: offset,
	})
	if errors.Is(err, store.ErrUnsupported) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "status must be active, resolved, or all")
		return
	}
	if err != nil {
		a.logger.Error("list alerts failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "alerts_read_failed", "could not read alerts")
		return
	}
	result := make([]alertResponse, 0, len(alerts))
	for _, alert := range alerts {
		result = append(result, presentAlert(alert))
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"alerts": result, "total": total, "limit": limit, "offset": offset,
	})
}

func (a *App) handleAcknowledgeAlert(response http.ResponseWriter, request *http.Request) {
	session := sessionFromContext(request.Context())
	alert, err := a.store.AcknowledgeAlert(
		request.Context(), chi.URLParam(request, "alertID"), session.AdminID, time.Now().UTC(),
	)
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "alert_not_found", "alert not found")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		a.writeError(response, request, http.StatusConflict, "alert_resolved", "resolved alert cannot be acknowledged")
		return
	}
	if err != nil {
		a.logger.Error("acknowledge alert failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "alert_acknowledge_failed", "could not acknowledge alert")
		return
	}
	writeJSON(response, http.StatusOK, presentAlert(alert))
}

func (a *App) handleAgentOperations(response http.ResponseWriter, request *http.Request) {
	after, err := strconv.ParseInt(request.URL.Query().Get("after"), 10, 64)
	if err != nil || after < 0 {
		a.writeError(response, request, http.StatusBadRequest, "invalid_sequence", "after must be a non-negative integer")
		return
	}
	now := time.Now().UTC()
	operations, err := a.store.ListPendingNodeOperations(
		request.Context(), agentFromContext(request.Context()), after, now, 20,
	)
	if errors.Is(err, store.ErrConflict) {
		a.writeError(response, request, http.StatusConflict, "installation_conflict", "Agent installation does not match enrollment")
		return
	}
	if err != nil {
		a.logger.Error("list Agent operations failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "operations_read_failed", "operations are unavailable")
		return
	}
	writeJSON(response, http.StatusOK, protocol.NodeOperationsResponse{
		Operations: operations, ServerTime: now,
	})
}

func (a *App) handleAgentOperationResult(response http.ResponseWriter, request *http.Request) {
	operationID := chi.URLParam(request, "operationID")
	if _, err := uuid.Parse(operationID); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_operation", "operation ID is invalid")
		return
	}
	var input protocol.OperationResultRequest
	if err := decodeJSON(response, request, &input, 64*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid operation result")
		return
	}
	now := time.Now().UTC()
	if input.Sequence < 1 || (input.Status != "succeeded" && input.Status != "failed") ||
		input.CompletedAt.IsZero() || input.CompletedAt.After(now.Add(5*time.Minute)) ||
		len(input.ErrorCode) > 64 || !validOperationErrorCode(input.ErrorCode) ||
		(input.Status == "failed" && input.ErrorCode == "") ||
		(input.Status == "succeeded" && (input.ErrorCode != "" || input.ErrorMessage != "")) ||
		(input.Backup != nil && (input.Backup.SizeBytes < 0 || len(input.Backup.LocalPath) > 512)) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "operation result fields are invalid")
		return
	}
	input.Output = nodeops.SanitizeOutput(input.Output, nodeops.MaxLogLines, nodeops.MaxOutputSize)
	input.ErrorMessage = nodeops.SanitizeMessage(input.ErrorMessage, 512)
	err := a.store.RecordNodeOperationResult(
		request.Context(), agentFromContext(request.Context()), operationID, input, now,
	)
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "operation_not_found", "operation not found")
		return
	}
	if errors.Is(err, store.ErrExpired) {
		a.writeError(response, request, http.StatusGone, "operation_expired", "operation expired")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		a.writeError(response, request, http.StatusConflict, "operation_result_conflict", "operation result conflicts with recorded state")
		return
	}
	if errors.Is(err, store.ErrUnsupported) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "operation result metadata is invalid")
		return
	}
	if err != nil {
		a.logger.Error("record operation result failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "operation_result_failed", "operation result could not be recorded")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (a *App) writeOperationStoreError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		a.writeError(response, request, http.StatusNotFound, "operation_resource_not_found", "node or operation not found")
	case errors.Is(err, store.ErrPending):
		a.writeError(response, request, http.StatusConflict, "node_not_enrolled", "node Agent is not enrolled")
	case errors.Is(err, store.ErrConflict):
		a.writeError(response, request, http.StatusConflict, "operation_conflict", "operation is not eligible for this action")
	case errors.Is(err, store.ErrUnsupported):
		a.writeError(response, request, http.StatusUnprocessableEntity, "operation_unsupported", "operation type or parameters are unsupported")
	default:
		a.logger.Error("node operation failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "operation_failed", "node operation could not be completed")
	}
	return true
}

func presentNodeOperation(operation store.NodeOperation) nodeOperationResponse {
	return nodeOperationResponse{
		ID: operation.ID, NodeID: operation.NodeID, NodeName: operation.NodeName,
		Sequence: operation.Sequence, Type: operation.Type, Status: operation.Status,
		RetryOf: operation.RetryOf, Attempt: operation.Attempt, MaxLines: operation.MaxLines,
		Target: operation.Target,
		Output: operation.Output, ErrorCode: operation.ErrorCode,
		ErrorMessage: operation.ErrorMessage, RolledBack: operation.RolledBack,
		RequestedBy: operation.RequestedBy,
		ExpiresAt:   operation.ExpiresAt, StartedAt: operation.StartedAt,
		CompletedAt: operation.CompletedAt, CreatedAt: operation.CreatedAt,
		UpdatedAt: operation.UpdatedAt,
	}
}

func presentAlert(alert store.Alert) alertResponse {
	return alertResponse{
		ID: alert.ID, NodeID: alert.NodeID, NodeName: alert.NodeName,
		Type: alert.Type, Severity: alert.Severity, Status: alert.Status,
		Message: alert.Message, OccurrenceCount: alert.OccurrenceCount,
		FirstSeenAt: alert.FirstSeenAt, LastSeenAt: alert.LastSeenAt,
		AcknowledgedBy: alert.AcknowledgedBy, AcknowledgedAt: alert.AcknowledgedAt,
		ResolvedAt: alert.ResolvedAt, CreatedAt: alert.CreatedAt, UpdatedAt: alert.UpdatedAt,
	}
}

func validOperationErrorCode(value string) bool {
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') &&
			character != '_' && character != '-' && character != '.' {
			return false
		}
	}
	return true
}

func boundedQueryLimit(value string, fallback, maximum int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > maximum {
		return fallback
	}
	return parsed
}

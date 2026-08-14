package server

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/store"
	"github.com/go-chi/chi/v5"
)

type userRequest struct {
	Username          string     `json:"username"`
	DisplayName       string     `json:"display_name"`
	Notes             string     `json:"notes"`
	Enabled           *bool      `json:"enabled"`
	ExpiresAt         *time.Time `json:"expires_at"`
	TrafficLimitBytes *int64     `json:"traffic_limit_bytes"`
	NodeIDs           []string   `json:"node_ids"`
}

type assignmentRequest struct {
	NodeID            string `json:"node_id"`
	Enabled           *bool  `json:"enabled"`
	TrafficLimitBytes *int64 `json:"traffic_limit_bytes"`
}

type kickUserRequest struct {
	NodeID string `json:"node_id"`
}

type userResponse struct {
	ID                   string               `json:"id"`
	Username             string               `json:"username"`
	DisplayName          string               `json:"display_name"`
	Notes                string               `json:"notes"`
	Enabled              bool                 `json:"enabled"`
	ExpiresAt            *time.Time           `json:"expires_at"`
	Status               string               `json:"status"`
	TrafficLimitBytes    int64                `json:"traffic_limit_bytes"`
	TrafficUploadBytes   int64                `json:"traffic_upload_bytes"`
	TrafficDownloadBytes int64                `json:"traffic_download_bytes"`
	TrafficUsedBytes     int64                `json:"traffic_used_bytes"`
	QuotaState           string               `json:"quota_state"`
	LastTrafficAt        *time.Time           `json:"last_traffic_at"`
	OnlineConnections    int                  `json:"online_connections"`
	OnlineNodes          int                  `json:"online_nodes"`
	Assignments          []assignmentResponse `json:"assignments"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
}

type assignmentResponse struct {
	ID                    string     `json:"id"`
	NodeID                string     `json:"node_id"`
	NodeName              string     `json:"node_name"`
	NodeAdapter           string     `json:"node_adapter"`
	Enabled               bool       `json:"enabled"`
	TrafficLimitBytes     int64      `json:"traffic_limit_bytes"`
	TrafficUploadBytes    int64      `json:"traffic_upload_bytes"`
	TrafficDownloadBytes  int64      `json:"traffic_download_bytes"`
	TrafficUsedBytes      int64      `json:"traffic_used_bytes"`
	QuotaState            string     `json:"quota_state"`
	LastTrafficAt         *time.Time `json:"last_traffic_at"`
	OnlineConnections     int        `json:"online_connections"`
	OnlineSampledAt       *time.Time `json:"online_sampled_at"`
	KickGeneration        int64      `json:"kick_generation"`
	CredentialFingerprint string     `json:"credential_fingerprint"`
	ManagementMode        string     `json:"management_mode"`
	RemoteClientID        int64      `json:"remote_client_id,omitempty"`
	SubscriptionEligible  bool       `json:"subscription_eligible"`
	SubscriptionReason    string     `json:"subscription_reason"`
	DesiredVersion        int64      `json:"desired_version"`
	AppliedVersion        int64      `json:"applied_version"`
	State                 string     `json:"state"`
	LastErrorCode         string     `json:"last_error_code"`
	LastErrorMessage      string     `json:"last_error_message"`
	LastAttemptAt         *time.Time `json:"last_attempt_at"`
	AppliedAt             *time.Time `json:"applied_at"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

const maxJSONSafeInteger = int64(1<<53 - 1)

type credentialResponse struct {
	NodeID                string `json:"node_id"`
	NodeName              string `json:"node_name"`
	Credential            string `json:"credential"`
	CredentialFingerprint string `json:"credential_fingerprint"`
}

func (a *App) handleListUsers(response http.ResponseWriter, request *http.Request) {
	users, err := a.store.ListUsers(request.Context())
	if err != nil {
		a.logger.Error("list users failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "users_read_failed", "could not read users")
		return
	}
	now := time.Now().UTC()
	result := make([]userResponse, 0, len(users))
	for _, user := range users {
		result = append(result, presentUser(user, now))
	}
	writeJSON(response, http.StatusOK, map[string]any{"users": result})
}

func (a *App) handleGetUser(response http.ResponseWriter, request *http.Request) {
	user, err := a.store.GetUser(request.Context(), chi.URLParam(request, "userID"))
	if a.writeUserStoreError(response, request, err) {
		return
	}
	writeJSON(response, http.StatusOK, presentUser(user, time.Now().UTC()))
}

func (a *App) handleCreateUser(response http.ResponseWriter, request *http.Request) {
	var input userRequest
	if err := decodeJSON(response, request, &input, 64*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid user request")
		return
	}
	input = normalizeUserRequest(input)
	if message := validateUserRequest(input, false); message != "" {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", message)
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	trafficLimit := int64(0)
	if input.TrafficLimitBytes != nil {
		trafficLimit = *input.TrafficLimitBytes
	}
	now := time.Now().UTC()
	user, credentials, err := a.store.CreateUser(request.Context(), store.NewUser{
		ID: cryptoutil.NewID(), Username: input.Username, DisplayName: input.DisplayName,
		Notes: input.Notes, Enabled: enabled, ExpiresAt: input.ExpiresAt,
		TrafficLimitBytes: trafficLimit,
		NodeIDs:           input.NodeIDs, Now: now,
	}, a.masterKey)
	if a.writeUserStoreError(response, request, err) {
		return
	}
	setCredentialResponseHeaders(response)
	writeJSON(response, http.StatusCreated, map[string]any{
		"user":        presentUser(user, now),
		"credentials": presentCredentials(credentials),
	})
}

func (a *App) handleUpdateUser(response http.ResponseWriter, request *http.Request) {
	var input userRequest
	if err := decodeJSON(response, request, &input, 64*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid user request")
		return
	}
	input = normalizeUserRequest(input)
	if message := validateUserRequest(input, true); message != "" {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", message)
		return
	}
	now := time.Now().UTC()
	trafficLimit := int64(0)
	if input.TrafficLimitBytes == nil {
		current, err := a.store.GetUser(request.Context(), chi.URLParam(request, "userID"))
		if a.writeUserStoreError(response, request, err) {
			return
		}
		trafficLimit = current.TrafficLimitBytes
	} else {
		trafficLimit = *input.TrafficLimitBytes
	}
	user, err := a.store.UpdateUser(request.Context(), chi.URLParam(request, "userID"), store.UpdateUser{
		Username: input.Username, DisplayName: input.DisplayName, Notes: input.Notes,
		Enabled: *input.Enabled, ExpiresAt: input.ExpiresAt,
		TrafficLimitBytes: trafficLimit, Now: now,
	})
	if a.writeUserStoreError(response, request, err) {
		return
	}
	writeJSON(response, http.StatusOK, presentUser(user, now))
}

func (a *App) handleArchiveUser(response http.ResponseWriter, request *http.Request) {
	err := a.store.ArchiveUser(
		request.Context(), chi.URLParam(request, "userID"), time.Now().UTC(),
	)
	if a.writeUserStoreError(response, request, err) {
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (a *App) handleAssignUser(response http.ResponseWriter, request *http.Request) {
	var input assignmentRequest
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid assignment request")
		return
	}
	input.NodeID = strings.TrimSpace(input.NodeID)
	if input.NodeID == "" {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "node_id is required")
		return
	}
	trafficLimit := int64(0)
	if input.TrafficLimitBytes != nil {
		if !validTrafficLimit(*input.TrafficLimitBytes) {
			a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "traffic_limit_bytes must be between 0 and the JavaScript safe integer limit")
			return
		}
		trafficLimit = *input.TrafficLimitBytes
	}
	now := time.Now().UTC()
	user, credential, err := a.store.AssignUser(
		request.Context(), chi.URLParam(request, "userID"), input.NodeID, trafficLimit, now, a.masterKey,
	)
	if a.writeUserStoreError(response, request, err) {
		return
	}
	setCredentialResponseHeaders(response)
	writeJSON(response, http.StatusCreated, map[string]any{
		"user":       presentUser(user, now),
		"credential": presentCredential(credential),
	})
}

func (a *App) handleUpdateAssignment(response http.ResponseWriter, request *http.Request) {
	var input assignmentRequest
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid assignment request")
		return
	}
	if input.Enabled == nil && input.TrafficLimitBytes == nil {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "enabled or traffic_limit_bytes is required")
		return
	}
	if input.TrafficLimitBytes != nil && !validTrafficLimit(*input.TrafficLimitBytes) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "traffic_limit_bytes must be between 0 and the JavaScript safe integer limit")
		return
	}
	now := time.Now().UTC()
	userID := chi.URLParam(request, "userID")
	nodeID := chi.URLParam(request, "nodeID")
	current, err := a.store.GetUser(request.Context(), userID)
	if a.writeUserStoreError(response, request, err) {
		return
	}
	var currentAssignment *store.UserAssignment
	for index := range current.Assignments {
		if current.Assignments[index].NodeID == nodeID {
			currentAssignment = &current.Assignments[index]
			break
		}
	}
	if currentAssignment == nil {
		a.writeUserStoreError(response, request, store.ErrNotFound)
		return
	}
	enabled := currentAssignment.Enabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	trafficLimit := currentAssignment.TrafficLimitBytes
	if input.TrafficLimitBytes != nil {
		trafficLimit = *input.TrafficLimitBytes
	}
	user, err := a.store.UpdateAssignment(request.Context(), userID, nodeID, store.AssignmentUpdate{
		Enabled: enabled, TrafficLimitBytes: trafficLimit, Now: now,
	})
	if a.writeUserStoreError(response, request, err) {
		return
	}
	writeJSON(response, http.StatusOK, presentUser(user, now))
}

func (a *App) handleKickUser(response http.ResponseWriter, request *http.Request) {
	var input kickUserRequest
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid kick request")
		return
	}
	input.NodeID = strings.TrimSpace(input.NodeID)
	count, err := a.store.RequestUserKick(
		request.Context(), chi.URLParam(request, "userID"), input.NodeID, time.Now().UTC(),
	)
	if a.writeUserStoreError(response, request, err) {
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]any{"requested_nodes": count})
}

func (a *App) handleUnassignUser(response http.ResponseWriter, request *http.Request) {
	err := a.store.UnassignUser(
		request.Context(), chi.URLParam(request, "userID"), chi.URLParam(request, "nodeID"),
		time.Now().UTC(),
	)
	if a.writeUserStoreError(response, request, err) {
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (a *App) handleRevealAssignmentCredential(response http.ResponseWriter, request *http.Request) {
	credential, err := a.store.RevealAssignmentCredential(
		request.Context(), chi.URLParam(request, "userID"), chi.URLParam(request, "nodeID"),
		a.masterKey,
	)
	if a.writeUserStoreError(response, request, err) {
		return
	}
	setCredentialResponseHeaders(response)
	writeJSON(response, http.StatusOK, presentCredential(credential))
}

func setCredentialResponseHeaders(response http.ResponseWriter) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Pragma", "no-cache")
}

func (a *App) writeUserStoreError(
	response http.ResponseWriter,
	request *http.Request,
	err error,
) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		a.writeError(response, request, http.StatusNotFound, "user_resource_not_found", "user or assignment not found")
	case errors.Is(err, store.ErrUnsupported):
		a.writeError(response, request, http.StatusUnprocessableEntity, "adapter_users_unsupported", "node adapter does not support managed users")
	case errors.Is(err, store.ErrReadOnly):
		a.writeError(response, request, http.StatusConflict, "assignment_read_only", "read-only assignments cannot change managed settings or credentials")
	case errors.Is(err, store.ErrPending):
		a.writeError(response, request, http.StatusConflict, "credential_rotation_pending", "wait for all pending node changes to apply before rotating credentials")
	case errors.Is(err, store.ErrConflict):
		a.writeError(response, request, http.StatusConflict, "user_conflict", "username or assignment already exists")
	default:
		a.logger.Error("user operation failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "user_operation_failed", "user operation failed")
	}
	return true
}

func normalizeUserRequest(input userRequest) userRequest {
	input.Username = strings.TrimSpace(input.Username)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Notes = strings.TrimSpace(input.Notes)
	for index := range input.NodeIDs {
		input.NodeIDs[index] = strings.TrimSpace(input.NodeIDs[index])
	}
	return input
}

func validateUserRequest(input userRequest, requireEnabled bool) string {
	if !usernamePattern.MatchString(input.Username) {
		return "username must be 3-32 letters, numbers, dots, underscores, or hyphens"
	}
	if len(input.DisplayName) > 64 || len(input.Notes) > 500 {
		return "display_name must be at most 64 characters and notes at most 500 characters"
	}
	if requireEnabled && input.Enabled == nil {
		return "enabled is required"
	}
	if input.TrafficLimitBytes != nil && !validTrafficLimit(*input.TrafficLimitBytes) {
		return "traffic_limit_bytes must be between 0 and the JavaScript safe integer limit"
	}
	if len(input.NodeIDs) > 64 {
		return "too many node assignments"
	}
	seen := make(map[string]struct{}, len(input.NodeIDs))
	for _, nodeID := range input.NodeIDs {
		if nodeID == "" {
			return "node_ids cannot contain an empty value"
		}
		if _, exists := seen[nodeID]; exists {
			return "node_ids must be unique"
		}
		seen[nodeID] = struct{}{}
	}
	return ""
}

func validTrafficLimit(value int64) bool {
	return value >= 0 && value <= maxJSONSafeInteger
}

func presentUser(user store.User, now time.Time) userResponse {
	status := "active"
	if !user.Enabled {
		status = "disabled"
	} else if user.ExpiresAt != nil && !now.Before(*user.ExpiresAt) {
		status = "expired"
	}
	assignments := make([]assignmentResponse, 0, len(user.Assignments))
	onlineConnections := 0
	onlineNodes := 0
	for _, assignment := range user.Assignments {
		assignments = append(assignments, presentAssignment(assignment))
		onlineConnections += assignment.OnlineConnections
		if assignment.OnlineConnections > 0 {
			onlineNodes++
		}
	}
	return userResponse{
		ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Notes: user.Notes,
		Enabled: user.Enabled, ExpiresAt: user.ExpiresAt, Status: status,
		TrafficLimitBytes: user.TrafficLimitBytes, TrafficUploadBytes: user.TrafficUploadBytes,
		TrafficDownloadBytes: user.TrafficDownloadBytes, TrafficUsedBytes: user.TrafficUsedBytes,
		QuotaState: user.QuotaState, LastTrafficAt: user.LastTrafficAt,
		OnlineConnections: onlineConnections, OnlineNodes: onlineNodes,
		Assignments: assignments, CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt,
	}
}

func presentAssignment(assignment store.UserAssignment) assignmentResponse {
	return assignmentResponse{
		ID: assignment.ID, NodeID: assignment.NodeID, NodeName: assignment.NodeName,
		NodeAdapter: assignment.NodeAdapter, Enabled: assignment.Enabled,
		TrafficLimitBytes:    assignment.TrafficLimitBytes,
		TrafficUploadBytes:   assignment.TrafficUploadBytes,
		TrafficDownloadBytes: assignment.TrafficDownloadBytes,
		TrafficUsedBytes:     assignment.TrafficUsedBytes, QuotaState: assignment.QuotaState,
		LastTrafficAt: assignment.LastTrafficAt, OnlineConnections: assignment.OnlineConnections,
		OnlineSampledAt: assignment.OnlineSampledAt, KickGeneration: assignment.KickGeneration,
		CredentialFingerprint: assignment.CredentialFingerprint,
		ManagementMode:        assignment.ManagementMode, RemoteClientID: assignment.RemoteClientID,
		SubscriptionEligible: assignment.SubscriptionEligible,
		SubscriptionReason:   assignment.SubscriptionReason,
		DesiredVersion:       assignment.DesiredVersion, AppliedVersion: assignment.AppliedVersion,
		State: assignment.State, LastErrorCode: assignment.LastErrorCode,
		LastErrorMessage: assignment.LastErrorMessage, LastAttemptAt: assignment.LastAttemptAt,
		AppliedAt: assignment.AppliedAt, CreatedAt: assignment.CreatedAt, UpdatedAt: assignment.UpdatedAt,
	}
}

func presentCredentials(credentials []store.CreatedCredential) []credentialResponse {
	result := make([]credentialResponse, 0, len(credentials))
	for _, credential := range credentials {
		result = append(result, presentCredential(credential))
	}
	return result
}

func presentCredential(credential store.CreatedCredential) credentialResponse {
	return credentialResponse{
		NodeID: credential.Assignment.NodeID, NodeName: credential.Assignment.NodeName,
		Credential:            credential.Secret,
		CredentialFingerprint: credential.Assignment.CredentialFingerprint,
	}
}

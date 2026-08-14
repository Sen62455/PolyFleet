package server

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/Sen62455/PolyFleet/internal/store"
)

type suiTargetsRequest struct {
	InboundIDs []int64 `json:"inbound_ids"`
}

type suiImportRequest struct {
	UserID string `json:"user_id"`
}

type suiAdoptRequest struct {
	ConfirmName string `json:"confirm_name"`
}

type suiInboundResponse struct {
	RemoteID   int64     `json:"remote_id"`
	Tag        string    `json:"tag"`
	Type       string    `json:"type"`
	Listen     string    `json:"listen"`
	ListenPort int       `json:"listen_port"`
	ObservedAt time.Time `json:"observed_at"`
}

type suiClientResponse struct {
	RemoteID       int64     `json:"remote_id"`
	Name           string    `json:"name"`
	Enabled        bool      `json:"enabled"`
	InboundIDs     []int64   `json:"inbound_ids"`
	UploadBytes    int64     `json:"upload_bytes"`
	DownloadBytes  int64     `json:"download_bytes"`
	ExpiresAt      int64     `json:"expires_at"`
	Online         bool      `json:"online"`
	ObservedAt     time.Time `json:"observed_at"`
	MappedUserID   string    `json:"mapped_user_id,omitempty"`
	MappedUsername string    `json:"mapped_username,omitempty"`
	ManagementMode string    `json:"management_mode,omitempty"`
}

type suiStateResponse struct {
	NodeID           string               `json:"node_id"`
	AdapterStatus    string               `json:"adapter_status"`
	AdapterVersion   string               `json:"adapter_version"`
	AdapterErrorCode string               `json:"adapter_error_code"`
	LastProbedAt     *time.Time           `json:"last_probed_at"`
	LastDiscoveredAt *time.Time           `json:"last_discovered_at"`
	TargetInboundIDs []int64              `json:"target_inbound_ids"`
	Inbounds         []suiInboundResponse `json:"inbounds"`
	Clients          []suiClientResponse  `json:"clients"`
}

func (a *App) handleGetSUIState(response http.ResponseWriter, request *http.Request) {
	state, err := a.store.GetSUIState(request.Context(), chi.URLParam(request, "nodeID"))
	if a.writeSUIStoreError(response, request, err) {
		return
	}
	writeJSON(response, http.StatusOK, presentSUIState(state))
}

func (a *App) handleSetSUITargets(response http.ResponseWriter, request *http.Request) {
	var input suiTargetsRequest
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid S-UI target request")
		return
	}
	if len(input.InboundIDs) > 64 {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "too many S-UI target inbounds")
		return
	}
	state, err := a.store.SetSUITargetInbounds(
		request.Context(), chi.URLParam(request, "nodeID"), input.InboundIDs, time.Now().UTC(),
	)
	if a.writeSUIStoreError(response, request, err) {
		return
	}
	writeJSON(response, http.StatusOK, presentSUIState(state))
}

func (a *App) handleImportSUIClient(response http.ResponseWriter, request *http.Request) {
	remoteID, ok := positiveInt64Param(chi.URLParam(request, "clientID"))
	if !ok {
		a.writeError(response, request, http.StatusBadRequest, "invalid_client_id", "client ID must be a positive integer")
		return
	}
	var input suiImportRequest
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid S-UI import request")
		return
	}
	input.UserID = strings.TrimSpace(input.UserID)
	if _, err := uuid.Parse(input.UserID); err != nil {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "user_id is required")
		return
	}
	user, err := a.store.ImportSUIClient(
		request.Context(), chi.URLParam(request, "nodeID"), remoteID,
		input.UserID, time.Now().UTC(), a.masterKey,
	)
	if a.writeSUIStoreError(response, request, err) {
		return
	}
	writeJSON(response, http.StatusCreated, presentUser(user, time.Now().UTC()))
}

func (a *App) handleAdoptSUIClient(response http.ResponseWriter, request *http.Request) {
	remoteID, ok := positiveInt64Param(chi.URLParam(request, "clientID"))
	if !ok {
		a.writeError(response, request, http.StatusBadRequest, "invalid_client_id", "client ID must be a positive integer")
		return
	}
	var input suiAdoptRequest
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid S-UI adoption request")
		return
	}
	input.ConfirmName = strings.TrimSpace(input.ConfirmName)
	if input.ConfirmName == "" || len(input.ConfirmName) > 128 {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "confirm_name must match the discovered client")
		return
	}
	user, err := a.store.AdoptSUIClient(
		request.Context(), chi.URLParam(request, "nodeID"), remoteID,
		input.ConfirmName, time.Now().UTC(),
	)
	if a.writeSUIStoreError(response, request, err) {
		return
	}
	writeJSON(response, http.StatusAccepted, presentUser(user, time.Now().UTC()))
}

func (a *App) handleAgentSUIReport(response http.ResponseWriter, request *http.Request) {
	identity := agentFromContext(request.Context())
	var input protocol.SUIReportRequest
	if err := decodeJSON(response, request, &input, 1024*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid S-UI report")
		return
	}
	now := time.Now().UTC()
	if !validSUIReport(input, now) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "S-UI report values are outside accepted bounds")
		return
	}
	if err := a.store.RecordSUIReport(request.Context(), identity, input, now); err != nil {
		if errors.Is(err, store.ErrConflict) {
			a.writeError(response, request, http.StatusConflict, "installation_conflict", "Agent installation does not match S-UI node")
			return
		}
		a.logger.Error("record S-UI report failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "sui_report_failed", "S-UI report could not be recorded")
		return
	}
	writeJSON(response, http.StatusOK, protocol.SUIReportResponse{Accepted: true, ServerTime: now})
}

func (a *App) handleCredentialMaterial(response http.ResponseWriter, request *http.Request) {
	setCredentialResponseHeaders(response)
	identity := agentFromContext(request.Context())
	if !identity.Enabled {
		a.writeError(response, request, http.StatusForbidden, "credential_material_denied", "credential material request was denied")
		return
	}
	var input protocol.CredentialMaterialRequest
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid credential material request")
		return
	}
	hash, err := base64.RawURLEncoding.DecodeString(input.SnapshotSHA256)
	if err != nil || len(hash) != 32 || input.CredentialRef == "" ||
		len(input.CredentialRef) > 64 || input.DesiredVersion < 1 {
		a.writeError(response, request, http.StatusForbidden, "credential_material_denied", "credential material request was denied")
		return
	}
	secret, err := a.store.GetCredentialMaterial(
		request.Context(), identity, input.CredentialRef, input.DesiredVersion,
		hash, a.masterKey,
	)
	if err != nil {
		if !errors.Is(err, store.ErrUnauthorized) {
			a.logger.Error("credential material lookup failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		}
		a.writeError(response, request, http.StatusForbidden, "credential_material_denied", "credential material request was denied")
		return
	}
	writeJSON(response, http.StatusOK, protocol.CredentialMaterialResponse{
		CredentialRef: input.CredentialRef, Secret: secret,
	})
}

func validSUIReport(input protocol.SUIReportRequest, now time.Time) bool {
	if _, err := uuid.Parse(input.InstallationID); err != nil {
		return false
	}
	if input.Adapter.Name != "s_ui" || !validAdapterStatus(input.Adapter.Status) ||
		len(input.Adapter.Version) > 64 || len(input.Adapter.ErrorCode) > 64 ||
		input.Adapter.LastProbedAt == nil || input.Adapter.LastProbedAt.IsZero() ||
		input.Adapter.LastProbedAt.After(now.Add(10*time.Minute)) ||
		input.Core.Name != "sing-box" || len(input.Core.Version) > 64 || input.SampledAt.IsZero() ||
		input.SampledAt.After(now.Add(10*time.Minute)) ||
		len(input.Inbounds) > 256 || len(input.Clients) > 10000 {
		return false
	}
	if input.Adapter.Status == "compatible" && input.Adapter.Version == "" {
		return false
	}
	inboundIDs := make(map[int64]struct{}, len(input.Inbounds))
	for _, inbound := range input.Inbounds {
		if inbound.RemoteID <= 0 || inbound.Type != "hysteria2" || inbound.Tag == "" ||
			len(inbound.Tag) > 128 || len(inbound.Listen) > 128 ||
			inbound.ListenPort < 0 || inbound.ListenPort > 65535 {
			return false
		}
		if _, exists := inboundIDs[inbound.RemoteID]; exists {
			return false
		}
		inboundIDs[inbound.RemoteID] = struct{}{}
	}
	clientIDs := make(map[int64]struct{}, len(input.Clients))
	for _, client := range input.Clients {
		if client.RemoteID <= 0 || client.Name == "" || len(client.Name) > 128 ||
			client.UploadBytes < 0 || client.DownloadBytes < 0 || client.ExpiresAt < 0 ||
			len(client.InboundIDs) > 256 || client.Group != "" || client.Description != "" ||
			client.CredentialFingerprint != "" {
			return false
		}
		if _, exists := clientIDs[client.RemoteID]; exists {
			return false
		}
		clientIDs[client.RemoteID] = struct{}{}
		seenInbound := make(map[int64]struct{}, len(client.InboundIDs))
		for _, inboundID := range client.InboundIDs {
			if inboundID <= 0 {
				return false
			}
			if _, exists := seenInbound[inboundID]; exists {
				return false
			}
			seenInbound[inboundID] = struct{}{}
		}
		if client.MappedUserID != "" {
			if _, err := uuid.Parse(client.MappedUserID); err != nil ||
				(client.ManagementMode != "read_only" && client.ManagementMode != "managed") {
				return false
			}
		} else if client.ManagementMode != "" {
			return false
		}
	}
	return true
}

func validAdapterStatus(status string) bool {
	switch status {
	case "unknown", "compatible", "incompatible", "unavailable", "not_configured":
		return true
	default:
		return false
	}
}

func presentSUIState(state store.SUIState) suiStateResponse {
	result := suiStateResponse{
		NodeID: state.NodeID, AdapterStatus: state.AdapterStatus,
		AdapterVersion: state.AdapterVersion, AdapterErrorCode: state.AdapterErrorCode,
		LastProbedAt: state.LastProbedAt, LastDiscoveredAt: state.LastDiscoveredAt,
		TargetInboundIDs: append([]int64(nil), state.TargetInboundIDs...),
		Inbounds:         make([]suiInboundResponse, 0, len(state.Inbounds)),
		Clients:          make([]suiClientResponse, 0, len(state.Clients)),
	}
	for _, inbound := range state.Inbounds {
		result.Inbounds = append(result.Inbounds, suiInboundResponse{
			RemoteID: inbound.RemoteID, Tag: inbound.Tag, Type: inbound.Type,
			Listen: inbound.Listen, ListenPort: inbound.ListenPort,
			ObservedAt: inbound.ObservedAt,
		})
	}
	for _, client := range state.Clients {
		result.Clients = append(result.Clients, suiClientResponse{
			RemoteID: client.RemoteID, Name: client.Name, Enabled: client.Enabled,
			InboundIDs: client.InboundIDs, UploadBytes: client.UploadBytes,
			DownloadBytes: client.DownloadBytes, ExpiresAt: client.ExpiresAt,
			Online: client.Online, ObservedAt: client.ObservedAt,
			MappedUserID: client.MappedUserID, MappedUsername: client.MappedUsername,
			ManagementMode: client.ManagementMode,
		})
	}
	return result
}

func (a *App) writeSUIStoreError(response http.ResponseWriter, request *http.Request, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		a.writeError(response, request, http.StatusNotFound, "sui_resource_not_found", "S-UI node, client, or user not found")
	case errors.Is(err, store.ErrUnsupported):
		a.writeError(response, request, http.StatusUnprocessableEntity, "sui_adapter_required", "node does not use the S-UI adapter")
	case errors.Is(err, store.ErrConflict):
		a.writeError(response, request, http.StatusConflict, "sui_mapping_conflict", "S-UI target or mapping conflicts with current discovery")
	default:
		a.logger.Error("S-UI operation failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "sui_operation_failed", "S-UI operation failed")
	}
	return true
}

func positiveInt64Param(value string) (int64, bool) {
	result, err := strconv.ParseInt(value, 10, 64)
	return result, err == nil && result > 0 && result <= 1<<53-1
}

package server

import (
	"encoding/base64"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/Sen62455/PolyFleet/internal/store"
)

func (a *App) handleAgentEnroll(response http.ResponseWriter, request *http.Request) {
	now := time.Now().UTC()
	if !a.enrollLimiter.Allow(remoteIP(request), now) {
		response.Header().Set("Retry-After", "300")
		a.writeError(response, request, http.StatusTooManyRequests, "rate_limited", "too many enrollment attempts")
		return
	}
	if request.Header.Get("X-HyFleet-Protocol") != strconv.Itoa(protocol.MajorVersion) {
		a.writeError(response, request, http.StatusUpgradeRequired, "protocol_incompatible", "unsupported Agent protocol")
		return
	}
	var input protocol.EnrollRequest
	if err := decodeJSON(response, request, &input, 32*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid enrollment request")
		return
	}
	if !validEnrollRequest(input, requestIDFromContext(request.Context())) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "invalid enrollment fields")
		return
	}
	result, err := a.store.EnrollAgent(request.Context(), input.EnrollmentToken, store.EnrollmentFacts{
		InstallationID: input.InstallationID,
		RequestID:      input.RequestID,
		AgentVersion:   input.AgentVersion,
		OSName:         input.OS,
		OSVersion:      input.OSVersion,
		Architecture:   input.Architecture,
		AdapterType:    input.Adapter.Type,
		CoreName:       input.Adapter.CoreName,
		Capabilities:   input.Capabilities,
	}, a.masterKey, now)
	if errors.Is(err, store.ErrExpired) {
		a.writeError(response, request, http.StatusGone, "enrollment_expired", "enrollment token expired")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		a.writeError(response, request, http.StatusConflict, "enrollment_conflict", "enrollment request conflicts with node state")
		return
	}
	if errors.Is(err, store.ErrUnauthorized) {
		a.writeError(response, request, http.StatusUnauthorized, "enrollment_rejected", "enrollment rejected")
		return
	}
	if errors.Is(err, store.ErrUnsupported) {
		a.writeError(response, request, http.StatusUpgradeRequired, "agent_capabilities_missing", "Agent does not support the node's required capabilities")
		return
	}
	if err != nil {
		a.logger.Error("agent enrollment failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "enrollment_failed", "enrollment failed")
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func validEnrollRequest(input protocol.EnrollRequest, requestID string) bool {
	if input.EnrollmentToken == "" || len(input.EnrollmentToken) > 256 ||
		input.RequestID != requestID || len(input.AgentVersion) > 64 ||
		len(input.OS) > 64 || len(input.OSVersion) > 64 || len(input.Architecture) > 32 ||
		len(input.Capabilities) > 32 {
		return false
	}
	if _, err := uuid.Parse(input.InstallationID); err != nil {
		return false
	}
	if _, err := uuid.Parse(input.RequestID); err != nil {
		return false
	}
	switch input.Adapter.Type {
	case "native_hysteria2", "standalone_sing_box", "s_ui", store.AdapterSingBoxVLESSReality:
	default:
		return false
	}
	for _, capability := range input.Capabilities {
		if capability == "" || len(capability) > 64 {
			return false
		}
	}
	return true
}

func (a *App) handleAgentHeartbeat(response http.ResponseWriter, request *http.Request) {
	identity := agentFromContext(request.Context())
	var input protocol.HeartbeatRequest
	if err := decodeJSON(response, request, &input, 64*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid heartbeat request")
		return
	}
	if !validHeartbeat(input) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "heartbeat values are outside accepted bounds")
		return
	}
	now := time.Now().UTC()
	desired, err := a.store.RecordHeartbeat(request.Context(), identity, input, now)
	if errors.Is(err, store.ErrConflict) {
		a.writeError(response, request, http.StatusConflict, "installation_conflict", "Agent installation does not match enrollment")
		return
	}
	if err != nil {
		a.logger.Error("record heartbeat failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "heartbeat_failed", "heartbeat could not be recorded")
		return
	}
	writeJSON(response, http.StatusOK, protocol.HeartbeatResponse{
		ServerTime:     now,
		DesiredVersion: desired,
	})
}

func validHeartbeat(input protocol.HeartbeatRequest) bool {
	if input.InstallationID == "" || input.Agent.Protocol != protocol.MajorVersion ||
		input.AppliedVersion < 0 || input.Host.UptimeSeconds < 0 || input.Agent.Version == "" ||
		len(input.Agent.Version) > 64 || len(input.Core.Name) > 64 || len(input.Core.Version) > 64 ||
		len(input.Host.Hostname) > 255 || len(input.Host.KernelVersion) > 128 {
		return false
	}
	if len(input.Capabilities) > 32 {
		return false
	}
	seenCapabilities := make(map[string]struct{}, len(input.Capabilities))
	for _, capability := range input.Capabilities {
		if capability == "" || len(capability) > 64 {
			return false
		}
		if _, duplicate := seenCapabilities[capability]; duplicate {
			return false
		}
		seenCapabilities[capability] = struct{}{}
	}
	if input.Adapter.Name != "" || input.Adapter.Status != "" {
		switch input.Adapter.Name {
		case "s_ui", "native_hysteria2", "standalone_sing_box", store.AdapterSingBoxVLESSReality:
		default:
			return false
		}
		if !validAdapterStatus(input.Adapter.Status) || len(input.Adapter.Version) > 64 ||
			len(input.Adapter.ErrorCode) > 64 ||
			(input.Adapter.LastProbedAt != nil && input.Adapter.LastProbedAt.IsZero()) {
			return false
		}
	}
	if _, err := uuid.Parse(input.InstallationID); err != nil {
		return false
	}
	values := []float64{input.Host.CPUPercent, input.Host.Load1, input.Host.Load5, input.Host.Load15}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100000 {
			return false
		}
	}
	if input.Host.CPUPercent > 100 || input.Host.CPUCores < 0 || input.Host.CPUCores > 4096 ||
		input.Host.MemoryUsedBytes < 0 ||
		input.Host.MemoryTotalBytes < input.Host.MemoryUsedBytes || input.Host.DiskUsedBytes < 0 ||
		input.Host.SwapUsedBytes < 0 || input.Host.SwapTotalBytes < input.Host.SwapUsedBytes ||
		input.Host.DiskTotalBytes < input.Host.DiskUsedBytes ||
		input.Host.DiskReadBytesPerSecond < 0 || input.Host.DiskWriteBytesPerSecond < 0 ||
		input.Host.NetworkRXBPS < 0 || input.Host.NetworkTXBPS < 0 ||
		input.Host.NetworkRXBytesTotal < 0 || input.Host.NetworkTXBytesTotal < 0 ||
		input.SampledAt.IsZero() ||
		input.Usage.OutboxBatches < 0 || input.Usage.OutboxBatches > 1000000 ||
		len(input.Usage.LastErrorCode) > 64 {
		return false
	}
	if input.Usage.LastSampledAt != nil && input.Usage.LastSampledAt.IsZero() {
		return false
	}
	return true
}

func (a *App) handleAgentDesired(response http.ResponseWriter, request *http.Request) {
	identity := agentFromContext(request.Context())
	after, err := strconv.ParseInt(request.URL.Query().Get("after"), 10, 64)
	if err != nil || after < 0 {
		a.writeError(response, request, http.StatusBadRequest, "invalid_version", "after must be a non-negative integer")
		return
	}
	node, err := a.store.GetNode(request.Context(), identity.NodeID)
	if err != nil {
		a.writeError(response, request, http.StatusNotFound, "node_not_found", "node not found")
		return
	}
	if node.DesiredVersion <= after {
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if node.AdapterType == store.AdapterSingBoxVLESSReality {
		compatible, capabilityErr := a.store.HasAgentCapabilities(
			request.Context(), identity.NodeID,
			"desired_state_v2", "credential_material_v1", "sing_box_vless_reality",
			"reality_user_control_v1",
		)
		if capabilityErr != nil {
			a.logger.Error("read Agent capabilities failed", "request_id", requestIDFromContext(request.Context()), "error", capabilityErr)
			a.writeError(response, request, http.StatusInternalServerError, "desired_read_failed", "desired state unavailable")
			return
		}
		if !compatible {
			a.writeError(response, request, http.StatusUpgradeRequired, "agent_capabilities_missing", "Agent does not support this desired state")
			return
		}
	}
	envelope, err := a.store.GetDesiredSnapshot(request.Context(), identity.NodeID, node.DesiredVersion)
	if err != nil {
		a.logger.Error("read desired snapshot failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "desired_read_failed", "desired state unavailable")
		return
	}
	response.Header().Set("ETag", `"`+envelope.SHA256+`"`)
	writeJSON(response, http.StatusOK, envelope)
}

func (a *App) handleAgentDesiredAck(response http.ResponseWriter, request *http.Request) {
	identity := agentFromContext(request.Context())
	version, err := strconv.ParseInt(chi.URLParam(request, "version"), 10, 64)
	if err != nil || version < 1 {
		a.writeError(response, request, http.StatusBadRequest, "invalid_version", "version must be a positive integer")
		return
	}
	var input protocol.DesiredAckRequest
	if err := decodeJSON(response, request, &input, 32*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid acknowledgement")
		return
	}
	if input.Status != "applied" && input.Status != "failed" {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "status must be applied or failed")
		return
	}
	hash, err := base64.RawURLEncoding.DecodeString(input.SnapshotHash)
	if err != nil || len(hash) != 32 || len(input.ErrorCode) > 64 || len(input.Message) > 512 {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "invalid acknowledgement fields")
		return
	}
	if input.Adapter != identity.AdapterType {
		a.writeError(response, request, http.StatusConflict, "adapter_conflict", "adapter does not match node")
		return
	}
	if input.Status == "failed" && input.Reality != nil {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "failed acknowledgements cannot include Reality material")
		return
	}
	if identity.AdapterType == store.AdapterSingBoxVLESSReality {
		if input.Status == "applied" && !validRealityAckMaterial(input.Reality) {
			a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "applied Reality acknowledgement requires valid public material")
			return
		}
	} else if input.Reality != nil {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "Reality material does not apply to this adapter")
		return
	}
	err = a.store.AcknowledgeDesiredWithMaterial(
		request.Context(), identity, version, hash, input.Status,
		strings.TrimSpace(input.ErrorCode), strings.TrimSpace(input.Message),
		input.Reality, time.Now().UTC(),
	)
	if errors.Is(err, store.ErrVersionConflict) {
		a.writeError(response, request, http.StatusConflict, "desired_version_conflict", "desired state changed; poll again")
		return
	}
	if err != nil {
		a.logger.Error("desired acknowledgement failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "acknowledgement_failed", "acknowledgement could not be recorded")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func validRealityAckMaterial(material *protocol.AppliedRealityMaterial) bool {
	if material == nil || material.KeyGeneration < 1 || len(material.ShortID) != 16 {
		return false
	}
	publicKey, err := base64.RawURLEncoding.DecodeString(material.PublicKey)
	if err != nil || len(publicKey) != 32 {
		return false
	}
	for _, character := range material.ShortID {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

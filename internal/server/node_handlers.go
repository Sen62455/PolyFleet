package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/store"
	"github.com/go-chi/chi/v5"
)

type nodeRequest struct {
	Name               string              `json:"name"`
	Provider           string              `json:"provider"`
	Region             string              `json:"region"`
	AdapterType        string              `json:"adapter_type"`
	PublicHost         *string             `json:"public_host"`
	PublicPort         *int                `json:"public_port"`
	SNI                *string             `json:"sni"`
	TLSInsecure        *bool               `json:"tls_insecure"`
	TLSCertFingerprint *string             `json:"tls_cert_fingerprint"`
	TLSPublicKeySHA256 *string             `json:"tls_public_key_sha256"`
	Reality            *nodeRealityRequest `json:"reality"`
	TrafficLimitBytes  *int64              `json:"traffic_limit_bytes"`
	TrafficResetDay    *int                `json:"traffic_reset_day"`
	Enabled            *bool               `json:"enabled"`
}

type nodeTrafficCalibrationRequest struct {
	ProviderUsedBytes int64 `json:"provider_used_bytes"`
}

type nodeRealityRequest struct {
	HandshakeServer string `json:"handshake_server"`
	HandshakePort   int    `json:"handshake_port"`
}

type nodeRealityResponse struct {
	HandshakeServer        string     `json:"handshake_server"`
	HandshakePort          int        `json:"handshake_port"`
	KeyGeneration          int64      `json:"key_generation"`
	AppliedKeyGeneration   int64      `json:"applied_key_generation"`
	PublicKey              string     `json:"public_key"`
	ShortID                string     `json:"short_id"`
	MaterialAppliedVersion int64      `json:"material_applied_version"`
	MaterialReportedAt     *time.Time `json:"material_reported_at"`
}

type rotateRealityIdentityRequest struct {
	ExpectedKeyGeneration  int64 `json:"expected_key_generation"`
	ExpectedDesiredVersion int64 `json:"expected_desired_version"`
}

type nodeResponse struct {
	ID                        string               `json:"id"`
	Name                      string               `json:"name"`
	Provider                  string               `json:"provider"`
	Region                    string               `json:"region"`
	AdapterType               string               `json:"adapter_type"`
	AdapterStatus             string               `json:"adapter_status"`
	AdapterVersion            string               `json:"adapter_version"`
	AdapterErrorCode          string               `json:"adapter_error_code"`
	AdapterLastProbedAt       *time.Time           `json:"adapter_last_probed_at"`
	AdapterLastDiscoveredAt   *time.Time           `json:"adapter_last_discovered_at"`
	SUITargetInboundIDs       []int64              `json:"s_ui_target_inbound_ids"`
	PublicHost                string               `json:"public_host"`
	PublicPort                int                  `json:"public_port"`
	SNI                       string               `json:"sni"`
	TLSInsecure               bool                 `json:"tls_insecure"`
	TLSCertFingerprint        string               `json:"tls_cert_fingerprint"`
	TLSPublicKeySHA256        string               `json:"tls_public_key_sha256"`
	Reality                   *nodeRealityResponse `json:"reality"`
	Enabled                   bool                 `json:"enabled"`
	Status                    string               `json:"status"`
	StatusReason              string               `json:"status_reason"`
	DesiredVersion            int64                `json:"desired_version"`
	AppliedVersion            int64                `json:"applied_version"`
	AgentInstallationID       string               `json:"agent_installation_id,omitempty"`
	AgentVersion              string               `json:"agent_version"`
	ProtocolVersion           int                  `json:"protocol_version"`
	OSName                    string               `json:"os_name"`
	OSVersion                 string               `json:"os_version"`
	Architecture              string               `json:"architecture"`
	Hostname                  string               `json:"hostname"`
	KernelVersion             string               `json:"kernel_version"`
	CoreName                  string               `json:"core_name"`
	CoreVersion               string               `json:"core_version"`
	CoreRunning               bool                 `json:"core_running"`
	UptimeSeconds             int64                `json:"uptime_seconds"`
	CPUCores                  int                  `json:"cpu_cores"`
	CPUPercent                float64              `json:"cpu_percent"`
	MemoryUsedBytes           int64                `json:"memory_used_bytes"`
	MemoryTotalBytes          int64                `json:"memory_total_bytes"`
	SwapUsedBytes             int64                `json:"swap_used_bytes"`
	SwapTotalBytes            int64                `json:"swap_total_bytes"`
	DiskUsedBytes             int64                `json:"disk_used_bytes"`
	DiskTotalBytes            int64                `json:"disk_total_bytes"`
	DiskReadBytesPerSecond    int64                `json:"disk_read_bytes_per_second"`
	DiskWriteBytesPerSecond   int64                `json:"disk_write_bytes_per_second"`
	NetworkRXBPS              int64                `json:"network_rx_bps"`
	NetworkTXBPS              int64                `json:"network_tx_bps"`
	NetworkRXBytesTotal       int64                `json:"network_rx_bytes_total"`
	NetworkTXBytesTotal       int64                `json:"network_tx_bytes_total"`
	Load1                     float64              `json:"load_1"`
	Load5                     float64              `json:"load_5"`
	Load15                    float64              `json:"load_15"`
	UsageEnabled              bool                 `json:"usage_enabled"`
	UsageAvailable            bool                 `json:"usage_available"`
	UsageOutboxBatches        int                  `json:"usage_outbox_batches"`
	UsageErrorCode            string               `json:"usage_error_code"`
	UsageSampledAt            *time.Time           `json:"usage_sampled_at"`
	TrafficUploadBytes        int64                `json:"traffic_upload_bytes"`
	TrafficDownloadBytes      int64                `json:"traffic_download_bytes"`
	TrafficUnattributedBytes  int64                `json:"traffic_unattributed_bytes"`
	TrafficLastReportAt       *time.Time           `json:"traffic_last_report_at"`
	TrafficLimitBytes         int64                `json:"traffic_limit_bytes"`
	TrafficResetDay           int                  `json:"traffic_reset_day"`
	TrafficCycleStartedAt     *time.Time           `json:"traffic_cycle_started_at"`
	TrafficCycleUploadBytes   int64                `json:"traffic_cycle_upload_bytes"`
	TrafficCycleDownloadBytes int64                `json:"traffic_cycle_download_bytes"`
	TrafficUsedBytes          int64                `json:"traffic_used_bytes"`
	TrafficCalibrationBytes   *int64               `json:"traffic_calibration_bytes"`
	TrafficCalibratedAt       *time.Time           `json:"traffic_calibrated_at"`
	OnlineUsers               int                  `json:"online_users"`
	OnlineConnections         int                  `json:"online_connections"`
	OnlineUnknownUsers        int                  `json:"online_unknown_users"`
	OnlineSampledAt           *time.Time           `json:"online_sampled_at"`
	OnlineLastReportAt        *time.Time           `json:"online_last_report_at"`
	LastSeenAt                *time.Time           `json:"last_seen_at"`
	LastAppliedAt             *time.Time           `json:"last_applied_at"`
	CreatedAt                 time.Time            `json:"created_at"`
	UpdatedAt                 time.Time            `json:"updated_at"`
}

type nodeMetricResponse struct {
	BucketAt                time.Time `json:"bucket_at"`
	CPUPercent              float64   `json:"cpu_percent"`
	MemoryUsedBytes         int64     `json:"memory_used_bytes"`
	MemoryTotalBytes        int64     `json:"memory_total_bytes"`
	SwapUsedBytes           int64     `json:"swap_used_bytes"`
	SwapTotalBytes          int64     `json:"swap_total_bytes"`
	DiskUsedBytes           int64     `json:"disk_used_bytes"`
	DiskTotalBytes          int64     `json:"disk_total_bytes"`
	DiskReadBytesPerSecond  int64     `json:"disk_read_bytes_per_second"`
	DiskWriteBytesPerSecond int64     `json:"disk_write_bytes_per_second"`
	NetworkRXBPS            int64     `json:"network_rx_bps"`
	NetworkTXBPS            int64     `json:"network_tx_bps"`
	Load1                   float64   `json:"load_1"`
	Load5                   float64   `json:"load_5"`
	Load15                  float64   `json:"load_15"`
	SampledAt               time.Time `json:"sampled_at"`
}

func (a *App) handleListNodes(response http.ResponseWriter, request *http.Request) {
	nodes, err := a.store.ListNodes(request.Context())
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "nodes_read_failed", "could not read nodes")
		return
	}
	result := make([]nodeResponse, 0, len(nodes))
	now := time.Now().UTC()
	for _, node := range nodes {
		result = append(result, a.presentNode(node, now))
	}
	writeJSON(response, http.StatusOK, map[string]any{"nodes": result})
}

func (a *App) handleGetNode(response http.ResponseWriter, request *http.Request) {
	node, err := a.store.GetNode(request.Context(), chi.URLParam(request, "nodeID"))
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "node_not_found", "node not found")
		return
	}
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "node_read_failed", "could not read node")
		return
	}
	writeJSON(response, http.StatusOK, a.presentNode(node, time.Now().UTC()))
}

func (a *App) handleListNodeMetrics(response http.ResponseWriter, request *http.Request) {
	rangeName := strings.TrimSpace(request.URL.Query().Get("range"))
	if rangeName == "" {
		rangeName = "24h"
	}
	ranges := map[string]time.Duration{
		"1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour,
		"7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour,
	}
	duration, ok := ranges[rangeName]
	if !ok {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "range must be 1h, 6h, 24h, 7d, or 30d")
		return
	}
	until := time.Now().UTC()
	samples, step, err := a.store.ListNodeMetricSamples(
		request.Context(), chi.URLParam(request, "nodeID"), until.Add(-duration), until, 360,
	)
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "node_not_found", "node not found")
		return
	}
	if err != nil {
		a.logger.Error("list node metrics failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "node_metrics_read_failed", "could not read node metrics")
		return
	}
	result := make([]nodeMetricResponse, 0, len(samples))
	for _, sample := range samples {
		result = append(result, nodeMetricResponse{
			BucketAt: sample.BucketAt, CPUPercent: sample.CPUPercent,
			MemoryUsedBytes: sample.MemoryUsedBytes, MemoryTotalBytes: sample.MemoryTotalBytes,
			SwapUsedBytes: sample.SwapUsedBytes, SwapTotalBytes: sample.SwapTotalBytes,
			DiskUsedBytes: sample.DiskUsedBytes, DiskTotalBytes: sample.DiskTotalBytes,
			DiskReadBytesPerSecond:  sample.DiskReadBytesPerSecond,
			DiskWriteBytesPerSecond: sample.DiskWriteBytesPerSecond,
			NetworkRXBPS:            sample.NetworkRXBPS, NetworkTXBPS: sample.NetworkTXBPS,
			Load1: sample.Load1, Load5: sample.Load5, Load15: sample.Load15,
			SampledAt: sample.SampledAt,
		})
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"range": rangeName, "step_seconds": int64(step / time.Second), "samples": result,
	})
}

func (a *App) handleCreateNode(response http.ResponseWriter, request *http.Request) {
	var input nodeRequest
	if err := decodeJSON(response, request, &input, 32*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid node request")
		return
	}
	input = normalizeNodeRequest(input)
	if validationMessage := validateNodeRequest(input); validationMessage != "" {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", validationMessage)
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	publicHost, publicPort, sni, tlsInsecure, tlsCertFingerprint, tlsPublicKeySHA256 := nodeEndpointValues(input, nil)
	if validationMessage := validateEffectiveRealityNode(
		input.AdapterType, publicHost, sni, tlsInsecure, tlsCertFingerprint,
		tlsPublicKeySHA256, input.Reality, false,
	); validationMessage != "" {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", validationMessage)
		return
	}
	now := time.Now().UTC()
	trafficLimit, trafficResetDay := nodeTrafficBudgetValues(input, nil)
	node, err := a.store.CreateNode(request.Context(), store.NewNode{
		ID:                 cryptoutil.NewID(),
		Name:               input.Name,
		Provider:           input.Provider,
		Region:             input.Region,
		AdapterType:        input.AdapterType,
		PublicHost:         publicHost,
		PublicPort:         publicPort,
		SNI:                sni,
		TLSInsecure:        tlsInsecure,
		TLSCertFingerprint: tlsCertFingerprint,
		TLSPublicKeySHA256: tlsPublicKeySHA256,
		VLESSReality:       requestedRealitySettings(input.Reality, nil),
		TrafficLimitBytes:  trafficLimit,
		TrafficResetDay:    trafficResetDay,
		Enabled:            enabled,
		Now:                now,
	})
	if err != nil {
		a.writeError(response, request, http.StatusConflict, "node_conflict", "a node with that name already exists")
		return
	}
	writeJSON(response, http.StatusCreated, a.presentNode(node, now))
}

func (a *App) handleUpdateNode(response http.ResponseWriter, request *http.Request) {
	var input nodeRequest
	if err := decodeJSON(response, request, &input, 32*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid node request")
		return
	}
	input = normalizeNodeRequest(input)
	if validationMessage := validateNodeRequest(input); validationMessage != "" || input.Enabled == nil {
		if validationMessage == "" {
			validationMessage = "enabled is required"
		}
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", validationMessage)
		return
	}
	current, err := a.store.GetNode(request.Context(), chi.URLParam(request, "nodeID"))
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "node_not_found", "node not found")
		return
	}
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "node_read_failed", "could not read node")
		return
	}
	publicHost, publicPort, sni, tlsInsecure, tlsCertFingerprint, tlsPublicKeySHA256 := nodeEndpointValues(input, &current)
	if validationMessage := validateEffectiveRealityNode(
		input.AdapterType, publicHost, sni, tlsInsecure, tlsCertFingerprint,
		tlsPublicKeySHA256, input.Reality, current.AdapterType == store.AdapterSingBoxVLESSReality,
	); validationMessage != "" {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", validationMessage)
		return
	}
	now := time.Now().UTC()
	trafficLimit, trafficResetDay := nodeTrafficBudgetValues(input, &current)
	node, err := a.store.UpdateNode(request.Context(), current.ID, store.UpdateNode{
		Name:               input.Name,
		Provider:           input.Provider,
		Region:             input.Region,
		AdapterType:        input.AdapterType,
		PublicHost:         publicHost,
		PublicPort:         publicPort,
		SNI:                sni,
		TLSInsecure:        tlsInsecure,
		TLSCertFingerprint: tlsCertFingerprint,
		TLSPublicKeySHA256: tlsPublicKeySHA256,
		VLESSReality:       requestedRealitySettings(input.Reality, current.VLESSReality),
		TrafficLimitBytes:  trafficLimit,
		TrafficResetDay:    trafficResetDay,
		Enabled:            *input.Enabled,
		Now:                now,
	})
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "node_not_found", "node not found")
		return
	}
	if err != nil {
		a.writeError(response, request, http.StatusConflict, "node_conflict", "a node with that name already exists")
		return
	}
	writeJSON(response, http.StatusOK, a.presentNode(node, now))
}

func (a *App) handleArchiveNode(response http.ResponseWriter, request *http.Request) {
	err := a.store.ArchiveNode(request.Context(), chi.URLParam(request, "nodeID"), time.Now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "node_not_found", "node not found")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		a.writeError(response, request, http.StatusConflict, "node_has_assignments", "remove active user assignments or wait for the agent to apply pending removals before archiving the node")
		return
	}
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "node_archive_failed", "could not archive node")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (a *App) handleCalibrateNodeTraffic(response http.ResponseWriter, request *http.Request) {
	var input nodeTrafficCalibrationRequest
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid traffic calibration request")
		return
	}
	if !validTrafficLimit(input.ProviderUsedBytes) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "provider_used_bytes must be between 0 and the JavaScript safe integer limit")
		return
	}
	node, err := a.store.CalibrateNodeTraffic(
		request.Context(), chi.URLParam(request, "nodeID"), input.ProviderUsedBytes,
		time.Now().UTC(),
	)
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "node_not_found", "node not found")
		return
	}
	if err != nil {
		a.logger.Error("calibrate node traffic failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "traffic_calibration_failed", "could not calibrate node traffic")
		return
	}
	writeJSON(response, http.StatusOK, a.presentNode(node, time.Now().UTC()))
}

func (a *App) handleEnrollmentToken(response http.ResponseWriter, request *http.Request) {
	session := sessionFromContext(request.Context())
	token, err := a.store.CreateEnrollmentToken(
		request.Context(), chi.URLParam(request, "nodeID"), session.AdminID,
		time.Now().UTC(), 10*time.Minute,
	)
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "node_not_found", "node not found")
		return
	}
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "enrollment_token_failed", "could not create enrollment token")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"node_id":          token.NodeID,
		"enrollment_token": token.Token,
		"expires_at":       token.ExpiresAt,
	})
}

func (a *App) handleRotateRealityIdentity(response http.ResponseWriter, request *http.Request) {
	var input rotateRealityIdentityRequest
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid Reality identity rotation request")
		return
	}
	if input.ExpectedKeyGeneration < 1 || input.ExpectedDesiredVersion < 1 {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "expected_key_generation and expected_desired_version must be positive")
		return
	}
	now := time.Now().UTC()
	node, err := a.store.RotateVLESSRealityIdentity(
		request.Context(), chi.URLParam(request, "nodeID"),
		input.ExpectedKeyGeneration, input.ExpectedDesiredVersion, now,
	)
	switch {
	case errors.Is(err, store.ErrNotFound):
		a.writeError(response, request, http.StatusNotFound, "node_not_found", "node not found")
		return
	case errors.Is(err, store.ErrUnsupported):
		a.writeError(response, request, http.StatusUnprocessableEntity, "reality_identity_rotation_unsupported", "node does not use the VLESS Reality adapter")
		return
	case errors.Is(err, store.ErrPending):
		a.writeError(response, request, http.StatusConflict, "reality_identity_rotation_pending", "enroll the node and wait for the current Reality identity to apply before rotating")
		return
	case errors.Is(err, store.ErrConflict):
		a.writeError(response, request, http.StatusConflict, "reality_identity_rotation_conflict", "Reality identity generation or desired version changed")
		return
	case err != nil:
		a.logger.Error("rotate Reality identity failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "reality_identity_rotation_failed", "could not rotate Reality identity")
		return
	}
	writeJSON(response, http.StatusAccepted, a.presentNode(node, now))
}

func normalizeNodeRequest(input nodeRequest) nodeRequest {
	input.Name = strings.TrimSpace(input.Name)
	input.Provider = strings.TrimSpace(input.Provider)
	input.Region = strings.TrimSpace(input.Region)
	input.AdapterType = strings.TrimSpace(input.AdapterType)
	if input.PublicHost != nil {
		value := normalizeEndpointHost(*input.PublicHost)
		input.PublicHost = &value
	}
	if input.SNI != nil {
		value := normalizeEndpointHost(*input.SNI)
		input.SNI = &value
	}
	if input.Reality != nil {
		input.Reality.HandshakeServer = normalizeEndpointHost(input.Reality.HandshakeServer)
	}
	if input.TLSCertFingerprint != nil {
		value := strings.TrimSpace(*input.TLSCertFingerprint)
		if normalized, ok := canonicalTLSCertFingerprint(value); ok {
			value = normalized
		}
		input.TLSCertFingerprint = &value
	}
	if input.TLSPublicKeySHA256 != nil {
		value := strings.TrimSpace(*input.TLSPublicKeySHA256)
		if normalized, ok := canonicalTLSPublicKeySHA256(value); ok {
			value = normalized
		}
		input.TLSPublicKeySHA256 = &value
	}
	return input
}

func validateNodeRequest(input nodeRequest) string {
	if len(input.Name) < 2 || len(input.Name) > 64 {
		return "node name must be between 2 and 64 characters"
	}
	if len(input.Provider) > 64 || len(input.Region) > 64 {
		return "provider and region must be at most 64 characters"
	}
	if input.PublicHost != nil && *input.PublicHost != "" && !validEndpointHost(*input.PublicHost) {
		return "public_host must be a domain name or IP address without a scheme or port"
	}
	if input.PublicPort != nil && (*input.PublicPort < 1 || *input.PublicPort > 65535) {
		return "public_port must be between 1 and 65535"
	}
	if input.SNI != nil && *input.SNI != "" && !validEndpointHost(*input.SNI) {
		return "sni must be a domain name or IP address"
	}
	if input.TLSCertFingerprint != nil && *input.TLSCertFingerprint != "" {
		if _, ok := canonicalTLSCertFingerprint(*input.TLSCertFingerprint); !ok {
			return "tls_cert_fingerprint must be a SHA-256 certificate fingerprint"
		}
	}
	if input.TLSPublicKeySHA256 != nil && *input.TLSPublicKeySHA256 != "" {
		if _, ok := canonicalTLSPublicKeySHA256(*input.TLSPublicKeySHA256); !ok {
			return "tls_public_key_sha256 must be a base64-encoded SHA-256 public key hash"
		}
	}
	if input.TrafficLimitBytes != nil && !validTrafficLimit(*input.TrafficLimitBytes) {
		return "traffic_limit_bytes must be between 0 and the JavaScript safe integer limit"
	}
	if input.TrafficResetDay != nil && (*input.TrafficResetDay < 1 || *input.TrafficResetDay > 31) {
		return "traffic_reset_day must be between 1 and 31"
	}
	switch input.AdapterType {
	case "native_hysteria2", "standalone_sing_box", "s_ui", store.AdapterSingBoxVLESSReality:
		return ""
	default:
		return "unsupported adapter type"
	}
}

func (a *App) presentNode(node store.Node, now time.Time) nodeResponse {
	status := node.Status
	if !node.Enabled {
		status = "disabled"
	} else if node.LastSeenAt != nil {
		age := now.Sub(*node.LastSeenAt)
		if age >= a.config.OfflineAfter {
			status = "offline"
		} else if age >= a.config.StaleAfter {
			status = "stale"
		}
	}
	var reality *nodeRealityResponse
	if node.VLESSReality != nil {
		reality = &nodeRealityResponse{
			HandshakeServer:      node.VLESSReality.HandshakeServer,
			HandshakePort:        node.VLESSReality.HandshakeServerPort,
			KeyGeneration:        node.VLESSReality.DesiredKeyGeneration,
			AppliedKeyGeneration: node.VLESSReality.AppliedKeyGeneration,
			PublicKey:            node.VLESSReality.PublicKey, ShortID: node.VLESSReality.ShortID,
			MaterialAppliedVersion: node.VLESSReality.MaterialAppliedVersion,
			MaterialReportedAt:     node.VLESSReality.MaterialReportedAt,
		}
	}
	return nodeResponse{
		ID: node.ID, Name: node.Name, Provider: node.Provider, Region: node.Region,
		AdapterType: node.AdapterType, AdapterStatus: node.AdapterStatus,
		AdapterVersion: node.AdapterVersion, AdapterErrorCode: node.AdapterErrorCode,
		AdapterLastProbedAt:     node.AdapterLastProbedAt,
		AdapterLastDiscoveredAt: node.AdapterLastDiscoveredAt,
		SUITargetInboundIDs:     node.SUITargetInboundIDs,
		PublicHost:              node.PublicHost, PublicPort: node.PublicPort,
		SNI: node.SNI, TLSInsecure: node.TLSInsecure,
		TLSCertFingerprint: node.TLSCertFingerprint,
		TLSPublicKeySHA256: node.TLSPublicKeySHA256,
		Reality:            reality,
		Enabled:            node.Enabled, Status: status,
		StatusReason: node.StatusReason, DesiredVersion: node.DesiredVersion,
		AppliedVersion: node.AppliedVersion, AgentInstallationID: node.AgentInstallationID,
		AgentVersion: node.AgentVersion, ProtocolVersion: node.ProtocolVersion,
		OSName: node.OSName, OSVersion: node.OSVersion, Architecture: node.Architecture,
		Hostname: node.Hostname, KernelVersion: node.KernelVersion,
		CoreName: node.CoreName, CoreVersion: node.CoreVersion, CoreRunning: node.CoreRunning,
		UptimeSeconds: node.UptimeSeconds, CPUCores: node.CPUCores, CPUPercent: node.CPUPercent,
		MemoryUsedBytes: node.MemoryUsedBytes, MemoryTotalBytes: node.MemoryTotalBytes,
		SwapUsedBytes: node.SwapUsedBytes, SwapTotalBytes: node.SwapTotalBytes,
		DiskUsedBytes: node.DiskUsedBytes, DiskTotalBytes: node.DiskTotalBytes,
		DiskReadBytesPerSecond:  node.DiskReadBytesPerSecond,
		DiskWriteBytesPerSecond: node.DiskWriteBytesPerSecond,
		NetworkRXBPS:            node.NetworkRXBPS, NetworkTXBPS: node.NetworkTXBPS,
		NetworkRXBytesTotal: node.NetworkRXBytesTotal, NetworkTXBytesTotal: node.NetworkTXBytesTotal,
		Load1: node.Load1, Load5: node.Load5, Load15: node.Load15,
		UsageEnabled: node.UsageEnabled, UsageAvailable: node.UsageAvailable,
		UsageOutboxBatches: node.UsageOutboxBatches, UsageErrorCode: node.UsageErrorCode,
		UsageSampledAt: node.UsageSampledAt, TrafficUploadBytes: node.TrafficUploadBytes,
		TrafficDownloadBytes:      node.TrafficDownloadBytes,
		TrafficUnattributedBytes:  node.TrafficUnattributedBytes,
		TrafficLastReportAt:       node.TrafficLastReportAt,
		TrafficLimitBytes:         node.TrafficLimitBytes,
		TrafficResetDay:           node.TrafficResetDay,
		TrafficCycleStartedAt:     node.TrafficCycleStartedAt,
		TrafficCycleUploadBytes:   node.TrafficCycleUploadBytes,
		TrafficCycleDownloadBytes: node.TrafficCycleDownloadBytes,
		TrafficUsedBytes:          store.EffectiveNodeTrafficUsed(node),
		TrafficCalibrationBytes:   node.TrafficCalibrationBytes,
		TrafficCalibratedAt:       node.TrafficCalibratedAt,
		OnlineUsers:               node.OnlineUsers,
		OnlineConnections:         node.OnlineConnections, OnlineUnknownUsers: node.OnlineUnknownUsers,
		OnlineSampledAt: node.OnlineSampledAt, OnlineLastReportAt: node.OnlineLastReportAt,
		LastSeenAt: node.LastSeenAt, LastAppliedAt: node.LastAppliedAt,
		CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt,
	}
}

func nodeTrafficBudgetValues(input nodeRequest, current *store.Node) (int64, int) {
	limit := int64(0)
	resetDay := 1
	if current != nil {
		limit = current.TrafficLimitBytes
		resetDay = current.TrafficResetDay
	}
	if input.TrafficLimitBytes != nil {
		limit = *input.TrafficLimitBytes
	}
	if input.TrafficResetDay != nil {
		resetDay = *input.TrafficResetDay
	}
	return limit, resetDay
}

func validateEffectiveRealityNode(
	adapter, publicHost, sni string,
	tlsInsecure bool,
	tlsCertFingerprint, tlsPublicKeySHA256 string,
	reality *nodeRealityRequest,
	allowOmittedReality bool,
) string {
	if adapter != store.AdapterSingBoxVLESSReality {
		if reality != nil {
			return "reality settings require the VLESS Reality adapter"
		}
		return ""
	}
	if publicHost == "" {
		return "public_host is required for the VLESS Reality adapter"
	}
	if sni == "" || !validDNSName(sni) {
		return "sni must be a DNS name for the VLESS Reality adapter"
	}
	if tlsInsecure || tlsCertFingerprint != "" || tlsPublicKeySHA256 != "" {
		return "certificate verification overrides and TLS pins do not apply to Reality"
	}
	if reality == nil {
		if allowOmittedReality {
			return ""
		}
		return "reality settings are required for the VLESS Reality adapter"
	}
	if !validDNSName(reality.HandshakeServer) {
		return "reality.handshake_server must be a DNS name"
	}
	if reality.HandshakePort != 443 {
		return "reality.handshake_port must be 443"
	}
	return ""
}

func requestedRealitySettings(
	request *nodeRealityRequest,
	current *store.VLESSRealitySettings,
) *store.VLESSRealitySettings {
	if request == nil {
		return nil
	}
	generation := int64(1)
	if current != nil {
		generation = current.DesiredKeyGeneration
	}
	return &store.VLESSRealitySettings{
		HandshakeServer:      request.HandshakeServer,
		HandshakeServerPort:  request.HandshakePort,
		DesiredKeyGeneration: generation,
	}
}

func nodeEndpointValues(input nodeRequest, current *store.Node) (string, int, string, bool, string, string) {
	publicHost := ""
	publicPort := 443
	sni := ""
	tlsInsecure := false
	tlsCertFingerprint := ""
	tlsPublicKeySHA256 := ""
	if current != nil {
		publicHost = current.PublicHost
		publicPort = current.PublicPort
		sni = current.SNI
		tlsInsecure = current.TLSInsecure
		tlsCertFingerprint = current.TLSCertFingerprint
		tlsPublicKeySHA256 = current.TLSPublicKeySHA256
	}
	if input.PublicHost != nil {
		publicHost = *input.PublicHost
	}
	if input.PublicPort != nil {
		publicPort = *input.PublicPort
	}
	if input.SNI != nil {
		sni = *input.SNI
	}
	if input.TLSInsecure != nil {
		tlsInsecure = *input.TLSInsecure
	}
	if input.TLSCertFingerprint != nil {
		tlsCertFingerprint = *input.TLSCertFingerprint
	}
	if input.TLSPublicKeySHA256 != nil {
		tlsPublicKeySHA256 = *input.TLSPublicKeySHA256
	}
	return publicHost, publicPort, sni, tlsInsecure, tlsCertFingerprint, tlsPublicKeySHA256
}

func canonicalTLSCertFingerprint(value string) (string, bool) {
	compact := strings.ReplaceAll(strings.TrimSpace(value), ":", "")
	if len(compact) != sha256.Size*2 {
		return "", false
	}
	decoded, err := hex.DecodeString(compact)
	if err != nil || len(decoded) != sha256.Size {
		return "", false
	}
	hexValue := strings.ToUpper(hex.EncodeToString(decoded))
	var result strings.Builder
	result.Grow(len(hexValue) + sha256.Size - 1)
	for index := 0; index < len(hexValue); index += 2 {
		if index > 0 {
			result.WriteByte(':')
		}
		result.WriteString(hexValue[index : index+2])
	}
	return result.String(), true
}

func canonicalTLSPublicKeySHA256(value string) (string, bool) {
	decoded, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != sha256.Size {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(decoded), true
}

func normalizeEndpointHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		candidate := strings.TrimSuffix(strings.TrimPrefix(value, "["), "]")
		if net.ParseIP(candidate) != nil {
			return candidate
		}
	}
	return value
}

func validEndpointHost(value string) bool {
	if len(value) > 253 || strings.ContainsAny(value, "/?#@[] ") {
		return false
	}
	if net.ParseIP(value) != nil {
		return true
	}
	value = strings.TrimSuffix(value, ".")
	if value == "" || strings.Contains(value, ":") {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
				(character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}

func validDNSName(value string) bool {
	return value != "" && value == strings.ToLower(value) &&
		!strings.HasSuffix(value, ".") && strings.Contains(value, ".") &&
		net.ParseIP(value) == nil && validEndpointHost(value)
}

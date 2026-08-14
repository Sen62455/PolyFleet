package server

import (
	"errors"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/Sen62455/PolyFleet/internal/store"
)

type nodeTelemetryResponse struct {
	Supported          bool                        `json:"supported"`
	SampledAt          *time.Time                  `json:"sampled_at"`
	ProcessesAvailable bool                        `json:"processes_available"`
	ProcessesErrorCode string                      `json:"processes_error_code"`
	ProcessesSampledAt *time.Time                  `json:"processes_sampled_at"`
	ProcessesTotal     int                         `json:"processes_total"`
	ProcessesTruncated bool                        `json:"processes_truncated"`
	Processes          []protocol.ProcessTelemetry `json:"processes"`
	ServicesAvailable  bool                        `json:"services_available"`
	ServicesErrorCode  string                      `json:"services_error_code"`
	ServicesSampledAt  *time.Time                  `json:"services_sampled_at"`
	ServicesTotal      int                         `json:"services_total"`
	ServicesTruncated  bool                        `json:"services_truncated"`
	Services           []protocol.ServiceTelemetry `json:"services"`
}

func (a *App) handleAgentTelemetry(response http.ResponseWriter, request *http.Request) {
	identity := agentFromContext(request.Context())
	var input protocol.TelemetrySnapshotRequest
	if err := decodeJSON(response, request, &input, 128*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid telemetry request")
		return
	}
	now := time.Now().UTC()
	if !validTelemetrySnapshot(input, now) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "telemetry values are outside accepted bounds")
		return
	}
	err := a.store.RecordNodeTelemetry(request.Context(), identity, input, now)
	if errors.Is(err, store.ErrConflict) {
		a.writeError(response, request, http.StatusConflict, "installation_conflict", "Agent installation does not match enrollment")
		return
	}
	if err != nil {
		a.logger.Error("record telemetry failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "telemetry_failed", "telemetry could not be recorded")
		return
	}
	writeJSON(response, http.StatusOK, protocol.TelemetrySnapshotResponse{Accepted: true, ServerTime: now})
}

func (a *App) handleGetNodeTelemetry(response http.ResponseWriter, request *http.Request) {
	telemetry, err := a.store.GetNodeTelemetry(request.Context(), chi.URLParam(request, "nodeID"))
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "node_not_found", "node not found")
		return
	}
	if err != nil {
		a.logger.Error("read node telemetry failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "node_telemetry_read_failed", "could not read node telemetry")
		return
	}
	writeJSON(response, http.StatusOK, nodeTelemetryResponse{
		Supported: telemetry.Supported, SampledAt: telemetry.SampledAt,
		ProcessesAvailable: telemetry.ProcessesAvailable,
		ProcessesErrorCode: telemetry.ProcessesErrorCode,
		ProcessesSampledAt: telemetry.ProcessesSampledAt,
		ProcessesTotal:     telemetry.ProcessesTotal, ProcessesTruncated: telemetry.ProcessesTruncated,
		Processes: telemetry.Processes, ServicesAvailable: telemetry.ServicesAvailable,
		ServicesErrorCode: telemetry.ServicesErrorCode,
		ServicesSampledAt: telemetry.ServicesSampledAt,
		ServicesTotal:     telemetry.ServicesTotal, ServicesTruncated: telemetry.ServicesTruncated,
		Services: telemetry.Services,
	})
}

func validTelemetrySnapshot(input protocol.TelemetrySnapshotRequest, now time.Time) bool {
	if _, err := uuid.Parse(input.InstallationID); err != nil || input.SampledAt.IsZero() ||
		input.SampledAt.Before(now.Add(-15*time.Minute)) || input.SampledAt.After(now.Add(5*time.Minute)) {
		return false
	}
	if !validTelemetrySection(
		input.ProcessesAvailable, input.ProcessesErrorCode, input.ProcessesTotal,
		input.ProcessesTruncated, len(input.Processes), protocol.MaxTelemetryProcesses,
	) || !validTelemetrySection(
		input.ServicesAvailable, input.ServicesErrorCode, input.ServicesTotal,
		input.ServicesTruncated, len(input.Services), protocol.MaxTelemetryServices,
	) {
		return false
	}
	processes := make(map[int]struct{}, len(input.Processes))
	for _, process := range input.Processes {
		if process.PID < 1 || process.Name == "" || !validTelemetryText(process.Name, 64) ||
			!validTelemetryText(process.Unit, 128) || !validTelemetryFloat(process.CPUPercent) ||
			process.CPUPercent > 409600 || process.RSSBytes < 0 || process.UptimeSeconds < 0 {
			return false
		}
		if _, exists := processes[process.PID]; exists {
			return false
		}
		processes[process.PID] = struct{}{}
	}
	services := make(map[string]struct{}, len(input.Services))
	for _, service := range input.Services {
		if service.Unit == "" || !strings.HasSuffix(service.Unit, ".service") ||
			!validTelemetryText(service.Unit, 128) || !validTelemetryText(service.Description, 256) ||
			service.ActiveState == "" || !validTelemetryText(service.ActiveState, 32) ||
			service.SubState == "" || !validTelemetryText(service.SubState, 32) ||
			!validTelemetryFloat(service.CPUPercent) || !validTelemetryFloat(service.CPUPeakPercent) ||
			service.CPUPercent > 409600 || service.CPUPeakPercent > 409600 ||
			service.CPUPeakPercent < service.CPUPercent || service.MemoryBytes < 0 ||
			service.MemoryPeakBytes < 0 || service.Tasks < 0 || service.Tasks > 1000000000 ||
			service.Restarts < 0 || service.Restarts > 1000000000 || service.MainPID < 0 {
			return false
		}
		if _, exists := services[service.Unit]; exists {
			return false
		}
		services[service.Unit] = struct{}{}
	}
	return true
}

func validTelemetrySection(
	available bool,
	errorCode string,
	total int,
	truncated bool,
	entries int,
	maximumEntries int,
) bool {
	if entries > maximumEntries || total < entries || total > 1000000 || truncated != (total > entries) {
		return false
	}
	if available {
		return errorCode == ""
	}
	return validTelemetryErrorCode(errorCode) && entries == 0 && total == 0 && !truncated
}

func validTelemetryErrorCode(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func validTelemetryText(value string, maximum int) bool {
	if len(value) > maximum {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validTelemetryFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

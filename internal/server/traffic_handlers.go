package server

import (
	"errors"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/Sen62455/PolyFleet/internal/store"
)

const (
	maxTrafficBatchesPerRequest = 20
	maxTrafficDeltaBytes        = int64(1 << 50)
	maxOnlineUsersPerSnapshot   = 10000
	maxOnlineConnectionCount    = 1000000
)

func (a *App) handleAgentTrafficBatches(response http.ResponseWriter, request *http.Request) {
	identity := agentFromContext(request.Context())
	var input protocol.TrafficBatchesRequest
	if err := decodeJSON(response, request, &input, 4*1024*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid traffic batch request")
		return
	}
	if len(input.Batches) < 1 || len(input.Batches) > maxTrafficBatchesPerRequest {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "traffic batch count is outside accepted bounds")
		return
	}
	now := time.Now().UTC()
	results := make([]protocol.TrafficBatchResult, 0, len(input.Batches))
	for _, batch := range input.Batches {
		if code := validateTrafficBatch(batch, now); code != "" {
			results = append(results, protocol.TrafficBatchResult{
				ID: batch.ID, Status: "rejected", ErrorCode: code,
			})
			continue
		}
		result, err := a.store.IngestTrafficBatch(request.Context(), identity, batch, now)
		if err != nil {
			a.logger.Error("traffic batch ingestion failed",
				"request_id", requestIDFromContext(request.Context()),
				"batch_id", batch.ID, "error", err)
			a.writeError(response, request, http.StatusInternalServerError,
				"traffic_ingest_failed", "traffic batch could not be recorded")
			return
		}
		results = append(results, protocol.TrafficBatchResult{
			ID: batch.ID, Status: result.Status, ErrorCode: result.ErrorCode,
		})
	}
	writeJSON(response, http.StatusOK, protocol.TrafficBatchesResponse{
		Results: results, ServerTime: now,
	})
}

func validateTrafficBatch(batch protocol.TrafficBatch, now time.Time) string {
	if _, err := uuid.Parse(batch.ID); err != nil {
		return "invalid_batch_id"
	}
	if _, err := uuid.Parse(batch.InstallationID); err != nil {
		return "invalid_installation_id"
	}
	if _, err := uuid.Parse(batch.SourceEpoch); err != nil {
		return "invalid_source_epoch"
	}
	if batch.Sequence < 1 || batch.SampledAt.IsZero() || batch.SampledAt.After(now.Add(10*time.Minute)) {
		return "invalid_sample_metadata"
	}
	if len(batch.Items) < 1 || len(batch.Items) > protocol.MaxTrafficItemsPerBatch {
		return "invalid_item_count"
	}
	seen := make(map[string]struct{}, len(batch.Items))
	var uploadTotal, downloadTotal int64
	for _, item := range batch.Items {
		if !validUsageUserID(item.UserID) {
			return "invalid_user_id"
		}
		if _, exists := seen[item.UserID]; exists {
			return "duplicate_user_id"
		}
		seen[item.UserID] = struct{}{}
		if item.UploadBytes < 0 || item.DownloadBytes < 0 ||
			item.UploadBytes > maxTrafficDeltaBytes || item.DownloadBytes > maxTrafficDeltaBytes ||
			(item.UploadBytes == 0 && item.DownloadBytes == 0) {
			return "invalid_traffic_delta"
		}
		if uploadTotal > math.MaxInt64-item.UploadBytes || downloadTotal > math.MaxInt64-item.DownloadBytes {
			return "batch_total_overflow"
		}
		uploadTotal += item.UploadBytes
		downloadTotal += item.DownloadBytes
	}
	return ""
}

func (a *App) handleAgentOnlineSnapshot(response http.ResponseWriter, request *http.Request) {
	identity := agentFromContext(request.Context())
	var input protocol.OnlineSnapshotRequest
	if err := decodeJSON(response, request, &input, 2*1024*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid online snapshot")
		return
	}
	now := time.Now().UTC()
	if !validOnlineSnapshot(input, now) {
		a.writeError(response, request, http.StatusUnprocessableEntity,
			"validation_failed", "online snapshot values are outside accepted bounds")
		return
	}
	accepted, err := a.store.RecordOnlineSnapshot(request.Context(), identity, input, now)
	if errors.Is(err, store.ErrUnsupported) {
		a.writeError(response, request, http.StatusUnprocessableEntity,
			"adapter_online_unsupported", "node adapter does not support online snapshots")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		a.writeError(response, request, http.StatusConflict,
			"installation_conflict", "Agent installation does not match enrollment")
		return
	}
	if err != nil {
		a.logger.Error("online snapshot failed", "request_id", requestIDFromContext(request.Context()), "error", err)
		a.writeError(response, request, http.StatusInternalServerError,
			"online_snapshot_failed", "online snapshot could not be recorded")
		return
	}
	writeJSON(response, http.StatusOK, protocol.OnlineSnapshotResponse{
		Accepted: accepted, ServerTime: now,
	})
}

func validOnlineSnapshot(input protocol.OnlineSnapshotRequest, now time.Time) bool {
	if _, err := uuid.Parse(input.SnapshotID); err != nil {
		return false
	}
	if _, err := uuid.Parse(input.InstallationID); err != nil {
		return false
	}
	if input.SampledAt.IsZero() || input.SampledAt.After(now.Add(10*time.Minute)) ||
		len(input.Users) > maxOnlineUsersPerSnapshot {
		return false
	}
	seen := make(map[string]struct{}, len(input.Users))
	for _, user := range input.Users {
		if !validUsageUserID(user.UserID) || user.Connections < 1 ||
			user.Connections > maxOnlineConnectionCount {
			return false
		}
		if _, exists := seen[user.UserID]; exists {
			return false
		}
		seen[user.UserID] = struct{}{}
	}
	return true
}

func validUsageUserID(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character == 0x7f {
			return false
		}
	}
	return true
}

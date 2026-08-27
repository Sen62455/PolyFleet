package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/store"
	"github.com/go-chi/chi/v5"
)

type nodeAssetRequest struct {
	Plan               string     `json:"plan"`
	PurchasedAt        *time.Time `json:"purchased_at"`
	ExpiresAt          *time.Time `json:"expires_at"`
	RenewalCycleMonths int        `json:"renewal_cycle_months"`
	AutoRenew          bool       `json:"auto_renew"`
	Notes              string     `json:"notes"`
}

type nodeAssetResponse struct {
	NodeID             string     `json:"node_id"`
	NodeName           string     `json:"node_name"`
	Plan               string     `json:"plan"`
	PurchasedAt        *time.Time `json:"purchased_at"`
	ExpiresAt          *time.Time `json:"expires_at"`
	RenewalCycleMonths int        `json:"renewal_cycle_months"`
	AutoRenew          bool       `json:"auto_renew"`
	Notes              string     `json:"notes"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func presentNodeAsset(asset store.NodeAsset) nodeAssetResponse {
	return nodeAssetResponse{
		NodeID: asset.NodeID, NodeName: asset.NodeName, Plan: asset.Plan,
		PurchasedAt: asset.PurchasedAt, ExpiresAt: asset.ExpiresAt,
		RenewalCycleMonths: asset.RenewalCycleMonths, AutoRenew: asset.AutoRenew,
		Notes: asset.Notes, CreatedAt: asset.CreatedAt, UpdatedAt: asset.UpdatedAt,
	}
}

func (a *App) handleListNodeAssets(response http.ResponseWriter, request *http.Request) {
	assets, err := a.store.ListNodeAssets(request.Context())
	if err != nil {
		a.logger.Error("list node assets failed", "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "assets_read_failed", "could not read VPS assets")
		return
	}
	result := make([]nodeAssetResponse, 0, len(assets))
	for _, asset := range assets {
		result = append(result, presentNodeAsset(asset))
	}
	writeJSON(response, http.StatusOK, map[string]any{"assets": result})
}

func (a *App) handleUpsertNodeAsset(response http.ResponseWriter, request *http.Request) {
	var input nodeAssetRequest
	if err := decodeJSON(response, request, &input, 32*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid VPS asset request")
		return
	}
	input.Plan = strings.TrimSpace(input.Plan)
	input.Notes = strings.TrimSpace(input.Notes)
	if len(input.Plan) > 120 || len(input.Notes) > 1000 ||
		input.RenewalCycleMonths < 0 || input.RenewalCycleMonths > 120 ||
		(input.PurchasedAt != nil && input.ExpiresAt != nil && input.ExpiresAt.Before(*input.PurchasedAt)) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "invalid plan, dates, renewal cycle, or notes")
		return
	}
	asset, err := a.store.UpsertNodeAsset(request.Context(), chi.URLParam(request, "nodeID"), store.NodeAssetInput{
		Plan: input.Plan, PurchasedAt: input.PurchasedAt, ExpiresAt: input.ExpiresAt,
		RenewalCycleMonths: input.RenewalCycleMonths, AutoRenew: input.AutoRenew,
		Notes: input.Notes, Now: time.Now().UTC(),
	})
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "node_not_found", "node not found")
		return
	}
	if err != nil {
		a.logger.Error("update node asset failed", "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "asset_update_failed", "could not update VPS asset")
		return
	}
	writeJSON(response, http.StatusOK, presentNodeAsset(asset))
}

type subscriptionOperationResponse struct {
	TokenID              string     `json:"token_id"`
	UserID               string     `json:"user_id"`
	Username             string     `json:"username"`
	DisplayName          string     `json:"display_name"`
	Name                 string     `json:"name"`
	TokenPrefix          string     `json:"token_prefix"`
	AllowedFormats       []string   `json:"allowed_formats"`
	Status               string     `json:"status"`
	TokenExpiresAt       *time.Time `json:"token_expires_at"`
	UserExpiresAt        *time.Time `json:"user_expires_at"`
	LastUsedAt           *time.Time `json:"last_used_at"`
	LastTrafficAt        *time.Time `json:"last_traffic_at"`
	RevokedAt            *time.Time `json:"revoked_at"`
	TrafficLimitBytes    int64      `json:"traffic_limit_bytes"`
	TrafficUploadBytes   int64      `json:"traffic_upload_bytes"`
	TrafficDownloadBytes int64      `json:"traffic_download_bytes"`
	TrafficUsedBytes     int64      `json:"traffic_used_bytes"`
	AssignmentCount      int        `json:"assignment_count"`
	OnlineNodes          int        `json:"online_nodes"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

func presentSubscriptionOperation(item store.SubscriptionOperation) subscriptionOperationResponse {
	return subscriptionOperationResponse{
		TokenID: item.TokenID, UserID: item.UserID, Username: item.Username,
		DisplayName: item.DisplayName, Name: item.Name, TokenPrefix: item.TokenPrefix,
		AllowedFormats: item.AllowedFormats, Status: item.Status,
		TokenExpiresAt: item.TokenExpiresAt, UserExpiresAt: item.UserExpiresAt,
		LastUsedAt: item.LastUsedAt, LastTrafficAt: item.LastTrafficAt,
		RevokedAt: item.RevokedAt, TrafficLimitBytes: item.TrafficLimitBytes,
		TrafficUploadBytes:   item.TrafficUploadBytes,
		TrafficDownloadBytes: item.TrafficDownloadBytes,
		TrafficUsedBytes:     item.TrafficUsedBytes, AssignmentCount: item.AssignmentCount,
		OnlineNodes: item.OnlineNodes, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

func queryOffset(value string) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func (a *App) handleListSubscriptionOperations(response http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	limit := boundedQueryLimit(query.Get("limit"), 50, 200)
	offset := queryOffset(query.Get("offset"))
	items, total, err := a.store.ListSubscriptionOperations(request.Context(), store.SubscriptionOperationFilter{
		Status: query.Get("status"), Search: query.Get("search"), Limit: limit,
		Offset: offset, Now: time.Now().UTC(),
	})
	if errors.Is(err, store.ErrUnsupported) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "unsupported subscription status filter")
		return
	}
	if err != nil {
		a.logger.Error("list subscription operations failed", "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "subscriptions_read_failed", "could not read subscriptions")
		return
	}
	result := make([]subscriptionOperationResponse, 0, len(items))
	for _, item := range items {
		result = append(result, presentSubscriptionOperation(item))
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"subscriptions": result, "total": total, "limit": limit, "offset": offset,
	})
}

type optionalTime struct {
	Set   bool
	Value *time.Time
}

func (value *optionalTime) UnmarshalJSON(data []byte) error {
	value.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		value.Value = nil
		return nil
	}
	var parsed time.Time
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	value.Value = &parsed
	return nil
}

type subscriptionOperationPatch struct {
	TokenExpiresAt    optionalTime `json:"token_expires_at"`
	UserExpiresAt     optionalTime `json:"user_expires_at"`
	TrafficLimitBytes *int64       `json:"traffic_limit_bytes"`
	Revoke            bool         `json:"revoke"`
}

func (a *App) handlePatchSubscriptionOperation(response http.ResponseWriter, request *http.Request) {
	var input subscriptionOperationPatch
	if err := decodeJSON(response, request, &input, 16*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid subscription update")
		return
	}
	if !input.TokenExpiresAt.Set && !input.UserExpiresAt.Set && input.TrafficLimitBytes == nil && !input.Revoke {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "at least one subscription change is required")
		return
	}
	if input.TrafficLimitBytes != nil && !validTrafficLimit(*input.TrafficLimitBytes) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "traffic_limit_bytes must be a JavaScript-safe non-negative integer")
		return
	}
	now := time.Now().UTC()
	tokenID := chi.URLParam(request, "tokenID")
	updated, err := a.store.UpdateSubscriptionOperation(request.Context(), tokenID, store.SubscriptionOperationUpdate{
		TokenExpiresAtSet: input.TokenExpiresAt.Set,
		TokenExpiresAt:    input.TokenExpiresAt.Value,
		UserExpiresAtSet:  input.UserExpiresAt.Set,
		UserExpiresAt:     input.UserExpiresAt.Value,
		TrafficLimitBytes: input.TrafficLimitBytes,
		Revoke:            input.Revoke,
		Now:               now,
	})
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "subscription_not_found", "subscription not found")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		a.writeError(response, request, http.StatusConflict, "subscription_update_conflict", "subscription is no longer editable")
		return
	}
	if err != nil {
		a.logger.Error("update subscription operation failed", "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "subscription_update_failed", "could not update subscription")
		return
	}
	writeJSON(response, http.StatusOK, presentSubscriptionOperation(updated))
}

type trafficPointResponse struct {
	BucketAt      time.Time `json:"bucket_at"`
	UploadBytes   int64     `json:"upload_bytes"`
	DownloadBytes int64     `json:"download_bytes"`
}

type trafficRankResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	UploadBytes   int64  `json:"upload_bytes"`
	DownloadBytes int64  `json:"download_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
}

func presentTrafficRanks(ranks []store.TrafficRank) []trafficRankResponse {
	result := make([]trafficRankResponse, 0, len(ranks))
	for _, rank := range ranks {
		result = append(result, trafficRankResponse{
			ID: rank.ID, Name: rank.Name, UploadBytes: rank.UploadBytes,
			DownloadBytes: rank.DownloadBytes, TotalBytes: rank.UploadBytes + rank.DownloadBytes,
		})
	}
	return result
}

func (a *App) handleTrafficReport(response http.ResponseWriter, request *http.Request) {
	rangeValue := strings.TrimSpace(request.URL.Query().Get("range"))
	days := 30
	switch rangeValue {
	case "", "30d":
	case "7d":
		days = 7
	default:
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "range must be 7d or 30d")
		return
	}
	groupBy := strings.TrimSpace(request.URL.Query().Get("group_by"))
	if groupBy != "" && groupBy != "all" && groupBy != "day" && groupBy != "user" && groupBy != "node" {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "group_by must be all, day, user, or node")
		return
	}
	to := time.Now().UTC().Truncate(24 * time.Hour).Add(24 * time.Hour)
	from := to.AddDate(0, 0, -days)
	report, err := a.store.TrafficReport(request.Context(), from, to, 8)
	if err != nil {
		a.logger.Error("read traffic report failed", "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "traffic_report_failed", "could not build traffic report")
		return
	}
	daily := make([]trafficPointResponse, 0, len(report.Daily))
	for _, point := range report.Daily {
		daily = append(daily, trafficPointResponse{
			BucketAt: point.BucketAt, UploadBytes: point.UploadBytes,
			DownloadBytes: point.DownloadBytes,
		})
	}
	previousDaily := make([]trafficPointResponse, 0, len(report.PreviousDaily))
	for _, point := range report.PreviousDaily {
		previousDaily = append(previousDaily, trafficPointResponse{
			BucketAt: point.BucketAt, UploadBytes: point.UploadBytes,
			DownloadBytes: point.DownloadBytes,
		})
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"range": strconv.Itoa(days) + "d", "from": report.From, "to": report.To,
		"upload_bytes": report.UploadBytes, "download_bytes": report.DownloadBytes,
		"total_bytes":             report.UploadBytes + report.DownloadBytes,
		"previous_upload_bytes":   report.PreviousUploadBytes,
		"previous_download_bytes": report.PreviousDownloadBytes,
		"previous_total_bytes":    report.PreviousUploadBytes + report.PreviousDownloadBytes,
		"daily":                   daily, "previous_daily": previousDaily,
		"top_users": presentTrafficRanks(report.TopUsers),
		"top_nodes": presentTrafficRanks(report.TopNodes),
	})
}

type bulkNodeRequest struct {
	NodeIDs  []string `json:"node_ids"`
	Action   string   `json:"action"`
	MaxLines int      `json:"max_lines"`
}

func (a *App) handleBulkNodes(response http.ResponseWriter, request *http.Request) {
	var input bulkNodeRequest
	if err := decodeJSON(response, request, &input, 64*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid bulk node request")
		return
	}
	input.Action = strings.TrimSpace(input.Action)
	if len(input.NodeIDs) < 1 || len(input.NodeIDs) > 50 {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "node_ids must contain between 1 and 50 nodes")
		return
	}
	valid := map[string]bool{
		"probe_core": true, "restart_core": true, "backup_config": true,
		"tail_core_log": true, "retry_sync": true,
	}
	if !valid[input.Action] {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "unsupported bulk node action")
		return
	}
	nodeIDs := make([]string, 0, len(input.NodeIDs))
	seen := make(map[string]bool, len(input.NodeIDs))
	for _, nodeID := range input.NodeIDs {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" {
			a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "node_ids must not contain blank values")
			return
		}
		if !seen[nodeID] {
			seen[nodeID] = true
			nodeIDs = append(nodeIDs, nodeID)
		}
	}
	session := sessionFromContext(request.Context())
	now := time.Now().UTC()
	results := make([]map[string]any, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		entry := map[string]any{"node_id": nodeID}
		var err error
		if input.Action == "retry_sync" {
			_, err = a.store.RetryNodeSync(request.Context(), nodeID, now)
		} else {
			var operation store.NodeOperation
			operation, err = a.store.CreateNodeOperation(
				request.Context(), nodeID, input.Action, input.MaxLines, session.AdminID, now,
			)
			if err == nil {
				entry["operation"] = presentNodeOperation(operation)
			}
		}
		if err != nil {
			entry["status"] = "failed"
			switch {
			case errors.Is(err, store.ErrNotFound):
				entry["error"] = "node not found"
			case errors.Is(err, store.ErrConflict):
				entry["error"] = "operation conflicts with current node state"
			default:
				entry["error"] = "operation could not be queued"
			}
		} else {
			entry["status"] = "accepted"
		}
		results = append(results, entry)
	}
	writeJSON(response, http.StatusOK, map[string]any{"results": results})
}

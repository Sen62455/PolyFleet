package server

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
)

func TestOperationsLayerAPIs(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)

	createdNode := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "operations-node", "provider": "DMIT", "region": "Los Angeles",
		"adapter_type": "native_hysteria2", "traffic_limit_bytes": 2 << 40,
	}, app.csrf, "")
	requireStatus(t, createdNode, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, createdNode, &node)

	expiresAt := time.Now().UTC().AddDate(1, 0, 0).Truncate(time.Second)
	assetResponse := app.request(t, http.MethodPut, "/api/v1/nodes/"+node.ID+"/asset", map[string]any{
		"plan": "LAX.Pro.Pocket", "expires_at": expiresAt,
		"renewal_cycle_months": 12, "auto_renew": false, "notes": "manual asset record",
	}, app.csrf, "")
	requireStatus(t, assetResponse, http.StatusOK)
	var asset nodeAssetResponse
	decodeResponse(t, assetResponse, &asset)
	if asset.Plan != "LAX.Pro.Pocket" || asset.ExpiresAt == nil || asset.RenewalCycleMonths != 12 {
		t.Fatalf("unexpected asset response: %#v", asset)
	}
	assets := app.request(t, http.MethodGet, "/api/v1/assets", nil, "", "")
	requireStatus(t, assets, http.StatusOK)
	if !strings.Contains(assets.Body.String(), "LAX.Pro.Pocket") {
		t.Fatalf("asset list does not include saved plan: %s", assets.Body.String())
	}

	createdUser := app.request(t, http.MethodPost, "/api/v1/users", map[string]any{
		"username": "ops-user", "display_name": "Operations User", "notes": "",
		"enabled": true, "traffic_limit_bytes": 10 << 30, "node_ids": []string{},
	}, app.csrf, "")
	requireStatus(t, createdUser, http.StatusCreated)
	var created struct {
		User userResponse `json:"user"`
	}
	decodeResponse(t, createdUser, &created)
	tokenResponse := app.request(t, http.MethodPost,
		"/api/v1/users/"+created.User.ID+"/subscription-tokens", map[string]any{
			"name": "Primary", "allowed_formats": []string{"clash", "sing-box"},
			"expires_at": time.Now().UTC().Add(30 * 24 * time.Hour),
		}, app.csrf, "")
	requireStatus(t, tokenResponse, http.StatusCreated)
	var issued issuedSubscriptionTokenResponse
	decodeResponse(t, tokenResponse, &issued)

	listed := app.request(t, http.MethodGet, "/api/v1/subscriptions?status=active&limit=20", nil, "", "")
	requireStatus(t, listed, http.StatusOK)
	var page struct {
		Subscriptions []subscriptionOperationResponse `json:"subscriptions"`
		Total         int                             `json:"total"`
	}
	decodeResponse(t, listed, &page)
	if page.Total != 1 || len(page.Subscriptions) != 1 || page.Subscriptions[0].TokenID != issued.Subscription.ID {
		t.Fatalf("unexpected subscription operations page: %#v", page)
	}
	newExpiry := time.Now().UTC().Add(90 * 24 * time.Hour).Truncate(time.Second)
	patched := app.request(t, http.MethodPatch,
		"/api/v1/subscriptions/"+issued.Subscription.ID, map[string]any{
			"token_expires_at": newExpiry, "user_expires_at": newExpiry,
			"traffic_limit_bytes": 20 << 30,
		}, app.csrf, "")
	requireStatus(t, patched, http.StatusOK)
	var subscription subscriptionOperationResponse
	decodeResponse(t, patched, &subscription)
	if subscription.TrafficLimitBytes != 20<<30 || subscription.UserExpiresAt == nil {
		t.Fatalf("unexpected patched subscription: %#v", subscription)
	}
	cleared := app.request(t, http.MethodPatch,
		"/api/v1/subscriptions/"+issued.Subscription.ID, map[string]any{
			"token_expires_at": nil, "user_expires_at": nil,
		}, app.csrf, "")
	requireStatus(t, cleared, http.StatusOK)
	decodeResponse(t, cleared, &subscription)
	if subscription.TokenExpiresAt != nil || subscription.UserExpiresAt != nil {
		t.Fatalf("subscription expiry was not cleared: %#v", subscription)
	}

	now := time.Now().UTC()
	batchID := cryptoutil.NewID()
	if _, err := app.store.DB().Exec(`
		INSERT INTO traffic_batches(
			id, node_id, agent_installation_id, source_epoch, sequence,
			sampled_at, received_at, item_count, upload_bytes, download_bytes, payload_sha256
		) VALUES (?, ?, ?, 'epoch-1', 1, ?, ?, 1, ?, ?, ?)
	`, batchID, node.ID, cryptoutil.NewID(), now.UnixMilli(), now.UnixMilli(),
		int64(2<<30), int64(3<<30), bytes.Repeat([]byte{0x42}, 32)); err != nil {
		t.Fatalf("seed traffic batch: %v", err)
	}
	if _, err := app.store.DB().Exec(`
		INSERT INTO traffic_batch_items(
			batch_id, node_id, user_id, upload_bytes, download_bytes, disposition
		) VALUES (?, ?, ?, ?, ?, 'accounted')
	`, batchID, node.ID, created.User.ID, int64(2<<30), int64(3<<30)); err != nil {
		t.Fatalf("seed traffic item: %v", err)
	}
	previousBatchID := cryptoutil.NewID()
	previousSample := now.AddDate(0, 0, -31).UnixMilli()
	if _, err := app.store.DB().Exec(`
		INSERT INTO traffic_batches(
			id, node_id, agent_installation_id, source_epoch, sequence,
			sampled_at, received_at, item_count, upload_bytes, download_bytes, payload_sha256
		) VALUES (?, ?, ?, 'epoch-previous', 1, ?, ?, 1, ?, ?, ?)
	`, previousBatchID, node.ID, cryptoutil.NewID(), previousSample, previousSample,
		int64(1<<30), int64(1<<30), bytes.Repeat([]byte{0x24}, 32)); err != nil {
		t.Fatalf("seed previous traffic batch: %v", err)
	}
	report := app.request(t, http.MethodGet, "/api/v1/reports/traffic?range=30d&group_by=all", nil, "", "")
	requireStatus(t, report, http.StatusOK)
	var traffic struct {
		TotalBytes         int64                  `json:"total_bytes"`
		PreviousTotalBytes int64                  `json:"previous_total_bytes"`
		Daily              []trafficPointResponse `json:"daily"`
		PreviousDaily      []trafficPointResponse `json:"previous_daily"`
		TopUsers           []trafficRankResponse  `json:"top_users"`
		TopNodes           []trafficRankResponse  `json:"top_nodes"`
	}
	decodeResponse(t, report, &traffic)
	if traffic.TotalBytes != 5<<30 || traffic.PreviousTotalBytes != 2<<30 ||
		len(traffic.Daily) != 30 || len(traffic.PreviousDaily) != 30 ||
		len(traffic.TopUsers) != 1 || len(traffic.TopNodes) != 1 {
		t.Fatalf("unexpected traffic report: %#v", traffic)
	}

	privateWebhook := app.request(t, http.MethodPut, "/api/v1/settings/notifications", map[string]any{
		"name": "private", "kind": "webhook", "enabled": true,
		"events": []string{"created"}, "url": "https://127.0.0.1/hook",
	}, app.csrf, "")
	requireStatus(t, privateWebhook, http.StatusUnprocessableEntity)
	createdNotifier := app.request(t, http.MethodPut, "/api/v1/settings/notifications", map[string]any{
		"name": "Operations webhook", "kind": "webhook", "enabled": true,
		"events": []string{"created", "resolved"}, "url": "https://hooks.example.com/polyfleet",
	}, app.csrf, "")
	requireStatus(t, createdNotifier, http.StatusOK)
	if strings.Contains(createdNotifier.Body.String(), "hooks.example.com/polyfleet") {
		t.Fatalf("notifier response leaked full webhook URL: %s", createdNotifier.Body.String())
	}

	old := now.Add(-10 * time.Minute).UnixMilli()
	if _, err := app.store.DB().Exec(`
		UPDATE nodes SET agent_installation_id = ?, last_seen_at = ?, status = 'offline'
		WHERE id = ?
	`, cryptoutil.NewID(), old, node.ID); err != nil {
		t.Fatalf("mark node offline: %v", err)
	}
	alerts := app.request(t, http.MethodGet,
		"/api/v1/alerts?status=active&node_id="+node.ID+"&type=offline&limit=10&offset=0",
		nil, "", "")
	requireStatus(t, alerts, http.StatusOK)
	var deliveryCount int
	if err := app.store.DB().QueryRow(`
		SELECT COUNT(*) FROM notification_deliveries WHERE event_type = 'created'
	`).Scan(&deliveryCount); err != nil || deliveryCount != 1 {
		t.Fatalf("queued delivery count = %d, error = %v", deliveryCount, err)
	}
	var deliveryID string
	if err := app.store.DB().QueryRow(`
		SELECT id FROM notification_deliveries WHERE event_type = 'created'
	`).Scan(&deliveryID); err != nil {
		t.Fatalf("read queued delivery: %v", err)
	}
	for attempt := 0; attempt < 6; attempt++ {
		if err := app.store.RecordNotificationDelivery(
			t.Context(), deliveryID, false, http.StatusBadGateway, "bounded failure",
			now.Add(time.Duration(attempt)*time.Hour),
		); err != nil {
			t.Fatalf("record notification attempt %d: %v", attempt+1, err)
		}
	}
	var deliveryStatus string
	var attempts int
	if err := app.store.DB().QueryRow(`
		SELECT status, attempt_count FROM notification_deliveries WHERE id = ?
	`, deliveryID).Scan(&deliveryStatus, &attempts); err != nil {
		t.Fatalf("read failed delivery: %v", err)
	}
	if deliveryStatus != "failed" || attempts != 6 {
		t.Fatalf("delivery retry state = %s/%d, want failed/6", deliveryStatus, attempts)
	}
}

func TestUserListPaginationAndSearch(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)
	for _, username := range []string{"alpha-operator", "bravo-operator", "charlie-operator"} {
		response := app.request(t, http.MethodPost, "/api/v1/users", map[string]any{
			"username": username, "display_name": username, "notes": "pagination fixture",
			"enabled": true, "traffic_limit_bytes": 0, "node_ids": []string{},
		}, app.csrf, "")
		requireStatus(t, response, http.StatusCreated)
	}

	first := app.request(t, http.MethodGet, "/api/v1/users?limit=2&offset=0&search=operator", nil, "", "")
	requireStatus(t, first, http.StatusOK)
	var page struct {
		Users  []userResponse `json:"users"`
		Total  int            `json:"total"`
		Limit  int            `json:"limit"`
		Offset int            `json:"offset"`
	}
	decodeResponse(t, first, &page)
	if page.Total != 3 || len(page.Users) != 2 || page.Limit != 2 || page.Offset != 0 {
		t.Fatalf("unexpected first user page: %#v", page)
	}
	second := app.request(t, http.MethodGet, "/api/v1/users?limit=2&offset=2&search=operator", nil, "", "")
	requireStatus(t, second, http.StatusOK)
	decodeResponse(t, second, &page)
	if page.Total != 3 || len(page.Users) != 1 || page.Offset != 2 {
		t.Fatalf("unexpected second user page: %#v", page)
	}
}

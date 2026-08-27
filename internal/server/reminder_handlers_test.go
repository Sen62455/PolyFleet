package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReminderRuleAndAuthorizedTelegramBotFlow(t *testing.T) {
	app := newTestApp(t)
	app.bootstrap(t)

	createdNode := app.request(t, http.MethodPost, "/api/v1/nodes", map[string]any{
		"name": "dmit2-reality", "provider": "DMIT", "region": "Los Angeles",
		"adapter_type": "native_hysteria2", "traffic_limit_bytes": int64(2 << 40),
	}, app.csrf, "")
	requireStatus(t, createdNode, http.StatusCreated)
	var node nodeResponse
	decodeResponse(t, createdNode, &node)

	createdNotifier := app.request(t, http.MethodPut, "/api/v1/settings/notifications", map[string]any{
		"name": "Operations Telegram", "kind": "telegram", "enabled": true,
		"events":    []string{"created", "resolved"},
		"bot_token": "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijk",
		"chat_id":   "12345678",
	}, app.csrf, "")
	requireStatus(t, createdNotifier, http.StatusOK)
	var notifier notificationNotifierResponse
	decodeResponse(t, createdNotifier, &notifier)

	createdRule := app.request(t, http.MethodPut, "/api/v1/reminder-rules", map[string]any{
		"name": "Six-hour status", "notifier_id": notifier.ID,
		"kind": "fleet_summary", "enabled": true, "interval_minutes": 360,
		"lead_days": 30, "threshold_percent": 80, "node_ids": []string{node.ID},
	}, app.csrf, "")
	requireStatus(t, createdRule, http.StatusOK)
	var rule notificationReminderRuleResponse
	decodeResponse(t, createdRule, &rule)
	if rule.Kind != "fleet_summary" || rule.IntervalMinutes != 360 || len(rule.NodeIDs) != 1 {
		t.Fatalf("unexpected reminder rule: %#v", rule)
	}

	var sendCount atomic.Int32
	app.application.notifierClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/getUpdates") {
			var poll struct {
				Offset int64 `json:"offset"`
			}
			_ = json.NewDecoder(request.Body).Decode(&poll)
			body := `{"ok":true,"result":[]}`
			if poll.Offset >= 0 {
				body = `{"ok":true,"result":[` +
					`{"update_id":10,"message":{"text":"/status","chat":{"id":999}}},` +
					`{"update_id":11,"message":{"text":"dmit2-reality","chat":{"id":12345678}}}` +
					`]}`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		}
		sendCount.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`)),
		}, nil
	})}

	botAccess := app.request(t, http.MethodPut,
		"/api/v1/notifiers/"+notifier.ID+"/telegram-bot",
		map[string]any{"enabled": true}, app.csrf, "")
	requireStatus(t, botAccess, http.StatusOK)

	manualRun := app.request(t, http.MethodPost,
		"/api/v1/reminder-rules/"+rule.ID+"/run", map[string]any{}, app.csrf, "")
	requireStatus(t, manualRun, http.StatusOK)
	if !strings.Contains(manualRun.Body.String(), `"sent":true`) {
		t.Fatalf("manual reminder did not send: %s", manualRun.Body.String())
	}

	if err := app.application.PollTelegramBots(t.Context(), 25); err != nil {
		t.Fatalf("PollTelegramBots() error = %v", err)
	}
	if sendCount.Load() != 2 {
		t.Fatalf("Telegram sends = %d, want reminder plus one authorized bot reply", sendCount.Load())
	}
	var offset int64
	if err := app.store.DB().QueryRow(
		"SELECT update_offset FROM telegram_bot_access WHERE notifier_id = ?", notifier.ID,
	).Scan(&offset); err != nil || offset != 12 {
		t.Fatalf("Telegram update offset = %d, error = %v", offset, err)
	}

	settings := app.request(t, http.MethodGet, "/api/v1/settings/notifications", nil, "", "")
	requireStatus(t, settings, http.StatusOK)
	if !strings.Contains(settings.Body.String(), "Six-hour status") ||
		!strings.Contains(settings.Body.String(), `"telegram_bots"`) ||
		strings.Contains(settings.Body.String(), "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijk") {
		t.Fatalf("unexpected notification settings response: %s", settings.Body.String())
	}
}

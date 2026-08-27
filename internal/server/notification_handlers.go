package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/store"
	"github.com/go-chi/chi/v5"
)

var telegramTokenPattern = regexp.MustCompile(`^[0-9]{5,16}:[A-Za-z0-9_-]{20,128}$`)
var telegramChatIDPattern = regexp.MustCompile(`^(?:-?[0-9]+|@[A-Za-z][A-Za-z0-9_]{4,31})$`)

type notifierSecretConfig struct {
	URL      string `json:"url,omitempty"`
	BotToken string `json:"bot_token,omitempty"`
	ChatID   string `json:"chat_id,omitempty"`
}

type notificationNotifierRequest struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Enabled  *bool    `json:"enabled"`
	Events   []string `json:"events"`
	URL      string   `json:"url"`
	BotToken string   `json:"bot_token"`
	ChatID   string   `json:"chat_id"`
}

type notificationNotifierResponse struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	Enabled    bool      `json:"enabled"`
	TargetHint string    `json:"target_hint"`
	Events     []string  `json:"events"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type notificationDeliveryResponse struct {
	ID            string     `json:"id"`
	NotifierID    string     `json:"notifier_id"`
	NotifierName  string     `json:"notifier_name"`
	NotifierKind  string     `json:"notifier_kind"`
	AlertID       string     `json:"alert_id"`
	EventType     string     `json:"event_type"`
	Status        string     `json:"status"`
	AttemptCount  int        `json:"attempt_count"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	LastError     string     `json:"last_error"`
	ResponseCode  int        `json:"response_code"`
	DeliveredAt   *time.Time `json:"delivered_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

func presentNotificationNotifier(item store.NotificationNotifier) notificationNotifierResponse {
	return notificationNotifierResponse{
		ID: item.ID, Name: item.Name, Kind: item.Kind, Enabled: item.Enabled,
		TargetHint: item.TargetHint, Events: item.Events,
		CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
	}
}

func presentNotificationDelivery(item store.NotificationDelivery) notificationDeliveryResponse {
	return notificationDeliveryResponse{
		ID: item.ID, NotifierID: item.NotifierID, NotifierName: item.NotifierName,
		NotifierKind: item.NotifierKind, AlertID: item.AlertID, EventType: item.EventType,
		Status: item.Status, AttemptCount: item.AttemptCount,
		NextAttemptAt: item.NextAttemptAt, LastError: item.LastError,
		ResponseCode: item.ResponseCode, DeliveredAt: item.DeliveredAt,
		CreatedAt: item.CreatedAt,
	}
}

func notificationConfigAD(id string) []byte {
	return []byte("polyfleet-notifier:" + id)
}

func (a *App) openNotifierSecret(id string, ciphertext []byte) (notifierSecretConfig, error) {
	plaintext, err := cryptoutil.Open(a.masterKey, ciphertext, notificationConfigAD(id))
	if err != nil {
		return notifierSecretConfig{}, errors.New("notification configuration could not be decrypted")
	}
	var secret notifierSecretConfig
	if err := json.Unmarshal(plaintext, &secret); err != nil {
		return notifierSecretConfig{}, errors.New("notification configuration is invalid")
	}
	return secret, nil
}

func (a *App) handleGetNotificationSettings(response http.ResponseWriter, request *http.Request) {
	notifiers, err := a.store.ListNotificationNotifiers(request.Context())
	if err != nil {
		a.logger.Error("list notification settings failed", "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "notification_settings_failed", "could not read notification settings")
		return
	}
	deliveries, err := a.store.ListNotificationDeliveries(request.Context(), 30)
	if err != nil {
		a.logger.Error("list notification deliveries failed", "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "notification_deliveries_failed", "could not read notification deliveries")
		return
	}
	publicNotifiers := make([]notificationNotifierResponse, 0, len(notifiers))
	for _, notifier := range notifiers {
		publicNotifiers = append(publicNotifiers, presentNotificationNotifier(notifier))
	}
	publicDeliveries := make([]notificationDeliveryResponse, 0, len(deliveries))
	for _, delivery := range deliveries {
		publicDeliveries = append(publicDeliveries, presentNotificationDelivery(delivery))
	}
	rules, err := a.store.ListNotificationReminderRules(request.Context())
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "notification_reminders_failed", "could not read reminder rules")
		return
	}
	publicRules := make([]notificationReminderRuleResponse, 0, len(rules))
	for _, rule := range rules {
		publicRules = append(publicRules, presentNotificationReminderRule(rule))
	}
	botAccess, err := a.store.ListTelegramBotAccess(request.Context())
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "telegram_bot_settings_failed", "could not read Telegram bot settings")
		return
	}
	publicBotAccess := make([]telegramBotAccessResponse, 0, len(botAccess))
	for _, access := range botAccess {
		publicBotAccess = append(publicBotAccess, presentTelegramBotAccess(access))
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"notifiers": publicNotifiers, "deliveries": publicDeliveries,
		"reminder_rules": publicRules, "telegram_bots": publicBotAccess,
	})
}

func normalizeNotifierInput(input notificationNotifierRequest) (notificationNotifierRequest, error) {
	input.ID = strings.TrimSpace(input.ID)
	input.Name = strings.TrimSpace(input.Name)
	input.Kind = strings.TrimSpace(strings.ToLower(input.Kind))
	input.URL = strings.TrimSpace(input.URL)
	input.BotToken = strings.TrimSpace(input.BotToken)
	input.ChatID = strings.TrimSpace(input.ChatID)
	if input.Name == "" || len(input.Name) > 80 {
		return input, errors.New("name must contain between 1 and 80 characters")
	}
	if input.Kind != "telegram" && input.Kind != "slack" && input.Kind != "webhook" {
		return input, errors.New("kind must be telegram, slack, or webhook")
	}
	if len(input.Events) == 0 {
		input.Events = []string{"created", "resolved"}
	}
	for _, event := range input.Events {
		if event != "created" && event != "resolved" {
			return input, errors.New("events may only contain created and resolved")
		}
	}
	return input, nil
}

func notifierSecret(input notificationNotifierRequest) (notifierSecretConfig, string, error) {
	switch input.Kind {
	case "telegram":
		if !telegramTokenPattern.MatchString(input.BotToken) {
			return notifierSecretConfig{}, "", errors.New("a valid Telegram bot token is required")
		}
		if !telegramChatIDPattern.MatchString(input.ChatID) {
			return notifierSecretConfig{}, "", errors.New("Telegram Chat ID must be numeric or a public channel @username")
		}
		hint := input.ChatID
		if len(hint) > 8 {
			hint = "..." + hint[len(hint)-8:]
		}
		return notifierSecretConfig{BotToken: input.BotToken, ChatID: input.ChatID}, hint, nil
	case "slack", "webhook":
		parsed, err := validateWebhookURL(input.URL)
		if err != nil {
			return notifierSecretConfig{}, "", err
		}
		return notifierSecretConfig{URL: parsed.String()}, parsed.Hostname(), nil
	default:
		return notifierSecretConfig{}, "", errors.New("unsupported notifier kind")
	}
}

func (a *App) handlePutNotificationSettings(response http.ResponseWriter, request *http.Request) {
	var input notificationNotifierRequest
	if err := decodeJSON(response, request, &input, 32*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid notification setting")
		return
	}
	input, err := normalizeNotifierInput(input)
	if err != nil {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", err.Error())
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if input.ID == "" {
		input.ID = cryptoutil.NewID()
	}
	secret, hint, secretErr := notifierSecret(input)
	var ciphertext []byte
	if secretErr != nil && input.ID != "" && input.URL == "" && input.BotToken == "" && input.ChatID == "" {
		existing, readErr := a.store.GetNotificationNotifier(request.Context(), input.ID)
		if readErr == nil && existing.Kind == input.Kind {
			ciphertext = existing.ConfigCiphertext
			hint = existing.TargetHint
			secretErr = nil
		}
	}
	if secretErr != nil {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", secretErr.Error())
		return
	}
	if len(ciphertext) == 0 {
		encoded, encodeErr := json.Marshal(secret)
		if encodeErr != nil {
			a.writeError(response, request, http.StatusInternalServerError, "notification_config_failed", "could not encode notification setting")
			return
		}
		ciphertext, err = cryptoutil.Seal(a.masterKey, encoded, notificationConfigAD(input.ID))
		if err != nil {
			a.logger.Error("encrypt notifier config failed", "error", err)
			a.writeError(response, request, http.StatusInternalServerError, "notification_config_failed", "could not protect notification setting")
			return
		}
	}
	notifier, err := a.store.UpsertNotificationNotifier(request.Context(), store.NotificationNotifier{
		ID: input.ID, Name: input.Name, Kind: input.Kind, Enabled: enabled,
		ConfigCiphertext: ciphertext, TargetHint: hint, Events: input.Events,
	}, time.Now().UTC())
	if errors.Is(err, store.ErrUnsupported) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "unsupported notification event")
		return
	}
	if err != nil {
		a.logger.Error("save notification setting failed", "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "notification_config_failed", "could not save notification setting")
		return
	}
	writeJSON(response, http.StatusOK, presentNotificationNotifier(notifier))
}

func (a *App) handleDeleteNotifier(response http.ResponseWriter, request *http.Request) {
	err := a.store.DeleteNotificationNotifier(request.Context(), chi.URLParam(request, "notifierID"))
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "notifier_not_found", "notifier not found")
		return
	}
	if err != nil {
		a.logger.Error("delete notifier failed", "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "notifier_delete_failed", "could not delete notifier")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (a *App) handleTestNotifier(response http.ResponseWriter, request *http.Request) {
	notifier, err := a.store.GetNotificationNotifier(request.Context(), chi.URLParam(request, "notifierID"))
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "notifier_not_found", "notifier not found")
		return
	}
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "notifier_read_failed", "could not read notifier")
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"event": "test", "status": "ok", "message": "PolyFleet notification channel test",
		"occurred_at": time.Now().UTC(),
	})
	status, err := a.deliverNotification(request.Context(), notifier.Kind, notifier.ID, notifier.ConfigCiphertext, payload)
	if err != nil {
		a.writeError(response, request, http.StatusBadGateway, "notifier_test_failed", err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"delivered": true, "response_code": status})
}

func validateWebhookURL(raw string) (*url.URL, error) {
	if len(raw) > 2048 {
		return nil, errors.New("webhook URL is too long")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("webhook URL must be an absolute HTTPS URL without credentials")
	}
	if parsed.Fragment != "" {
		return nil, errors.New("webhook URL must not contain a fragment")
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return nil, errors.New("webhook URL host is not allowed")
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return nil, errors.New("webhook URL must use the standard HTTPS port")
	}
	if ip := net.ParseIP(host); ip != nil && !publicWebhookIP(ip) {
		return nil, errors.New("webhook URL must not target a private or local address")
	}
	return parsed, nil
}

func publicWebhookIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 0 || v4[0] == 127 || (v4[0] == 100 && v4[1]&0xc0 == 0x40) ||
			(v4[0] == 169 && v4[1] == 254) || (v4[0] == 198 && (v4[1] == 18 || v4[1] == 19)) {
			return false
		}
	}
	return true
}

func safeWebhookClient() *http.Client {
	dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	resolver := net.DefaultResolver
	transport := &http.Transport{
		Proxy:             nil,
		ForceAttemptHTTP2: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := resolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("resolve webhook host: %w", err)
			}
			for _, address := range addresses {
				if !publicWebhookIP(address.IP) {
					return nil, errors.New("webhook host resolved to a private or local address")
				}
			}
			var lastErr error
			for _, resolved := range addresses {
				connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
				if err == nil {
					return connection, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          10,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("notification redirects are not allowed")
		},
	}
}

func (a *App) DispatchNotifications(ctx context.Context, limit int) error {
	now := time.Now().UTC()
	deliveries, err := a.store.ListDueNotificationDeliveries(ctx, now, limit)
	if err != nil {
		return err
	}
	var result error
	for _, delivery := range deliveries {
		status, sendErr := a.deliverNotification(
			ctx, delivery.NotifierKind, delivery.NotifierID,
			delivery.ConfigCiphertext, []byte(delivery.PayloadJSON),
		)
		errorMessage := ""
		if sendErr != nil {
			errorMessage = sendErr.Error()
			result = errors.Join(result, fmt.Errorf("deliver notification %s: %w", delivery.ID, sendErr))
		}
		if recordErr := a.store.RecordNotificationDelivery(
			ctx, delivery.ID, sendErr == nil, status, errorMessage, time.Now().UTC(),
		); recordErr != nil {
			result = errors.Join(result, recordErr)
		}
	}
	return result
}

func (a *App) deliverNotification(
	ctx context.Context,
	kind, notifierID string,
	ciphertext, payload []byte,
) (int, error) {
	secret, err := a.openNotifierSecret(notifierID, ciphertext)
	if err != nil {
		return 0, err
	}
	endpoint := secret.URL
	body := payload
	contentType := "application/json"
	var alertPayload struct {
		Event    string `json:"event"`
		NodeName string `json:"node_name"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
	}
	_ = json.Unmarshal(payload, &alertPayload)
	text := fmt.Sprintf("[PolyFleet] %s · %s · %s", alertPayload.Event, alertPayload.NodeName, alertPayload.Message)
	switch kind {
	case "telegram":
		endpoint = "https://api.telegram.org/bot" + secret.BotToken + "/sendMessage"
		body, _ = json.Marshal(map[string]any{
			"chat_id": secret.ChatID, "text": text, "disable_web_page_preview": true,
		})
	case "slack":
		body, _ = json.Marshal(map[string]string{"text": text})
	case "webhook":
	default:
		return 0, errors.New("notification kind is unsupported")
	}
	parsed, err := validateWebhookURL(endpoint)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "PolyFleet-Notifier/1")
	client := a.notifierClient
	if client == nil {
		client = safeWebhookClient()
	}
	response, err := client.Do(request)
	if err != nil {
		// The Telegram bot token is part of the request path. Never persist the
		// transport error because net/http includes the full URL in that string.
		return 0, errors.New("notification transport failed")
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if kind == "telegram" {
			return response.StatusCode, telegramResponseError(response.StatusCode, responseBody)
		}
		return response.StatusCode, fmt.Errorf("notification endpoint returned HTTP %s", strconv.Itoa(response.StatusCode))
	}
	return response.StatusCode, nil
}

func telegramResponseError(status int, responseBody []byte) error {
	var payload struct {
		Description string `json:"description"`
	}
	_ = json.Unmarshal(responseBody, &payload)
	description := strings.ToLower(payload.Description)
	switch {
	case strings.Contains(description, "chat not found"):
		return errors.New("Telegram 找不到目标会话；请填写数字 Chat ID 或可写频道的 @username，并先让机器人加入目标")
	case strings.Contains(description, "bot was blocked"):
		return errors.New("Telegram 机器人已被目标用户屏蔽；请解除屏蔽并先向机器人发送 /start")
	case strings.Contains(description, "can't initiate conversation"):
		return errors.New("Telegram 机器人不能主动发起私聊；请先向机器人发送 /start")
	case strings.Contains(description, "not enough rights"), strings.Contains(description, "not a member of the channel"):
		return errors.New("Telegram 机器人没有向目标会话发消息的权限；请将其加入目标并授予发言权限")
	case status == http.StatusUnauthorized:
		return errors.New("Telegram Bot Token 无效或已被撤销；请在 BotFather 重新生成")
	case status == http.StatusForbidden:
		return errors.New("Telegram 拒绝发送；请确认机器人未被屏蔽，并已加入目标且拥有发言权限")
	case status == http.StatusBadRequest:
		return errors.New("Telegram 无法识别目标会话；请检查 Chat ID，并确认它不是机器人的用户名")
	default:
		return fmt.Errorf("Telegram 通知接口返回 HTTP %d", status)
	}
}

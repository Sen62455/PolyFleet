package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/store"
	"github.com/go-chi/chi/v5"
)

type notificationReminderRuleRequest struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	NotifierID       string   `json:"notifier_id"`
	Kind             string   `json:"kind"`
	Enabled          *bool    `json:"enabled"`
	IntervalMinutes  int      `json:"interval_minutes"`
	LeadDays         int      `json:"lead_days"`
	ThresholdPercent int      `json:"threshold_percent"`
	NodeIDs          []string `json:"node_ids"`
}

type notificationReminderRuleResponse struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	NotifierID       string     `json:"notifier_id"`
	NotifierName     string     `json:"notifier_name"`
	Kind             string     `json:"kind"`
	Enabled          bool       `json:"enabled"`
	IntervalMinutes  int        `json:"interval_minutes"`
	LeadDays         int        `json:"lead_days"`
	ThresholdPercent int        `json:"threshold_percent"`
	NodeIDs          []string   `json:"node_ids"`
	LastRunAt        *time.Time `json:"last_run_at"`
	LastSuccessAt    *time.Time `json:"last_success_at"`
	LastResult       string     `json:"last_result"`
	LastError        string     `json:"last_error"`
	NextRunAt        time.Time  `json:"next_run_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type telegramBotAccessResponse struct {
	NotifierID   string     `json:"notifier_id"`
	NotifierName string     `json:"notifier_name"`
	Enabled      bool       `json:"enabled"`
	LastPollAt   *time.Time `json:"last_poll_at"`
	LastError    string     `json:"last_error"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

func presentNotificationReminderRule(rule store.NotificationReminderRule) notificationReminderRuleResponse {
	return notificationReminderRuleResponse{
		ID: rule.ID, Name: rule.Name, NotifierID: rule.NotifierID,
		NotifierName: rule.NotifierName, Kind: rule.Kind, Enabled: rule.Enabled,
		IntervalMinutes: rule.IntervalMinutes, LeadDays: rule.LeadDays,
		ThresholdPercent: rule.ThresholdPercent, NodeIDs: rule.NodeIDs,
		LastRunAt: rule.LastRunAt, LastSuccessAt: rule.LastSuccessAt,
		LastResult: rule.LastResult, LastError: rule.LastError,
		NextRunAt: rule.NextRunAt, CreatedAt: rule.CreatedAt, UpdatedAt: rule.UpdatedAt,
	}
}

func presentTelegramBotAccess(access store.TelegramBotAccess) telegramBotAccessResponse {
	return telegramBotAccessResponse{
		NotifierID: access.NotifierID, NotifierName: access.NotifierName,
		Enabled: access.Enabled, LastPollAt: access.LastPollAt,
		LastError: access.LastError, UpdatedAt: access.UpdatedAt,
	}
}

func (a *App) handlePutNotificationReminderRule(response http.ResponseWriter, request *http.Request) {
	var input notificationReminderRuleRequest
	if err := decodeJSON(response, request, &input, 32*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid reminder rule")
		return
	}
	if strings.TrimSpace(input.ID) == "" {
		input.ID = cryptoutil.NewID()
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	rule, err := a.store.UpsertNotificationReminderRule(request.Context(), store.NotificationReminderRule{
		ID: input.ID, Name: input.Name, NotifierID: input.NotifierID,
		Kind: input.Kind, Enabled: enabled, IntervalMinutes: input.IntervalMinutes,
		LeadDays: input.LeadDays, ThresholdPercent: input.ThresholdPercent,
		NodeIDs: input.NodeIDs,
	}, time.Now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "reminder_target_not_found", "notification channel or node not found")
		return
	}
	if errors.Is(err, store.ErrUnsupported) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "validation_failed", "invalid reminder rule fields")
		return
	}
	if err != nil {
		a.logger.Error("save notification reminder rule failed", "error", err)
		a.writeError(response, request, http.StatusInternalServerError, "reminder_save_failed", "could not save reminder rule")
		return
	}
	writeJSON(response, http.StatusOK, presentNotificationReminderRule(rule))
}

func (a *App) handleDeleteNotificationReminderRule(response http.ResponseWriter, request *http.Request) {
	if err := a.store.DeleteNotificationReminderRule(request.Context(), chi.URLParam(request, "ruleID")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			a.writeError(response, request, http.StatusNotFound, "reminder_not_found", "reminder rule not found")
			return
		}
		a.writeError(response, request, http.StatusInternalServerError, "reminder_delete_failed", "could not delete reminder rule")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (a *App) handleRunNotificationReminderRule(response http.ResponseWriter, request *http.Request) {
	rule, err := a.store.GetNotificationReminderRule(request.Context(), chi.URLParam(request, "ruleID"))
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "reminder_not_found", "reminder rule not found")
		return
	}
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "reminder_read_failed", "could not read reminder rule")
		return
	}
	now := time.Now().UTC()
	sent, resultText, runErr := a.executeNotificationReminder(request.Context(), rule, now)
	errorMessage := ""
	if runErr != nil {
		errorMessage = runErr.Error()
	}
	if recordErr := a.store.RecordNotificationReminderRun(
		request.Context(), rule.ID, resultText, errorMessage, runErr == nil, now,
	); recordErr != nil {
		a.logger.Error("record manual reminder run failed", "error", recordErr)
	}
	if runErr != nil {
		a.writeError(response, request, http.StatusBadGateway, "reminder_run_failed", runErr.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"sent": sent, "result": resultText})
}

func (a *App) handlePutTelegramBotAccess(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(response, request, &input, 4*1024); err != nil {
		a.writeError(response, request, http.StatusBadRequest, "invalid_request", "invalid Telegram bot setting")
		return
	}
	notifier, err := a.store.GetNotificationNotifier(request.Context(), chi.URLParam(request, "notifierID"))
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(response, request, http.StatusNotFound, "notifier_not_found", "notifier not found")
		return
	}
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "notifier_read_failed", "could not read notifier")
		return
	}
	baselineOffset := int64(0)
	if input.Enabled {
		secret, err := a.openNotifierSecret(notifier.ID, notifier.ConfigCiphertext)
		if err != nil {
			a.writeError(response, request, http.StatusUnprocessableEntity, "telegram_config_invalid", err.Error())
			return
		}
		if _, err := strconv.ParseInt(secret.ChatID, 10, 64); err != nil {
			a.writeError(response, request, http.StatusUnprocessableEntity, "telegram_chat_not_interactive", "Bot 查询只支持数字 Chat ID 的私聊或群组")
			return
		}
		updates, err := a.fetchTelegramUpdates(request.Context(), secret, -1, 1)
		if err != nil {
			a.writeError(response, request, http.StatusBadGateway, "telegram_poll_failed", err.Error())
			return
		}
		for _, update := range updates {
			if update.UpdateID >= baselineOffset {
				baselineOffset = update.UpdateID + 1
			}
		}
	}
	access, err := a.store.UpsertTelegramBotAccess(request.Context(), notifier.ID, input.Enabled, time.Now().UTC())
	if errors.Is(err, store.ErrUnsupported) {
		a.writeError(response, request, http.StatusUnprocessableEntity, "telegram_required", "Bot 查询仅支持 Telegram 通道")
		return
	}
	if err != nil {
		a.writeError(response, request, http.StatusInternalServerError, "telegram_bot_save_failed", "could not save Telegram bot setting")
		return
	}
	if input.Enabled {
		now := time.Now().UTC()
		if err := a.store.RecordTelegramBotPoll(request.Context(), notifier.ID, baselineOffset, "", now); err != nil {
			a.writeError(response, request, http.StatusInternalServerError, "telegram_bot_save_failed", "could not initialize Telegram bot offset")
			return
		}
		items, listErr := a.store.ListTelegramBotAccess(request.Context())
		if listErr == nil {
			for _, item := range items {
				if item.NotifierID == notifier.ID {
					access = item
					break
				}
			}
		}
	}
	writeJSON(response, http.StatusOK, presentTelegramBotAccess(access))
}

func (a *App) DispatchNotificationReminders(ctx context.Context, limit int) error {
	now := time.Now().UTC()
	rules, err := a.store.ListDueNotificationReminderRules(ctx, now, limit)
	if err != nil {
		return err
	}
	var result error
	for _, rule := range rules {
		_, resultText, runErr := a.executeNotificationReminder(ctx, rule, now)
		errorMessage := ""
		if runErr != nil {
			errorMessage = runErr.Error()
			result = errors.Join(result, fmt.Errorf("run reminder %s: %w", rule.ID, runErr))
		}
		if recordErr := a.store.RecordNotificationReminderRun(
			ctx, rule.ID, resultText, errorMessage, runErr == nil, now,
		); recordErr != nil {
			result = errors.Join(result, recordErr)
		}
	}
	return result
}

func (a *App) executeNotificationReminder(
	ctx context.Context,
	rule store.NotificationReminderRule,
	now time.Time,
) (bool, string, error) {
	message, resultText, shouldSend, err := a.renderNotificationReminder(ctx, rule, now)
	if err != nil {
		return false, "生成提醒失败", err
	}
	if !shouldSend {
		return false, resultText, nil
	}
	notifier, err := a.store.GetNotificationNotifier(ctx, rule.NotifierID)
	if err != nil {
		return false, "读取通知通道失败", err
	}
	if !notifier.Enabled {
		return false, "通知通道已停用", errors.New("notification channel is disabled")
	}
	payload, _ := json.Marshal(map[string]any{
		"event": "reminder", "node_name": rule.Name, "severity": "info",
		"message": truncateNotificationText(message, 3500), "occurred_at": now,
	})
	if _, err := a.deliverNotification(
		ctx, notifier.Kind, notifier.ID, notifier.ConfigCiphertext, payload,
	); err != nil {
		return false, "发送失败", err
	}
	return true, resultText, nil
}

func (a *App) renderNotificationReminder(
	ctx context.Context,
	rule store.NotificationReminderRule,
	now time.Time,
) (string, string, bool, error) {
	nodes, err := a.store.ListNodes(ctx)
	if err != nil {
		return "", "", false, err
	}
	wanted := make(map[string]bool, len(rule.NodeIDs))
	for _, nodeID := range rule.NodeIDs {
		wanted[nodeID] = true
	}
	filtered := make([]store.Node, 0, len(nodes))
	for _, node := range nodes {
		if len(wanted) == 0 || wanted[node.ID] {
			filtered = append(filtered, node)
		}
	}
	switch rule.Kind {
	case "fleet_summary":
		alerts, _, err := a.store.ListAlertsPage(ctx, store.AlertFilter{Status: "active", Limit: 200})
		if err != nil {
			return "", "", false, err
		}
		alertCount := 0
		for _, alert := range alerts {
			if len(wanted) == 0 || wanted[alert.NodeID] {
				alertCount++
			}
		}
		online, abnormal, users := 0, 0, 0
		var used, limit int64
		for _, node := range filtered {
			if node.Status == "online" {
				online++
			} else if node.Enabled {
				abnormal++
			}
			users += node.OnlineUsers
			used += nodeTrafficUsed(node)
			limit += node.TrafficLimitBytes
		}
		message := fmt.Sprintf(
			"运行概览 · %s\n节点：%d 在线 / %d 总计 / %d 异常\n活动告警：%d\n在线用户：%d\n本期双向流量：%s%s",
			now.Format("2006-01-02 15:04 UTC"), online, len(filtered), abnormal,
			alertCount, users, formatByteSize(used), formatTrafficLimit(limit),
		)
		return message, fmt.Sprintf("已发送 %d 个节点的运行概览", len(filtered)), true, nil

	case "active_alerts":
		alerts, _, err := a.store.ListAlertsPage(ctx, store.AlertFilter{Status: "active", Limit: 200})
		if err != nil {
			return "", "", false, err
		}
		lines := make([]string, 0)
		for _, alert := range alerts {
			if alert.Status != "open" || (len(wanted) > 0 && !wanted[alert.NodeID]) {
				continue
			}
			lines = append(lines, fmt.Sprintf("• %s：%s（%s）", alert.NodeName, alertTypeLabel(alert.Type), alert.Message))
			if len(lines) == 20 {
				break
			}
		}
		if len(lines) == 0 {
			return "", "当前没有活动告警，未发送", false, nil
		}
		return "活动告警提醒\n" + strings.Join(lines, "\n"), fmt.Sprintf("已发送 %d 项活动告警", len(lines)), true, nil

	case "asset_expiry":
		assets, err := a.store.ListNodeAssets(ctx)
		if err != nil {
			return "", "", false, err
		}
		deadline := now.AddDate(0, 0, rule.LeadDays)
		lines := make([]string, 0)
		for _, asset := range assets {
			if asset.ExpiresAt == nil || (len(wanted) > 0 && !wanted[asset.NodeID]) || asset.ExpiresAt.After(deadline) {
				continue
			}
			days := int(asset.ExpiresAt.Sub(now).Hours() / 24)
			state := fmt.Sprintf("剩余 %d 天", days)
			if asset.ExpiresAt.Before(now) {
				state = fmt.Sprintf("已过期 %d 天", -days)
			} else if asset.AutoRenew {
				state += "，自动续费"
			}
			lines = append(lines, fmt.Sprintf("• %s：%s（%s）", asset.NodeName, asset.ExpiresAt.Format("2006-01-02"), state))
		}
		if len(lines) == 0 {
			return "", fmt.Sprintf("未来 %d 天没有节点到期，未发送", rule.LeadDays), false, nil
		}
		return fmt.Sprintf("VPS 到期提醒（%d 天内）\n%s", rule.LeadDays, strings.Join(lines, "\n")), fmt.Sprintf("已发送 %d 台 VPS 的到期提醒", len(lines)), true, nil

	case "traffic_usage":
		lines := make([]string, 0)
		for _, node := range filtered {
			if node.TrafficLimitBytes <= 0 {
				continue
			}
			used := nodeTrafficUsed(node)
			percent := int(float64(used) / float64(node.TrafficLimitBytes) * 100)
			if percent >= rule.ThresholdPercent {
				lines = append(lines, fmt.Sprintf("• %s：%d%%（%s / %s）", node.Name, percent, formatByteSize(used), formatByteSize(node.TrafficLimitBytes)))
			}
		}
		if len(lines) == 0 {
			return "", fmt.Sprintf("没有节点达到 %d%% 流量阈值，未发送", rule.ThresholdPercent), false, nil
		}
		return fmt.Sprintf("节点流量提醒（阈值 %d%%）\n%s", rule.ThresholdPercent, strings.Join(lines, "\n")), fmt.Sprintf("已发送 %d 个节点的流量提醒", len(lines)), true, nil
	default:
		return "", "", false, store.ErrUnsupported
	}
}

type telegramUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

func (a *App) PollTelegramBots(ctx context.Context, limit int) error {
	accesses, err := a.store.ListTelegramBotAccess(ctx)
	if err != nil {
		return err
	}
	var result error
	for _, access := range accesses {
		if !access.Enabled || !access.Notifier.Enabled {
			continue
		}
		secret, secretErr := a.openNotifierSecret(access.NotifierID, access.Notifier.ConfigCiphertext)
		if secretErr != nil {
			_ = a.store.RecordTelegramBotPoll(ctx, access.NotifierID, access.UpdateOffset, secretErr.Error(), time.Now().UTC())
			result = errors.Join(result, secretErr)
			continue
		}
		allowedChatID, parseErr := strconv.ParseInt(secret.ChatID, 10, 64)
		if parseErr != nil {
			err := errors.New("interactive Telegram channel requires a numeric Chat ID")
			_ = a.store.RecordTelegramBotPoll(ctx, access.NotifierID, access.UpdateOffset, err.Error(), time.Now().UTC())
			result = errors.Join(result, err)
			continue
		}
		updates, fetchErr := a.fetchTelegramUpdates(ctx, secret, access.UpdateOffset, limit)
		if fetchErr != nil {
			_ = a.store.RecordTelegramBotPoll(ctx, access.NotifierID, access.UpdateOffset, fetchErr.Error(), time.Now().UTC())
			result = errors.Join(result, fetchErr)
			continue
		}
		offset := access.UpdateOffset
		lastError := ""
		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			if update.Message == nil || update.Message.Chat.ID != allowedChatID || strings.TrimSpace(update.Message.Text) == "" {
				continue
			}
			reply, replyErr := a.telegramBotReply(ctx, update.Message.Text, time.Now().UTC())
			if replyErr != nil {
				lastError = "could not build Telegram bot reply"
				result = errors.Join(result, replyErr)
				continue
			}
			payload, _ := json.Marshal(map[string]any{
				"event": "bot", "node_name": "查询", "severity": "info",
				"message": truncateNotificationText(reply, 3500),
			})
			if _, sendErr := a.deliverNotification(
				ctx, access.Notifier.Kind, access.Notifier.ID,
				access.Notifier.ConfigCiphertext, payload,
			); sendErr != nil {
				lastError = sendErr.Error()
				result = errors.Join(result, sendErr)
			}
		}
		if recordErr := a.store.RecordTelegramBotPoll(ctx, access.NotifierID, offset, lastError, time.Now().UTC()); recordErr != nil {
			result = errors.Join(result, recordErr)
		}
	}
	return result
}

func (a *App) fetchTelegramUpdates(
	ctx context.Context,
	secret notifierSecretConfig,
	offset int64,
	limit int,
) ([]telegramUpdate, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	endpoint := "https://api.telegram.org/bot" + secret.BotToken + "/getUpdates"
	parsed, err := validateWebhookURL(endpoint)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{
		"offset": offset, "limit": limit, "timeout": 0,
		"allowed_updates": []string{"message"},
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("could not create Telegram poll request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "PolyFleet-Notifier/1")
	client := a.notifierClient
	if client == nil {
		client = safeWebhookClient()
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("Telegram bot polling transport failed")
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 256*1024))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, telegramResponseError(response.StatusCode, responseBody)
	}
	var payload struct {
		OK     bool             `json:"ok"`
		Result []telegramUpdate `json:"result"`
	}
	if err := json.Unmarshal(responseBody, &payload); err != nil || !payload.OK {
		return nil, errors.New("Telegram bot polling returned an invalid response")
	}
	return payload.Result, nil
}

func (a *App) telegramBotReply(ctx context.Context, raw string, now time.Time) (string, error) {
	text := strings.TrimSpace(raw)
	parts := strings.Fields(text)
	command := ""
	argument := ""
	if len(parts) > 0 {
		command = strings.ToLower(strings.Split(parts[0], "@")[0])
	}
	if len(parts) > 1 {
		argument = strings.TrimSpace(strings.Join(parts[1:], " "))
	}
	switch command {
	case "/start", "/help":
		return "可用命令：\n/status 全局状态\n/nodes 节点列表\n/node 节点名 节点详情\n也可以直接发送节点名称。", nil
	case "/status":
		rule := store.NotificationReminderRule{Kind: "fleet_summary"}
		message, _, _, err := a.renderNotificationReminder(ctx, rule, now)
		return message, err
	case "/nodes":
		nodes, err := a.store.ListNodes(ctx)
		if err != nil {
			return "", err
		}
		lines := []string{"节点列表"}
		for _, node := range nodes {
			lines = append(lines, fmt.Sprintf("• %s：%s，CPU %.0f%%，在线用户 %d", node.Name, nodeStatusLabel(node.Status), node.CPUPercent, node.OnlineUsers))
		}
		return strings.Join(lines, "\n"), nil
	case "/node":
		if argument == "" {
			return "请使用 /node 节点名，例如 /node dmit2-reality", nil
		}
		return a.renderTelegramNodeDetail(ctx, argument, now)
	default:
		if strings.HasPrefix(command, "/") {
			return "未知命令。发送 /help 查看可用命令。", nil
		}
		return a.renderTelegramNodeDetail(ctx, text, now)
	}
}

func (a *App) renderTelegramNodeDetail(ctx context.Context, query string, now time.Time) (string, error) {
	nodes, err := a.store.ListNodes(ctx)
	if err != nil {
		return "", err
	}
	wanted := strings.ToLower(strings.TrimSpace(query))
	matches := make([]store.Node, 0)
	for _, node := range nodes {
		name := strings.ToLower(node.Name)
		if strings.EqualFold(node.ID, wanted) || name == wanted {
			matches = []store.Node{node}
			break
		}
		if strings.Contains(name, wanted) {
			matches = append(matches, node)
		}
	}
	if len(matches) == 0 {
		return fmt.Sprintf("没有找到节点“%s”。发送 /nodes 查看节点列表。", query), nil
	}
	if len(matches) > 1 {
		names := make([]string, 0, len(matches))
		for _, node := range matches {
			names = append(names, node.Name)
		}
		return "找到多个节点，请输入更完整的名称：\n• " + strings.Join(names, "\n• "), nil
	}
	node := matches[0]
	assetText := "未设置"
	assets, err := a.store.ListNodeAssets(ctx)
	if err == nil {
		for _, asset := range assets {
			if asset.NodeID == node.ID && asset.ExpiresAt != nil {
				assetText = asset.ExpiresAt.Format("2006-01-02")
				break
			}
		}
	}
	lastSeen := "尚未连接"
	if node.LastSeenAt != nil {
		lastSeen = relativeDuration(now.Sub(*node.LastSeenAt))
	}
	traffic := formatByteSize(nodeTrafficUsed(node)) + formatTrafficLimit(node.TrafficLimitBytes)
	memory := percentOf(node.MemoryUsedBytes, node.MemoryTotalBytes)
	disk := percentOf(node.DiskUsedBytes, node.DiskTotalBytes)
	return fmt.Sprintf(
		"节点详情 · %s\n状态：%s%s\n位置：%s / %s\n协议：%s\nAgent：%s\n核心：%s\nCPU：%.1f%%  内存：%.1f%%  磁盘：%.1f%%\n负载：%.2f / %.2f / %.2f\n网络：↓ %s  ↑ %s\n在线：%d 用户 / %d 连接\n本期流量：%s\nVPS 到期：%s",
		node.Name, nodeStatusLabel(node.Status), statusReasonSuffix(node.StatusReason),
		node.Provider, node.Region, node.AdapterType, lastSeen,
		map[bool]string{true: "运行中", false: "已停止"}[node.CoreRunning],
		node.CPUPercent, memory, disk, node.Load1, node.Load5, node.Load15,
		formatBitRate(node.NetworkRXBPS), formatBitRate(node.NetworkTXBPS),
		node.OnlineUsers, node.OnlineConnections, traffic, assetText,
	), nil
}

func nodeTrafficUsed(node store.Node) int64 {
	proxy := node.TrafficCycleUploadBytes + node.TrafficCycleDownloadBytes
	if node.TrafficCalibrationBytes == nil || node.TrafficCalibrationProxyBytes == nil {
		return proxy
	}
	delta := proxy - *node.TrafficCalibrationProxyBytes
	if delta < 0 {
		delta = 0
	}
	return *node.TrafficCalibrationBytes + delta
}

func formatByteSize(value int64) string {
	if value <= 0 {
		return "0 B"
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	scaled := float64(value)
	unit := 0
	for scaled >= 1024 && unit < len(units)-1 {
		scaled /= 1024
		unit++
	}
	return fmt.Sprintf("%.1f %s", scaled, units[unit])
}

func formatTrafficLimit(limit int64) string {
	if limit <= 0 {
		return "（未设置配额）"
	}
	return " / " + formatByteSize(limit)
}

func formatBitRate(value int64) string {
	if value <= 0 {
		return "0 bps"
	}
	units := []string{"bps", "Kbps", "Mbps", "Gbps"}
	scaled := float64(value)
	unit := 0
	for scaled >= 1000 && unit < len(units)-1 {
		scaled /= 1000
		unit++
	}
	return fmt.Sprintf("%.1f %s", scaled, units[unit])
}

func percentOf(used, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

func relativeDuration(duration time.Duration) string {
	if duration < time.Minute {
		return "刚刚"
	}
	if duration < time.Hour {
		return fmt.Sprintf("%d 分钟前", int(duration.Minutes()))
	}
	if duration < 24*time.Hour {
		return fmt.Sprintf("%d 小时前", int(duration.Hours()))
	}
	return fmt.Sprintf("%d 天前", int(duration.Hours()/24))
}

func nodeStatusLabel(status string) string {
	labels := map[string]string{
		"pending": "待连接", "online": "在线", "stale": "数据延迟",
		"offline": "离线", "degraded": "异常", "disabled": "已停用",
	}
	if label := labels[status]; label != "" {
		return label
	}
	return status
}

func alertTypeLabel(alertType string) string {
	labels := map[string]string{
		"offline": "节点离线", "degraded": "节点异常", "core_down": "核心停止",
		"usage_error": "用量采集异常", "sync_failed": "配置同步失败",
		"sync_stuck": "配置同步超时", "operation_failed": "运维操作失败",
		"traffic_quota_warning": "流量达到 80%", "traffic_quota_exhausted": "流量已用尽",
	}
	if label := labels[alertType]; label != "" {
		return label
	}
	return alertType
}

func statusReasonSuffix(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ""
	}
	return "（" + reason + "）"
}

func truncateNotificationText(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes-1]) + "…"
}

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type NotificationReminderRule struct {
	ID               string
	Name             string
	NotifierID       string
	NotifierName     string
	Kind             string
	Enabled          bool
	IntervalMinutes  int
	LeadDays         int
	ThresholdPercent int
	NodeIDs          []string
	LastRunAt        *time.Time
	LastSuccessAt    *time.Time
	LastResult       string
	LastError        string
	NextRunAt        time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type TelegramBotAccess struct {
	NotifierID   string
	NotifierName string
	Enabled      bool
	UpdateOffset int64
	LastPollAt   *time.Time
	LastError    string
	UpdatedAt    time.Time
	Notifier     NotificationNotifier
}

const reminderRuleColumns = `
	r.id, r.name, r.notifier_id, n.name, r.kind, r.enabled,
	r.interval_minutes, r.lead_days, r.threshold_percent, r.node_ids,
	r.last_run_at, r.last_success_at, r.last_result, r.last_error,
	r.next_run_at, r.created_at, r.updated_at
`

func scanNotificationReminderRule(row rowScanner) (NotificationReminderRule, error) {
	var rule NotificationReminderRule
	var enabled int
	var nodeIDs string
	var lastRunAt, lastSuccessAt sql.NullInt64
	var nextRunAt, createdAt, updatedAt int64
	if err := row.Scan(
		&rule.ID, &rule.Name, &rule.NotifierID, &rule.NotifierName, &rule.Kind,
		&enabled, &rule.IntervalMinutes, &rule.LeadDays, &rule.ThresholdPercent,
		&nodeIDs, &lastRunAt, &lastSuccessAt, &rule.LastResult, &rule.LastError,
		&nextRunAt, &createdAt, &updatedAt,
	); err != nil {
		return NotificationReminderRule{}, err
	}
	if err := json.Unmarshal([]byte(nodeIDs), &rule.NodeIDs); err != nil {
		return NotificationReminderRule{}, fmt.Errorf("decode reminder node IDs: %w", err)
	}
	rule.Enabled = enabled == 1
	rule.LastRunAt = nullableTime(lastRunAt)
	rule.LastSuccessAt = nullableTime(lastSuccessAt)
	rule.NextRunAt = unixTime(nextRunAt)
	rule.CreatedAt = unixTime(createdAt)
	rule.UpdatedAt = unixTime(updatedAt)
	return rule, nil
}

func (s *Store) ListNotificationReminderRules(ctx context.Context) ([]NotificationReminderRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+reminderRuleColumns+`
		FROM notification_reminder_rules r
		JOIN notification_notifiers n ON n.id = r.notifier_id
		ORDER BY r.name COLLATE NOCASE, r.id
	`)
	if err != nil {
		return nil, fmt.Errorf("list notification reminder rules: %w", err)
	}
	defer rows.Close()
	result := make([]NotificationReminderRule, 0)
	for rows.Next() {
		rule, err := scanNotificationReminderRule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan notification reminder rule: %w", err)
		}
		result = append(result, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification reminder rules: %w", err)
	}
	return result, nil
}

func (s *Store) GetNotificationReminderRule(ctx context.Context, id string) (NotificationReminderRule, error) {
	rule, err := scanNotificationReminderRule(s.db.QueryRowContext(ctx, `
		SELECT `+reminderRuleColumns+`
		FROM notification_reminder_rules r
		JOIN notification_notifiers n ON n.id = r.notifier_id
		WHERE r.id = ?
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return NotificationReminderRule{}, ErrNotFound
	}
	if err != nil {
		return NotificationReminderRule{}, fmt.Errorf("get notification reminder rule: %w", err)
	}
	return rule, nil
}

func normalizeReminderRule(rule NotificationReminderRule) (NotificationReminderRule, error) {
	rule.ID = strings.TrimSpace(rule.ID)
	rule.Name = strings.TrimSpace(rule.Name)
	rule.NotifierID = strings.TrimSpace(rule.NotifierID)
	rule.Kind = strings.TrimSpace(strings.ToLower(rule.Kind))
	if rule.ID == "" || rule.Name == "" || len(rule.Name) > 80 || rule.NotifierID == "" {
		return rule, ErrUnsupported
	}
	switch rule.Kind {
	case "fleet_summary", "active_alerts", "asset_expiry", "traffic_usage":
	default:
		return rule, ErrUnsupported
	}
	if rule.IntervalMinutes < 15 || rule.IntervalMinutes > 10080 ||
		rule.LeadDays < 0 || rule.LeadDays > 365 ||
		rule.ThresholdPercent < 1 || rule.ThresholdPercent > 100 {
		return rule, ErrUnsupported
	}
	wanted := make(map[string]bool, len(rule.NodeIDs))
	nodeIDs := make([]string, 0, len(rule.NodeIDs))
	for _, nodeID := range rule.NodeIDs {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID != "" && !wanted[nodeID] {
			wanted[nodeID] = true
			nodeIDs = append(nodeIDs, nodeID)
		}
	}
	rule.NodeIDs = nodeIDs
	return rule, nil
}

func (s *Store) UpsertNotificationReminderRule(
	ctx context.Context,
	rule NotificationReminderRule,
	now time.Time,
) (NotificationReminderRule, error) {
	rule, err := normalizeReminderRule(rule)
	if err != nil {
		return NotificationReminderRule{}, err
	}
	var notifierKind string
	if err := s.db.QueryRowContext(ctx,
		"SELECT kind FROM notification_notifiers WHERE id = ?", rule.NotifierID,
	).Scan(&notifierKind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NotificationReminderRule{}, ErrNotFound
		}
		return NotificationReminderRule{}, fmt.Errorf("find reminder notifier: %w", err)
	}
	for _, nodeID := range rule.NodeIDs {
		var exists int
		if err := s.db.QueryRowContext(ctx,
			"SELECT 1 FROM nodes WHERE id = ? AND archived_at IS NULL", nodeID,
		).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return NotificationReminderRule{}, ErrNotFound
			}
			return NotificationReminderRule{}, fmt.Errorf("validate reminder node: %w", err)
		}
	}
	encodedNodeIDs, err := json.Marshal(rule.NodeIDs)
	if err != nil {
		return NotificationReminderRule{}, fmt.Errorf("encode reminder node IDs: %w", err)
	}
	nextRunAt := now.Add(time.Duration(rule.IntervalMinutes) * time.Minute)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_reminder_rules(
			id, name, notifier_id, kind, enabled, interval_minutes, lead_days,
			threshold_percent, node_ids, next_run_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			notifier_id = excluded.notifier_id,
			kind = excluded.kind,
			enabled = excluded.enabled,
			next_run_at = CASE
				WHEN notification_reminder_rules.interval_minutes <> excluded.interval_minutes
				THEN excluded.next_run_at
				ELSE notification_reminder_rules.next_run_at
			END,
			interval_minutes = excluded.interval_minutes,
			lead_days = excluded.lead_days,
			threshold_percent = excluded.threshold_percent,
			node_ids = excluded.node_ids,
			updated_at = excluded.updated_at
	`, rule.ID, rule.Name, rule.NotifierID, rule.Kind, boolInt(rule.Enabled),
		rule.IntervalMinutes, rule.LeadDays, rule.ThresholdPercent, string(encodedNodeIDs),
		nextRunAt.UnixMilli(), now.UnixMilli(), now.UnixMilli()); err != nil {
		return NotificationReminderRule{}, fmt.Errorf("upsert notification reminder rule: %w", err)
	}
	return s.GetNotificationReminderRule(ctx, rule.ID)
}

func (s *Store) DeleteNotificationReminderRule(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM notification_reminder_rules WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete notification reminder rule: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read reminder rule deletion: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListDueNotificationReminderRules(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]NotificationReminderRule, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+reminderRuleColumns+`
		FROM notification_reminder_rules r
		JOIN notification_notifiers n ON n.id = r.notifier_id
		WHERE r.enabled = 1 AND n.enabled = 1 AND r.next_run_at <= ?
		ORDER BY r.next_run_at, r.created_at LIMIT ?
	`, now.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("list due notification reminder rules: %w", err)
	}
	defer rows.Close()
	result := make([]NotificationReminderRule, 0)
	for rows.Next() {
		rule, err := scanNotificationReminderRule(rows)
		if err != nil {
			return nil, fmt.Errorf("scan due notification reminder rule: %w", err)
		}
		result = append(result, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due notification reminder rules: %w", err)
	}
	return result, nil
}

func (s *Store) RecordNotificationReminderRun(
	ctx context.Context,
	id, resultText, errorMessage string,
	success bool,
	now time.Time,
) error {
	if len(resultText) > 160 {
		resultText = resultText[:160]
	}
	if len(errorMessage) > 512 {
		errorMessage = errorMessage[:512]
	}
	nextExpression := "? + interval_minutes * 60000"
	nextBase := now.UnixMilli()
	if !success {
		nextExpression = "? + 300000"
	}
	query := `
		UPDATE notification_reminder_rules
		SET last_run_at = ?,
			last_success_at = CASE WHEN ? THEN ? ELSE last_success_at END,
			last_result = ?, last_error = ?,
			next_run_at = ` + nextExpression + `,
			updated_at = ?
		WHERE id = ?
	`
	result, err := s.db.ExecContext(ctx, query,
		now.UnixMilli(), success, now.UnixMilli(), resultText, errorMessage,
		nextBase, now.UnixMilli(), id,
	)
	if err != nil {
		return fmt.Errorf("record notification reminder run: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListTelegramBotAccess(ctx context.Context) ([]TelegramBotAccess, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT n.id, n.name, COALESCE(b.enabled, 0), COALESCE(b.update_offset, 0),
		       b.last_poll_at, COALESCE(b.last_error, ''), COALESCE(b.updated_at, n.updated_at),
		       n.kind, n.enabled, n.config_ciphertext, n.target_hint, n.events,
		       n.created_at, n.updated_at
		FROM notification_notifiers n
		LEFT JOIN telegram_bot_access b ON b.notifier_id = n.id
		WHERE n.kind = 'telegram'
		ORDER BY n.name COLLATE NOCASE, n.id
	`)
	if err != nil {
		return nil, fmt.Errorf("list Telegram bot access: %w", err)
	}
	defer rows.Close()
	result := make([]TelegramBotAccess, 0)
	for rows.Next() {
		var access TelegramBotAccess
		var enabled, notifierEnabled int
		var lastPollAt sql.NullInt64
		var accessUpdatedAt, notifierCreatedAt, notifierUpdatedAt int64
		var events string
		if err := rows.Scan(
			&access.NotifierID, &access.NotifierName, &enabled, &access.UpdateOffset,
			&lastPollAt, &access.LastError, &accessUpdatedAt,
			&access.Notifier.Kind, &notifierEnabled, &access.Notifier.ConfigCiphertext,
			&access.Notifier.TargetHint, &events, &notifierCreatedAt, &notifierUpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan Telegram bot access: %w", err)
		}
		access.Enabled = enabled == 1
		access.LastPollAt = nullableTime(lastPollAt)
		access.UpdatedAt = unixTime(accessUpdatedAt)
		access.Notifier.ID = access.NotifierID
		access.Notifier.Name = access.NotifierName
		access.Notifier.Enabled = notifierEnabled == 1
		access.Notifier.Events = strings.Split(events, ",")
		access.Notifier.CreatedAt = unixTime(notifierCreatedAt)
		access.Notifier.UpdatedAt = unixTime(notifierUpdatedAt)
		result = append(result, access)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Telegram bot access: %w", err)
	}
	return result, nil
}

func (s *Store) UpsertTelegramBotAccess(
	ctx context.Context,
	notifierID string,
	enabled bool,
	now time.Time,
) (TelegramBotAccess, error) {
	var kind string
	if err := s.db.QueryRowContext(ctx,
		"SELECT kind FROM notification_notifiers WHERE id = ?", notifierID,
	).Scan(&kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TelegramBotAccess{}, ErrNotFound
		}
		return TelegramBotAccess{}, fmt.Errorf("find Telegram notifier: %w", err)
	}
	if kind != "telegram" {
		return TelegramBotAccess{}, ErrUnsupported
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO telegram_bot_access(notifier_id, enabled, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(notifier_id) DO UPDATE SET
			enabled = excluded.enabled, last_error = '', updated_at = excluded.updated_at
	`, notifierID, boolInt(enabled), now.UnixMilli()); err != nil {
		return TelegramBotAccess{}, fmt.Errorf("upsert Telegram bot access: %w", err)
	}
	items, err := s.ListTelegramBotAccess(ctx)
	if err != nil {
		return TelegramBotAccess{}, err
	}
	for _, item := range items {
		if item.NotifierID == notifierID {
			return item, nil
		}
	}
	return TelegramBotAccess{}, ErrNotFound
}

func (s *Store) RecordTelegramBotPoll(
	ctx context.Context,
	notifierID string,
	updateOffset int64,
	errorMessage string,
	now time.Time,
) error {
	if len(errorMessage) > 512 {
		errorMessage = errorMessage[:512]
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE telegram_bot_access
		SET update_offset = MAX(update_offset, ?), last_poll_at = ?,
			last_error = ?, updated_at = ?
		WHERE notifier_id = ? AND enabled = 1
	`, updateOffset, now.UnixMilli(), errorMessage, now.UnixMilli(), notifierID)
	if err != nil {
		return fmt.Errorf("record Telegram bot poll: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type NotificationNotifier struct {
	ID               string
	Name             string
	Kind             string
	Enabled          bool
	ConfigCiphertext []byte
	TargetHint       string
	Events           []string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type NotificationDelivery struct {
	ID               string
	NotifierID       string
	NotifierName     string
	NotifierKind     string
	ConfigCiphertext []byte
	AlertID          string
	EventType        string
	PayloadJSON      string
	Status           string
	AttemptCount     int
	NextAttemptAt    time.Time
	LastError        string
	ResponseCode     int
	DeliveredAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func normalizeNotificationEvents(events []string) ([]string, error) {
	if len(events) == 0 {
		return []string{"created", "resolved"}, nil
	}
	wanted := make(map[string]bool, len(events))
	for _, event := range events {
		event = strings.TrimSpace(strings.ToLower(event))
		if event != "created" && event != "resolved" {
			return nil, ErrUnsupported
		}
		wanted[event] = true
	}
	result := make([]string, 0, 2)
	for _, event := range []string{"created", "resolved"} {
		if wanted[event] {
			result = append(result, event)
		}
	}
	return result, nil
}

func scanNotificationNotifier(row rowScanner) (NotificationNotifier, error) {
	var notifier NotificationNotifier
	var enabled int
	var events string
	var createdAt, updatedAt int64
	if err := row.Scan(
		&notifier.ID, &notifier.Name, &notifier.Kind, &enabled,
		&notifier.ConfigCiphertext, &notifier.TargetHint, &events, &createdAt, &updatedAt,
	); err != nil {
		return NotificationNotifier{}, err
	}
	notifier.Enabled = enabled == 1
	notifier.Events = strings.Split(events, ",")
	notifier.CreatedAt = unixTime(createdAt)
	notifier.UpdatedAt = unixTime(updatedAt)
	return notifier, nil
}

func (s *Store) ListNotificationNotifiers(ctx context.Context) ([]NotificationNotifier, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, kind, enabled, config_ciphertext, target_hint, events,
		       created_at, updated_at
		FROM notification_notifiers ORDER BY name COLLATE NOCASE, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list notification notifiers: %w", err)
	}
	defer rows.Close()
	result := make([]NotificationNotifier, 0)
	for rows.Next() {
		notifier, err := scanNotificationNotifier(rows)
		if err != nil {
			return nil, fmt.Errorf("scan notification notifier: %w", err)
		}
		result = append(result, notifier)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification notifiers: %w", err)
	}
	return result, nil
}

func (s *Store) GetNotificationNotifier(
	ctx context.Context,
	id string,
) (NotificationNotifier, error) {
	notifier, err := scanNotificationNotifier(s.db.QueryRowContext(ctx, `
		SELECT id, name, kind, enabled, config_ciphertext, target_hint, events,
		       created_at, updated_at
		FROM notification_notifiers WHERE id = ?
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return NotificationNotifier{}, ErrNotFound
	}
	if err != nil {
		return NotificationNotifier{}, fmt.Errorf("get notification notifier: %w", err)
	}
	return notifier, nil
}

func (s *Store) UpsertNotificationNotifier(
	ctx context.Context,
	notifier NotificationNotifier,
	now time.Time,
) (NotificationNotifier, error) {
	events, err := normalizeNotificationEvents(notifier.Events)
	if err != nil {
		return NotificationNotifier{}, err
	}
	if len(notifier.ConfigCiphertext) == 0 {
		return NotificationNotifier{}, errors.New("notifier configuration is empty")
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_notifiers(
			id, name, kind, enabled, config_ciphertext, target_hint, events,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			kind = excluded.kind,
			enabled = excluded.enabled,
			config_ciphertext = excluded.config_ciphertext,
			target_hint = excluded.target_hint,
			events = excluded.events,
			updated_at = excluded.updated_at
	`, notifier.ID, notifier.Name, notifier.Kind, boolInt(notifier.Enabled),
		notifier.ConfigCiphertext, notifier.TargetHint, strings.Join(events, ","),
		now.UnixMilli(), now.UnixMilli()); err != nil {
		return NotificationNotifier{}, fmt.Errorf("upsert notification notifier: %w", err)
	}
	return s.GetNotificationNotifier(ctx, notifier.ID)
}

func (s *Store) DeleteNotificationNotifier(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM notification_notifiers WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete notification notifier: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read notifier deletion: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListNotificationDeliveries(
	ctx context.Context,
	limit int,
) ([]NotificationDelivery, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	return s.listNotificationDeliveries(ctx, `
		WHERE 1 = 1 ORDER BY d.created_at DESC LIMIT ?
	`, limit)
}

func (s *Store) ListDueNotificationDeliveries(
	ctx context.Context,
	now time.Time,
	limit int,
) ([]NotificationDelivery, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	return s.listNotificationDeliveries(ctx, `
		WHERE d.status IN ('queued', 'retry') AND d.next_attempt_at <= ?
		  AND n.enabled = 1
		ORDER BY d.next_attempt_at, d.created_at LIMIT ?
	`, now.UnixMilli(), limit)
}

func (s *Store) listNotificationDeliveries(
	ctx context.Context,
	where string,
	args ...any,
) ([]NotificationDelivery, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.notifier_id, n.name, n.kind, n.config_ciphertext,
		       d.alert_id, d.event_type, d.payload_json, d.status,
		       d.attempt_count, d.next_attempt_at, d.last_error,
		       d.response_code, d.delivered_at, d.created_at, d.updated_at
		FROM notification_deliveries d
		JOIN notification_notifiers n ON n.id = d.notifier_id
	`+where, args...)
	if err != nil {
		return nil, fmt.Errorf("list notification deliveries: %w", err)
	}
	defer rows.Close()
	result := make([]NotificationDelivery, 0)
	for rows.Next() {
		var delivery NotificationDelivery
		var deliveredAt sql.NullInt64
		var nextAttemptAt, createdAt, updatedAt int64
		if err := rows.Scan(
			&delivery.ID, &delivery.NotifierID, &delivery.NotifierName,
			&delivery.NotifierKind, &delivery.ConfigCiphertext, &delivery.AlertID,
			&delivery.EventType, &delivery.PayloadJSON, &delivery.Status,
			&delivery.AttemptCount, &nextAttemptAt, &delivery.LastError,
			&delivery.ResponseCode, &deliveredAt, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan notification delivery: %w", err)
		}
		delivery.NextAttemptAt = unixTime(nextAttemptAt)
		delivery.DeliveredAt = nullableTime(deliveredAt)
		delivery.CreatedAt = unixTime(createdAt)
		delivery.UpdatedAt = unixTime(updatedAt)
		result = append(result, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification deliveries: %w", err)
	}
	return result, nil
}

func (s *Store) RecordNotificationDelivery(
	ctx context.Context,
	id string,
	delivered bool,
	responseCode int,
	errorMessage string,
	now time.Time,
) error {
	if len(errorMessage) > 512 {
		errorMessage = errorMessage[:512]
	}
	if delivered {
		result, err := s.db.ExecContext(ctx, `
			UPDATE notification_deliveries
			SET status = 'delivered', attempt_count = attempt_count + 1,
			    response_code = ?, last_error = '', delivered_at = ?, updated_at = ?
			WHERE id = ? AND status IN ('queued', 'retry')
		`, responseCode, now.UnixMilli(), now.UnixMilli(), id)
		if err != nil {
			return fmt.Errorf("record delivered notification: %w", err)
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			return ErrConflict
		}
		return nil
	}
	var attempts int
	if err := s.db.QueryRowContext(ctx, `
		SELECT attempt_count + 1 FROM notification_deliveries
		WHERE id = ? AND status IN ('queued', 'retry')
	`, id).Scan(&attempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		return fmt.Errorf("read notification attempt: %w", err)
	}
	delays := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}
	status := "retry"
	next := now
	if attempts > len(delays) {
		status = "failed"
	} else {
		next = now.Add(delays[attempts-1])
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE notification_deliveries
		SET status = ?, attempt_count = ?, next_attempt_at = ?, response_code = ?,
		    last_error = ?, updated_at = ?
		WHERE id = ? AND status IN ('queued', 'retry')
	`, status, attempts, next.UnixMilli(), responseCode, errorMessage, now.UnixMilli(), id); err != nil {
		return fmt.Errorf("record failed notification: %w", err)
	}
	return nil
}

func enqueueAlertNotificationTx(
	ctx context.Context,
	tx *sql.Tx,
	alertID, eventType, payloadJSON string,
	now time.Time,
) error {
	if eventType != "created" && eventType != "resolved" {
		return ErrUnsupported
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM notification_notifiers
		WHERE enabled = 1 AND instr(',' || events || ',', ',' || ? || ',') > 0
	`, eventType)
	if err != nil {
		return fmt.Errorf("list alert notification targets: %w", err)
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan alert notification target: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close alert notification targets: %w", err)
	}
	for _, notifierID := range ids {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO notification_deliveries(
				id, notifier_id, alert_id, event_type, payload_json,
				next_attempt_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(notifier_id, alert_id, event_type) DO NOTHING
		`, uuid.NewString(), notifierID, alertID, eventType, payloadJSON,
			now.UnixMilli(), now.UnixMilli(), now.UnixMilli()); err != nil {
			return fmt.Errorf("queue alert notification: %w", err)
		}
	}
	return nil
}

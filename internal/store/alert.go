package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Alert struct {
	ID              string
	NodeID          string
	NodeName        string
	Type            string
	Severity        string
	Status          string
	Message         string
	OccurrenceCount int
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	AcknowledgedBy  string
	AcknowledgedAt  *time.Time
	ResolvedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

const alertColumns = `
	a.id, a.node_id, n.name, a.type, a.severity, a.status, a.message,
	a.occurrence_count, a.first_seen_at, a.last_seen_at,
	COALESCE(a.acknowledged_by, ''), a.acknowledged_at, a.resolved_at,
	a.created_at, a.updated_at
`

func scanAlert(row rowScanner) (Alert, error) {
	var alert Alert
	var firstSeenAt, lastSeenAt, createdAt, updatedAt int64
	var acknowledgedAt, resolvedAt sql.NullInt64
	if err := row.Scan(
		&alert.ID, &alert.NodeID, &alert.NodeName, &alert.Type, &alert.Severity,
		&alert.Status, &alert.Message, &alert.OccurrenceCount, &firstSeenAt,
		&lastSeenAt, &alert.AcknowledgedBy, &acknowledgedAt, &resolvedAt,
		&createdAt, &updatedAt,
	); err != nil {
		return Alert{}, err
	}
	alert.FirstSeenAt = unixTime(firstSeenAt)
	alert.LastSeenAt = unixTime(lastSeenAt)
	alert.AcknowledgedAt = nullableTime(acknowledgedAt)
	alert.ResolvedAt = nullableTime(resolvedAt)
	alert.CreatedAt = unixTime(createdAt)
	alert.UpdatedAt = unixTime(updatedAt)
	return alert, nil
}

type AlertFilter struct {
	Status string
	NodeID string
	Type   string
	Limit  int
	Offset int
}

func (s *Store) ListAlerts(ctx context.Context, status string, limit int) ([]Alert, error) {
	alerts, _, err := s.ListAlertsPage(ctx, AlertFilter{Status: status, Limit: limit})
	return alerts, err
}

func (s *Store) ListAlertsPage(
	ctx context.Context,
	filter AlertFilter,
) ([]Alert, int, error) {
	if filter.Limit < 1 || filter.Limit > 200 {
		filter.Limit = 100
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	where := []string{"n.archived_at IS NULL"}
	args := make([]any, 0, 6)
	switch strings.TrimSpace(filter.Status) {
	case "", "active":
		where = append(where, "a.resolved_at IS NULL")
	case "resolved":
		where = append(where, "a.resolved_at IS NOT NULL")
	case "all":
	default:
		return nil, 0, ErrUnsupported
	}
	if filter.NodeID != "" {
		where = append(where, "a.node_id = ?")
		args = append(args, filter.NodeID)
	}
	if filter.Type != "" {
		where = append(where, "a.type = ?")
		args = append(args, filter.Type)
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM alerts a JOIN nodes n ON n.id = a.node_id
		WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count alerts: %w", err)
	}
	queryArgs := append(append([]any(nil), args...), filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+alertColumns+`
		FROM alerts a JOIN nodes n ON n.id = a.node_id
		WHERE `+whereSQL+`
		ORDER BY CASE a.severity WHEN 'critical' THEN 0 ELSE 1 END,
		         a.last_seen_at DESC LIMIT ? OFFSET ?
	`, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()
	alerts := make([]Alert, 0)
	for rows.Next() {
		alert, err := scanAlert(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan alert: %w", err)
		}
		alerts = append(alerts, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate alerts: %w", err)
	}
	return alerts, total, nil
}

func (s *Store) AcknowledgeAlert(
	ctx context.Context,
	alertID, adminID string,
	now time.Time,
) (Alert, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE alerts SET status = 'acknowledged', acknowledged_by = ?,
			acknowledged_at = ?, updated_at = ?
		WHERE id = ? AND resolved_at IS NULL
	`, adminID, now.UnixMilli(), now.UnixMilli(), alertID)
	if err != nil {
		return Alert{}, fmt.Errorf("acknowledge alert: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return Alert{}, fmt.Errorf("read alert acknowledgement: %w", err)
	}
	if count == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM alerts WHERE id = ?)", alertID,
		).Scan(&exists); err != nil {
			return Alert{}, fmt.Errorf("check acknowledged alert: %w", err)
		}
		if exists == 0 {
			return Alert{}, ErrNotFound
		}
		return Alert{}, ErrConflict
	}
	alert, err := scanAlert(s.db.QueryRowContext(ctx, `
		SELECT `+alertColumns+`
		FROM alerts a JOIN nodes n ON n.id = a.node_id WHERE a.id = ?
	`, alertID))
	if err != nil {
		return Alert{}, fmt.Errorf("read acknowledged alert: %w", err)
	}
	return alert, nil
}

type alertCondition struct {
	nodeID                      string
	enabled                     bool
	status                      string
	statusReason                string
	installed                   bool
	coreRunning                 bool
	lastActivityAt              time.Time
	usageEnabled                bool
	usageErrorCode              string
	desiredVersion              int64
	appliedVersion              int64
	desiredCreatedAt            time.Time
	failedAssignments           bool
	unrecoveredOperationFailure bool
	trafficLimitBytes           int64
	trafficUsedBytes            int64
}

func (s *Store) ReconcileAlerts(
	ctx context.Context,
	now time.Time,
	offlineAfter, syncStuckAfter time.Duration,
) error {
	if offlineAfter <= 0 {
		offlineAfter = 90 * time.Second
	}
	if syncStuckAfter <= 0 {
		syncStuckAfter = 5 * time.Minute
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin alert reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		UPDATE node_operations
		SET status = 'expired', completed_at = ?, updated_at = ?,
			error_code = 'operation_expired', error_message = 'operation expired before completion'
		WHERE status = 'queued' AND expires_at <= ?
	`, now.UnixMilli(), now.UnixMilli(), now.UnixMilli()); err != nil {
		return fmt.Errorf("expire operations during alert reconciliation: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT n.id, n.enabled, n.status, n.status_reason,
		       (COALESCE(n.agent_installation_id, '') <> ''), n.core_running,
		       COALESCE(n.last_seen_at, n.updated_at), n.usage_enabled,
		       n.usage_error_code, n.desired_version, n.applied_version,
		       COALESCE(s.created_at, n.updated_at),
		       EXISTS(
		           SELECT 1 FROM node_user_assignments a
		           WHERE a.node_id = n.id AND a.state = 'failed'
		       ),
		       EXISTS(
		           SELECT 1 FROM node_operations failed
		           WHERE failed.node_id = n.id AND failed.status IN ('failed', 'expired')
		             AND NOT EXISTS (
		                 SELECT 1 FROM node_operations recovered
		                 WHERE recovered.node_id = failed.node_id
		                   AND recovered.type = failed.type
		                   AND recovered.sequence > failed.sequence
		                   AND recovered.status = 'succeeded'
		             )
		       ),
		       n.traffic_limit_bytes,
		       CASE
		         WHEN n.traffic_calibration_bytes IS NULL OR n.traffic_calibration_proxy_bytes IS NULL
		           THEN n.traffic_cycle_upload_bytes + n.traffic_cycle_download_bytes
		         ELSE n.traffic_calibration_bytes + MAX(
		           0,
		           n.traffic_cycle_upload_bytes + n.traffic_cycle_download_bytes -
		           n.traffic_calibration_proxy_bytes
		         )
		       END
		FROM nodes n
		LEFT JOIN node_snapshots s
		  ON s.node_id = n.id AND s.version = n.desired_version
		WHERE n.archived_at IS NULL
	`)
	if err != nil {
		return fmt.Errorf("query alert conditions: %w", err)
	}
	conditions := make([]alertCondition, 0)
	for rows.Next() {
		var condition alertCondition
		var enabled, installed, coreRunning, usageEnabled int
		var lastActivityAt, desiredCreatedAt int64
		var failedAssignments, operationFailure int
		if err := rows.Scan(
			&condition.nodeID, &enabled, &condition.status, &condition.statusReason,
			&installed, &coreRunning, &lastActivityAt, &usageEnabled,
			&condition.usageErrorCode, &condition.desiredVersion,
			&condition.appliedVersion, &desiredCreatedAt, &failedAssignments,
			&operationFailure, &condition.trafficLimitBytes, &condition.trafficUsedBytes,
		); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan alert condition: %w", err)
		}
		condition.enabled = enabled == 1
		condition.installed = installed == 1
		condition.coreRunning = coreRunning == 1
		condition.usageEnabled = usageEnabled == 1
		condition.failedAssignments = failedAssignments == 1
		condition.unrecoveredOperationFailure = operationFailure == 1
		condition.lastActivityAt = unixTime(lastActivityAt)
		condition.desiredCreatedAt = unixTime(desiredCreatedAt)
		conditions = append(conditions, condition)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate alert conditions: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close alert conditions: %w", err)
	}
	for _, condition := range conditions {
		recent := now.Sub(condition.lastActivityAt) < offlineAfter
		trafficWarning := condition.trafficLimitBytes > 0 &&
			condition.trafficUsedBytes < condition.trafficLimitBytes &&
			condition.trafficUsedBytes >= condition.trafficLimitBytes-condition.trafficLimitBytes/5
		trafficExhausted := condition.trafficLimitBytes > 0 &&
			condition.trafficUsedBytes >= condition.trafficLimitBytes
		checks := []struct {
			alertType string
			severity  string
			active    bool
			message   string
		}{
			{"offline", "critical", condition.enabled && condition.installed && !recent, "Agent heartbeat is overdue"},
			{"degraded", "warning", condition.enabled && condition.status == "degraded", condition.statusReason},
			{"core_down", "critical", condition.enabled && condition.installed && recent && !condition.coreRunning, "core service is not running"},
			{"usage_error", "warning", condition.enabled && condition.usageEnabled && condition.usageErrorCode != "", condition.usageErrorCode},
			{"sync_failed", "warning", condition.enabled && condition.failedAssignments, "one or more desired assignments failed"},
			{
				"sync_stuck", "warning",
				condition.enabled && condition.installed && condition.desiredVersion > condition.appliedVersion &&
					now.Sub(condition.desiredCreatedAt) >= syncStuckAfter,
				"desired state has not been applied within the expected window",
			},
			{"operation_failed", "warning", condition.unrecoveredOperationFailure, "a node operation failed or expired"},
			{"traffic_quota_warning", "warning", trafficWarning, "node traffic has reached 80% of the configured monthly allowance"},
			{"traffic_quota_exhausted", "critical", trafficExhausted, "node traffic has reached the configured monthly allowance"},
		}
		for _, check := range checks {
			if check.active {
				if check.message == "" {
					check.message = check.alertType
				}
				if err := upsertAlertTx(
					ctx, tx, condition.nodeID, check.alertType, check.severity,
					check.message, now,
				); err != nil {
					return err
				}
			} else if err := resolveAlertTx(ctx, tx, condition.nodeID, check.alertType, now); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit alert reconciliation: %w", err)
	}
	return nil
}

func upsertAlertTx(
	ctx context.Context,
	tx *sql.Tx,
	nodeID, alertType, severity, message string,
	now time.Time,
) error {
	if len(message) > 512 {
		message = message[:512]
	}
	var existingID string
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM alerts WHERE node_id = ? AND type = ? AND resolved_at IS NULL
	`, nodeID, alertType).Scan(&existingID)
	if err == nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE alerts SET severity = ?, message = ?, last_seen_at = ?, updated_at = ?
			WHERE id = ?
		`, severity, message, now.UnixMilli(), now.UnixMilli(), existingID); err != nil {
			return fmt.Errorf("update alert: %w", err)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("find active alert: %w", err)
	}
	alertID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO alerts(
			id, node_id, type, severity, status, message,
			first_seen_at, last_seen_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'open', ?, ?, ?, ?, ?)
	`, alertID, nodeID, alertType, severity, message, now.UnixMilli(),
		now.UnixMilli(), now.UnixMilli(), now.UnixMilli()); err != nil {
		return fmt.Errorf("insert alert: %w", err)
	}
	payload, err := alertNotificationJSON(ctx, tx, alertID, "created", now)
	if err != nil {
		return err
	}
	return enqueueAlertNotificationTx(ctx, tx, alertID, "created", payload, now)
}

func resolveAlertTx(ctx context.Context, tx *sql.Tx, nodeID, alertType string, now time.Time) error {
	var alertID string
	err := tx.QueryRowContext(ctx, `
		SELECT id FROM alerts WHERE node_id = ? AND type = ? AND resolved_at IS NULL
	`, nodeID, alertType).Scan(&alertID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find resolving alert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE alerts SET status = 'resolved', resolved_at = ?, updated_at = ?
		WHERE id = ? AND resolved_at IS NULL
	`, now.UnixMilli(), now.UnixMilli(), alertID); err != nil {
		return fmt.Errorf("resolve alert: %w", err)
	}
	payload, err := alertNotificationJSON(ctx, tx, alertID, "resolved", now)
	if err != nil {
		return err
	}
	return enqueueAlertNotificationTx(ctx, tx, alertID, "resolved", payload, now)
}

type alertNotificationPayload struct {
	Event      string    `json:"event"`
	AlertID    string    `json:"alert_id"`
	NodeID     string    `json:"node_id"`
	NodeName   string    `json:"node_name"`
	Type       string    `json:"type"`
	Severity   string    `json:"severity"`
	Status     string    `json:"status"`
	Message    string    `json:"message"`
	OccurredAt time.Time `json:"occurred_at"`
}

func alertNotificationJSON(
	ctx context.Context,
	tx *sql.Tx,
	alertID, event string,
	now time.Time,
) (string, error) {
	var payload alertNotificationPayload
	payload.Event = event
	payload.AlertID = alertID
	payload.OccurredAt = now
	if err := tx.QueryRowContext(ctx, `
		SELECT a.node_id, n.name, a.type, a.severity, a.status, a.message
		FROM alerts a JOIN nodes n ON n.id = a.node_id WHERE a.id = ?
	`, alertID).Scan(
		&payload.NodeID, &payload.NodeName, &payload.Type, &payload.Severity,
		&payload.Status, &payload.Message,
	); err != nil {
		return "", fmt.Errorf("read alert notification payload: %w", err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode alert notification payload: %w", err)
	}
	return string(encoded), nil
}

func (s *Store) GetAlert(ctx context.Context, alertID string) (Alert, error) {
	alert, err := scanAlert(s.db.QueryRowContext(ctx, `
		SELECT `+alertColumns+`
		FROM alerts a JOIN nodes n ON n.id = a.node_id WHERE a.id = ?
	`, alertID))
	if errors.Is(err, sql.ErrNoRows) {
		return Alert{}, ErrNotFound
	}
	if err != nil {
		return Alert{}, fmt.Errorf("get alert: %w", err)
	}
	return alert, nil
}

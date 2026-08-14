package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
)

type TrafficIngestResult struct {
	Status    string
	ErrorCode string
}

type effectiveQuotaState struct {
	NodeID  string
	UserID  string
	Limited bool
}

func (s *Store) IngestTrafficBatch(
	ctx context.Context,
	identity AgentIdentity,
	batch protocol.TrafficBatch,
	now time.Time,
) (TrafficIngestResult, error) {
	if batch.InstallationID != identity.InstallationID {
		return TrafficIngestResult{Status: "rejected", ErrorCode: "installation_conflict"}, nil
	}
	canonical, err := json.Marshal(batch)
	if err != nil {
		return TrafficIngestResult{}, fmt.Errorf("encode traffic batch fingerprint: %w", err)
	}
	fingerprint := sha256.Sum256(canonical)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TrafficIngestResult{}, fmt.Errorf("begin traffic batch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existingFingerprint []byte
	err = tx.QueryRowContext(ctx, `
		SELECT payload_sha256 FROM traffic_batches WHERE id = ?
	`, batch.ID).Scan(&existingFingerprint)
	if err == nil {
		if bytes.Equal(existingFingerprint, fingerprint[:]) {
			return TrafficIngestResult{Status: "duplicate"}, nil
		}
		return TrafficIngestResult{Status: "rejected", ErrorCode: "batch_id_conflict"}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TrafficIngestResult{}, fmt.Errorf("check traffic batch ID: %w", err)
	}
	var sequenceBatchID string
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM traffic_batches
		WHERE node_id = ? AND agent_installation_id = ? AND source_epoch = ? AND sequence = ?
	`, identity.NodeID, identity.InstallationID, batch.SourceEpoch, batch.Sequence).Scan(&sequenceBatchID)
	if err == nil {
		return TrafficIngestResult{Status: "rejected", ErrorCode: "sequence_conflict"}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return TrafficIngestResult{}, fmt.Errorf("check traffic sequence: %w", err)
	}

	userIDs := make([]string, 0, len(batch.Items))
	var uploadTotal, downloadTotal int64
	for _, item := range batch.Items {
		userIDs = append(userIDs, item.UserID)
		if uploadTotal > math.MaxInt64-item.UploadBytes || downloadTotal > math.MaxInt64-item.DownloadBytes {
			return TrafficIngestResult{Status: "rejected", ErrorCode: "batch_total_overflow"}, nil
		}
		uploadTotal += item.UploadBytes
		downloadTotal += item.DownloadBytes
	}
	before, err := effectiveQuotaStatesTx(ctx, tx, userIDs)
	if err != nil {
		return TrafficIngestResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO traffic_batches(
			id, node_id, agent_installation_id, source_epoch, sequence,
			sampled_at, received_at, item_count, upload_bytes, download_bytes, payload_sha256
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, batch.ID, identity.NodeID, identity.InstallationID, batch.SourceEpoch, batch.Sequence,
		batch.SampledAt.UnixMilli(), now.UnixMilli(), len(batch.Items), uploadTotal,
		downloadTotal, fingerprint[:]); err != nil {
		return TrafficIngestResult{}, fmt.Errorf("insert traffic batch: %w", err)
	}

	var unattributed int64
	for _, item := range batch.Items {
		disposition, err := classifyTrafficItemTx(ctx, tx, identity.NodeID, item.UserID)
		if err != nil {
			return TrafficIngestResult{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO traffic_batch_items(
				batch_id, node_id, user_id, upload_bytes, download_bytes, disposition
			) VALUES (?, ?, ?, ?, ?, ?)
		`, batch.ID, identity.NodeID, item.UserID, item.UploadBytes,
			item.DownloadBytes, disposition); err != nil {
			return TrafficIngestResult{}, fmt.Errorf("insert traffic batch item: %w", err)
		}
		if disposition != "accounted" {
			itemTotal, ok := checkedAdd(item.UploadBytes, item.DownloadBytes)
			if !ok || unattributed > math.MaxInt64-itemTotal {
				return TrafficIngestResult{Status: "rejected", ErrorCode: "batch_total_overflow"}, nil
			}
			unattributed += itemTotal
			continue
		}
		if err := addAccountedTrafficTx(ctx, tx, identity.NodeID, batch.ID, item, now); err != nil {
			return TrafficIngestResult{}, err
		}
	}
	if err := addNodeTrafficTx(
		ctx, tx, identity.NodeID, uploadTotal, downloadTotal, unattributed,
		batch.SampledAt, now,
	); err != nil {
		return TrafficIngestResult{}, err
	}

	after, err := effectiveQuotaStatesTx(ctx, tx, userIDs)
	if err != nil {
		return TrafficIngestResult{}, err
	}
	changed := make(map[string]map[string]struct{})
	for key, current := range after {
		previous, existed := before[key]
		if existed && previous.Limited == current.Limited {
			continue
		}
		if current.Limited {
			if err := requestKickTx(ctx, tx, current.NodeID, current.UserID, "quota_limited", now); err != nil {
				return TrafficIngestResult{}, err
			}
		}
		addChangedAssignment(changed, current.NodeID, current.UserID)
	}
	if err := bumpChangedAssignmentsTx(ctx, tx, changed, now); err != nil {
		return TrafficIngestResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return TrafficIngestResult{}, fmt.Errorf("commit traffic batch: %w", err)
	}
	return TrafficIngestResult{Status: "accepted"}, nil
}

func classifyTrafficItemTx(
	ctx context.Context,
	tx *sql.Tx,
	nodeID, userID string,
) (string, error) {
	var archivedAt sql.NullInt64
	err := tx.QueryRowContext(ctx, "SELECT archived_at FROM users WHERE id = ?", userID).Scan(&archivedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "unknown_user", nil
	}
	if err != nil {
		return "", fmt.Errorf("classify traffic user: %w", err)
	}
	if archivedAt.Valid {
		return "archived_user", nil
	}
	var exists int
	err = tx.QueryRowContext(ctx, `
		SELECT 1 FROM node_user_assignments WHERE node_id = ? AND user_id = ?
	`, nodeID, userID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return "unassigned", nil
	}
	if err != nil {
		return "", fmt.Errorf("classify traffic assignment: %w", err)
	}
	return "accounted", nil
}

func addAccountedTrafficTx(
	ctx context.Context,
	tx *sql.Tx,
	nodeID, batchID string,
	item protocol.TrafficDelta,
	now time.Time,
) error {
	var userUpload, userDownload, userUsed, userLimit int64
	if err := tx.QueryRowContext(ctx, `
		SELECT traffic_upload_bytes, traffic_download_bytes, traffic_used_bytes,
		       traffic_limit_bytes
		FROM users WHERE id = ?
	`, item.UserID).Scan(&userUpload, &userDownload, &userUsed, &userLimit); err != nil {
		return fmt.Errorf("read user traffic cache: %w", err)
	}
	var assignmentUpload, assignmentDownload, assignmentUsed, assignmentLimit int64
	if err := tx.QueryRowContext(ctx, `
		SELECT traffic_upload_bytes, traffic_download_bytes, traffic_used_bytes,
		       traffic_limit_bytes
		FROM node_user_assignments WHERE node_id = ? AND user_id = ?
	`, nodeID, item.UserID).Scan(
		&assignmentUpload, &assignmentDownload, &assignmentUsed, &assignmentLimit,
	); err != nil {
		return fmt.Errorf("read assignment traffic cache: %w", err)
	}
	deltaUsed, ok := checkedAdd(item.UploadBytes, item.DownloadBytes)
	if !ok || userUpload > math.MaxInt64-item.UploadBytes ||
		userDownload > math.MaxInt64-item.DownloadBytes || userUsed > math.MaxInt64-deltaUsed ||
		assignmentUpload > math.MaxInt64-item.UploadBytes ||
		assignmentDownload > math.MaxInt64-item.DownloadBytes ||
		assignmentUsed > math.MaxInt64-deltaUsed {
		return errors.New("traffic aggregate exceeds SQLite integer range")
	}
	userUpload += item.UploadBytes
	userDownload += item.DownloadBytes
	userUsed += deltaUsed
	assignmentUpload += item.UploadBytes
	assignmentDownload += item.DownloadBytes
	assignmentUsed += deltaUsed
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO traffic_totals(
			node_id, user_id, upload_bytes, download_bytes, last_batch_id, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id, user_id) DO UPDATE SET
			upload_bytes = excluded.upload_bytes,
			download_bytes = excluded.download_bytes,
			last_batch_id = excluded.last_batch_id,
			updated_at = excluded.updated_at
	`, nodeID, item.UserID, assignmentUpload, assignmentDownload,
		batchID, now.UnixMilli()); err != nil {
		return fmt.Errorf("update traffic total: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE users SET traffic_upload_bytes = ?, traffic_download_bytes = ?,
			traffic_used_bytes = ?, quota_state = ?, last_traffic_at = ?, updated_at = ?
		WHERE id = ?
	`, userUpload, userDownload, userUsed, quotaState(userLimit, userUsed),
		now.UnixMilli(), now.UnixMilli(), item.UserID); err != nil {
		return fmt.Errorf("update user traffic cache: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE node_user_assignments
		SET traffic_upload_bytes = ?, traffic_download_bytes = ?, traffic_used_bytes = ?,
			quota_state = ?, last_traffic_at = ?, updated_at = ?
		WHERE node_id = ? AND user_id = ?
	`, assignmentUpload, assignmentDownload, assignmentUsed,
		quotaState(assignmentLimit, assignmentUsed), now.UnixMilli(), now.UnixMilli(),
		nodeID, item.UserID); err != nil {
		return fmt.Errorf("update assignment traffic cache: %w", err)
	}
	return nil
}

func addNodeTrafficTx(
	ctx context.Context,
	tx *sql.Tx,
	nodeID string,
	upload, download, unattributed int64,
	sampledAt time.Time,
	now time.Time,
) error {
	var currentUpload, currentDownload, currentUnattributed int64
	var resetDay int
	var storedCycleStarted sql.NullInt64
	var cycleUpload, cycleDownload int64
	var calibratedAt sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
		SELECT traffic_upload_bytes, traffic_download_bytes, traffic_unattributed_bytes,
		       traffic_reset_day, traffic_cycle_started_at,
		       traffic_cycle_upload_bytes, traffic_cycle_download_bytes,
		       traffic_calibrated_at
		FROM nodes WHERE id = ?
	`, nodeID).Scan(
		&currentUpload, &currentDownload, &currentUnattributed, &resetDay,
		&storedCycleStarted, &cycleUpload, &cycleDownload, &calibratedAt,
	); err != nil {
		return fmt.Errorf("read node traffic cache: %w", err)
	}
	if currentUpload > math.MaxInt64-upload || currentDownload > math.MaxInt64-download ||
		currentUnattributed > math.MaxInt64-unattributed {
		return errors.New("node traffic aggregate exceeds SQLite integer range")
	}
	cycleStart := trafficCycleStart(now, resetDay)
	nextStart := trafficCycleNextStart(cycleStart, resetDay)
	cycleChanged := !storedCycleStarted.Valid || storedCycleStarted.Int64 != cycleStart.UnixMilli()
	if cycleChanged {
		var err error
		cycleUpload, cycleDownload, err = trafficCycleTotalsTx(ctx, tx, nodeID, cycleStart, nextStart)
		if err != nil {
			return err
		}
	} else if !sampledAt.Before(cycleStart) && sampledAt.Before(nextStart) {
		if cycleUpload > math.MaxInt64-upload || cycleDownload > math.MaxInt64-download {
			return errors.New("node traffic cycle aggregate exceeds SQLite integer range")
		}
		cycleUpload += upload
		cycleDownload += download
	}
	clearCalibration := cycleChanged && calibratedAt.Valid && calibratedAt.Int64 < cycleStart.UnixMilli()
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes SET traffic_upload_bytes = ?, traffic_download_bytes = ?,
			traffic_unattributed_bytes = ?, traffic_last_report_at = ?, updated_at = ?
			, traffic_cycle_started_at = ?, traffic_cycle_upload_bytes = ?,
			traffic_cycle_download_bytes = ?,
			traffic_calibration_bytes = CASE WHEN ? THEN NULL ELSE traffic_calibration_bytes END,
			traffic_calibration_proxy_bytes = CASE WHEN ? THEN NULL ELSE traffic_calibration_proxy_bytes END,
			traffic_calibrated_at = CASE WHEN ? THEN NULL ELSE traffic_calibrated_at END
		WHERE id = ?
	`, currentUpload+upload, currentDownload+download,
		currentUnattributed+unattributed, now.UnixMilli(), now.UnixMilli(),
		cycleStart.UnixMilli(), cycleUpload, cycleDownload,
		clearCalibration, clearCalibration, clearCalibration, nodeID); err != nil {
		return fmt.Errorf("update node traffic cache: %w", err)
	}
	return nil
}

func effectiveQuotaStatesTx(
	ctx context.Context,
	tx *sql.Tx,
	userIDs []string,
) (map[string]effectiveQuotaState, error) {
	result := make(map[string]effectiveQuotaState)
	seen := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if _, exists := seen[userID]; exists {
			continue
		}
		seen[userID] = struct{}{}
		rows, err := tx.QueryContext(ctx, `
			SELECT a.node_id,
			       CASE WHEN u.quota_state = 'limited' OR a.quota_state = 'limited'
			            THEN 1 ELSE 0 END
			FROM node_user_assignments a
			JOIN users u ON u.id = a.user_id
			WHERE a.user_id = ? AND u.archived_at IS NULL
		`, userID)
		if err != nil {
			return nil, fmt.Errorf("read effective quota state: %w", err)
		}
		for rows.Next() {
			var state effectiveQuotaState
			var limited int
			state.UserID = userID
			if err := rows.Scan(&state.NodeID, &limited); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan effective quota state: %w", err)
			}
			state.Limited = limited == 1
			result[quotaStateKey(state.NodeID, userID)] = state
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate effective quota state: %w", err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close effective quota state: %w", err)
		}
	}
	return result, nil
}

func requestKickTx(
	ctx context.Context,
	tx *sql.Tx,
	nodeID, userID, reason string,
	now time.Time,
) error {
	var adapter string
	if err := tx.QueryRowContext(ctx, `
		SELECT adapter_type FROM nodes WHERE id = ? AND archived_at IS NULL
	`, nodeID).Scan(&adapter); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read kick node adapter: %w", err)
	}
	if adapter != "native_hysteria2" && adapter != AdapterSingBoxVLESSReality {
		return nil
	}
	if len(reason) > 64 {
		reason = reason[:64]
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO node_kick_targets(node_id, user_id, generation, reason, requested_at)
		VALUES (?, ?, 1, ?, ?)
		ON CONFLICT(node_id, user_id) DO UPDATE SET
			generation = node_kick_targets.generation + 1,
			reason = excluded.reason,
			requested_at = excluded.requested_at
	`, nodeID, userID, reason, now.UnixMilli()); err != nil {
		return fmt.Errorf("request node kick: %w", err)
	}
	return nil
}

func bumpChangedAssignmentsTx(
	ctx context.Context,
	tx *sql.Tx,
	changed map[string]map[string]struct{},
	now time.Time,
) error {
	nodeIDs := make([]string, 0, len(changed))
	for nodeID := range changed {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	for _, nodeID := range nodeIDs {
		version, err := bumpNodeSnapshot(ctx, tx, nodeID, now)
		if err != nil {
			return err
		}
		for userID := range changed[nodeID] {
			if err := markAssignmentPending(ctx, tx, userID, nodeID, version, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) RecordOnlineSnapshot(
	ctx context.Context,
	identity AgentIdentity,
	snapshot protocol.OnlineSnapshotRequest,
	now time.Time,
) (bool, error) {
	if snapshot.InstallationID != identity.InstallationID {
		return false, ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin online snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var previousSampledAt int64
	err = tx.QueryRowContext(ctx, `
		SELECT sampled_at FROM node_online_snapshots WHERE node_id = ?
	`, identity.NodeID).Scan(&previousSampledAt)
	if err == nil && snapshot.SampledAt.UnixMilli() <= previousSampledAt {
		return false, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("read previous online snapshot: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM node_online_users WHERE node_id = ?", identity.NodeID); err != nil {
		return false, fmt.Errorf("replace online users: %w", err)
	}
	totalConnections := 0
	knownUsers := 0
	unknownUsers := 0
	for _, user := range snapshot.Users {
		if totalConnections > math.MaxInt-user.Connections {
			return false, errors.New("online connection total exceeds integer range")
		}
		totalConnections += user.Connections
		var exists int
		err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM node_user_assignments WHERE node_id = ? AND user_id = ?
		`, identity.NodeID, user.UserID).Scan(&exists)
		known := 1
		if errors.Is(err, sql.ErrNoRows) {
			known = 0
			unknownUsers++
		} else if err != nil {
			return false, fmt.Errorf("classify online user: %w", err)
		} else {
			knownUsers++
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO node_online_users(
				node_id, user_id, connections, known_assignment, sampled_at
			) VALUES (?, ?, ?, ?, ?)
		`, identity.NodeID, user.UserID, user.Connections, known,
			snapshot.SampledAt.UnixMilli()); err != nil {
			return false, fmt.Errorf("insert online user: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO node_online_snapshots(
			node_id, agent_installation_id, snapshot_id, sampled_at, received_at,
			total_connections, known_users, unknown_users
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			agent_installation_id = excluded.agent_installation_id,
			snapshot_id = excluded.snapshot_id,
			sampled_at = excluded.sampled_at,
			received_at = excluded.received_at,
			total_connections = excluded.total_connections,
			known_users = excluded.known_users,
			unknown_users = excluded.unknown_users
	`, identity.NodeID, identity.InstallationID, snapshot.SnapshotID,
		snapshot.SampledAt.UnixMilli(), now.UnixMilli(), totalConnections,
		knownUsers, unknownUsers); err != nil {
		return false, fmt.Errorf("upsert online snapshot: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes SET online_users = ?, online_connections = ?, online_unknown_users = ?,
			online_sampled_at = ?, online_last_report_at = ?, updated_at = ?
		WHERE id = ?
	`, knownUsers, totalConnections, unknownUsers, snapshot.SampledAt.UnixMilli(),
		now.UnixMilli(), now.UnixMilli(), identity.NodeID); err != nil {
		return false, fmt.Errorf("update node online state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit online snapshot: %w", err)
	}
	return true, nil
}

func (s *Store) RequestUserKick(
	ctx context.Context,
	userID, nodeID string,
	now time.Time,
) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin user kick: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM users WHERE id = ? AND archived_at IS NULL
	`, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("find kick user: %w", err)
	}
	query := `
		SELECT a.node_id, n.adapter_type
		FROM node_user_assignments a
		JOIN nodes n ON n.id = a.node_id AND n.archived_at IS NULL
		WHERE a.user_id = ?
	`
	arguments := []any{userID}
	if nodeID != "" {
		query += " AND a.node_id = ?"
		arguments = append(arguments, nodeID)
	}
	query += " ORDER BY a.node_id"
	rows, err := tx.QueryContext(ctx, query, arguments...)
	if err != nil {
		return 0, fmt.Errorf("list kick assignments: %w", err)
	}
	nodeIDs := make([]string, 0)
	for rows.Next() {
		var current, adapter string
		if err := rows.Scan(&current, &adapter); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan kick assignment: %w", err)
		}
		if adapter == "native_hysteria2" || adapter == AdapterSingBoxVLESSReality {
			nodeIDs = append(nodeIDs, current)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("iterate kick assignments: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close kick assignments: %w", err)
	}
	if len(nodeIDs) == 0 {
		return 0, ErrNotFound
	}
	changed := make(map[string]map[string]struct{}, len(nodeIDs))
	for _, currentNodeID := range nodeIDs {
		if err := requestKickTx(ctx, tx, currentNodeID, userID, "manual", now); err != nil {
			return 0, err
		}
		addChangedAssignment(changed, currentNodeID, userID)
	}
	if err := bumpChangedAssignmentsTx(ctx, tx, changed, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit user kick: %w", err)
	}
	return len(nodeIDs), nil
}

func (s *Store) EnforceExpiredUsers(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM users
		WHERE archived_at IS NULL AND enabled = 1 AND expires_at IS NOT NULL
		  AND expires_at <= ? AND expiry_enforced_at IS NULL
		ORDER BY expires_at LIMIT ?
	`, now.UnixMilli(), limit)
	if err != nil {
		return 0, fmt.Errorf("list expired users: %w", err)
	}
	userIDs := make([]string, 0, limit)
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan expired user: %w", err)
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("iterate expired users: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close expired users: %w", err)
	}
	if len(userIDs) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin expiry enforcement: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	changed := make(map[string]map[string]struct{})
	enforced := 0
	for _, userID := range userIDs {
		result, err := tx.ExecContext(ctx, `
			UPDATE users SET expiry_enforced_at = ?, updated_at = ?
			WHERE id = ? AND expiry_enforced_at IS NULL AND expires_at <= ?
		`, now.UnixMilli(), now.UnixMilli(), userID, now.UnixMilli())
		if err != nil {
			return 0, fmt.Errorf("mark expiry enforced: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("read expiry enforcement result: %w", err)
		}
		if count == 0 {
			continue
		}
		enforced++
		nodeIDs, err := assignmentNodeIDs(ctx, tx, userID)
		if err != nil {
			return 0, err
		}
		for _, nodeID := range nodeIDs {
			if err := requestKickTx(ctx, tx, nodeID, userID, "expired", now); err != nil {
				return 0, err
			}
			addChangedAssignment(changed, nodeID, userID)
		}
	}
	if err := bumpChangedAssignmentsTx(ctx, tx, changed, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit expiry enforcement: %w", err)
	}
	return enforced, nil
}

func quotaState(limit, used int64) string {
	if limit == 0 {
		return "unlimited"
	}
	if used >= limit {
		return "limited"
	}
	return "active"
}

func expiryEnforcedValue(expiresAt *time.Time, now time.Time) any {
	if expiresAt != nil && !now.Before(expiresAt.UTC()) {
		return now.UnixMilli()
	}
	return nil
}

func quotaStateKey(nodeID, userID string) string {
	return nodeID + "\x00" + userID
}

func addChangedAssignment(changed map[string]map[string]struct{}, nodeID, userID string) {
	if changed[nodeID] == nil {
		changed[nodeID] = make(map[string]struct{})
	}
	changed[nodeID][userID] = struct{}{}
}

func checkedAdd(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || left > math.MaxInt64-right {
		return 0, false
	}
	return left + right, true
}

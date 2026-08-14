package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/google/uuid"
)

const (
	DefaultOperationLogLines = 100
	MaxOperationLogLines     = 200
	MaxOperationOutputBytes  = 32 * 1024
	MaxPendingNodeOperations = 20
)

type NodeOperation struct {
	ID           string
	NodeID       string
	NodeName     string
	Sequence     int64
	Type         string
	Status       string
	RetryOf      string
	Attempt      int
	MaxLines     int
	Output       string
	ErrorCode    string
	ErrorMessage string
	RolledBack   bool
	RequestedBy  string
	ExpiresAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type ConfigBackup struct {
	ID          string
	NodeID      string
	NodeName    string
	OperationID string
	LocalPath   string
	SHA256      string
	SizeBytes   int64
	CreatedAt   time.Time
}

const operationColumns = `
	o.id, o.node_id, n.name, o.sequence, o.type, o.status,
	COALESCE(o.retry_of, ''), o.attempt, o.max_lines, o.output,
	o.error_code, o.error_message, o.rolled_back,
	COALESCE((SELECT username FROM admins WHERE id = o.requested_by), o.requested_by),
	o.expires_at, o.started_at, o.completed_at, o.created_at, o.updated_at
`

func scanNodeOperation(row rowScanner) (NodeOperation, error) {
	var operation NodeOperation
	var rolledBack int
	var expiresAt, createdAt, updatedAt int64
	var startedAt, completedAt sql.NullInt64
	if err := row.Scan(
		&operation.ID, &operation.NodeID, &operation.NodeName, &operation.Sequence,
		&operation.Type, &operation.Status, &operation.RetryOf, &operation.Attempt,
		&operation.MaxLines, &operation.Output, &operation.ErrorCode,
		&operation.ErrorMessage, &rolledBack, &operation.RequestedBy, &expiresAt,
		&startedAt, &completedAt, &createdAt, &updatedAt,
	); err != nil {
		return NodeOperation{}, err
	}
	operation.RolledBack = rolledBack == 1
	operation.ExpiresAt = unixTime(expiresAt)
	operation.StartedAt = nullableTime(startedAt)
	operation.CompletedAt = nullableTime(completedAt)
	operation.CreatedAt = unixTime(createdAt)
	operation.UpdatedAt = unixTime(updatedAt)
	return operation, nil
}

func ValidOperationType(value string) bool {
	switch value {
	case "probe_core", "restart_core", "tail_core_log", "backup_config":
		return true
	default:
		return false
	}
}

func ValidOperationStatus(value string) bool {
	switch value {
	case "queued", "running", "succeeded", "failed", "expired":
		return true
	default:
		return false
	}
}

type OperationFilter struct {
	NodeID string
	Type   string
	Status string
	Limit  int
	Offset int
}

type OperationPage struct {
	Operations []NodeOperation
	Total      int
}

func (s *Store) ListOperations(ctx context.Context, filter OperationFilter) (OperationPage, error) {
	if filter.Limit < 1 || filter.Limit > 100 {
		filter.Limit = 20
	}
	if filter.Offset < 0 || (filter.Type != "" && !ValidOperationType(filter.Type)) ||
		(filter.Status != "" && !ValidOperationStatus(filter.Status)) {
		return OperationPage{}, ErrUnsupported
	}
	where := []string{"n.archived_at IS NULL"}
	arguments := make([]any, 0, 5)
	if filter.NodeID != "" {
		where = append(where, "o.node_id = ?")
		arguments = append(arguments, filter.NodeID)
	}
	if filter.Type != "" {
		where = append(where, "o.type = ?")
		arguments = append(arguments, filter.Type)
	}
	if filter.Status != "" {
		where = append(where, "o.status = ?")
		arguments = append(arguments, filter.Status)
	}
	fromWhere := ` FROM node_operations o JOIN nodes n ON n.id = o.node_id WHERE ` + strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+fromWhere, arguments...).Scan(&total); err != nil {
		return OperationPage{}, fmt.Errorf("count operations: %w", err)
	}
	queryArguments := append(append([]any{}, arguments...), filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+operationColumns+fromWhere+`
		ORDER BY o.created_at DESC, o.sequence DESC LIMIT ? OFFSET ?
	`, queryArguments...)
	if err != nil {
		return OperationPage{}, fmt.Errorf("list operations: %w", err)
	}
	defer rows.Close()
	operations := make([]NodeOperation, 0, filter.Limit)
	for rows.Next() {
		operation, err := scanNodeOperation(rows)
		if err != nil {
			return OperationPage{}, fmt.Errorf("scan operation: %w", err)
		}
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		return OperationPage{}, fmt.Errorf("iterate operations: %w", err)
	}
	return OperationPage{Operations: operations, Total: total}, nil
}

func normalizeOperationLines(operationType string, maxLines int) (int, error) {
	if operationType != "tail_core_log" {
		if maxLines != 0 {
			return 0, ErrUnsupported
		}
		return 0, nil
	}
	if maxLines == 0 {
		return DefaultOperationLogLines, nil
	}
	if maxLines < 1 || maxLines > MaxOperationLogLines {
		return 0, ErrUnsupported
	}
	return maxLines, nil
}

func operationTTL(operationType string) time.Duration {
	switch operationType {
	case "restart_core", "backup_config":
		return 15 * time.Minute
	default:
		return 10 * time.Minute
	}
}

func (s *Store) CreateNodeOperation(
	ctx context.Context,
	nodeID, operationType string,
	maxLines int,
	requestedBy string,
	now time.Time,
) (NodeOperation, error) {
	if !ValidOperationType(operationType) {
		return NodeOperation{}, ErrUnsupported
	}
	normalizedLines, err := normalizeOperationLines(operationType, maxLines)
	if err != nil {
		return NodeOperation{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NodeOperation{}, fmt.Errorf("begin create node operation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var installationID, adapter string
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(agent_installation_id, ''), adapter_type
		FROM nodes WHERE id = ? AND archived_at IS NULL
	`, nodeID).Scan(&installationID, &adapter); errors.Is(err, sql.ErrNoRows) {
		return NodeOperation{}, ErrNotFound
	} else if err != nil {
		return NodeOperation{}, fmt.Errorf("read operation node: %w", err)
	}
	if installationID == "" {
		return NodeOperation{}, ErrPending
	}
	if adapter == AdapterSingBoxVLESSReality && operationType == "tail_core_log" {
		return NodeOperation{}, ErrUnsupported
	}
	if err := ensureOperationQueueCapacity(ctx, tx, nodeID); err != nil {
		return NodeOperation{}, err
	}
	operation, err := insertNodeOperationTx(
		ctx, tx, nodeID, operationType, normalizedLines, "", 1, requestedBy, now,
	)
	if err != nil {
		return NodeOperation{}, err
	}
	if err := tx.Commit(); err != nil {
		return NodeOperation{}, fmt.Errorf("commit create node operation: %w", err)
	}
	return s.GetNodeOperation(ctx, nodeID, operation.ID)
}

func insertNodeOperationTx(
	ctx context.Context,
	tx *sql.Tx,
	nodeID, operationType string,
	maxLines int,
	retryOf string,
	attempt int,
	requestedBy string,
	now time.Time,
) (NodeOperation, error) {
	var sequence int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence), 0) + 1 FROM node_operations WHERE node_id = ?
	`, nodeID).Scan(&sequence); err != nil {
		return NodeOperation{}, fmt.Errorf("allocate operation sequence: %w", err)
	}
	operationID := uuid.NewString()
	expiresAt := now.Add(operationTTL(operationType))
	var retryValue any
	if retryOf != "" {
		retryValue = retryOf
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO node_operations(
			id, node_id, sequence, type, retry_of, attempt, max_lines,
			requested_by, expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, operationID, nodeID, sequence, operationType, retryValue, attempt,
		maxLines, requestedBy, expiresAt.UnixMilli(), now.UnixMilli(), now.UnixMilli()); err != nil {
		return NodeOperation{}, fmt.Errorf("insert node operation: %w", err)
	}
	return NodeOperation{ID: operationID, NodeID: nodeID, Sequence: sequence}, nil
}

func (s *Store) GetNodeOperation(ctx context.Context, nodeID, operationID string) (NodeOperation, error) {
	operation, err := scanNodeOperation(s.db.QueryRowContext(ctx, `
		SELECT `+operationColumns+`
		FROM node_operations o JOIN nodes n ON n.id = o.node_id
		WHERE o.id = ? AND o.node_id = ? AND n.archived_at IS NULL
	`, operationID, nodeID))
	if errors.Is(err, sql.ErrNoRows) {
		return NodeOperation{}, ErrNotFound
	}
	if err != nil {
		return NodeOperation{}, fmt.Errorf("get node operation: %w", err)
	}
	return operation, nil
}

func (s *Store) ListNodeOperations(ctx context.Context, nodeID string, limit int) ([]NodeOperation, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+operationColumns+`
		FROM node_operations o JOIN nodes n ON n.id = o.node_id
		WHERE o.node_id = ? AND n.archived_at IS NULL
		ORDER BY o.sequence DESC LIMIT ?
	`, nodeID, limit)
	if err != nil {
		return nil, fmt.Errorf("list node operations: %w", err)
	}
	defer rows.Close()
	operations := make([]NodeOperation, 0)
	for rows.Next() {
		operation, err := scanNodeOperation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan node operation: %w", err)
		}
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate node operations: %w", err)
	}
	// Release the query connection before the existence check below. With the
	// original one-connection pool, an empty operation list otherwise waited on
	// its own still-open rows and starved heartbeats for every node.
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close node operations: %w", err)
	}
	if len(operations) == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM nodes WHERE id = ? AND archived_at IS NULL)", nodeID,
		).Scan(&exists); err != nil {
			return nil, fmt.Errorf("check operation node: %w", err)
		}
		if exists == 0 {
			return nil, ErrNotFound
		}
	}
	return operations, nil
}

func (s *Store) RetryNodeOperation(
	ctx context.Context,
	nodeID, operationID, requestedBy string,
	now time.Time,
) (NodeOperation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NodeOperation{}, fmt.Errorf("begin retry node operation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var operationType, status, adapter string
	var maxLines, attempt int
	if err := tx.QueryRowContext(ctx, `
		SELECT o.type, o.status, o.max_lines, o.attempt, n.adapter_type
		FROM node_operations o JOIN nodes n ON n.id = o.node_id AND n.archived_at IS NULL
		WHERE o.id = ? AND o.node_id = ?
	`, operationID, nodeID).Scan(&operationType, &status, &maxLines, &attempt, &adapter); errors.Is(err, sql.ErrNoRows) {
		return NodeOperation{}, ErrNotFound
	} else if err != nil {
		return NodeOperation{}, fmt.Errorf("read retry operation: %w", err)
	}
	if status != "failed" && status != "expired" {
		return NodeOperation{}, ErrConflict
	}
	if adapter == AdapterSingBoxVLESSReality && operationType == "tail_core_log" {
		return NodeOperation{}, ErrUnsupported
	}
	if err := ensureOperationQueueCapacity(ctx, tx, nodeID); err != nil {
		return NodeOperation{}, err
	}
	operation, err := insertNodeOperationTx(
		ctx, tx, nodeID, operationType, maxLines, operationID, attempt+1, requestedBy, now,
	)
	if err != nil {
		return NodeOperation{}, err
	}
	if err := tx.Commit(); err != nil {
		return NodeOperation{}, fmt.Errorf("commit retry node operation: %w", err)
	}
	return s.GetNodeOperation(ctx, nodeID, operation.ID)
}

func ensureOperationQueueCapacity(ctx context.Context, tx *sql.Tx, nodeID string) error {
	var pending int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM node_operations
		WHERE node_id = ? AND status IN ('queued', 'running')
	`, nodeID).Scan(&pending); err != nil {
		return fmt.Errorf("count pending node operations: %w", err)
	}
	if pending >= MaxPendingNodeOperations {
		return ErrConflict
	}
	return nil
}

func (s *Store) ListPendingNodeOperations(
	ctx context.Context,
	identity AgentIdentity,
	after int64,
	now time.Time,
	limit int,
) ([]protocol.NodeOperation, error) {
	if limit < 1 || limit > 20 {
		limit = 20
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin list pending operations: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var installationID string
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(agent_installation_id, '') FROM nodes
		WHERE id = ? AND archived_at IS NULL
	`, identity.NodeID).Scan(&installationID); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("read operation Agent: %w", err)
	}
	if installationID != identity.InstallationID {
		return nil, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE node_operations
		SET status = 'expired', completed_at = ?, updated_at = ?,
			error_code = 'operation_expired', error_message = 'operation expired before completion'
		WHERE node_id = ? AND sequence > ? AND status = 'queued'
		  AND expires_at <= ?
	`, now.UnixMilli(), now.UnixMilli(), identity.NodeID, after, now.UnixMilli()); err != nil {
		return nil, fmt.Errorf("expire pending operations: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, sequence, type, max_lines, attempt, created_at, expires_at
		FROM node_operations
		WHERE node_id = ? AND sequence > ?
		  AND (status = 'running' OR (status = 'queued' AND expires_at > ?))
		ORDER BY sequence LIMIT ?
	`, identity.NodeID, after, now.UnixMilli(), limit)
	if err != nil {
		return nil, fmt.Errorf("query pending operations: %w", err)
	}
	operations := make([]protocol.NodeOperation, 0)
	operationIDs := make([]string, 0)
	for rows.Next() {
		var operation protocol.NodeOperation
		var createdAt, expiresAt int64
		if err := rows.Scan(
			&operation.ID, &operation.Sequence, &operation.Type, &operation.MaxLines,
			&operation.Attempt, &createdAt, &expiresAt,
		); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan pending operation: %w", err)
		}
		operation.CreatedAt = unixTime(createdAt)
		operation.ExpiresAt = unixTime(expiresAt)
		operations = append(operations, operation)
		operationIDs = append(operationIDs, operation.ID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate pending operations: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close pending operations: %w", err)
	}
	for _, operationID := range operationIDs {
		if _, err := tx.ExecContext(ctx, `
			UPDATE node_operations SET status = 'running',
				started_at = COALESCE(started_at, ?), updated_at = ?
			WHERE id = ? AND status IN ('queued', 'running')
		`, now.UnixMilli(), now.UnixMilli(), operationID); err != nil {
			return nil, fmt.Errorf("mark operation running: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit pending operations: %w", err)
	}
	return operations, nil
}

func (s *Store) RecordNodeOperationResult(
	ctx context.Context,
	identity AgentIdentity,
	operationID string,
	result protocol.OperationResultRequest,
	now time.Time,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin operation result: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var operationType, status, output, errorCode, errorMessage, installationID string
	var sequence int64
	var rolledBack int
	err = tx.QueryRowContext(ctx, `
		SELECT o.type, o.status, o.sequence, o.output, o.error_code,
		       o.error_message, o.rolled_back, COALESCE(n.agent_installation_id, '')
		FROM node_operations o JOIN nodes n ON n.id = o.node_id
		WHERE o.id = ? AND o.node_id = ? AND n.archived_at IS NULL
	`, operationID, identity.NodeID).Scan(
		&operationType, &status, &sequence, &output, &errorCode,
		&errorMessage, &rolledBack, &installationID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read operation result target: %w", err)
	}
	if installationID != identity.InstallationID || sequence != result.Sequence {
		return ErrConflict
	}
	if status == "succeeded" || status == "failed" {
		if status == result.Status && output == result.Output && errorCode == result.ErrorCode &&
			errorMessage == result.ErrorMessage && (rolledBack == 1) == result.RolledBack {
			return nil
		}
		return ErrConflict
	}
	if status == "expired" {
		return ErrExpired
	}
	if result.Status != "succeeded" && result.Status != "failed" {
		return ErrUnsupported
	}
	completedAt := result.CompletedAt.UTC()
	if completedAt.IsZero() {
		completedAt = now
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE node_operations SET status = ?, output = ?, error_code = ?,
			error_message = ?, rolled_back = ?, completed_at = ?, updated_at = ?
		WHERE id = ?
	`, result.Status, result.Output, result.ErrorCode, result.ErrorMessage,
		boolInt(result.RolledBack), completedAt.UnixMilli(), now.UnixMilli(), operationID); err != nil {
		return fmt.Errorf("update operation result: %w", err)
	}
	if result.Backup != nil {
		backup := result.Backup
		cleanBackupPath := path.Clean(backup.LocalPath)
		if !validConfigBackupPath(cleanBackupPath) ||
			cleanBackupPath != backup.LocalPath || len(backup.LocalPath) > 512 || len(backup.SHA256) != 64 ||
			strings.IndexFunc(backup.SHA256, func(character rune) bool {
				return !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f'))
			}) >= 0 || backup.SizeBytes < 0 {
			return ErrUnsupported
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO config_backups(
				id, node_id, operation_id, local_path, sha256, size_bytes, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`, uuid.NewString(), identity.NodeID, operationID, backup.LocalPath,
			backup.SHA256, backup.SizeBytes, completedAt.UnixMilli()); err != nil {
			return fmt.Errorf("record config backup: %w", err)
		}
	}
	if result.Status == "failed" {
		message := result.ErrorCode
		if result.ErrorMessage != "" {
			message += ": " + result.ErrorMessage
		}
		if err := upsertAlertTx(
			ctx, tx, identity.NodeID, "operation_failed", "warning", message, now,
		); err != nil {
			return err
		}
	} else if operationType != "" {
		var unresolvedFailures int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM node_operations failed
			WHERE failed.node_id = ?
			  AND failed.status IN ('failed', 'expired')
			  AND NOT EXISTS (
			      SELECT 1 FROM node_operations recovered
			      WHERE recovered.node_id = failed.node_id AND recovered.type = failed.type
			        AND recovered.sequence > failed.sequence AND recovered.status = 'succeeded'
			  )
		`, identity.NodeID).Scan(&unresolvedFailures); err != nil {
			return fmt.Errorf("check unresolved operation failures: %w", err)
		}
		if unresolvedFailures == 0 {
			if err := resolveAlertTx(ctx, tx, identity.NodeID, "operation_failed", now); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit operation result: %w", err)
	}
	return nil
}

func validConfigBackupPath(value string) bool {
	return path.Clean(value) == value &&
		(strings.HasPrefix(value, "/var/lib/hyfleet-backups/") ||
			strings.HasPrefix(value, "/var/lib/hyfleet-backups-lab/"))
}

func (s *Store) ListConfigBackups(ctx context.Context, nodeID string, limit int) ([]ConfigBackup, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT b.id, b.node_id, n.name, b.operation_id, b.local_path,
		       b.sha256, b.size_bytes, b.created_at
		FROM config_backups b JOIN nodes n ON n.id = b.node_id
		WHERE b.node_id = ? AND n.archived_at IS NULL
		ORDER BY b.created_at DESC LIMIT ?
	`, nodeID, limit)
	if err != nil {
		return nil, fmt.Errorf("list config backups: %w", err)
	}
	defer rows.Close()
	backups := make([]ConfigBackup, 0)
	for rows.Next() {
		var backup ConfigBackup
		var createdAt int64
		if err := rows.Scan(
			&backup.ID, &backup.NodeID, &backup.NodeName, &backup.OperationID,
			&backup.LocalPath, &backup.SHA256, &backup.SizeBytes, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan config backup: %w", err)
		}
		backup.CreatedAt = unixTime(createdAt)
		backups = append(backups, backup)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate config backups: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close config backups: %w", err)
	}
	if len(backups) == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx,
			"SELECT EXISTS(SELECT 1 FROM nodes WHERE id = ? AND archived_at IS NULL)", nodeID,
		).Scan(&exists); err != nil {
			return nil, fmt.Errorf("check backup node: %w", err)
		}
		if exists == 0 {
			return nil, ErrNotFound
		}
	}
	return backups, nil
}

func (s *Store) RetryNodeSync(ctx context.Context, nodeID string, now time.Time) (Node, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, fmt.Errorf("begin retry node sync: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var installationID, adapter string
	var enabled int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(agent_installation_id, ''), adapter_type, enabled
		FROM nodes WHERE id = ? AND archived_at IS NULL
	`, nodeID).Scan(&installationID, &adapter, &enabled); errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNotFound
	} else if err != nil {
		return Node{}, fmt.Errorf("read node for retry sync: %w", err)
	}
	if installationID == "" {
		return Node{}, ErrPending
	}
	if adapter != "native_hysteria2" && adapter != "s_ui" && adapter != AdapterSingBoxVLESSReality {
		return Node{}, ErrUnsupported
	}
	version, err := bumpNodeSnapshot(ctx, tx, nodeID, now)
	if err != nil {
		return Node{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE node_user_assignments
		SET desired_version = ?, state = 'pending', last_error_code = '',
			last_error_message = '', updated_at = ?
		WHERE node_id = ?
	`, version, now.UnixMilli(), nodeID); err != nil {
		return Node{}, fmt.Errorf("mark retry assignments pending: %w", err)
	}
	status := "pending"
	if enabled == 0 {
		status = "disabled"
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes SET status = ?, status_reason = '', updated_at = ? WHERE id = ?
	`, status, now.UnixMilli(), nodeID); err != nil {
		return Node{}, fmt.Errorf("mark retry node pending: %w", err)
	}
	if err := resolveAlertTx(ctx, tx, nodeID, "sync_failed", now); err != nil {
		return Node{}, err
	}
	if err := tx.Commit(); err != nil {
		return Node{}, fmt.Errorf("commit retry node sync: %w", err)
	}
	return s.GetNode(ctx, nodeID)
}

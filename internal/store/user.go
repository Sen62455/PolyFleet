package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/google/uuid"
)

const credentialKeyVersion = 1

type User struct {
	ID                   string
	Username             string
	DisplayName          string
	Notes                string
	Enabled              bool
	ExpiresAt            *time.Time
	TrafficLimitBytes    int64
	TrafficUploadBytes   int64
	TrafficDownloadBytes int64
	TrafficUsedBytes     int64
	QuotaState           string
	LastTrafficAt        *time.Time
	ExpiryEnforcedAt     *time.Time
	Assignments          []UserAssignment
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type UserAssignment struct {
	ID                    string
	UserID                string
	NodeID                string
	NodeName              string
	NodeAdapter           string
	Enabled               bool
	TrafficLimitBytes     int64
	TrafficUploadBytes    int64
	TrafficDownloadBytes  int64
	TrafficUsedBytes      int64
	QuotaState            string
	LastTrafficAt         *time.Time
	OnlineConnections     int
	OnlineSampledAt       *time.Time
	KickGeneration        int64
	DesiredCredentialID   string
	AppliedCredentialID   string
	CredentialFingerprint string
	CredentialProtocol    string
	ManagementMode        string
	RemoteClientID        int64
	SubscriptionEligible  bool
	SubscriptionReason    string
	DesiredVersion        int64
	AppliedVersion        int64
	State                 string
	LastErrorCode         string
	LastErrorMessage      string
	LastAttemptAt         *time.Time
	AppliedAt             *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type NewUser struct {
	ID                string
	Username          string
	DisplayName       string
	Notes             string
	Enabled           bool
	ExpiresAt         *time.Time
	TrafficLimitBytes int64
	NodeIDs           []string
	Now               time.Time
}

type UpdateUser struct {
	Username          string
	DisplayName       string
	Notes             string
	Enabled           bool
	ExpiresAt         *time.Time
	TrafficLimitBytes int64
	Now               time.Time
}

type AssignmentUpdate struct {
	Enabled           bool
	TrafficLimitBytes int64
	Now               time.Time
}

type CreatedCredential struct {
	Assignment UserAssignment
	Secret     string
}

const userColumns = `
	id, username, display_name, notes, enabled, expires_at,
	traffic_limit_bytes, traffic_upload_bytes, traffic_download_bytes,
	traffic_used_bytes, quota_state, last_traffic_at, expiry_enforced_at,
	created_at, updated_at
`

func scanUser(row rowScanner) (User, error) {
	var user User
	var enabled int
	var expiresAt sql.NullInt64
	var lastTrafficAt, expiryEnforcedAt sql.NullInt64
	var createdAt, updatedAt int64
	err := row.Scan(
		&user.ID, &user.Username, &user.DisplayName, &user.Notes, &enabled,
		&expiresAt, &user.TrafficLimitBytes, &user.TrafficUploadBytes,
		&user.TrafficDownloadBytes, &user.TrafficUsedBytes, &user.QuotaState,
		&lastTrafficAt, &expiryEnforcedAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return User{}, err
	}
	user.Enabled = enabled == 1
	user.ExpiresAt = nullableTime(expiresAt)
	user.LastTrafficAt = nullableTime(lastTrafficAt)
	user.ExpiryEnforcedAt = nullableTime(expiryEnforcedAt)
	user.CreatedAt = unixTime(createdAt)
	user.UpdatedAt = unixTime(updatedAt)
	user.Assignments = []UserAssignment{}
	return user, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+userColumns+` FROM users
		WHERE archived_at IS NULL ORDER BY username COLLATE NOCASE
	`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	users := make([]User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close users: %w", err)
	}

	userIDs := make([]string, 0, len(users))
	for _, user := range users {
		userIDs = append(userIDs, user.ID)
	}
	assignments, err := s.listAssignmentsForUsers(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	indexes := make(map[string]int, len(users))
	for index := range users {
		indexes[users[index].ID] = index
	}
	for _, assignment := range assignments {
		if index, ok := indexes[assignment.UserID]; ok {
			users[index].Assignments = append(users[index].Assignments, assignment)
		}
	}
	return users, nil
}

func (s *Store) ListUsersPage(
	ctx context.Context,
	search string,
	limit, offset int,
) ([]User, int, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	where := "archived_at IS NULL"
	args := make([]any, 0, 4)
	if search = strings.TrimSpace(search); search != "" {
		where += ` AND (username LIKE ? ESCAPE '\' OR display_name LIKE ? ESCAPE '\' OR notes LIKE ? ESCAPE '\')`
		pattern := "%" + escapeLike(search) + "%"
		args = append(args, pattern, pattern, pattern)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}
	queryArgs := append(append([]any(nil), args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+userColumns+` FROM users WHERE `+where+`
		ORDER BY username COLLATE NOCASE LIMIT ? OFFSET ?
	`, queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list user page: %w", err)
	}
	users := make([]User, 0, limit)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			_ = rows.Close()
			return nil, 0, fmt.Errorf("scan user page: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, 0, fmt.Errorf("iterate user page: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, fmt.Errorf("close user page: %w", err)
	}
	if len(users) == 0 {
		return users, total, nil
	}
	userIDs := make([]string, 0, len(users))
	for _, user := range users {
		userIDs = append(userIDs, user.ID)
	}
	assignments, err := s.listAssignmentsForUsers(ctx, userIDs)
	if err != nil {
		return nil, 0, err
	}
	indexes := make(map[string]int, len(users))
	for index := range users {
		indexes[users[index].ID] = index
	}
	for _, assignment := range assignments {
		if index, ok := indexes[assignment.UserID]; ok {
			users[index].Assignments = append(users[index].Assignments, assignment)
		}
	}
	return users, total, nil
}

func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	user, err := scanUser(s.db.QueryRowContext(ctx, `
		SELECT `+userColumns+` FROM users
		WHERE id = ? AND archived_at IS NULL
	`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get user: %w", err)
	}
	user.Assignments, err = s.listAssignments(ctx, id)
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *Store) CreateUser(
	ctx context.Context,
	input NewUser,
	masterKey []byte,
) (User, []CreatedCredential, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, nil, fmt.Errorf("begin create user: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	expiresAt := nullableUnixMilli(input.ExpiresAt)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users(
			id, username, display_name, notes, enabled, expires_at,
			traffic_limit_bytes, quota_state, expiry_enforced_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, input.ID, input.Username, input.DisplayName, input.Notes, boolInt(input.Enabled),
		expiresAt, input.TrafficLimitBytes, quotaState(input.TrafficLimitBytes, 0),
		expiryEnforcedValue(input.ExpiresAt, input.Now),
		input.Now.UnixMilli(), input.Now.UnixMilli()); err != nil {
		return User{}, nil, fmt.Errorf("%w: insert user: %v", ErrConflict, err)
	}

	credentials := make([]CreatedCredential, 0, len(input.NodeIDs))
	for _, nodeID := range input.NodeIDs {
		created, err := assignUserTx(ctx, tx, input.ID, nodeID, 0, input.Now, masterKey)
		if err != nil {
			return User{}, nil, err
		}
		credentials = append(credentials, created)
	}
	if err := tx.Commit(); err != nil {
		return User{}, nil, fmt.Errorf("commit create user: %w", err)
	}
	user, err := s.GetUser(ctx, input.ID)
	if err != nil {
		return User{}, nil, err
	}
	return user, credentials, nil
}

func (s *Store) UpdateUser(ctx context.Context, id string, input UpdateUser) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin update user: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := updateUserTx(ctx, tx, id, input); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit update user: %w", err)
	}
	return s.GetUser(ctx, id)
}

func updateUserTx(ctx context.Context, tx *sql.Tx, id string, input UpdateUser) error {
	var currentEnabled int
	var currentExpiryEnforcedAt sql.NullInt64
	var trafficUsed int64
	if err := tx.QueryRowContext(ctx, `
		SELECT enabled, expiry_enforced_at, traffic_used_bytes
		FROM users WHERE id = ? AND archived_at IS NULL
	`, id).Scan(
		&currentEnabled, &currentExpiryEnforcedAt, &trafficUsed,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read current user state: %w", err)
	}
	nodeIDs, err := assignmentNodeIDs(ctx, tx, id)
	if err != nil {
		return err
	}
	beforeQuota, err := effectiveQuotaStatesTx(ctx, tx, []string{id})
	if err != nil {
		return err
	}
	expiryValue := expiryEnforcedValue(input.ExpiresAt, input.Now)
	if expiryValue != nil && currentExpiryEnforcedAt.Valid {
		expiryValue = currentExpiryEnforcedAt.Int64
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE users SET username = ?, display_name = ?, notes = ?, enabled = ?,
			expires_at = ?, traffic_limit_bytes = ?, quota_state = ?,
			expiry_enforced_at = ?, updated_at = ?
		WHERE id = ? AND archived_at IS NULL
	`, input.Username, input.DisplayName, input.Notes, boolInt(input.Enabled),
		nullableUnixMilli(input.ExpiresAt), input.TrafficLimitBytes,
		quotaState(input.TrafficLimitBytes, trafficUsed), expiryValue,
		input.Now.UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("%w: update user: %v", ErrConflict, err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read update user result: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	afterQuota, err := effectiveQuotaStatesTx(ctx, tx, []string{id})
	if err != nil {
		return err
	}
	disableKick := currentEnabled == 1 && !input.Enabled
	expiredNow := input.ExpiresAt != nil && !input.Now.Before(input.ExpiresAt.UTC())
	expiryKick := expiredNow && !currentExpiryEnforcedAt.Valid
	changed := make(map[string]map[string]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		reason := ""
		if disableKick {
			reason = "user_disabled"
		} else if expiryKick {
			reason = "expired"
		} else {
			key := quotaStateKey(nodeID, id)
			before := beforeQuota[key]
			after := afterQuota[key]
			if !before.Limited && after.Limited {
				reason = "quota_limited"
			}
		}
		if reason != "" {
			if err := requestKickTx(ctx, tx, nodeID, id, reason, input.Now); err != nil {
				return err
			}
		}
		addChangedAssignment(changed, nodeID, id)
	}
	if err := bumpChangedAssignmentsTx(ctx, tx, changed, input.Now); err != nil {
		return err
	}
	return nil
}

func (s *Store) UpdateAssignment(
	ctx context.Context,
	userID, nodeID string,
	input AssignmentUpdate,
) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin update assignment: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var currentEnabled int
	var trafficUsed int64
	var managementMode, adapter string
	if err := tx.QueryRowContext(ctx, `
		SELECT a.enabled, a.traffic_used_bytes, a.management_mode, n.adapter_type
		FROM node_user_assignments a
		JOIN nodes n ON n.id = a.node_id AND n.archived_at IS NULL
		WHERE a.user_id = ? AND a.node_id = ?
	`, userID, nodeID).Scan(&currentEnabled, &trafficUsed, &managementMode, &adapter); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("read assignment state: %w", err)
	}
	if managementMode == "read_only" {
		return User{}, ErrReadOnly
	}
	beforeQuota, err := effectiveQuotaStatesTx(ctx, tx, []string{userID})
	if err != nil {
		return User{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE node_user_assignments
		SET enabled = ?, traffic_limit_bytes = ?, quota_state = ?, updated_at = ?
		WHERE user_id = ? AND node_id = ?
	`, boolInt(input.Enabled), input.TrafficLimitBytes,
		quotaState(input.TrafficLimitBytes, trafficUsed), input.Now.UnixMilli(),
		userID, nodeID); err != nil {
		return User{}, fmt.Errorf("update assignment: %w", err)
	}
	afterQuota, err := effectiveQuotaStatesTx(ctx, tx, []string{userID})
	if err != nil {
		return User{}, err
	}
	reason := ""
	if currentEnabled == 1 && !input.Enabled {
		reason = "assignment_disabled"
	} else {
		key := quotaStateKey(nodeID, userID)
		if !beforeQuota[key].Limited && afterQuota[key].Limited {
			reason = "quota_limited"
		}
	}
	if reason != "" {
		if err := requestKickTx(ctx, tx, nodeID, userID, reason, input.Now); err != nil {
			return User{}, err
		}
	}
	version, err := bumpNodeSnapshot(ctx, tx, nodeID, input.Now)
	if err != nil {
		return User{}, err
	}
	if err := markAssignmentPending(ctx, tx, userID, nodeID, version, input.Now); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit update assignment: %w", err)
	}
	return s.GetUser(ctx, userID)
}

func (s *Store) ArchiveUser(ctx context.Context, id string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin archive user: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	nodeIDs, err := assignmentNodeIDs(ctx, tx, id)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE users SET enabled = 0, archived_at = ?, updated_at = ?
		WHERE id = ? AND archived_at IS NULL
	`, now.UnixMilli(), now.UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("archive user: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read archive user result: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_credentials SET state = 'revoked', revoked_at = ?
		WHERE user_id = ? AND state IN ('staged', 'applied')
	`, now.UnixMilli(), id); err != nil {
		return fmt.Errorf("revoke archived user credentials: %w", err)
	}
	for _, nodeID := range nodeIDs {
		if err := requestKickTx(ctx, tx, nodeID, id, "user_archived", now); err != nil {
			return err
		}
		version, err := bumpNodeSnapshot(ctx, tx, nodeID, now)
		if err != nil {
			return err
		}
		if err := markAssignmentPending(ctx, tx, id, nodeID, version, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit archive user: %w", err)
	}
	return nil
}

func (s *Store) AssignUser(
	ctx context.Context,
	userID, nodeID string,
	trafficLimitBytes int64,
	now time.Time,
	masterKey []byte,
) (User, CreatedCredential, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, CreatedCredential{}, fmt.Errorf("begin assign user: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var exists int
	if err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM users WHERE id = ? AND archived_at IS NULL
	`, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, CreatedCredential{}, ErrNotFound
		}
		return User{}, CreatedCredential{}, fmt.Errorf("find assignment user: %w", err)
	}
	credential, err := assignUserTx(ctx, tx, userID, nodeID, trafficLimitBytes, now, masterKey)
	if err != nil {
		return User{}, CreatedCredential{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, CreatedCredential{}, fmt.Errorf("commit assign user: %w", err)
	}
	user, err := s.GetUser(ctx, userID)
	return user, credential, err
}

func (s *Store) SetAssignmentEnabled(
	ctx context.Context,
	userID, nodeID string,
	enabled bool,
	now time.Time,
) (User, error) {
	user, err := s.GetUser(ctx, userID)
	if err != nil {
		return User{}, err
	}
	limit := int64(0)
	found := false
	for _, assignment := range user.Assignments {
		if assignment.NodeID == nodeID {
			limit = assignment.TrafficLimitBytes
			found = true
			break
		}
	}
	if !found {
		return User{}, ErrNotFound
	}
	return s.UpdateAssignment(ctx, userID, nodeID, AssignmentUpdate{
		Enabled: enabled, TrafficLimitBytes: limit, Now: now,
	})
}

func (s *Store) RotateAssignmentCredential(
	ctx context.Context,
	userID, nodeID string,
	now time.Time,
	masterKey []byte,
) (User, CreatedCredential, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, CreatedCredential{}, fmt.Errorf("begin rotate assignment credential: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	credential, err := rotateAssignmentCredentialTx(ctx, tx, userID, nodeID, now, masterKey)
	if err != nil {
		return User{}, CreatedCredential{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, CreatedCredential{}, fmt.Errorf("commit rotated assignment credential: %w", err)
	}
	user, err := s.GetUser(ctx, userID)
	if err != nil {
		return User{}, CreatedCredential{}, err
	}
	for _, assignment := range user.Assignments {
		if assignment.NodeID == nodeID {
			credential.Assignment = assignment
			break
		}
	}
	return user, credential, nil
}

func (s *Store) RotateUserCredentials(
	ctx context.Context,
	userID string,
	now time.Time,
	masterKey []byte,
) (User, []CreatedCredential, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, nil, fmt.Errorf("begin rotate user credentials: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
		SELECT a.node_id
		FROM node_user_assignments a
		JOIN users u ON u.id = a.user_id AND u.archived_at IS NULL
		JOIN nodes n ON n.id = a.node_id AND n.archived_at IS NULL
		WHERE a.user_id = ? AND a.management_mode = 'managed'
		ORDER BY n.name COLLATE NOCASE, n.id
	`, userID)
	if err != nil {
		return User{}, nil, fmt.Errorf("list credential rotation targets: %w", err)
	}
	nodeIDs := make([]string, 0)
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			_ = rows.Close()
			return User{}, nil, fmt.Errorf("scan credential rotation target: %w", err)
		}
		nodeIDs = append(nodeIDs, nodeID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return User{}, nil, fmt.Errorf("iterate credential rotation targets: %w", err)
	}
	if err := rows.Close(); err != nil {
		return User{}, nil, fmt.Errorf("close credential rotation targets: %w", err)
	}
	if len(nodeIDs) == 0 {
		var exists int
		if err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM users WHERE id = ? AND archived_at IS NULL
		`, userID).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return User{}, nil, ErrNotFound
			}
			return User{}, nil, fmt.Errorf("find credential rotation user: %w", err)
		}
	}
	credentials := make([]CreatedCredential, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		credential, err := rotateAssignmentCredentialTx(
			ctx, tx, userID, nodeID, now, masterKey,
		)
		if err != nil {
			return User{}, nil, err
		}
		credentials = append(credentials, credential)
	}
	if err := tx.Commit(); err != nil {
		return User{}, nil, fmt.Errorf("commit rotated user credentials: %w", err)
	}
	user, err := s.GetUser(ctx, userID)
	if err != nil {
		return User{}, nil, err
	}
	assignments := make(map[string]UserAssignment, len(user.Assignments))
	for _, assignment := range user.Assignments {
		assignments[assignment.NodeID] = assignment
	}
	for index := range credentials {
		credentials[index].Assignment = assignments[credentials[index].Assignment.NodeID]
	}
	return user, credentials, nil
}

func (s *Store) UnassignUser(ctx context.Context, userID, nodeID string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin unassign user: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var credentialID string
	if err := tx.QueryRowContext(ctx, `
		SELECT desired_credential_id FROM node_user_assignments
		WHERE user_id = ? AND node_id = ?
	`, userID, nodeID).Scan(&credentialID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("read removed assignment: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM node_user_assignments WHERE user_id = ? AND node_id = ?
	`, userID, nodeID); err != nil {
		return fmt.Errorf("delete assignment: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE user_credentials SET state = 'revoked', revoked_at = ? WHERE id = ?
	`, now.UnixMilli(), credentialID); err != nil {
		return fmt.Errorf("revoke removed credential: %w", err)
	}
	if err := requestKickTx(ctx, tx, nodeID, userID, "assignment_removed", now); err != nil {
		return err
	}
	if _, err := bumpNodeSnapshot(ctx, tx, nodeID, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit unassign user: %w", err)
	}
	return nil
}

func (s *Store) RevealAssignmentCredential(
	ctx context.Context,
	userID, nodeID string,
	masterKey []byte,
) (CreatedCredential, error) {
	var assignment UserAssignment
	var ciphertext []byte
	var keyVersion int
	var state, credentialProtocol string
	var createdAt, updatedAt int64
	var enabled int
	err := s.db.QueryRowContext(ctx, `
		SELECT a.id, a.user_id, a.node_id, n.name, n.adapter_type, a.enabled,
		       a.desired_credential_id, COALESCE(a.applied_credential_id, ''),
		       c.secret_fingerprint, a.management_mode, COALESCE(a.remote_client_id, 0),
		       a.desired_version, a.applied_version, a.state,
		       a.last_error_code, a.last_error_message, a.last_attempt_at, a.applied_at,
		       a.traffic_limit_bytes, a.traffic_upload_bytes, a.traffic_download_bytes,
		       a.traffic_used_bytes, a.quota_state, a.last_traffic_at,
		       COALESCE(o.connections, 0), o.sampled_at, COALESCE(k.generation, 0),
		       a.created_at, a.updated_at, c.secret_ciphertext, c.key_version, c.state,
		       c.protocol
		FROM node_user_assignments a
		JOIN users u ON u.id = a.user_id AND u.archived_at IS NULL
		JOIN nodes n ON n.id = a.node_id AND n.archived_at IS NULL
		JOIN user_credentials c ON c.id = a.desired_credential_id
		LEFT JOIN node_online_users o ON o.node_id = a.node_id AND o.user_id = a.user_id
		LEFT JOIN node_kick_targets k ON k.node_id = a.node_id AND k.user_id = a.user_id
		WHERE a.user_id = ? AND a.node_id = ?
	`, userID, nodeID).Scan(
		&assignment.ID, &assignment.UserID, &assignment.NodeID, &assignment.NodeName,
		&assignment.NodeAdapter, &enabled, &assignment.DesiredCredentialID,
		&assignment.AppliedCredentialID, &assignment.CredentialFingerprint,
		&assignment.ManagementMode, &assignment.RemoteClientID,
		&assignment.DesiredVersion, &assignment.AppliedVersion, &assignment.State,
		&assignment.LastErrorCode, &assignment.LastErrorMessage,
		newNullableTimeScanner(&assignment.LastAttemptAt), newNullableTimeScanner(&assignment.AppliedAt),
		&assignment.TrafficLimitBytes, &assignment.TrafficUploadBytes,
		&assignment.TrafficDownloadBytes, &assignment.TrafficUsedBytes,
		&assignment.QuotaState, newNullableTimeScanner(&assignment.LastTrafficAt),
		&assignment.OnlineConnections, newNullableTimeScanner(&assignment.OnlineSampledAt),
		&assignment.KickGeneration,
		&createdAt, &updatedAt, &ciphertext, &keyVersion, &state, &credentialProtocol,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CreatedCredential{}, ErrNotFound
	}
	if err != nil {
		return CreatedCredential{}, fmt.Errorf("read assignment credential: %w", err)
	}
	if assignment.ManagementMode == "read_only" {
		return CreatedCredential{}, ErrReadOnly
	}
	if keyVersion != credentialKeyVersion || (state != "staged" && state != "applied") {
		return CreatedCredential{}, ErrConflict
	}
	secret, err := cryptoutil.Open(masterKey, ciphertext, credentialAAD(
		assignment.DesiredCredentialID, userID, nodeID, credentialProtocol, keyVersion,
	))
	if err != nil {
		return CreatedCredential{}, fmt.Errorf("open assignment credential: %w", err)
	}
	assignment.Enabled = enabled == 1
	assignment.CredentialProtocol = credentialProtocol
	assignment.CreatedAt = unixTime(createdAt)
	assignment.UpdatedAt = unixTime(updatedAt)
	return CreatedCredential{Assignment: assignment, Secret: string(secret)}, nil
}

func assignUserTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, nodeID string,
	trafficLimitBytes int64,
	now time.Time,
	masterKey []byte,
) (CreatedCredential, error) {
	return assignUserTxWithMode(
		ctx, tx, userID, nodeID, trafficLimitBytes, "managed", 0, now, masterKey,
	)
}

func assignUserTxWithMode(
	ctx context.Context,
	tx *sql.Tx,
	userID, nodeID string,
	trafficLimitBytes int64,
	managementMode string,
	remoteClientID int64,
	now time.Time,
	masterKey []byte,
) (CreatedCredential, error) {
	var nodeName, adapter, targetInboundJSON string
	if err := tx.QueryRowContext(ctx, `
		SELECT name, adapter_type, sui_target_inbound_ids
		FROM nodes WHERE id = ? AND archived_at IS NULL
	`, nodeID).Scan(&nodeName, &adapter, &targetInboundJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CreatedCredential{}, ErrNotFound
		}
		return CreatedCredential{}, fmt.Errorf("find assignment node: %w", err)
	}
	if !supportsManagedUsers(adapter) {
		return CreatedCredential{}, ErrUnsupported
	}
	if managementMode != "managed" && managementMode != "read_only" {
		return CreatedCredential{}, ErrUnsupported
	}
	if adapter == "native_hysteria2" && (managementMode != "managed" || remoteClientID != 0) {
		return CreatedCredential{}, ErrUnsupported
	}
	if adapter == AdapterSingBoxVLESSReality &&
		(managementMode != "managed" || remoteClientID != 0) {
		return CreatedCredential{}, ErrUnsupported
	}
	if adapter == "s_ui" && managementMode == "managed" && remoteClientID == 0 {
		var targetInboundIDs []int64
		if err := json.Unmarshal([]byte(targetInboundJSON), &targetInboundIDs); err != nil {
			return CreatedCredential{}, fmt.Errorf("decode S-UI target inbounds: %w", err)
		}
		if len(targetInboundIDs) == 0 {
			return CreatedCredential{}, ErrConflict
		}
	}
	credentialProtocol, err := credentialProtocolForAdapter(adapter)
	if err != nil {
		return CreatedCredential{}, err
	}
	credentialID, secret, fingerprint, err := createUserCredentialTx(
		ctx, tx, userID, nodeID, credentialProtocol, now, masterKey,
	)
	if err != nil {
		return CreatedCredential{}, err
	}
	assignmentID := cryptoutil.NewID()
	var priorUpload, priorDownload int64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE((
			SELECT upload_bytes FROM traffic_totals WHERE node_id = ? AND user_id = ?
		), 0), COALESCE((
			SELECT download_bytes FROM traffic_totals WHERE node_id = ? AND user_id = ?
		), 0)
	`, nodeID, userID, nodeID, userID).Scan(&priorUpload, &priorDownload); err != nil {
		return CreatedCredential{}, fmt.Errorf("read prior assignment traffic: %w", err)
	}
	priorUsed, ok := checkedAdd(priorUpload, priorDownload)
	if !ok {
		return CreatedCredential{}, errors.New("prior assignment traffic exceeds integer range")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO node_user_assignments(
			id, node_id, user_id, desired_credential_id, enabled, state,
			management_mode, remote_client_id,
			traffic_limit_bytes, quota_state,
			traffic_upload_bytes, traffic_download_bytes, traffic_used_bytes,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, 1, 'pending', ?, NULLIF(?, 0), ?, ?, ?, ?, ?, ?, ?)
	`, assignmentID, nodeID, userID, credentialID, managementMode, remoteClientID,
		trafficLimitBytes,
		quotaState(trafficLimitBytes, priorUsed), priorUpload, priorDownload,
		priorUsed, now.UnixMilli(), now.UnixMilli()); err != nil {
		return CreatedCredential{}, fmt.Errorf("%w: user is already assigned to node", ErrConflict)
	}
	version, err := bumpNodeSnapshot(ctx, tx, nodeID, now)
	if err != nil {
		return CreatedCredential{}, err
	}
	if err := markAssignmentPending(ctx, tx, userID, nodeID, version, now); err != nil {
		return CreatedCredential{}, err
	}
	return CreatedCredential{
		Assignment: UserAssignment{
			ID: assignmentID, UserID: userID, NodeID: nodeID, NodeName: nodeName,
			NodeAdapter: adapter, Enabled: true, TrafficLimitBytes: trafficLimitBytes,
			TrafficUploadBytes: priorUpload, TrafficDownloadBytes: priorDownload,
			TrafficUsedBytes: priorUsed, QuotaState: quotaState(trafficLimitBytes, priorUsed),
			DesiredCredentialID:   credentialID,
			CredentialFingerprint: fingerprint, DesiredVersion: version, State: "pending",
			CredentialProtocol: credentialProtocol,
			ManagementMode:     managementMode, RemoteClientID: remoteClientID,
			CreatedAt: now, UpdatedAt: now,
		},
		Secret: secret,
	}, nil
}

func rotateAssignmentCredentialTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, nodeID string,
	now time.Time,
	masterKey []byte,
) (CreatedCredential, error) {
	var assignmentID, nodeName, adapter, desiredCredentialID, appliedCredentialID, state string
	var managementMode string
	var desiredVersion, appliedVersion int64
	err := tx.QueryRowContext(ctx, `
		SELECT a.id, n.name, n.adapter_type, a.desired_credential_id,
		       COALESCE(a.applied_credential_id, ''), a.state,
		       a.desired_version, a.applied_version, a.management_mode
		FROM node_user_assignments a
		JOIN users u ON u.id = a.user_id AND u.archived_at IS NULL
		JOIN nodes n ON n.id = a.node_id AND n.archived_at IS NULL
		WHERE a.user_id = ? AND a.node_id = ?
	`, userID, nodeID).Scan(
		&assignmentID, &nodeName, &adapter, &desiredCredentialID,
		&appliedCredentialID, &state, &desiredVersion, &appliedVersion, &managementMode,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CreatedCredential{}, ErrNotFound
	}
	if err != nil {
		return CreatedCredential{}, fmt.Errorf("read credential rotation assignment: %w", err)
	}
	if managementMode == "read_only" {
		return CreatedCredential{}, ErrReadOnly
	}
	if !supportsManagedUsers(adapter) || managementMode != "managed" {
		return CreatedCredential{}, ErrUnsupported
	}
	if state != "applied" || appliedVersion < desiredVersion ||
		desiredCredentialID == "" || desiredCredentialID != appliedCredentialID {
		return CreatedCredential{}, ErrPending
	}
	credentialProtocol, err := credentialProtocolForAdapter(adapter)
	if err != nil {
		return CreatedCredential{}, err
	}
	credentialID, secret, fingerprint, err := createUserCredentialTx(
		ctx, tx, userID, nodeID, credentialProtocol, now, masterKey,
	)
	if err != nil {
		return CreatedCredential{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE node_user_assignments
		SET desired_credential_id = ?, state = 'pending', last_error_code = '',
		    last_error_message = '', updated_at = ?
		WHERE id = ?
	`, credentialID, now.UnixMilli(), assignmentID); err != nil {
		return CreatedCredential{}, fmt.Errorf("stage rotated assignment credential: %w", err)
	}
	version, err := bumpNodeSnapshot(ctx, tx, nodeID, now)
	if err != nil {
		return CreatedCredential{}, err
	}
	if err := markAssignmentPending(ctx, tx, userID, nodeID, version, now); err != nil {
		return CreatedCredential{}, err
	}
	return CreatedCredential{Assignment: UserAssignment{
		ID: assignmentID, UserID: userID, NodeID: nodeID, NodeName: nodeName,
		NodeAdapter: adapter, DesiredCredentialID: credentialID,
		AppliedCredentialID: appliedCredentialID, CredentialFingerprint: fingerprint,
		CredentialProtocol: credentialProtocol,
		ManagementMode:     managementMode,
		DesiredVersion:     version, AppliedVersion: appliedVersion, State: "pending",
		UpdatedAt: now,
	}, Secret: secret}, nil
}

func createUserCredentialTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, nodeID, credentialProtocol string,
	now time.Time,
	masterKey []byte,
) (string, string, string, error) {
	var secret string
	var err error
	switch credentialProtocol {
	case CredentialProtocolHY2:
		secret, err = cryptoutil.RandomToken(32)
	case CredentialProtocolVLESS:
		var generated uuid.UUID
		generated, err = uuid.NewRandom()
		secret = generated.String()
	default:
		return "", "", "", ErrUnsupported
	}
	if err != nil {
		return "", "", "", err
	}
	verifier := sha256.Sum256([]byte(secret))
	credentialID := cryptoutil.NewID()
	ciphertext, err := cryptoutil.Seal(masterKey, []byte(secret), credentialAAD(
		credentialID, userID, nodeID, credentialProtocol, credentialKeyVersion,
	))
	if err != nil {
		return "", "", "", err
	}
	fingerprint := "fp_" + base64.RawURLEncoding.EncodeToString(verifier[:6])
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_credentials(
			id, user_id, node_id, protocol, secret_ciphertext, verifier_sha256,
			secret_fingerprint, key_version, state, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'staged', ?)
	`, credentialID, userID, nodeID, credentialProtocol, ciphertext, verifier[:], fingerprint,
		credentialKeyVersion, now.UnixMilli()); err != nil {
		return "", "", "", fmt.Errorf("insert user credential: %w", err)
	}
	return credentialID, secret, fingerprint, nil
}

func (s *Store) listAssignments(ctx context.Context, userID string) ([]UserAssignment, error) {
	if userID == "" {
		return s.listAssignmentsForUsers(ctx, nil)
	}
	return s.listAssignmentsForUsers(ctx, []string{userID})
}

func (s *Store) listAssignmentsForUsers(ctx context.Context, userIDs []string) ([]UserAssignment, error) {
	query := `
		SELECT a.id, a.user_id, a.node_id, n.name, n.adapter_type, a.enabled,
		       a.desired_credential_id, COALESCE(a.applied_credential_id, ''),
		       c.secret_fingerprint, c.protocol, a.management_mode,
		       COALESCE(a.remote_client_id, 0),
		       a.desired_version, a.applied_version, a.state,
		       a.last_error_code, a.last_error_message, a.last_attempt_at, a.applied_at,
		       a.traffic_limit_bytes, a.traffic_upload_bytes, a.traffic_download_bytes,
		       a.traffic_used_bytes, a.quota_state, a.last_traffic_at,
		       COALESCE(o.connections, 0), o.sampled_at, COALESCE(k.generation, 0),
		       n.enabled, n.status, n.public_host, COALESCE(ac.state, ''),
		       n.adapter_status, n.core_running, n.applied_version,
		       CASE
		         WHEN n.adapter_type <> 'sing_box_vless_reality' THEN 1
		         WHEN r.public_key <> '' AND r.short_id <> ''
		          AND r.applied_key_generation = r.desired_key_generation
		          AND r.material_applied_version = a.applied_version THEN 1
		         ELSE 0
		       END,
		       a.created_at, a.updated_at
		FROM node_user_assignments a
		JOIN nodes n ON n.id = a.node_id
		JOIN user_credentials c ON c.id = a.desired_credential_id
		LEFT JOIN user_credentials ac ON ac.id = a.applied_credential_id
		LEFT JOIN node_online_users o ON o.node_id = a.node_id AND o.user_id = a.user_id
		LEFT JOIN node_kick_targets k ON k.node_id = a.node_id AND k.user_id = a.user_id
		LEFT JOIN node_vless_reality r ON r.node_id = a.node_id
	`
	arguments := make([]any, 0, len(userIDs))
	if len(userIDs) > 0 {
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(userIDs)), ",")
		query += " WHERE a.user_id IN (" + placeholders + ")"
		for _, userID := range userIDs {
			arguments = append(arguments, userID)
		}
	}
	query += " ORDER BY n.name COLLATE NOCASE"
	rows, err := s.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list user assignments: %w", err)
	}
	defer rows.Close()
	assignments := make([]UserAssignment, 0)
	for rows.Next() {
		var assignment UserAssignment
		var enabled int
		var nodeEnabled int
		var coreRunning int
		var nodeAppliedVersion int64
		var realityReady int
		var nodeStatus, publicHost, appliedCredentialState, adapterStatus string
		var createdAt, updatedAt int64
		var lastAttemptAt, appliedAt, lastTrafficAt, onlineSampledAt sql.NullInt64
		if err := rows.Scan(
			&assignment.ID, &assignment.UserID, &assignment.NodeID, &assignment.NodeName,
			&assignment.NodeAdapter, &enabled, &assignment.DesiredCredentialID,
			&assignment.AppliedCredentialID, &assignment.CredentialFingerprint,
			&assignment.CredentialProtocol, &assignment.ManagementMode,
			&assignment.RemoteClientID,
			&assignment.DesiredVersion, &assignment.AppliedVersion, &assignment.State,
			&assignment.LastErrorCode, &assignment.LastErrorMessage,
			&lastAttemptAt, &appliedAt, &assignment.TrafficLimitBytes,
			&assignment.TrafficUploadBytes, &assignment.TrafficDownloadBytes,
			&assignment.TrafficUsedBytes, &assignment.QuotaState, &lastTrafficAt,
			&assignment.OnlineConnections, &onlineSampledAt, &assignment.KickGeneration,
			&nodeEnabled, &nodeStatus, &publicHost, &appliedCredentialState,
			&adapterStatus, &coreRunning, &nodeAppliedVersion,
			&realityReady,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan user assignment: %w", err)
		}
		assignment.Enabled = enabled == 1
		assignment.LastAttemptAt = nullableTime(lastAttemptAt)
		assignment.AppliedAt = nullableTime(appliedAt)
		assignment.LastTrafficAt = nullableTime(lastTrafficAt)
		assignment.OnlineSampledAt = nullableTime(onlineSampledAt)
		assignment.SubscriptionReason = subscriptionEligibilityReason(
			assignment, nodeEnabled == 1, realityReady == 1, coreRunning == 1,
			nodeAppliedVersion, nodeStatus, publicHost, appliedCredentialState, adapterStatus,
		)
		assignment.SubscriptionEligible = assignment.SubscriptionReason == ""
		assignment.CreatedAt = unixTime(createdAt)
		assignment.UpdatedAt = unixTime(updatedAt)
		assignments = append(assignments, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user assignments: %w", err)
	}
	return assignments, nil
}

func subscriptionEligibilityReason(
	assignment UserAssignment,
	nodeEnabled, realityReady, coreRunning bool,
	nodeAppliedVersion int64,
	nodeStatus, publicHost, appliedCredentialState, adapterStatus string,
) string {
	switch {
	case assignment.ManagementMode != "managed":
		return "read_only_requires_adoption"
	case !supportsManagedUsers(assignment.NodeAdapter):
		return "adapter_not_supported"
	case !nodeEnabled:
		return "node_disabled"
	case assignment.NodeAdapter == AdapterSingBoxVLESSReality && adapterStatus != "compatible":
		return "adapter_not_compatible"
	case nodeStatus == "pending" || nodeStatus == "degraded" || nodeStatus == "disabled":
		return "node_not_ready"
	case publicHost == "":
		return "endpoint_missing"
	case assignment.NodeAdapter == AdapterSingBoxVLESSReality && !coreRunning:
		return "core_not_running"
	case assignment.NodeAdapter == AdapterSingBoxVLESSReality &&
		(assignment.CredentialProtocol != CredentialProtocolVLESS ||
			assignment.AppliedCredentialID == "" ||
			assignment.AppliedCredentialID != assignment.DesiredCredentialID ||
			assignment.AppliedVersion != nodeAppliedVersion):
		return "applied_state_mismatch"
	case assignment.NodeAdapter == AdapterSingBoxVLESSReality && !realityReady:
		return "reality_material_missing"
	case !assignment.Enabled:
		return "assignment_disabled"
	case assignment.QuotaState == "limited":
		return "assignment_quota_limited"
	case assignment.State != "applied" || assignment.AppliedVersion < assignment.DesiredVersion:
		return "assignment_not_applied"
	case assignment.AppliedCredentialID == "" || appliedCredentialState != "applied":
		return "credential_not_applied"
	default:
		return ""
	}
}

func assignmentNodeIDs(ctx context.Context, tx *sql.Tx, userID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT node_id FROM node_user_assignments WHERE user_id = ? ORDER BY node_id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list assignment nodes: %w", err)
	}
	defer rows.Close()
	nodeIDs := make([]string, 0)
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			return nil, fmt.Errorf("scan assignment node: %w", err)
		}
		nodeIDs = append(nodeIDs, nodeID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate assignment nodes: %w", err)
	}
	return nodeIDs, nil
}

func markAssignmentPending(
	ctx context.Context,
	tx *sql.Tx,
	userID, nodeID string,
	version int64,
	now time.Time,
) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE node_user_assignments
		SET desired_version = ?, state = 'pending', last_error_code = '',
			last_error_message = '', updated_at = ?
		WHERE user_id = ? AND node_id = ?
	`, version, now.UnixMilli(), userID, nodeID); err != nil {
		return fmt.Errorf("mark assignment pending: %w", err)
	}
	return nil
}

func credentialAAD(
	credentialID, userID, nodeID, protocol string,
	keyVersion int,
) []byte {
	return []byte(strings.Join([]string{
		credentialID, userID, nodeID, protocol, fmt.Sprintf("v%d", keyVersion),
	}, "\x00"))
}

func nullableUnixMilli(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().UnixMilli()
}

type nullableTimeScanner struct {
	destination **time.Time
}

func newNullableTimeScanner(destination **time.Time) *nullableTimeScanner {
	return &nullableTimeScanner{destination: destination}
}

func (scanner *nullableTimeScanner) Scan(value any) error {
	if value == nil {
		*scanner.destination = nil
		return nil
	}
	var milliseconds int64
	switch typed := value.(type) {
	case int64:
		milliseconds = typed
	case int:
		milliseconds = int64(typed)
	default:
		return fmt.Errorf("unsupported time value %T", value)
	}
	result := unixTime(milliseconds)
	*scanner.destination = &result
	return nil
}

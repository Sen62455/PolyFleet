package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

type SUIInbound struct {
	RemoteID   int64
	Tag        string
	Type       string
	Listen     string
	ListenPort int
	ObservedAt time.Time
}

type SUIClient struct {
	RemoteID       int64
	Name           string
	Enabled        bool
	InboundIDs     []int64
	UploadBytes    int64
	DownloadBytes  int64
	ExpiresAt      int64
	Online         bool
	ObservedAt     time.Time
	MappedUserID   string
	MappedUsername string
	ManagementMode string
}

type SUIState struct {
	NodeID           string
	AdapterStatus    string
	AdapterVersion   string
	AdapterErrorCode string
	LastProbedAt     *time.Time
	LastDiscoveredAt *time.Time
	TargetInboundIDs []int64
	Inbounds         []SUIInbound
	Clients          []SUIClient
}

func (s *Store) RecordSUIReport(
	ctx context.Context,
	identity AgentIdentity,
	report protocol.SUIReportRequest,
	now time.Time,
) error {
	if identity.AdapterType != "s_ui" || identity.InstallationID != report.InstallationID {
		return ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin S-UI report: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	status := "online"
	statusReason := ""
	if !identity.Enabled {
		status = "disabled"
	} else if report.Adapter.Status == "incompatible" ||
		report.Adapter.Status == "unavailable" || report.Adapter.Status == "not_configured" {
		status = "degraded"
		statusReason = report.Adapter.ErrorCode
	}
	probedAt := nullableUnixMilli(report.Adapter.LastProbedAt)
	result, err := tx.ExecContext(ctx, `
		UPDATE nodes SET adapter_status = ?, adapter_version = ?, adapter_error_code = ?,
			adapter_last_probed_at = ?, core_name = ?, core_version = ?, core_running = ?,
			status = ?, status_reason = ?, updated_at = ?
		WHERE id = ? AND agent_installation_id = ? AND adapter_type = 's_ui'
	`, report.Adapter.Status, report.Adapter.Version, report.Adapter.ErrorCode,
		probedAt, report.Core.Name, report.Core.Version, boolInt(report.Core.Running),
		status, statusReason, now.UnixMilli(), identity.NodeID, identity.InstallationID)
	if err != nil {
		return fmt.Errorf("update S-UI probe: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return ErrConflict
	}
	if report.Adapter.Status == "compatible" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM sui_discovered_inbounds WHERE node_id = ?`, identity.NodeID); err != nil {
			return fmt.Errorf("clear S-UI inbounds: %w", err)
		}
		for _, inbound := range report.Inbounds {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO sui_discovered_inbounds(
					node_id, remote_id, tag, type, listen, listen_port, observed_at
				) VALUES (?, ?, ?, ?, ?, ?, ?)
			`, identity.NodeID, inbound.RemoteID, inbound.Tag, inbound.Type,
				inbound.Listen, inbound.ListenPort, report.SampledAt.UnixMilli()); err != nil {
				return fmt.Errorf("insert S-UI inbound: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM sui_discovered_clients WHERE node_id = ?`, identity.NodeID); err != nil {
			return fmt.Errorf("clear S-UI clients: %w", err)
		}
		for _, client := range report.Clients {
			inboundJSON, err := json.Marshal(client.InboundIDs)
			if err != nil {
				return fmt.Errorf("encode S-UI client inbounds: %w", err)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO sui_discovered_clients(
					node_id, remote_id, name, enabled, inbound_ids,
					upload_bytes, download_bytes, expires_at, online,
					client_group, description, observed_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', ?)
			`, identity.NodeID, client.RemoteID, client.Name, boolInt(client.Enabled),
				string(inboundJSON), client.UploadBytes, client.DownloadBytes,
				client.ExpiresAt, boolInt(client.Online), report.SampledAt.UnixMilli()); err != nil {
				return fmt.Errorf("insert S-UI client: %w", err)
			}
			if client.MappedUserID != "" {
				if _, err := tx.ExecContext(ctx, `
					UPDATE OR IGNORE node_user_assignments
					SET remote_client_id = ?, updated_at = ?
					WHERE node_id = ? AND user_id = ? AND management_mode = ?
				`, client.RemoteID, now.UnixMilli(), identity.NodeID,
					client.MappedUserID, client.ManagementMode); err != nil {
					return fmt.Errorf("update S-UI assignment mapping: %w", err)
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE nodes SET adapter_last_discovered_at = ?, updated_at = ? WHERE id = ?
		`, report.SampledAt.UnixMilli(), now.UnixMilli(), identity.NodeID); err != nil {
			return fmt.Errorf("mark S-UI discovery: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit S-UI report: %w", err)
	}
	return nil
}

func (s *Store) GetSUIState(ctx context.Context, nodeID string) (SUIState, error) {
	node, err := s.GetNode(ctx, nodeID)
	if err != nil {
		return SUIState{}, err
	}
	if node.AdapterType != "s_ui" {
		return SUIState{}, ErrUnsupported
	}
	state := SUIState{
		NodeID: node.ID, AdapterStatus: node.AdapterStatus, AdapterVersion: node.AdapterVersion,
		AdapterErrorCode: node.AdapterErrorCode, LastProbedAt: node.AdapterLastProbedAt,
		LastDiscoveredAt: node.AdapterLastDiscoveredAt,
		TargetInboundIDs: append([]int64(nil), node.SUITargetInboundIDs...),
		Inbounds:         make([]SUIInbound, 0), Clients: make([]SUIClient, 0),
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT remote_id, tag, type, listen, listen_port, observed_at
		FROM sui_discovered_inbounds WHERE node_id = ? ORDER BY remote_id
	`, nodeID)
	if err != nil {
		return SUIState{}, fmt.Errorf("list S-UI inbounds: %w", err)
	}
	for rows.Next() {
		var inbound SUIInbound
		var observedAt int64
		if err := rows.Scan(&inbound.RemoteID, &inbound.Tag, &inbound.Type,
			&inbound.Listen, &inbound.ListenPort, &observedAt); err != nil {
			_ = rows.Close()
			return SUIState{}, fmt.Errorf("scan S-UI inbound: %w", err)
		}
		inbound.ObservedAt = unixTime(observedAt)
		state.Inbounds = append(state.Inbounds, inbound)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return SUIState{}, fmt.Errorf("iterate S-UI inbounds: %w", err)
	}
	if err := rows.Close(); err != nil {
		return SUIState{}, fmt.Errorf("close S-UI inbounds: %w", err)
	}
	clientRows, err := s.db.QueryContext(ctx, `
		SELECT c.remote_id, c.name, c.enabled, c.inbound_ids, c.upload_bytes,
		       c.download_bytes, c.expires_at, c.online, c.observed_at,
		       COALESCE(a.user_id, ''), COALESCE(u.username, ''),
		       COALESCE(a.management_mode, '')
		FROM sui_discovered_clients c
		LEFT JOIN node_user_assignments a
		  ON a.node_id = c.node_id AND a.remote_client_id = c.remote_id
		LEFT JOIN users u ON u.id = a.user_id AND u.archived_at IS NULL
		WHERE c.node_id = ? ORDER BY c.remote_id
	`, nodeID)
	if err != nil {
		return SUIState{}, fmt.Errorf("list S-UI clients: %w", err)
	}
	defer clientRows.Close()
	for clientRows.Next() {
		var client SUIClient
		var enabled, online int
		var inboundJSON string
		var observedAt int64
		if err := clientRows.Scan(
			&client.RemoteID, &client.Name, &enabled, &inboundJSON,
			&client.UploadBytes, &client.DownloadBytes, &client.ExpiresAt, &online,
			&observedAt, &client.MappedUserID, &client.MappedUsername,
			&client.ManagementMode,
		); err != nil {
			return SUIState{}, fmt.Errorf("scan S-UI client: %w", err)
		}
		if err := json.Unmarshal([]byte(inboundJSON), &client.InboundIDs); err != nil {
			return SUIState{}, fmt.Errorf("decode S-UI client inbounds: %w", err)
		}
		client.Enabled = enabled == 1
		client.Online = online == 1
		client.ObservedAt = unixTime(observedAt)
		state.Clients = append(state.Clients, client)
	}
	if err := clientRows.Err(); err != nil {
		return SUIState{}, fmt.Errorf("iterate S-UI clients: %w", err)
	}
	return state, nil
}

func (s *Store) SetSUITargetInbounds(
	ctx context.Context,
	nodeID string,
	targets []int64,
	now time.Time,
) (SUIState, error) {
	targets = append([]int64(nil), targets...)
	sort.Slice(targets, func(left, right int) bool { return targets[left] < targets[right] })
	for index, target := range targets {
		if target <= 0 || (index > 0 && target == targets[index-1]) {
			return SUIState{}, ErrConflict
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SUIState{}, fmt.Errorf("begin S-UI target update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var adapter, currentJSON string
	if err := tx.QueryRowContext(ctx, `
		SELECT adapter_type, sui_target_inbound_ids
		FROM nodes WHERE id = ? AND archived_at IS NULL
	`, nodeID).Scan(&adapter, &currentJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SUIState{}, ErrNotFound
		}
		return SUIState{}, fmt.Errorf("read S-UI node targets: %w", err)
	}
	if adapter != "s_ui" {
		return SUIState{}, ErrUnsupported
	}
	if len(targets) == 0 {
		var managedAssignments int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM node_user_assignments
			WHERE node_id = ? AND management_mode = 'managed'
		`, nodeID).Scan(&managedAssignments); err != nil {
			return SUIState{}, fmt.Errorf("count managed S-UI assignments: %w", err)
		}
		if managedAssignments > 0 {
			return SUIState{}, ErrConflict
		}
	}
	for _, target := range targets {
		var exists int
		if err := tx.QueryRowContext(ctx, `
			SELECT 1 FROM sui_discovered_inbounds
			WHERE node_id = ? AND remote_id = ? AND type = 'hysteria2'
		`, nodeID, target).Scan(&exists); err != nil {
			return SUIState{}, ErrConflict
		}
	}
	encoded, err := json.Marshal(targets)
	if err != nil {
		return SUIState{}, err
	}
	if string(encoded) != currentJSON {
		if _, err := tx.ExecContext(ctx, `
			UPDATE nodes SET sui_target_inbound_ids = ?, updated_at = ? WHERE id = ?
		`, string(encoded), now.UnixMilli(), nodeID); err != nil {
			return SUIState{}, fmt.Errorf("update S-UI targets: %w", err)
		}
		version, err := bumpNodeSnapshot(ctx, tx, nodeID, now)
		if err != nil {
			return SUIState{}, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE node_user_assignments
			SET desired_version = ?, state = 'pending', last_error_code = '',
			    last_error_message = '', updated_at = ?
			WHERE node_id = ?
		`, version, now.UnixMilli(), nodeID); err != nil {
			return SUIState{}, fmt.Errorf("mark S-UI assignments pending: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return SUIState{}, fmt.Errorf("commit S-UI targets: %w", err)
	}
	return s.GetSUIState(ctx, nodeID)
}

func (s *Store) ImportSUIClient(
	ctx context.Context,
	nodeID string,
	remoteClientID int64,
	userID string,
	now time.Time,
	masterKey []byte,
) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin S-UI import: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var remoteName string
	if err := tx.QueryRowContext(ctx, `
		SELECT c.name FROM sui_discovered_clients c
		JOIN nodes n ON n.id = c.node_id AND n.adapter_type = 's_ui' AND n.archived_at IS NULL
		WHERE c.node_id = ? AND c.remote_id = ?
	`, nodeID, remoteClientID).Scan(&remoteName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("read S-UI import candidate: %w", err)
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM users WHERE id = ? AND archived_at IS NULL
	`, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("read S-UI import user: %w", err)
	}
	if _, err := assignUserTxWithMode(
		ctx, tx, userID, nodeID, 0, "read_only", remoteClientID, now, masterKey,
	); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit S-UI import: %w", err)
	}
	return s.GetUser(ctx, userID)
}

func (s *Store) AdoptSUIClient(
	ctx context.Context,
	nodeID string,
	remoteClientID int64,
	confirmedName string,
	now time.Time,
) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, fmt.Errorf("begin S-UI adoption: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var userID, currentName, mode, assignmentState, targetInboundJSON string
	var desiredVersion, appliedVersion int64
	if err := tx.QueryRowContext(ctx, `
		SELECT a.user_id, c.name, a.management_mode, a.state,
		       a.desired_version, a.applied_version, n.sui_target_inbound_ids
		FROM node_user_assignments a
		JOIN nodes n ON n.id = a.node_id AND n.adapter_type = 's_ui' AND n.archived_at IS NULL
		JOIN sui_discovered_clients c ON c.node_id = a.node_id AND c.remote_id = a.remote_client_id
		JOIN users u ON u.id = a.user_id AND u.archived_at IS NULL
		WHERE a.node_id = ? AND a.remote_client_id = ?
	`, nodeID, remoteClientID).Scan(
		&userID, &currentName, &mode, &assignmentState, &desiredVersion, &appliedVersion,
		&targetInboundJSON,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("read S-UI adoption: %w", err)
	}
	if mode != "read_only" || assignmentState != "applied" || appliedVersion < desiredVersion ||
		confirmedName == "" || confirmedName != currentName {
		return User{}, ErrConflict
	}
	var targetInboundIDs []int64
	if err := json.Unmarshal([]byte(targetInboundJSON), &targetInboundIDs); err != nil {
		return User{}, fmt.Errorf("decode S-UI adoption targets: %w", err)
	}
	if len(targetInboundIDs) == 0 {
		return User{}, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE node_user_assignments
		SET management_mode = 'managed', updated_at = ?
		WHERE node_id = ? AND user_id = ?
	`, now.UnixMilli(), nodeID, userID); err != nil {
		return User{}, fmt.Errorf("adopt S-UI assignment: %w", err)
	}
	version, err := bumpNodeSnapshot(ctx, tx, nodeID, now)
	if err != nil {
		return User{}, err
	}
	if err := markAssignmentPending(ctx, tx, userID, nodeID, version, now); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, fmt.Errorf("commit S-UI adoption: %w", err)
	}
	return s.GetUser(ctx, userID)
}

func (s *Store) GetCredentialMaterial(
	ctx context.Context,
	identity AgentIdentity,
	credentialRef string,
	desiredVersion int64,
	snapshotHash []byte,
	masterKey []byte,
) (string, error) {
	if !identity.Enabled ||
		(identity.AdapterType != "s_ui" && identity.AdapterType != AdapterSingBoxVLESSReality) ||
		credentialRef == "" || desiredVersion < 1 ||
		len(snapshotHash) != sha256.Size {
		return "", ErrUnauthorized
	}
	var canonical, expectedHash []byte
	var currentVersion int64
	err := s.db.QueryRowContext(ctx, `
		SELECT s.canonical_json, s.sha256, n.desired_version
		FROM node_snapshots s
		JOIN nodes n ON n.id = s.node_id AND n.archived_at IS NULL
		WHERE s.node_id = ? AND s.version = ? AND n.adapter_type = ?
	`, identity.NodeID, desiredVersion, identity.AdapterType).Scan(
		&canonical, &expectedHash, &currentVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnauthorized
	}
	if err != nil {
		return "", fmt.Errorf("read credential material snapshot: %w", err)
	}
	if currentVersion != desiredVersion || len(expectedHash) != sha256.Size ||
		subtle.ConstantTimeCompare(expectedHash, snapshotHash) != 1 {
		return "", ErrUnauthorized
	}
	var snapshot protocol.DesiredSnapshot
	if err := json.Unmarshal(canonical, &snapshot); err != nil {
		return "", fmt.Errorf("decode credential material snapshot: %w", err)
	}
	found := false
	for _, user := range snapshot.Users {
		if user.Credential.Ref != credentialRef {
			continue
		}
		if identity.AdapterType == AdapterSingBoxVLESSReality &&
			(!user.Enabled || user.QuotaState == "limited") {
			continue
		}
		if identity.AdapterType == "s_ui" && user.ManagementMode == "managed" {
			found = true
			break
		}
		if identity.AdapterType == AdapterSingBoxVLESSReality &&
			snapshot.SchemaVersion == 2 && snapshot.VLESSReality != nil &&
			user.Credential.Protocol == CredentialProtocolVLESS {
			found = true
			break
		}
	}
	if !found {
		return "", ErrUnauthorized
	}
	var userID, nodeID, state, managementMode, credentialProtocol string
	var ciphertext []byte
	var keyVersion int
	var userEnabled, assignmentEnabled, nodeEnabled int
	err = s.db.QueryRowContext(ctx, `
		SELECT c.user_id, c.node_id, c.secret_ciphertext, c.key_version, c.state,
		       a.management_mode, c.protocol, u.enabled, a.enabled, n.enabled
		FROM user_credentials c
		JOIN node_user_assignments a
		  ON a.node_id = c.node_id AND a.user_id = c.user_id
		JOIN users u ON u.id = a.user_id AND u.archived_at IS NULL
		JOIN nodes n ON n.id = a.node_id AND n.archived_at IS NULL
		WHERE c.id = ? AND c.node_id = ? AND a.desired_credential_id = c.id
	`, credentialRef, identity.NodeID).Scan(
		&userID, &nodeID, &ciphertext, &keyVersion, &state, &managementMode,
		&credentialProtocol, &userEnabled, &assignmentEnabled, &nodeEnabled,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnauthorized
	}
	if err != nil {
		return "", fmt.Errorf("read credential material: %w", err)
	}
	if nodeID != identity.NodeID || managementMode != "managed" || nodeEnabled != 1 ||
		keyVersion != credentialKeyVersion || (state != "staged" && state != "applied") {
		return "", ErrUnauthorized
	}
	if identity.AdapterType == AdapterSingBoxVLESSReality &&
		(userEnabled != 1 || assignmentEnabled != 1) {
		return "", ErrUnauthorized
	}
	expectedProtocol, err := credentialProtocolForAdapter(identity.AdapterType)
	if err != nil || credentialProtocol != expectedProtocol {
		return "", ErrUnauthorized
	}
	secret, err := cryptoutil.Open(masterKey, ciphertext, credentialAAD(
		credentialRef, userID, nodeID, credentialProtocol, keyVersion,
	))
	if err != nil {
		return "", fmt.Errorf("open credential material: %w", err)
	}
	return string(secret), nil
}

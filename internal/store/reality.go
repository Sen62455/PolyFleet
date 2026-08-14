package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
)

const (
	AdapterSingBoxVLESSReality = "sing_box_vless_reality"
	CredentialProtocolVLESS    = "vless"
	CredentialProtocolHY2      = "hysteria2"
)

type VLESSRealitySettings struct {
	NodeID                 string
	HandshakeServer        string
	HandshakeServerPort    int
	DesiredKeyGeneration   int64
	AppliedKeyGeneration   int64
	PublicKey              string
	ShortID                string
	MaterialAppliedVersion int64
	MaterialReportedAt     *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

func normalizeVLESSRealitySettings(settings VLESSRealitySettings) VLESSRealitySettings {
	if settings.HandshakeServerPort == 0 {
		settings.HandshakeServerPort = 443
	}
	if settings.DesiredKeyGeneration == 0 {
		settings.DesiredKeyGeneration = 1
	}
	return settings
}

func credentialProtocolForAdapter(adapter string) (string, error) {
	switch adapter {
	case "native_hysteria2", "s_ui":
		return CredentialProtocolHY2, nil
	case AdapterSingBoxVLESSReality:
		return CredentialProtocolVLESS, nil
	default:
		return "", ErrUnsupported
	}
}

func supportsManagedUsers(adapter string) bool {
	_, err := credentialProtocolForAdapter(adapter)
	return err == nil
}

func upsertVLESSRealitySettings(
	ctx context.Context,
	tx *sql.Tx,
	nodeID string,
	settings VLESSRealitySettings,
	now time.Time,
) error {
	settings = normalizeVLESSRealitySettings(settings)
	if settings.HandshakeServer == "" || settings.HandshakeServerPort < 1 ||
		settings.HandshakeServerPort > 65535 || settings.DesiredKeyGeneration < 1 {
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO node_vless_reality(
			node_id, handshake_server, handshake_server_port,
			desired_key_generation, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET
			handshake_server = excluded.handshake_server,
			handshake_server_port = excluded.handshake_server_port,
			desired_key_generation = excluded.desired_key_generation,
			updated_at = excluded.updated_at
		WHERE excluded.desired_key_generation >= node_vless_reality.desired_key_generation
	`, nodeID, settings.HandshakeServer, settings.HandshakeServerPort,
		settings.DesiredKeyGeneration, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return fmt.Errorf("upsert VLESS Reality settings: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read VLESS Reality settings result: %w", err)
	}
	if rowsAffected != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) getVLESSRealitySettings(
	ctx context.Context,
	nodeID string,
) (*VLESSRealitySettings, error) {
	var settings VLESSRealitySettings
	var reportedAt sql.NullInt64
	var createdAt, updatedAt int64
	err := s.db.QueryRowContext(ctx, `
		SELECT node_id, handshake_server, handshake_server_port,
		       desired_key_generation, applied_key_generation, public_key, short_id,
		       material_applied_version, material_reported_at, created_at, updated_at
		FROM node_vless_reality WHERE node_id = ?
	`, nodeID).Scan(
		&settings.NodeID, &settings.HandshakeServer, &settings.HandshakeServerPort,
		&settings.DesiredKeyGeneration, &settings.AppliedKeyGeneration,
		&settings.PublicKey, &settings.ShortID, &settings.MaterialAppliedVersion,
		&reportedAt, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get VLESS Reality settings: %w", err)
	}
	settings.MaterialReportedAt = nullableTime(reportedAt)
	settings.CreatedAt = unixTime(createdAt)
	settings.UpdatedAt = unixTime(updatedAt)
	return &settings, nil
}

func (s *Store) RotateVLESSRealityIdentity(
	ctx context.Context,
	nodeID string,
	expectedGeneration, expectedDesiredVersion int64,
	now time.Time,
) (Node, error) {
	if expectedGeneration < 1 || expectedDesiredVersion < 1 {
		return Node{}, ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, fmt.Errorf("begin Reality identity rotation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var adapter, installationID, publicKey, shortID string
	var enabled int
	var desiredVersion, appliedVersion, desiredGeneration, appliedGeneration int64
	var materialAppliedVersion int64
	err = tx.QueryRowContext(ctx, `
		SELECT n.adapter_type, COALESCE(n.agent_installation_id, ''), n.enabled,
		       n.desired_version, n.applied_version,
		       COALESCE(r.desired_key_generation, 0),
		       COALESCE(r.applied_key_generation, 0),
		       COALESCE(r.public_key, ''), COALESCE(r.short_id, ''),
		       COALESCE(r.material_applied_version, 0)
		FROM nodes n
		LEFT JOIN node_vless_reality r ON r.node_id = n.id
		WHERE n.id = ? AND n.archived_at IS NULL
	`, nodeID).Scan(
		&adapter, &installationID, &enabled, &desiredVersion, &appliedVersion,
		&desiredGeneration, &appliedGeneration, &publicKey, &shortID,
		&materialAppliedVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, fmt.Errorf("read Reality identity rotation target: %w", err)
	}
	if adapter != AdapterSingBoxVLESSReality {
		return Node{}, ErrUnsupported
	}
	if desiredGeneration != expectedGeneration || desiredVersion != expectedDesiredVersion {
		return Node{}, ErrConflict
	}
	if installationID == "" || desiredVersion != appliedVersion ||
		desiredGeneration != appliedGeneration || materialAppliedVersion != appliedVersion ||
		publicKey == "" || shortID == "" {
		return Node{}, ErrPending
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE node_vless_reality
		SET desired_key_generation = desired_key_generation + 1, updated_at = ?
		WHERE node_id = ? AND desired_key_generation = ?
	`, now.UnixMilli(), nodeID, expectedGeneration)
	if err != nil {
		return Node{}, fmt.Errorf("advance Reality identity generation: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return Node{}, fmt.Errorf("read Reality identity rotation result: %w", err)
	}
	if rowsAffected != 1 {
		return Node{}, ErrConflict
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
		return Node{}, fmt.Errorf("mark Reality identity assignments pending: %w", err)
	}
	status := "pending"
	if enabled == 0 {
		status = "disabled"
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes SET status = ?, status_reason = '', updated_at = ? WHERE id = ?
	`, status, now.UnixMilli(), nodeID); err != nil {
		return Node{}, fmt.Errorf("mark Reality identity rotation pending: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Node{}, fmt.Errorf("commit Reality identity rotation: %w", err)
	}
	return s.GetNode(ctx, nodeID)
}

func validAppliedRealityMaterial(material *protocol.AppliedRealityMaterial) bool {
	if material == nil || material.KeyGeneration < 1 || len(material.ShortID) != 16 {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(material.PublicKey)
	if err != nil || len(decoded) != 32 {
		return false
	}
	for _, character := range material.ShortID {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (s *Store) ListAgentCapabilities(ctx context.Context, nodeID string) ([]string, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx,
		"SELECT 1 FROM nodes WHERE id = ? AND archived_at IS NULL", nodeID,
	).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find capability node: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT capability FROM node_agent_capabilities
		WHERE node_id = ? ORDER BY capability
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("list Agent capabilities: %w", err)
	}
	defer rows.Close()
	capabilities := make([]string, 0)
	for rows.Next() {
		var capability string
		if err := rows.Scan(&capability); err != nil {
			return nil, fmt.Errorf("scan Agent capability: %w", err)
		}
		capabilities = append(capabilities, capability)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Agent capabilities: %w", err)
	}
	return capabilities, nil
}

func (s *Store) HasAgentCapabilities(
	ctx context.Context,
	nodeID string,
	required ...string,
) (bool, error) {
	capabilities, err := s.ListAgentCapabilities(ctx, nodeID)
	if err != nil {
		return false, err
	}
	have := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		have[capability] = struct{}{}
	}
	for _, capability := range required {
		if _, ok := have[capability]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func normalizeAgentCapabilities(capabilities []string) []string {
	unique := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if capability != "" && len(capability) <= 64 {
			unique[capability] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for capability := range unique {
		result = append(result, capability)
	}
	sort.Strings(result)
	return result
}

func containsAllCapabilities(capabilities []string, required ...string) bool {
	have := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		have[capability] = struct{}{}
	}
	for _, capability := range required {
		if _, ok := have[capability]; !ok {
			return false
		}
	}
	return true
}

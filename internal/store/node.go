package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
)

type Node struct {
	ID                           string
	Name                         string
	Provider                     string
	Region                       string
	AdapterType                  string
	AdapterStatus                string
	AdapterVersion               string
	AdapterErrorCode             string
	AdapterLastProbedAt          *time.Time
	AdapterLastDiscoveredAt      *time.Time
	SUITargetInboundIDs          []int64
	VLESSReality                 *VLESSRealitySettings
	PublicHost                   string
	PublicPort                   int
	SNI                          string
	TLSInsecure                  bool
	TLSCertFingerprint           string
	TLSPublicKeySHA256           string
	Enabled                      bool
	Status                       string
	StatusReason                 string
	DesiredVersion               int64
	AppliedVersion               int64
	AgentInstallationID          string
	AgentVersion                 string
	ProtocolVersion              int
	OSName                       string
	OSVersion                    string
	Architecture                 string
	Hostname                     string
	KernelVersion                string
	CoreName                     string
	CoreVersion                  string
	CoreRunning                  bool
	UptimeSeconds                int64
	CPUCores                     int
	CPUPercent                   float64
	MemoryUsedBytes              int64
	MemoryTotalBytes             int64
	SwapUsedBytes                int64
	SwapTotalBytes               int64
	DiskUsedBytes                int64
	DiskTotalBytes               int64
	DiskReadBytesPerSecond       int64
	DiskWriteBytesPerSecond      int64
	NetworkRXBPS                 int64
	NetworkTXBPS                 int64
	NetworkRXBytesTotal          int64
	NetworkTXBytesTotal          int64
	Load1                        float64
	Load5                        float64
	Load15                       float64
	LastSeenAt                   *time.Time
	LastAppliedAt                *time.Time
	UsageEnabled                 bool
	UsageAvailable               bool
	UsageOutboxBatches           int
	UsageErrorCode               string
	UsageSampledAt               *time.Time
	TrafficUploadBytes           int64
	TrafficDownloadBytes         int64
	TrafficUnattributedBytes     int64
	TrafficLastReportAt          *time.Time
	TrafficLimitBytes            int64
	TrafficResetDay              int
	TrafficCycleStartedAt        *time.Time
	TrafficCycleUploadBytes      int64
	TrafficCycleDownloadBytes    int64
	TrafficCalibrationBytes      *int64
	TrafficCalibrationProxyBytes *int64
	TrafficCalibratedAt          *time.Time
	OnlineUsers                  int
	OnlineConnections            int
	OnlineUnknownUsers           int
	OnlineSampledAt              *time.Time
	OnlineLastReportAt           *time.Time
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
}

type NewNode struct {
	ID                 string
	Name               string
	Provider           string
	Region             string
	AdapterType        string
	PublicHost         string
	PublicPort         int
	SNI                string
	TLSInsecure        bool
	TLSCertFingerprint string
	TLSPublicKeySHA256 string
	Enabled            bool
	VLESSReality       *VLESSRealitySettings
	TrafficLimitBytes  int64
	TrafficResetDay    int
	Now                time.Time
}

type UpdateNode struct {
	Name               string
	Provider           string
	Region             string
	AdapterType        string
	PublicHost         string
	PublicPort         int
	SNI                string
	TLSInsecure        bool
	TLSCertFingerprint string
	TLSPublicKeySHA256 string
	Enabled            bool
	VLESSReality       *VLESSRealitySettings
	TrafficLimitBytes  int64
	TrafficResetDay    int
	Now                time.Time
}

const nodeColumns = `
	id, name, provider, region, adapter_type, adapter_status, adapter_version,
	adapter_error_code, adapter_last_probed_at, adapter_last_discovered_at,
	sui_target_inbound_ids, public_host, public_port, sni,
	tls_insecure, tls_cert_fingerprint, tls_public_key_sha256,
	enabled, status, status_reason,
	desired_version, applied_version, COALESCE(agent_installation_id, ''),
	agent_version, protocol_version, os_name, os_version, architecture,
	hostname, kernel_version, core_name, core_version, core_running,
	uptime_seconds, cpu_cores, cpu_percent, memory_used_bytes, memory_total_bytes,
	swap_used_bytes, swap_total_bytes, disk_used_bytes, disk_total_bytes,
	disk_read_bytes_per_second, disk_write_bytes_per_second,
	network_rx_bps, network_tx_bps, network_rx_bytes_total, network_tx_bytes_total,
	load_1, load_5, load_15,
	last_seen_at, last_applied_at, usage_enabled, usage_available,
	usage_outbox_batches, usage_error_code, usage_sampled_at,
	traffic_upload_bytes, traffic_download_bytes, traffic_unattributed_bytes,
	traffic_last_report_at, traffic_limit_bytes, traffic_reset_day,
	traffic_cycle_started_at, traffic_cycle_upload_bytes, traffic_cycle_download_bytes,
	traffic_calibration_bytes, traffic_calibration_proxy_bytes, traffic_calibrated_at,
	online_users, online_connections, online_unknown_users,
	online_sampled_at, online_last_report_at, created_at, updated_at
`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanNode(row rowScanner) (Node, error) {
	var node Node
	var enabled, tlsInsecure, coreRunning, usageEnabled, usageAvailable int
	var lastSeen, lastApplied, usageSampled, trafficLastReport sql.NullInt64
	var onlineSampled, onlineLastReport sql.NullInt64
	var trafficCycleStarted, trafficCalibrated sql.NullInt64
	var trafficCalibration, trafficCalibrationProxy sql.NullInt64
	var adapterLastProbed, adapterLastDiscovered sql.NullInt64
	var suiTargetInboundJSON string
	var created, updated int64
	err := row.Scan(
		&node.ID, &node.Name, &node.Provider, &node.Region, &node.AdapterType,
		&node.AdapterStatus, &node.AdapterVersion, &node.AdapterErrorCode,
		&adapterLastProbed, &adapterLastDiscovered, &suiTargetInboundJSON,
		&node.PublicHost, &node.PublicPort, &node.SNI, &tlsInsecure,
		&node.TLSCertFingerprint, &node.TLSPublicKeySHA256,
		&enabled, &node.Status, &node.StatusReason, &node.DesiredVersion,
		&node.AppliedVersion, &node.AgentInstallationID, &node.AgentVersion,
		&node.ProtocolVersion, &node.OSName, &node.OSVersion, &node.Architecture,
		&node.Hostname, &node.KernelVersion, &node.CoreName, &node.CoreVersion,
		&coreRunning, &node.UptimeSeconds, &node.CPUCores, &node.CPUPercent,
		&node.MemoryUsedBytes, &node.MemoryTotalBytes, &node.SwapUsedBytes,
		&node.SwapTotalBytes, &node.DiskUsedBytes, &node.DiskTotalBytes,
		&node.DiskReadBytesPerSecond, &node.DiskWriteBytesPerSecond,
		&node.NetworkRXBPS, &node.NetworkTXBPS, &node.NetworkRXBytesTotal,
		&node.NetworkTXBytesTotal, &node.Load1, &node.Load5, &node.Load15,
		&lastSeen, &lastApplied, &usageEnabled, &usageAvailable,
		&node.UsageOutboxBatches, &node.UsageErrorCode, &usageSampled,
		&node.TrafficUploadBytes, &node.TrafficDownloadBytes,
		&node.TrafficUnattributedBytes, &trafficLastReport,
		&node.TrafficLimitBytes, &node.TrafficResetDay, &trafficCycleStarted,
		&node.TrafficCycleUploadBytes, &node.TrafficCycleDownloadBytes,
		&trafficCalibration, &trafficCalibrationProxy, &trafficCalibrated,
		&node.OnlineUsers, &node.OnlineConnections, &node.OnlineUnknownUsers,
		&onlineSampled, &onlineLastReport, &created, &updated,
	)
	if err != nil {
		return Node{}, err
	}
	node.Enabled = enabled == 1
	node.TLSInsecure = tlsInsecure == 1
	node.CoreRunning = coreRunning == 1
	node.UsageEnabled = usageEnabled == 1
	node.UsageAvailable = usageAvailable == 1
	node.LastSeenAt = nullableTime(lastSeen)
	node.LastAppliedAt = nullableTime(lastApplied)
	node.UsageSampledAt = nullableTime(usageSampled)
	node.TrafficLastReportAt = nullableTime(trafficLastReport)
	node.TrafficCycleStartedAt = nullableTime(trafficCycleStarted)
	node.TrafficCalibrationBytes = nullableInt64(trafficCalibration)
	node.TrafficCalibrationProxyBytes = nullableInt64(trafficCalibrationProxy)
	node.TrafficCalibratedAt = nullableTime(trafficCalibrated)
	node.OnlineSampledAt = nullableTime(onlineSampled)
	node.OnlineLastReportAt = nullableTime(onlineLastReport)
	node.AdapterLastProbedAt = nullableTime(adapterLastProbed)
	node.AdapterLastDiscoveredAt = nullableTime(adapterLastDiscovered)
	if err := json.Unmarshal([]byte(suiTargetInboundJSON), &node.SUITargetInboundIDs); err != nil {
		return Node{}, fmt.Errorf("decode S-UI target inbounds: %w", err)
	}
	node.CreatedAt = unixTime(created)
	node.UpdatedAt = unixTime(updated)
	return node, nil
}

func (s *Store) CreateNode(ctx context.Context, input NewNode) (Node, error) {
	if input.PublicPort == 0 {
		input.PublicPort = 443
	}
	if input.TrafficResetDay == 0 {
		input.TrafficResetDay = 1
	}
	cycleStart := trafficCycleStart(input.Now, input.TrafficResetDay)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, fmt.Errorf("begin create node: %w", err)
	}
	status := "pending"
	if !input.Enabled {
		status = "disabled"
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO nodes(
			id, name, provider, region, adapter_type, public_host, public_port,
			sni, tls_insecure, tls_cert_fingerprint, tls_public_key_sha256,
			enabled, status, traffic_limit_bytes, traffic_reset_day, traffic_cycle_started_at,
			desired_version, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`, input.ID, input.Name, input.Provider, input.Region, input.AdapterType,
		input.PublicHost, input.PublicPort, input.SNI, boolInt(input.TLSInsecure),
		input.TLSCertFingerprint, input.TLSPublicKeySHA256,
		boolInt(input.Enabled), status, input.TrafficLimitBytes, input.TrafficResetDay,
		cycleStart.UnixMilli(), input.Now.UnixMilli(), input.Now.UnixMilli())
	if err != nil {
		_ = tx.Rollback()
		return Node{}, fmt.Errorf("insert node: %w", err)
	}
	if input.AdapterType == AdapterSingBoxVLESSReality {
		if input.VLESSReality == nil {
			_ = tx.Rollback()
			return Node{}, ErrConflict
		}
		if err := upsertVLESSRealitySettings(
			ctx, tx, input.ID, *input.VLESSReality, input.Now,
		); err != nil {
			_ = tx.Rollback()
			return Node{}, err
		}
	}
	if err := insertSnapshot(ctx, tx, input.ID, input.AdapterType, 1, input.Now); err != nil {
		_ = tx.Rollback()
		return Node{}, err
	}
	if err := tx.Commit(); err != nil {
		return Node{}, fmt.Errorf("commit create node: %w", err)
	}
	return s.GetNode(ctx, input.ID)
}

func (s *Store) ListNodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+nodeColumns+" FROM nodes WHERE archived_at IS NULL ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	nodes := make([]Node, 0)
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan node: %w", err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate nodes: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close nodes: %w", err)
	}
	for index := range nodes {
		if nodes[index].AdapterType != AdapterSingBoxVLESSReality {
			continue
		}
		nodes[index].VLESSReality, err = s.getVLESSRealitySettings(ctx, nodes[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return nodes, nil
}

func (s *Store) GetNode(ctx context.Context, id string) (Node, error) {
	node, err := scanNode(s.db.QueryRowContext(ctx,
		"SELECT "+nodeColumns+" FROM nodes WHERE id = ? AND archived_at IS NULL", id,
	))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Node{}, ErrNotFound
		}
		return Node{}, fmt.Errorf("get node: %w", err)
	}
	if node.AdapterType == AdapterSingBoxVLESSReality {
		node.VLESSReality, err = s.getVLESSRealitySettings(ctx, node.ID)
		if err != nil {
			return Node{}, err
		}
	}
	return node, nil
}

func (s *Store) UpdateNode(ctx context.Context, id string, input UpdateNode) (Node, error) {
	if input.PublicPort == 0 {
		input.PublicPort = 443
	}
	if input.TrafficResetDay == 0 {
		input.TrafficResetDay = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, fmt.Errorf("begin update node: %w", err)
	}
	var version int64
	var currentAdapter, installationID string
	var currentResetDay int
	err = tx.QueryRowContext(ctx,
		`SELECT desired_version, adapter_type, COALESCE(agent_installation_id, ''), traffic_reset_day
		 FROM nodes WHERE id = ? AND archived_at IS NULL`, id,
	).Scan(&version, &currentAdapter, &installationID, &currentResetDay)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return Node{}, ErrNotFound
	}
	if err != nil {
		_ = tx.Rollback()
		return Node{}, fmt.Errorf("read node version: %w", err)
	}
	if input.AdapterType != currentAdapter {
		var dependentState int
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM node_user_assignments WHERE node_id = ?
				UNION ALL
				SELECT 1 FROM user_credentials WHERE node_id = ?
				UNION ALL
				SELECT 1 FROM node_enrollment_tokens WHERE node_id = ?
			)
		`, id, id, id).Scan(&dependentState); err != nil {
			_ = tx.Rollback()
			return Node{}, fmt.Errorf("check node adapter dependencies: %w", err)
		}
		if installationID != "" || dependentState != 0 {
			_ = tx.Rollback()
			return Node{}, ErrConflict
		}
	}
	version++
	cycleStart := trafficCycleStart(input.Now, input.TrafficResetDay)
	cycleUpload, cycleDownload, err := trafficCycleTotalsTx(
		ctx, tx, id, cycleStart, trafficCycleNextStart(cycleStart, input.TrafficResetDay),
	)
	if err != nil {
		return Node{}, err
	}
	status := "pending"
	if !input.Enabled {
		status = "disabled"
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE nodes SET name = ?, provider = ?, region = ?, adapter_type = ?,
			public_host = ?, public_port = ?, sni = ?, tls_insecure = ?,
			tls_cert_fingerprint = ?, tls_public_key_sha256 = ?, enabled = ?,
			status = ?, status_reason = '', desired_version = ?, updated_at = ?
			, traffic_limit_bytes = ?, traffic_reset_day = ?, traffic_cycle_started_at = ?,
			traffic_cycle_upload_bytes = ?, traffic_cycle_download_bytes = ?
		WHERE id = ? AND archived_at IS NULL
	`, input.Name, input.Provider, input.Region, input.AdapterType,
		input.PublicHost, input.PublicPort, input.SNI, boolInt(input.TLSInsecure),
		input.TLSCertFingerprint, input.TLSPublicKeySHA256,
		boolInt(input.Enabled), status, version, input.Now.UnixMilli(),
		input.TrafficLimitBytes, input.TrafficResetDay, cycleStart.UnixMilli(),
		cycleUpload, cycleDownload, id)
	if err != nil {
		_ = tx.Rollback()
		return Node{}, fmt.Errorf("update node: %w", err)
	}
	if currentResetDay != input.TrafficResetDay {
		if _, err := tx.ExecContext(ctx, `
			UPDATE nodes SET traffic_calibration_bytes = NULL,
				traffic_calibration_proxy_bytes = NULL, traffic_calibrated_at = NULL
			WHERE id = ?
		`, id); err != nil {
			_ = tx.Rollback()
			return Node{}, fmt.Errorf("clear traffic calibration after reset-day change: %w", err)
		}
	}
	if input.AdapterType == AdapterSingBoxVLESSReality {
		settings := input.VLESSReality
		if settings == nil && currentAdapter == AdapterSingBoxVLESSReality {
			var existing VLESSRealitySettings
			err := tx.QueryRowContext(ctx, `
				SELECT handshake_server, handshake_server_port, desired_key_generation
				FROM node_vless_reality WHERE node_id = ?
			`, id).Scan(
				&existing.HandshakeServer, &existing.HandshakeServerPort,
				&existing.DesiredKeyGeneration,
			)
			if err != nil {
				_ = tx.Rollback()
				return Node{}, fmt.Errorf("read current VLESS Reality settings: %w", err)
			}
			settings = &existing
		}
		if settings == nil {
			_ = tx.Rollback()
			return Node{}, ErrConflict
		}
		if err := upsertVLESSRealitySettings(ctx, tx, id, *settings, input.Now); err != nil {
			_ = tx.Rollback()
			return Node{}, err
		}
	} else if currentAdapter == AdapterSingBoxVLESSReality {
		if _, err := tx.ExecContext(ctx, "DELETE FROM node_vless_reality WHERE node_id = ?", id); err != nil {
			_ = tx.Rollback()
			return Node{}, fmt.Errorf("delete VLESS Reality settings: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE node_snapshots SET superseded_at = ? WHERE node_id = ? AND superseded_at IS NULL",
		input.Now.UnixMilli(), id,
	); err != nil {
		_ = tx.Rollback()
		return Node{}, fmt.Errorf("supersede snapshot: %w", err)
	}
	if err := insertSnapshot(ctx, tx, id, input.AdapterType, version, input.Now); err != nil {
		_ = tx.Rollback()
		return Node{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE node_user_assignments
		SET desired_version = ?, state = 'pending', last_error_code = '',
			last_error_message = '', updated_at = ?
		WHERE node_id = ?
	`, version, input.Now.UnixMilli(), id); err != nil {
		_ = tx.Rollback()
		return Node{}, fmt.Errorf("mark node assignments pending: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Node{}, fmt.Errorf("commit update node: %w", err)
	}
	return s.GetNode(ctx, id)
}

func (s *Store) ArchiveNode(ctx context.Context, id string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin archive node: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var assignments int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM node_user_assignments a
		JOIN users u ON u.id = a.user_id
		WHERE a.node_id = ? AND (
			u.archived_at IS NULL OR a.state <> 'applied' OR a.applied_version < a.desired_version
		)
	`, id).Scan(&assignments); err != nil {
		return fmt.Errorf("count node assignments: %w", err)
	}
	if assignments != 0 {
		return ErrConflict
	}
	var pendingAppliedSnapshot int
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM nodes n
			WHERE n.id = ? AND COALESCE(n.agent_installation_id, '') <> ''
			  AND n.applied_version < n.desired_version
		)
	`, id).Scan(&pendingAppliedSnapshot); err != nil {
		return fmt.Errorf("check pending node removals: %w", err)
	}
	if pendingAppliedSnapshot != 0 {
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE nodes SET enabled = 0, status = 'disabled', archived_at = ?, updated_at = ?
		WHERE id = ? AND archived_at IS NULL
	`, now.UnixMilli(), now.UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("archive node: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read archive result: %w", err)
	}
	if count == 0 {
		return ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit archive node: %w", err)
	}
	return nil
}

func insertSnapshot(ctx context.Context, tx *sql.Tx, nodeID, adapter string, version int64, now time.Time) error {
	users := make([]protocol.DesiredUser, 0)
	kicks := make([]protocol.DesiredKick, 0)
	var sui *protocol.DesiredSUI
	var vlessReality *protocol.DesiredVLESSReality
	if supportsManagedUsers(adapter) {
		rows, err := tx.QueryContext(ctx, `
			SELECT u.id, u.username, c.id, c.secret_fingerprint, c.protocol,
			       c.verifier_sha256,
			       (u.enabled AND a.enabled AND n.enabled), u.expires_at,
			       CASE
			         WHEN u.quota_state = 'limited' OR a.quota_state = 'limited' THEN 'limited'
			         WHEN u.quota_state = 'unlimited' AND a.quota_state = 'unlimited' THEN 'unlimited'
			         ELSE 'active'
			       END, a.management_mode, COALESCE(a.remote_client_id, 0)
			FROM node_user_assignments a
			JOIN users u ON u.id = a.user_id
			JOIN nodes n ON n.id = a.node_id
			JOIN user_credentials c ON c.id = a.desired_credential_id
			WHERE a.node_id = ? AND u.archived_at IS NULL
			  AND c.state IN ('staged', 'applied')
			ORDER BY u.id
		`, nodeID)
		if err != nil {
			return fmt.Errorf("read desired users: %w", err)
		}
		for rows.Next() {
			var user protocol.DesiredUser
			var credentialProtocol string
			var verifier []byte
			var enabled int
			var expiresAt sql.NullInt64
			var managementMode string
			var remoteClientID int64
			if err := rows.Scan(
				&user.ID, &user.Username, &user.Credential.Ref,
				&user.Credential.Fingerprint, &credentialProtocol,
				&verifier, &enabled, &expiresAt,
				&user.QuotaState, &managementMode, &remoteClientID,
			); err != nil {
				return fmt.Errorf("scan desired user: %w", err)
			}
			if adapter == "native_hysteria2" {
				if len(verifier) != sha256.Size {
					return errors.New("desired credential verifier has invalid length")
				}
				user.Credential.VerifierSHA256 = base64.RawURLEncoding.EncodeToString(verifier)
			} else if adapter == "s_ui" {
				user.ManagementMode = managementMode
				user.RemoteClientID = remoteClientID
			} else if adapter == AdapterSingBoxVLESSReality {
				user.Credential.Protocol = credentialProtocol
			}
			user.Enabled = enabled == 1
			user.ExpiresAt = nullableTime(expiresAt)
			if adapter == AdapterSingBoxVLESSReality && user.ExpiresAt != nil &&
				!now.UTC().Before(user.ExpiresAt.UTC()) {
				user.Enabled = false
			}
			users = append(users, user)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return fmt.Errorf("iterate desired users: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close desired users: %w", err)
		}
		if adapter == "native_hysteria2" || adapter == AdapterSingBoxVLESSReality {
			kickRows, err := tx.QueryContext(ctx, `
				SELECT user_id, generation FROM node_kick_targets
				WHERE node_id = ? ORDER BY user_id
			`, nodeID)
			if err != nil {
				return fmt.Errorf("read desired kicks: %w", err)
			}
			for kickRows.Next() {
				var kick protocol.DesiredKick
				if err := kickRows.Scan(&kick.UserID, &kick.Generation); err != nil {
					_ = kickRows.Close()
					return fmt.Errorf("scan desired kick: %w", err)
				}
				kicks = append(kicks, kick)
			}
			if err := kickRows.Err(); err != nil {
				_ = kickRows.Close()
				return fmt.Errorf("iterate desired kicks: %w", err)
			}
			if err := kickRows.Close(); err != nil {
				return fmt.Errorf("close desired kicks: %w", err)
			}
		}
		if adapter == "s_ui" {
			var targetInboundJSON string
			if err := tx.QueryRowContext(ctx, `
				SELECT sui_target_inbound_ids FROM nodes WHERE id = ?
			`, nodeID).Scan(&targetInboundJSON); err != nil {
				return fmt.Errorf("read S-UI target inbounds: %w", err)
			}
			targets := make([]int64, 0)
			if err := json.Unmarshal([]byte(targetInboundJSON), &targets); err != nil {
				return fmt.Errorf("decode S-UI target inbounds: %w", err)
			}
			sui = &protocol.DesiredSUI{TargetInboundIDs: targets}
		} else if adapter == AdapterSingBoxVLESSReality {
			var desired protocol.DesiredVLESSReality
			if err := tx.QueryRowContext(ctx, `
				SELECT n.public_port, n.sni, r.handshake_server,
				       r.handshake_server_port, r.desired_key_generation
				FROM nodes n JOIN node_vless_reality r ON r.node_id = n.id
				WHERE n.id = ?
			`, nodeID).Scan(
				&desired.ListenPort, &desired.ServerName, &desired.HandshakeServer,
				&desired.HandshakeServerPort, &desired.KeyGeneration,
			); err != nil {
				return fmt.Errorf("read desired VLESS Reality settings: %w", err)
			}
			desired.Flow = "xtls-rprx-vision"
			desired.Network = "tcp"
			vlessReality = &desired
		}
	}
	schemaVersion := 1
	if adapter == AdapterSingBoxVLESSReality {
		schemaVersion = 2
	}
	snapshot := protocol.DesiredSnapshot{
		SchemaVersion: schemaVersion,
		NodeID:        nodeID,
		Version:       version,
		Adapter:       adapter,
		Users:         users,
		Kicks:         kicks,
		SUI:           sui,
		VLESSReality:  vlessReality,
		GeneratedAt:   now.UTC(),
	}
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode desired snapshot: %w", err)
	}
	hash := sha256.Sum256(canonical)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO node_snapshots(node_id, version, canonical_json, sha256, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, nodeID, version, canonical, hash[:], now.UnixMilli())
	if err != nil {
		return fmt.Errorf("insert desired snapshot: %w", err)
	}
	return nil
}

func bumpNodeSnapshot(ctx context.Context, tx *sql.Tx, nodeID string, now time.Time) (int64, error) {
	var adapter string
	var currentVersion int64
	if err := tx.QueryRowContext(ctx, `
		SELECT adapter_type, desired_version
		FROM nodes WHERE id = ? AND archived_at IS NULL
	`, nodeID).Scan(&adapter, &currentVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("read node for snapshot: %w", err)
	}
	version := currentVersion + 1
	if _, err := tx.ExecContext(ctx, `
		UPDATE nodes SET desired_version = ?, updated_at = ? WHERE id = ?
	`, version, now.UnixMilli(), nodeID); err != nil {
		return 0, fmt.Errorf("advance node snapshot version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE node_snapshots SET superseded_at = ?
		WHERE node_id = ? AND superseded_at IS NULL
	`, now.UnixMilli(), nodeID); err != nil {
		return 0, fmt.Errorf("supersede node snapshot: %w", err)
	}
	if err := insertSnapshot(ctx, tx, nodeID, adapter, version, now); err != nil {
		return 0, err
	}
	return version, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

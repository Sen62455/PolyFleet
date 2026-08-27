package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/Sen62455/PolyFleet/internal/store/migrations"
	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type migration0010Fixture struct {
	nativeNodeID            string
	suiNodeID               string
	userID                  string
	nativeDesiredCredential string
	nativeAppliedCredential string
	suiCredential           string
	nativeAssignment        string
	suiAssignment           string
	subscriptionTokenID     string
	subscriptionSecret      string
	nativeDesiredSecret     string
	suiSecret               string
	masterKey               []byte
	now                     time.Time
}

func TestTrafficAlertMigrationPreservesExistingAlert(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "vless-reality.db")
	fixture := createMigration0010Fixture(t, ctx, path)

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open(upgrade through traffic alerts) error = %v", err)
	}
	defer database.Close()

	var alertType, severity, status, message string
	var occurrenceCount int
	if err := database.DB().QueryRowContext(ctx, `
		SELECT type, severity, status, message, occurrence_count
		FROM alerts WHERE node_id = ?
	`, fixture.nativeNodeID).Scan(
		&alertType, &severity, &status, &message, &occurrenceCount,
	); err != nil {
		t.Fatalf("read alert after 0013 migration: %v", err)
	}
	if alertType != "offline" || severity != "warning" || status != "acknowledged" ||
		message != "legacy alert" || occurrenceCount != 2 {
		t.Fatalf("preserved alert = %q %q %q %q %d",
			alertType, severity, status, message, occurrenceCount)
	}
	if _, err := database.DB().ExecContext(ctx, `
		INSERT INTO alerts(
			id, node_id, type, severity, status, message, occurrence_count,
			first_seen_at, last_seen_at, created_at, updated_at
		) VALUES (?, ?, 'traffic_quota_warning', 'warning', 'open', 'traffic warning',
			1, ?, ?, ?, ?)
	`, uuid.NewString(), fixture.nativeNodeID, fixture.now.UnixMilli(), fixture.now.UnixMilli(),
		fixture.now.UnixMilli(), fixture.now.UnixMilli()); err != nil {
		t.Fatalf("insert new traffic alert after migration: %v", err)
	}
	var foreignKeyViolations int
	if err := database.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_foreign_key_check").Scan(
		&foreignKeyViolations,
	); err != nil || foreignKeyViolations != 0 {
		t.Fatalf("foreign key violations after alert migration = %d, error = %v",
			foreignKeyViolations, err)
	}
}

func TestVLESSRealityMigrationFromV123PreservesComplete0010Database(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "v1.2.3.db")
	fixture := createMigration0010Fixture(t, ctx, path)

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open(v1.2.3 upgrade) error = %v", err)
	}
	defer database.Close()

	assertMigration0011Recorded(t, ctx, database)
	assertMigration0010RowsPreserved(t, ctx, database, fixture)
	assertMigration0010ObjectsReadable(t, ctx, database, fixture)
	assertMigration0011ForeignKeysAndConstraints(t, ctx, database, fixture)
	assertMigration0011AcceptsRealityData(t, ctx, database, fixture)
}

func createMigration0010Fixture(
	t *testing.T,
	ctx context.Context,
	path string,
) migration0010Fixture {
	t.Helper()
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open(v1.2.3 fixture) error = %v", err)
	}
	legacy.SetMaxOpenConns(1)
	defer legacy.Close()

	if _, err := legacy.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable fixture foreign keys: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatalf("create fixture migration table: %v", err)
	}
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	migrationNames := []string{
		"0001_foundation.sql",
		"0002_active_node_name.sql",
		"0003_native_users.sql",
		"0004_traffic_online_quota.sql",
		"0005_unified_subscriptions.sql",
		"0006_sui_adapter.sql",
		"0007_bounded_operations.sql",
		"0008_host_monitoring.sql",
		"0009_tls_pins.sql",
		"0010_runtime_telemetry.sql",
	}
	for _, name := range migrationNames {
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatalf("read fixture migration %s: %v", name, err)
		}
		if _, err := legacy.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply fixture migration %s: %v", name, err)
		}
		if _, err := legacy.ExecContext(ctx, `
			INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)
		`, name, now.UnixMilli()); err != nil {
			t.Fatalf("record fixture migration %s: %v", name, err)
		}
	}
	if _, err := legacy.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable fixture foreign keys: %v", err)
	}
	var foreignKeys int
	if err := legacy.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("fixture foreign_keys = %d, error = %v", foreignKeys, err)
	}

	fixture := migration0010Fixture{
		nativeNodeID:            uuid.NewString(),
		suiNodeID:               uuid.NewString(),
		userID:                  uuid.NewString(),
		nativeDesiredCredential: uuid.NewString(),
		nativeAppliedCredential: uuid.NewString(),
		suiCredential:           uuid.NewString(),
		nativeAssignment:        uuid.NewString(),
		suiAssignment:           uuid.NewString(),
		subscriptionTokenID:     uuid.NewString(),
		subscriptionSecret:      "hys_migration0011_subscription_secret",
		nativeDesiredSecret:     "migration-native-desired-secret",
		suiSecret:               "migration-sui-secret",
		masterKey:               bytes.Repeat([]byte{0xa1}, 32),
		now:                     now,
	}
	adminID := uuid.NewString()
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO admins(id, username, password_hash, created_at, updated_at)
		VALUES (?, 'migration-admin', 'password-hash', ?, ?)
	`, adminID, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatalf("seed fixture administrator: %v", err)
	}

	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO nodes(
			id, name, provider, region, adapter_type, enabled, status, status_reason,
			desired_version, applied_version, agent_installation_id,
			agent_credential_hash, agent_version, protocol_version, os_name,
			os_version, architecture, core_name, core_version, core_running,
			uptime_seconds, cpu_percent, memory_used_bytes, memory_total_bytes,
			disk_used_bytes, disk_total_bytes, network_rx_bps, network_tx_bps,
			load_1, load_5, load_15, last_seen_at, last_applied_at,
			created_at, updated_at, usage_enabled, usage_available,
			usage_outbox_batches, usage_error_code, usage_sampled_at,
			traffic_upload_bytes, traffic_download_bytes, traffic_unattributed_bytes,
			traffic_last_report_at, online_users, online_connections,
			online_unknown_users, online_sampled_at, online_last_report_at,
			public_host, public_port, sni, tls_insecure, adapter_status,
			adapter_version, adapter_error_code, adapter_last_probed_at,
			adapter_last_discovered_at, sui_target_inbound_ids, hostname,
			kernel_version, cpu_cores, swap_used_bytes, swap_total_bytes,
			disk_read_bytes_per_second, disk_write_bytes_per_second,
			network_rx_bytes_total, network_tx_bytes_total,
			tls_cert_fingerprint, tls_public_key_sha256
		) VALUES (
			?, 'Legacy Native', 'DMIT', 'HKG', 'native_hysteria2', 1, 'online',
			'healthy', 7, 6, 'native-installation', ?, '1.2.3', 1, 'linux',
			'debian-12', 'amd64', 'hysteria', '2.6.2', 1, 86400, 12.5,
			1048576, 2097152, 3145728, 4194304, 500, 600, 0.1, 0.2, 0.3,
			?, ?, ?, ?, 1, 1, 2, '', ?, 101, 202, 3, ?, 1, 2, 0, ?, ?,
			'native.example.com', 8443, 'native-sni.example.com', 0,
			'compatible', 'native-adapter-1', '', ?, ?, '[]', 'native-host',
			'6.1.0', 4, 1024, 2048, 11, 22, 1001, 2002,
			'AA:BB:CC', 'legacy-native-public-key-pin'
		)
	`, fixture.nativeNodeID, bytes.Repeat([]byte{0x11}, 32),
		now.Add(-time.Minute).UnixMilli(), now.Add(-2*time.Minute).UnixMilli(),
		now.UnixMilli(), now.UnixMilli(), now.Add(-time.Minute).UnixMilli(),
		now.Add(-time.Minute).UnixMilli(), now.Add(-time.Minute).UnixMilli(),
		now.Add(-time.Minute).UnixMilli(), now.Add(-time.Minute).UnixMilli(),
		now.Add(-time.Minute).UnixMilli(), now.Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatalf("seed native fixture node: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO nodes(
			id, name, provider, region, adapter_type, enabled, status,
			desired_version, applied_version, agent_installation_id,
			agent_version, protocol_version, core_name, core_version, core_running,
			created_at, updated_at, public_host, public_port, sni, adapter_status,
			adapter_version, adapter_last_probed_at, adapter_last_discovered_at,
			sui_target_inbound_ids, hostname, kernel_version, cpu_cores
		) VALUES (
			?, 'Legacy S-UI', 'LisaHost', 'LAX', 's_ui', 1, 'online', 4, 4,
			'sui-installation', '1.2.3', 1, 'sing-box', '1.12.0', 1, ?, ?,
			'sui.example.com', 443, 'sui.example.com', 'compatible', 's-ui-1',
			?, ?, '[41,42]', 'sui-host', '6.1.0', 2
		)
	`, fixture.suiNodeID, now.UnixMilli(), now.UnixMilli(),
		now.Add(-time.Minute).UnixMilli(), now.Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatalf("seed S-UI fixture node: %v", err)
	}

	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO users(
			id, username, display_name, notes, enabled, traffic_limit_bytes,
			traffic_upload_bytes, traffic_download_bytes, traffic_used_bytes,
			quota_state, last_traffic_at, created_at, updated_at
		) VALUES (?, 'legacy-subscriber', 'Legacy Subscriber', 'migration fixture',
			1, 1000000, 123, 456, 579, 'active', ?, ?, ?)
	`, fixture.userID, now.Add(-time.Minute).UnixMilli(), now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatalf("seed fixture user: %v", err)
	}
	insertMigration0010Credential(
		t, ctx, legacy, fixture.nativeDesiredCredential, fixture.userID,
		fixture.nativeNodeID, fixture.nativeDesiredSecret, "fp_native_desired",
		"staged", fixture.masterKey, now,
	)
	insertMigration0010Credential(
		t, ctx, legacy, fixture.nativeAppliedCredential, fixture.userID,
		fixture.nativeNodeID, "migration-native-applied-secret", "fp_native_applied",
		"applied", fixture.masterKey, now,
	)
	insertMigration0010Credential(
		t, ctx, legacy, fixture.suiCredential, fixture.userID,
		fixture.suiNodeID, fixture.suiSecret, "fp_sui", "applied", fixture.masterKey, now,
	)
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO node_user_assignments(
			id, node_id, user_id, desired_credential_id, applied_credential_id,
			enabled, desired_version, applied_version, state, last_error_code,
			last_error_message, last_attempt_at, applied_at, created_at, updated_at,
			traffic_limit_bytes, traffic_upload_bytes, traffic_download_bytes,
			traffic_used_bytes, quota_state, last_traffic_at, management_mode,
			remote_client_id
		) VALUES (?, ?, ?, ?, ?, 1, 7, 6, 'pending', 'apply_pending',
			'waiting for version seven', ?, ?, ?, ?, 1000000, 12, 34, 46,
			'active', ?, 'managed', NULL)
	`, fixture.nativeAssignment, fixture.nativeNodeID, fixture.userID,
		fixture.nativeDesiredCredential, fixture.nativeAppliedCredential,
		now.Add(-time.Minute).UnixMilli(), now.Add(-2*time.Minute).UnixMilli(),
		now.UnixMilli(), now.UnixMilli(), now.Add(-time.Minute).UnixMilli()); err != nil {
		t.Fatalf("seed native fixture assignment: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO node_user_assignments(
			id, node_id, user_id, desired_credential_id, applied_credential_id,
			enabled, desired_version, applied_version, state, applied_at,
			created_at, updated_at, quota_state, management_mode, remote_client_id
		) VALUES (?, ?, ?, ?, ?, 1, 4, 4, 'applied', ?, ?, ?, 'unlimited',
			'managed', 9001)
	`, fixture.suiAssignment, fixture.suiNodeID, fixture.userID,
		fixture.suiCredential, fixture.suiCredential, now.UnixMilli(),
		now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatalf("seed S-UI fixture assignment: %v", err)
	}

	insertMigration0010Snapshot(t, ctx, legacy, protocol.DesiredSnapshot{
		SchemaVersion: 1, NodeID: fixture.nativeNodeID, Version: 6,
		Adapter: "native_hysteria2", GeneratedAt: now.Add(-time.Minute),
	}, now.Add(-time.Minute), &now)
	insertMigration0010Snapshot(t, ctx, legacy, protocol.DesiredSnapshot{
		SchemaVersion: 1, NodeID: fixture.nativeNodeID, Version: 7,
		Adapter: "native_hysteria2", Users: []protocol.DesiredUser{{
			ID: fixture.userID, Username: "legacy-subscriber",
			Credential: protocol.DesiredCredential{
				Ref: fixture.nativeDesiredCredential, Fingerprint: "fp_native_desired",
			}, Enabled: true, QuotaState: "active",
		}}, GeneratedAt: now,
	}, now, nil)
	insertMigration0010Snapshot(t, ctx, legacy, protocol.DesiredSnapshot{
		SchemaVersion: 1, NodeID: fixture.suiNodeID, Version: 4, Adapter: "s_ui",
		Users: []protocol.DesiredUser{{
			ID: fixture.userID, Username: "legacy-subscriber",
			Credential: protocol.DesiredCredential{
				Ref: fixture.suiCredential, Fingerprint: "fp_sui",
			}, Enabled: true, ManagementMode: "managed", RemoteClientID: 9001,
			QuotaState: "unlimited",
		}}, SUI: &protocol.DesiredSUI{TargetInboundIDs: []int64{41, 42}},
		GeneratedAt: now,
	}, now, nil)

	seedMigration0010NodeRelations(t, ctx, legacy, fixture, adminID)
	if err := assertNoForeignKeyViolations(ctx, legacy); err != nil {
		t.Fatalf("fixture foreign-key violation: %v", err)
	}
	return fixture
}

func insertMigration0010Credential(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	credentialID, userID, nodeID, secret, fingerprint, state string,
	masterKey []byte,
	now time.Time,
) {
	t.Helper()
	verifier := sha256.Sum256([]byte(secret))
	ciphertext, err := cryptoutil.Seal(masterKey, []byte(secret), credentialAAD(
		credentialID, userID, nodeID, CredentialProtocolHY2, credentialKeyVersion,
	))
	if err != nil {
		t.Fatalf("seal fixture credential %s: %v", credentialID, err)
	}
	var appliedAt any
	if state == "applied" {
		appliedAt = now.UnixMilli()
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO user_credentials(
			id, user_id, node_id, protocol, secret_ciphertext, verifier_sha256,
			secret_fingerprint, key_version, state, created_at, applied_at
		) VALUES (?, ?, ?, 'hysteria2', ?, ?, ?, 1, ?, ?, ?)
	`, credentialID, userID, nodeID, ciphertext, verifier[:], fingerprint,
		state, now.UnixMilli(), appliedAt); err != nil {
		t.Fatalf("seed fixture credential %s: %v", credentialID, err)
	}
}

func insertMigration0010Snapshot(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	snapshot protocol.DesiredSnapshot,
	createdAt time.Time,
	supersededAt *time.Time,
) {
	t.Helper()
	canonical, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("encode fixture snapshot: %v", err)
	}
	digest := sha256.Sum256(canonical)
	if _, err := database.ExecContext(ctx, `
		INSERT INTO node_snapshots(
			node_id, version, canonical_json, sha256, created_at, superseded_at
		) VALUES (?, ?, ?, ?, ?, ?)
	`, snapshot.NodeID, snapshot.Version, canonical, digest[:], createdAt.UnixMilli(),
		nullableUnixMilli(supersededAt)); err != nil {
		t.Fatalf("seed fixture snapshot %s/%d: %v", snapshot.NodeID, snapshot.Version, err)
	}
}

func seedMigration0010NodeRelations(
	t *testing.T,
	ctx context.Context,
	database *sql.DB,
	fixture migration0010Fixture,
	adminID string,
) {
	t.Helper()
	now := fixture.now.UnixMilli()
	enrollmentID := uuid.NewString()
	operationID := uuid.NewString()
	batchID := uuid.NewString()
	statements := []struct {
		name string
		body string
		args []any
	}{
		{name: "enrollment", body: `
			INSERT INTO node_enrollment_tokens(
				id, node_id, token_hash, expires_at, created_by, created_at
			) VALUES (?, ?, ?, ?, ?, ?)
		`, args: []any{enrollmentID, fixture.nativeNodeID, bytes.Repeat([]byte{0x21}, 32), now + 3600000, adminID, now}},
		{name: "metric", body: `
			INSERT INTO node_metric_samples(
				node_id, bucket_at, cpu_percent, memory_used_bytes, memory_total_bytes,
				disk_used_bytes, disk_total_bytes, network_rx_bps, network_tx_bps,
				load_1, load_5, load_15, sampled_at, swap_used_bytes,
				swap_total_bytes, disk_read_bytes_per_second,
				disk_write_bytes_per_second
			) VALUES (?, ?, 7.5, 1, 2, 3, 4, 5, 6, 0.1, 0.2, 0.3, ?, 7, 8, 9, 10)
		`, args: []any{fixture.nativeNodeID, now, now}},
		{name: "traffic batch", body: `
			INSERT INTO traffic_batches(
				id, node_id, agent_installation_id, source_epoch, sequence,
				sampled_at, received_at, item_count, upload_bytes, download_bytes,
				payload_sha256
			) VALUES (?, ?, 'native-installation', 'epoch-1', 1, ?, ?, 1, 12, 34, ?)
		`, args: []any{batchID, fixture.nativeNodeID, now, now, bytes.Repeat([]byte{0x31}, 32)}},
		{name: "traffic item", body: `
			INSERT INTO traffic_batch_items(
				batch_id, node_id, user_id, upload_bytes, download_bytes, disposition
			) VALUES (?, ?, ?, 12, 34, 'accounted')
		`, args: []any{batchID, fixture.nativeNodeID, fixture.userID}},
		{name: "traffic total", body: `
			INSERT INTO traffic_totals(
				node_id, user_id, upload_bytes, download_bytes, last_batch_id, updated_at
			) VALUES (?, ?, 12, 34, ?, ?)
		`, args: []any{fixture.nativeNodeID, fixture.userID, batchID, now}},
		{name: "online snapshot", body: `
			INSERT INTO node_online_snapshots(
				node_id, agent_installation_id, snapshot_id, sampled_at, received_at,
				total_connections, known_users, unknown_users
			) VALUES (?, 'native-installation', 'online-1', ?, ?, 2, 1, 0)
		`, args: []any{fixture.nativeNodeID, now, now}},
		{name: "online user", body: `
			INSERT INTO node_online_users(
				node_id, user_id, connections, known_assignment, sampled_at
			) VALUES (?, ?, 2, 1, ?)
		`, args: []any{fixture.nativeNodeID, fixture.userID, now}},
		{name: "kick", body: `
			INSERT INTO node_kick_targets(
				node_id, user_id, generation, reason, requested_at
			) VALUES (?, ?, 3, 'migration-test', ?)
		`, args: []any{fixture.nativeNodeID, fixture.userID, now}},
		{name: "operation", body: `
			INSERT INTO node_operations(
				id, node_id, sequence, type, status, attempt, max_lines,
				requested_by, expires_at, created_at, updated_at
			) VALUES (?, ?, 1, 'backup_config', 'succeeded', 1, 0, ?, ?, ?, ?)
		`, args: []any{operationID, fixture.nativeNodeID, adminID, now + 60000, now, now}},
		{name: "backup", body: `
			INSERT INTO config_backups(
				id, node_id, operation_id, local_path, sha256, size_bytes, created_at
			) VALUES (?, ?, ?, '/var/lib/hyfleet-backups/migration.bak', ?, 128, ?)
		`, args: []any{uuid.NewString(), fixture.nativeNodeID, operationID, fmt.Sprintf("%064x", 1), now}},
		{name: "alert", body: `
			INSERT INTO alerts(
				id, node_id, type, severity, status, message, occurrence_count,
				first_seen_at, last_seen_at, created_at, updated_at
			) VALUES (?, ?, 'offline', 'warning', 'acknowledged', 'legacy alert',
				2, ?, ?, ?, ?)
		`, args: []any{uuid.NewString(), fixture.nativeNodeID, now, now, now, now}},
		{name: "telemetry", body: `
			INSERT INTO node_telemetry_snapshots(
				node_id, sampled_at, processes_available, processes_error_code,
				processes_total, processes_truncated, processes_sampled_at,
				processes_json, services_available, services_error_code,
				services_total, services_truncated, services_sampled_at,
				services_json, received_at
			) VALUES (?, ?, 1, '', 1, 0, ?, '[{"name":"sing-box"}]',
				1, '', 1, 0, ?, '[{"unit":"sing-box.service"}]', ?)
		`, args: []any{fixture.suiNodeID, now, now, now, now}},
		{name: "S-UI inbound", body: `
			INSERT INTO sui_discovered_inbounds(
				node_id, remote_id, tag, type, listen, listen_port, observed_at
			) VALUES (?, 41, 'legacy-inbound', 'hysteria2', '0.0.0.0', 443, ?)
		`, args: []any{fixture.suiNodeID, now}},
		{name: "S-UI client", body: `
			INSERT INTO sui_discovered_clients(
				node_id, remote_id, name, enabled, inbound_ids, upload_bytes,
				download_bytes, expires_at, online, client_group, description,
				observed_at
			) VALUES (?, 9001, 'legacy-subscriber', 1, '[41,42]', 55, 66,
				0, 1, 'legacy', 'preserve me', ?)
		`, args: []any{fixture.suiNodeID, now}},
		{name: "subscription", body: `
			INSERT INTO subscription_tokens(
				id, user_id, name, token_hash, token_prefix, allowed_formats,
				last_used_at, created_at, updated_at
			) VALUES (?, ?, 'legacy primary', ?, 'hys_migr', 'uri,sing-box', ?, ?, ?)
		`, args: []any{fixture.subscriptionTokenID, fixture.userID,
			cryptoutil.TokenHash(fixture.subscriptionSecret), now, now, now}},
	}
	for _, statement := range statements {
		if _, err := database.ExecContext(ctx, statement.body, statement.args...); err != nil {
			t.Fatalf("seed fixture %s: %v", statement.name, err)
		}
	}
}

func assertMigration0011Recorded(t *testing.T, ctx context.Context, database *Store) {
	t.Helper()
	var migrationCount, realityMigrationCount int
	if err := database.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM schema_migrations",
	).Scan(&migrationCount); err != nil || migrationCount != 14 {
		t.Fatalf("migration count = %d, error = %v", migrationCount, err)
	}
	if err := database.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM schema_migrations WHERE version = '0011_vless_reality.sql'
	`).Scan(&realityMigrationCount); err != nil || realityMigrationCount != 1 {
		t.Fatalf("Reality migration count = %d, error = %v", realityMigrationCount, err)
	}
}

func assertMigration0010RowsPreserved(
	t *testing.T,
	ctx context.Context,
	database *Store,
	fixture migration0010Fixture,
) {
	t.Helper()
	wantCounts := map[string]int{
		"nodes":                    2,
		"users":                    1,
		"user_credentials":         3,
		"node_user_assignments":    2,
		"node_snapshots":           3,
		"node_enrollment_tokens":   1,
		"node_metric_samples":      1,
		"traffic_batches":          1,
		"traffic_batch_items":      1,
		"traffic_totals":           1,
		"node_online_snapshots":    1,
		"node_online_users":        1,
		"node_kick_targets":        1,
		"node_operations":          1,
		"config_backups":           1,
		"alerts":                   1,
		"node_telemetry_snapshots": 1,
		"sui_discovered_inbounds":  1,
		"sui_discovered_clients":   1,
		"subscription_tokens":      1,
		"node_agent_capabilities":  0,
		"node_vless_reality":       0,
	}
	for table, want := range wantCounts {
		var got int
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
		if err := database.DB().QueryRowContext(ctx, query).Scan(&got); err != nil || got != want {
			t.Fatalf("%s row count = %d, want %d, error = %v", table, got, want, err)
		}
	}

	var desiredCredential, appliedCredential, state, mode string
	var desiredVersion, appliedVersion, remoteClientID int64
	if err := database.DB().QueryRowContext(ctx, `
		SELECT desired_credential_id, applied_credential_id, desired_version,
		       applied_version, state, management_mode, COALESCE(remote_client_id, 0)
		FROM node_user_assignments WHERE id = ?
	`, fixture.nativeAssignment).Scan(
		&desiredCredential, &appliedCredential, &desiredVersion, &appliedVersion,
		&state, &mode, &remoteClientID,
	); err != nil {
		t.Fatalf("read preserved native assignment: %v", err)
	}
	if desiredCredential != fixture.nativeDesiredCredential ||
		appliedCredential != fixture.nativeAppliedCredential || desiredVersion != 7 ||
		appliedVersion != 6 || state != "pending" || mode != "managed" || remoteClientID != 0 {
		t.Fatalf("preserved native assignment = %q %q %d %d %q %q %d",
			desiredCredential, appliedCredential, desiredVersion, appliedVersion,
			state, mode, remoteClientID)
	}
	if err := database.DB().QueryRowContext(ctx, `
		SELECT desired_credential_id, applied_credential_id, desired_version,
		       applied_version, state, management_mode, remote_client_id
		FROM node_user_assignments WHERE id = ?
	`, fixture.suiAssignment).Scan(
		&desiredCredential, &appliedCredential, &desiredVersion, &appliedVersion,
		&state, &mode, &remoteClientID,
	); err != nil {
		t.Fatalf("read preserved S-UI assignment: %v", err)
	}
	if desiredCredential != fixture.suiCredential || appliedCredential != fixture.suiCredential ||
		desiredVersion != 4 || appliedVersion != 4 || state != "applied" ||
		mode != "managed" || remoteClientID != 9001 {
		t.Fatalf("preserved S-UI assignment = %q %q %d %d %q %q %d",
			desiredCredential, appliedCredential, desiredVersion, appliedVersion,
			state, mode, remoteClientID)
	}

	for credentialID, wantState := range map[string]string{
		fixture.nativeDesiredCredential: "staged",
		fixture.nativeAppliedCredential: "applied",
		fixture.suiCredential:           "applied",
	} {
		var protocolName, credentialState string
		var verifierLength int
		if err := database.DB().QueryRowContext(ctx, `
			SELECT protocol, state, length(verifier_sha256)
			FROM user_credentials WHERE id = ?
		`, credentialID).Scan(&protocolName, &credentialState, &verifierLength); err != nil {
			t.Fatalf("read preserved credential %s: %v", credentialID, err)
		}
		if protocolName != CredentialProtocolHY2 || credentialState != wantState || verifierLength != sha256.Size {
			t.Fatalf("preserved credential %s = %q %q verifier=%d",
				credentialID, protocolName, credentialState, verifierLength)
		}
	}

	var discoveredDescription, telemetryProcesses, backupPath string
	if err := database.DB().QueryRowContext(ctx, `
		SELECT description FROM sui_discovered_clients
		WHERE node_id = ? AND remote_id = 9001
	`, fixture.suiNodeID).Scan(&discoveredDescription); err != nil || discoveredDescription != "preserve me" {
		t.Fatalf("preserved S-UI discovery = %q, error = %v", discoveredDescription, err)
	}
	if err := database.DB().QueryRowContext(ctx, `
		SELECT CAST(processes_json AS TEXT) FROM node_telemetry_snapshots WHERE node_id = ?
	`, fixture.suiNodeID).Scan(&telemetryProcesses); err != nil || telemetryProcesses != `[{"name":"sing-box"}]` {
		t.Fatalf("preserved telemetry = %q, error = %v", telemetryProcesses, err)
	}
	if err := database.DB().QueryRowContext(ctx,
		"SELECT local_path FROM config_backups",
	).Scan(&backupPath); err != nil || backupPath != "/var/lib/hyfleet-backups/migration.bak" {
		t.Fatalf("preserved backup = %q, error = %v", backupPath, err)
	}
}

func assertMigration0010ObjectsReadable(
	t *testing.T,
	ctx context.Context,
	database *Store,
	fixture migration0010Fixture,
) {
	t.Helper()
	native, err := database.GetNode(ctx, fixture.nativeNodeID)
	if err != nil {
		t.Fatalf("GetNode(preserved native) error = %v", err)
	}
	if native.Name != "Legacy Native" || native.AdapterType != "native_hysteria2" ||
		native.Provider != "DMIT" || native.DesiredVersion != 7 || native.AppliedVersion != 6 ||
		native.PublicHost != "native.example.com" || native.PublicPort != 8443 ||
		native.Hostname != "native-host" || native.TLSCertFingerprint != "AA:BB:CC" ||
		native.TLSPublicKeySHA256 != "legacy-native-public-key-pin" {
		t.Fatalf("preserved native node = %#v", native)
	}
	sui, err := database.GetNode(ctx, fixture.suiNodeID)
	if err != nil {
		t.Fatalf("GetNode(preserved S-UI) error = %v", err)
	}
	if sui.Name != "Legacy S-UI" || sui.AdapterType != "s_ui" ||
		sui.DesiredVersion != 4 || sui.AppliedVersion != 4 ||
		len(sui.SUITargetInboundIDs) != 2 || sui.SUITargetInboundIDs[0] != 41 ||
		sui.SUITargetInboundIDs[1] != 42 {
		t.Fatalf("preserved S-UI node = %#v", sui)
	}

	user, err := database.GetUser(ctx, fixture.userID)
	if err != nil {
		t.Fatalf("GetUser(preserved) error = %v", err)
	}
	if user.Username != "legacy-subscriber" || user.TrafficUsedBytes != 579 ||
		len(user.Assignments) != 2 {
		t.Fatalf("preserved user = %#v", user)
	}
	revealed, err := database.RevealAssignmentCredential(
		ctx, fixture.userID, fixture.nativeNodeID, fixture.masterKey,
	)
	if err != nil || revealed.Secret != fixture.nativeDesiredSecret ||
		revealed.Assignment.DesiredCredentialID != fixture.nativeDesiredCredential ||
		revealed.Assignment.AppliedCredentialID != fixture.nativeAppliedCredential ||
		revealed.Assignment.CredentialProtocol != CredentialProtocolHY2 {
		t.Fatalf("RevealAssignmentCredential(preserved) = (%#v, %v)", revealed, err)
	}

	for nodeID, expected := range map[string]struct {
		version      int64
		credentialID string
	}{
		fixture.nativeNodeID: {version: 7, credentialID: fixture.nativeDesiredCredential},
		fixture.suiNodeID:    {version: 4, credentialID: fixture.suiCredential},
	} {
		envelope, err := database.GetDesiredSnapshot(ctx, nodeID, expected.version)
		if err != nil || envelope.Snapshot.NodeID != nodeID ||
			envelope.Snapshot.Version != expected.version || len(envelope.Snapshot.Users) != 1 ||
			envelope.Snapshot.Users[0].Credential.Ref != expected.credentialID {
			t.Fatalf("preserved desired snapshot %s/%d = (%#v, %v)",
				nodeID, expected.version, envelope, err)
		}
	}
	appliedEnvelope, err := database.GetDesiredSnapshot(ctx, fixture.nativeNodeID, 6)
	if err != nil || appliedEnvelope.Snapshot.Version != 6 {
		t.Fatalf("preserved applied-version snapshot = (%#v, %v)", appliedEnvelope, err)
	}

	tokens, err := database.ListSubscriptionTokens(ctx, fixture.userID)
	if err != nil || len(tokens) != 1 || tokens[0].ID != fixture.subscriptionTokenID ||
		tokens[0].Name != "legacy primary" || len(tokens[0].AllowedFormats) != 2 ||
		tokens[0].AllowedFormats[0] != "uri" || tokens[0].AllowedFormats[1] != "sing-box" {
		t.Fatalf("preserved subscription tokens = (%#v, %v)", tokens, err)
	}
	subscription, err := database.ResolveSubscription(
		ctx, cryptoutil.TokenHash(fixture.subscriptionSecret), "uri",
		fixture.now.Add(time.Hour), fixture.masterKey,
	)
	if err != nil || len(subscription.Endpoints) != 1 ||
		subscription.Endpoints[0].NodeID != fixture.suiNodeID ||
		subscription.Endpoints[0].AdapterType != "s_ui" ||
		subscription.Endpoints[0].Credential != fixture.suiSecret {
		t.Fatalf("ResolveSubscription(preserved) = (%#v, %v)", subscription, err)
	}
}

func assertMigration0011ForeignKeysAndConstraints(
	t *testing.T,
	ctx context.Context,
	database *Store,
	fixture migration0010Fixture,
) {
	t.Helper()
	var foreignKeys int
	if err := database.DB().QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("upgraded foreign_keys = %d, error = %v", foreignKeys, err)
	}
	if err := assertNoForeignKeyViolations(ctx, database.DB()); err != nil {
		t.Fatalf("upgraded foreign-key violation: %v", err)
	}

	invalidNodeID := uuid.NewString()
	if _, err := database.DB().ExecContext(ctx, `
		INSERT INTO nodes(id, name, adapter_type, created_at, updated_at)
		VALUES (?, 'Invalid Adapter', 'trojan', ?, ?)
	`, invalidNodeID, fixture.now.UnixMilli(), fixture.now.UnixMilli()); err == nil {
		t.Fatal("upgraded nodes CHECK accepted an unsupported adapter")
	}
	if _, err := database.DB().ExecContext(ctx, `
		INSERT INTO nodes(id, name, adapter_type, created_at, updated_at)
		VALUES (?, 'legacy native', 'native_hysteria2', ?, ?)
	`, uuid.NewString(), fixture.now.UnixMilli(), fixture.now.UnixMilli()); err == nil {
		t.Fatal("upgraded active-node name constraint accepted a duplicate")
	}
	if _, err := database.DB().ExecContext(ctx, `
		INSERT INTO user_credentials(
			id, user_id, node_id, protocol, secret_ciphertext, verifier_sha256,
			secret_fingerprint, state, created_at
		) VALUES (?, ?, ?, 'trojan', x'01', ?, 'invalid', 'staged', ?)
	`, uuid.NewString(), fixture.userID, fixture.nativeNodeID,
		bytes.Repeat([]byte{0x41}, sha256.Size), fixture.now.UnixMilli()); err == nil {
		t.Fatal("upgraded credential CHECK accepted an unsupported protocol")
	}
	if _, err := database.DB().ExecContext(ctx, `
		INSERT INTO node_snapshots(node_id, version, canonical_json, sha256, created_at)
		VALUES (?, 1, '{}', ?, ?)
	`, uuid.NewString(), bytes.Repeat([]byte{0x42}, sha256.Size), fixture.now.UnixMilli()); err == nil {
		t.Fatal("upgraded snapshot foreign key accepted a missing node")
	}

	deleteSUI, err := database.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin cascade check: %v", err)
	}
	if _, err := deleteSUI.ExecContext(ctx, "DELETE FROM nodes WHERE id = ?", fixture.suiNodeID); err != nil {
		_ = deleteSUI.Rollback()
		t.Fatalf("delete S-UI node for cascade check: %v", err)
	}
	for table, column := range map[string]string{
		"node_snapshots":           "node_id",
		"user_credentials":         "node_id",
		"node_user_assignments":    "node_id",
		"sui_discovered_inbounds":  "node_id",
		"sui_discovered_clients":   "node_id",
		"node_telemetry_snapshots": "node_id",
	} {
		var count int
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = ?", table, column)
		if err := deleteSUI.QueryRowContext(ctx, query, fixture.suiNodeID).Scan(&count); err != nil || count != 0 {
			_ = deleteSUI.Rollback()
			t.Fatalf("%s cascade count = %d, error = %v", table, count, err)
		}
	}
	if err := deleteSUI.Rollback(); err != nil {
		t.Fatalf("rollback cascade check: %v", err)
	}

	if _, err := database.DB().ExecContext(ctx,
		"DELETE FROM user_credentials WHERE id = ?", fixture.nativeAppliedCredential,
	); err == nil {
		t.Fatal("assignment foreign key did not protect its applied credential")
	}
	if _, err := database.DB().ExecContext(ctx,
		"DELETE FROM nodes WHERE id = ?", fixture.nativeNodeID,
	); err == nil {
		t.Fatal("traffic history foreign key did not protect its node")
	}
	if err := assertNoForeignKeyViolations(ctx, database.DB()); err != nil {
		t.Fatalf("foreign-key violation after constraint checks: %v", err)
	}
}

func assertMigration0011AcceptsRealityData(
	t *testing.T,
	ctx context.Context,
	database *Store,
	fixture migration0010Fixture,
) {
	t.Helper()
	realityNodeID := uuid.NewString()
	reality, err := database.CreateNode(ctx, NewNode{
		ID: realityNodeID, Name: "Migrated Reality", AdapterType: AdapterSingBoxVLESSReality,
		PublicHost: "reality.example.com", PublicPort: 18443,
		SNI: "www.cloudflare.com", Enabled: true,
		VLESSReality: &VLESSRealitySettings{
			HandshakeServer: "www.cloudflare.com", HandshakeServerPort: 443,
			DesiredKeyGeneration: 1,
		},
		Now: fixture.now.Add(2 * time.Hour),
	})
	if err != nil || reality.AdapterType != AdapterSingBoxVLESSReality ||
		reality.VLESSReality == nil ||
		reality.VLESSReality.HandshakeServer != "www.cloudflare.com" {
		t.Fatalf("CreateNode(Reality after migration) = (%#v, %v)", reality, err)
	}
	capability := "desired_vless_reality_v1"
	if _, err := database.DB().ExecContext(ctx, `
		INSERT INTO node_agent_capabilities(node_id, capability, reported_at)
		VALUES (?, ?, ?)
	`, reality.ID, capability, fixture.now.Add(2*time.Hour).UnixMilli()); err != nil {
		t.Fatalf("insert Reality capability after migration: %v", err)
	}
	capabilities, err := database.ListAgentCapabilities(ctx, reality.ID)
	if err != nil || len(capabilities) != 1 || capabilities[0] != capability {
		t.Fatalf("ListAgentCapabilities(after migration) = (%#v, %v)", capabilities, err)
	}

	vlessUser, credentials, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "migrated-vless-user", Enabled: true,
		NodeIDs: []string{reality.ID}, Now: fixture.now.Add(2*time.Hour + time.Minute),
	}, fixture.masterKey)
	if err != nil || len(vlessUser.Assignments) != 1 || len(credentials) != 1 ||
		vlessUser.Assignments[0].CredentialProtocol != CredentialProtocolVLESS ||
		credentials[0].Secret == "" {
		t.Fatalf("CreateUser(VLESS after migration) = (%#v, %#v, %v)",
			vlessUser, credentials, err)
	}
	var protocolName, credentialNodeID string
	if err := database.DB().QueryRowContext(ctx, `
		SELECT protocol, node_id FROM user_credentials WHERE id = ?
	`, vlessUser.Assignments[0].DesiredCredentialID).Scan(
		&protocolName, &credentialNodeID,
	); err != nil || protocolName != CredentialProtocolVLESS || credentialNodeID != reality.ID {
		t.Fatalf("stored VLESS credential = %q/%q, error = %v", protocolName, credentialNodeID, err)
	}
	envelope, err := database.GetDesiredSnapshot(ctx, reality.ID, reality.DesiredVersion)
	if err != nil || envelope.Snapshot.SchemaVersion != 2 ||
		envelope.Snapshot.VLESSReality == nil {
		t.Fatalf("Reality snapshot after migration = (%#v, %v)", envelope, err)
	}
	if err := assertNoForeignKeyViolations(ctx, database.DB()); err != nil {
		t.Fatalf("foreign-key violation after Reality creation: %v", err)
	}
}

func assertNoForeignKeyViolations(ctx context.Context, database interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) error {
	rows, err := database.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var constraint int
		if err := rows.Scan(&table, &rowID, &parent, &constraint); err != nil {
			return err
		}
		return fmt.Errorf("table=%s rowid=%v parent=%s constraint=%d",
			table, rowID, parent, constraint)
	}
	return rows.Err()
}

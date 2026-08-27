package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/Sen62455/PolyFleet/internal/store/migrations"
	"github.com/google/uuid"
)

func TestOpenAppliesMigrationsAndSQLitePolicy(t *testing.T) {
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	checks := map[string]string{
		"foreign_keys": "1",
		"busy_timeout": "5000",
		"journal_mode": "wal",
	}
	for pragma, want := range checks {
		var value string
		if err := database.DB().QueryRow("PRAGMA " + pragma).Scan(&value); err != nil {
			t.Fatalf("PRAGMA %s error = %v", pragma, err)
		}
		if strings.ToLower(value) != want {
			t.Fatalf("PRAGMA %s = %q, want %q", pragma, value, want)
		}
	}
	var migrations int
	if err := database.DB().QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&migrations); err != nil || migrations == 0 {
		t.Fatalf("migration count = %d, error = %v", migrations, err)
	}
	var operationsLayer int
	if err := database.DB().QueryRow(`
		SELECT COUNT(*) FROM schema_migrations WHERE version = '0014_operations_layer.sql'
	`).Scan(&operationsLayer); err != nil || operationsLayer != 1 {
		t.Fatalf("operations layer migration count = %d, error = %v", operationsLayer, err)
	}
	var notificationAutomation int
	if err := database.DB().QueryRow(`
		SELECT COUNT(*) FROM schema_migrations WHERE version = '0015_notification_automation.sql'
	`).Scan(&notificationAutomation); err != nil || notificationAutomation != 1 {
		t.Fatalf("notification automation migration count = %d, error = %v", notificationAutomation, err)
	}
}

func TestHeartbeatPoolSurvivesBusyReadConnection(t *testing.T) {
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	if got := database.DB().Stats().MaxOpenConnections; got != 4 {
		t.Fatalf("MaxOpenConnections = %d, want 4", got)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	identities := make([]AgentIdentity, 0, 3)
	for index := 0; index < 3; index++ {
		node, err := database.CreateNode(t.Context(), NewNode{
			ID: uuid.NewString(), Name: fmt.Sprintf("heartbeat-node-%d", index),
			AdapterType: "native_hysteria2", Enabled: true, Now: now,
		})
		if err != nil {
			t.Fatalf("CreateNode(%d) error = %v", index, err)
		}
		installationID := uuid.NewString()
		if _, err := database.DB().ExecContext(t.Context(), `
			UPDATE nodes SET agent_installation_id = ? WHERE id = ?
		`, installationID, node.ID); err != nil {
			t.Fatalf("bind node %d: %v", index, err)
		}
		identities = append(identities, AgentIdentity{
			NodeID: node.ID, InstallationID: installationID, AdapterType: "native_hysteria2", Enabled: true,
		})
	}
	heldRead, err := database.DB().Conn(t.Context())
	if err != nil {
		t.Fatalf("reserve read connection: %v", err)
	}
	defer heldRead.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	errorsByNode := make(chan error, len(identities))
	var wait sync.WaitGroup
	for _, identity := range identities {
		identity := identity
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := database.RecordHeartbeat(ctx, identity, protocol.HeartbeatRequest{
				InstallationID: identity.InstallationID,
				Agent:          protocol.AgentInfo{Version: "pool-test", Protocol: protocol.MajorVersion},
				Core:           protocol.CoreInfo{Name: "hysteria", Running: true},
				Host:           protocol.HostMetrics{MemoryTotalBytes: 1, DiskTotalBytes: 1},
				SampledAt:      now,
			}, now)
			errorsByNode <- err
		}()
	}
	wait.Wait()
	close(errorsByNode)
	for err := range errorsByNode {
		if err != nil {
			t.Fatalf("RecordHeartbeat() while one connection is reserved: %v", err)
		}
	}
}

func TestHeartbeatCapabilitiesRefreshAndMissingFieldPreservation(t *testing.T) {
	database, err := Open(t.Context(), filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	node, err := database.CreateNode(t.Context(), NewNode{
		ID: uuid.NewString(), Name: "heartbeat-capabilities",
		AdapterType: "native_hysteria2", Enabled: true, Now: now,
	})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	installationID := uuid.NewString()
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE nodes SET agent_installation_id = ? WHERE id = ?
	`, installationID, node.ID); err != nil {
		t.Fatalf("bind node: %v", err)
	}
	if _, err := database.DB().ExecContext(t.Context(), `
		INSERT INTO node_agent_capabilities(node_id, capability, reported_at)
		VALUES (?, 'enrollment_capability', ?)
	`, node.ID, now.UnixMilli()); err != nil {
		t.Fatalf("seed capability: %v", err)
	}
	identity := AgentIdentity{
		NodeID: node.ID, InstallationID: installationID,
		AdapterType: "native_hysteria2", Enabled: true,
	}
	heartbeat := protocol.HeartbeatRequest{
		InstallationID: installationID,
		Agent:          protocol.AgentInfo{Version: "test", Protocol: protocol.MajorVersion},
		Core:           protocol.CoreInfo{Name: "hysteria", Running: true},
		Host:           protocol.HostMetrics{MemoryTotalBytes: 1, DiskTotalBytes: 1},
		SampledAt:      now,
	}
	if _, err := database.RecordHeartbeat(t.Context(), identity, heartbeat, now); err != nil {
		t.Fatalf("RecordHeartbeat(missing capabilities) error = %v", err)
	}
	capabilities, err := database.ListAgentCapabilities(t.Context(), node.ID)
	if err != nil || fmt.Sprint(capabilities) != "[enrollment_capability]" {
		t.Fatalf("capabilities after omitted field = (%v, %v)", capabilities, err)
	}
	heartbeat.Capabilities = []string{"runtime_capability", "runtime_capability"}
	heartbeat.SampledAt = now.Add(time.Minute)
	if _, err := database.RecordHeartbeat(t.Context(), identity, heartbeat, now.Add(time.Minute)); err != nil {
		t.Fatalf("RecordHeartbeat(refresh capabilities) error = %v", err)
	}
	capabilities, err = database.ListAgentCapabilities(t.Context(), node.ID)
	if err != nil || fmt.Sprint(capabilities) != "[runtime_capability]" {
		t.Fatalf("capabilities after refresh = (%v, %v)", capabilities, err)
	}
	heartbeat.Capabilities = []string{}
	heartbeat.SampledAt = now.Add(2 * time.Minute)
	if _, err := database.RecordHeartbeat(t.Context(), identity, heartbeat, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("RecordHeartbeat(clear capabilities) error = %v", err)
	}
	capabilities, err = database.ListAgentCapabilities(t.Context(), node.ID)
	if err != nil || len(capabilities) != 0 {
		t.Fatalf("capabilities after explicit empty field = (%v, %v)", capabilities, err)
	}
}

func TestEnrollmentTokenRequiresExistingAdministrator(t *testing.T) {
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC()
	node, err := database.CreateNode(context.Background(), NewNode{
		ID: uuid.NewString(), Name: "test-node", AdapterType: "native_hysteria2", Enabled: true, Now: now,
	})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := database.CreateEnrollmentToken(context.Background(), node.ID, "missing-admin", now, 10*time.Minute); err == nil {
		t.Fatal("CreateEnrollmentToken() bypassed the administrator foreign key")
	}
	admin := Admin{
		ID: uuid.NewString(), Username: "admin", PasswordHash: "unused", CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateAdmin(context.Background(), admin); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	issued, err := database.CreateEnrollmentToken(context.Background(), node.ID, admin.ID, now, 10*time.Minute)
	if err != nil {
		t.Fatalf("CreateEnrollmentToken(valid) error = %v", err)
	}
	if issued.Token == "" || len(cryptoutil.TokenHash(issued.Token)) != 32 {
		t.Fatal("issued enrollment token is invalid")
	}
}

func TestActiveNodeNameMigrationPreservesRelationsAndAllowsReuse(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = legacy.Close() })
	if _, err := legacy.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable legacy foreign keys: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatalf("create legacy migration table: %v", err)
	}
	foundation, err := migrations.Files.ReadFile("0001_foundation.sql")
	if err != nil {
		t.Fatalf("read foundation migration: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, string(foundation)); err != nil {
		t.Fatalf("apply foundation migration: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	nodeID := uuid.NewString()
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO nodes(id, name, adapter_type, created_at, updated_at)
		VALUES (?, 'LisaHost', 'native_hysteria2', ?, ?)
	`, nodeID, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatalf("insert legacy node: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO node_snapshots(node_id, version, canonical_json, sha256, created_at)
		VALUES (?, 1, '{}', x'00', ?)
	`, nodeID, now.UnixMilli()); err != nil {
		t.Fatalf("insert legacy snapshot: %v", err)
	}
	if _, err := legacy.ExecContext(ctx,
		"INSERT INTO schema_migrations(version, applied_at) VALUES ('0001_foundation.sql', ?)",
		now.UnixMilli(),
	); err != nil {
		t.Fatalf("record foundation migration: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open(upgrade) error = %v", err)
	}
	defer database.Close()
	var snapshotCount int
	if err := database.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM node_snapshots WHERE node_id = ?", nodeID,
	).Scan(&snapshotCount); err != nil || snapshotCount != 1 {
		t.Fatalf("snapshot count = %d, error = %v", snapshotCount, err)
	}
	if err := database.ArchiveNode(ctx, nodeID, now.Add(time.Second)); err != nil {
		t.Fatalf("ArchiveNode() error = %v", err)
	}
	replacement, err := database.CreateNode(ctx, NewNode{
		ID: uuid.NewString(), Name: "lisahost", AdapterType: "s_ui", Enabled: true, Now: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("CreateNode(reuse archived name) error = %v", err)
	}
	if replacement.ID == nodeID || replacement.Name != "lisahost" {
		t.Fatalf("unexpected replacement node: %#v", replacement)
	}
}

func TestPhaseThreeMigrationPreservesPhaseTwoUsers(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "phase-two.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = legacy.Close() })
	if _, err := legacy.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable legacy foreign keys: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)
	`); err != nil {
		t.Fatalf("create migration table: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	for _, name := range []string{"0001_foundation.sql", "0002_active_node_name.sql", "0003_native_users.sql"} {
		body, readErr := migrations.Files.ReadFile(name)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		if _, err := legacy.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if _, err := legacy.ExecContext(ctx,
			"INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", name, now.UnixMilli(),
		); err != nil {
			t.Fatalf("record %s: %v", name, err)
		}
	}
	nodeID := uuid.NewString()
	userID := uuid.NewString()
	credentialID := uuid.NewString()
	assignmentID := uuid.NewString()
	verifier := sha256.Sum256([]byte("phase-two-secret"))
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO nodes(id, name, adapter_type, created_at, updated_at)
		VALUES (?, 'LisaHost', 'native_hysteria2', ?, ?)
	`, nodeID, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatalf("seed phase two node: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO users(id, username, display_name, enabled, created_at, updated_at)
		VALUES (?, 'existing-user', 'Existing User', 1, ?, ?)
	`, userID, now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatalf("seed phase two user: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO user_credentials(
			id, user_id, node_id, protocol, secret_ciphertext, verifier_sha256,
			secret_fingerprint, state, created_at
		) VALUES (?, ?, ?, 'hysteria2', ?, ?, 'fp_existing', 'applied', ?)
	`, credentialID, userID, nodeID, []byte("encrypted"), verifier[:], now.UnixMilli()); err != nil {
		t.Fatalf("seed phase two credential: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO node_user_assignments(
			id, node_id, user_id, desired_credential_id, applied_credential_id,
			enabled, desired_version, applied_version, state, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, 1, 1, 1, 'applied', ?, ?)
	`, assignmentID, nodeID, userID, credentialID, credentialID,
		now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatalf("seed phase two assignment: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close phase two database: %v", err)
	}

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open(upgrade) error = %v", err)
	}
	defer database.Close()
	user, err := database.GetUser(ctx, userID)
	if err != nil {
		t.Fatalf("GetUser(upgraded) error = %v", err)
	}
	if user.Username != "existing-user" || user.TrafficUsedBytes != 0 || user.QuotaState != "unlimited" ||
		len(user.Assignments) != 1 || user.Assignments[0].CredentialFingerprint != "fp_existing" {
		t.Fatalf("upgraded user = %#v", user)
	}
	var violations int
	rows, err := database.DB().QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	for rows.Next() {
		violations++
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close foreign_key_check: %v", err)
	}
	if violations != 0 {
		t.Fatalf("foreign key violations after upgrade = %d", violations)
	}
}

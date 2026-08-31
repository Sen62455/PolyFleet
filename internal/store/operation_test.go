package store

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/google/uuid"
)

func TestRetryNodeSyncRebuildsOnlyLatestDesiredState(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	database, _, node, _ := newOperationTestStore(t, "native_hysteria2", now)
	user, _, err := database.CreateUser(t.Context(), NewUser{
		ID: uuid.NewString(), Username: "offline-user", DisplayName: "First",
		Enabled: true, NodeIDs: []string{node.ID}, Now: now.Add(time.Second),
	}, bytes.Repeat([]byte{0x72}, 32))
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	user, err = database.UpdateUser(t.Context(), user.ID, UpdateUser{
		Username: "offline-user", DisplayName: "Latest", Enabled: false,
		TrafficLimitBytes: 7 << 30, Now: now.Add(2 * time.Second),
	})
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	before, err := database.GetNode(t.Context(), node.ID)
	if err != nil {
		t.Fatalf("GetNode(before retry) error = %v", err)
	}
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE node_user_assignments SET state = 'failed',
			last_error_code = 'offline', last_error_message = 'node unavailable'
		WHERE node_id = ? AND user_id = ?
	`, node.ID, user.ID); err != nil {
		t.Fatalf("mark assignment failed: %v", err)
	}
	retried, err := database.RetryNodeSync(t.Context(), node.ID, now.Add(3*time.Second))
	if err != nil || retried.DesiredVersion != before.DesiredVersion+1 || retried.Status != "pending" {
		t.Fatalf("RetryNodeSync() = %#v, error = %v", retried, err)
	}
	envelope, err := database.GetDesiredSnapshot(t.Context(), node.ID, retried.DesiredVersion)
	if err != nil || len(envelope.Snapshot.Users) != 1 ||
		envelope.Snapshot.Users[0].Username != "offline-user" || envelope.Snapshot.Users[0].Enabled {
		t.Fatalf("latest retry snapshot = %#v, error = %v", envelope.Snapshot, err)
	}
	storedUser, err := database.GetUser(t.Context(), user.ID)
	if err != nil || len(storedUser.Assignments) != 1 ||
		storedUser.Assignments[0].State != "pending" ||
		storedUser.Assignments[0].DesiredVersion != retried.DesiredVersion {
		t.Fatalf("assignment after retry = %#v, error = %v", storedUser.Assignments, err)
	}
}

func TestEmptyNodeHistoryReleasesSingleConnection(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	database, _, node, _ := newOperationTestStore(t, "native_hysteria2", now)
	database.DB().SetMaxOpenConns(1)
	database.DB().SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	operations, err := database.ListNodeOperations(ctx, node.ID, 50)
	if err != nil {
		t.Fatalf("ListNodeOperations(empty) error = %v", err)
	}
	if len(operations) != 0 {
		t.Fatalf("ListNodeOperations(empty) = %#v", operations)
	}
	backups, err := database.ListConfigBackups(ctx, node.ID, 50)
	if err != nil {
		t.Fatalf("ListConfigBackups(empty) error = %v", err)
	}
	if len(backups) != 0 {
		t.Fatalf("ListConfigBackups(empty) = %#v", backups)
	}
}

func TestRealityNodeOperationRejectsLogTail(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	database, admin, _, _ := newOperationTestStore(t, "native_hysteria2", now)
	node := createVLESSRealityTestNode(t, database, "Reality operations", now)
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE nodes SET agent_installation_id = ? WHERE id = ?
	`, uuid.NewString(), node.ID); err != nil {
		t.Fatalf("bind Reality Agent: %v", err)
	}
	if _, err := database.CreateNodeOperation(
		t.Context(), node.ID, "tail_core_log", 20, admin.ID, now,
	); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Reality tail_core_log error = %v", err)
	}
}

func TestPingOperationStoresCanonicalIPAddress(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	database, admin, node, identity := newOperationTestStore(t, "native_hysteria2", now)
	if _, err := database.CreateTargetedNodeOperation(
		t.Context(), node.ID, "ping", 0, "example.com", admin.ID, now,
	); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("CreateTargetedNodeOperation(hostname) error = %v", err)
	}
	operation, err := database.CreateTargetedNodeOperation(
		t.Context(), node.ID, "ping", 0, "2001:0db8:0:0::1", admin.ID, now,
	)
	if err != nil || operation.Target != "2001:db8::1" {
		t.Fatalf("CreateTargetedNodeOperation() = %#v, error = %v", operation, err)
	}
	pending, err := database.ListPendingNodeOperations(t.Context(), identity, 0, now, 20)
	if err != nil || len(pending) != 1 || pending[0].Target != "2001:db8::1" {
		t.Fatalf("ListPendingNodeOperations(ping) = %#v, error = %v", pending, err)
	}
}

func newOperationTestStore(t *testing.T, adapter string, now time.Time) (*Store, Admin, Node, AgentIdentity) {
	t.Helper()
	database, err := Open(context.Background(), filepath.Join(t.TempDir(), "operations.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	admin := Admin{
		ID: uuid.NewString(), Username: "operator", PasswordHash: "unused",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := database.CreateAdmin(t.Context(), admin); err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	node, err := database.CreateNode(t.Context(), NewNode{
		ID: uuid.NewString(), Name: "operations-node", AdapterType: adapter,
		PublicHost: "node.example.test", Enabled: true, Now: now,
	})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	installationID := uuid.NewString()
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE nodes SET agent_installation_id = ?, status = 'online',
			last_seen_at = ?, core_running = 1, updated_at = ? WHERE id = ?
	`, installationID, now.UnixMilli(), now.UnixMilli(), node.ID); err != nil {
		t.Fatalf("bind Agent: %v", err)
	}
	identity := AgentIdentity{
		NodeID: node.ID, InstallationID: installationID, AdapterType: adapter, Enabled: true,
	}
	return database, admin, node, identity
}

func TestNodeOperationsSequenceResultRetryAndBackupMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	database, admin, node, identity := newOperationTestStore(t, "native_hysteria2", now)

	logOperation, err := database.CreateNodeOperation(
		t.Context(), node.ID, "tail_core_log", 0, admin.ID, now,
	)
	if err != nil {
		t.Fatalf("CreateNodeOperation(log) error = %v", err)
	}
	backupOperation, err := database.CreateNodeOperation(
		t.Context(), node.ID, "backup_config", 0, admin.ID, now.Add(time.Millisecond),
	)
	if err != nil {
		t.Fatalf("CreateNodeOperation(backup) error = %v", err)
	}
	if logOperation.Sequence != 1 || logOperation.MaxLines != DefaultOperationLogLines ||
		backupOperation.Sequence != 2 {
		t.Fatalf("operation sequence/defaults = %#v, %#v", logOperation, backupOperation)
	}
	pending, err := database.ListPendingNodeOperations(t.Context(), identity, 0, now, 20)
	if err != nil || len(pending) != 2 || pending[0].Sequence != 1 || pending[1].Sequence != 2 {
		t.Fatalf("ListPendingNodeOperations() = %#v, error = %v", pending, err)
	}
	failure := protocol.OperationResultRequest{
		Sequence: 1, Status: "failed", ErrorCode: "core_log_failed",
		ErrorMessage: "journal unavailable", CompletedAt: now.Add(time.Second),
	}
	if err := database.RecordNodeOperationResult(
		t.Context(), identity, logOperation.ID, failure, now.Add(time.Second),
	); err != nil {
		t.Fatalf("RecordNodeOperationResult(failed) error = %v", err)
	}
	if err := database.RecordNodeOperationResult(
		t.Context(), identity, logOperation.ID, failure, now.Add(2*time.Second),
	); err != nil {
		t.Fatalf("idempotent result error = %v", err)
	}
	conflicting := failure
	conflicting.ErrorMessage = "different"
	if err := database.RecordNodeOperationResult(
		t.Context(), identity, logOperation.ID, conflicting, now.Add(2*time.Second),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting result error = %v, want ErrConflict", err)
	}
	retry, err := database.RetryNodeOperation(
		t.Context(), node.ID, logOperation.ID, admin.ID, now.Add(3*time.Second),
	)
	if err != nil || retry.Sequence != 3 || retry.Attempt != 2 || retry.RetryOf != logOperation.ID {
		t.Fatalf("RetryNodeOperation() = %#v, error = %v", retry, err)
	}

	backupResult := protocol.OperationResultRequest{
		Sequence: 2, Status: "succeeded", Output: "configuration backup created",
		Backup: &protocol.Backup{
			LocalPath: "/var/lib/hyfleet-backups/config-test.bak",
			SHA256:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			SizeBytes: 321,
		},
		CompletedAt: now.Add(4 * time.Second),
	}
	if err := database.RecordNodeOperationResult(
		t.Context(), identity, backupOperation.ID, backupResult, now.Add(4*time.Second),
	); err != nil {
		t.Fatalf("RecordNodeOperationResult(backup) error = %v", err)
	}
	backups, err := database.ListConfigBackups(t.Context(), node.ID, 50)
	if err != nil || len(backups) != 1 || backups[0].SizeBytes != 321 ||
		backups[0].OperationID != backupOperation.ID {
		t.Fatalf("ListConfigBackups() = %#v, error = %v", backups, err)
	}
	var schemaContainsBody int
	if err := database.DB().QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM pragma_table_info('config_backups')
		WHERE name IN ('body', 'content', 'config')
	`).Scan(&schemaContainsBody); err != nil || schemaContainsBody != 0 {
		t.Fatalf("config backup schema stores body: count=%d error=%v", schemaContainsBody, err)
	}
}

func TestValidConfigBackupPathAllowsOnlyManagedRoots(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/var/lib/hyfleet-backups/config-test.bak", want: true},
		{path: "/var/lib/hyfleet-backups-lab/config-test.bak", want: true},
		{path: "/var/lib/hyfleet-backups"},
		{path: "/var/lib/hyfleet-backups-lab"},
		{path: "/var/lib/hyfleet-backups-lab-escape/config-test.bak"},
		{path: "/var/lib/hyfleet-backups/../hyfleet-backups-lab/config-test.bak"},
		{path: "/tmp/config-test.bak"},
	}
	for _, test := range tests {
		if got := validConfigBackupPath(test.path); got != test.want {
			t.Errorf("validConfigBackupPath(%q) = %v, want %v", test.path, got, test.want)
		}
	}
}

func TestAlertReconciliationDeduplicatesAcknowledgesAndResolves(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	database, admin, node, _ := newOperationTestStore(t, "native_hysteria2", now)
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE nodes SET status = 'degraded', status_reason = 'adapter unavailable',
			core_running = 0, usage_enabled = 1, usage_error_code = 'stats_failed',
			last_seen_at = ?, updated_at = ? WHERE id = ?
	`, now.UnixMilli(), now.UnixMilli(), node.ID); err != nil {
		t.Fatalf("set alert conditions: %v", err)
	}
	reconcileAt := now.Add(time.Minute)
	if err := database.ReconcileAlerts(t.Context(), reconcileAt, 90*time.Second, 5*time.Minute); err != nil {
		t.Fatalf("ReconcileAlerts(first) error = %v", err)
	}
	if err := database.ReconcileAlerts(t.Context(), reconcileAt.Add(time.Second), 90*time.Second, 5*time.Minute); err != nil {
		t.Fatalf("ReconcileAlerts(second) error = %v", err)
	}
	alerts, err := database.ListAlerts(t.Context(), "active", 100)
	if err != nil || len(alerts) != 3 {
		t.Fatalf("active alerts = %#v, error = %v", alerts, err)
	}
	seen := map[string]Alert{}
	for _, alert := range alerts {
		seen[alert.Type] = alert
	}
	for _, alertType := range []string{"degraded", "core_down", "usage_error"} {
		if _, ok := seen[alertType]; !ok {
			t.Fatalf("missing %s alert in %#v", alertType, alerts)
		}
	}
	acknowledged, err := database.AcknowledgeAlert(
		t.Context(), seen["core_down"].ID, admin.ID, reconcileAt.Add(2*time.Second),
	)
	if err != nil || acknowledged.Status != "acknowledged" {
		t.Fatalf("AcknowledgeAlert() = %#v, error = %v", acknowledged, err)
	}
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE nodes SET status = 'online', status_reason = '', core_running = 1,
			usage_error_code = '', desired_version = applied_version,
			last_seen_at = ?, updated_at = ? WHERE id = ?
	`, reconcileAt.UnixMilli(), reconcileAt.UnixMilli(), node.ID); err != nil {
		t.Fatalf("clear alert conditions: %v", err)
	}
	if err := database.ReconcileAlerts(
		t.Context(), reconcileAt.Add(3*time.Second), 90*time.Second, 5*time.Minute,
	); err != nil {
		t.Fatalf("ReconcileAlerts(resolve) error = %v", err)
	}
	active, err := database.ListAlerts(t.Context(), "active", 100)
	if err != nil || len(active) != 0 {
		t.Fatalf("active alerts after recovery = %#v, error = %v", active, err)
	}
	resolved, err := database.GetAlert(t.Context(), seen["core_down"].ID)
	if err != nil || resolved.Status != "resolved" || resolved.ResolvedAt == nil {
		t.Fatalf("resolved acknowledged alert = %#v, error = %v", resolved, err)
	}

	if err := database.ReconcileAlerts(
		t.Context(), reconcileAt.Add(3*time.Minute), 90*time.Second, 5*time.Minute,
	); err != nil {
		t.Fatalf("ReconcileAlerts(offline) error = %v", err)
	}
	offline, err := database.ListAlerts(t.Context(), "active", 100)
	if err != nil || len(offline) != 1 || offline[0].Type != "offline" {
		t.Fatalf("offline alerts = %#v, error = %v", offline, err)
	}
}

func TestNodeTrafficQuotaAlertThresholdsAndRecovery(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	database, _, node, _ := newOperationTestStore(t, "native_hysteria2", now)
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE nodes SET agent_installation_id = ?, status = 'online', core_running = 1,
			last_seen_at = ?, traffic_limit_bytes = 1000,
			traffic_cycle_upload_bytes = 400, traffic_cycle_download_bytes = 399,
			updated_at = ? WHERE id = ?
	`, uuid.NewString(), now.UnixMilli(), now.UnixMilli(), node.ID); err != nil {
		t.Fatalf("seed node traffic budget: %v", err)
	}
	reconcile := func(at time.Time) []Alert {
		t.Helper()
		if err := database.ReconcileAlerts(t.Context(), at, 90*time.Second, 5*time.Minute); err != nil {
			t.Fatalf("ReconcileAlerts(%s) error = %v", at, err)
		}
		alerts, err := database.ListAlerts(t.Context(), "active", 100)
		if err != nil {
			t.Fatalf("ListAlerts(active) error = %v", err)
		}
		return alerts
	}
	trafficAlerts := func(alerts []Alert) map[string]Alert {
		t.Helper()
		result := make(map[string]Alert)
		for _, alert := range alerts {
			if alert.Type == "traffic_quota_warning" || alert.Type == "traffic_quota_exhausted" {
				result[alert.Type] = alert
			}
		}
		return result
	}

	if got := trafficAlerts(reconcile(now.Add(time.Second))); len(got) != 0 {
		t.Fatalf("traffic alerts below 80%% = %#v", got)
	}
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE nodes SET traffic_cycle_download_bytes = 400 WHERE id = ?
	`, node.ID); err != nil {
		t.Fatalf("raise traffic to warning threshold: %v", err)
	}
	warning := trafficAlerts(reconcile(now.Add(2 * time.Second)))
	if len(warning) != 1 || warning["traffic_quota_warning"].Severity != "warning" {
		t.Fatalf("traffic alerts at 80%% = %#v", warning)
	}
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE nodes SET traffic_cycle_download_bytes = 600 WHERE id = ?
	`, node.ID); err != nil {
		t.Fatalf("raise traffic to exhausted threshold: %v", err)
	}
	exhausted := trafficAlerts(reconcile(now.Add(3 * time.Second)))
	if len(exhausted) != 1 || exhausted["traffic_quota_exhausted"].Severity != "critical" {
		t.Fatalf("traffic alerts at 100%% = %#v", exhausted)
	}
	resolvedWarning, err := database.GetAlert(t.Context(), warning["traffic_quota_warning"].ID)
	if err != nil || resolvedWarning.Status != "resolved" {
		t.Fatalf("warning after exhaustion = %#v, error = %v", resolvedWarning, err)
	}
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE nodes SET traffic_limit_bytes = 2000 WHERE id = ?
	`, node.ID); err != nil {
		t.Fatalf("raise node allowance: %v", err)
	}
	if got := trafficAlerts(reconcile(now.Add(4 * time.Second))); len(got) != 0 {
		t.Fatalf("traffic alerts after allowance recovery = %#v", got)
	}
	resolvedCritical, err := database.GetAlert(t.Context(), exhausted["traffic_quota_exhausted"].ID)
	if err != nil || resolvedCritical.Status != "resolved" {
		t.Fatalf("critical alert after recovery = %#v, error = %v", resolvedCritical, err)
	}
	if _, err := database.DB().ExecContext(t.Context(), `
		UPDATE nodes SET traffic_cycle_upload_bytes = 800, traffic_cycle_download_bytes = 800
		WHERE id = ?
	`, node.ID); err != nil {
		t.Fatalf("raise traffic to warning threshold after recovery: %v", err)
	}
	reopened := trafficAlerts(reconcile(now.Add(5 * time.Second)))
	if len(reopened) != 1 || reopened["traffic_quota_warning"].ID == warning["traffic_quota_warning"].ID {
		t.Fatalf("reopened traffic warning = %#v", reopened)
	}
}

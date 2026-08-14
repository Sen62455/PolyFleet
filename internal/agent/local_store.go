package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/Sen62455/PolyFleet/internal/protocol"
	_ "modernc.org/sqlite"
)

type trafficCounters struct {
	TX int64
	RX int64
}

type pendingKick struct {
	UserID     string
	Generation int64
}

type suiClientMapping struct {
	UserID                string
	RemoteClientID        int64
	ManagementMode        string
	RemoteName            string
	CredentialFingerprint string
}

type pendingOperationResult struct {
	OperationID string
	Sequence    int64
	Result      protocol.OperationResultRequest
}

type localStore struct {
	db *sql.DB
}

func openLocalStore(ctx context.Context, path string) (*localStore, error) {
	if path == "" {
		return nil, errors.New("Agent database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create Agent database directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create Agent database: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close Agent database bootstrap file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("secure Agent database: %w", err)
	}

	database, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open Agent database: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = FULL",
	} {
		if _, err := database.ExecContext(ctx, pragma); err != nil {
			_ = database.Close()
			return nil, fmt.Errorf("configure Agent database: %w", err)
		}
	}
	store := &localStore{db: database}
	if err := store.migrate(ctx); err != nil {
		_ = database.Close()
		return nil, err
	}
	return store, nil
}

func (store *localStore) Close() error {
	return store.db.Close()
}

func (store *localStore) migrate(ctx context.Context) error {
	if _, err := store.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS usage_runtime (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			source_epoch TEXT NOT NULL,
			next_sequence INTEGER NOT NULL CHECK (next_sequence >= 1),
			initialized INTEGER NOT NULL DEFAULT 0 CHECK (initialized IN (0, 1)),
			counter_epoch TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE IF NOT EXISTS usage_baselines (
			user_id TEXT PRIMARY KEY,
			tx_bytes INTEGER NOT NULL CHECK (tx_bytes >= 0),
			rx_bytes INTEGER NOT NULL CHECK (rx_bytes >= 0),
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS traffic_outbox (
			ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT NOT NULL UNIQUE,
			source_epoch TEXT NOT NULL,
			sequence INTEGER NOT NULL CHECK (sequence >= 1),
			sampled_at INTEGER NOT NULL,
			payload_json BLOB NOT NULL,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			last_attempt_at INTEGER,
			last_error_code TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			UNIQUE(source_epoch, sequence)
		);
		CREATE TABLE IF NOT EXISTS kick_state (
			user_id TEXT PRIMARY KEY,
			applied_generation INTEGER NOT NULL CHECK (applied_generation >= 0),
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS kick_outbox (
			user_id TEXT PRIMARY KEY,
			generation INTEGER NOT NULL CHECK (generation >= 1),
			attempt_count INTEGER NOT NULL DEFAULT 0,
			last_attempt_at INTEGER,
			last_error_code TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS sui_runtime (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			node_id TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS sui_client_mappings (
			user_id TEXT PRIMARY KEY,
			remote_client_id INTEGER NOT NULL UNIQUE CHECK (remote_client_id > 0),
			management_mode TEXT NOT NULL CHECK (management_mode IN ('read_only', 'managed')),
			remote_name TEXT NOT NULL,
			credential_fingerprint TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS operation_results (
			sequence INTEGER PRIMARY KEY CHECK (sequence >= 1),
			operation_id TEXT NOT NULL UNIQUE,
			payload_json BLOB NOT NULL,
			reported_at INTEGER,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			last_attempt_at INTEGER,
			last_error_code TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS operation_results_pending_idx
			ON operation_results(sequence) WHERE reported_at IS NULL;
		CREATE TABLE IF NOT EXISTS operation_runtime (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			last_sequence INTEGER NOT NULL DEFAULT 0 CHECK (last_sequence >= 0)
		);
		INSERT OR IGNORE INTO operation_runtime(singleton, last_sequence) VALUES (1, 0);
	`); err != nil {
		return fmt.Errorf("migrate Agent database: %w", err)
	}
	if err := ensureLocalColumn(ctx, store.db, "usage_runtime", "initialized",
		"ALTER TABLE usage_runtime ADD COLUMN initialized INTEGER NOT NULL DEFAULT 0 CHECK (initialized IN (0, 1))"); err != nil {
		return err
	}
	if err := ensureLocalColumn(ctx, store.db, "usage_runtime", "counter_epoch",
		"ALTER TABLE usage_runtime ADD COLUMN counter_epoch TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO usage_runtime(singleton, source_epoch, next_sequence)
		VALUES (1, ?, 1)
	`, cryptoutil.NewID()); err != nil {
		return fmt.Errorf("initialize traffic source epoch: %w", err)
	}
	return nil
}

func (store *localStore) lastOperationSequence(ctx context.Context) (int64, error) {
	var sequence int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT MAX(value) FROM (
			SELECT last_sequence AS value FROM operation_runtime WHERE singleton = 1
			UNION ALL
			SELECT COALESCE(MAX(sequence), 0) AS value FROM operation_results
		)
	`).Scan(&sequence); err != nil {
		return 0, fmt.Errorf("read last operation sequence: %w", err)
	}
	return sequence, nil
}

func (store *localStore) recordOperationResult(
	ctx context.Context,
	operation protocol.NodeOperation,
	result protocol.OperationResultRequest,
	now time.Time,
) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode operation result: %w", err)
	}
	var existingID string
	var existingPayload []byte
	err = store.db.QueryRowContext(ctx, `
		SELECT operation_id, payload_json FROM operation_results
		WHERE sequence = ? OR operation_id = ?
	`, operation.Sequence, operation.ID).Scan(&existingID, &existingPayload)
	if err == nil {
		if existingID == operation.ID && string(existingPayload) == string(payload) {
			return nil
		}
		return errors.New("operation result conflicts with local sequence")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check local operation result: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO operation_results(sequence, operation_id, payload_json, created_at)
		VALUES (?, ?, ?, ?)
	`, operation.Sequence, operation.ID, payload, now.UnixMilli()); err != nil {
		return fmt.Errorf("record local operation result: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE operation_runtime SET last_sequence = MAX(last_sequence, ?)
		WHERE singleton = 1
	`, operation.Sequence); err != nil {
		return fmt.Errorf("advance local operation sequence: %w", err)
	}
	return nil
}

func (store *localStore) listPendingOperationResults(
	ctx context.Context,
	limit int,
) ([]pendingOperationResult, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT operation_id, sequence, payload_json
		FROM operation_results WHERE reported_at IS NULL
		ORDER BY sequence LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending operation results: %w", err)
	}
	defer rows.Close()
	results := make([]pendingOperationResult, 0)
	for rows.Next() {
		var result pendingOperationResult
		var payload []byte
		if err := rows.Scan(&result.OperationID, &result.Sequence, &payload); err != nil {
			return nil, fmt.Errorf("scan pending operation result: %w", err)
		}
		if err := json.Unmarshal(payload, &result.Result); err != nil {
			return nil, fmt.Errorf("decode pending operation result: %w", err)
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending operation results: %w", err)
	}
	return results, nil
}

func (store *localStore) markOperationResultReported(
	ctx context.Context,
	operationID string,
	now time.Time,
) error {
	result, err := store.db.ExecContext(ctx, `
		UPDATE operation_results SET reported_at = ?, attempt_count = attempt_count + 1,
			last_attempt_at = ?, last_error_code = ''
		WHERE operation_id = ? AND reported_at IS NULL
	`, now.UnixMilli(), now.UnixMilli(), operationID)
	if err != nil {
		return fmt.Errorf("mark operation result reported: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read operation result update: %w", err)
	}
	if count == 0 {
		return errors.New("pending operation result not found")
	}
	if _, err := store.db.ExecContext(ctx, `
		DELETE FROM operation_results
		WHERE reported_at IS NOT NULL AND sequence NOT IN (
			SELECT sequence FROM operation_results ORDER BY sequence DESC LIMIT 500
		)
	`); err != nil {
		return fmt.Errorf("prune reported operation results: %w", err)
	}
	return nil
}

func (store *localStore) recordOperationResultFailure(
	ctx context.Context,
	operationID, errorCode string,
	now time.Time,
) error {
	if _, err := store.db.ExecContext(ctx, `
		UPDATE operation_results SET attempt_count = attempt_count + 1,
			last_attempt_at = ?, last_error_code = ?
		WHERE operation_id = ? AND reported_at IS NULL
	`, now.UnixMilli(), errorCode, operationID); err != nil {
		return fmt.Errorf("record operation result failure: %w", err)
	}
	return nil
}

func (store *localStore) bindSUIStore(ctx context.Context, nodeID string) error {
	if nodeID == "" {
		return errors.New("cannot bind S-UI store without a node ID")
	}
	var current string
	err := store.db.QueryRowContext(ctx, `
		SELECT node_id FROM sui_runtime WHERE singleton = 1
	`).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = store.db.ExecContext(ctx, `
			INSERT INTO sui_runtime(singleton, node_id) VALUES (1, ?)
		`, nodeID)
		if err != nil {
			return fmt.Errorf("bind S-UI store: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read S-UI store binding: %w", err)
	}
	if current != nodeID {
		return errors.New("S-UI ownership database belongs to another node")
	}
	return nil
}

func (store *localStore) listSUIMappings(ctx context.Context) ([]suiClientMapping, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT user_id, remote_client_id, management_mode, remote_name,
		       credential_fingerprint
		FROM sui_client_mappings ORDER BY user_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list S-UI mappings: %w", err)
	}
	defer rows.Close()
	result := make([]suiClientMapping, 0)
	for rows.Next() {
		var mapping suiClientMapping
		if err := rows.Scan(
			&mapping.UserID, &mapping.RemoteClientID, &mapping.ManagementMode,
			&mapping.RemoteName, &mapping.CredentialFingerprint,
		); err != nil {
			return nil, fmt.Errorf("scan S-UI mapping: %w", err)
		}
		result = append(result, mapping)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate S-UI mappings: %w", err)
	}
	return result, nil
}

func (store *localStore) upsertSUIMapping(
	ctx context.Context,
	mapping suiClientMapping,
	now time.Time,
) error {
	if mapping.UserID == "" || mapping.RemoteClientID <= 0 ||
		(mapping.ManagementMode != "read_only" && mapping.ManagementMode != "managed") ||
		mapping.RemoteName == "" {
		return errors.New("invalid S-UI mapping")
	}
	_, err := store.db.ExecContext(ctx, `
		INSERT INTO sui_client_mappings(
			user_id, remote_client_id, management_mode, remote_name,
			credential_fingerprint, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			remote_client_id = excluded.remote_client_id,
			management_mode = excluded.management_mode,
			remote_name = excluded.remote_name,
			credential_fingerprint = excluded.credential_fingerprint,
			updated_at = excluded.updated_at
	`, mapping.UserID, mapping.RemoteClientID, mapping.ManagementMode,
		mapping.RemoteName, mapping.CredentialFingerprint,
		now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return fmt.Errorf("upsert S-UI mapping: %w", err)
	}
	return nil
}

func (store *localStore) deleteSUIMapping(ctx context.Context, userID string) error {
	if _, err := store.db.ExecContext(ctx, `
		DELETE FROM sui_client_mappings WHERE user_id = ?
	`, userID); err != nil {
		return fmt.Errorf("delete S-UI mapping: %w", err)
	}
	return nil
}

func (store *localStore) primeTrafficBaseline(
	ctx context.Context,
	userID string,
	counters trafficCounters,
	now time.Time,
) error {
	if userID == "" || counters.TX < 0 || counters.RX < 0 {
		return errors.New("invalid traffic baseline")
	}
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO usage_baselines(user_id, tx_bytes, rx_bytes, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			tx_bytes = excluded.tx_bytes,
			rx_bytes = excluded.rx_bytes,
			updated_at = excluded.updated_at
	`, userID, counters.TX, counters.RX, now.UnixMilli()); err != nil {
		return fmt.Errorf("prime traffic baseline: %w", err)
	}
	return nil
}

func ensureLocalColumn(ctx context.Context, database *sql.DB, table, column, statement string) error {
	rows, err := database.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return fmt.Errorf("inspect Agent database table %s: %w", table, err)
	}
	found := false
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("inspect Agent database column: %w", err)
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("inspect Agent database columns: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close Agent database column inspection: %w", err)
	}
	if found {
		return nil
	}
	if _, err := database.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("add Agent database column %s.%s: %w", table, column, err)
	}
	return nil
}

func (store *localStore) recordTrafficSample(
	ctx context.Context,
	installationID string,
	counters map[string]trafficCounters,
	sampledAt time.Time,
	counterEpoch ...string,
) ([]protocol.TrafficBatch, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin traffic sample: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var sourceEpoch string
	var sequence int64
	var initialized int
	var storedCounterEpoch string
	if err := tx.QueryRowContext(ctx, `
		SELECT source_epoch, next_sequence, initialized, counter_epoch
		FROM usage_runtime WHERE singleton = 1
	`).Scan(&sourceEpoch, &sequence, &initialized, &storedCounterEpoch); err != nil {
		return nil, fmt.Errorf("read traffic source state: %w", err)
	}
	currentCounterEpoch := ""
	if len(counterEpoch) > 0 {
		currentCounterEpoch = counterEpoch[0]
	}
	if len(counterEpoch) > 1 {
		return nil, errors.New("multiple traffic counter epochs supplied")
	}
	if currentCounterEpoch != "" {
		if _, err := uuid.Parse(currentCounterEpoch); err != nil {
			return nil, errors.New("traffic counter epoch is invalid")
		}
		if storedCounterEpoch != currentCounterEpoch && initialized == 1 {
			sourceEpoch = cryptoutil.NewID()
			sequence = 1
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM usage_baselines;
				UPDATE usage_runtime SET source_epoch = ?, next_sequence = 1,
					counter_epoch = ? WHERE singleton = 1
			`, sourceEpoch, currentCounterEpoch); err != nil {
				return nil, fmt.Errorf("rotate traffic source after counter epoch change: %w", err)
			}
		} else if storedCounterEpoch == "" {
			if _, err := tx.ExecContext(ctx, `
				UPDATE usage_runtime SET counter_epoch = ? WHERE singleton = 1
			`, currentCounterEpoch); err != nil {
				return nil, fmt.Errorf("store traffic counter epoch: %w", err)
			}
		}
	}

	userIDs := make([]string, 0, len(counters))
	for userID := range counters {
		userIDs = append(userIDs, userID)
	}
	sort.Strings(userIDs)

	resetUsers := make(map[string]struct{})
	for _, userID := range userIDs {
		current := counters[userID]
		var previousTX, previousRX int64
		err := tx.QueryRowContext(ctx, `
			SELECT tx_bytes, rx_bytes FROM usage_baselines WHERE user_id = ?
		`, userID).Scan(&previousTX, &previousRX)
		if err == nil && (current.TX < previousTX || current.RX < previousRX) {
			resetUsers[userID] = struct{}{}
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("read traffic baseline: %w", err)
		}
	}
	if initialized == 0 {
		for _, userID := range userIDs {
			current := counters[userID]
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO usage_baselines(user_id, tx_bytes, rx_bytes, updated_at)
				VALUES (?, ?, ?, ?)
				ON CONFLICT(user_id) DO UPDATE SET
					tx_bytes = excluded.tx_bytes,
					rx_bytes = excluded.rx_bytes,
					updated_at = excluded.updated_at
			`, userID, current.TX, current.RX, sampledAt.UnixMilli()); err != nil {
				return nil, fmt.Errorf("initialize traffic baseline: %w", err)
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE usage_runtime SET initialized = 1 WHERE singleton = 1
		`); err != nil {
			return nil, fmt.Errorf("mark traffic baseline initialized: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit initial traffic baseline: %w", err)
		}
		return nil, nil
	}
	if len(resetUsers) > 0 {
		sourceEpoch = cryptoutil.NewID()
		sequence = 1
		if _, err := tx.ExecContext(ctx, `
			UPDATE usage_runtime SET source_epoch = ?, next_sequence = 1 WHERE singleton = 1
		`, sourceEpoch); err != nil {
			return nil, fmt.Errorf("rotate traffic source epoch: %w", err)
		}
	}

	items := make([]protocol.TrafficDelta, 0, len(counters))
	for _, userID := range userIDs {
		current := counters[userID]
		var previousTX, previousRX int64
		err := tx.QueryRowContext(ctx, `
			SELECT tx_bytes, rx_bytes FROM usage_baselines WHERE user_id = ?
		`, userID).Scan(&previousTX, &previousRX)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("read traffic baseline after reset check: %w", err)
		}
		if errors.Is(err, sql.ErrNoRows) {
			previousTX = 0
			previousRX = 0
		}
		if _, reset := resetUsers[userID]; reset {
			previousTX = 0
			previousRX = 0
		}
		uploadDelta := current.TX - previousTX
		downloadDelta := current.RX - previousRX
		if uploadDelta < 0 || downloadDelta < 0 {
			return nil, errors.New("traffic counter decreased without rotating its source epoch")
		}
		if uploadDelta != 0 || downloadDelta != 0 {
			items = append(items, protocol.TrafficDelta{
				UserID: userID, UploadBytes: uploadDelta, DownloadBytes: downloadDelta,
			})
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO usage_baselines(user_id, tx_bytes, rx_bytes, updated_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(user_id) DO UPDATE SET
				tx_bytes = excluded.tx_bytes,
				rx_bytes = excluded.rx_bytes,
				updated_at = excluded.updated_at
		`, userID, current.TX, current.RX, sampledAt.UnixMilli()); err != nil {
			return nil, fmt.Errorf("write traffic baseline: %w", err)
		}
	}

	if len(items) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit empty traffic sample: %w", err)
		}
		return nil, nil
	}
	batches := make([]protocol.TrafficBatch, 0,
		(len(items)+protocol.MaxTrafficItemsPerBatch-1)/protocol.MaxTrafficItemsPerBatch)
	createdAt := time.Now().UTC().UnixMilli()
	for start := 0; start < len(items); start += protocol.MaxTrafficItemsPerBatch {
		end := min(start+protocol.MaxTrafficItemsPerBatch, len(items))
		batch := protocol.TrafficBatch{
			ID: cryptoutil.NewID(), InstallationID: installationID, SourceEpoch: sourceEpoch,
			Sequence: sequence + int64(len(batches)), SampledAt: sampledAt.UTC(),
			Items: append([]protocol.TrafficDelta(nil), items[start:end]...),
		}
		payload, err := json.Marshal(batch)
		if err != nil {
			return nil, fmt.Errorf("encode traffic outbox batch: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO traffic_outbox(
				id, source_epoch, sequence, sampled_at, payload_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?)
		`, batch.ID, batch.SourceEpoch, batch.Sequence, batch.SampledAt.UnixMilli(), payload,
			createdAt); err != nil {
			return nil, fmt.Errorf("insert traffic outbox batch: %w", err)
		}
		batches = append(batches, batch)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE usage_runtime SET next_sequence = ? WHERE singleton = 1
	`, sequence+int64(len(batches))); err != nil {
		return nil, fmt.Errorf("advance traffic sequence: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit traffic sample: %w", err)
	}
	return batches, nil
}

func (store *localStore) listTrafficOutbox(ctx context.Context, limit int) ([]protocol.TrafficBatch, error) {
	if limit < 1 {
		limit = 1
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT payload_json FROM traffic_outbox ORDER BY ordinal LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list traffic outbox: %w", err)
	}
	defer rows.Close()
	batches := make([]protocol.TrafficBatch, 0, limit)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("scan traffic outbox: %w", err)
		}
		var batch protocol.TrafficBatch
		if err := json.Unmarshal(payload, &batch); err != nil {
			return nil, fmt.Errorf("decode traffic outbox: %w", err)
		}
		batches = append(batches, batch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate traffic outbox: %w", err)
	}
	return batches, nil
}

func (store *localStore) deleteTrafficOutbox(ctx context.Context, id string) error {
	if _, err := store.db.ExecContext(ctx, "DELETE FROM traffic_outbox WHERE id = ?", id); err != nil {
		return fmt.Errorf("delete traffic outbox batch: %w", err)
	}
	return nil
}

func (store *localStore) recordTrafficFailure(ctx context.Context, id, code string, now time.Time) error {
	if len(code) > 64 {
		code = code[:64]
	}
	if _, err := store.db.ExecContext(ctx, `
		UPDATE traffic_outbox SET attempt_count = attempt_count + 1,
			last_attempt_at = ?, last_error_code = ? WHERE id = ?
	`, now.UnixMilli(), code, id); err != nil {
		return fmt.Errorf("record traffic outbox failure: %w", err)
	}
	return nil
}

func (store *localStore) trafficOutboxCount(ctx context.Context) (int, error) {
	var count int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM traffic_outbox").Scan(&count); err != nil {
		return 0, fmt.Errorf("count traffic outbox: %w", err)
	}
	return count, nil
}

func (store *localStore) queueKicks(ctx context.Context, kicks []protocol.DesiredKick, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin desired kick queue: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, kick := range kicks {
		if kick.UserID == "" || kick.Generation < 1 {
			return errors.New("desired kick has invalid identity or generation")
		}
		var applied int64
		err := tx.QueryRowContext(ctx, `
			SELECT applied_generation FROM kick_state WHERE user_id = ?
		`, kick.UserID).Scan(&applied)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read applied kick generation: %w", err)
		}
		if applied >= kick.Generation {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO kick_outbox(user_id, generation, created_at)
			VALUES (?, ?, ?)
			ON CONFLICT(user_id) DO UPDATE SET
				generation = CASE
					WHEN excluded.generation > kick_outbox.generation THEN excluded.generation
					ELSE kick_outbox.generation
				END,
				last_error_code = ''
		`, kick.UserID, kick.Generation, now.UnixMilli()); err != nil {
			return fmt.Errorf("queue desired kick: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit desired kick queue: %w", err)
	}
	return nil
}

func (store *localStore) listPendingKicks(ctx context.Context, limit int) ([]pendingKick, error) {
	if limit < 1 {
		limit = 1
	}
	rows, err := store.db.QueryContext(ctx, `
		SELECT user_id, generation FROM kick_outbox ORDER BY created_at, user_id LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending kicks: %w", err)
	}
	defer rows.Close()
	result := make([]pendingKick, 0, limit)
	for rows.Next() {
		var kick pendingKick
		if err := rows.Scan(&kick.UserID, &kick.Generation); err != nil {
			return nil, fmt.Errorf("scan pending kick: %w", err)
		}
		result = append(result, kick)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending kicks: %w", err)
	}
	return result, nil
}

func (store *localStore) markKicksApplied(ctx context.Context, kicks []pendingKick, now time.Time) error {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin applied kicks: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, kick := range kicks {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO kick_state(user_id, applied_generation, updated_at)
			VALUES (?, ?, ?)
			ON CONFLICT(user_id) DO UPDATE SET
				applied_generation = CASE
					WHEN excluded.applied_generation > kick_state.applied_generation
					THEN excluded.applied_generation ELSE kick_state.applied_generation END,
				updated_at = excluded.updated_at
		`, kick.UserID, kick.Generation, now.UnixMilli()); err != nil {
			return fmt.Errorf("record applied kick: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM kick_outbox WHERE user_id = ? AND generation <= ?
		`, kick.UserID, kick.Generation); err != nil {
			return fmt.Errorf("delete applied kick: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit applied kicks: %w", err)
	}
	return nil
}

func (store *localStore) recordKickFailure(ctx context.Context, kicks []pendingKick, code string, now time.Time) error {
	if len(code) > 64 {
		code = code[:64]
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin kick failure: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, kick := range kicks {
		if _, err := tx.ExecContext(ctx, `
			UPDATE kick_outbox SET attempt_count = attempt_count + 1,
				last_attempt_at = ?, last_error_code = ?
			WHERE user_id = ? AND generation = ?
		`, now.UnixMilli(), code, kick.UserID, kick.Generation); err != nil {
			return fmt.Errorf("record kick failure: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit kick failure: %w", err)
	}
	return nil
}

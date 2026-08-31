-- hyfleet:foreign-keys-off

CREATE TABLE node_operations_new (
    id TEXT PRIMARY KEY,
    node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence >= 1),
    type TEXT NOT NULL CHECK (
        type IN ('probe_core', 'restart_core', 'tail_core_log', 'backup_config', 'ping')
    ),
    status TEXT NOT NULL DEFAULT 'queued' CHECK (
        status IN ('queued', 'running', 'succeeded', 'failed', 'expired')
    ),
    retry_of TEXT REFERENCES node_operations_new(id) ON DELETE SET NULL,
    attempt INTEGER NOT NULL DEFAULT 1 CHECK (attempt >= 1),
    max_lines INTEGER NOT NULL DEFAULT 0 CHECK (max_lines BETWEEN 0 AND 200),
    target TEXT NOT NULL DEFAULT '' CHECK (length(target) <= 64),
    output TEXT NOT NULL DEFAULT '',
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    rolled_back INTEGER NOT NULL DEFAULT 0 CHECK (rolled_back IN (0, 1)),
    requested_by TEXT NOT NULL REFERENCES admins(id),
    expires_at INTEGER NOT NULL,
    started_at INTEGER,
    completed_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(node_id, sequence)
);

INSERT INTO node_operations_new(
    id, node_id, sequence, type, status, retry_of, attempt, max_lines, target,
    output, error_code, error_message, rolled_back, requested_by, expires_at,
    started_at, completed_at, created_at, updated_at
)
SELECT
    id, node_id, sequence, type, status, retry_of, attempt, max_lines, '',
    output, error_code, error_message, rolled_back, requested_by, expires_at,
    started_at, completed_at, created_at, updated_at
FROM node_operations;

DROP TABLE node_operations;
ALTER TABLE node_operations_new RENAME TO node_operations;

CREATE INDEX node_operations_node_created_idx
    ON node_operations(node_id, created_at DESC);
CREATE INDEX node_operations_pending_idx
    ON node_operations(node_id, sequence)
    WHERE status IN ('queued', 'running');
CREATE INDEX node_operations_retry_idx ON node_operations(retry_of);
CREATE INDEX node_operations_history_idx
    ON node_operations(created_at DESC, node_id, type, status);

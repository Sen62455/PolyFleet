CREATE TABLE node_assets (
    node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
    plan TEXT NOT NULL DEFAULT '',
    purchased_at INTEGER,
    expires_at INTEGER,
    renewal_cycle_months INTEGER NOT NULL DEFAULT 0
        CHECK (renewal_cycle_months BETWEEN 0 AND 120),
    auto_renew INTEGER NOT NULL DEFAULT 0 CHECK (auto_renew IN (0, 1)),
    notes TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX node_assets_expiry_idx
    ON node_assets(expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE notification_notifiers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('telegram', 'slack', 'webhook')),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    config_ciphertext BLOB NOT NULL,
    target_hint TEXT NOT NULL DEFAULT '',
    events TEXT NOT NULL DEFAULT 'created,resolved',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE notification_deliveries (
    id TEXT PRIMARY KEY,
    notifier_id TEXT NOT NULL REFERENCES notification_notifiers(id) ON DELETE CASCADE,
    alert_id TEXT NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('created', 'resolved')),
    payload_json TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'retry', 'delivered', 'failed')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at INTEGER NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    response_code INTEGER NOT NULL DEFAULT 0,
    delivered_at INTEGER,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(notifier_id, alert_id, event_type)
);

CREATE INDEX notification_deliveries_due_idx
    ON notification_deliveries(status, next_attempt_at)
    WHERE status IN ('queued', 'retry');


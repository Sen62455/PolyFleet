CREATE TABLE notification_reminder_rules (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    notifier_id TEXT NOT NULL REFERENCES notification_notifiers(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN (
        'fleet_summary', 'active_alerts', 'asset_expiry', 'traffic_usage'
    )),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    interval_minutes INTEGER NOT NULL CHECK (interval_minutes BETWEEN 15 AND 10080),
    lead_days INTEGER NOT NULL DEFAULT 30 CHECK (lead_days BETWEEN 0 AND 365),
    threshold_percent INTEGER NOT NULL DEFAULT 80 CHECK (threshold_percent BETWEEN 1 AND 100),
    node_ids TEXT NOT NULL DEFAULT '[]',
    last_run_at INTEGER,
    last_success_at INTEGER,
    last_result TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    next_run_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX notification_reminder_rules_due_idx
    ON notification_reminder_rules(enabled, next_run_at) WHERE enabled = 1;

CREATE TABLE telegram_bot_access (
    notifier_id TEXT PRIMARY KEY REFERENCES notification_notifiers(id) ON DELETE CASCADE,
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    update_offset INTEGER NOT NULL DEFAULT 0 CHECK (update_offset >= 0),
    last_poll_at INTEGER,
    last_error TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL
);


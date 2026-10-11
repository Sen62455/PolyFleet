-- The operator configures forwarding on the entry server before enabling this.
-- Existing client-side chains retain their current behavior.
ALTER TABLE exit_proxies ADD COLUMN server_side INTEGER NOT NULL DEFAULT 0
    CHECK (server_side IN (0, 1));

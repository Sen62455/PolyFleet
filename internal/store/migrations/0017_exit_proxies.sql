CREATE TABLE exit_proxies (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN ('rented_ip', 'landing_vps')),
    protocol TEXT NOT NULL CHECK (protocol IN ('socks5', 'http')),
    host TEXT NOT NULL CHECK (length(host) BETWEEN 1 AND 253),
    port INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    username TEXT NOT NULL DEFAULT '' CHECK (length(username) <= 128),
    password_ciphertext BLOB,
    tls INTEGER NOT NULL DEFAULT 0 CHECK (tls IN (0, 1)),
    skip_cert_verify INTEGER NOT NULL DEFAULT 0 CHECK (skip_cert_verify IN (0, 1)),
    udp INTEGER NOT NULL DEFAULT 1 CHECK (udp IN (0, 1)),
    notes TEXT NOT NULL DEFAULT '' CHECK (length(notes) <= 512),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE INDEX exit_proxies_kind_name_idx ON exit_proxies(kind, name COLLATE NOCASE);

ALTER TABLE nodes ADD COLUMN exit_proxy_id TEXT REFERENCES exit_proxies(id) ON DELETE SET NULL;

CREATE INDEX nodes_exit_proxy_idx ON nodes(exit_proxy_id) WHERE exit_proxy_id IS NOT NULL;

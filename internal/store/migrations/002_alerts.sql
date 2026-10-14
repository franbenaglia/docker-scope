-- Alert rules, maintenance windows and incidents (ADR-013).
-- Durations in milliseconds; timestamps Unix ms UTC.

CREATE TABLE rules (
    id              INTEGER PRIMARY KEY,
    name            TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('not_running', 'unhealthy', 'restarts', 'cpu', 'memory')),
    enabled         INTEGER NOT NULL DEFAULT 1,
    scope_container TEXT NOT NULL DEFAULT '', -- container name; '' = any
    scope_project   TEXT NOT NULL DEFAULT '', -- Compose project; '' = any
    scope_service   TEXT NOT NULL DEFAULT '', -- Compose service; '' = any
    threshold       REAL NOT NULL DEFAULT 0,  -- cpu: % of one core; memory: % of limit; restarts: count
    for_ms          INTEGER NOT NULL DEFAULT 0,
    window_ms       INTEGER NOT NULL DEFAULT 0,
    cooldown_ms     INTEGER NOT NULL DEFAULT 0,
    recover_ms      INTEGER NOT NULL DEFAULT 0,
    hysteresis      REAL NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);

-- Default rules from the SPEC examples. not_running and cpu need a deliberate
-- scope/threshold, so they ship disabled.
INSERT INTO rules (name, kind, enabled, threshold, for_ms, window_ms, cooldown_ms, recover_ms, hysteresis, created_at, updated_at) VALUES
    ('Unhealthy persistente',      'unhealthy', 1, 0,  30000,  0,      300000, 0, 0, CAST(strftime('%s','now') AS INTEGER) * 1000, CAST(strftime('%s','now') AS INTEGER) * 1000),
    ('Reinicios repetidos',        'restarts',  1, 3,  0,      300000, 300000, 0, 0, CAST(strftime('%s','now') AS INTEGER) * 1000, CAST(strftime('%s','now') AS INTEGER) * 1000),
    ('Memoria alta',               'memory',    1, 80, 60000,  0,      300000, 0, 5, CAST(strftime('%s','now') AS INTEGER) * 1000, CAST(strftime('%s','now') AS INTEGER) * 1000),
    ('CPU alta (ejemplo)',         'cpu',       0, 90, 120000, 0,      300000, 0, 10, CAST(strftime('%s','now') AS INTEGER) * 1000, CAST(strftime('%s','now') AS INTEGER) * 1000);

CREATE TABLE maintenance (
    id         INTEGER PRIMARY KEY,
    container  TEXT NOT NULL DEFAULT '', -- container name; '' = global
    until      INTEGER NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
CREATE INDEX maintenance_until ON maintenance (until);

CREATE TABLE incidents (
    id             INTEGER PRIMARY KEY,
    rule_id        INTEGER NOT NULL,
    rule_name      TEXT NOT NULL,
    kind           TEXT NOT NULL,
    container_id   TEXT NOT NULL,
    container_name TEXT NOT NULL,
    state          TEXT NOT NULL CHECK (state IN ('open', 'resolved')),
    started_at     INTEGER NOT NULL, -- condition first observed (pending start)
    opened_at      INTEGER NOT NULL,
    resolved_at    INTEGER,
    acked_at       INTEGER,          -- acknowledging does not resolve
    suppressed     INTEGER NOT NULL DEFAULT 0, -- opened during maintenance: no notification
    summary        TEXT NOT NULL,
    cause          TEXT NOT NULL DEFAULT '',   -- probable cause, never asserted as certain
    resolution     TEXT NOT NULL DEFAULT '',
    peak           REAL
);
-- Deduplication: at most one open incident per rule and container.
CREATE UNIQUE INDEX incidents_active ON incidents (rule_id, container_id) WHERE state = 'open';
CREATE INDEX incidents_opened ON incidents (opened_at);
CREATE INDEX incidents_container ON incidents (container_id);

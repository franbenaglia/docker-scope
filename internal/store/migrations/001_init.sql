-- DockerScope schema v1. Timestamps are Unix milliseconds, UTC.
-- No log contents are stored (SPEC "Logs"): only metadata and metrics.

-- Known containers, so history and incidents can show names of containers
-- that no longer exist. Identity is the full ID.
CREATE TABLE containers (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    image           TEXT NOT NULL DEFAULT '',
    compose_project TEXT NOT NULL DEFAULT '',
    compose_service TEXT NOT NULL DEFAULT '',
    first_seen      INTEGER NOT NULL,
    last_seen       INTEGER NOT NULL,
    removed_at      INTEGER
);

-- Raw samples, one per container per collector round (ADR-011: kept for
-- min(24h, retention)). NULL = not available, never zero.
CREATE TABLE samples (
    container_id       TEXT NOT NULL,
    ts                 INTEGER NOT NULL, -- daemon read time
    cpu_pct            REAL,             -- % of one core (ADR-002)
    mem_used           INTEGER,          -- bytes, inactive cache excluded when known
    mem_limit          INTEGER,          -- effective limit, bytes
    mem_limit_explicit INTEGER NOT NULL DEFAULT 0,
    mem_pct            REAL,
    net_rx_bps         REAL,
    net_tx_bps         REAL,
    blk_read_bps       REAL,
    blk_write_bps      REAL,
    pids               INTEGER,
    PRIMARY KEY (container_id, ts)
) WITHOUT ROWID;
CREATE INDEX samples_ts ON samples (ts);

-- One-minute rollups of samples, kept for the full retention.
CREATE TABLE samples_1m (
    container_id  TEXT NOT NULL,
    ts            INTEGER NOT NULL, -- minute start
    n             INTEGER NOT NULL, -- raw samples aggregated
    cpu_avg       REAL,
    cpu_max       REAL,
    mem_avg       REAL,
    mem_max       INTEGER,
    mem_limit     INTEGER,
    mem_pct_avg   REAL,
    mem_pct_max   REAL,
    net_rx_bps    REAL,
    net_tx_bps    REAL,
    blk_read_bps  REAL,
    blk_write_bps REAL,
    pids_max      INTEGER,
    PRIMARY KEY (container_id, ts)
) WITHOUT ROWID;
CREATE INDEX samples_1m_ts ON samples_1m (ts);

-- Small key/value state (e.g. rollup watermark).
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) WITHOUT ROWID;

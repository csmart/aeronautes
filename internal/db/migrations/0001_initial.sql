-- Schema v1: the DESIGN.md §51 tables.
--
-- Rules that shape every table here:
--   * no secret values, ever (K-SEC-003) — bookmarks carry secret_ref, a
--     SecretStore lookup name, never the credential itself;
--   * timestamps are RFC 3339 UTC strings;
--   * JSON payloads are TEXT columns named *_json.

CREATE TABLE bookmarks (
    id                   TEXT PRIMARY KEY,
    display_name         TEXT NOT NULL,
    protocol_profile     TEXT NOT NULL DEFAULT 'swiftstack_v1',
    auth_url             TEXT NOT NULL,
    username             TEXT NOT NULL,
    storage_url_override TEXT,
    -- Name of this bookmark's credential in the SecretStore; NULL for
    -- session-only credentials.
    secret_ref           TEXT,
    tls_mode             TEXT NOT NULL DEFAULT 'system', -- system | custom_ca | insecure
    tls_ca_id            TEXT,
    performance_profile  TEXT NOT NULL DEFAULT 'inherit',
    segment_placement    TEXT NOT NULL DEFAULT 'rclone_default', -- rclone_default | sidecar | in_container
    -- NULL means the default: 5 GiB capped at the cluster max_file_size (§25).
    chunk_size_bytes     INTEGER,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
);

CREATE TABLE capability_snapshots (
    bookmark_id TEXT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
    fetched_at  TEXT NOT NULL,
    source      TEXT NOT NULL, -- advertised | observed | safely_probed | operator_declared
    payload_json TEXT NOT NULL,
    PRIMARY KEY (bookmark_id, fetched_at)
);

CREATE TABLE segment_evidence (
    id            INTEGER PRIMARY KEY,
    bookmark_id   TEXT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
    container     TEXT NOT NULL,
    prefix        TEXT NOT NULL DEFAULT '',
    confidence    TEXT NOT NULL, -- confirmed | declared | suspected
    reason        TEXT NOT NULL, -- observed_dlo | observed_slo | app_upload | user_pin | naming_heuristic
    source_object TEXT,
    first_seen    TEXT NOT NULL,
    last_seen     TEXT NOT NULL
);
CREATE INDEX idx_segment_evidence_scope
    ON segment_evidence (bookmark_id, container, prefix);

-- ExclusiveOwnershipProof records (§28.6b): unique segment prefixes
-- Aeronautes/rclone wrote for a specific job.
CREATE TABLE segment_ownership (
    id          INTEGER PRIMARY KEY,
    bookmark_id TEXT NOT NULL REFERENCES bookmarks(id) ON DELETE CASCADE,
    job_id      TEXT NOT NULL,
    container   TEXT NOT NULL,
    prefix      TEXT NOT NULL,
    created_at  TEXT NOT NULL
);
CREATE INDEX idx_segment_ownership_scope
    ON segment_ownership (bookmark_id, container, prefix);

CREATE TABLE performance_profiles (
    bookmark_id  TEXT PRIMARY KEY REFERENCES bookmarks(id) ON DELETE CASCADE,
    payload_json TEXT NOT NULL
);

CREATE TABLE jobs (
    id                 TEXT PRIMARY KEY,
    type               TEXT NOT NULL,
    state              TEXT NOT NULL,
    source             TEXT,
    destination        TEXT,
    created_at         TEXT NOT NULL,
    started_at         TEXT,
    finished_at        TEXT,
    execution_engine   TEXT,
    effective_config_json TEXT,
    counters_json      TEXT,
    error              TEXT
);
CREATE INDEX idx_jobs_state ON jobs (state);

-- Retention/compaction for audit_events is a product feature (§51): a
-- size/age cap with export before compaction, implemented with the audit
-- writer.
CREATE TABLE audit_events (
    id                  INTEGER PRIMARY KEY,
    at                  TEXT NOT NULL,
    bookmark_id         TEXT,
    operation           TEXT NOT NULL,
    resource            TEXT NOT NULL,
    summary             TEXT NOT NULL,
    execution_engine    TEXT,
    result              TEXT NOT NULL,
    transaction_ids_json TEXT
);
CREATE INDEX idx_audit_events_at ON audit_events (at);

CREATE TABLE ui_preferences (
    name  TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

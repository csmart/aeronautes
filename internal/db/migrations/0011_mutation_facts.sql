-- Identity-keyed mutation facts (ADR 0058). Retention, audit compaction
-- and terminal-job pruning must not delete these rows; compacted
-- audit_events are not this ledger.
CREATE TABLE mutation_facts (
    identity       TEXT PRIMARY KEY COLLATE BINARY,
    bookmark_id    TEXT NOT NULL,
    operation      TEXT NOT NULL,
    resource       TEXT NOT NULL COLLATE BINARY,
    outcome        TEXT NOT NULL CHECK (outcome IN (
        'intent', 'applied', 'conflict', 'verify_mismatch', 'failed', 'unverified'
    )),
    summary        TEXT NOT NULL DEFAULT '',
    trans_ids_json TEXT,
    created_at     TEXT NOT NULL,
    decided_at     TEXT
);
CREATE INDEX idx_mutation_facts_resource
    ON mutation_facts (bookmark_id, operation, resource, created_at);

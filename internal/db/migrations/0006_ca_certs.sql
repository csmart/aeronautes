-- Per-bookmark CA bundles (§12.2/§12.3): PEM stored by id, referenced
-- from bookmarks.tls_ca_id. Certificates are public material — this is
-- trust configuration, not a secret store.
CREATE TABLE ca_certs (
    id       TEXT PRIMARY KEY,
    pem      TEXT NOT NULL,
    added_at TEXT NOT NULL
);

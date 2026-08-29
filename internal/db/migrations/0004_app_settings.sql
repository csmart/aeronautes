-- Application-level settings (§38 budgets, §40 default profile): a
-- small typed KV — every key is enumerated in code, never free-form
-- engine args.
CREATE TABLE app_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

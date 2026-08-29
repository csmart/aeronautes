-- §42 benchmark results and the per-bookmark recommendation feeding
-- the Auto profile (§40). One row per bookmark: the latest run wins.
CREATE TABLE benchmark_results (
    bookmark_id         TEXT PRIMARY KEY REFERENCES bookmarks(id),
    ran_at              TEXT NOT NULL,
    results_json        TEXT NOT NULL,
    recommendation_json TEXT NOT NULL
);

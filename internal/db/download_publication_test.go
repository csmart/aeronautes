package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

func TestDownloadPublicationUpgradePreservesHistory(t *testing.T) {
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, historical := range migrations {
		boundary := historical.version
		if boundary < 10 || boundary >= 13 {
			continue
		}
		t.Run(fmt.Sprint(boundary), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.sqlite")
			raw, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if _, err := raw.ExecContext(t.Context(), `CREATE TABLE schema_migrations (
				version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			for _, migration := range migrations {
				if migration.version > boundary {
					continue
				}
				if _, err := raw.ExecContext(t.Context(), migration.sql); err != nil {
					t.Fatal(err)
				}
				if _, err := raw.ExecContext(t.Context(), `INSERT INTO schema_migrations VALUES (?, ?, 'historical')`, migration.version, migration.name); err != nil {
					t.Fatal(err)
				}
			}
			const counters = `{"objectsCopied":1,"objectsVerified":1,"bytesCopied":10}`
			if _, err := raw.ExecContext(t.Context(), `INSERT INTO jobs
				(id, type, state, created_at, finished_at, counters_json, error, item_records_version)
				VALUES ('historical', 'download', 'complete', '2026-09-01T00:00:00Z', '2026-09-01T00:01:00Z', ?, '', 1)`, counters); err != nil {
				t.Fatal(err)
			}
			const source = `{"kind":"swift","bookmarkId":"account","container":"source","object":"object"}`
			const destination = `{"kind":"local","localPath":"/historical/object"}`
			if _, err := raw.ExecContext(t.Context(), `INSERT INTO job_items
				(id, job_id, item_key, source_json, destination_json, planned, admitted, listed_bytes, created_at)
				VALUES (1, 'historical', 'object', ?, ?, 1, 1, 10, '2026-09-01T00:00:00Z')`, source, destination); err != nil {
				t.Fatal(err)
			}
			if _, err := raw.ExecContext(t.Context(), `INSERT INTO job_item_observations VALUES
				(1, 'execution', 'copied', 'reported_applied', '', '', 0, 10, '2026-09-01T00:00:01Z'),
				(1, 'verification', 'verified', 'not_attempted', '', '', 0, 0, '2026-09-01T00:00:02Z')`); err != nil {
				t.Fatal(err)
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				d, err := Open(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}
				var required, publications, observations, unchangedObservations int
				var gotSource, gotDestination, gotCounters, state, finished string
				err = d.Reader().QueryRowContext(t.Context(), `SELECT i.publication_required, i.source_json, i.destination_json,
					j.counters_json, j.state, j.finished_at, (SELECT COUNT(*) FROM job_item_publications),
					(SELECT COUNT(*) FROM job_item_observations),
					(SELECT COUNT(*) FROM job_item_observations WHERE item_id=1 AND diagnostic_stage='' AND diagnostic_code='' AND http_status=0 AND
					 ((phase='execution' AND result='copied' AND effect='reported_applied' AND copied_bytes=10 AND created_at='2026-09-01T00:00:01Z') OR
					  (phase='verification' AND result='verified' AND effect='not_attempted' AND copied_bytes=0 AND created_at='2026-09-01T00:00:02Z')))
					FROM job_items i JOIN jobs j ON j.id=i.job_id WHERE i.id=1 AND i.staging_json IS NULL`).Scan(
					&required, &gotSource, &gotDestination, &gotCounters, &state, &finished, &publications, &observations, &unchangedObservations)
				if err != nil {
					_ = d.Close()
					t.Fatalf("historical rows need an explicit non-required publication default: %v", err)
				}
				if required != 0 || publications != 0 || observations != 2 || unchangedObservations != 2 || gotSource != source || gotDestination != destination || gotCounters != counters || state != "complete" || finished != "2026-09-01T00:01:00Z" {
					t.Errorf("upgrade altered historical facts: required=%d publications=%d observations=%d unchanged=%d counters=%q state=%q finished=%q", required, publications, observations, unchangedObservations, gotCounters, state, finished)
				}
				if err := d.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

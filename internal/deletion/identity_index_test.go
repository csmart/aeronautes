package deletion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"modernc.org/sqlite"
)

func testIdentityIndex(t *testing.T) *identityIndex {
	t.Helper()
	index, err := newIdentityIndex(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := index.close(); err != nil {
			t.Error(err)
		}
	})
	return index
}

func identityRows(t *testing.T, index *identityIndex) int {
	t.Helper()
	// Context rollback can still be running after Commit returns. The
	// writer's connection can see pending inserts, so it cannot establish
	// which identities committed.
	reader, err := sql.Open("sqlite", identityIndexURI(index.path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	}()
	var count int
	if err := reader.QueryRowContext(t.Context(), "SELECT count(*) FROM seen").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestIdentityIndexPreservesBinaryNamesAndFacets(t *testing.T) {
	index := testIdentityIndex(t)
	names := []string{"", "a", "A", "a ", " a", "a/b", "a%2Fb", "é", "e\u0301", "a\x00b", "a\xffb", strings.Repeat("n", 1024)}
	var facets []identityFacet
	var want []bool
	for _, name := range names {
		facets = append(facets, identityFacet{reportedFailureIdentity, name}, identityFacet{reportedFailureIdentity, name})
		want = append(want, true, false)
	}
	got, err := index.add(t.Context(), facets)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("first transaction: inserted=%v err=%v", got, err)
	}
	got, err = index.add(t.Context(), facets)
	if err != nil || !slices.Equal(got, make([]bool, len(facets))) {
		t.Fatalf("repeated transaction: inserted=%v err=%v", got, err)
	}
	for _, name := range names {
		got, err := index.add(t.Context(), []identityFacet{{unresolvedIdentity, name}})
		if err != nil || !slices.Equal(got, []bool{true}) {
			t.Fatalf("second facet of %q: inserted=%v err=%v", name, got, err)
		}
	}
	if got := identityRows(t, index); got != 2*len(names) {
		t.Fatalf("distinct binary facets=%d, want %d", got, 2*len(names))
	}
	var nonBinary int
	if err := index.conn.QueryRowContext(t.Context(), "SELECT count(*) FROM seen WHERE typeof(name) != 'blob'").Scan(&nonBinary); err != nil {
		t.Fatal(err)
	}
	if nonBinary != 0 {
		t.Fatal("an identity was stored with text equality")
	}
}

func TestIdentityIndexRejectsOversizedTransactions(t *testing.T) {
	index := testIdentityIndex(t)
	facets := make([]identityFacet, 2001)
	for i := range facets {
		facets[i] = identityFacet{reportedFailureIdentity, fmt.Sprintf("n%d", i)}
	}
	if got, err := index.add(t.Context(), facets); err == nil || got != nil {
		t.Fatalf("oversized transaction: inserted=%v err=%v", got, err)
	}
	if got := identityRows(t, index); got != 0 {
		t.Fatalf("oversized transaction inserted %d rows", got)
	}
}

func TestIdentityIndexEffectiveResourceBounds(t *testing.T) {
	index := testIdentityIndex(t)
	for _, setting := range []struct{ query, want string }{
		{"PRAGMA page_size", "4096"},
		{"PRAGMA cache_size", "256"},
		{"PRAGMA mmap_size", "0"},
		{"PRAGMA temp_store", "1"},
		{"PRAGMA journal_mode", "delete"},
		{"PRAGMA synchronous", "2"},
		{"PRAGMA busy_timeout", "0"},
	} {
		var got string
		if err := index.conn.QueryRowContext(t.Context(), setting.query).Scan(&got); err != nil || got != setting.want {
			t.Errorf("%s: got=%s want=%s err=%v", setting.query, got, setting.want, err)
		}
	}
	var spill int
	if err := index.conn.QueryRowContext(t.Context(), "PRAGMA cache_spill").Scan(&spill); err != nil || spill <= 0 {
		t.Fatalf("cache spill: got=%d err=%v", spill, err)
	}
	if stats := index.db.Stats(); stats.OpenConnections != 1 || stats.MaxOpenConnections != 1 {
		t.Fatalf("connection bound: %+v", stats)
	}
}

func TestIdentityIndexRollsBackAndStopsAfterFailure(t *testing.T) {
	index := testIdentityIndex(t)
	if _, err := index.add(t.Context(), []identityFacet{{reportedFailureIdentity, "previous"}}); err != nil {
		t.Fatal(err)
	}
	got, err := index.add(t.Context(), []identityFacet{{reportedFailureIdentity, "uncommitted"}, {0, "invalid facet"}})
	var storageErr *sqlite.Error
	if !errors.As(err, &storageErr) || storageErr.Code()&0xff != 19 || got != nil {
		t.Fatalf("constraint failure: inserted=%v err=%v", got, err)
	}
	if got := identityRows(t, index); got != 1 {
		t.Fatalf("failed transaction changed the committed set to %d rows", got)
	}
	if got, err := index.add(t.Context(), []identityFacet{{reportedFailureIdentity, "later"}}); err == nil || got != nil {
		t.Fatalf("index resumed after failure: inserted=%v err=%v", got, err)
	}
	if got := identityRows(t, index); got != 1 {
		t.Fatalf("resumed index changed the committed set to %d rows", got)
	}
}

func TestIdentityIndexFullDiskDoesNotPublishPartialBatch(t *testing.T) {
	for _, limited := range []bool{false, true} {
		t.Run(fmt.Sprintf("limited_%t", limited), func(t *testing.T) {
			index := testIdentityIndex(t)
			if limited {
				var pages int
				if err := index.conn.QueryRowContext(t.Context(), "PRAGMA max_page_count=2").Scan(&pages); err != nil || pages != 2 {
					t.Fatalf("set owned database page limit: pages=%d err=%v", pages, err)
				}
			}
			var facets []identityFacet
			for i := range 20 {
				facets = append(facets, identityFacet{reportedFailureIdentity, fmt.Sprintf("%04d-%s", i, strings.Repeat("x", 4096))})
			}
			got, err := index.add(t.Context(), facets)
			if !limited {
				if err != nil || len(got) != len(facets) || slices.Contains(got, false) || identityRows(t, index) != len(facets) {
					t.Fatalf("unlimited control: inserted=%v err=%v", got, err)
				}
				return
			}
			var storageErr *sqlite.Error
			if !errors.As(err, &storageErr) || storageErr.Code()&0xff != 13 || got != nil {
				t.Fatalf("SQLITE_FULL: inserted=%v err=%v", got, err)
			}
			if got := identityRows(t, index); got != 0 {
				t.Fatalf("full database retained %d uncommitted facets", got)
			}
		})
	}
}

func TestIdentityIndexCommitFailureStopsAccounting(t *testing.T) {
	index := testIdentityIndex(t)
	reader, err := sql.Open("sqlite", identityIndexURI(index.path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	}()
	tx, err := reader.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			t.Error(err)
		}
	}()
	var count int
	if err := tx.QueryRowContext(t.Context(), "SELECT count(*) FROM seen").Scan(&count); err != nil {
		t.Fatal(err)
	}
	// A reader permits the insert but prevents the rollback journal's
	// commit, exercising a failure after all insert statements succeeded.
	got, err := index.add(t.Context(), []identityFacet{{reportedFailureIdentity, "issued"}})
	var storageErr *sqlite.Error
	if !errors.As(err, &storageErr) || storageErr.Code()&0xff != 5 || got != nil || !strings.Contains(err.Error(), "commit") {
		t.Fatalf("blocked commit: inserted=%v err=%v", got, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got, err := index.add(t.Context(), []identityFacet{{reportedFailureIdentity, "issued"}}); err == nil || got != nil {
		t.Fatalf("retried uncertain identity: inserted=%v err=%v", got, err)
	}
}

func TestIdentityIndexCancelledAccountingStops(t *testing.T) {
	index := testIdentityIndex(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := index.add(ctx, []identityFacet{{reportedFailureIdentity, "issued"}})
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("cancelled accounting: inserted=%v err=%v", got, err)
	}
	if got, err := index.add(t.Context(), []identityFacet{{reportedFailureIdentity, "issued"}}); err == nil || got != nil {
		t.Fatalf("accounting resumed after cancellation: inserted=%v err=%v", got, err)
	}
}

func TestIdentityIndexCancellationAfterInserts(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled_%t", cancelled), func(t *testing.T) {
			index := testIdentityIndex(t)
			if _, err := index.add(t.Context(), []identityFacet{{reportedFailureIdentity, "previous"}}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reached := false
			index.beforeCommit = func(tx *sql.Tx) {
				var count int
				if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM seen").Scan(&count); err != nil || count != 2 {
					t.Fatalf("pending insertion: count=%d err=%v", count, err)
				}
				if count := identityRows(t, index); count != 1 {
					t.Fatalf("committed view includes pending insertion: rows=%d, want the previous row only", count)
				}
				reached = true
				if cancelled {
					cancel()
				}
			}
			got, err := index.add(ctx, []identityFacet{{reportedFailureIdentity, "pending"}})
			index.beforeCommit = nil
			if !reached {
				t.Fatal("accounting did not reach the pending insertion")
			}
			if !cancelled {
				if err != nil || !slices.Equal(got, []bool{true}) || identityRows(t, index) != 2 {
					t.Fatalf("uncancelled control: inserted=%v err=%v", got, err)
				}
				return
			}
			// database/sql may finish its context rollback before Commit
			// observes cancellation; both outcomes must withhold membership.
			if (!errors.Is(err, context.Canceled) && !errors.Is(err, sql.ErrTxDone)) || got != nil {
				t.Fatalf("cancelled after inserts: inserted=%v err=%v", got, err)
			}
			if got := identityRows(t, index); got != 1 {
				t.Fatalf("cancelled transaction retained %d rows, want the previous row only", got)
			}
			if got, err := index.add(t.Context(), []identityFacet{{reportedFailureIdentity, "later"}}); err == nil || got != nil {
				t.Fatalf("accounting resumed after interrupted commit: inserted=%v err=%v", got, err)
			}
		})
	}
}

func TestIdentityIndexEmptyOutcomeAndClosedOwner(t *testing.T) {
	index := testIdentityIndex(t)
	if got, err := index.add(t.Context(), nil); err != nil || len(got) != 0 {
		t.Fatalf("empty outcome: inserted=%v err=%v", got, err)
	}
	if got, err := index.add(t.Context(), []identityFacet{{reportedFailureIdentity, "known"}}); err != nil || !slices.Equal(got, []bool{true}) {
		t.Fatalf("outcome after empty batch: inserted=%v err=%v", got, err)
	}
	if err := index.close(); err != nil {
		t.Fatal(err)
	}
	if got, err := index.add(t.Context(), []identityFacet{{reportedFailureIdentity, "later"}}); err == nil || got != nil {
		t.Fatalf("closed owner accepted outcome: inserted=%v err=%v", got, err)
	}
}

func TestIdentityIndexMissingTableStopsAccounting(t *testing.T) {
	index := testIdentityIndex(t)
	if _, err := index.conn.ExecContext(t.Context(), "DROP TABLE seen"); err != nil {
		t.Fatal(err)
	}
	got, err := index.add(t.Context(), []identityFacet{{reportedFailureIdentity, "issued"}})
	var storageErr *sqlite.Error
	if !errors.As(err, &storageErr) || got != nil {
		t.Fatalf("missing table: inserted=%v err=%v", got, err)
	}
	if got, err := index.add(t.Context(), nil); err == nil || got != nil {
		t.Fatalf("accounting resumed after missing table: inserted=%v err=%v", got, err)
	}
}

func TestIdentityIndexLostConnectionIsNotReopened(t *testing.T) {
	index, err := newIdentityIndex(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := index.close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Error(err)
		}
	}()
	if err := index.conn.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := index.add(t.Context(), []identityFacet{{reportedFailureIdentity, "issued"}}); !errors.Is(err, sql.ErrConnDone) || got != nil {
		t.Fatalf("lost connection: inserted=%v err=%v", got, err)
	}
	if err := index.close(); !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("lost connection close was hidden: %v", err)
	}
	if _, err := os.Stat(index.path); err != nil {
		t.Fatalf("unchecked close did not preserve the private index: %v", err)
	}
	if stats := index.db.Stats(); stats.OpenConnections != 0 {
		t.Fatalf("lost connection left an open pool: %+v", stats)
	}
}

func TestIdentityIndexCommitsBoundedBatchesAcrossPasses(t *testing.T) {
	index := testIdentityIndex(t)
	const names = 5000
	for pass := range 2 {
		for start := 0; start < names; start += 1000 {
			facets := make([]identityFacet, 0, 2000)
			for n := start; n < start+1000; n++ {
				name := fmt.Sprintf("name-%08d", n)
				facets = append(facets, identityFacet{reportedFailureIdentity, name}, identityFacet{unresolvedIdentity, name})
			}
			got, err := index.add(t.Context(), facets)
			if err != nil || len(got) != len(facets) {
				t.Fatalf("pass %d start %d: inserted length=%d err=%v", pass, start, len(got), err)
			}
			if slices.Contains(got, pass != 0) {
				t.Fatalf("pass %d start %d: a facet was lost or counted twice", pass, start)
			}
		}
	}
	if got := identityRows(t, index); got != 2*names {
		t.Fatalf("distinct facets across passes=%d, want %d", got, 2*names)
	}
}

func TestIdentityIndexReportsRemovalFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("this owned permission-failure fixture needs Unix file modes without root bypass")
	}
	index, err := newIdentityIndex(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var closeErr error
	defer func() {
		if err := os.Chmod(index.dir, 0o700); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Error(err)
		}
		if err := index.close(); err != nil && closeErr == nil {
			t.Error(err)
		}
		if err := os.RemoveAll(index.dir); err != nil {
			t.Error(err)
		}
	}()
	if _, err := index.add(t.Context(), []identityFacet{{reportedFailureIdentity, "known"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(index.dir, 0o500); err != nil {
		t.Fatal(err)
	}
	closeErr = index.close()
	if closeErr == nil || !errors.Is(closeErr, os.ErrPermission) {
		t.Fatalf("removal failure was hidden: %v", closeErr)
	}
	if _, err := index.conn.ExecContext(t.Context(), "SELECT 1"); !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("failed removal left connection live: %v", err)
	}
}

func TestIdentityIndexSetupAndCleanup(t *testing.T) {
	for _, root := range []string{"", "relative-cache"} {
		t.Run("refuse nonabsolute cache "+root, func(t *testing.T) {
			if index, err := newIdentityIndex(t.Context(), root); err == nil || index != nil {
				t.Fatalf("nonabsolute cache: index=%v err=%v", index, err)
			}
		})
	}
	t.Run("private files and URI characters", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "cache # % space")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		index, err := newIdentityIndex(t.Context(), root)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := index.close(); err != nil {
				t.Error(err)
			}
		}()
		if runtime.GOOS != "windows" {
			for path, want := range map[string]os.FileMode{index.dir: 0o700, index.path: 0o600} {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm() != want {
					t.Fatalf("%s permissions=%o, want %o", path, info.Mode().Perm(), want)
				}
			}
		}
		if err := index.close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(index.dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("private directory remains: %v", err)
		}
		if _, err := index.conn.ExecContext(t.Context(), "SELECT 1"); !errors.Is(err, sql.ErrConnDone) {
			t.Fatalf("private connection remains usable: %v", err)
		}
	})
	t.Run("unavailable cache has no fallback", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "cache-file")
		if err := os.WriteFile(root, []byte("sentinel"), 0o600); err != nil {
			t.Fatal(err)
		}
		if index, err := newIdentityIndex(t.Context(), root); err == nil || index != nil {
			t.Fatalf("unavailable cache: index=%v err=%v", index, err)
		}
		if data, err := os.ReadFile(root); err != nil || string(data) != "sentinel" {
			t.Fatalf("cache sentinel changed: %q %v", data, err)
		}
	})
	t.Run("cancelled setup removes owned files", func(t *testing.T) {
		root := t.TempDir()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if index, err := newIdentityIndex(ctx, root); !errors.Is(err, context.Canceled) || index != nil {
			t.Fatalf("cancelled setup: index=%v err=%v", index, err)
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatalf("setup left owned files: entries=%v err=%v", entries, err)
		}
	})
}

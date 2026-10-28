package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
	"modernc.org/sqlite"
)

// The benchmark and segment services still run ADR 0036's private
// identity index until their own records retire it; these fixtures own its
// cache location, refuse its writes and reopen a job from the database file.

func deletionAccountingCache(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv("LocalAppData", root)
	t.Setenv("HOME", root)
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if relative, err := filepath.Rel(root, cache); err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("cache is outside the owned fixture: %s: %v", cache, err)
	}
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	return cache
}

func reopenedAccountingJob(t *testing.T, h *harness, id string) jobs.Job {
	t.Helper()
	var sequence int
	var name, path string
	if err := h.db.Reader().QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	reopened, err := db.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	row, err := jobs.NewStore(reopened).Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func refuseDeletionAccounting(t *testing.T, cache string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(cache, "aeronautes", "deletion-identities", "delete-identities-*", "identities.sqlite"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("one owned active index required: %v %v", paths, err)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(paths[0])}
	u.RawQuery = "mode=rw"
	connector, err := sqlite.NewConnector(u.String())
	if err != nil {
		t.Fatal(err)
	}
	side := sql.OpenDB(connector)
	side.SetMaxOpenConns(1)
	_, writeErr := side.ExecContext(t.Context(), `CREATE TRIGGER refuse_seen BEFORE INSERT ON seen
		BEGIN SELECT RAISE(ABORT, 'fixture deletion index refusal'); END`)
	closeErr := side.Close()
	if writeErr != nil || closeErr != nil || side.Stats().OpenConnections != 0 {
		t.Fatalf("closed side trigger: write=%v close=%v stats=%+v", writeErr, closeErr, side.Stats())
	}
}

// bulkIdentityServer answers the bulk-delete POST from a canned reply
// and forwards everything else to the mock. It drives a reply the mock
// cannot produce: one whose counts claim
// successes while a requested name gets no disposition at all.
type bulkIdentityServer struct {
	inner http.Handler
	reply map[string]any
	// forward replays the request into the service first, so the
	// deletions really happen, before the same substituted reply is
	// returned. That is the fault boundary: two runs differing only in
	// whether the effect was applied, driven by identical bytes.
	forward bool
	calls   int
	sent    []byte
	// beforeList runs once, on the server goroutine, before the first
	// prefix listing is answered. It lets a test act on facts the job
	// only publishes after it starts, without racing the job.
	beforeList func()
	listOnce   sync.Once
}

func (b *bulkIdentityServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if b.beforeList != nil && r.Method == http.MethodGet && r.URL.Query().Has("prefix") {
		b.listOnce.Do(b.beforeList)
	}
	if r.Method == http.MethodPost && r.URL.Query().Has("bulk-delete") {
		body, _ := io.ReadAll(r.Body)
		if b.forward {
			replay := httptest.NewRequest(http.MethodPost, r.URL.String(), bytes.NewReader(body))
			replay.Header = r.Header.Clone()
			b.inner.ServeHTTP(httptest.NewRecorder(), replay)
		}
		b.calls++
		payload, _ := json.Marshal(b.reply)
		b.sent = payload
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
		return
	}
	b.inner.ServeHTTP(w, r)
}

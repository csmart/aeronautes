// Package systemtest is the war room's behavioral harness (§65.10): it
// drives the REAL internal/app services (the same entry points the UI
// calls) against two containerized-Swift accounts, hands each test
// throwaway prefixed containers, and witnesses whole-account state with
// the independent oracle before and after every action.
package systemtest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/bookmarks"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/jobs"
	"github.com/csmart/aeronautes/internal/secrets"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

// Account is one Swift account under test: its bookmark in the product
// plus the independent oracle that witnesses it.
type Account struct {
	BookmarkID string
	Oracle     *oracle.Oracle
	authURL    string
	user, key  string
	storageURL string
	token      string
	rawClient  *http.Client
}

// Harness wires the service layer and both accounts for one test.
type Harness struct {
	T       *testing.T
	ctx     context.Context
	ns      string
	counter int64

	Bookmarks *app.BookmarkService
	Browse    *app.BrowseService
	Inspector *app.InspectorService
	Mutate    *app.MutateService
	Transfer  *app.TransferService
	Delete    *app.DeleteService
	Jobs      *app.JobService
	Audit     *app.AuditService

	Primary   *Account // the active browse/inspect/mutate account
	Secondary *Account // for cross-account Copy/Mirror
}

// New builds a harness or skips when SAIO is unavailable. A second
// account is MANDATORY (§65.10) — under AERONAUTES_REQUIRE_TWO_ACCOUNTS
// a missing one is a failure, never a silent skip.
func New(t *testing.T) *Harness {
	t.Helper()
	authURL := os.Getenv("AERONAUTES_SAIO_AUTH_URL")
	if authURL == "" {
		t.Skip("AERONAUTES_SAIO_AUTH_URL not set")
	}
	ctx := context.Background()
	database, err := db.Open(ctx, filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	store := bookmarks.NewStore(database, secrets.NewSessionStore())
	connections := app.NewConnections(store)
	ev := evidence.NewStore(database)
	auditStore := audit.NewStore(database)
	jobStore := jobs.NewStore(database)
	runner := app.NewJobRunner(jobStore, auditStore)
	transfer := app.NewTransferServiceWithRunner(connections, jobStore, runner)
	app.WireEvidenceStore(transfer, ev)

	h := &Harness{
		T: t, ctx: ctx,
		ns:        fmt.Sprintf("st%d", time.Now().UnixNano()),
		Bookmarks: app.NewBookmarkService(store, connections, jobStore),
		Browse:    app.NewBrowseService(connections, ev),
		Inspector: app.NewInspectorService(connections, ev),
		Mutate:    app.NewMutateService(connections, auditStore, ev),
		Transfer:  transfer,
		Delete:    app.NewDeleteService(connections, jobStore, runner, ev),
		Jobs:      app.NewJobService(jobStore),
		Audit:     app.NewAuditService(auditStore),
	}

	primary, err := h.account(authURL, envOr("AERONAUTES_SAIO_USER", "test:tester"), envOr("AERONAUTES_SAIO_KEY", "testing"), "primary")
	if err != nil {
		t.Fatalf("primary SAIO account unavailable: %v", err)
	}
	h.Primary = primary

	// A missing/unreachable secondary is a Skip locally, a Fatal in CI
	// (where AERONAUTES_REQUIRE_TWO_ACCOUNTS is set) — never an
	// unconditional Fatal that would sink the whole package on a
	// one-account laptop.
	secondary, secErr := h.account(authURL, envOr("AERONAUTES_SAIO_USER2", "test2:tester2"), envOr("AERONAUTES_SAIO_KEY2", "testing2"), "secondary")
	h.Secondary = secondary
	reach := secErr
	if reach == nil {
		reach = h.secondaryReachable()
	}
	if reach != nil {
		if os.Getenv("AERONAUTES_REQUIRE_TWO_ACCOUNTS") != "" {
			t.Fatalf("second SAIO account unavailable but AERONAUTES_REQUIRE_TWO_ACCOUNTS is set: %v", reach)
		}
		t.Skipf("second SAIO account unavailable (%v); cross-account matrix needs it", reach)
	}

	// Primary is the active connection for browse/inspect/mutate.
	if err := connections.Connect(ctx, h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	return h
}

// account provisions a test account. It returns an error rather than
// Fatal-ing so the caller can decide: the primary must exist (Fatal),
// but a missing secondary is a Skip on a one-account laptop unless
// AERONAUTES_REQUIRE_TWO_ACCOUNTS is set (grok war-room P2 — a 401 on
// the secondary auth used to fatal the whole package).
func (h *Harness) account(authURL, user, key, name string) (*Account, error) {
	h.T.Helper()
	saved, err := h.Bookmarks.Save(h.ctx, app.BookmarkDTO{
		DisplayName: name, AuthURL: authURL, Username: user,
	}, key)
	if err != nil {
		return nil, err
	}
	ora, err := oracle.Connect(authURL, user, key)
	if err != nil {
		return nil, err
	}
	a := &Account{BookmarkID: saved.ID, Oracle: ora, authURL: authURL, user: user, key: key, rawClient: &http.Client{Timeout: 30 * time.Second}}
	if err := h.rawAuth(a); err != nil {
		return nil, err
	}
	return a, nil
}

// rawAuth gives the account a raw writer for test setup/teardown that
// never touches the product.
func (h *Harness) rawAuth(a *Account) error {
	req, _ := http.NewRequest(http.MethodGet, a.authURL, nil)
	req.Header.Set("X-Auth-User", a.user)
	req.Header.Set("X-Auth-Key", a.key)
	resp, err := a.rawClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("raw auth %s as %s: HTTP %d", a.authURL, a.user, resp.StatusCode)
	}
	a.storageURL = resp.Header.Get("X-Storage-Url")
	a.token = resp.Header.Get("X-Auth-Token")
	return nil
}

func (h *Harness) secondaryReachable() error {
	if h.Secondary == nil || h.Secondary.storageURL == "" || h.Secondary.storageURL == h.Primary.storageURL {
		return fmt.Errorf("secondary resolves to the same account as primary")
	}
	return nil
}

// Container returns a unique throwaway container name on the given
// account, created and registered for teardown. Names are prefixed with
// the harness namespace so parallel/residual runs can't collide.
func (h *Harness) Container(a *Account, label string) string {
	h.T.Helper()
	name := fmt.Sprintf("%s-%s-%d", h.ns, label, atomic.AddInt64(&h.counter, 1))
	h.rawPut(a, "/"+name, nil, nil)
	h.T.Cleanup(func() { h.purge(a, name) })
	return name
}

func (h *Harness) rawPut(a *Account, path string, body []byte, headers map[string]string) {
	h.rawPutQuery(a, path, "", body, headers)
}

func (h *Harness) rawPutQuery(a *Account, path, query string, body []byte, headers map[string]string) {
	h.T.Helper()
	url := a.storageURL + path
	if query != "" {
		url += "?" + query
	}
	req, _ := http.NewRequest(http.MethodPut, url, strings.NewReader(string(body)))
	req.Header.Set("X-Auth-Token", a.token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.rawClient.Do(req)
	if err != nil {
		h.T.Fatal(err)
	}
	// A non-2xx seed (typo'd path, 401, quota) leaves an empty container;
	// the matrix would then assert "copy completed" against missing
	// source objects. Fail closed and loud (grok war-room P1 SEED-404).
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		h.T.Fatalf("seed PUT %s: HTTP %d %s", path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	resp.Body.Close()
}

// Seed writes a raw object directly (test setup, bypassing the product).
// The object name is percent-escaped with the oracle's own encoder so a
// hostile name seeds the object the oracle will later look for (codex
// war-room H-07).
func (h *Harness) Seed(a *Account, container, name string, body []byte, headers map[string]string) {
	h.rawPut(a, "/"+container+"/"+oracle.Escape(name), body, headers)
}

// SeedSLO writes a static-large-object manifest directly (test setup).
// The referenced segments must already exist.
func (h *Harness) SeedSLO(a *Account, container, name, manifestJSON string) {
	h.rawPutQuery(a, "/"+container+"/"+oracle.Escape(name), "multipart-manifest=put", []byte(manifestJSON), nil)
}

func (h *Harness) purge(a *Account, container string) {
	snap, err := a.Oracle.SnapshotContainers(container)
	if err != nil {
		return
	}
	for _, cs := range snap.Containers {
		for name := range cs.Objects {
			req, _ := http.NewRequest(http.MethodDelete, a.storageURL+"/"+container+"/"+oracle.Escape(name), nil)
			req.Header.Set("X-Auth-Token", a.token)
			if resp, err := a.rawClient.Do(req); err == nil {
				resp.Body.Close()
			}
		}
	}
	req, _ := http.NewRequest(http.MethodDelete, a.storageURL+"/"+container, nil)
	req.Header.Set("X-Auth-Token", a.token)
	if resp, err := a.rawClient.Do(req); err == nil {
		resp.Body.Close()
	}
}

// Snapshot witnesses the named containers on an account.
func (h *Harness) Snapshot(a *Account, containers ...string) *oracle.AccountSnapshot {
	h.T.Helper()
	snap, err := a.Oracle.SnapshotContainers(containers...)
	if err != nil {
		h.T.Fatal(err)
	}
	return snap
}

// SnapshotNamespace witnesses EVERY container this harness created on the
// account (all share the ns prefix), including any the engine created as
// a side effect — a copy/mirror segment sidecar, a versions archive.
// Naming only the primary container would leave such a sidecar invisible
// to AssertNoCollateral (grok war-room P1: COPY-SIDECAR). Scoped to the
// harness namespace so a parallel test's containers never look like
// collateral here.
func (h *Harness) SnapshotNamespace(a *Account) *oracle.AccountSnapshot {
	h.T.Helper()
	names, err := a.Oracle.ListContainers(h.ns)
	if err != nil {
		h.T.Fatal(err)
	}
	return h.Snapshot(a, names...)
}

// AssertNoCollateral fails the test if anything changed on the account
// beyond the object/container names listed in allowed ("container/name"
// or "container"). This is the negative-space invariant: nothing
// outside the approved set moved.
func (h *Harness) AssertNoCollateral(before, after *oracle.AccountSnapshot, allowed ...string) {
	h.T.Helper()
	if stray := collateral(before, after, allowed); len(stray) > 0 {
		h.T.Fatalf("collateral change outside the approved set %v:\n  %s", allowed, strings.Join(stray, "\n  "))
	}
}

// collateral returns every change from before to after that is NOT in
// the approved set — the pure core of AssertNoCollateral, unit-testable
// without SAIO. An allow entry is an object ref "container/name", a
// change string "container/name: <what>", a bare container name (for
// container add/remove/config only), or a "container: <what>" config
// change. A bare container name does NOT blanket-allow object changes
// inside it.
func collateral(before, after *oracle.AccountSnapshot, allowed []string) []string {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	d := oracle.Diff(before, after)
	var stray []string
	for _, x := range append(append([]string{}, d.AddedObjects...), d.RemovedObjects...) {
		if !ok[x] {
			stray = append(stray, x)
		}
	}
	for _, x := range d.ChangedObjects {
		// Diff formats a change as "container/name: <what>"; split on that
		// exact separator so a colon inside an object name can't misparse.
		ref, _, _ := strings.Cut(x, ": ")
		if !ok[ref] {
			stray = append(stray, x)
		}
	}
	for _, x := range append(d.AddedContainers, d.RemovedContainers...) {
		if !ok[x] {
			stray = append(stray, "container "+x)
		}
	}
	for _, x := range d.ChangedContainers {
		name, _, _ := strings.Cut(x, ": ")
		if !ok[x] && !ok[name] {
			stray = append(stray, "container-config "+x)
		}
	}
	return stray
}

func (h *Harness) AwaitJob(jobID string) *app.JobDTO {
	h.T.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		job, err := h.Jobs.Get(h.ctx, jobID)
		if err != nil {
			h.T.Fatal(err)
		}
		switch job.State {
		case "complete", "partial", "failed", "cancelled":
			return job
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.T.Fatalf("job %s never reached a terminal state", jobID)
	return nil
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// diffOf is Diff over the oracle snapshots (a thin re-export for tests).
func diffOf(before, after *oracle.AccountSnapshot) oracle.Delta {
	return oracle.Diff(before, after)
}

// Package systemtest is the war room's behavioral harness (§65.10): it
// drives the REAL internal/app services (the same entry points the UI
// calls) against two containerized-Swift accounts, hands each test
// throwaway prefixed containers, and witnesses whole-account state with
// the independent oracle before and after every action.
package systemtest

import (
	"context"
	"fmt"
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

	h.Primary = h.account(authURL, envOr("AERONAUTES_SAIO_USER", "test:tester"), envOr("AERONAUTES_SAIO_KEY", "testing"), "primary")
	h.Secondary = h.account(authURL, envOr("AERONAUTES_SAIO_USER2", "test2:tester2"), envOr("AERONAUTES_SAIO_KEY2", "testing2"), "secondary")
	if err := h.secondaryReachable(); err != nil {
		if os.Getenv("AERONAUTES_REQUIRE_TWO_ACCOUNTS") != "" {
			t.Fatalf("second SAIO account unavailable but AERONAUTES_REQUIRE_TWO_ACCOUNTS is set: %v", err)
		}
		t.Skipf("second SAIO account unavailable (%v); cross-account matrix needs it", err)
	}

	// Primary is the active connection for browse/inspect/mutate.
	if err := connections.Connect(ctx, h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *Harness) account(authURL, user, key, name string) *Account {
	h.T.Helper()
	saved, err := h.Bookmarks.Save(h.ctx, app.BookmarkDTO{
		DisplayName: name, AuthURL: authURL, Username: user,
	}, key)
	if err != nil {
		h.T.Fatal(err)
	}
	ora, err := oracle.Connect(authURL, user, key)
	if err != nil {
		h.T.Fatal(err)
	}
	a := &Account{BookmarkID: saved.ID, Oracle: ora, authURL: authURL, user: user, key: key, rawClient: &http.Client{Timeout: 30 * time.Second}}
	h.rawAuth(a)
	return a
}

// rawAuth gives the account a raw writer for test setup/teardown that
// never touches the product.
func (h *Harness) rawAuth(a *Account) {
	req, _ := http.NewRequest(http.MethodGet, a.authURL, nil)
	req.Header.Set("X-Auth-User", a.user)
	req.Header.Set("X-Auth-Key", a.key)
	resp, err := a.rawClient.Do(req)
	if err != nil {
		h.T.Fatal(err)
	}
	defer resp.Body.Close()
	a.storageURL = resp.Header.Get("X-Storage-Url")
	a.token = resp.Header.Get("X-Auth-Token")
}

func (h *Harness) secondaryReachable() error {
	if h.Secondary.storageURL == "" || h.Secondary.storageURL == h.Primary.storageURL {
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
	h.T.Helper()
	req, _ := http.NewRequest(http.MethodPut, a.storageURL+path, strings.NewReader(string(body)))
	req.Header.Set("X-Auth-Token", a.token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.rawClient.Do(req)
	if err != nil {
		h.T.Fatal(err)
	}
	resp.Body.Close()
}

// Seed writes a raw object directly (test setup, bypassing the product).
func (h *Harness) Seed(a *Account, container, name string, body []byte, headers map[string]string) {
	h.rawPut(a, "/"+container+"/"+name, body, headers)
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

// AssertNoCollateral fails the test if anything changed on the account
// beyond the object/container names listed in allowed ("container/name"
// or "container"). This is the negative-space invariant: nothing
// outside the approved set moved.
func (h *Harness) AssertNoCollateral(before, after *oracle.AccountSnapshot, allowed ...string) {
	h.T.Helper()
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	d := oracle.Diff(before, after)
	var stray []string
	for _, x := range append(append([]string{}, d.AddedObjects...), d.RemovedObjects...) {
		if !ok[x] && !ok[containerOf(x)] {
			stray = append(stray, x)
		}
	}
	for _, x := range d.ChangedObjects {
		name := strings.SplitN(x, ":", 2)[0]
		if !ok[name] && !ok[containerOf(name)] {
			stray = append(stray, x)
		}
	}
	for _, x := range append(d.AddedContainers, d.RemovedContainers...) {
		if !ok[x] {
			stray = append(stray, "container "+x)
		}
	}
	if len(stray) > 0 {
		h.T.Fatalf("collateral change outside the approved set %v:\n  %s", allowed, strings.Join(stray, "\n  "))
	}
}

func containerOf(objRef string) string {
	c, _, _ := strings.Cut(objRef, "/")
	return c
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

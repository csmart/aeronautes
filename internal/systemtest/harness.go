// Package systemtest is the war room's behavioral harness (§65.10): it
// drives the REAL internal/app services (the same entry points the UI
// calls) against two containerized-Swift accounts, hands each test
// throwaway prefixed containers, and witnesses their state with the
// independent oracle before and after the action under test. The witness
// is scoped to the harness namespace and to the identity the oracle
// models — see that package for what is and is not covered.
package systemtest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	// The scenario-evidence ledger. mu guards it because the war room is
	// designed to drive one harness from concurrent goroutines (codex
	// war-room F-03).
	mu           sync.Mutex
	invoked      map[string]bool  // bridge actions invoked through a wrapper
	pending      []recordedCredit // validated scenario credits, committed iff the test passes
	finalFailed  bool             // captured by the last cleanup, read by RunScenario
	finalSkipped bool
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

	// Registered FIRST so LIFO runs it LAST — after every other cleanup
	// (purges, residue sweep, db close) — capturing the test's TRULY final
	// disposition. RunScenario reads this to decide whether to commit
	// credits, instead of a finalizer that would run before an
	// earlier-registered cleanup (codex war-room F-02).
	t.Cleanup(func() {
		h.finalFailed = t.Failed()
		h.finalSkipped = t.Skipped()
	})
	// Checked db close: a close/durability error fails the test (and so
	// blocks credit), rather than being silently ignored (codex F-02).
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("harness db close: %v", err)
		}
	})

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

	// The residue sweep runs after every Container purge (which the body
	// registers later) and before the disposition capture registered above
	// — so a leak fails the test and blocks credit (codex R-04).
	t.Cleanup(h.assertNoResidue)
	return h
}

// RunScenario runs a recording test body inside a subtest and commits its
// scenario credits only if that subtest PASSED — judged AFTER its entire
// lifecycle, including every cleanup, has completed (codex war-room F-02).
// A test that only witnesses the oracle (records nothing) can call New
// directly; a test that records scenarios MUST use RunScenario so its
// disposition is observed from the outside.
func RunScenario(t *testing.T, body func(t *testing.T, h *Harness)) {
	runScenario(t, New, body)
}

// runScenario is the core with an injectable harness factory, so the
// disposition rule (commit only after a subtest that neither failed nor
// skipped, judged post-cleanup) is testable without SAIO.
func runScenario(t *testing.T, newHarness func(*testing.T) *Harness, body func(t *testing.T, h *Harness)) {
	t.Helper()
	var h *Harness
	t.Run("scenario", func(st *testing.T) {
		h = newHarness(st)
		if h != nil {
			body(st, h)
		}
	})
	// After the subtest and ALL its cleanups have run, commit iff it truly
	// passed. The disposition was captured by the harness's first-registered
	// (last-running) cleanup, so a late skip or a cleanup failure is seen.
	if h == nil || h.finalFailed || h.finalSkipped {
		return
	}
	h.mu.Lock()
	pending := append([]recordedCredit(nil), h.pending...)
	h.mu.Unlock()
	for _, c := range pending {
		creditScenario(c.action, c.scenario, t.Name())
	}
}

// assertNoResidue fails the test if it left any ns-prefixed container
// behind on either account — a safety harness must leave Swift exactly as
// it found it, and silently accepted residue trains the suite to tolerate
// unexplained state (codex R-04). It reports, and does not blindly delete.
// It retries a few times because Swift's account container listing is
// eventually consistent: a just-deleted container can linger for a beat,
// but a REAL leak persists across the retries.
func (h *Harness) assertNoResidue() {
	h.T.Helper()
	for _, a := range []*Account{h.Primary, h.Secondary} {
		if a == nil {
			continue
		}
		var names []string
		var err error
		for attempt := 0; attempt < 5; attempt++ {
			names, err = a.Oracle.ListContainers(h.ns)
			if err != nil || len(names) == 0 {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if err != nil {
			h.T.Errorf("residue sweep: listing %q-prefixed containers on %s failed: %v", h.ns, a.user, err)
			continue
		}
		if len(names) > 0 {
			h.T.Errorf("test left %d container(s) behind on %s (namespace %q): %v", len(names), a.user, h.ns, names)
		}
	}
}

// markInvoked records (concurrency-safely, codex F-03) that this test
// invoked a bridge action through a harness wrapper — the OBSERVED fact a
// credit requires, so a no-op test that never called the action earns
// nothing.
func (h *Harness) markInvoked(action string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.invoked == nil {
		h.invoked = map[string]bool{}
	}
	h.invoked[action] = true
}

// claim is the private core: it validates the pair against the inventory
// and requires the action to have been observed invoked, then queues the
// credit (committed later by RunScenario iff the test passes). It never
// decides the scenario from a bare label — the public Credit* helpers do
// that from OBSERVED OUTCOME evidence, so one call cannot certify a
// scenario it did not produce (codex war-room F-01).
func (h *Harness) claim(action, scenario string) {
	h.T.Helper()
	a, ok := Inventory[action]
	if !ok {
		h.T.Fatalf("claim: %q is not an inventoried action", action)
	}
	if !scenarioRequired(a, scenario) {
		h.T.Fatalf("claim: %q is not a required scenario for %q (needs %v)", scenario, action, a.required())
	}
	h.mu.Lock()
	invoked := h.invoked[action]
	if invoked {
		h.pending = append(h.pending, recordedCredit{action, scenario})
	}
	h.mu.Unlock()
	if !invoked {
		h.T.Fatalf("claim %q/%q: the harness never observed this action invoked — call it through a harness wrapper", action, scenario)
	}
}

// CreditHappyJob credits "happy" for a job-running action ONLY when the
// job reached the terminal COMPLETE state — a refused or failed start
// cannot be labelled happy (codex F-01).
func (h *Harness) CreditHappyJob(action, jobID string) {
	h.T.Helper()
	if job := h.AwaitJob(jobID); job.State != "complete" {
		h.T.Fatalf("CreditHappyJob %s: job %s is %q, not complete", action, jobID, job.State)
	}
	h.claim(action, "happy")
}

// CreditApplied credits "happy" for a synchronous mutate ONLY when its
// result outcome is "applied".
func (h *Harness) CreditApplied(action, outcome string) {
	h.T.Helper()
	if outcome != "applied" {
		h.T.Fatalf("CreditApplied %s: outcome %q, not applied", action, outcome)
	}
	h.claim(action, "happy")
}

// CreditRefusal credits "refusal" ONLY when the call actually returned an
// error — a successful call cannot be recorded as a refusal (codex F-01).
func (h *Harness) CreditRefusal(action string, err error) {
	h.T.Helper()
	if err == nil {
		h.T.Fatalf("CreditRefusal %s: the call returned no error, so it was not refused", action)
	}
	h.claim(action, "refusal")
}

// CreditConflict credits "conflict" ONLY when the outcome is "conflict".
func (h *Harness) CreditConflict(action, outcome string) {
	h.T.Helper()
	if outcome != "conflict" {
		h.T.Fatalf("CreditConflict %s: outcome %q, not conflict", action, outcome)
	}
	h.claim(action, "conflict")
}

// Bridge-action wrappers: tests call THESE (not the raw services) so the
// harness observes the invocation for scenario crediting (codex R-01).

func (h *Harness) StartCopy(ctx context.Context, req app.CopyRequestDTO) (string, error) {
	h.markInvoked("TransferService.StartCopy")
	return h.Transfer.StartCopy(ctx, req)
}

func (h *Harness) StartMirror(ctx context.Context, req app.MirrorRequestDTO) (string, error) {
	h.markInvoked("TransferService.StartMirror")
	return h.Transfer.StartMirror(ctx, req)
}

func (h *Harness) StartDelete(ctx context.Context, req app.DeleteRequestDTO) (string, error) {
	h.markInvoked("DeleteService.StartDelete")
	return h.Delete.StartDelete(ctx, req)
}

func (h *Harness) DeleteManifestWithSegments(ctx context.Context, req app.SegmentsDeleteRequestDTO) (*app.SegmentsDeleteResultDTO, error) {
	h.markInvoked("DeleteService.DeleteManifestWithSegments")
	return h.Delete.DeleteManifestWithSegments(ctx, req)
}

func (h *Harness) UpdateObjectMetadata(ctx context.Context, container, name, baseFingerprint string, edit app.ObjectMetadataEditDTO) (app.MutationResultDTO, error) {
	h.markInvoked("MutateService.UpdateObjectMetadata")
	return h.Mutate.UpdateObjectMetadata(ctx, container, name, baseFingerprint, edit)
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
	a := &Account{BookmarkID: saved.ID, Oracle: ora, authURL: authURL, user: user, key: key, rawClient: &http.Client{
		Timeout: 30 * time.Second,
		// The raw seed/teardown client refuses redirects too, so a
		// misrouted setup write cannot silently land elsewhere or resend
		// the token (codex war-room H-06/H-07 — match the oracle).
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
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

// purge deletes a container's objects and then the container itself,
// enumerating by RAW listing (never a logical GET, so a broken manifest
// whose segments were purged first cannot block cleanup) and checking
// every DELETE status. It does not fail the test directly — the
// end-of-test residue sweep (assertNoResidue) is the single assertion that
// nothing survived (codex R-04).
func (h *Harness) purge(a *Account, container string) {
	if names, err := a.Oracle.ListObjects(container); err == nil {
		for _, name := range names {
			h.rawDeleteChecked(a, "/"+container+"/"+oracle.Escape(name))
		}
	}
	h.rawDeleteChecked(a, "/"+container)
}

// rawDeleteChecked issues a DELETE and treats 2xx and 404 as success. A
// 409 (container not yet empty under eventual consistency) is retried a
// bounded number of times; other outcomes are left for the residue sweep
// to surface rather than silently swallowed.
func (h *Harness) rawDeleteChecked(a *Account, path string) {
	for attempt := 0; attempt < 5; attempt++ {
		req, _ := http.NewRequest(http.MethodDelete, a.storageURL+path, nil)
		req.Header.Set("X-Auth-Token", a.token)
		resp, err := a.rawClient.Do(req)
		if err != nil {
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusNotFound {
			return
		}
		if resp.StatusCode == http.StatusConflict {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return
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

// SnapshotNamespace witnesses every container whose name shares this
// harness's ns prefix — the ones it created AND ns-prefixed side effects
// the engine derives from them (e.g. a "{dest}_segments" sidecar, since
// dest is itself ns-prefixed). Naming only the primary container would
// leave such a sidecar invisible to AssertNoCollateral (grok war-room
// P1: COPY-SIDECAR). It is NOT a whole-account witness: a container the
// engine creates OUTSIDE this namespace is not seen (codex war-room
// F-06). Prefix-scoping also keeps a parallel test's containers from
// looking like collateral here.
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
// without SAIO. An allow entry is an object ref "container/name" or a
// bare container name (for container add/remove/config only). Matching is
// on the delta's TYPED identity: the "container/name" key is CONSTRUCTED
// from the typed fields, never parsed back out of a formatted string, so
// an object whose name contains ": " cannot masquerade as an allowed one
// (codex war-room F-01). A bare container name does NOT blanket-allow
// object changes inside it.
func collateral(before, after *oracle.AccountSnapshot, allowed []string) []string {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	d := oracle.Diff(before, after)
	var stray []string
	for _, x := range append(append([]oracle.ObjectChange{}, d.AddedObjects...), d.RemovedObjects...) {
		if !ok[x.Ref()] {
			stray = append(stray, "object "+x.Ref())
		}
	}
	for _, x := range d.ChangedObjects {
		if !ok[x.Ref()] {
			stray = append(stray, "object "+x.Ref())
		}
	}
	for _, x := range append(d.AddedContainers, d.RemovedContainers...) {
		if !ok[x] {
			stray = append(stray, "container "+x)
		}
	}
	for _, x := range d.ChangedContainers {
		// Bare container name allows any config change to THAT exact
		// container — matched on the typed Container field, so a distinct
		// container named "c: other" is not covered by an allow for "c".
		if !ok[x.Container] {
			stray = append(stray, "container-config "+x.Container)
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

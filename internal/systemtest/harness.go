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
	"crypto/rand"
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
	"github.com/csmart/aeronautes/internal/scenariocontract"
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
	T         *testing.T
	ctx       context.Context
	ns        string
	counter   int64
	database  *db.DB
	jobStore  *jobs.Store
	statePath string

	Bookmarks *app.BookmarkService
	Browse    *app.BrowseService
	Inspector *app.InspectorService
	Mutate    *app.MutateService
	Transfer  *app.TransferService
	Delete    *app.DeleteService
	Jobs      *app.JobService
	Audit     *app.AuditService
	Find      *app.FindService
	Settings  *app.SettingsService

	Primary   *Account // the active browse/inspect/mutate account
	Secondary *Account // for cross-account Copy/Mirror
}

type harnessConfig struct {
	authURL            string
	user, key          string
	user2, key2        string
	requireTwoAccounts bool
}

// New builds a harness or skips when SAIO is unavailable. A second
// account is MANDATORY (§65.10) — under AERONAUTES_REQUIRE_TWO_ACCOUNTS
// a missing one is a failure, never a silent skip.
func New(t *testing.T) *Harness {
	t.Helper()
	return newHarness(t, harnessConfig{
		authURL:            os.Getenv("AERONAUTES_SAIO_AUTH_URL"),
		user:               envOr("AERONAUTES_SAIO_USER", "test:tester"),
		key:                envOr("AERONAUTES_SAIO_KEY", "testing"),
		user2:              envOr("AERONAUTES_SAIO_USER2", "test2:tester2"),
		key2:               envOr("AERONAUTES_SAIO_KEY2", "testing2"),
		requireTwoAccounts: os.Getenv("AERONAUTES_REQUIRE_TWO_ACCOUNTS") != "",
	})
}

func newHarness(t *testing.T, cfg harnessConfig) *Harness {
	t.Helper()
	if cfg.authURL == "" {
		t.Skip("AERONAUTES_SAIO_AUTH_URL not set")
	}
	ctx := context.Background()
	statePath := filepath.Join(t.TempDir(), "state.sqlite")
	database, err := db.Open(ctx, statePath)
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
		database: database, jobStore: jobStore, statePath: statePath,
		// Parallel fixtures cannot rely on clock resolution for namespace isolation.
		ns:        "st" + rand.Text(),
		Bookmarks: app.NewBookmarkService(store, connections, jobStore),
		Browse:    app.NewBrowseService(connections, ev),
		Inspector: app.NewInspectorService(connections, ev),
		Mutate:    app.NewMutateService(connections, auditStore, ev),
		Transfer:  transfer,
		Delete:    app.NewDeleteService(connections, jobStore, runner, ev),
		Jobs:      app.NewJobService(jobStore),
		Audit:     app.NewAuditService(auditStore),
		Find:      app.NewFindService(connections),
		Settings:  app.NewSettingsService(database, runner),
	}

	// Checked db close: a close/durability error fails the test (so its
	// coverage claims are not credited by the reconciler) rather than being
	// silently ignored.
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("harness db close: %v", err)
		}
	})

	primary, err := h.account(cfg.authURL, cfg.user, cfg.key, "primary")
	if err != nil {
		t.Fatalf("primary SAIO account unavailable: %v", err)
	}
	h.Primary = primary

	// A missing/unreachable secondary is a Skip locally, a Fatal in CI
	// (where AERONAUTES_REQUIRE_TWO_ACCOUNTS is set) — never an
	// unconditional Fatal that would sink the whole package on a
	// one-account laptop.
	secondary, secErr := h.account(cfg.authURL, cfg.user2, cfg.key2, "secondary")
	h.Secondary = secondary
	reach := secErr
	if reach == nil {
		reach = h.secondaryReachable()
	}
	if reach != nil {
		if cfg.requireTwoAccounts {
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
	// — so a leak fails the test and blocks credit.
	t.Cleanup(h.assertNoResidue)
	t.Cleanup(h.CloseFind)
	return h
}

// assertNoResidue fails the test if it left any ns-prefixed container
// behind on either account — a safety harness must leave Swift exactly as
// it found it, and silently accepted residue trains the suite to tolerate
// unexplained state. It reports, and does not blindly delete.
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

// Receipt is the single-use record of ONE bridge-action invocation through
// a harness wrapper. Its Credit* methods emit a claim only after checking
// the scenario against the action, job, error, and outcome captured from
// THAT one call, so for a cooperating author using this API the evidence
// for call B does not get attached to call A, and a single invocation does
// not credit a scenario it did not produce. This is convenience machinery
// that catches accidental mix-ups, not an anti-forgery boundary: a test can
// still bypass the API. Emitting only writes a line; whether it becomes a
// credit is decided OUT OF PROCESS by cmd/scenariocov, which requires the
// emitting top-level test to terminally PASS (claims come only from top-level
// tests).
//
// Credit* are all defined on this one type, so calling the wrong one for an
// action compiles and is caught at run time by the checks below, not by the
// compiler.
type Receipt struct {
	h          *Harness
	action     string
	jobID      string // set for job-running actions
	outcome    string // set for synchronous mutates (result.Outcome)
	err        error  // the call's returned error
	scanID     string
	findStatus *app.FindStatusDTO

	// consumed guards single use. It is atomic so a test that races two
	// Credit* calls on one receipt gets a deterministic single winner
	// instead of a data race (see receipt_race_test.go).
	consumed atomic.Bool
}

// JobID, Err, and Outcome expose this call's result so the test can make
// its own assertions before crediting.
func (r *Receipt) JobID() string   { return r.jobID }
func (r *Receipt) Err() error      { return r.err }
func (r *Receipt) Outcome() string { return r.outcome }

// consume marks the receipt used, returning false if it was already used.
// The compare-and-swap makes concurrent consumption safe: exactly one
// caller observes true.
func (r *Receipt) consume() bool { return r.consumed.CompareAndSwap(false, true) }

func (r *Receipt) emit(scenario, evidence string) {
	r.h.T.Helper()
	if !r.consume() {
		r.h.T.Fatalf("receipt for %s already consumed — one invocation credits at most one scenario", r.action)
	}
	if !scenariocontract.Requires(r.action, scenario) {
		r.h.T.Fatalf("receipt %s: %q is not a required scenario", r.action, scenario)
	}
	// Claims must come from a top-level test: the go test -json stream does
	// not label parentage, so a subtest name cannot be told from a sibling and
	// the reconciler rejects any slash-bearing claimant. Credit after the
	// subtests finish, from the top-level test, until a real parent protocol
	// exists.
	if strings.Contains(r.h.T.Name(), "/") {
		r.h.T.Fatalf("receipt %s: credit from a top-level test, not subtest %q", r.action, r.h.T.Name())
	}
	r.h.T.Logf("%s%s", scenariocontract.ClaimMarker, claimJSON(r.action, scenario, evidence))
}

// CreditHappy emits a happy claim for a JOB action, validating from THIS
// receipt that the call succeeded and its job reached the terminal
// complete state — so a refused start, or a DIFFERENT action's job, cannot
// certify happy.
func (r *Receipt) CreditHappy() {
	r.h.T.Helper()
	if r.err != nil {
		r.h.T.Fatalf("CreditHappy %s: the call returned an error (%v), not a happy path", r.action, r.err)
	}
	if r.jobID == "" {
		r.h.T.Fatalf("CreditHappy %s: no job id on the receipt", r.action)
	}
	if job := r.h.AwaitJob(r.jobID); job.State != "complete" {
		r.h.T.Fatalf("CreditHappy %s: job %s is %q, not complete", r.action, r.jobID, job.State)
	}
	r.emit("happy", "job "+r.jobID+" complete")
}

// A refused item can leave an admitted Copy job partial. Credit that
// state only from this call's terminal job, never from a start error.
func (r *Receipt) CreditPartial() {
	r.h.T.Helper()
	if r.err != nil || r.jobID == "" || scenariocontract.All()[r.action].Kind != scenariocontract.Job {
		r.h.T.Fatalf("CreditPartial %s: not an admitted job (%v)", r.action, r.err)
	}
	if job := r.h.AwaitJob(r.jobID); job.State != "partial" {
		r.h.T.Fatalf("CreditPartial %s: job %s is %q, not partial", r.action, r.jobID, job.State)
	}
	r.emit("partial", "job "+r.jobID+" partial")
}

// CreditApplied emits a happy claim for a SYNCHRONOUS mutate, validating
// from THIS receipt that the outcome was "applied".
func (r *Receipt) CreditApplied() {
	r.h.T.Helper()
	if r.err != nil {
		r.h.T.Fatalf("CreditApplied %s: returned an error (%v)", r.action, r.err)
	}
	if r.outcome != "applied" {
		r.h.T.Fatalf("CreditApplied %s: outcome %q, not applied", r.action, r.outcome)
	}
	r.emit("happy", "outcome applied")
}

// CreditConflict emits a conflict claim, validating from THIS receipt that
// the outcome was "conflict".
func (r *Receipt) CreditConflict() {
	r.h.T.Helper()
	if r.outcome != "conflict" {
		r.h.T.Fatalf("CreditConflict %s: outcome %q, not conflict", r.action, r.outcome)
	}
	r.emit("conflict", "outcome conflict")
}

// CreditRefusal emits a refusal claim, validating from THIS receipt that
// the call actually returned an error.
func (r *Receipt) CreditRefusal() {
	r.h.T.Helper()
	if r.err == nil {
		r.h.T.Fatalf("CreditRefusal %s: the call returned no error, so it was not refused", r.action)
	}
	r.emit("refusal", "refused: "+r.err.Error())
}

func (r *Receipt) CreditRead() {
	r.h.T.Helper()
	if r.err != nil || r.action == "FindService.StartFind" || scenariocontract.All()[r.action].Kind != scenariocontract.Read {
		r.h.T.Fatalf("CreditRead %s: not a successful read (%v)", r.action, r.err)
	}
	r.emit("happy", "read completed")
}

// Bridge-action wrappers: tests call THESE (not the raw services) so each
// invocation yields a receipt that binds any later credit to this exact
// call's action and result.

func (h *Harness) CredentialStatus(ctx context.Context) (app.CredentialStatusDTO, *Receipt) {
	status, err := h.Bookmarks.CredentialStatus(ctx)
	return status, &Receipt{h: h, action: "BookmarkService.CredentialStatus", err: err}
}

func (h *Harness) RecoveryStatus(ctx context.Context) (app.RecoveryStatusDTO, *Receipt) {
	status, err := h.Jobs.RecoveryStatus(ctx)
	return status, &Receipt{h: h, action: "JobService.RecoveryStatus", err: err}
}

func (h *Harness) JobHistory(ctx context.Context, bookmark, cursor string, limit int) (app.JobHistoryDTO, *Receipt) {
	page, err := h.Jobs.History(ctx, bookmark, cursor, limit)
	return page, &Receipt{h: h, action: "JobService.History", err: err}
}

func (h *Harness) JobItems(ctx context.Context, job, cursor string, limit int) (app.JobItemPageDTO, *Receipt) {
	page, err := h.Jobs.Items(ctx, job, cursor, limit)
	return page, &Receipt{h: h, action: "JobService.Items", err: err}
}

func (h *Harness) JobItemTotals(ctx context.Context, job string) (app.JobItemTotalsDTO, *Receipt) {
	totals, err := h.Jobs.ItemTotals(ctx, job)
	return totals, &Receipt{h: h, action: "JobService.ItemTotals", err: err}
}

func (h *Harness) StartCopy(ctx context.Context, req app.CopyRequestDTO) *Receipt {
	jobID, err := h.Transfer.StartCopy(ctx, req)
	return &Receipt{h: h, action: "TransferService.StartCopy", jobID: jobID, err: err}
}

func (h *Harness) RerunJob(ctx context.Context, id string) *Receipt {
	jobID, err := h.Transfer.RerunJob(ctx, id)
	return &Receipt{h: h, action: "TransferService.RerunJob", jobID: jobID, err: err}
}

func (h *Harness) StartMirror(ctx context.Context, req app.MirrorRequestDTO) *Receipt {
	jobID, err := h.Transfer.StartMirror(ctx, req)
	return &Receipt{h: h, action: "TransferService.StartMirror", jobID: jobID, err: err}
}

func (h *Harness) StartDelete(ctx context.Context, req app.DeleteRequestDTO) *Receipt {
	jobID, err := h.Delete.StartDelete(ctx, req)
	return &Receipt{h: h, action: "DeleteService.StartDelete", jobID: jobID, err: err}
}

func (h *Harness) DeleteManifestWithSegments(ctx context.Context, req app.SegmentsDeleteRequestDTO) *Receipt {
	// The result DTO (failed/remaining segments) is intentionally not yet
	// surfaced on the receipt: this action's partial/retry/cancel cells are
	// deferred, and the full DTO and typed outcomes should be retained BEFORE
	// those cells are added. Until then only happy/refusal/conflict are
	// creditable, none of which needs the remainder list.
	_, err := h.Delete.DeleteManifestWithSegments(ctx, req)
	return &Receipt{h: h, action: "DeleteService.DeleteManifestWithSegments", err: err}
}

func (h *Harness) UpdateObjectMetadata(ctx context.Context, container, name, baseFingerprint string, edit app.ObjectMetadataEditDTO) *Receipt {
	result, err := h.Mutate.UpdateObjectMetadata(ctx, container, name, baseFingerprint, edit)
	return &Receipt{h: h, action: "MutateService.UpdateObjectMetadata", outcome: result.Outcome, err: err}
}

// account provisions a test account. It returns an error rather than
// Fatal-ing so the caller can decide: the primary must exist (Fatal),
// but a missing secondary is a Skip on a one-account laptop unless
// AERONAUTES_REQUIRE_TWO_ACCOUNTS is set (a 401 on
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
	h.T.Cleanup(ora.Close)
	a := &Account{BookmarkID: saved.ID, Oracle: ora, authURL: authURL, user: user, key: key, rawClient: &http.Client{
		Timeout: 30 * time.Second,
		// The raw seed/teardown client refuses redirects too, so a
		// misrouted setup write cannot silently land elsewhere or resend
		// the token (match the oracle).
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
	// source objects. Fail closed and loud.
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		h.T.Fatalf("seed PUT %s: HTTP %d %s", path, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	resp.Body.Close()
}

// Seed writes a raw object directly (test setup, bypassing the product).
// The object name is percent-escaped with the oracle's own encoder so a
// hostile name seeds the object the oracle will later look for.
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
// nothing survived.
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
// leave such a sidecar invisible to AssertNoCollateral. It is NOT a whole-account witness: a container the
// engine creates OUTSIDE this namespace is not seen. Prefix-scoping also keeps a parallel test's containers from
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
// an object whose name contains ": " cannot masquerade as an allowed one.
// A bare container name does NOT blanket-allow object changes inside it.
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

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
	"database/sql"
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
	"github.com/csmart/aeronautes/internal/swifttest"
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
	// connections is the registry the services share; tests read the
	// active bookmark from it to name a write's scope (ADR 0079).
	connections *app.Connections
	T           *testing.T
	ctx         context.Context
	ns          string
	counter     int64
	database    *db.DB
	jobStore    *jobs.Store
	statePath   string

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
	// MockPrimary is the primary account's in-process mock when the
	// harness runs against swifttest, so a test can count its requests;
	// nil against the owned Swift fixture.
	MockPrimary *swifttest.Server
	// Evidence lets a fixture record the ownership proofs the segment
	// dialog reads, so a test can state what the operator was shown.
	Evidence *evidence.Store

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
	// The settings main.go gives the transfer service, so a case that turns
	// an option off exercises the same path the shipped shell takes.
	settings := app.NewSettingsService(database, runner)
	app.WireSettings(transfer, settings)

	h := &Harness{
		T: t, ctx: ctx, connections: connections,
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
		Settings:  settings,
		Evidence:  ev,
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

	// Prove the fixture is writable before any test writes to it. The
	// pipeline's wait proves the proxy answers /info, which is weaker
	// (ADR 0050).
	if err := h.writableWithin("primary", h.Primary, fixtureWritableDeadline); err != nil {
		t.Fatal(err)
	}
	if h.Secondary != nil {
		if err := h.writableWithin("secondary", h.Secondary, fixtureWritableDeadline); err != nil {
			t.Fatal(err)
		}
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
	// The shell's native public-read confirmation answers yes here, as an
	// operator would; refusals are proved at the app level (ADR 0073 B).
	app.WireConfirmer(h.Mutate, confirmPublicRead{})
	return h
}

// confirmPublicRead answers the native confirmation for tests.
type confirmPublicRead struct{}

func (confirmPublicRead) ConfirmPublicRead(context.Context, string, string) (bool, error) {
	return true, nil
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

func (h *Harness) jobBookmark(ctx context.Context, jobID string) string {
	if h.jobStore != nil {
		raw, err := h.jobStore.Get(ctx, jobID)
		if err == nil {
			if bookmark := jobAccount(raw); bookmark != "" {
				return bookmark
			}
		}
	}
	if h.Primary != nil {
		return h.Primary.BookmarkID
	}
	return ""
}

func (h *Harness) JobItems(ctx context.Context, job, cursor string, limit int) (app.JobItemPageDTO, *Receipt) {
	page, err := h.Jobs.Items(ctx, h.jobBookmark(ctx, job), job, cursor, limit)
	return page, &Receipt{h: h, action: "JobService.Items", err: err}
}

func (h *Harness) JobItemTotals(ctx context.Context, job string) (app.JobItemTotalsDTO, *Receipt) {
	totals, err := h.Jobs.ItemTotals(ctx, h.jobBookmark(ctx, job), job)
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
	result, err := h.Mutate.UpdateObjectMetadata(ctx, h.ActiveScope(ctx), container, name, baseFingerprint, edit)
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
	// Each account owns its transport, as the oracle already does for
	// itself. Left unset, this client rides http.DefaultTransport, whose
	// pool is shared with everything else in the test binary: any
	// CloseIdleConnections anywhere in the process can then break a seed
	// request that is in flight on a pooled connection, which is how a
	// download case's teardown once failed an unrelated container PUT with
	// "transport connection broken: http: CloseIdleConnections called".
	transport := http.DefaultTransport.(*http.Transport).Clone()
	h.T.Cleanup(transport.CloseIdleConnections)
	a := &Account{BookmarkID: saved.ID, authURL: authURL, user: user, key: key, rawClient: &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		// The raw seed/teardown client refuses redirects too, so a
		// misrouted setup write cannot silently land elsewhere or resend
		// the token (match the oracle).
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	attempts, err := h.authenticateWithin(name, a, fixtureWritableDeadline)
	if err != nil {
		return nil, err
	}
	if attempts > 1 {
		// A retry hides an unhealthy fixture from the result. It must not
		// hide it from the run: without this line a fourth-attempt success
		// and a first-attempt success read identically (ADR 0051).
		h.T.Logf("%s account authenticated on attempt %d; the fixture refused at least once", name, attempts)
	}
	return a, nil
}

// definitiveAuthRefusal reports whether the fixture has answered and its
// answer settles the question. 401 and 403 mean these credentials do not
// name a usable account, which no amount of waiting changes: tempauth
// answers an absent account and a wrong key that way (ADR 0051). Keeping
// this immediate is what leaves the one-account skip as fast as it was,
// rather than costing thirty seconds at every harness construction.
func definitiveAuthRefusal(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

// authenticateWithin connects the oracle and the raw client, retrying a
// transient failure to the same deadline the readiness gate uses.
//
// The retry is here rather than in oracle.Connect because four of that
// function's eight callers authenticate against an httptest server, where
// an absent endpoint is the expected condition and must fail at once
// (ADR 0051).
//
// A retried setup is NOT evidence of a healthy fixture. That is why a
// success needing more than one attempt logs its attempt count: without
// it a fixture that authenticates only on the fourth try is
// indistinguishable from one that answered first time.
// It returns the number of attempts it took, so the caller reports a
// retried setup rather than this function deciding how to say it.
func (h *Harness) authenticateWithin(label string, a *Account, within time.Duration) (int, error) {
	h.T.Helper()
	deadline := time.Now().Add(within)
	for attempt := 1; ; attempt++ {
		ora, status, err := h.tryAuthenticate(a)
		if err == nil {
			h.T.Cleanup(ora.Close)
			a.Oracle = ora
			return attempt, nil
		}
		if definitiveAuthRefusal(status) {
			return attempt, fmt.Errorf("%s account refused authentication with HTTP %d, which no retry changes: %w", label, status, err)
		}
		if time.Now().After(deadline) {
			return attempt, fmt.Errorf("%s account not authenticated within %s after %d attempts, last status %d: %w",
				label, within, attempt, status, err)
		}
		time.Sleep(fixtureRetryInterval)
	}
}

// tryAuthenticate makes one attempt at both calls and reports the status
// that decided it, so the caller can tell a definitive refusal from a
// transient one. A zero status means no answer arrived at all.
//
// The raw client goes first because it is the call whose status this
// harness can observe directly. oracle.Connect reports a non-2xx only
// inside an error string, and deciding whether to retry by parsing that
// string would rest the decision on a message rather than on a status.
// Once the raw client has a 2xx from the same endpoint with the same
// credentials, an oracle failure cannot be a credential refusal, so it
// is transient by construction and needs no status of its own.
func (h *Harness) tryAuthenticate(a *Account) (*oracle.Oracle, int, error) {
	status, err := h.rawAuthStatus(a)
	if err != nil {
		return nil, status, err
	}
	ora, err := oracle.Connect(a.authURL, a.user, a.key)
	if err != nil {
		return nil, 0, err
	}
	return ora, status, nil
}

// rawAuth gives the account a raw writer for test setup/teardown that
// never touches the product.
// rawAuthStatus authenticates the raw client and returns the status that
// decided the outcome alongside any error, so a caller can distinguish a
// credential refusal from a connection that never answered. A zero status
// means the request produced no response.
func (h *Harness) rawAuthStatus(a *Account) (int, error) {
	req, _ := http.NewRequest(http.MethodGet, a.authURL, nil)
	req.Header.Set("X-Auth-User", a.user)
	req.Header.Set("X-Auth-Key", a.key)
	resp, err := a.rawClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Errorf("raw auth %s as %s: HTTP %d", a.authURL, a.user, resp.StatusCode)
	}
	a.storageURL = resp.Header.Get("X-Storage-Url")
	a.token = resp.Header.Get("X-Auth-Token")
	return resp.StatusCode, nil
}

func (h *Harness) secondaryReachable() error {
	if h.Secondary == nil || h.Secondary.storageURL == "" || h.Secondary.storageURL == h.Primary.storageURL {
		return fmt.Errorf("secondary resolves to the same account as primary")
	}
	return nil
}

// fixtureWritableDeadline bounds the readiness gate. A fixture that
// cannot take a container within this window is reported with what was
// last observed, rather than waited on until the package timeout turns
// it into a report about the wrong thing.
const fixtureWritableDeadline = 30 * time.Second

// fixtureRetryInterval paces both setup retries. Short enough that a
// transient refusal costs a fraction of a second, long enough not to
// spin against a fixture that is still starting.
const fixtureRetryInterval = 250 * time.Millisecond

// writableWithin proves what a test actually needs before its first write:
// that an authenticated client can create a container on the account it
// was given. The pipeline gates on the proxy answering /info, and a run
// has recorded that gate passing while the first test's raw container
// PUT answered 404 on the same fixture (ADR 0050). Authentication does
// not establish this either: it yields a token and a storage URL without
// writing anything.
//
// It does not prove the fixture STAYS writable. One round trip before
// the run is all this claims; a fixture that fails later fails the test
// that reaches it, as it does today.
//
// The probe carries the harness namespace, so a concurrent suite cannot
// collide with it and the residue sweep accounts for it like any other
// container of this harness.
func (h *Harness) writableWithin(label string, a *Account, within time.Duration) error {
	name := fmt.Sprintf("%s-readiness-%d", h.ns, atomic.AddInt64(&h.counter, 1))
	deadline := time.Now().Add(within)
	for attempt := 1; ; attempt++ {
		status, body, err := h.probeWritable(a, name)
		if err == nil && status/100 == 2 {
			// Leave nothing behind: the probe is setup, not a fixture.
			h.rawDeleteChecked(a, "/"+name)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s account not writable within %s: PUT /%s after %d attempts, last status %d %q, last error %v",
				label, within, name, attempt, status, body, err)
		}
		time.Sleep(fixtureRetryInterval)
	}
}

// probeWritable issues one container PUT and reports what happened. It
// does not fail the test: an unready fixture is expected to refuse for a
// while, and only the deadline in writableWithin decides.
func (h *Harness) probeWritable(a *Account, name string) (int, string, error) {
	req, err := http.NewRequest(http.MethodPut, a.storageURL+"/"+name, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("X-Auth-Token", a.token)
	resp, err := a.rawClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(body)), nil
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

func insertFixtureBookmark(t *testing.T, database *db.DB, id string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := database.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `INSERT INTO bookmarks (
			id, display_name, protocol_profile, auth_url, username,
			tls_mode, performance_profile, segment_placement, created_at, updated_at
		) VALUES (?, ?, 'swiftstack_v1', 'http://example.invalid', 'user',
			'system', 'inherit', 'rclone_default', ?, ?)`, id, id, now, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func jobAccount(j jobs.Job) string {
	if j.SourceBookmarkID != "" {
		return j.SourceBookmarkID
	}
	return j.DestBookmarkID
}

func publicJob(t *testing.T, store *jobs.Store, id string) *app.JobDTO {
	t.Helper()
	raw, err := store.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	job, err := app.NewJobService(store).Get(t.Context(), jobAccount(raw), id)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func (h *Harness) AwaitJob(jobID string) *app.JobDTO {
	h.T.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		job, err := h.Jobs.Get(h.ctx, h.jobBookmark(h.ctx, jobID), jobID)
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

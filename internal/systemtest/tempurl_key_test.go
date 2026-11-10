package systemtest

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/applog"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/mutationfacts"
)

// TestTempURLKey* certify the three war-room scenarios for
// MutateService.UpdateTempURLKey (ADR 0101): a slot is set and then cleared
// (happy), a slot Swift does not read is refused before any request
// (refusal), and a presence the operator did not see is a conflict with no
// POST (conflict). Remote effects are witnessed by raw HEADs, never by the
// adapter under test, and the secret is searched for in every durable
// record and the application log (K-SEC-006).
//
// The account level runs on the mock harness only: the real-Swift account
// is shared across suite processes and its two slots are not
// process-unique, so a run that set one could change another run's
// presence under it. The owned container is safe on both.
func TestTempURLKeyContainerMock(t *testing.T) {
	h := findMockHarness(t)
	testTempURLKey(t, h, h.Container(h.Primary, "tempurl"))
}

func TestTempURLKeyAccountMock(t *testing.T) { testTempURLKey(t, findMockHarness(t), "") }

func TestTempURLKeyContainerSwift(t *testing.T) {
	h := New(t)
	testTempURLKey(t, h, h.Container(h.Primary, "tempurl"))
}

func testTempURLKey(t *testing.T, h *Harness, container string) {
	t.Helper()
	ctx := context.Background()
	logPath := captureApplicationLog(t)
	if _, err := h.Browse.Connect(ctx, h.Primary.BookmarkID); err != nil {
		t.Fatal(err)
	}
	trace := traceMetadataWrites(t, h)
	secret := "tempurl-" + h.ns + "-s3cr3t"
	metaPrefix, path := "X-Account-Meta-", ""
	if container != "" {
		metaPrefix, path = "X-Container-Meta-", "/"+container
	}
	before := h.SnapshotNamespace(h.Primary)
	operation, resource := "account.tempurl.update", "account"
	if container != "" {
		operation, resource = "container.tempurl.update", container
	}

	// HAPPY: set a slot against the presence the inspector shows, then
	// clear it against the presence that left. The after-state carries the
	// scope the write was bound to, so the card can act again from it.
	shown := shownTempURLSlots(t, h, ctx, container)
	bound := h.ActiveScope(ctx)
	dto, rec := h.UpdateTempURLKey(ctx, app.TempURLKeyEditDTO{Container: container, Slot: "Temp-URL-Key", BaseSlots: shown, Secret: secret})
	if rec.Err() != nil || rec.Outcome() != "applied" {
		t.Fatalf("set: outcome %q, %v", rec.Outcome(), rec.Err())
	}
	var afterScope app.ListingScopeDTO
	switch {
	case container == "" && dto.AccountAfter != nil:
		afterScope = dto.AccountAfter.Scope
	case container != "" && dto.ContainerAfter != nil:
		afterScope = dto.ContainerAfter.Scope
	}
	if afterScope != bound {
		t.Fatalf("after-state scope = %+v; want the bound scope %+v", afterScope, bound)
	}
	if raw := rawTempURLSlots(t, h.Primary, path); raw["Temp-Url-Key"] != secret {
		t.Fatalf("raw slots after set = %v, want the secret in slot 1", keysOf(raw))
	}
	if after := shownTempURLSlots(t, h, ctx, container); !containsFold(after, "Temp-Url-Key") {
		t.Fatalf("inspector slots after set = %v, want slot 1 shown", after)
	}
	if holdsSecret(t, dto, secret) {
		t.Error("the returned DTO carries the secret")
	}
	rec.CreditApplied()

	_, cleared := h.UpdateTempURLKey(ctx, app.TempURLKeyEditDTO{Container: container, Slot: "Temp-URL-Key", BaseSlots: shownTempURLSlots(t, h, ctx, container), Clear: true})
	if cleared.Err() != nil || cleared.Outcome() != "applied" {
		t.Fatalf("clear: outcome %q, %v", cleared.Outcome(), cleared.Err())
	}
	if raw := rawTempURLSlots(t, h.Primary, path); raw["Temp-Url-Key"] != "" {
		t.Fatalf("raw slots after clear = %v, want slot 1 gone", keysOf(raw))
	}
	h.AssertNoCollateral(before, h.SnapshotNamespace(h.Primary))

	// REFUSAL: a slot Swift does not read is refused before any request, and
	// the refusal does not echo the secret.
	writes := len(trace.snapshot())
	_, refused := h.UpdateTempURLKey(ctx, app.TempURLKeyEditDTO{Container: container, Slot: "Temp-URL-Key-3", Secret: secret})
	if refused.Err() == nil || !strings.Contains(refused.Err().Error(), "slot") {
		t.Fatalf("unknown slot: err = %v, want a refusal naming the slot", refused.Err())
	}
	if strings.Contains(refused.Err().Error(), secret) {
		t.Error("the refusal carries the secret")
	}
	if got := trace.snapshot(); len(got) != writes {
		t.Fatalf("refusal issued writes: %v", got[writes:])
	}
	refused.CreditRefusal()

	// CONFLICT: a competing writer fills the second slot after the operator
	// saw the presence; the stale presence is refused with no POST and the
	// competing key is left where it was.
	stale := shownTempURLSlots(t, h, ctx, container)
	competing := "competing-" + h.ns
	competingSave(t, h.Primary, path, map[string]string{metaPrefix + "Temp-URL-Key-2": competing})
	t.Cleanup(func() {
		competingSave(t, h.Primary, path, map[string]string{strings.Replace(metaPrefix, "X-", "X-Remove-", 1) + "Temp-URL-Key-2": "x"})
	})
	writes = len(trace.snapshot())
	_, conflicted := h.UpdateTempURLKey(ctx, app.TempURLKeyEditDTO{Container: container, Slot: "Temp-URL-Key", BaseSlots: stale, Secret: secret})
	if conflicted.Err() != nil || conflicted.Outcome() != "conflict" {
		t.Fatalf("stale presence: outcome %q, %v; want conflict", conflicted.Outcome(), conflicted.Err())
	}
	if got := trace.snapshot(); len(got) != writes {
		t.Fatalf("conflict issued writes: %v", got[writes:])
	}
	if raw := rawTempURLSlots(t, h.Primary, path); raw["Temp-Url-Key-2"] != competing || raw["Temp-Url-Key"] != "" {
		t.Fatalf("raw slots after conflict = %v, want only the competing key", keysOf(raw))
	}
	conflicted.CreditConflict()

	// K-SEC-006: the secret is in no durable record and not in the log. The
	// audit trail names the slot and the kind of change, nothing more.
	facts, err := mutationfacts.NewStore(h.database).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range facts {
		if holdsSecret(t, fact, secret) {
			t.Error("a mutation fact carries the secret")
		}
	}
	events, err := audit.NewStore(h.database).List(ctx, audit.Query{OperationPrefix: operation})
	if err != nil {
		t.Fatal(err)
	}
	var summaries []string
	for _, event := range events {
		if event.Resource != resource {
			continue
		}
		summaries = append(summaries, event.Summary)
		if strings.Contains(event.Summary, secret) {
			t.Error("an audit event carries the secret")
		}
	}
	for _, want := range []string{"set TempURL key slot Temp-Url-Key", "cleared TempURL key slot Temp-Url-Key"} {
		if !containsFold(summaries, want) {
			t.Errorf("audit summaries for %s = %q, want %q among them", resource, summaries, want)
		}
	}
	// Nothing on the key path logs when it succeeds, so an empty capture would
	// "lack the secret" too: a line written here proves the capture is live.
	const sentinel = "tempurl key proof: the log capture is live"
	applog.Infof("%s", sentinel)
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), sentinel) {
		t.Fatal("the application log was not captured, so its silence about the secret proves nothing")
	}
	if strings.Contains(string(logged), secret) {
		t.Error("the application log carries the secret")
	}
}

// captureApplicationLog sends the process log to a file for the test's
// duration through the same switches an operator uses, so the proof reads
// what a support capture would hold. The restoring Configure is registered
// before Setenv so it runs after the environment is restored.
func captureApplicationLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "application.log")
	t.Cleanup(func() {
		if _, _, err := applog.Configure(); err != nil {
			t.Error(err)
		}
	})
	t.Setenv(applog.EnvLevel, "debug")
	t.Setenv(applog.EnvFile, path)
	_, closeLog, err := applog.Configure()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeLog() })
	return path
}

// shownTempURLSlots reads the slot presence the inspector shows for the
// account or a container: the operator's view, and the operation's base.
func shownTempURLSlots(t *testing.T, h *Harness, ctx context.Context, container string) []string {
	t.Helper()
	if container == "" {
		account, err := h.Browse.Account(ctx, h.Primary.BookmarkID)
		if err != nil {
			t.Fatal(err)
		}
		return append([]string{}, account.TempURLKeySlots...)
	}
	detail, err := h.Inspector.Container(ctx, h.Primary.BookmarkID, container)
	if err != nil {
		t.Fatal(err)
	}
	return append([]string{}, detail.TempURLKeySlots...)
}

// rawTempURLSlots reads the stored slot values behind the redaction with a
// raw HEAD, keyed by slot name as the store returns it: the oracle's view.
func rawTempURLSlots(t *testing.T, a *Account, path string) map[string]string {
	t.Helper()
	req, err := http.NewRequest(http.MethodHead, a.storageURL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Auth-Token", a.token)
	resp, err := a.rawClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("raw HEAD %s: %s", path, resp.Status)
	}
	slots := map[string]string{}
	for name, values := range resp.Header {
		lower := strings.ToLower(name)
		for _, prefix := range []string{"x-account-meta-", "x-container-meta-"} {
			if strings.HasPrefix(lower, prefix) && strings.HasPrefix(lower[len(prefix):], "temp-url-key") {
				slots[name[len(prefix):]] = values[0]
			}
		}
	}
	return slots
}

// holdsSecret searches the complete JSON form of a value, nested portraits
// included, where a %+v print would stop at their pointers.
func holdsSecret(t *testing.T, value any, secret string) bool {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(encoded), secret)
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}

func keysOf(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// The durable record of a slot write (ADR 0062 through ADR 0101 decision
// 3) at the public service boundary on the mock, with the store's effects
// read by raw HEADs: a refused intent sends no POST and leaves no fact; a
// record refused after an applied write surfaces as residue carrying the
// attempt identity while the slot has changed on the store and the fact
// stays at intent; a verify read that fails after the POST is recorded as
// unverified with the observed transaction ids, never as failed; and a
// record refused after that uncertain write is residue too. No error
// carries the secret.
func TestTempURLKeyFactsMock(t *testing.T) {
	for _, tc := range []struct {
		name, fault, refuse, errorText, outcome string
		posted                                  bool
	}{
		{name: "intent_refused", refuse: "intent", errorText: "refuse intent"},
		{name: "outcome_refused", refuse: "outcome", errorText: "tempurl key write residue", outcome: "intent", posted: true},
		{name: "audit_refused", refuse: "audit", errorText: "tempurl key write residue", outcome: "intent", posted: true},
		{name: "verify_read_refused", fault: "verify", errorText: "tempurl key write unverified", outcome: "unverified", posted: true},
		{name: "uncertain_record_refused", fault: "verify", refuse: "outcome", errorText: "tempurl key write residue", outcome: "intent", posted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := findMockHarness(t)
			ctx := context.Background()
			if _, err := h.Browse.Connect(ctx, h.Primary.BookmarkID); err != nil {
				t.Fatal(err)
			}
			container := h.Container(h.Primary, "tempurl-facts")
			trace := installContainerFactProxy(t, h, container, tc.fault, nil)
			refuseFactRecord(t, h, tc.refuse, "container.tempurl.update")
			secret := "tempurl-" + h.ns + "-facts"

			dto, err := h.Mutate.UpdateTempURLKey(ctx, h.ActiveScope(ctx), app.TempURLKeyEditDTO{Container: container, Slot: "Temp-URL-Key", Secret: secret})
			if err == nil || !strings.Contains(err.Error(), tc.errorText) || dto.Outcome != "" {
				t.Fatalf("result = %+v, %v; want an empty DTO and %q", dto, err, tc.errorText)
			}
			if strings.Contains(err.Error(), secret) {
				t.Error("the error carries the secret")
			}
			posts, ids := trace.snapshot()
			raw := rawTempURLSlots(t, h.Primary, "/"+container)
			facts, factsErr := mutationfacts.NewStore(h.database).All(ctx)
			if factsErr != nil {
				t.Fatal(factsErr)
			}
			if !tc.posted {
				if posts != 0 || raw["Temp-Url-Key"] != "" || len(facts) != 0 {
					t.Fatalf("posts = %d, raw slots = %v, facts = %+v; want nothing sent and nothing recorded", posts, keysOf(raw), facts)
				}
				return
			}
			if posts != 1 || raw["Temp-Url-Key"] != secret {
				t.Fatalf("posts = %d, raw slots = %v; want one POST that set the slot", posts, keysOf(raw))
			}
			if len(facts) != 1 || string(facts[0].Outcome) != tc.outcome || !strings.Contains(err.Error(), facts[0].Identity) {
				t.Fatalf("facts = %+v, err = %v; want outcome %q and the identity in the error", facts, err, tc.outcome)
			}
			events, err := audit.NewStore(h.database).List(ctx, audit.Query{OperationPrefix: "container.tempurl.update"})
			if err != nil {
				t.Fatal(err)
			}
			switch tc.outcome {
			case "intent":
				if len(events) != 0 {
					t.Errorf("audit events = %+v; want none when the record was refused", events)
				}
			case "unverified":
				if len(events) != 1 || string(events[0].Result) != "unverified" || !reflect.DeepEqual(events[0].TransIDs, ids) || strings.Contains(events[0].Summary, secret) {
					t.Errorf("audit events = %+v; want one unverified event carrying the observed ids %v and no secret", events, ids)
				}
				if !reflect.DeepEqual(facts[0].TransIDs, ids) {
					t.Errorf("fact ids = %v; want the observed %v", facts[0].TransIDs, ids)
				}
			}
		})
	}
}

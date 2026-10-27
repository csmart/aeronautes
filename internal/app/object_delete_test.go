package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

const deleteFixture = "normal/hello.txt"

// shownObject reads the inspector's detail for a fixture object: the identity a
// delete carries is the one the operator was shown, never a listing row.
func shownObject(t *testing.T, h *harness, name string) ObjectDetailDTO {
	t.Helper()
	detail, err := h.inspector.Object(context.Background(), h.bookmarkID(), "fixtures", name)
	if err != nil {
		t.Fatal(err)
	}
	return detail
}

func objectDeleteFacts(t *testing.T, h *harness) []mutationfacts.Fact {
	t.Helper()
	all, err := factsOn(t, h).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func objectDeleteEvents(t *testing.T, h *harness) []AuditEventDTO {
	t.Helper()
	events, err := h.auditSvc.RecentFor(context.Background(), h.bookmarkID(), 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []AuditEventDTO
	for _, e := range events {
		if e.Operation == "object.delete" {
			out = append(out, e)
		}
	}
	return out
}

func deleteRequests(log []string, name string) int {
	n := 0
	for _, line := range log {
		if strings.HasPrefix(line, "DELETE ") && strings.Contains(line, name) {
			n++
		}
	}
	return n
}

func TestObjectDeleteAppliesAndRecordsTheFact(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	shown := shownObject(t, h, deleteFixture)

	result, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, shown.ContentFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "applied" || result.ObjectAfter != nil {
		t.Fatalf("result = %+v, want applied with nothing observed", result)
	}
	if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", deleteFixture); !swiftcontrol.IsNotFound(err) {
		t.Fatalf("the object is still readable: %v", err)
	}
	if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", "empty-object"); err != nil {
		t.Fatalf("a sibling was touched: %v", err)
	}
	facts := objectDeleteFacts(t, h)
	if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || facts[0].Operation != "object.delete" || facts[0].Resource != "fixtures/"+deleteFixture {
		t.Fatalf("facts = %+v", facts)
	}
	events := objectDeleteEvents(t, h)
	if len(events) != 1 || events[0].Result != "applied" || !strings.Contains(events[0].Summary, "nothing observed at the name afterwards") {
		t.Fatalf("audit = %+v", events)
	}
}

// ADR 0079: a portrait that is empty, names another generation or another
// bookmark is refused before any intent and before any request.
func TestObjectDeleteRefusesAChangedScopeWithoutTouchingAnything(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	shown := shownObject(t, h, deleteFixture)
	current := h.portrait()
	requests := h.mock.Requests()

	for name, scope := range map[string]ListingScopeDTO{
		"empty":            {},
		"moved generation": {BookmarkID: current.BookmarkID, Generation: current.Generation + 7},
		"another bookmark": {BookmarkID: "not-the-active-one", Generation: current.Generation},
	} {
		_, err := h.mutate.DeleteObject(ctx, scope, "fixtures", deleteFixture, shown.ContentFingerprint)
		if !errors.Is(err, ErrMutationScopeChanged) {
			t.Fatalf("%s: err = %v, want the scope refusal", name, err)
		}
	}
	if got := h.mock.Requests(); got != requests {
		t.Fatalf("refused deletes sent %d requests to the store", got-requests)
	}
	if facts := objectDeleteFacts(t, h); len(facts) != 0 {
		t.Fatalf("refused deletes recorded facts: %+v", facts)
	}
}

func TestObjectDeleteIntentAbortSendsNoDelete(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	shown := shownObject(t, h, deleteFixture)
	refuseSQL(t, h, `CREATE TRIGGER refuse_intent BEFORE INSERT ON mutation_facts
		BEGIN SELECT RAISE(ABORT,'fixture required intent refusal'); END`)
	requests := h.mock.Requests()

	if _, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, shown.ContentFingerprint); err == nil {
		t.Fatal("a refused intent must fail the call")
	}
	if n := deleteRequests(h.mock.RequestLog(), deleteFixture); n != 0 {
		t.Fatalf("%d DELETE requests left although the intent was refused", n)
	}
	if got := h.mock.Requests(); got != requests {
		t.Fatalf("a refused intent still sent %d requests of some kind", got-requests)
	}
	if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", deleteFixture); err != nil {
		t.Fatalf("the object is gone: %v", err)
	}
}

// ADR 0085: a replacement between the display and the request is refused with
// both identities in the fact and the event, and nothing is deleted.
func TestObjectDeleteRecordsAnIdentityConflict(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	shown := shownObject(t, h, deleteFixture)
	h.mock.PutObject("fixtures", deleteFixture, swifttest.Object{Bytes: 40, ContentType: "text/plain", ETag: "00000000000000000000000000000040"})

	_, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, shown.ContentFingerprint)
	var conflict *swiftcontrol.IdentityConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want an identity conflict", err)
	}
	if detail, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", deleteFixture); err != nil || detail.ContentLength != 40 {
		t.Fatalf("the replacement was touched: %+v %v", detail, err)
	}
	facts := objectDeleteFacts(t, h)
	if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeConflict {
		t.Fatalf("facts = %+v", facts)
	}
	events := objectDeleteEvents(t, h)
	if len(events) != 1 || events[0].Result != "conflict" || !strings.Contains(events[0].Summary, shown.ContentFingerprint) || !strings.Contains(events[0].Summary, conflict.Found) {
		t.Fatalf("audit = %+v", events)
	}
}

// ADR 0058 and ADR 0092 decision 4: a landed or possibly landed DELETE whose
// outcome cannot be recorded is residue carrying the identity, and a reopen
// shows the intent and no terminal outcome.
func TestObjectDeleteResidueWhenTheOutcomeCannotBeRecorded(t *testing.T) {
	for name, loseReply := range map[string]bool{"landed": false, "reply lost": true} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			ctx := context.Background()
			shown := shownObject(t, h, deleteFixture)
			if loseReply {
				h.mock.DropNextObjectDELETE()
			}
			refuseSQL(t, h, `CREATE TRIGGER refuse_audit BEFORE INSERT ON audit_events
				BEGIN SELECT RAISE(ABORT,'fixture required audit refusal'); END`)

			_, residue := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, shown.ContentFingerprint)
			if !errors.Is(residue, errObjectDeleteResidue) {
				t.Fatalf("err = %v, want residue", residue)
			}
			all, err := reopenFacts(t, h).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 1 || all[0].Outcome != mutationfacts.OutcomeIntent {
				t.Fatalf("reopen = %+v, want the intent alone", all)
			}
			if !strings.Contains(residue.Error(), all[0].Identity) {
				t.Fatalf("the residue error does not name its identity %s: %v", all[0].Identity, residue)
			}
		})
	}
}

// A refusal that destroyed nothing is not residue when its record fails.
func TestObjectDeleteRefusalRecordFailureIsNotResidue(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	shown := shownObject(t, h, deleteFixture)
	h.mock.PutObject("fixtures", deleteFixture, swifttest.Object{Bytes: 40, ContentType: "text/plain", ETag: "00000000000000000000000000000040"})
	refuseSQL(t, h, `CREATE TRIGGER refuse_audit BEFORE INSERT ON audit_events
		BEGIN SELECT RAISE(ABORT,'fixture required audit refusal'); END`)

	_, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, shown.ContentFingerprint)
	if err == nil || errors.Is(err, errObjectDeleteResidue) {
		t.Fatalf("err = %v, want the record failure without a residue claim", err)
	}
}

func TestObjectDeleteLostReplyIsUnverified(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	shown := shownObject(t, h, deleteFixture)
	h.mock.DropNextObjectDELETE()

	_, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, shown.ContentFingerprint)
	if !errors.Is(err, swiftcontrol.ErrUnverifiedDelete) {
		t.Fatalf("err = %v, want unverified", err)
	}
	facts := objectDeleteFacts(t, h)
	if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeUnverified {
		t.Fatalf("facts = %+v", facts)
	}
	events := objectDeleteEvents(t, h)
	if len(events) != 1 || events[0].Result != "unverified" || !strings.Contains(events[0].Summary, "no store answer proves the delete") {
		t.Fatalf("audit = %+v", events)
	}
}

// ADR 0092 decision 5: a successor the literal read observed reaches the
// caller as ObjectAfter and the ledger as a described observation.
func TestObjectDeleteDisclosesAnObservedSuccessor(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	shown := shownObject(t, h, deleteFixture)
	placed := false
	h.mock.AfterRequest = func(method, path string) {
		if method == "DELETE" && strings.HasSuffix(path, "/fixtures/"+deleteFixture) && !placed {
			placed = true
			h.mock.PutObject("fixtures", deleteFixture, swifttest.Object{SymlinkTarget: "fixtures/nowhere"})
		}
	}

	result, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, shown.ContentFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "applied" || result.ObjectAfter == nil || result.ObjectAfter.SymlinkTarget != "fixtures/nowhere" {
		t.Fatalf("result = %+v, want applied with the link observed", result)
	}
	events := objectDeleteEvents(t, h)
	if len(events) != 1 || !strings.Contains(events[0].Summary, "the name now holds a symlink") || !strings.Contains(events[0].Summary, "archived predecessor or a concurrent write") {
		t.Fatalf("audit = %+v", events)
	}
	// The DELETE's own transaction identifier survives into the record, not
	// only the comparison HEAD's.
	facts := objectDeleteFacts(t, h)
	if len(facts) != 1 || !containsString(facts[0].TransIDs, "txswifttest-object-delete") || !containsString(events[0].TransIDs, "txswifttest-object-delete") {
		t.Fatalf("the DELETE transaction id is missing: facts %+v event %+v", facts, events[0])
	}
}

// ADR 0092: an empty object name must never broaden the action into a
// container action, and the inspector must never supply an identity for one.
func TestObjectDeleteRefusesAnEmptyNameBeforeAnythingHappens(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	requests := h.mock.Requests()
	if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", ""); err == nil {
		t.Fatal("the inspector answered an empty object name")
	}
	identity := shownObject(t, h, deleteFixture).ContentFingerprint
	requests = h.mock.Requests()
	for name, parts := range map[string][2]string{"empty name": {"fixtures", ""}, "empty container": {"", deleteFixture}} {
		if _, err := h.mutate.DeleteObject(ctx, h.portrait(), parts[0], parts[1], identity); err == nil {
			t.Fatalf("%s was not refused", name)
		}
	}
	if got := h.mock.Requests(); got != requests {
		t.Fatalf("refused deletes sent %d requests", got-requests)
	}
	if facts := objectDeleteFacts(t, h); len(facts) != 0 {
		t.Fatalf("refused deletes recorded facts: %+v", facts)
	}
	if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", deleteFixture); err != nil {
		t.Fatalf("the object is gone: %v", err)
	}
}

// A literal read that fails after a landed DELETE is unverified, and its
// summary names the failure by kind, never by the request text, which carries
// the URL and with it the object name. A name the summary filter would refuse
// must not make the outcome unrecordable (review db:900).
func TestObjectDeleteKeepsTheAuditSafeWhenTheLiteralReadFails(t *testing.T) {
	for _, name := range []string{"private-name.txt", "password.txt"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.mock.PutObject("fixtures", name, swifttest.Object{Bytes: 12, ContentType: "text/plain", ETag: "0123456789abcdef0123456789abcdef"})
			var literalReads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/fixtures/"+name) && r.URL.Query().Get("symlink") == "get" {
					literalReads.Add(1)
					if hj, ok := w.(http.Hijacker); ok {
						if conn, _, err := hj.Hijack(); err == nil {
							conn.Close()
							return
						}
					}
					t.Errorf("the literal read could not be cut off")
					return
				}
				h.mock.ServeHTTP(w, r)
			}))
			t.Cleanup(server.Close)
			h.authURL = server.URL + "/auth/v1.0"
			h.connect(t)
			ctx := context.Background()
			shown := shownObject(t, h, name)

			_, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", name, shown.ContentFingerprint)
			if literalReads.Load() == 0 {
				t.Fatal("the literal read was never reached")
			}
			if !errors.Is(err, swiftcontrol.ErrUnverifiedDelete) || errors.Is(err, errObjectDeleteResidue) {
				t.Fatalf("err = %v, want unverified without a residue claim", err)
			}
			if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", name); !swiftcontrol.IsNotFound(err) {
				t.Fatalf("the DELETE did not land: %v", err)
			}
			facts := objectDeleteFacts(t, h)
			if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeUnverified {
				t.Fatalf("facts = %+v", facts)
			}
			events := objectDeleteEvents(t, h)
			if len(events) != 1 || events[0].Result != "unverified" {
				t.Fatalf("audit = %+v", events)
			}
			summary := events[0].Summary
			if strings.Contains(summary, name) || strings.Contains(summary, server.URL) || strings.Contains(summary, "http") || !strings.Contains(summary, "the literal read of the name failed afterwards (") {
				t.Fatalf("summary = %q", summary)
			}
		})
	}
}

// gatedHarness fronts the mock with a proxy so a case can alter one exchange
// of the public service: the connection is made through the gate before
// anything else runs.
func gatedHarness(t *testing.T, gate func(w http.ResponseWriter, r *http.Request, h *harness) bool) *harness {
	t.Helper()
	h := newHarness(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gate(w, r, h) {
			return
		}
		h.mock.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	h.authURL = server.URL + "/auth/v1.0"
	h.connect(t)
	return h
}

func isLiteralReadOf(r *http.Request, name string) bool {
	return r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/fixtures/"+name) && r.URL.Query().Get("symlink") == "get"
}

// The failure streams and successor kinds ADR 0092 names, through the public
// service with its durable record: each lands as the outcome word the contract
// gives it, with a summary that says what was observed.
func TestObjectDeleteFailureStreamsAndSuccessorsThroughTheService(t *testing.T) {
	ctx := context.Background()
	t.Run("a server error is unverified with the object still observed", func(t *testing.T) {
		h := newHarness(t)
		h.connect(t)
		shown := shownObject(t, h, deleteFixture)
		h.mock.FailDeletes = true
		_, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, shown.ContentFingerprint)
		if !errors.Is(err, swiftcontrol.ErrUnverifiedDelete) {
			t.Fatalf("err = %v", err)
		}
		facts := objectDeleteFacts(t, h)
		if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeUnverified || !strings.Contains(facts[0].Summary, "the identity that was shown") {
			t.Fatalf("facts = %+v", facts)
		}
		if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", deleteFixture); err != nil {
			t.Fatalf("the object is gone after a server error: %v", err)
		}
	})
	t.Run("the same identity written back is unverified", func(t *testing.T) {
		h := newHarness(t)
		h.connect(t)
		shown := shownObject(t, h, deleteFixture)
		placed := false
		h.mock.AfterRequest = func(method, path string) {
			if method == "DELETE" && strings.HasSuffix(path, "/fixtures/"+deleteFixture) && !placed {
				placed = true
				h.mock.PutObject("fixtures", deleteFixture, swifttest.Object{Bytes: 12, ContentType: "text/plain", ETag: "5eb63bbbe01eeed093cb22bb8f5acdc3"})
			}
		}
		_, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, shown.ContentFingerprint)
		if !errors.Is(err, swiftcontrol.ErrUnverifiedDelete) || !placed {
			t.Fatalf("err = %v placed = %v", err, placed)
		}
		if facts := objectDeleteFacts(t, h); len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeUnverified || !strings.Contains(facts[0].Summary, "the identity that was shown") {
			t.Fatalf("facts = %+v", facts)
		}
	})
	t.Run("an incomparable literal read is unverified and says so", func(t *testing.T) {
		h := gatedHarness(t, func(w http.ResponseWriter, r *http.Request, h *harness) bool {
			if !isLiteralReadOf(r, deleteFixture) {
				return false
			}
			rec := httptest.NewRecorder()
			h.mock.ServeHTTP(rec, r)
			for k, values := range rec.Header() {
				if k != "Etag" {
					w.Header()[k] = append([]string(nil), values...)
				}
			}
			w.WriteHeader(rec.Code)
			return true
		})
		shown := shownObject(t, h, deleteFixture)
		placed := false
		h.mock.AfterRequest = func(method, path string) {
			if method == "DELETE" && strings.HasSuffix(path, "/fixtures/"+deleteFixture) && !placed {
				placed = true
				h.mock.PutObject("fixtures", deleteFixture, swifttest.Object{Bytes: 12, ContentType: "text/plain", ETag: "5eb63bbbe01eeed093cb22bb8f5acdc3"})
			}
		}
		_, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, shown.ContentFingerprint)
		if !errors.Is(err, swiftcontrol.ErrUnverifiedDelete) {
			t.Fatalf("err = %v", err)
		}
		if facts := objectDeleteFacts(t, h); len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeUnverified || !strings.Contains(facts[0].Summary, "did not report completely") {
			t.Fatalf("facts = %+v", facts)
		}
	})
	t.Run("a manifest whose segments are missing is observed as the successor", func(t *testing.T) {
		h := newHarness(t)
		h.connect(t)
		shown := shownObject(t, h, deleteFixture)
		placed := false
		h.mock.AfterRequest = func(method, path string) {
			if method == "DELETE" && strings.HasSuffix(path, "/fixtures/"+deleteFixture) && !placed {
				placed = true
				h.mock.PutObject("fixtures", deleteFixture, swifttest.Object{Manifest: "missing-segments/x/", ContentType: "video/mp4"})
			}
		}
		result, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, shown.ContentFingerprint)
		if err != nil || result.Outcome != "applied" || result.ObjectAfter == nil || result.ObjectAfter.Class != "dlo" {
			t.Fatalf("result = %+v, err = %v", result, err)
		}
		if facts := objectDeleteFacts(t, h); len(facts) != 1 || !strings.Contains(facts[0].Summary, "dlo manifest") || !containsString(facts[0].TransIDs, "txswifttest-object-delete") {
			t.Fatalf("facts = %+v", facts)
		}
	})
	t.Run("an empty object replaced by a link is observed as a link", func(t *testing.T) {
		h := newHarness(t)
		h.connect(t)
		shown := shownObject(t, h, "empty-object")
		placed := false
		h.mock.AfterRequest = func(method, path string) {
			if method == "DELETE" && strings.HasSuffix(path, "/fixtures/empty-object") && !placed {
				placed = true
				h.mock.PutObject("fixtures", "empty-object", swifttest.Object{SymlinkTarget: "fixtures/nowhere"})
			}
		}
		result, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", "empty-object", shown.ContentFingerprint)
		if err != nil || result.Outcome != "applied" || result.ObjectAfter == nil || result.ObjectAfter.SymlinkTarget != "fixtures/nowhere" {
			t.Fatalf("result = %+v, err = %v", result, err)
		}
		if facts := objectDeleteFacts(t, h); len(facts) != 1 || !strings.Contains(facts[0].Summary, "symlink") || !containsString(facts[0].TransIDs, "txswifttest-object-delete") {
			t.Fatalf("facts = %+v", facts)
		}
	})
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// ADR 0085's accepted exposure, measured rather than prevented: a writer whose
// replacement lands after the final comparison HEAD and before the DELETE
// arrives loses that replacement, and the delete reports an applied absence.
// Nothing here may be read as the window being closed.
func TestObjectDeleteExposureAfterTheFinalComparisonIsMeasured(t *testing.T) {
	ctx := context.Background()
	landed := false
	h := gatedHarness(t, func(w http.ResponseWriter, r *http.Request, h *harness) bool {
		if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/fixtures/"+deleteFixture) && !landed {
			landed = true
			h.mock.PutObject("fixtures", deleteFixture, swifttest.Object{Bytes: 40, ContentType: "text/plain", ETag: "00000000000000000000000000000040"})
		}
		return false
	})
	shown := shownObject(t, h, deleteFixture)
	result, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, shown.ContentFingerprint)
	if err != nil || result.Outcome != "applied" || result.ObjectAfter != nil || !landed {
		t.Fatalf("result = %+v, err = %v, landed = %v", result, err, landed)
	}
	if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", deleteFixture); !swiftcontrol.IsNotFound(err) {
		t.Fatalf("the replacement that landed inside the window survived: %v", err)
	}
	if facts := objectDeleteFacts(t, h); len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || !containsString(facts[0].TransIDs, "txswifttest-object-delete") || !strings.Contains(facts[0].Summary, "nothing observed") {
		t.Fatalf("facts = %+v", facts)
	}
	if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", "empty-object"); err != nil {
		t.Fatalf("a sibling was touched: %v", err)
	}
}

// ADR 0079 with the real generation change: saving the bookmark's key moves
// the generation, and a portrait captured before it is refused before intent,
// audit or request.
func TestObjectDeleteRefusesAPortraitFromBeforeAKeyChange(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	shown := shownObject(t, h, deleteFixture)
	stale := h.portrait()
	if _, err := h.bookmarks.Save(ctx, BookmarkDTO{ID: h.bookmarkID(), DisplayName: "Mock", AuthURL: h.authURL, Username: "test:tester"}, "testing"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.browse.Connect(ctx, h.bookmarkID()); err != nil {
		t.Fatal(err)
	}
	if current := h.portrait(); current.Generation <= stale.Generation {
		t.Fatalf("the generation did not move: %+v after %+v", current, stale)
	}
	requests := h.mock.Requests()
	if _, err := h.mutate.DeleteObject(ctx, stale, "fixtures", deleteFixture, shown.ContentFingerprint); !errors.Is(err, ErrMutationScopeChanged) {
		t.Fatalf("err = %v, want the scope refusal", err)
	}
	if got := h.mock.Requests(); got != requests {
		t.Fatalf("the refused delete sent %d requests", got-requests)
	}
	if facts := objectDeleteFacts(t, h); len(facts) != 0 {
		t.Fatalf("facts = %+v", facts)
	}
	if events := objectDeleteEvents(t, h); len(events) != 0 {
		t.Fatalf("audit = %+v", events)
	}
	if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", deleteFixture); err != nil {
		t.Fatalf("the object is gone: %v", err)
	}
}

// A store refusal is failed and destroys nothing; a reply lost before the store
// applied anything is unverified with the object still observed; both say so.
func TestObjectDeleteStoreRefusalAndLostReplyVariants(t *testing.T) {
	ctx := context.Background()
	t.Run("a refused DELETE is failed with the object intact", func(t *testing.T) {
		h := gatedHarness(t, func(w http.ResponseWriter, r *http.Request, h *harness) bool {
			if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/fixtures/"+deleteFixture) {
				w.WriteHeader(http.StatusForbidden)
				return true
			}
			return false
		})
		shown := shownObject(t, h, deleteFixture)
		_, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, shown.ContentFingerprint)
		var se *swiftcontrol.SwiftError
		if !errors.As(err, &se) || se.Kind != swiftcontrol.KindForbidden || errors.Is(err, swiftcontrol.ErrUnverifiedDelete) {
			t.Fatalf("err = %v, want the store's refusal", err)
		}
		facts := objectDeleteFacts(t, h)
		if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeFailed || !strings.Contains(facts[0].Summary, "destroyed nothing") {
			t.Fatalf("facts = %+v", facts)
		}
		if events := objectDeleteEvents(t, h); len(events) != 1 || events[0].Result != "failed" {
			t.Fatalf("audit = %+v", events)
		}
		if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", deleteFixture); err != nil {
			t.Fatalf("the object is gone after a refused DELETE: %v", err)
		}
	})
	t.Run("a reply lost before the store acted is unverified with the object still observed", func(t *testing.T) {
		h := gatedHarness(t, func(w http.ResponseWriter, r *http.Request, h *harness) bool {
			if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/fixtures/"+deleteFixture) {
				if hj, ok := w.(http.Hijacker); ok {
					if conn, _, err := hj.Hijack(); err == nil {
						conn.Close()
						return true
					}
				}
				t.Errorf("the DELETE could not be cut off")
				return true
			}
			return false
		})
		shown := shownObject(t, h, deleteFixture)
		_, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, shown.ContentFingerprint)
		if !errors.Is(err, swiftcontrol.ErrUnverifiedDelete) {
			t.Fatalf("err = %v, want unverified", err)
		}
		facts := objectDeleteFacts(t, h)
		if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeUnverified ||
			!strings.Contains(facts[0].Summary, "no store answer proves the delete, and the name now holds") || !strings.Contains(facts[0].Summary, "the identity that was shown") {
			t.Fatalf("facts = %+v", facts)
		}
		if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", deleteFixture); err != nil {
			t.Fatalf("the object is gone although the store never answered: %v", err)
		}
	})
}

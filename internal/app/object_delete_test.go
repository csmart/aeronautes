package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

const deleteFixture = "normal/hello.txt"

// shownObject reads the inspector's detail for a fixture object, as the
// window does before it offers the object's deletion.
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

// requestsNaming are the requests the store received for one object after the
// first skip of its log.
func requestsNaming(h *harness, skip int, name string) []string {
	var seen []string
	for _, line := range h.mock.RequestLog()[skip:] {
		if strings.Contains(line, "/fixtures/"+name) {
			seen = append(seen, line)
		}
	}
	return seen
}

// ADR 0122 decision 6: one DELETE, the store's answer as the result, and the
// fact and the event that say so. The name is not read before or after.
func TestObjectDeleteSendsOneDeleteAndRecordsTheAnswer(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	before := len(h.mock.RequestLog())

	result, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, "delete "+deleteFixture)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "applied" || result.ObjectAfter != nil {
		t.Fatalf("result = %+v, want applied", result)
	}
	if got := requestsNaming(h, before, deleteFixture); len(got) != 1 || !strings.HasPrefix(got[0], "DELETE ") {
		t.Fatalf("the store received %q for the object, want one DELETE and no read", got)
	}
	if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", deleteFixture); !swiftcontrol.IsNotFound(err) {
		t.Fatalf("the object is still readable: %v", err)
	}
	if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", "empty-object"); err != nil {
		t.Fatalf("a sibling was touched: %v", err)
	}
	facts := objectDeleteFacts(t, h)
	if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied || facts[0].Operation != "object.delete" || facts[0].Resource != "fixtures/"+deleteFixture || !containsString(facts[0].TransIDs, "txswifttest-object-delete") {
		t.Fatalf("facts = %+v", facts)
	}
	events := objectDeleteEvents(t, h)
	if len(events) != 1 || events[0].Result != "applied" || !strings.Contains(events[0].Summary, "the store accepted the DELETE") || !containsString(events[0].TransIDs, "txswifttest-object-delete") {
		t.Fatalf("audit = %+v", events)
	}
}

// The old refusal is gone, not merely untested: an object another writer
// replaced after the operator looked is deleted, and the deletion succeeds.
func TestObjectDeleteDeletesWhatAnotherWriterPutThere(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	shownObject(t, h, deleteFixture)
	h.mock.PutObject("fixtures", deleteFixture, swifttest.Object{Bytes: 40, ContentType: "text/plain", ETag: "00000000000000000000000000000040"})

	result, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, "delete "+deleteFixture)
	if err != nil || result.Outcome != "applied" {
		t.Fatalf("result = %+v, err = %v, want applied", result, err)
	}
	if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", deleteFixture); !swiftcontrol.IsNotFound(err) {
		t.Fatalf("the replaced object is still readable: %v", err)
	}
	if facts := objectDeleteFacts(t, h); len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeApplied {
		t.Fatalf("facts = %+v", facts)
	}
}

// A name that was already gone is the outcome asked for.
func TestObjectDeleteTakesNotFoundAsAlreadyGone(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	h.mock.RemoveObject("fixtures", deleteFixture)

	result, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, "delete "+deleteFixture)
	if err != nil || result.Outcome != "applied" {
		t.Fatalf("result = %+v, err = %v, want applied", result, err)
	}
	events := objectDeleteEvents(t, h)
	if len(events) != 1 || events[0].Result != "applied" || !strings.Contains(events[0].Summary, "already gone") {
		t.Fatalf("audit = %+v", events)
	}
}

// ADR 0079: a portrait that is empty, names another generation or another
// bookmark is refused before any intent and before any request.
func TestObjectDeleteRefusesAChangedScopeWithoutTouchingAnything(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	current := h.portrait()
	requests := h.mock.Requests()

	for name, scope := range map[string]ListingScopeDTO{
		"empty":            {},
		"moved generation": {BookmarkID: current.BookmarkID, Generation: current.Generation + 7},
		"another bookmark": {BookmarkID: "not-the-active-one", Generation: current.Generation},
	} {
		_, err := h.mutate.DeleteObject(ctx, scope, "fixtures", deleteFixture, "delete "+deleteFixture)
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
	refuseSQL(t, h, `CREATE TRIGGER refuse_intent BEFORE INSERT ON mutation_facts
		BEGIN SELECT RAISE(ABORT,'fixture required intent refusal'); END`)
	requests := h.mock.Requests()

	if _, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, "delete "+deleteFixture); err == nil {
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

func TestObjectDeleteResidueWhenTheOutcomeCannotBeRecorded(t *testing.T) {
	for name, loseReply := range map[string]bool{"landed": false, "reply lost": true} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			ctx := context.Background()
			if loseReply {
				h.mock.DropNextObjectDELETE()
			}
			refuseSQL(t, h, `CREATE TRIGGER refuse_audit BEFORE INSERT ON audit_events
				BEGIN SELECT RAISE(ABORT,'fixture required audit refusal'); END`)

			_, residue := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, "delete "+deleteFixture)
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

// forbiddenDeletes is a store that refuses one fixture object's DELETE.
func forbiddenDeletes(t *testing.T, name string) *harness {
	return gatedHarness(t, func(w http.ResponseWriter, r *http.Request, h *harness) bool {
		if r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/fixtures/"+name) {
			w.WriteHeader(http.StatusForbidden)
			return true
		}
		return false
	})
}

// A refusal that destroyed nothing is not residue when its record fails.
func TestObjectDeleteRefusalRecordFailureIsNotResidue(t *testing.T) {
	h := forbiddenDeletes(t, deleteFixture)
	refuseSQL(t, h, `CREATE TRIGGER refuse_audit BEFORE INSERT ON audit_events
		BEGIN SELECT RAISE(ABORT,'fixture required audit refusal'); END`)

	_, err := h.mutate.DeleteObject(context.Background(), h.portrait(), "fixtures", deleteFixture, "delete "+deleteFixture)
	var se *swiftcontrol.SwiftError
	if !errors.As(err, &se) || se.Kind != swiftcontrol.KindForbidden || errors.Is(err, errObjectDeleteResidue) {
		t.Fatalf("err = %v, want the store's refusal and the record failure without a residue claim", err)
	}
}

func TestObjectDeleteRefusesAnEmptyNameBeforeAnythingHappens(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", ""); err == nil {
		t.Fatal("the inspector answered an empty object name")
	}
	requests := h.mock.Requests()
	for name, parts := range map[string][2]string{"empty name": {"fixtures", ""}, "empty container": {"", deleteFixture}} {
		if _, err := h.mutate.DeleteObject(ctx, h.portrait(), parts[0], parts[1], "delete "+parts[1]); err == nil {
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

// gatedHarness is a harness whose store sits behind a gate that may answer a
// request itself.
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

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// One object and one marker are armed by typing "delete" and the name (ADR
// 0093 decision 9): nothing else, the bare name that only a button stood for
// before included, reaches the store or the ledger.
func TestObjectAndMarkerDeleteNeedTheVerbAndTheName(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
	requests := h.mock.Requests()
	factsBefore := len(objectDeleteFacts(t, h))
	for typed, wanted := range map[string]string{"": "delete " + deleteFixture, deleteFixture: "delete " + deleteFixture, "delete other": "delete " + deleteFixture} {
		if _, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, typed); err == nil || !strings.Contains(err.Error(), `does not match "`+wanted+`"`) {
			t.Errorf("object armed by %q: %v", typed, err)
		}
	}
	if _, err := h.mutate.DeleteMarker(ctx, h.portrait(), "fixtures", "folder-marker/", "folder-marker/"); err == nil || !strings.Contains(err.Error(), `does not match "delete folder-marker/"`) {
		t.Errorf("marker armed by its bare name: %v", err)
	}
	if got := h.mock.Requests(); got != requests {
		t.Fatalf("refused deletions sent %d requests", got-requests)
	}
	if got := len(objectDeleteFacts(t, h)); got != factsBefore {
		t.Fatalf("refused deletions recorded %d facts", got-factsBefore)
	}
}

func TestObjectDeleteRefusesAPortraitFromBeforeAKeyChange(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	ctx := context.Background()
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
	if _, err := h.mutate.DeleteObject(ctx, stale, "fixtures", deleteFixture, "delete "+deleteFixture); !errors.Is(err, ErrMutationScopeChanged) {
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

// A store refusal is failed and destroys nothing. A DELETE that got no
// answer is the unknown, and nothing is read to settle it: the store here
// goes on holding the object, as one whose replicas lag would go on serving
// it, and that is not looked at.
func TestObjectDeleteStoreRefusalAndLostReply(t *testing.T) {
	ctx := context.Background()
	t.Run("a refused DELETE is failed with the object intact", func(t *testing.T) {
		h := forbiddenDeletes(t, deleteFixture)
		_, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, "delete "+deleteFixture)
		var se *swiftcontrol.SwiftError
		if !errors.As(err, &se) || se.Kind != swiftcontrol.KindForbidden || errors.Is(err, swiftcontrol.ErrUnverifiedDelete) {
			t.Fatalf("err = %v, want the store's refusal", err)
		}
		facts := objectDeleteFacts(t, h)
		if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeFailed || !strings.Contains(facts[0].Summary, "refused by the store with HTTP 403; nothing was deleted") {
			t.Fatalf("facts = %+v", facts)
		}
		if events := objectDeleteEvents(t, h); len(events) != 1 || events[0].Result != "failed" {
			t.Fatalf("audit = %+v", events)
		}
		if _, err := h.inspector.Object(ctx, h.bookmarkID(), "fixtures", deleteFixture); err != nil {
			t.Fatalf("the object is gone after a refused DELETE: %v", err)
		}
	})
	t.Run("a DELETE with no answer is unverified and nothing is read after it", func(t *testing.T) {
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
		before := len(h.mock.RequestLog())
		_, err := h.mutate.DeleteObject(ctx, h.portrait(), "fixtures", deleteFixture, "delete "+deleteFixture)
		if !errors.Is(err, swiftcontrol.ErrUnverifiedDelete) {
			t.Fatalf("err = %v, want unverified", err)
		}
		facts := objectDeleteFacts(t, h)
		if len(facts) != 1 || facts[0].Outcome != mutationfacts.OutcomeUnverified || !strings.Contains(facts[0].Summary, "the store did not answer the DELETE") {
			t.Fatalf("facts = %+v", facts)
		}
		if events := objectDeleteEvents(t, h); len(events) != 1 || events[0].Result != "unverified" {
			t.Fatalf("audit = %+v", events)
		}
		if got := requestsNaming(h, before, deleteFixture); len(got) != 0 {
			t.Fatalf("the store received %q for the object after the lost DELETE, want no read", got)
		}
	})
}

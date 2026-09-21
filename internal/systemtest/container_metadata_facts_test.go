package systemtest

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swifttest"
)

// ADR 0062 requires facts at the public service boundary. Remote effects
// are witnessed by the raw-HTTP oracle, never by the adapter being tested.
func TestContainerMetadataFactsMock(t *testing.T) {
	testContainerMetadataFacts(t, findMockHarness)
}

func TestContainerMetadataFactsSwift(t *testing.T) {
	testContainerMetadataFacts(t, New)
}

func testContainerMetadataFacts(t *testing.T, harness func(*testing.T) *Harness) {
	for _, tc := range []struct {
		name, fault, refuse, outcome, errorText string
		posts                                   int
		changed, hold, switchAccount, prune     bool
	}{
		{name: "applied", outcome: "applied", posts: 1, changed: true},
		{name: "missing_post_id", fault: "missing_id", outcome: "applied", posts: 1, changed: true},
		{name: "intent_refused", refuse: "intent", errorText: "refuse intent"},
		{name: "outcome_refused", refuse: "outcome", outcome: "intent", errorText: "container metadata write residue", posts: 1, changed: true},
		{name: "audit_refused", refuse: "audit", outcome: "intent", errorText: "container metadata write residue", posts: 1, changed: true},
		{name: "conflict", fault: "conflict", outcome: "conflict"},
		{name: "conflict_record_refused", fault: "conflict", refuse: "outcome", outcome: "intent", errorText: "refuse outcome"},
		{name: "invalid_metadata", fault: "invalid", outcome: "failed", errorText: "metadata"},
		{name: "public_acl_refused", fault: "public", errorText: "ackPublic"},
		{name: "preflight_refused", fault: "preflight", outcome: "failed", errorText: "versioning mode could not be established"},
		{name: "modern_versioning_refused", fault: "modern", outcome: "failed", errorText: "modern object versioning"},
		{name: "received_post_refusal", fault: "refused", outcome: "failed", errorText: "403", posts: 1},
		{name: "verify_mismatch", fault: "mismatch", outcome: "verify_mismatch", posts: 1, changed: true, hold: true},
		{name: "lost_post_response", fault: "lost", outcome: "unverified", errorText: "container metadata write unverified", posts: 1, changed: true, hold: true},
		{name: "lost_post_record_refused", fault: "lost", refuse: "outcome", outcome: "intent", errorText: "container metadata write residue", posts: 1, changed: true, hold: true},
		{name: "verify_read_refused", fault: "verify", outcome: "unverified", errorText: "container metadata write unverified", posts: 1, changed: true},
		{name: "captured_account", outcome: "applied", posts: 1, changed: true, hold: true, switchAccount: true},
		{name: "audit_retention", outcome: "applied", posts: 1, changed: true, prune: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := harness(t)
			if h.Secondary == nil {
				t.Skip("container facts require two owned accounts")
			}
			container := h.Container(h.Primary, "facts")
			h.Seed(h.Primary, container, "collateral", []byte("keep these exact bytes"), map[string]string{"X-Object-Meta-Keep": "yes"})
			before := h.Snapshot(h.Primary, container)
			secondaryBefore := h.Snapshot(h.Secondary, container)
			edit := app.ContainerEditDTO{Meta: map[string]string{"Recorded": "yes"}}
			fingerprint := ""
			switch tc.fault {
			case "conflict":
				fingerprint = "stale working copy"
			case "invalid":
				edit.Meta = map[string]string{"Bad\r\nKey": "no"}
			case "public":
				edit.ReadACL = ".r:*"
			}
			if tc.switchAccount {
				if err := evidence.NewStore(h.database).Record(t.Context(), h.Primary.BookmarkID, evidence.Observation{
					Container: container, Confidence: evidence.Confirmed, Reason: evidence.ObservedDLO, SourceObject: "source/manifest",
				}); err != nil {
					t.Fatal(err)
				}
			}
			var barrier *swifttest.HTTPResponseBarrier
			if tc.hold {
				storage, err := url.Parse(h.Primary.storageURL)
				if err != nil {
					t.Fatal(err)
				}
				barrier, err = swifttest.NewHTTPResponseBarrier(swifttest.HTTPResponseSelector{
					Operation: "container facts", Method: http.MethodPost,
					EscapedPath: storage.EscapedPath() + "/" + container, Occurrence: 1,
				}, tc.fault == "lost", 10*time.Second)
				if err != nil {
					t.Fatal(err)
				}
			}
			trace := installContainerFactProxy(t, h, container, tc.fault, barrier)
			refuseContainerFact(t, h, tc.refuse)
			type reply struct {
				dto app.MutationResultDTO
				err error
			}
			done := make(chan reply, 1)
			go func() {
				dto, err := h.Mutate.UpdateContainer(t.Context(), container, fingerprint, edit)
				done <- reply{dto, err}
			}()
			if barrier != nil {
				event, err := barrier.Wait(t.Context())
				if err != nil || event.StatusCode/100 != 2 {
					t.Fatalf("held POST = %+v, %v", event, err)
				}
				// Establish the write effect before deliberately losing its reply.
				if got := h.Snapshot(h.Primary, container).Containers[container].Meta["Recorded"]; got != "yes" {
					t.Fatalf("held POST did not change remote metadata: %q", got)
				}
				if tc.fault == "mismatch" {
					competingSave(t, h.Primary, "/"+container, map[string]string{"X-Container-Meta-Other": "editor"})
				}
				if tc.switchAccount {
					if _, err := h.Browse.Connect(t.Context(), h.Secondary.BookmarkID); err != nil {
						t.Fatal(err)
					}
				}
				if err := barrier.Release(); err != nil {
					t.Fatal(err)
				}
			}
			var got reply
			select {
			case got = <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("container update did not return")
			}
			if barrier != nil && barrier.Err() != nil {
				t.Fatalf("response barrier failed: %v", barrier.Err())
			}
			if tc.errorText == "" {
				if got.err != nil || got.dto.Outcome != tc.outcome {
					t.Errorf("result = %+v, %v; want %s", got.dto, got.err, tc.outcome)
				}
			} else if got.err == nil || !strings.Contains(got.err.Error(), tc.errorText) || got.dto.Outcome != "" {
				t.Errorf("result = %+v, %v; want empty DTO and %q", got.dto, got.err, tc.errorText)
			}
			if tc.posts == 0 && got.err != nil && strings.Contains(got.err.Error(), "residue") {
				t.Errorf("pre-POST error claims residue: %v", got.err)
			}
			posts, observedIDs := trace.snapshot()
			if tc.fault == "missing_id" && len(observedIDs) != 2 {
				t.Errorf("missing POST ID fixture observed %v; want two HEAD IDs", observedIDs)
			}
			if got.err == nil && !reflect.DeepEqual(got.dto.TransIDs, observedIDs) {
				t.Errorf("returned IDs = %v; observed %v", got.dto.TransIDs, observedIDs)
			}
			if posts != tc.posts {
				t.Errorf("POST count = %d, want %d", posts, tc.posts)
			}
			if tc.changed {
				before.Containers[container].Meta["Recorded"] = "yes"
			}
			if tc.fault == "mismatch" {
				before.Containers[container].Meta["Other"] = "editor"
				if got.dto.Mismatch == "" || got.dto.ContainerAfter == nil || got.dto.ContainerAfter.Meta["Other"] != "editor" {
					t.Errorf("mismatch lost observed state: %+v", got.dto)
				}
			}
			h.AssertNoCollateral(before, h.Snapshot(h.Primary, container))
			h.AssertNoCollateral(secondaryBefore, h.Snapshot(h.Secondary, container))
			if tc.switchAccount && (got.dto.ContainerAfter == nil || got.dto.ContainerAfter.Class != string(evidence.ClassConfirmed)) {
				t.Errorf("reply classification lost captured account: %+v", got.dto.ContainerAfter)
			}
			facts, err := mutationfacts.NewStore(h.database).All(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			wantFacts := 1
			if tc.outcome == "" {
				wantFacts = 0
			}
			if len(facts) != wantFacts {
				t.Errorf("durable facts = %+v; want %d", facts, wantFacts)
			}
			events, err := audit.NewStore(h.database).List(t.Context(), audit.Query{OperationPrefix: "container.update"})
			if err != nil {
				t.Fatal(err)
			}
			wantEvents := 1
			if tc.outcome == "" || tc.outcome == "intent" {
				wantEvents = 0
			}
			if len(events) != wantEvents {
				t.Errorf("audit events = %+v; want %d", events, wantEvents)
			}
			if len(events) == 1 && (events[0].BookmarkID != h.Primary.BookmarkID || string(events[0].Result) != tc.outcome || !reflect.DeepEqual(events[0].TransIDs, observedIDs)) {
				t.Errorf("audit = %+v; want captured account, %s, IDs %v", events[0], tc.outcome, observedIDs)
			}
			if tc.prune {
				removed, err := audit.NewStore(h.database).Prune(t.Context(), time.Now().Add(time.Hour))
				if err != nil || removed == 0 {
					t.Fatalf("audit prune = %d, %v", removed, err)
				}
			}
			// Close the writer handle, then look up the same identity in a new
			// database connection; this is ordinary reopen, not crash proof.
			if err := h.database.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := db.Open(t.Context(), h.statePath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := reopened.Close(); err != nil {
					t.Error(err)
				}
			})
			store := mutationfacts.NewStore(reopened)
			if len(facts) == 1 {
				fact, err := store.Lookup(t.Context(), facts[0].Identity)
				if err != nil {
					t.Fatal(err)
				}
				if fact.BookmarkID != h.Primary.BookmarkID || fact.Operation != "container.update" || fact.Resource != container || string(fact.Outcome) != tc.outcome || !reflect.DeepEqual(fact, facts[0]) {
					t.Errorf("reopened identity = %+v, original = %+v", fact, facts[0])
				}
				if tc.outcome == "intent" {
					if !fact.DecidedAt.IsZero() || len(fact.TransIDs) != 0 {
						t.Errorf("record refusal invented a terminal fact: %+v", fact)
					}
				} else if fact.DecidedAt.IsZero() || !reflect.DeepEqual(fact.TransIDs, observedIDs) {
					t.Errorf("fact lost decision or observed IDs: %+v; IDs %v", fact, observedIDs)
				}
				if strings.Contains(tc.errorText, "container metadata write") && (got.err == nil || !strings.Contains(got.err.Error(), fact.Identity)) {
					t.Errorf("uncertain write error lost identity %s: %v", fact.Identity, got.err)
				}
			} else if reopenedFacts, err := store.All(t.Context()); err != nil || len(reopenedFacts) != wantFacts {
				t.Errorf("reopened facts = %+v, %v; want %d", reopenedFacts, err, wantFacts)
			}
		})
	}
}

func refuseContainerFact(t *testing.T, h *Harness, phase string) {
	t.Helper()
	var statement string
	switch phase {
	case "":
		return
	case "intent":
		statement = `CREATE TRIGGER refuse_container_fact BEFORE INSERT ON mutation_facts BEGIN SELECT RAISE(ABORT, 'refuse intent'); END`
	case "outcome":
		statement = `CREATE TRIGGER refuse_container_fact BEFORE UPDATE ON mutation_facts BEGIN SELECT RAISE(ABORT, 'refuse outcome'); END`
	case "audit":
		statement = `CREATE TRIGGER refuse_container_fact BEFORE INSERT ON audit_events WHEN NEW.operation='container.update' BEGIN SELECT RAISE(ABORT, 'refuse audit'); END`
	default:
		t.Fatalf("unknown fact refusal %q", phase)
	}
	if err := h.database.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), statement)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

type containerFactTrace struct {
	mu           sync.Mutex
	heads, posts int
	ids          []string
}

func (p *containerFactTrace) snapshot() (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.posts, append([]string(nil), p.ids...)
}

func installContainerFactProxy(t *testing.T, h *Harness, container, fault string, barrier *swifttest.HTTPResponseBarrier) *containerFactTrace {
	t.Helper()
	a := h.Primary
	storage, err := url.Parse(a.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	path := storage.Path + "/" + container
	trace := &containerFactTrace{}
	observe := func(header http.Header) {
		id := header.Get("X-Trans-Id")
		if id == "" {
			id = header.Get("X-Openstack-Request-Id")
		}
		if id == "" {
			return
		}
		trace.mu.Lock()
		trace.ids = append(trace.ids, id)
		trace.mu.Unlock()
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 10 * time.Second
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	proxy.ModifyResponse = func(r *http.Response) error {
		if fault == "missing_id" && r.Request.URL.Path == path && r.Request.Method == http.MethodPost {
			r.Header.Del("X-Trans-Id")
			r.Header.Del("X-Openstack-Request-Id")
		}
		// The mock has no modern-versioning implementation. Inject the
		// preflight observation here; TestVersioningPreflight separately
		// exercises an actually versioned container on real Swift.
		if fault == "modern" && r.Request.URL.Path == path && r.Request.Method == http.MethodHead {
			r.Header.Set("X-Versions-Enabled", "true")
		}
		if r.Request.URL.Path == path && !(fault == "lost" && r.Request.Method == http.MethodPost) {
			observe(r.Header)
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("container fact fixture proxy: %v", err)
		http.Error(w, "fixture failure", http.StatusBadGateway)
	}
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == path {
			trace.mu.Lock()
			if r.Method == http.MethodPost {
				trace.posts++
			}
			if r.Method == http.MethodHead {
				trace.heads++
			}
			head := trace.heads
			trace.mu.Unlock()
			status := 0
			if r.Method == http.MethodHead && ((fault == "preflight" && head == 1) || (fault == "verify" && head == 2)) {
				status = http.StatusServiceUnavailable
			}
			if fault == "refused" && r.Method == http.MethodPost {
				status = http.StatusForbidden
			}
			if status != 0 {
				w.Header().Set("X-Trans-Id", "tx-container-facts-"+fault)
				observe(w.Header())
				w.WriteHeader(status)
				return
			}
		}
		proxy.ServeHTTP(w, r)
	})
	if barrier != nil {
		handler = barrier.Wrap(handler)
	}
	front := httptest.NewServer(handler)
	t.Cleanup(front.Close)
	if barrier != nil {
		t.Cleanup(barrier.Close)
	}
	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range bookmarks {
		if bookmark.ID == a.BookmarkID {
			bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
			if _, err := h.Bookmarks.Save(t.Context(), bookmark, a.key); err != nil {
				t.Fatal(err)
			}
			if _, err := h.Browse.Connect(t.Context(), a.BookmarkID); err != nil {
				t.Fatal(err)
			}
			return trace
		}
	}
	t.Fatal("container fact fixture bookmark not found")
	return nil
}

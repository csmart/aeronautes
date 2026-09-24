package systemtest

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/mutationfacts"
	"github.com/csmart/aeronautes/internal/swifttest"
)

func TestCreationFactsMock(t *testing.T)  { creationFactsCases(t, findMockHarness) }
func TestCreationFactsSwift(t *testing.T) { creationFactsCases(t, New) }

// ADR 0077: container and marker creation are durable facts at the public
// service boundary. Remote effects are witnessed by the raw-HTTP oracle;
// the ledger and the audit trail are read back through a reopened
// database.
func creationFactsCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, tc := range []struct {
		name, refuse, outcome, errorText                                  string
		marker, exists, lost, hold, switchAccount, created, verifyRefused bool
	}{
		{name: "container_applied", outcome: "applied", created: true},
		{name: "container_intent_refused", refuse: "intent", errorText: "refuse intent"},
		{name: "container_outcome_refused", refuse: "outcome", outcome: "intent", errorText: "container creation residue", created: true},
		{name: "container_audit_refused", refuse: "audit", outcome: "intent", errorText: "container creation residue", created: true},
		{name: "container_exists_refused", exists: true, outcome: "failed", errorText: "already exists"},
		{name: "container_lost_put_reply", lost: true, hold: true, outcome: "unverified", errorText: "container creation unverified", created: true},
		{name: "container_switch_after_put", hold: true, switchAccount: true, outcome: "applied", created: true},
		{name: "container_verify_read_refused", verifyRefused: true, outcome: "unverified", errorText: "container creation unverified", created: true},
		{name: "marker_applied", marker: true, outcome: "applied", created: true},
		{name: "marker_exists_refused", marker: true, exists: true, outcome: "failed", errorText: "already exists"},
		{name: "marker_verify_read_refused", marker: true, verifyRefused: true, outcome: "unverified", errorText: "marker creation unverified", created: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := makeHarness(t)
			if h.Secondary == nil {
				t.Skip("two owned Swift accounts are required")
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			storage, err := url.Parse(h.Primary.storageURL)
			if err != nil {
				t.Fatal(err)
			}
			var container, resource, operation, path string
			if tc.marker {
				container = h.Container(h.Primary, "marker-facts")
				if tc.exists {
					h.Seed(h.Primary, container, "folder/", nil, map[string]string{"Content-Type": "application/directory"})
				}
				resource, operation = container+"/folder/", "marker.create"
				path = storage.EscapedPath() + "/" + container + "/folder/"
			} else {
				if tc.exists {
					container = h.Container(h.Primary, "creation-facts")
				} else {
					container = fmt.Sprintf("%s-creation-facts-%d", h.ns, atomic.AddInt64(&h.counter, 1))
					t.Cleanup(func() {
						if _, ok := h.SnapshotNamespace(h.Primary).Containers[container]; ok {
							h.purge(h.Primary, container)
						}
					})
				}
				resource, operation = container, "container.create"
				path = storage.EscapedPath() + "/" + container
			}
			secondaryBefore := h.SnapshotNamespace(h.Secondary)
			var barrier *swifttest.HTTPResponseBarrier
			if tc.hold {
				selector := swifttest.HTTPResponseSelector{Operation: t.Name(), Method: http.MethodPut, EscapedPath: path, Occurrence: 1}
				if tc.marker {
					// The marker PUT carries an Expect header the barrier cannot
					// hold; its verify read, the second HEAD of the name, is the
					// reply that is lost instead.
					selector.Method, selector.Occurrence = http.MethodHead, 2
				}
				barrier, err = swifttest.NewHTTPResponseBarrier(selector, tc.lost, 30*time.Second)
				if err != nil {
					t.Fatal(err)
				}
				metadataWindowProxy(t, h, h.Primary, barrier)
			} else if tc.verifyRefused {
				// The confirming read after the PUT is the second HEAD of the
				// name; the store answers it with an error, so the creation
				// landed but cannot be confirmed.
				refuseNthHeadProxy(t, h, h.Primary, path, 2)
			} else if _, err := h.Browse.Connect(ctx, h.Primary.BookmarkID); err != nil {
				t.Fatal(err)
			}
			refuseCreationFact(t, h, tc.refuse, operation)
			done := make(chan error, 1)
			go func() {
				if tc.marker {
					_, err := h.Mutate.CreateMarker(ctx, container, "folder/")
					done <- err
					return
				}
				_, err := h.Mutate.CreateContainer(ctx, app.ContainerCreateDTO{Name: container})
				done <- err
			}()
			if barrier != nil {
				event, err := barrier.Wait(ctx)
				if err != nil || event.StatusCode/100 != 2 {
					t.Fatalf("held reply = %+v, %v", event, err)
				}
				if tc.switchAccount {
					if _, err := h.Browse.Connect(ctx, h.Secondary.BookmarkID); err != nil {
						t.Fatal(err)
					}
				}
				if err := barrier.Release(); err != nil {
					t.Fatal(err)
				}
			}
			var got error
			select {
			case got = <-done:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if barrier != nil {
				if err := barrier.Err(); err != nil {
					t.Fatal(err)
				}
			}
			if tc.errorText == "" {
				if got != nil {
					t.Fatalf("create = %v, want success", got)
				}
			} else if got == nil || !strings.Contains(got.Error(), tc.errorText) {
				t.Fatalf("create = %v, want %q", got, tc.errorText)
			}

			after := h.SnapshotNamespace(h.Primary)
			var created bool
			if tc.marker {
				_, created = after.Containers[container].Objects["folder/"]
			} else {
				_, created = after.Containers[container]
			}
			if created != tc.created && !tc.exists {
				t.Fatalf("remote %s created = %v, want %v", resource, created, tc.created)
			}
			h.AssertNoCollateral(secondaryBefore, h.SnapshotNamespace(h.Secondary))

			facts, err := mutationfacts.NewStore(h.database).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			events, err := audit.NewStore(h.database).List(ctx, audit.Query{OperationPrefix: operation})
			if err != nil {
				t.Fatal(err)
			}
			if tc.refuse == "intent" {
				if len(facts) != 0 || len(events) != 0 {
					t.Fatalf("a refused intent left records: facts=%+v audit=%+v", facts, events)
				}
				return
			}
			if len(facts) != 1 {
				t.Fatalf("facts = %+v, want one", facts)
			}
			fact := facts[0]
			if fact.BookmarkID != h.Primary.BookmarkID || fact.Operation != operation || fact.Resource != resource || string(fact.Outcome) != tc.outcome {
				t.Fatalf("fact = %+v, want %s %s %s under %s", fact, operation, resource, tc.outcome, h.Primary.BookmarkID)
			}
			if tc.outcome == "intent" {
				if len(events) != 0 || !fact.DecidedAt.IsZero() {
					t.Fatalf("a refused record left a terminal fact or an audit event: fact=%+v audit=%+v", fact, events)
				}
			} else {
				if len(events) != 1 || events[0].BookmarkID != h.Primary.BookmarkID || events[0].Resource != resource || string(events[0].Result) != tc.outcome {
					t.Fatalf("audit for %s = %+v, want one %s event for %s under %s", operation, events, tc.outcome, resource, h.Primary.BookmarkID)
				}
				if fact.DecidedAt.IsZero() {
					t.Fatalf("a terminal fact has no decision time: %+v", fact)
				}
			}
			if tc.outcome == "unverified" && !strings.Contains(got.Error(), fact.Identity) {
				t.Fatalf("an uncertain creation lost its identity %s: %v", fact.Identity, got)
			}
			// Reopen: the same identity, unchanged, in a new connection.
			if err := h.database.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := db.Open(ctx, h.statePath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			again, err := mutationfacts.NewStore(reopened).Lookup(ctx, fact.Identity)
			if err != nil || again.Outcome != fact.Outcome || again.BookmarkID != fact.BookmarkID || again.Resource != fact.Resource {
				t.Fatalf("reopened identity = %+v, %v; original %+v", again, err, fact)
			}
		})
	}
}

// refuseCreationFact makes the next intent, outcome or audit write of a
// creation fail, as a full disk or a closed database would.
func refuseCreationFact(t *testing.T, h *Harness, phase, operation string) {
	t.Helper()
	var statement string
	switch phase {
	case "":
		return
	case "intent":
		statement = `CREATE TRIGGER refuse_creation_fact BEFORE INSERT ON mutation_facts BEGIN SELECT RAISE(ABORT, 'refuse intent'); END`
	case "outcome":
		statement = `CREATE TRIGGER refuse_creation_fact BEFORE UPDATE ON mutation_facts BEGIN SELECT RAISE(ABORT, 'refuse outcome'); END`
	case "audit":
		statement = fmt.Sprintf(`CREATE TRIGGER refuse_creation_fact BEFORE INSERT ON audit_events WHEN NEW.operation='%s' BEGIN SELECT RAISE(ABORT, 'refuse audit'); END`, operation)
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

// refuseNthHeadProxy fronts the account's storage URL with a proxy that
// answers the nth HEAD of path with 503 and forwards everything else, then
// re-points the bookmark at it and reconnects, as metadataWindowProxy does.
func refuseNthHeadProxy(t *testing.T, h *Harness, account *Account, path string, n int) {
	t.Helper()
	storage, err := url.Parse(account.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 20 * time.Second
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("verify refusal proxy: %v", err)
		http.Error(w, "fixture proxy failed", http.StatusBadGateway)
	}
	var heads atomic.Int64
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead && r.URL.EscapedPath() == path && heads.Add(1) == int64(n) {
			http.Error(w, "fixture refuses the verify read", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	list, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range list {
		if bookmark.ID != account.BookmarkID {
			continue
		}
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, account.key); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Browse.Connect(t.Context(), account.BookmarkID); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("bookmark %s not found", account.BookmarkID)
}

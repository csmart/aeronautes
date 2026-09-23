package systemtest

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/audit"
	"github.com/csmart/aeronautes/internal/swifttest"
)

func TestCreationAccountMock(t *testing.T)  { creationAccountCases(t, findMockHarness) }
func TestCreationAccountSwift(t *testing.T) { creationAccountCases(t, New) }

// creationAccountCases proves that container and marker creation record
// and describe the account that admitted them, not whichever connection
// is active when the record is written (ADR 0065's relabel window for the
// creation paths): the fixture holds the creating PUT's reply, the
// browser connects to the other account meanwhile, and the audit event
// must still name the creating account, with nothing changed in the
// other one.
func creationAccountCases(t *testing.T, makeHarness func(*testing.T) *Harness) {
	for _, tc := range []struct {
		name      string
		marker    bool
		operation string
	}{
		{name: "container_switch_after_put", operation: "container.create"},
		{name: "marker_switch_after_put", marker: true, operation: "marker.create"},
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
			var container, resource, path string
			if tc.marker {
				container = h.Container(h.Primary, "marker-account")
				// The same container name in the other account, so a
				// misrouted marker would land somewhere rather than fail.
				h.rawPut(h.Secondary, "/"+container, nil, nil)
				t.Cleanup(func() { h.purge(h.Secondary, container) })
				resource = container + "/folder/"
				path = storage.EscapedPath() + "/" + container + "/folder/"
			} else {
				container = fmt.Sprintf("%s-create-account-%d", h.ns, atomic.AddInt64(&h.counter, 1))
				t.Cleanup(func() { h.purge(h.Primary, container) })
				resource = container
				path = storage.EscapedPath() + "/" + container
			}
			beforePrimary, beforeSecondary := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
			// The container PUT's reply is held. The marker PUT carries an
			// Expect header the barrier cannot hold, so its verify read, the
			// second HEAD of the marker name, is held instead: it follows
			// the PUT's reply and precedes the record, the same window.
			selector := swifttest.HTTPResponseSelector{Operation: t.Name(), Method: http.MethodPut, EscapedPath: path, Occurrence: 1}
			if tc.marker {
				selector.Method, selector.Occurrence = http.MethodHead, 2
			}
			barrier, err := swifttest.NewHTTPResponseBarrier(selector, false, 30*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			metadataWindowProxy(t, h, h.Primary, barrier)
			done := make(chan error, 1)
			go func() {
				if tc.marker {
					_, err := h.Mutate.CreateMarker(ctx, container, "folder/")
					done <- err
					return
				}
				detail, err := h.Mutate.CreateContainer(ctx, app.ContainerCreateDTO{Name: container})
				if err == nil && detail.Name != container {
					err = fmt.Errorf("created detail names %q", detail.Name)
				}
				done <- err
			}()
			event, err := barrier.Wait(ctx)
			if err != nil || event.StatusCode/100 != 2 {
				t.Fatalf("selected response = %+v, err = %v", event, err)
			}
			// The creating PUT has been answered but not yet recorded; the
			// browser moves to the other account before the record lands.
			if _, err := h.Browse.Connect(ctx, h.Secondary.BookmarkID); err != nil {
				t.Fatal(err)
			}
			if err := barrier.Release(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("create = %v", err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if err := barrier.Err(); err != nil {
				t.Fatal(err)
			}
			events, err := audit.NewStore(h.database).List(ctx, audit.Query{OperationPrefix: tc.operation})
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 {
				t.Fatalf("audit for %s = %+v, want one event", tc.operation, events)
			}
			if events[0].BookmarkID != h.Primary.BookmarkID || events[0].Resource != resource || events[0].Result != audit.ResultApplied {
				t.Fatalf("the record relabelled or misdescribed the creation: %+v, want %s under %s", events[0], resource, h.Primary.BookmarkID)
			}
			afterPrimary, afterSecondary := h.SnapshotNamespace(h.Primary), h.SnapshotNamespace(h.Secondary)
			h.AssertNoCollateral(beforeSecondary, afterSecondary)
			h.AssertNoCollateral(beforePrimary, afterPrimary, resource)
			if _, ok := afterPrimary.Containers[container]; !ok {
				t.Fatalf("the creating account lacks %s after the create", container)
			}
			if tc.marker {
				if _, ok := afterPrimary.Containers[container].Objects["folder/"]; !ok {
					t.Fatalf("the creating account lacks the marker: %+v", afterPrimary.Containers[container])
				}
			}
		})
	}
}

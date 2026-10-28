package systemtest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// The namespace invariant must not rest on the preflight. ADR 0044 says
// no delete this route issues escapes the canonical namespace, and that
// this holds regardless of what a listing showed, because listings are
// advisory and eventually consistent (K-CON-001).
//
// A preflight that observed nothing outside the namespace therefore has
// to be separated from a sweep that acts on the namespace. These two
// cases do that by intervening between the two, which is the only way
// the sweep's own argument becomes observable.

type segmentFront struct {
	mu       sync.Mutex
	listings int
	deletes  []string
	failNext bool
}

// segmentFrontFor puts a recording proxy in front of the store for the
// primary bookmark. afterDelete runs once when the first DELETE arrives,
// which the route issues only after consuming the whole preflight, so it
// is a point strictly between preflight and sweep. afterListing runs
// once when the first listing of the segment container has been proxied,
// which is inside the preflight and therefore before the identity
// recheck.
func segmentFrontFor(t *testing.T, h *Harness, segments string,
	afterDelete, afterListing func(), failListing bool) *segmentFront {
	t.Helper()
	storage, err := url.Parse(h.Primary.storageURL)
	if err != nil {
		t.Fatal(err)
	}
	target := *storage
	target.Path, target.RawPath, target.RawQuery = "", "", ""
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	transport.ResponseHeaderTimeout = 10 * time.Second
	t.Cleanup(transport.CloseIdleConnections)
	proxy := httputil.NewSingleHostReverseProxy(&target)
	proxy.Transport = transport

	f := &segmentFront{failNext: failListing}
	listingPath := storage.EscapedPath() + "/" + url.PathEscape(segments)
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isListing := r.Method == http.MethodGet && r.URL.EscapedPath() == listingPath
		// A sweep may delete per object or through one bulk POST, so
		// both carry names this test must see; watching DELETE alone
		// would miss every name in a bulk body.
		if r.Method == http.MethodPost && r.URL.RawQuery == "bulk-delete" {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err == nil {
				r.Body = io.NopCloser(bytes.NewReader(body))
				f.mu.Lock()
				for _, name := range strings.Split(string(body), "\n") {
					if name = strings.TrimSpace(name); name != "" {
						f.deletes = append(f.deletes, name)
					}
				}
				f.mu.Unlock()
			}
		}
		if r.Method == http.MethodDelete {
			f.mu.Lock()
			f.deletes = append(f.deletes, r.URL.EscapedPath())
			first := len(f.deletes) == 1
			f.mu.Unlock()
			// The first DELETE is the manifest, which the route issues
			// only after the preflight has been consumed in full. Acting
			// here is therefore unambiguously between the two, where a
			// page boundary inside the listing is not.
			if first && afterDelete != nil {
				afterDelete()
			}
		}
		if isListing {
			f.mu.Lock()
			f.listings++
			first := f.listings == 1
			fail := f.failNext
			f.mu.Unlock()
			if fail {
				// A listing the caller cannot complete. The route must
				// refuse rather than read this as "nothing outside".
				http.Error(w, "listing unavailable", http.StatusServiceUnavailable)
				return
			}
			proxy.ServeHTTP(w, r)
			if first && afterListing != nil {
				afterListing()
			}
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)

	bookmarks, err := h.Bookmarks.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, bookmark := range bookmarks {
		if bookmark.ID != h.Primary.BookmarkID {
			continue
		}
		bookmark.StorageURLOverride = front.URL + storage.EscapedPath()
		if _, err := h.Bookmarks.Save(t.Context(), bookmark, h.Primary.key); err != nil {
			t.Fatal(err)
		}
		if _, err := h.Browse.Connect(t.Context(), bookmark.ID); err != nil {
			t.Fatal(err)
		}
		return f
	}
	t.Fatal("primary bookmark not found")
	return nil
}

// A name appears outside the namespace after the preflight has already
// passed, which is exactly the advisory-listing case the decision says
// it cannot detect. The refusal legitimately does not fire. The
// containment still must: the sweep acts on the namespace, so the late
// name survives as an orphan rather than being destroyed.
func TestSegmentSweepContainsAStaleListing(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	segments, aliases := seedSubject(t, h, "job1")

	// Review the graph on the real store before the proxy is installed, so
	// the request carries the reviewed graph the dialog would (ADR 0095
	// decision 8) and the proxy only sees the deletion that follows.
	req := subjectRequest(h, aliases)
	req.Reviewed = h.ReviewSegmentMembership(ctx, aliases, "subject.bin")

	var once sync.Once
	front := segmentFrontFor(t, h, segments, func() {
		once.Do(func() {
			// Outside the namespace, but read by the raw prefix.
			h.Seed(h.Primary, segments, "job1late/00000000", []byte("appeared after the preflight"), nil)
		})
	}, nil, false)

	rec := h.DeleteManifestWithSegments(ctx, req)
	if rec.Err() != nil {
		t.Fatalf("the preflight observed nothing outside, so the delete should proceed: %v", rec.Err())
	}
	if job := h.AwaitJob(rec.JobID()); job.State != "complete" {
		t.Fatalf("the sweep did not complete: state %q, error %q", job.State, job.Error)
	}

	snapshot := h.SnapshotNamespace(h.Primary)
	if _, present := snapshot.Containers[segments].Objects["job1late/00000000"]; !present {
		t.Error("a name outside the namespace that appeared after the preflight was destroyed; " +
			"the containment must not depend on what the listing observed")
	}
	for _, gone := range []string{"job1/00000000", "job1/00000001"} {
		if _, present := snapshot.Containers[segments].Objects[gone]; present {
			t.Errorf("the subject's own segment %q survived; the fixture is wrong", gone)
		}
	}
	front.mu.Lock()
	defer front.mu.Unlock()
	for _, path := range front.deletes {
		if strings.Contains(path, "job1late") {
			t.Errorf("a delete was issued outside the namespace: %s", path)
		}
	}
}

// A preflight listing that fails must refuse. Reading a failed listing
// as "no outside names" would authorise the deletion it should stop.
func TestSegmentSweepRefusesAnUnreadableListing(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	segments, aliases := seedSubject(t, h, "job1")
	// Review on the real store before the failing proxy is installed.
	req := subjectRequest(h, aliases)
	req.Reviewed = h.ReviewSegmentMembership(ctx, aliases, "subject.bin")
	front := segmentFrontFor(t, h, segments, nil, nil, true)

	before := h.SnapshotNamespace(h.Primary)
	rec := h.DeleteManifestWithSegments(ctx, req)
	if rec.Err() == nil {
		t.Fatal("a segment deletion whose preflight listing failed was allowed; want a refusal")
	}
	if !strings.Contains(rec.Err().Error(), "before deleting segments") {
		t.Errorf("refusal %q does not say the listing is why", rec.Err())
	}
	h.AssertNoCollateral(before, h.SnapshotNamespace(h.Primary))
	front.mu.Lock()
	defer front.mu.Unlock()
	if len(front.deletes) > 0 {
		t.Errorf("a refused preflight still issued %d delete(s): %v", len(front.deletes), front.deletes)
	}
}

// The preflight issues a listing, so it must not sit between the final
// identity recheck and the DELETE: that would widen the window the
// recheck exists to close. Replacing the selected manifest while the
// preflight is in flight must therefore refuse, with nothing removed.
func TestSegmentDeleteRefusesAnIdentityMovedDuringThePreflight(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	segments, aliases := seedSubject(t, h, "job1")

	// Review the original graph on the real store before the proxy moves
	// the selected name; the request carries that graph, and the job's own
	// recheck (ADR 0095 decision 8) is what must catch the move.
	req := subjectRequest(h, aliases)
	req.Reviewed = h.ReviewSegmentMembership(ctx, aliases, "subject.bin")

	var once sync.Once
	front := segmentFrontFor(t, h, segments, nil, func() {
		once.Do(func() {
			// The selected name now holds a different object entirely.
			h.Seed(h.Primary, aliases, "subject.bin", []byte("a different object"), nil)
		})
	}, false)

	before := h.SnapshotNamespace(h.Primary)
	rec := h.DeleteManifestWithSegments(ctx, req)
	if rec.Err() != nil {
		t.Fatalf("the job did not start; the reviewed graph matched at startup: %v", rec.Err())
	}
	job := h.AwaitJob(rec.JobID())
	if job.State != "failed" {
		t.Fatalf("the selected manifest was replaced during the preflight but the job did not fail: state %q, error %q", job.State, job.Error)
	}
	if !strings.Contains(job.Error, "reviewed") {
		t.Errorf("job failure %q does not name the reviewed-graph change", job.Error)
	}
	// The fixture itself rewrote the selected name, so that one object is
	// the approved change; nothing else may have moved, and in particular
	// no segment may have been removed.
	h.AssertNoCollateral(before, h.SnapshotNamespace(h.Primary), aliases+"/subject.bin")
	front.mu.Lock()
	defer front.mu.Unlock()
	if len(front.deletes) > 0 {
		t.Errorf("a refused identity change still issued %d delete(s): %v", len(front.deletes), front.deletes)
	}
}

// A preflight retaining every outside name would grow without a bound.
// It must retain a count and a small sample, and the count must be the
// true total rather than the sample size.
func TestSegmentDeletePreflightCountsEveryOutsideName(t *testing.T) {
	h := New(t)
	ctx := context.Background()
	segments, aliases := seedSubject(t, h, "job1")
	const outside = 12
	for i := 0; i < outside; i++ {
		h.Seed(h.Primary, segments, fmt.Sprintf("job1x/%08d", i), []byte("outside"), nil)
	}

	req := subjectRequest(h, aliases)
	req.Reviewed = h.ReviewSegmentMembership(ctx, aliases, "subject.bin")
	rec := h.DeleteManifestWithSegments(ctx, req)
	if rec.Err() == nil {
		t.Fatal("want a refusal")
	}
	if want := fmt.Sprintf("reads %d name(s) outside", outside); !strings.Contains(rec.Err().Error(), want) {
		t.Errorf("refusal %q does not state the true count %q; a bounded sample must not truncate the decision",
			rec.Err(), want)
	}
	if got := strings.Count(rec.Err().Error(), "job1x/"); got > 5 {
		t.Errorf("refusal listed %d names; the message sample is bounded at five", got)
	}
	if !strings.Contains(rec.Err().Error(), "and 7 more") {
		t.Errorf("refusal %q does not account for the names beyond the sample", rec.Err())
	}
}

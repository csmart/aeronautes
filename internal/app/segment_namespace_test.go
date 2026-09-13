package app

import (
	"context"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
)

// The namespace boundary a dynamic large object's segment deletion is
// authorised over (ADR 0044). Swift reads the manifest prefix as a raw
// string match, so the read set may be wider; the destructive set is
// this.
func TestDLONamespaceBoundary(t *testing.T) {
	for _, c := range []struct{ prefix, want string }{
		{"job1", "job1/"},
		{"job1/", "job1/"},
		{"a/b", "a/b/"},
		{"a/b/", "a/b/"},
	} {
		if got := dloNamespace(c.prefix); got != c.want {
			t.Errorf("dloNamespace(%q) = %q, want %q", c.prefix, got, c.want)
		}
	}
}

// A manifest naming a container and no prefix would make the namespace a
// whole container, which no manifest's authority reaches, so it refuses
// before any scope is resolved.
//
// The real store rejects this header at creation with HTTP 400, so the
// case is latent there; the mock accepts it, which is what lets the
// guard be exercised at all. It is defence in depth against a store that
// does not refuse, not a reproduced defect.
func TestSegmentScopeRefusesAWholeContainerManifest(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
	h.mock.AddContainer("media", "standard")
	h.mock.AddContainer("segments", "standard")
	seedObject(h.mock, "segments", "unrelated/important.dat", "another job's data", nil)
	h.mock.PutObject("media", "whole.bin", swifttest.Object{Manifest: "segments/"})

	req := SegmentsDeleteRequestDTO{
		BookmarkID: h.transfer.connections.ActiveBookmarkID(),
		Container:  "media", Name: "whole.bin", Force: true, Confirm: "whole.bin",
	}
	if _, err := service.CheckSegmentOwnership(ctx, req); err == nil ||
		!strings.Contains(err.Error(), "no segment prefix") {
		t.Errorf("ownership check on a whole-container manifest = %v, want a bounded-scope refusal", err)
	}
	if _, err := service.DeleteManifestWithSegments(ctx, req); err == nil ||
		!strings.Contains(err.Error(), "no segment prefix") {
		t.Fatalf("delete on a whole-container manifest = %v, want a bounded-scope refusal", err)
	}
	// The refusal removed nothing: no DELETE ever reached the store, so
	// neither the unrelated object nor the manifest itself was touched.
	for _, line := range h.mock.RequestLog() {
		if strings.HasPrefix(line, "DELETE ") {
			t.Errorf("a refused whole-container manifest still issued %q", line)
		}
	}
}

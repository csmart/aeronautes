package local

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// ADR 0072 decision 4: a download selection wider than the ceiling is
// refused with the count and the ceiling named, keeps no plan, and stops
// listing at the page that crossed the ceiling; a selection within the
// ceiling plans as before, and a zero ceiling is unbounded.
func TestPreflightDownloadCeiling(t *testing.T) {
	source, mock := session(t)
	// More than one mock listing page (10,000 names per page) so the stop
	// is visible in the request count.
	const seeded = 10_001
	for i := range seeded {
		seed(mock, "media", fmt.Sprintf("scope/object-%05d", i), "x")
	}
	seed(mock, "media", "outside", "y")
	ctx := context.Background()

	requests := mock.Requests()
	plan, err := PreflightDownloadBounded(ctx, source, "media", "scope/", t.TempDir(), 5)
	refusedRequests := mock.Requests() - requests
	if err != nil || plan == nil || !plan.Rejected() || len(plan.Items) != 0 || plan.Objects != 0 || plan.TotalBytes != 0 {
		t.Fatalf("oversize selection was not refused without a plan: %+v %v", plan, err)
	}
	if len(plan.Problems) != 1 || plan.Problems[0].Kind != "selection-ceiling" || plan.Problems[0].Name != "scope/" ||
		!strings.Contains(plan.Problems[0].Detail, "more than 5 objects") || !strings.Contains(plan.Problems[0].Detail, "6 counted") {
		t.Fatalf("refusal does not name the count and the ceiling: %+v", plan.Problems)
	}
	if refusedRequests != 1 {
		t.Fatalf("the refused listing made %d requests, want only the page that crossed the ceiling", refusedRequests)
	}

	requests = mock.Requests()
	plan, err = PreflightDownloadBounded(ctx, source, "media", "scope/", t.TempDir(), seeded)
	plannedRequests := mock.Requests() - requests
	if err != nil || plan == nil || plan.Rejected() || len(plan.Items) != seeded || plan.Objects != seeded {
		t.Fatalf("selection at the ceiling did not plan: problems=%+v items=%d err=%v", plan.Problems, len(plan.Items), err)
	}
	if plannedRequests <= refusedRequests {
		t.Fatalf("planning the whole scope made %d requests, no more than the refused %d", plannedRequests, refusedRequests)
	}

	plan, err = PreflightDownloadBounded(ctx, source, "media", "scope/", t.TempDir(), 0)
	if err != nil || plan == nil || plan.Rejected() || len(plan.Items) != seeded {
		t.Fatalf("unbounded preflight changed: %+v %v", plan.Problems, err)
	}
	if _, err := PreflightDownloadBounded(ctx, source, "media", "scope/", t.TempDir(), -1); err == nil {
		t.Fatal("a negative ceiling was accepted")
	}
}

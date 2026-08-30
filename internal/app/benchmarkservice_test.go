package app

import (
	"context"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func TestBenchmarkRunsAndFeedsAuto(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	service := NewBenchmarkService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.db)
	service.setScale(24, []int{2, 4})
	h.transfer.setRecommendationSource(service.recommendationFor)

	// Consent gate: the typed account name, exactly.
	if _, err := service.StartBenchmark(ctx, BenchmarkRequestDTO{BookmarkID: bookmark, Confirm: "AUTH_wrong"}); err == nil ||
		!strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("wrong account confirmation accepted: %v", err)
	}
	jobsBefore, _ := h.jobsSvc.List(ctx, "", false, 10)
	if len(jobsBefore) != 0 {
		t.Fatal("refused benchmark created a job")
	}

	jobID, err := service.StartBenchmark(ctx, BenchmarkRequestDTO{BookmarkID: bookmark, Confirm: "AUTH_test"})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, jobID)
	if job.State != "complete" || job.Type != "benchmark" {
		t.Fatalf("job = %+v", job)
	}
	// The measured phases and the recommendation ride the job's
	// effective-configuration document.
	for _, want := range []string{"upload", "listing", "head", "bulk-delete", "recommendation"} {
		if !strings.Contains(job.Settings, want) {
			t.Errorf("results missing %q: %s", want, job.Settings)
		}
	}
	// The temporary container is gone.
	session := h.transfer.connections.sessions[bookmark]
	for entry, err := range session.Containers(ctx, swiftcontrol.ListOptions{Prefix: "aeronautes-benchmark-"}) {
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("benchmark container survived: %+v", entry)
	}

	// The persisted recommendation exists and feeds Auto: resolution
	// prefers it over the balanced fallback.
	latest, err := service.Latest(ctx, bookmark)
	if err != nil || latest == nil {
		t.Fatalf("latest = %+v %v", latest, err)
	}
	if latest.Recommendation.Transfers == 0 || latest.Recommendation.Checkers == 0 {
		t.Fatalf("recommendation = %+v", latest.Recommendation)
	}
	resolved, err := h.transfer.effectiveSettings(CopyRequestDTO{
		SourceBookmarkID: bookmark, Profile: "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Transfers != latest.Recommendation.Transfers || resolved.Checkers != latest.Recommendation.Checkers {
		t.Fatalf("auto did not consume the recommendation: %+v vs %+v", resolved, latest.Recommendation)
	}
}

package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

func TestBenchmarkDoesNotCreditBulkFailures(t *testing.T) {
	h := newHarness(t)
	var bulkCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !r.URL.Query().Has("bulk-delete") || bulkCalls.Add(1) != 1 {
			h.mock.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		names := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
		if len(names) != 2 || !strings.HasSuffix(names[0], "/bench/000002") || !strings.HasSuffix(names[1], "/bench/000003") {
			t.Errorf("unexpected benchmark bulk scope: %q", names)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Only the accepted name reaches the real mock. A later cleanup is
		// allowed through, so the old false success also destroys the survivor.
		r.Body = io.NopCloser(strings.NewReader(names[1] + "\n"))
		reply := httptest.NewRecorder()
		h.mock.ServeHTTP(reply, r)
		if reply.Code != http.StatusOK {
			t.Errorf("accepted benchmark deletion: HTTP %d", reply.Code)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"Number Deleted": 1, "Number Not Found": 0, "Response Status": "400 Bad Request",
			"Errors": [][2]string{{names[0], "403 Forbidden"}},
		}); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	h.authURL = server.URL + "/auth/v1.0"
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	service := NewBenchmarkService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.db)
	service.setScale(4, []int{2, 4})
	id, err := service.StartBenchmark(t.Context(), BenchmarkRequestDTO{BookmarkID: bookmark, Confirm: "AUTH_test"})
	if err != nil {
		t.Fatal(err)
	}
	job := awaitTerminal(t, h, id)
	if job.State != "partial" || !strings.Contains(job.Error, "bulk-delete") || job.Counters.ObjectsCopied != 18 {
		t.Errorf("incomplete bulk phase was credited: %+v", job)
	}
	if latest, err := service.Latest(t.Context(), bookmark); err != nil || latest != nil {
		t.Errorf("incomplete benchmark published a recommendation: %+v %v", latest, err)
	}
	if got := bulkCalls.Load(); got != 1 {
		t.Errorf("benchmark retried an issued name during cleanup: %d bulk requests", got)
	}
	session, err := h.transfer.connections.SessionFor(t.Context(), bookmark)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.ObjectDetail(t.Context(), job.Destination, "bench/000002"); err != nil {
		t.Errorf("refused name did not remain for inspection: %v", err)
	}
	if _, err := session.ObjectDetail(t.Context(), job.Destination, "bench/000003"); !swiftcontrol.IsNotFound(err) {
		t.Errorf("accepted deletion did not take effect: %v", err)
	}
}

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
	session := h.transfer.connections.sessions[bookmark].session
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

package deletion

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
)

func cluster(t *testing.T, infoJSON string) (*swiftcontrol.Session, *swiftcontrol.Capabilities, *swifttest.Server) {
	t.Helper()
	mock := swifttest.New("test:tester", "testing")
	mock.InfoJSON = infoJSON
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	session, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := session.Authenticate(ctx); err != nil {
		t.Fatal(err)
	}
	capabilities, err := session.FetchCapabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return session, capabilities, mock
}

func seedMany(mock *swifttest.Server, container, prefix string, n int) {
	for i := 0; i < n; i++ {
		mock.PutObject(container, fmt.Sprintf("%sobj-%04d", prefix, i), swifttest.Object{
			Bytes: 10, ETag: "aa", Data: []byte("payload-xx"),
		})
	}
}

func TestBulkStrategyDeletesPrefix(t *testing.T) {
	session, capabilities, mock := cluster(t, "")
	ctx := context.Background()
	mock.AddContainer("media", "standard")
	seedMany(mock, "media", "doomed/", 25)
	seedMany(mock, "media", "kept/", 5)
	mock.PutObject("media", "doomed/line\nbreak", swifttest.Object{Bytes: 3, ETag: "bb", Data: []byte("abc")})

	result, err := DeletePrefix(ctx, session, capabilities, "media", "doomed/", Options{BulkSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != StrategyBulk {
		t.Errorf("strategy = %s", result.Strategy)
	}
	// 26 objects including the newline name (bulk bodies are per-segment
	// percent-encoded, so %0A rides safely).
	if result.Deleted != 26 || result.Remaining != 0 || len(result.Failures) != 0 {
		t.Fatalf("result = %+v", result)
	}
	// The neighbouring prefix survived.
	var kept int
	for entry, err := range session.Objects(ctx, "media", swiftcontrol.ObjectListOptions{Deep: true}) {
		if err != nil {
			t.Fatal(err)
		}
		if o, ok := entry.(swiftcontrol.ObjectEntry); ok {
			if !strings.HasPrefix(o.Name, "kept/") {
				t.Errorf("unexpected survivor %q", o.Name)
			}
			kept++
		}
	}
	if kept != 5 {
		t.Errorf("kept = %d", kept)
	}
}

func TestParallelFallbackWithoutCapability(t *testing.T) {
	// A cluster whose /info advertises no bulk_delete.
	session, capabilities, mock := cluster(t, `{"swift": {"version": "2.36.0", "policies": [{"name": "standard", "default": true}]}}`)
	ctx := context.Background()
	mock.AddContainer("media", "standard")
	seedMany(mock, "media", "gone/", 40)

	result, err := DeletePrefix(ctx, session, capabilities, "media", "gone/", Options{Workers: 4})
	if err != nil {
		t.Fatal(err)
	}
	if result.Strategy != StrategyParallel || result.Deleted != 40 || result.Remaining != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestSharedSegmentsSurviveManifestDeletion(t *testing.T) {
	session, capabilities, mock := cluster(t, "")
	ctx := context.Background()
	mock.AddContainer("media", "standard")
	mock.AddContainer("media_segments", "standard")

	// Two manifests share the SAME segment prefix (§28.6 acceptance):
	// one lives inside the doomed scope, the sibling outside it.
	for i := 0; i < 3; i++ {
		mock.PutObject("media_segments", fmt.Sprintf("shared/%08d", i), swifttest.Object{
			Bytes: 100, ETag: "cc", Data: []byte(strings.Repeat("x", 100)),
		})
	}
	mock.PutObject("media", "doomed/video.bin", swifttest.Object{Manifest: "media_segments/shared/"})
	mock.PutObject("media", "kept-video.bin", swifttest.Object{Manifest: "media_segments/shared/"})
	seedMany(mock, "media", "doomed/", 5)

	result, err := DeletePrefix(ctx, session, capabilities, "media", "doomed/", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 6 || result.Remaining != 0 {
		t.Fatalf("result = %+v", result)
	}
	// The sibling manifest still assembles at full logical size: the
	// shared segments were never followed.
	d, err := session.ObjectDetail(ctx, "media", "kept-video.bin")
	if err != nil {
		t.Fatal(err)
	}
	if d.ContentLength != 300 {
		t.Fatalf("sibling logical size = %d: shared segments were harmed", d.ContentLength)
	}
	// And in the parallel path too.
	mock.PutObject("media", "doomed2/video.bin", swifttest.Object{Manifest: "media_segments/shared/"})
	if _, err := DeletePrefix(ctx, session, capabilities, "media", "doomed2/", Options{ForceParallel: true}); err != nil {
		t.Fatal(err)
	}
	d, _ = session.ObjectDetail(ctx, "media", "kept-video.bin")
	if d == nil || d.ContentLength != 300 {
		t.Fatal("parallel path harmed shared segments")
	}
}

func TestPlanToDeleteRaceIsManifestOnly(t *testing.T) {
	// The §28.6 race fixture: an object is plain when the operator
	// previews the deletion, and becomes a shared-prefix DLO before the
	// delete executes. The native path must delete the manifest alone.
	session, capabilities, mock := cluster(t, "")
	ctx := context.Background()
	mock.AddContainer("media", "standard")
	mock.AddContainer("media_segments", "standard")
	mock.PutObject("media", "scope/target.bin", swifttest.Object{Bytes: 4, ETag: "dd", Data: []byte("data")})
	mock.PutObject("media_segments", "live/0", swifttest.Object{Bytes: 50, ETag: "ee", Data: []byte(strings.Repeat("y", 50))})
	mock.PutObject("media", "other-manifest.bin", swifttest.Object{Manifest: "media_segments/live/"})

	// "Preview" happened; now the race: the target converts to a DLO
	// over the shared prefix before deletion runs.
	mock.PutObject("media", "scope/target.bin", swifttest.Object{Manifest: "media_segments/live/"})

	result, err := DeletePrefix(ctx, session, capabilities, "media", "scope/", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 1 || result.Remaining != 0 {
		t.Fatalf("result = %+v", result)
	}
	d, err := session.ObjectDetail(ctx, "media", "other-manifest.bin")
	if err != nil {
		t.Fatal(err)
	}
	if d.ContentLength != 50 {
		t.Fatal("race fixture failed: the other manifest's segments were deleted")
	}
}

func TestCancellationStopsTheRun(t *testing.T) {
	session, capabilities, mock := cluster(t, "")
	mock.AddContainer("media", "standard")
	seedMany(mock, "media", "big/", 50)
	ctx, cancel := context.WithCancel(context.Background())

	var calls int
	_, err := DeletePrefix(ctx, session, capabilities, "media", "big/", Options{
		BulkSize: 5,
		OnProgress: func(deleted, failed int64) {
			calls++
			if deleted >= 10 {
				cancel()
			}
		},
	})
	if err == nil || ctx.Err() == nil {
		t.Fatalf("cancelled run returned %v", err)
	}
	// Something was deleted, not everything.
	var left int
	for entry, entryErr := range session.Objects(context.Background(), "media", swiftcontrol.ObjectListOptions{Deep: true, Prefix: "big/"}) {
		if entryErr != nil {
			t.Fatal(entryErr)
		}
		if _, ok := entry.(swiftcontrol.ObjectEntry); ok {
			left++
		}
	}
	if left == 0 || left == 50 {
		t.Errorf("left = %d, want a partial stop", left)
	}
	_ = calls
}

func TestConfigSnapshotDiff(t *testing.T) {
	before := ContainerConfig{
		Policy: "gold", Meta: map[string]string{"Team": "ops"},
		ReadACL: ".r:*", Quota: 100,
		Versioning: swiftcontrol.ContainerVersioning{Mode: swiftcontrol.VersioningOff},
	}
	same := ContainerConfig{
		Policy: "gold", Meta: map[string]string{"Team": "ops"},
		ReadACL: ".r:*", Quota: 100,
		Versioning: swiftcontrol.ContainerVersioning{Mode: swiftcontrol.VersioningOff},
	}
	if diff := before.Diff(same); diff != "" {
		t.Errorf("identical configs diff = %q", diff)
	}
	changed := same
	changed.Policy = "standard"
	changed.Meta = map[string]string{"Team": "other"}
	diff := before.Diff(changed)
	if !strings.Contains(diff, "policy") || !strings.Contains(diff, "metadata") {
		t.Errorf("diff = %q", diff)
	}
}

func TestExactFailureAccounting(t *testing.T) {
	// K-DEL-002 (review-wave G15/H-07): the failure COUNT is exact and
	// every name streams through OnFailure even past the display-sample
	// cap, and a failed bulk request accounts for its whole batch.
	session, capabilities, mock := cluster(t, "")
	ctx := context.Background()
	mock.AddContainer("media", "standard")
	seedMany(mock, "media", "doomed/", 30)
	mock.FailDeletes = true // every delete answers 500

	var streamed int
	result, err := DeletePrefix(ctx, session, capabilities, "media", "doomed/", Options{
		ForceParallel: true, Workers: 4,
		OnFailure: func(name, detail string) { streamed++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Failed != int64(streamed) || result.Failed < 30 {
		t.Fatalf("Failed=%d streamed=%d (exact, uncapped accounting expected)", result.Failed, streamed)
	}
	if int64(len(result.Failures)) > result.Failed {
		t.Fatalf("sample %d exceeds exact count %d", len(result.Failures), result.Failed)
	}
}

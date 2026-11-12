package local

import (
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// A listing that lags behind the last segment is waited out within the
// bound, and one that never catches up fails the item unresolved with
// nothing published and the staging kept (F35, ADR 0111 decisions 3 and 6).
func TestSegmentedUploadsWaitOutALaggingListing(t *testing.T) {
	for _, kind := range []string{"slo", "dlo"} {
		for _, tc := range []struct {
			name      string
			lag       int
			published bool
		}{
			{"settles", 2, true},
			{"never settles", 1 << 20, false},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				mock := segmentMock()
				mock.ListingLag = tc.lag
				s := servedSession(t, mock)
				path := writeFile(t, t.TempDir(), "object", "0123456789")
				preflight, err := PreflightUpload(t.Context(), s, "media", "live/", []string{path})
				if err != nil || preflight.Rejected() {
					t.Fatalf("preflight: %+v %v", preflight, err)
				}
				preflight.LargeObjects, preflight.ChunkBytes = kind, 4
				report, err := Upload(t.Context(), s, &segmentingEngine{session: s, chunk: 4}, "aeronautes_dst:media", "media", "live/", "job1", preflight, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				live, liveErr := s.ObjectDetail(t.Context(), "media", "live/object")
				if !tc.published {
					if len(report.Failures) != 1 || !swiftcontrol.IsNotFound(liveErr) {
						t.Fatalf("a listing that never caught up published: %+v live %v", report, liveErr)
					}
					if _, err := s.ObjectDetail(t.Context(), "media", ".aeronautes/stage/job1/batch/object"); err != nil {
						t.Errorf("the staging of a failed item was not kept: %v", err)
					}
					return
				}
				if len(report.Failures) != 0 || liveErr != nil || string(live.Class) != kind {
					t.Fatalf("upload under a lagging listing: %+v live %+v %v", report, live, liveErr)
				}
				if got := readLive(t, s, "media", "live/object"); got != "0123456789" {
					t.Errorf("live content = %q", got)
				}
			})
		}
	}
}

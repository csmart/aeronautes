package local

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

// uploadTwoSegmented uploads two files larger than a 4-byte chunk in one
// job with a static plan and returns what the check hook was told, in
// order, with whether either live name existed when it was told.
func uploadTwoSegmented(t *testing.T, s *swiftcontrol.Session, known bool, hook func(bool) error) (*Report, []bool, bool) {
	t.Helper()
	dir := t.TempDir()
	paths := []string{writeFile(t, dir, "one", "0123456789"), writeFile(t, dir, "two", "abcdefghij")}
	preflight, err := PreflightUpload(t.Context(), s, "media", "live/", paths)
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	preflight.LargeObjects, preflight.ChunkBytes, preflight.StaticManifestsReturned = "slo", 4, known
	var told []bool
	livedEarly := false
	recorder := &UploadRecorder{
		Admit:   func(UploadAdmission) error { return nil },
		Execute: func(UploadItem, transfercopy.Observation) error { return nil },
		Verify:  func(UploadItem, transfercopy.Observation) error { return nil },
	}
	recorder.StaticManifests = func(returned bool) error {
		told = append(told, returned)
		for _, name := range []string{"live/one", "live/two"} {
			if _, err := s.ObjectDetail(t.Context(), "media", name); err == nil {
				livedEarly = true
			}
		}
		if hook != nil {
			return hook(returned)
		}
		return nil
	}
	report, err := Upload(t.Context(), s, &segmentingEngine{session: s, chunk: 4}, "aeronautes_dst:media", "media", "live/", "job1", preflight, nil, nil, recorder)
	if err != nil && hook == nil {
		t.Fatalf("upload: %v", err)
	}
	return report, told, livedEarly
}

func liveClass(t *testing.T, s *swiftcontrol.Session, name string) swiftcontrol.ObjectClass {
	t.Helper()
	detail, err := s.ObjectDetail(t.Context(), "media", name)
	if err != nil {
		return ""
	}
	return detail.Class
}

// A store that returns static manifests is checked once per job: the check
// is written, read and deleted, the run is told, and every segmented file
// is published static (ADR 0111 decision 9).
func TestStaticCheckRunsOnceWhereManifestsAreReturned(t *testing.T) {
	mock := segmentMock()
	s, watch := watchedSession(t, mock)
	report, told, _ := uploadTwoSegmented(t, s, false, nil)
	if len(report.Failures) != 0 || report.Done != 2 {
		t.Fatalf("report = %+v", report)
	}
	if len(told) != 1 || !told[0] {
		t.Errorf("the run was told %v, want returned once", told)
	}
	if n := len(watch.puts()); n != 3 {
		t.Errorf("%d manifest PUTs, want one check and two live objects", n)
	}
	for _, name := range []string{"live/one", "live/two"} {
		if class := liveClass(t, s, name); class != swiftcontrol.ClassSLO {
			t.Errorf("%s is %q, want static", name, class)
		}
	}
	if _, err := s.ObjectDetail(t.Context(), "media", ".aeronautes/stage/job1/static-check"); !swiftcontrol.IsNotFound(err) {
		t.Errorf("the check manifest was not deleted: %v", err)
	}
}

// A store that answers the manifest read with an empty body gets dynamic
// objects: the run is told before anything is published, every file is
// published from its staged dynamic manifest over segments the check left
// in place, and nothing is checked twice (ADR 0111 decisions 10 and 11).
func TestStaticCheckFallsBackWhereManifestsAreNotReturned(t *testing.T) {
	mock := segmentMock()
	mock.ManifestsNotReturned = true
	s, watch := watchedSession(t, mock)
	report, told, livedEarly := uploadTwoSegmented(t, s, false, nil)
	if len(report.Failures) != 0 || report.Done != 2 {
		t.Fatalf("report = %+v", report)
	}
	if len(told) != 1 || told[0] || livedEarly {
		t.Errorf("the run was told %v (a live name before: %t), want not returned once, before any publication", told, livedEarly)
	}
	if n := len(watch.puts()); n != 1 {
		t.Errorf("%d manifest PUTs, want the one check", n)
	}
	for name, want := range map[string]string{"live/one": "0123456789", "live/two": "abcdefghij"} {
		if class := liveClass(t, s, name); class != swiftcontrol.ClassDLO {
			t.Errorf("%s is %q, want dynamic", name, class)
		}
		if got := readLive(t, s, "media", name); got != want {
			t.Errorf("%s reads %q", name, got)
		}
	}
	if _, err := s.ObjectDetail(t.Context(), "media", ".aeronautes/stage/job1/static-check"); !swiftcontrol.IsNotFound(err) {
		t.Errorf("the check manifest was not deleted: %v", err)
	}
}

// A connection that already learned the store returns static manifests
// checks nothing.
func TestStaticCheckIsSkippedWhenTheConnectionKnows(t *testing.T) {
	mock := segmentMock()
	s, watch := watchedSession(t, mock)
	report, told, _ := uploadTwoSegmented(t, s, true, nil)
	if len(report.Failures) != 0 || len(told) != 0 || len(watch.puts()) != 2 {
		t.Fatalf("report %+v, told %v, %d manifest PUTs; want two live objects and no check", report, told, len(watch.puts()))
	}
}

// A check that neither reads back what it wrote nor gets the empty answer
// fails the item before any publication and leaves the question open; a
// record of the answer that cannot be kept stops the run.
func TestStaticCheckFailuresPublishNothing(t *testing.T) {
	t.Run("the check manifest is refused", func(t *testing.T) {
		mock := segmentMock()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/static-check") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			mock.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		s := bindingSession(t, server)
		report, told, _ := uploadTwoSegmented(t, s, false, nil)
		if len(report.Failures) != 2 || len(told) != 0 {
			t.Fatalf("report %+v, told %v; want both items failed and nothing learned", report, told)
		}
		// The question stays open, so each item checks and fails alone.
		for _, failure := range report.Failures {
			if failure.Stage != "static-check" {
				t.Errorf("failure %+v, want the check's own", failure)
			}
		}
		for _, name := range []string{"live/one", "live/two"} {
			if class := liveClass(t, s, name); class != "" {
				t.Errorf("%s was published as %q", name, class)
			}
		}
	})
	t.Run("the check reads back other entries", func(t *testing.T) {
		mock := segmentMock()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/static-check") && r.URL.Query().Get("multipart-manifest") == "get" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte("[]"))
				return
			}
			mock.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		s := bindingSession(t, server)
		report, told, _ := uploadTwoSegmented(t, s, false, nil)
		if len(report.Failures) != 2 || len(told) != 0 {
			t.Fatalf("report %+v, told %v; want both items failed and nothing learned", report, told)
		}
		for _, name := range []string{"live/one", "live/two"} {
			if class := liveClass(t, s, name); class != "" {
				t.Errorf("%s was published as %q", name, class)
			}
		}
	})
	t.Run("the answer cannot be recorded", func(t *testing.T) {
		mock := segmentMock()
		mock.ManifestsNotReturned = true
		s, watch := watchedSession(t, mock)
		_, told, _ := uploadTwoSegmented(t, s, false, func(bool) error { return errors.New("the job store is gone") })
		if len(told) != 1 || len(watch.puts()) != 1 {
			t.Errorf("told %v, %d manifest PUTs", told, len(watch.puts()))
		}
		for _, name := range []string{"live/one", "live/two"} {
			if class := liveClass(t, s, name); class != "" {
				t.Errorf("%s was published as %q after the record failed", name, class)
			}
		}
	})
}

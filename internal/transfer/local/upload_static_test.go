package local

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// manifestWatch serves the mock and keeps the headers of every static
// manifest PUT, calling before (when set) ahead of serving one.
type manifestWatch struct {
	mu      sync.Mutex
	headers []http.Header
	before  func()
}

func (m *manifestWatch) puts() []http.Header {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.headers
}

func watchedSession(t *testing.T, mock *swifttest.Server) (*swiftcontrol.Session, *manifestWatch) {
	t.Helper()
	watch := &manifestWatch{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Query().Get("multipart-manifest") == "put" {
			watch.mu.Lock()
			watch.headers = append(watch.headers, r.Header.Clone())
			before := watch.before
			watch.mu.Unlock()
			if before != nil {
				before()
			}
		}
		mock.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return bindingSession(t, server), watch
}

// expectedStaticTag is derived here from the chunks' own digests, not by
// the code under test: the MD5 of their hexadecimal MD5s in order.
func expectedStaticTag(pieces ...string) string {
	var tags strings.Builder
	for _, piece := range pieces {
		tags.WriteString(md5hex(piece))
	}
	sum := md5.Sum([]byte(tags.String()))
	return hex.EncodeToString(sum[:])
}

func countObjects(t *testing.T, s *swiftcontrol.Session, container string) int {
	t.Helper()
	n := 0
	for entry, err := range s.Objects(t.Context(), container, swiftcontrol.ObjectListOptions{Deep: true}) {
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := entry.(swiftcontrol.ObjectEntry); ok {
			n++
		}
	}
	return n
}

func TestFileChunksSplitAsTheEngineDoes(t *testing.T) {
	path := writeFile(t, t.TempDir(), "file", "0123456789")
	for _, tc := range []struct {
		chunk int64
		want  []string
	}{
		{4, []string{"0123", "4567", "89"}},
		{5, []string{"01234", "56789"}},
		{64, []string{"0123456789"}},
	} {
		chunks, err := fileChunks(path, 10, tc.chunk)
		if err != nil {
			t.Fatal(err)
		}
		if len(chunks) != len(tc.want) {
			t.Fatalf("chunk %d: %d chunks, want %d", tc.chunk, len(chunks), len(tc.want))
		}
		for i, piece := range tc.want {
			if chunks[i] != (chunk{size: int64(len(piece)), md5: md5hex(piece)}) {
				t.Errorf("chunk %d piece %d = %+v, want %q", tc.chunk, i, chunks[i], piece)
			}
		}
	}
	if _, err := fileChunks(path, 11, 4); err == nil {
		t.Error("a file whose size changed since the plan was chunked as planned")
	}
}

// A segmented upload is published as one static object over the engine's
// segments, with a conditional create and no content tag, and the staged
// dynamic manifest is removed while its segments stay (ADR 0111).
func TestStaticUploadPublishesTheSegmentsAsOneObject(t *testing.T) {
	mock := segmentMock()
	s, watch := watchedSession(t, mock)
	// A space in the name makes the engine's manifest header differ from
	// the names it lists, which is what the decoding is for.
	path := writeFile(t, t.TempDir(), "my file", "0123456789")
	preflight, err := PreflightUpload(t.Context(), s, "media", "live/", []string{path})
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	preflight.LargeObjects, preflight.ChunkBytes = "slo", 4
	report, err := Upload(t.Context(), s, &segmentingEngine{session: s, chunk: 4}, "aeronautes_dst:media", "media", "live/", "job1", preflight, nil, nil)
	if err != nil || report.Done != 1 || len(report.Failures) != 0 {
		t.Fatalf("upload: %+v %v", report, err)
	}
	live, err := s.ObjectDetail(t.Context(), "media", "live/my file")
	if err != nil {
		t.Fatal(err)
	}
	if live.Class != swiftcontrol.ClassSLO || live.ContentLength != 10 || strings.Trim(live.ETag, `"`) != expectedStaticTag("0123", "4567", "89") {
		t.Errorf("live object = %s of %d bytes with tag %s", live.Class, live.ContentLength, live.ETag)
	}
	if got := readLive(t, s, "media", "live/my file"); got != "0123456789" {
		t.Errorf("live content = %q", got)
	}
	puts := watch.puts()
	if len(puts) != 1 || puts[0].Get("If-None-Match") != "*" || puts[0].Get("ETag") != "" {
		t.Errorf("manifest PUTs = %v, want one conditional create declaring no content tag", puts)
	}
	if _, err := s.ObjectDetail(t.Context(), "media", ".aeronautes/stage/job1/batch/my file"); !swiftcontrol.IsNotFound(err) {
		t.Errorf("the staged dynamic manifest was not removed: %v", err)
	}
	if n := countObjects(t, s, "media_segments"); n != 3 {
		t.Errorf("%d segments remain, want the 3 the live object names", n)
	}
}

// Every segment must be the file's own chunk at its index, and nothing
// else may sit under the job's prefix; otherwise nothing is published
// (ADR 0111 decision 3).
func TestStaticCommitRefusesSegmentsThatAreNotTheFile(t *testing.T) {
	const run = ".aeronautes/stage/job1/batch/object/2026-11-12-000000-run/"
	for _, tc := range []struct {
		name     string
		tamper   func(*swifttest.Server)
		captured bool
		code     transfercopy.Code
	}{
		{"a segment holds other bytes", func(m *swifttest.Server) { seed(m, "media_segments", run+"00000001", "4568") }, true, transfercopy.CodeUnverified},
		{"a segment is short", func(m *swifttest.Server) { seed(m, "media_segments", run+"00000001", "456") }, true, transfercopy.CodeUnverified},
		{"an unexpected name is under the prefix", func(m *swifttest.Server) { seed(m, "media_segments", run+"stray", "x") }, true, transfercopy.CodeUnverified},
		{"a segment index beyond the file", func(m *swifttest.Server) { seed(m, "media_segments", run+"00000003", "x") }, true, transfercopy.CodeUnverified},
		{"a segment never appears", func(m *swifttest.Server) { m.RemoveObject("media_segments", run+"00000002") }, true, transfercopy.CodeIncomplete},
		{"the segments are not ones the job recorded", nil, false, transfercopy.CodeUnverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := segmentMock()
			s, watch := watchedSession(t, mock)
			staged := ".aeronautes/stage/job1/batch/object"
			prefix, err := stageSegmented(t.Context(), s, "media", staged, []byte("0123456789"), 4, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.tamper != nil {
				tc.tamper(mock)
			}
			plan := segmentPlan{class: swiftcontrol.ClassSLO, chunkBytes: 4}
			if tc.captured {
				plan.captured = []rclone.SegmentPrefix{prefix}
			}
			item := UploadItem{Path: writeFile(t, t.TempDir(), "object", "0123456789"), Name: "live/object", Size: 10, Route: "rclone"}
			result := commitStagedUpload(t.Context(), s, "media", staged, item, false, nil, nil, plan)
			if result.Result != transfercopy.ResultFailed || result.Effect != transfercopy.EffectUnresolved ||
				result.label != "stage-segments" || result.Diagnostic.Code != tc.code {
				t.Fatalf("result = %+v %q, want an unresolved stage-segments failure coded %s", result.Observation, result.label, tc.code)
			}
			if len(watch.puts()) != 0 {
				t.Error("a manifest was written over segments that are not the file")
			}
			if _, err := s.ObjectDetail(t.Context(), "media", item.Name); !swiftcontrol.IsNotFound(err) {
				t.Errorf("something was published: %v", err)
			}
		})
	}
}

// An acknowledged overwrite drops the conditional and replaces what is
// live with the static object; a create whose name appears just before
// the manifest PUT is refused by the store's conditional; and a live tag
// that is not the one the chunks make fails verification.
func TestStaticCommitPublication(t *testing.T) {
	stage := func(t *testing.T) (*swifttest.Server, *swiftcontrol.Session, *manifestWatch, segmentPlan, string) {
		mock := segmentMock()
		s, watch := watchedSession(t, mock)
		staged := ".aeronautes/stage/job1/batch/object"
		prefix, err := stageSegmented(t.Context(), s, "media", staged, []byte("0123456789"), 4, nil)
		if err != nil {
			t.Fatal(err)
		}
		return mock, s, watch, segmentPlan{class: swiftcontrol.ClassSLO, chunkBytes: 4, captured: []rclone.SegmentPrefix{prefix}}, staged
	}
	path := writeFile(t, t.TempDir(), "object", "0123456789")

	t.Run("an acknowledged overwrite", func(t *testing.T) {
		mock, s, watch, plan, staged := stage(t)
		seed(mock, "media", "live/object", "the earlier object")
		item := UploadItem{Path: path, Name: "live/object", Size: 10, Route: "rclone", Replaces: true}
		result := commitStagedUpload(t.Context(), s, "media", staged, item, true, comparesAgainstLive(t, s, "media", item), nil, plan)
		if result.Result != transfercopy.ResultCopied {
			t.Fatalf("result = %+v %q %v", result.Observation, result.label, result.failure)
		}
		if puts := watch.puts(); len(puts) != 1 || puts[0].Get("If-None-Match") != "" {
			t.Errorf("manifest PUTs = %v, want one without the create conditional", puts)
		}
		if got := readLive(t, s, "media", "live/object"); got != "0123456789" {
			t.Errorf("live content = %q", got)
		}
	})

	t.Run("a writer arriving while the segments are listed", func(t *testing.T) {
		mock, s, watch, plan, staged := stage(t)
		seed(mock, "media", "live/object", "the earlier object")
		item := UploadItem{Path: path, Name: "live/object", Size: 10, Route: "rclone", Replaces: true}
		hook := acknowledgedNow(t, s, "media", item)
		if _, _, err := hook(item); err != nil {
			t.Fatal(err)
		}
		// The listing is the last preparation, so a comparison placed
		// before it would pass and publish over this writer.
		var once bool
		mock.AfterRequest = func(method, path string) {
			if once || method != http.MethodGet || !strings.HasSuffix(path, "/media_segments") {
				return
			}
			once = true
			seed(mock, "media", "live/object", "another writer")
		}
		run := &uploadRun{report: &Report{}, recorder: &UploadRecorder{Acknowledged: hook, VersioningUnchanged: versioningHeld}}
		result := commitStagedUpload(t.Context(), s, "media", staged, item, true, run.confirmUnchanged(), nil, plan)
		if result.Result != transfercopy.ResultConflict || result.Effect != transfercopy.EffectNotAttempted || len(watch.puts()) != 0 {
			t.Fatalf("result = %+v %q, want the fixed point to refuse before any manifest PUT", result.Observation, result.label)
		}
		if got := readLive(t, s, "media", "live/object"); got != "another writer" {
			t.Errorf("the writer's object was replaced: %q", got)
		}
	})

	t.Run("a name that appears before the manifest PUT", func(t *testing.T) {
		mock, s, watch, plan, staged := stage(t)
		watch.before = func() { seed(mock, "media", "live/object", "another writer") }
		item := UploadItem{Path: path, Name: "live/object", Size: 10, Route: "rclone"}
		result := commitStagedUpload(t.Context(), s, "media", staged, item, false, nil, nil, plan)
		if result.Result != transfercopy.ResultConflict || result.label != "commit-conflict" {
			t.Fatalf("result = %+v %q, want the store's conditional to refuse it", result.Observation, result.label)
		}
		if got := readLive(t, s, "media", "live/object"); got != "another writer" {
			t.Errorf("the other writer's object was replaced: %q", got)
		}
	})

	t.Run("a live tag the chunks do not make", func(t *testing.T) {
		mock := segmentMock()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodHead || !strings.HasSuffix(r.URL.Path, "/media/live/object") {
				mock.ServeHTTP(w, r)
				return
			}
			recorder := httptest.NewRecorder()
			mock.ServeHTTP(recorder, r)
			for key, values := range recorder.Header() {
				w.Header()[key] = values
			}
			if recorder.Code == http.StatusOK {
				w.Header().Set("Etag", `"`+md5hex("not the chunks")+`"`)
			}
			w.WriteHeader(recorder.Code)
		}))
		t.Cleanup(server.Close)
		s := bindingSession(t, server)
		staged := ".aeronautes/stage/job1/batch/object"
		prefix, err := stageSegmented(t.Context(), s, "media", staged, []byte("0123456789"), 4, nil)
		if err != nil {
			t.Fatal(err)
		}
		plan := segmentPlan{class: swiftcontrol.ClassSLO, chunkBytes: 4, captured: []rclone.SegmentPrefix{prefix}}
		item := UploadItem{Path: path, Name: "live/object", Size: 10, Route: "rclone"}
		result := commitStagedUpload(t.Context(), s, "media", staged, item, false, nil, nil, plan)
		if result.Result != transfercopy.ResultFailed || result.label != "verify" {
			t.Fatalf("result = %+v %q, want the verification to refuse the live tag", result.Observation, result.label)
		}
	})
}

// The static commit records committing before its manifest PUT and
// committed after it, both for the decoded prefix the capture recorded. A
// committing record that cannot be written publishes nothing; a committed
// record that cannot be written leaves the object published, reported as an
// issue, with its staged manifest kept, as the dynamic commit does.
func TestStaticCommitRecoveryRecords(t *testing.T) {
	for _, tc := range []struct {
		name      string
		refuse    string
		published bool
		label     string
	}{
		{"both recorded", "", true, ""},
		{"committing refused", "committing", false, "commit-intent"},
		{"committed refused", "committed", true, "commit-record"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := segmentMock()
			s, watch := watchedSession(t, mock)
			staged := ".aeronautes/stage/job1/batch/my object"
			prefix, err := stageSegmented(t.Context(), s, "media", staged, []byte("0123456789"), 4, nil)
			if err != nil {
				t.Fatal(err)
			}
			var records []string
			recovery := func(kind, container, segmentPrefix, object string) error {
				records = append(records, kind+" "+container+"/"+segmentPrefix+" "+object)
				if kind == tc.refuse {
					return errors.New("injected record refusal")
				}
				return nil
			}
			plan := segmentPlan{class: swiftcontrol.ClassSLO, chunkBytes: 4, captured: []rclone.SegmentPrefix{prefix}}
			item := UploadItem{Path: writeFile(t, t.TempDir(), "my object", "0123456789"), Name: "live/my object", Size: 10, Route: "rclone"}
			result := commitStagedUpload(t.Context(), s, "media", staged, item, false, nil, recovery, plan)

			segments := prefix.Container + "/" + prefix.Prefix
			want := []string{"committing " + segments + " media/live/my object"}
			if tc.refuse != "committing" {
				want = append(want, "committed "+segments+" ")
			}
			if !slices.Equal(records, want) {
				t.Errorf("records = %q, want %q", records, want)
			}
			_, liveErr := s.ObjectDetail(t.Context(), "media", item.Name)
			if published := liveErr == nil; published != tc.published || (tc.published != (len(watch.puts()) == 1)) {
				t.Fatalf("published %t with %d manifest PUTs, want %t", published, len(watch.puts()), tc.published)
			}
			_, stagedErr := s.ObjectDetail(t.Context(), "media", staged)
			if stagedKept := stagedErr == nil; stagedKept != (tc.refuse != "") {
				t.Errorf("staged manifest kept = %t, want %t", stagedKept, tc.refuse != "")
			}
			switch tc.refuse {
			case "committing":
				if result.Result != transfercopy.ResultFailed || result.label != "commit-intent" {
					t.Errorf("result %+v %q, want the commit-intent failure", result.Observation, result.label)
				}
			case "committed":
				if result.Result != transfercopy.ResultCopied || !slices.ContainsFunc(result.issues, func(i uploadIssue) bool { return i.label == "commit-record" }) {
					t.Errorf("result %+v issues %+v, want a copy carrying the commit-record issue", result.Observation, result.issues)
				}
			}
		})
	}
}

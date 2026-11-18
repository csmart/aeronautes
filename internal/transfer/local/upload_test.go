package local

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// scriptedEngine is an engine a test writes by hand: it records each call and
// answers with run.
type scriptedEngine struct {
	dsts  []string
	names [][]string
	run   func(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error)
}

func (e *scriptedEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	e.dsts = append(e.dsts, dst)
	e.names = append(e.names, names)
	return e.run(ctx, src, dst, names, onEvent)
}

// since is the store's request log from mark on.
func since(mock *swifttest.Server, mark int) []string {
	return mock.RequestLog()[mark:]
}

// ADR 0122 required proof: an upload of plain files is one PUT for each to
// its own name. The engine sends those; the application itself asks the
// store nothing about a name, before its write or after it.
func TestUploadAsksNothingOfTheDestinationAroundAPlainFile(t *testing.T) {
	s, mock := session(t)
	dir := t.TempDir()
	paths := []string{writeFile(t, dir, "a.txt", "alpha"), writeFile(t, dir, "b.txt", "bravo")}
	preflight, err := PreflightUpload(t.Context(), s, "media", "docs/", paths)
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	mark := len(mock.RequestLog())
	report, err := Upload(t.Context(), s, &fsEngine{s}, "aeronautes_dst:media", "media", preflight, nil, nil)
	if err != nil || report.Done != 2 || report.Verified != 2 || report.Bytes != 10 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	want := []string{"PUT /v1/AUTH_test/media/docs/a.txt", "PUT /v1/AUTH_test/media/docs/b.txt"}
	if got := since(mock, mark); !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %q, want only the engine's two writes", got)
	}
	if readLive(t, s, "media", "docs/a.txt") != "alpha" || readLive(t, s, "media", "docs/b.txt") != "bravo" {
		t.Fatal("the names do not hold the files")
	}
}

// Each source directory goes to the engine with the destination directory
// its files belong in, so two files of one name in different folders are
// written at their own names, and a destination at the container's top is
// the container itself.
func TestUploadGivesEachFolderItsOwnDestination(t *testing.T) {
	s, _ := session(t)
	root := t.TempDir()
	top := writeFile(t, root, "same.txt", "top")
	nested := writeFile(t, t.TempDir(), "same.txt", "nested")
	preflight, err := PreflightUploadSources(t.Context(), s, "media", "", []UploadSource{
		{Path: top, Name: "same.txt"}, {Path: nested, Name: "pics/sub/same.txt"}})
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	engine := &scriptedEngine{run: (&fsEngine{s}).Copy}
	report, err := Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", preflight, nil, nil)
	if err != nil || report.Done != 2 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	if want := []string{"aeronautes_dst:media", "aeronautes_dst:media/pics/sub"}; !reflect.DeepEqual(engine.dsts, want) {
		t.Fatalf("engine destinations = %q, want %q", engine.dsts, want)
	}
	if readLive(t, s, "media", "same.txt") != "top" || readLive(t, s, "media", "pics/sub/same.txt") != "nested" {
		t.Fatal("the two files of one name are not at their own names")
	}
}

// ADR 0122 decision 7: each file's outcome is what the engine said about it.
// A file it reported as copied is done, also when it reported an error for
// it first and then got it through; one it reported only an error for failed
// with that error; one it said nothing about is unknown when the engine
// itself failed, and was not written when the engine finished cleanly.
func TestUploadTakesTheEnginesWordForEachFile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		exit   int
		silent transfercopy.Observation
	}{
		{"the engine finished cleanly", 0, transfercopy.Observation{Result: transfercopy.ResultFailed, Effect: transfercopy.EffectNotAttempted,
			Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageEngine, Code: transfercopy.CodeIncomplete}}},
		{"the engine failed", 3, transfercopy.Observation{Result: transfercopy.ResultFailed, Effect: transfercopy.EffectUnresolved,
			Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageEngine, Code: transfercopy.CodeUnknown}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := session(t)
			dir := t.TempDir()
			paths := []string{writeFile(t, dir, "copied", "alpha"), writeFile(t, dir, "refused", "bravo"), writeFile(t, dir, "silent", "gamma"), writeFile(t, dir, "retried", "delta")}
			preflight, err := PreflightUpload(t.Context(), s, "media", "", paths)
			if err != nil || preflight.Rejected() {
				t.Fatalf("preflight: %+v %v", preflight, err)
			}
			engine := &scriptedEngine{run: func(_ context.Context, _, _ string, _ []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
				onEvent(rclone.Event{Level: "error", Object: "refused", Msg: "Failed to copy: the store said no"})
				onEvent(rclone.Event{Level: "info", Object: "copied", Msg: "Copied (replaced existing)"})
				onEvent(rclone.Event{Level: "error", Object: "retried", Msg: "Failed to copy: a first try was reset"})
				onEvent(rclone.Event{Level: "info", Object: "retried", Msg: "Copied (new)"})
				return &rclone.Result{ExitCode: tc.exit}, nil
			}}
			executed := map[string]transfercopy.Observation{}
			verified := map[string]bool{}
			recorder := &UploadRecorder{
				Admit:   func(UploadAdmission) error { return nil },
				Execute: func(item UploadItem, o transfercopy.Observation) error { executed[item.Name] = o; return nil },
				Verify: func(item UploadItem, o transfercopy.Observation) error {
					verified[item.Name] = o.Result == transfercopy.ResultVerified
					return nil
				},
			}
			report, err := Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", preflight, nil, nil, recorder)
			if err != nil || report.Done != 2 || report.Bytes != 10 || len(report.Failures) != 2 {
				t.Fatalf("report = %+v, %v", report, err)
			}
			want := map[string]transfercopy.Observation{
				"copied":  {Result: transfercopy.ResultCopied, Effect: transfercopy.EffectReportedApplied, CopiedBytes: 5},
				"retried": {Result: transfercopy.ResultCopied, Effect: transfercopy.EffectReportedApplied, CopiedBytes: 5},
				"refused": {Result: transfercopy.ResultFailed, Effect: transfercopy.EffectUnresolved,
					Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageEngine, Code: transfercopy.CodeIO}},
				"silent": tc.silent,
			}
			if !reflect.DeepEqual(executed, want) {
				t.Fatalf("outcomes = %+v\nwant %+v", executed, want)
			}
			if !reflect.DeepEqual(verified, map[string]bool{"copied": true, "retried": true}) {
				t.Fatalf("second records = %+v, want one for each written file and no other", verified)
			}
			if !strings.Contains(report.Failures[0].Err, "the store said no") {
				t.Fatalf("the engine's own reason was lost: %+v", report.Failures)
			}
		})
	}
}

// ADR 0122 decision 14 (finding F78): a file the engine reports written or
// the same is recorded and shown in the progress while the engine is still
// running, and a file with an error line or none is recorded after the run.
// A file the engine reports again, in the run repeated for an expired
// sign-in, is recorded once.
func TestUploadRecordsEachFileWhenTheEngineReportsIt(t *testing.T) {
	s, _ := session(t)
	dir := t.TempDir()
	paths := []string{writeFile(t, dir, "written", "alpha"), writeFile(t, dir, "same", "bravo"),
		writeFile(t, dir, "refused", "gamma"), writeFile(t, dir, "silent", "delta")}
	preflight, err := PreflightUpload(t.Context(), s, "media", "", paths)
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	executed := map[string]int{}
	var shownDone, shownSame int64
	recorder := &UploadRecorder{
		Admit:   func(UploadAdmission) error { return nil },
		Execute: func(item UploadItem, _ transfercopy.Observation) error { executed[item.Name]++; return nil },
		Verify:  func(UploadItem, transfercopy.Observation) error { return nil },
	}
	recordedNow := func(t *testing.T, want map[string]int) {
		t.Helper()
		if !reflect.DeepEqual(executed, want) {
			t.Fatalf("recorded while the engine runs = %v, want %v", executed, want)
		}
	}
	runs := 0
	engine := &scriptedEngine{run: func(_ context.Context, _, _ string, _ []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
		runs++
		onEvent(rclone.Event{Level: "info", Object: "written", Msg: "Copied (new)"})
		if runs == 1 {
			recordedNow(t, map[string]int{"written": 1})
			return &rclone.Result{ExitCode: 1, Errors: []string{"401 Unauthorized"}}, nil
		}
		onEvent(rclone.Event{Level: "debug", Object: "same", Msg: "Unchanged skipping"})
		onEvent(rclone.Event{Level: "error", Object: "refused", Msg: "Failed to copy: the store said no"})
		recordedNow(t, map[string]int{"written": 1, "same": 1})
		if shownDone != 1 || shownSame != 1 {
			t.Fatalf("progress while the engine runs = %d written and %d the same, want 1 and 1", shownDone, shownSame)
		}
		return &rclone.Result{}, nil
	}}
	report, err := Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", preflight,
		func(done, same, _, _ int64) { shownDone, shownSame = done, same }, nil, recorder)
	if err != nil || runs != 2 || report.Done != 1 || report.Same != 1 || len(report.Failures) != 2 {
		t.Fatalf("runs = %d, report = %+v, %v", runs, report, err)
	}
	if want := map[string]int{"written": 1, "same": 1, "refused": 1, "silent": 1}; !reflect.DeepEqual(executed, want) {
		t.Fatalf("records = %v, want one for each file", executed)
	}
}

// headerWatch serves mock and keeps each request's method, path and two
// headers, for a test that asks what a write carried.
func headerWatch(t *testing.T, mock *swifttest.Server, alter func(*http.Request)) (*swiftcontrol.Session, *[]string) {
	t.Helper()
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			seen = append(seen, r.Method+" "+r.URL.Path+" etag="+r.Header.Get("ETag")+" if-none-match="+r.Header.Get("If-None-Match"))
			if alter != nil {
				alter(r)
			}
		}
		mock.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	s, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Authenticate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s, &seen
}

// ADR 0122 decision 3: a name the engine cannot carry is one PUT that
// declares the file's digest, with no create-only condition and nothing read
// before or after it.
func TestNativeUploadIsOnePUTThatDeclaresTheDigest(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	s, seen := headerWatch(t, mock, nil)
	path := writeFile(t, t.TempDir(), "object", "new local content")
	preflight, err := PreflightUpload(t.Context(), s, "media", "line\n/", []string{path})
	if err != nil || preflight.Rejected() || preflight.Items[0].Route != "native" {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	mark := len(*seen)
	report, err := Upload(t.Context(), s, nil, "aeronautes_dst:media", "media", preflight, nil, nil)
	if err != nil || report.Done != 1 || report.NativeRouted != 1 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	digest := md5.Sum([]byte("new local content"))
	want := []string{"PUT /v1/AUTH_test/media/line\n/object etag=" + hex.EncodeToString(digest[:]) + " if-none-match="}
	if got := (*seen)[mark:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %q, want one PUT with the file's digest and no condition: %q", got, want)
	}
	if readLive(t, s, "media", "line\n/object") != "new local content" {
		t.Fatal("the name does not hold the file")
	}
}

// ADR 0122 required proof: bytes that do not match the declared digest are
// refused by the store and reported failed, with the name left as it was.
func TestNativeUploadFailsWhenTheStoreRefusesTheBytes(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	s, _ := headerWatch(t, mock, func(r *http.Request) {
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/object") {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(bytes.ToUpper(body)))
		}
	})
	seed(mock, "media", "line\n/object", "what was there")
	path := writeFile(t, t.TempDir(), "object", "new local content")
	preflight, err := PreflightUploadSourcesReplacing(t.Context(), s, "media", "line\n/", SourcesByBaseName([]string{path}))
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	report, err := Upload(t.Context(), s, nil, "aeronautes_dst:media", "media", preflight, nil, nil)
	if err != nil || report.Done != 0 || len(report.Failures) != 1 || !strings.Contains(report.Failures[0].Err, "declared digest") {
		t.Fatalf("report = %+v, %v; want the file failed against its digest", report, err)
	}
	if readLive(t, s, "media", "line\n/object") != "what was there" {
		t.Fatal("the name no longer holds what was there")
	}
}

// staticPlan is a preflight for files of which big ones are published as
// static large objects by a chunk of ten bytes.
func staticPlan(t *testing.T, s *swiftcontrol.Session, known bool, paths ...string) *UploadPreflight {
	t.Helper()
	preflight, err := PreflightUpload(t.Context(), s, "media", "up/", paths)
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	preflight.LargeObjects, preflight.ChunkBytes, preflight.StaticManifestsReturned = string(swiftcontrol.ClassSLO), 10, known
	return preflight
}

func classOf(t *testing.T, s *swiftcontrol.Session, name string) swiftcontrol.ObjectClass {
	t.Helper()
	detail, err := s.ObjectDetail(t.Context(), "media", name)
	if err != nil {
		t.Fatal(err)
	}
	return detail.Class
}

func count(log []string, prefix string) int {
	n := 0
	for _, line := range log {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

// ADR 0122 decision 3 and ADR 0111: a file uploaded in segments becomes a
// static large object by one request, the manifest written over the dynamic
// one the engine wrote, naming the segments the engine reported. Whether the
// store returns static manifests is learned once, by reading the first one
// back at its own name, and is not asked again.
func TestUploadWritesTheStaticManifestOverTheEnginesDynamicOne(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(map[bool]string{true: "the connection already knows", false: "learned from the first"}[known], func(t *testing.T) {
			mock := segmentMock()
			s := servedSession(t, mock)
			if err := s.Authenticate(t.Context()); err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			big, bigger, small := writeFile(t, dir, "big", "0123456789abcdefghijKLMNO"), writeFile(t, dir, "bigger", "0123456789abcdefghijKLMNOPQRSTUVWXYZ"), writeFile(t, dir, "small", "tiny")
			preflight := staticPlan(t, s, known, big, bigger, small)
			var learned []bool
			recorder := &UploadRecorder{
				Admit:           func(UploadAdmission) error { return nil },
				Execute:         func(UploadItem, transfercopy.Observation) error { return nil },
				Verify:          func(UploadItem, transfercopy.Observation) error { return nil },
				StaticManifests: func(returned bool) error { learned = append(learned, returned); return nil },
			}
			mark := len(mock.RequestLog())
			report, err := Upload(t.Context(), s, &segmentingEngine{session: s, chunk: 10}, "aeronautes_dst:media", "media", preflight, nil, nil, recorder)
			// The upload's own requests, before this test reads anything.
			log := since(mock, mark)
			if err != nil || report.Done != 3 || len(report.Failures) != 0 {
				t.Fatalf("report = %+v, %v", report, err)
			}
			for name, want := range map[string]swiftcontrol.ObjectClass{"up/big": swiftcontrol.ClassSLO, "up/bigger": swiftcontrol.ClassSLO, "up/small": swiftcontrol.ClassPlain} {
				if got := classOf(t, s, name); got != want {
					t.Fatalf("%s is %s, want %s", name, got, want)
				}
			}
			if readLive(t, s, "media", "up/big") != "0123456789abcdefghijKLMNO" {
				t.Fatal("the static object does not assemble the file")
			}
			if puts := count(log, "PUT /v1/AUTH_test/media/up/big?multipart-manifest=put") + count(log, "PUT /v1/AUTH_test/media/up/bigger?multipart-manifest=put"); puts != 2 {
				t.Fatalf("static manifest writes = %d, want one for each large file: %q", puts, log)
			}
			reads, wantReads, wantLearned := count(log, "GET /v1/AUTH_test/media/up/"), 1, []bool{true}
			if known {
				wantReads, wantLearned = 0, nil
			}
			if reads != wantReads || !reflect.DeepEqual(learned, wantLearned) {
				t.Fatalf("manifest reads = %d and learned %v, want %d and %v: %q", reads, learned, wantReads, wantLearned, log)
			}
			if heads := count(log, "HEAD /v1/AUTH_test/media/up/"); heads != 0 {
				t.Fatalf("the destination was read %d time(s) around its write: %q", heads, log)
			}
			if want := []rclone.SegmentPrefix{{Container: "media_segments", Prefix: "up/big/2026-11-12-000000-run/"}, {Container: "media_segments", Prefix: "up/bigger/2026-11-12-000000-run/"}}; !reflect.DeepEqual(report.CapturedPrefixes, want) {
				t.Fatalf("captured segment paths = %+v, want %+v", report.CapturedPrefixes, want)
			}
		})
	}
}

// ADR 0111 decisions 9 and 10: a store that takes a static manifest and does
// not return it gets the dynamic manifest back at that name, and dynamic
// large objects for the rest of the job.
func TestUploadKeepsDynamicWhereStaticManifestsAreNotReturned(t *testing.T) {
	mock := segmentMock()
	mock.ManifestsNotReturned = true
	s := servedSession(t, mock)
	if err := s.Authenticate(t.Context()); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	preflight := staticPlan(t, s, false, writeFile(t, dir, "big", "0123456789abcdefghijKLMNO"), writeFile(t, dir, "bigger", "0123456789abcdefghijKLMNOPQRSTUVWXYZ"))
	var learned []bool
	recorder := &UploadRecorder{
		Admit:           func(UploadAdmission) error { return nil },
		Execute:         func(UploadItem, transfercopy.Observation) error { return nil },
		Verify:          func(UploadItem, transfercopy.Observation) error { return nil },
		StaticManifests: func(returned bool) error { learned = append(learned, returned); return nil },
	}
	mark := len(mock.RequestLog())
	report, err := Upload(t.Context(), s, &segmentingEngine{session: s, chunk: 10}, "aeronautes_dst:media", "media", preflight, nil, nil, recorder)
	log := since(mock, mark)
	if err != nil || report.Done != 2 || len(report.Failures) != 0 || !reflect.DeepEqual(learned, []bool{false}) {
		t.Fatalf("report = %+v, %v, learned %v", report, err, learned)
	}
	if puts := count(log, "PUT /v1/AUTH_test/media/up/big?multipart-manifest=put") + count(log, "PUT /v1/AUTH_test/media/up/bigger?multipart-manifest=put"); puts != 1 {
		t.Fatalf("static manifest writes = %d, want the first file's alone: %q", puts, log)
	}
	for _, name := range []string{"up/big", "up/bigger"} {
		if got := classOf(t, s, name); got != swiftcontrol.ClassDLO {
			t.Fatalf("%s is %s, want a dynamic large object", name, got)
		}
	}
	if readLive(t, s, "media", "up/big") != "0123456789abcdefghijKLMNO" {
		t.Fatal("the dynamic object put back does not assemble the file")
	}
}

// The engine records a file's modification time on every object it uploads,
// and the application's own writes of an uploaded file record the same: the
// one PUT of a name the engine cannot carry, the static manifest written over
// the engine's dynamic one, and the dynamic manifest put back where the store
// does not return static ones.
func TestTheApplicationsUploadWritesRecordTheFilesModificationTime(t *testing.T) {
	// Seconds since the epoch in fixed point with trailing zeros dropped,
	// which is how the engine's Swift library writes a time.
	changed := time.Unix(1756400000, 500_000_000)
	const want = "1756400000.5"
	for _, tc := range []struct {
		name, prefix, object string
		static, notReturned  bool
	}{
		{name: "a name the engine cannot carry", prefix: "line\n/", object: "line\n/big"},
		{name: "a static manifest", prefix: "up/", object: "up/big", static: true},
		{name: "a dynamic manifest put back", prefix: "up/", object: "up/big", static: true, notReturned: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := segmentMock()
			mock.ManifestsNotReturned = tc.notReturned
			s := servedSession(t, mock)
			if err := s.Authenticate(t.Context()); err != nil {
				t.Fatal(err)
			}
			path := writeFile(t, t.TempDir(), "big", "0123456789abcdefghijKLMNO")
			if err := os.Chtimes(path, changed, changed); err != nil {
				t.Fatal(err)
			}
			preflight, err := PreflightUpload(t.Context(), s, "media", tc.prefix, []string{path})
			if err != nil || preflight.Rejected() {
				t.Fatalf("preflight: %+v %v", preflight, err)
			}
			if tc.static {
				preflight.LargeObjects, preflight.ChunkBytes = string(swiftcontrol.ClassSLO), 10
			}
			report, err := Upload(t.Context(), s, &segmentingEngine{session: s, chunk: 10}, "aeronautes_dst:media", "media", preflight, nil, nil)
			if err != nil || report.Done != 1 {
				t.Fatalf("report = %+v, %v", report, err)
			}
			detail, err := s.ObjectDetail(t.Context(), "media", tc.object)
			if err != nil {
				t.Fatal(err)
			}
			if got := detail.Meta["Mtime"]; got != want {
				t.Fatalf("%s (%s) records modification time %q, want %q", tc.object, detail.Class, got, want)
			}
		})
	}
}

// The store checks a static manifest against the segments it names. A
// segment that does not hold the file's bytes makes it refuse the manifest:
// the file is reported failed, and the object stays the dynamic one the
// engine wrote. The engine's copied line for such a file is not its outcome,
// so nothing is recorded for it until the manifest's answer (ADR 0122
// decision 14), and then only the failure.
func TestUploadFailsAFileWhoseStaticManifestTheStoreRefuses(t *testing.T) {
	mock := segmentMock()
	s := servedSession(t, mock)
	if err := s.Authenticate(t.Context()); err != nil {
		t.Fatal(err)
	}
	preflight := staticPlan(t, s, true, writeFile(t, t.TempDir(), "big", "0123456789abcdefghijKLMNO"))
	var records []transfercopy.Result
	recorder := &UploadRecorder{
		Admit:   func(UploadAdmission) error { return nil },
		Execute: func(_ UploadItem, o transfercopy.Observation) error { records = append(records, o.Result); return nil },
		Verify:  func(UploadItem, transfercopy.Observation) error { return nil },
	}
	inner := &segmentingEngine{session: s, chunk: 10}
	engine := &scriptedEngine{run: func(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
		result, err := inner.Copy(ctx, src, dst, names, onEvent)
		if len(records) != 0 {
			t.Errorf("recorded %v at the engine's copied line, before the static manifest was answered", records)
		}
		// A segment whose bytes are not the file's, of the same length.
		seed(mock, "media_segments", "up/big/2026-11-12-000000-run/00000001", "ABCDEFGHIJ")
		return result, err
	}}
	report, err := Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", preflight, nil, nil, recorder)
	if err != nil || report.Done != 0 || len(report.Failures) != 1 || !strings.Contains(report.Failures[0].Err, "dynamic one the engine wrote") {
		t.Fatalf("report = %+v, %v; want the file failed and said to be dynamic", report, err)
	}
	if !reflect.DeepEqual(records, []transfercopy.Result{transfercopy.ResultFailed}) {
		t.Fatalf("records = %v, want the one failure", records)
	}
	if got := classOf(t, s, "up/big"); got != swiftcontrol.ClassDLO {
		t.Fatalf("the object is %s, want the dynamic one the engine wrote", got)
	}
}

// Every file is admitted to the record before the engine is given any, each
// outcome is recorded once, and a record that cannot be kept stops the run:
// the files of a folder the engine never reached are recorded as not
// attempted.
func TestUploadRecordsEveryFileAndStopsWhenARecordIsLost(t *testing.T) {
	s, _ := session(t)
	first, second := t.TempDir(), t.TempDir()
	a, b := writeFile(t, first, "a", "alpha"), writeFile(t, second, "b", "bravo")
	preflight, err := PreflightUploadSources(t.Context(), s, "media", "", []UploadSource{{Path: a, Name: "one/a"}, {Path: b, Name: "two/b"}})
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	var admitted []string
	executed := map[string]transfercopy.Result{}
	lost := errors.New("the record store stopped")
	recorder := &UploadRecorder{
		Admit: func(a UploadAdmission) error { admitted = append(admitted, a.Item.Name); return nil },
		Execute: func(item UploadItem, o transfercopy.Observation) error {
			if _, twice := executed[item.Name]; twice {
				t.Errorf("%s was recorded twice", item.Name)
			}
			executed[item.Name] = o.Result
			return nil
		},
		Verify: func(UploadItem, transfercopy.Observation) error { return lost },
	}
	engine := &scriptedEngine{}
	engine.run = func(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
		if !slices.Equal(admitted, []string{"one/a", "two/b"}) {
			t.Errorf("the engine was started with only %v admitted", admitted)
		}
		return (&fsEngine{s}).Copy(ctx, src, dst, names, onEvent)
	}
	report, err := Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", preflight, nil, nil, recorder)
	if !errors.Is(err, lost) || len(engine.dsts) != 1 || report.Done != 1 {
		t.Fatalf("report = %+v, err = %v, engine calls = %d; want the run stopped after the first folder", report, err, len(engine.dsts))
	}
	if want := map[string]transfercopy.Result{"one/a": transfercopy.ResultCopied, "two/b": transfercopy.ResultNotAttempted}; !reflect.DeepEqual(executed, want) {
		t.Fatalf("recorded outcomes = %v, want %v", executed, want)
	}
}

// A run stopped while the engine has a folder still records every file of
// that folder: the engine wrote them, whether or not the job can go on. Here
// the record of a segment path cannot be kept, which stops the run; both
// files of the first folder are recorded as written, and the folder the
// engine never reached is recorded as not attempted.
func TestUploadRecordsEveryFileOfAFolderTheEngineWasGiven(t *testing.T) {
	s, _ := session(t)
	first, second := t.TempDir(), t.TempDir()
	preflight, err := PreflightUploadSources(t.Context(), s, "media", "", []UploadSource{
		{Path: writeFile(t, first, "a", "alpha"), Name: "one/a"}, {Path: writeFile(t, first, "b", "bravo"), Name: "one/b"},
		{Path: writeFile(t, second, "c", "gamma"), Name: "two/c"}})
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	executed := map[string]transfercopy.Result{}
	recorder := &UploadRecorder{
		Admit:   func(UploadAdmission) error { return nil },
		Execute: func(item UploadItem, o transfercopy.Observation) error { executed[item.Name] = o.Result; return nil },
		Verify:  func(UploadItem, transfercopy.Observation) error { return nil },
	}
	lost := errors.New("the recovery record could not be kept")
	engine := &scriptedEngine{run: func(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
		onEvent(rclone.Event{Level: "debug", Object: "a", Msg: `Uploading segment file "one/a/run/00000000" into "media_segments"`})
		return (&fsEngine{s}).Copy(ctx, src, dst, names, onEvent)
	}}
	report, err := Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", preflight, nil,
		func(string, string, string, string) error { return lost }, recorder)
	if !errors.Is(err, lost) || len(engine.dsts) != 1 || report.Done != 2 {
		t.Fatalf("report = %+v, err = %v, engine calls = %d; want the run stopped after the first folder with both its files done", report, err, len(engine.dsts))
	}
	want := map[string]transfercopy.Result{"one/a": transfercopy.ResultCopied, "one/b": transfercopy.ResultCopied, "two/c": transfercopy.ResultNotAttempted}
	if !reflect.DeepEqual(executed, want) {
		t.Fatalf("recorded outcomes = %v, want %v", executed, want)
	}
}

// A stopped run sends nothing further: a file the engine uploaded in
// segments is recorded as written and stays the dynamic object the engine
// wrote, with no static manifest written over it.
func TestAStoppedUploadWritesNoStaticManifest(t *testing.T) {
	mock := segmentMock()
	s := servedSession(t, mock)
	if err := s.Authenticate(t.Context()); err != nil {
		t.Fatal(err)
	}
	preflight := staticPlan(t, s, true, writeFile(t, t.TempDir(), "big", "0123456789abcdefghijKLMNO"))
	lost := errors.New("the recovery record could not be kept")
	mark := len(mock.RequestLog())
	report, err := Upload(t.Context(), s, &segmentingEngine{session: s, chunk: 10}, "aeronautes_dst:media", "media", preflight, nil,
		func(string, string, string, string) error { return lost })
	log := since(mock, mark)
	if !errors.Is(err, lost) || report.Done != 1 {
		t.Fatalf("report = %+v, err = %v; want the run stopped with the file recorded as written", report, err)
	}
	if puts := count(log, "PUT /v1/AUTH_test/media/up/big?multipart-manifest=put"); puts != 0 {
		t.Fatalf("a stopped run wrote %d static manifest(s): %q", puts, log)
	}
	if got := classOf(t, s, "up/big"); got != swiftcontrol.ClassDLO {
		t.Fatalf("the object is %s, want the dynamic one the engine wrote", got)
	}
}

func TestUploadRefusesARecorderWithoutEveryRecord(t *testing.T) {
	s, mock := session(t)
	preflight, err := PreflightUpload(t.Context(), s, "media", "", []string{writeFile(t, t.TempDir(), "a", "alpha")})
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	mark := len(mock.RequestLog())
	partial := &UploadRecorder{Admit: func(UploadAdmission) error { return nil }, Execute: func(UploadItem, transfercopy.Observation) error { return nil }}
	if report, err := Upload(t.Context(), s, &fsEngine{s}, "aeronautes_dst:media", "media", preflight, nil, nil, partial); err == nil || report != nil || len(since(mock, mark)) != 0 {
		t.Fatalf("an upload ran without a way to record its outcomes: %+v, %v", report, err)
	}
}

// ADR 0104 decision 1: the job is asked whether to pause at the head of each
// unit, before a folder is handed to the engine and before a file the
// application sends itself, and nowhere else.
func TestUploadPausesAtTheHeadOfEachUnit(t *testing.T) {
	s, _ := session(t)
	first, second := t.TempDir(), t.TempDir()
	preflight, err := PreflightUploadSources(t.Context(), s, "media", "", []UploadSource{
		{Path: writeFile(t, first, "a", "alpha"), Name: "one/a"}, {Path: writeFile(t, first, "b", "bravo"), Name: "one/b"},
		{Path: writeFile(t, second, "c", "gamma"), Name: "line\n/c"}})
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	var order []string
	recorder := &UploadRecorder{
		Admit:   func(UploadAdmission) error { return nil },
		Execute: func(UploadItem, transfercopy.Observation) error { return nil },
		Verify: func(item UploadItem, _ transfercopy.Observation) error {
			order = append(order, "done "+filepath.Base(item.Path))
			return nil
		},
		Pause: func(context.Context) error { order = append(order, "pause"); return nil },
	}
	if report, err := Upload(t.Context(), s, &fsEngine{s}, "aeronautes_dst:media", "media", preflight, nil, nil, recorder); err != nil || report.Done != 3 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	// The file the application sends itself is a unit where it is met; the
	// engine's folder is a unit after the planned files have been walked.
	if want := []string{"pause", "done c", "pause", "done a", "done b"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

// A failure that looks like a refused token is worth one fresh engine, whose
// remotes resolve with a new token, and no more than one.
func TestUploadStartsTheEngineOnceMoreAfterARefusedToken(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failures  int
		wantCalls int
		wantDone  int64
	}{{"refused once", 1, 2, 1}, {"refused every time", 5, 2, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := session(t)
			preflight, err := PreflightUpload(t.Context(), s, "media", "", []string{writeFile(t, t.TempDir(), "a", "alpha")})
			if err != nil || preflight.Rejected() {
				t.Fatalf("preflight: %+v %v", preflight, err)
			}
			engine := &scriptedEngine{}
			engine.run = func(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
				if len(engine.dsts) <= tc.failures {
					return &rclone.Result{ExitCode: 1, Errors: []string{"HTTP 401 Unauthorized"}}, nil
				}
				return (&fsEngine{s}).Copy(ctx, src, dst, names, onEvent)
			}
			report, err := Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", preflight, nil, nil)
			if err != nil || len(engine.dsts) != tc.wantCalls || report.Done != tc.wantDone {
				t.Fatalf("report = %+v, %v, engine calls = %d; want %d call(s) and %d done", report, err, len(engine.dsts), tc.wantCalls, tc.wantDone)
			}
		})
	}
}

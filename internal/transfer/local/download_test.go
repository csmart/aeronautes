package local

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// downloadRecords collects what a download recorded for each item.
type downloadRecords struct {
	admitted []string
	executed map[string]transfercopy.Observation
	verified map[string]bool
}

func (r *downloadRecords) recorder() *DownloadRecorder {
	r.executed, r.verified = map[string]transfercopy.Observation{}, map[string]bool{}
	return &DownloadRecorder{
		Admit:   func(a DownloadAdmission) error { r.admitted = append(r.admitted, a.Item.Name); return nil },
		Execute: func(item DownloadItem, o transfercopy.Observation) error { r.executed[item.Name] = o; return nil },
		Verify: func(item DownloadItem, o transfercopy.Observation) error {
			r.verified[item.Name] = o.Result == transfercopy.ResultVerified
			return nil
		},
	}
}

// folderNames lists every entry under root, relative and slash-separated.
func folderNames(t *testing.T, root string) []string {
	t.Helper()
	var names []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || path == root {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func readLocal(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// ADR 0122 decision 5 and its required proof: the engine writes each file in
// the folder the operator chose. There is no stage folder, the application
// asks the store nothing about a file around its download, and what the
// engine said is each file's outcome and its record.
func TestDownloadWritesEachFileInTheChosenFolder(t *testing.T) {
	s, mock := session(t)
	seed(mock, "media", "scope/a.txt", "alpha")
	seed(mock, "media", "scope/sub/b.txt", "bravo")
	dest := t.TempDir()
	preflight, err := PreflightDownload(t.Context(), s, "media", "scope/", dest)
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	engine := &scriptedEngine{run: (&fsEngine{s}).Copy}
	var kept downloadRecords
	mark := len(mock.RequestLog())
	report, err := Download(t.Context(), s, engine, "aeronautes_src:media", "media", "scope/", dest, preflight, nil, kept.recorder())
	if err != nil || report.Done != 2 || report.Verified != 2 || report.Bytes != 10 || len(report.Failures) != 0 {
		t.Fatalf("report = %+v, %v", report, err)
	}
	if !reflect.DeepEqual(engine.dsts, []string{dest}) || !reflect.DeepEqual(engine.names, [][]string{{"a.txt", "sub/b.txt"}}) {
		t.Fatalf("engine was given %q with %q, want the chosen folder itself and the two names", engine.dsts, engine.names)
	}
	// The engine's two reads are the only requests: nothing of the
	// application's own before or after.
	if want := []string{"GET /v1/AUTH_test/media/scope/a.txt", "GET /v1/AUTH_test/media/scope/sub/b.txt"}; !reflect.DeepEqual(since(mock, mark), want) {
		t.Fatalf("requests = %q, want the engine's reads alone: %q", since(mock, mark), want)
	}
	if got := folderNames(t, dest); !reflect.DeepEqual(got, []string{"a.txt", "sub", "sub/b.txt"}) {
		t.Fatalf("the folder holds %q, want the two files and their folder and nothing else", got)
	}
	if readLocal(t, filepath.Join(dest, "a.txt")) != "alpha" || readLocal(t, filepath.Join(dest, "sub", "b.txt")) != "bravo" {
		t.Fatal("the files do not hold the objects' bytes")
	}
	for _, name := range []string{"scope/a.txt", "scope/sub/b.txt"} {
		if got := kept.executed[name]; got.Result != transfercopy.ResultCopied || got.Effect != transfercopy.EffectReportedApplied || !kept.verified[name] {
			t.Fatalf("%s recorded %+v, second record %v", name, got, kept.verified[name])
		}
	}
}

// ADR 0122 decision 7, measured on the pinned engine: a file's outcome is
// what the engine said about it. Its line is the file downloaded. Its line
// that the destination exists is a file left alone, recorded as a conflict.
// Its error line is the file failed with that error. Nothing said is unknown
// when the engine itself failed, and not downloaded when it ended cleanly.
func TestADownloadedFilesOutcomeIsWhatTheEngineSaid(t *testing.T) {
	for _, tc := range []struct {
		name    string
		run     func(onEvent func(rclone.Event)) *rclone.Result
		appears bool
		result  transfercopy.Result
		effect  transfercopy.Effect
		says    string
		stopped bool
	}{
		{name: "its line", result: transfercopy.ResultCopied, effect: transfercopy.EffectReportedApplied},
		{name: "a file that appeared since the preview", appears: true, result: transfercopy.ResultConflict,
			effect: transfercopy.EffectNotAttempted, says: "a download does not replace a file"},
		{name: "an error line", run: func(onEvent func(rclone.Event)) *rclone.Result {
			// The summary does not repeat the line, so only the file's own
			// line can be the reason recorded for it.
			onEvent(rclone.Event{Level: "error", Object: "a.txt", Msg: "Failed to copy: unexpected EOF"})
			return &rclone.Result{ExitCode: 5, Errors: []string{"Attempt 3/3 failed with 1 errors"}}
		}, result: transfercopy.ResultFailed, effect: transfercopy.EffectUnresolved, says: "unexpected EOF", stopped: true},
		{name: "nothing, and the engine failed", run: func(func(rclone.Event)) *rclone.Result {
			return &rclone.Result{ExitCode: 3}
		}, result: transfercopy.ResultFailed, effect: transfercopy.EffectUnresolved, says: "the engine stopped without reporting this file", stopped: true},
		{name: "nothing, and the engine ended cleanly", run: func(func(rclone.Event)) *rclone.Result {
			return &rclone.Result{}
		}, result: transfercopy.ResultFailed, effect: transfercopy.EffectNotAttempted, says: "it may no longer be at the source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := session(t)
			seed(mock, "media", "scope/a.txt", "alpha")
			dest := t.TempDir()
			preflight, err := PreflightDownload(t.Context(), s, "media", "scope/", dest)
			if err != nil || preflight.Rejected() {
				t.Fatalf("preflight: %+v %v", preflight, err)
			}
			if tc.appears {
				writeFile(t, dest, "a.txt", "the operator's own file")
			}
			engine := &scriptedEngine{run: (&fsEngine{s}).Copy}
			if tc.run != nil {
				engine.run = func(_ context.Context, _, _ string, _ []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
					return tc.run(onEvent), nil
				}
			}
			var kept downloadRecords
			report, _ := Download(t.Context(), s, engine, "aeronautes_src:media", "media", "scope/", dest, preflight, nil, kept.recorder())
			got := kept.executed["scope/a.txt"]
			if got.Result != tc.result || got.Effect != tc.effect {
				t.Fatalf("recorded %+v, want %s with effect %s (report %+v)", got, tc.result, tc.effect, report)
			}
			if tc.says != "" && (len(report.Failures) != 1 || !strings.Contains(report.Failures[0].Err, tc.says)) {
				t.Fatalf("failures = %+v, want one that says %q", report.Failures, tc.says)
			}
			if kept.verified["scope/a.txt"] != (tc.result == transfercopy.ResultCopied) || report.EngineStopped != tc.stopped {
				t.Fatalf("second record = %v and engine stopped = %v for a %s file", kept.verified["scope/a.txt"], report.EngineStopped, tc.result)
			}
			if tc.appears && readLocal(t, filepath.Join(dest, "a.txt")) != "the operator's own file" {
				t.Fatal("the operator's own file was replaced")
			}
		})
	}
}

// A name the engine cannot carry is read by the application in one GET into
// a temporary file beside its final name, and given that name by a link that
// does not replace: a file that appeared since the preview is a conflict and
// stays as it is. Bytes that are not the store's tag for a plain object fail
// the file and leave nothing at its name. The temporary is gone afterwards
// whatever happened.
func TestTheApplicationsOwnDownloadIsOneReadAndALinkThatDoesNotReplace(t *testing.T) {
	const name = "scope/a\uff1ab.txt" // the full-width colon is reserved to the engine
	for _, tc := range []struct {
		name    string
		appears bool
		corrupt bool
		holds   string
		result  transfercopy.Result
	}{
		{name: "a free name", holds: "native bytes", result: transfercopy.ResultCopied},
		{name: "a file that appeared since the preview", appears: true, holds: "the operator's own file", result: transfercopy.ResultConflict},
		{name: "bytes that are not the store's tag", corrupt: true, result: transfercopy.ResultFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := session(t)
			seed(mock, "media", name, "native bytes")
			if tc.corrupt {
				right := md5.Sum([]byte("native bytes"))
				mock.PutObject("media", name, swifttest.Object{Bytes: 12, ETag: hex.EncodeToString(right[:]), ContentType: "text/plain", Data: []byte("native BYTES")})
			}
			dest := t.TempDir()
			preflight, err := PreflightDownload(t.Context(), s, "media", "scope/", dest)
			if err != nil || preflight.Rejected() || len(preflight.Items) != 1 || preflight.Items[0].Route != "native" {
				t.Fatalf("preflight: %+v %v", preflight, err)
			}
			final := filepath.Join(dest, "a\uff1ab.txt")
			if tc.appears {
				writeFile(t, dest, "a\uff1ab.txt", "the operator's own file")
			}
			engine := &scriptedEngine{run: (&fsEngine{s}).Copy}
			var kept downloadRecords
			mark := len(mock.RequestLog())
			report, err := Download(t.Context(), s, engine, "aeronautes_src:media", "media", "scope/", dest, preflight, nil, kept.recorder())
			if err != nil || report.NativeRouted != 1 || len(engine.dsts) != 0 {
				t.Fatalf("report = %+v, %v, engine runs %d", report, err, len(engine.dsts))
			}
			if want := []string{"GET /v1/AUTH_test/media/" + name}; !reflect.DeepEqual(since(mock, mark), want) {
				t.Fatalf("requests = %q, want one GET", since(mock, mark))
			}
			want := []string{"a\uff1ab.txt"}
			if tc.holds == "" {
				want = nil
			}
			if got := folderNames(t, dest); !reflect.DeepEqual(got, want) {
				t.Fatalf("the folder holds %q, want %q and no temporary", got, want)
			}
			if tc.holds != "" && readLocal(t, final) != tc.holds {
				t.Fatalf("the file holds %q, want %q", readLocal(t, final), tc.holds)
			}
			if got := kept.executed[name]; got.Result != tc.result {
				t.Fatalf("recorded %+v, want %s", got, tc.result)
			}
		})
	}
}

// The application makes the folders a file needs, one component at a time
// through the held folder, before the engine runs (ADR 0043). A link where a
// folder should be refuses that file, which is never given to the engine,
// and the rest of the batch is downloaded.
func TestAFileWhoseFolderCannotBeMadeIsNotGivenToTheEngine(t *testing.T) {
	s, mock := session(t)
	seed(mock, "media", "scope/a.txt", "alpha")
	seed(mock, "media", "scope/sub/b.txt", "bravo")
	dest := t.TempDir()
	preflight, err := PreflightDownload(t.Context(), s, "media", "scope/", dest)
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(dest, "sub")); err != nil {
		t.Fatal(err)
	}
	engine := &scriptedEngine{run: (&fsEngine{s}).Copy}
	var kept downloadRecords
	report, err := Download(t.Context(), s, engine, "aeronautes_src:media", "media", "scope/", dest, preflight, nil, kept.recorder())
	if err != nil || report.Done != 1 || !reflect.DeepEqual(engine.names, [][]string{{"a.txt"}}) {
		t.Fatalf("report = %+v, %v, engine names %q", report, err, engine.names)
	}
	if got := kept.executed["scope/sub/b.txt"]; got.Result != transfercopy.ResultFailed || got.Effect != transfercopy.EffectNotAttempted {
		t.Fatalf("the file under the link was recorded %+v, want failed and not attempted", got)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("something was written through the link: %v", entries)
	}
}

// A download parks only at the head of a unit (ADR 0104). Cancelled while
// the engine runs, a file the engine reported is downloaded and one it did
// not is unknown; a batch never given to the engine is not attempted.
func TestACancelledDownloadRecordsWhatTheEngineSaid(t *testing.T) {
	s, mock := session(t)
	seed(mock, "media", "scope/a.txt", "alpha")
	seed(mock, "media", "scope/b.txt", "bravo")
	dest := t.TempDir()
	preflight, err := PreflightDownload(t.Context(), s, "media", "scope/", dest)
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	engine := &scriptedEngine{run: func(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
		if _, err := (&fsEngine{s}).Copy(ctx, src, dst, names[:1], onEvent); err != nil {
			return nil, err
		}
		cancel()
		return &rclone.Result{Cancelled: true, ExitCode: 130}, nil
	}}
	var kept downloadRecords
	report, err := Download(ctx, s, engine, "aeronautes_src:media", "media", "scope/", dest, preflight, nil, kept.recorder())
	if !errors.Is(err, context.Canceled) || report.Done != 1 || !report.EngineStopped {
		t.Fatalf("report = %+v, %v", report, err)
	}
	if got := kept.executed["scope/a.txt"]; got.Result != transfercopy.ResultCopied {
		t.Fatalf("the reported file was recorded %+v", got)
	}
	if got := kept.executed["scope/b.txt"]; got.Result != transfercopy.ResultFailed || got.Effect != transfercopy.EffectUnresolved || got.Diagnostic.Code != transfercopy.CodeCancelled {
		t.Fatalf("the unreported file was recorded %+v, want unknown and cancelled", got)
	}
	// Cancelled before it starts: nothing is admitted and no engine runs.
	dest2 := t.TempDir()
	preflight, err = PreflightDownload(t.Context(), s, "media", "scope/", dest2)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, stop := context.WithCancel(t.Context())
	stop()
	idle := &scriptedEngine{run: (&fsEngine{s}).Copy}
	kept = downloadRecords{}
	recorder := kept.recorder()
	if _, err := Download(cancelled, s, idle, "aeronautes_src:media", "media", "scope/", dest2, preflight, nil, recorder); !errors.Is(err, context.Canceled) || len(idle.dsts) != 0 {
		t.Fatalf("a cancelled start ran %d engines: %v", len(idle.dsts), err)
	}
}

// A record that cannot be kept stops the run: nothing further is given to
// the engine.
func TestADownloadRecordThatCannotBeKeptStopsTheRun(t *testing.T) {
	s, mock := session(t)
	seed(mock, "media", "scope/a.txt", "alpha")
	dest := t.TempDir()
	preflight, err := PreflightDownload(t.Context(), s, "media", "scope/", dest)
	if err != nil {
		t.Fatal(err)
	}
	lost := errors.New("the job store is gone")
	engine := &scriptedEngine{run: (&fsEngine{s}).Copy}
	_, err = Download(t.Context(), s, engine, "aeronautes_src:media", "media", "scope/", dest, preflight, nil, &DownloadRecorder{
		Admit:   func(DownloadAdmission) error { return lost },
		Execute: func(DownloadItem, transfercopy.Observation) error { return nil },
		Verify:  func(DownloadItem, transfercopy.Observation) error { return nil },
	})
	if !errors.Is(err, lost) || len(engine.dsts) != 0 {
		t.Fatalf("run ended %v after %d engine runs, want the lost record and none", err, len(engine.dsts))
	}
}

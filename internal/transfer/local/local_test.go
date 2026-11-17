package local

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func session(t *testing.T) (*swiftcontrol.Session, *swifttest.Server) {
	t.Helper()
	return sessionWithInfo(t, "")
}

// sessionWithInfo is session with the cluster's /info document pinned
// before anything serves it; "" serves the mock's default.
func sessionWithInfo(t *testing.T, info string) (*swiftcontrol.Session, *swifttest.Server) {
	t.Helper()
	mock := swifttest.New("test:tester", "testing")
	mock.InfoJSON = info
	srv := httptest.NewServer(mock)
	t.Cleanup(srv.Close)
	s, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: srv.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	mock.AddContainer("media", "standard")
	return s, mock
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func seed(mock *swifttest.Server, container, name, content string) {
	sum := md5.Sum([]byte(content))
	mock.PutObject(container, name, swifttest.Object{
		Bytes: int64(len(content)), ETag: hex.EncodeToString(sum[:]),
		ContentType: "text/plain", Data: []byte(content),
	})
}

// fsEngine is the test engine: local↔mock-swift over the sessions. It
// prints the lines the pinned engine prints (docs/rclone.md): one for each
// file it writes, and, for a download, which it is always told to run with
// the destination's existing files left alone, one for each file it leaves.
type fsEngine struct {
	session *swiftcontrol.Session
}

func (e *fsEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	parse := func(spec string) (container, prefix string, local bool) {
		if !strings.Contains(spec, ":") {
			return "", spec, true
		}
		path := spec[strings.Index(spec, ":")+1:]
		if slash := strings.Index(path, "/"); slash >= 0 {
			return path[:slash], path[slash+1:] + "/", false
		}
		return path, "", false
	}
	srcC, srcP, srcLocal := parse(src)
	dstC, dstP, dstLocal := parse(dst)
	for _, name := range names {
		switch {
		case srcLocal && !dstLocal: // upload
			data, err := os.ReadFile(filepath.Join(srcP, name))
			if err != nil {
				return &rclone.Result{ExitCode: 1}, nil
			}
			if _, err := e.session.PutObject(ctx, dstC, dstP+name, bytes.NewReader(data), "application/octet-stream", nil); err != nil {
				return &rclone.Result{ExitCode: 1}, nil
			}
			// The engine says which file it wrote, and an upload takes
			// its word for each one.
			if onEvent != nil {
				onEvent(rclone.Event{Level: "info", Object: name, Msg: "Copied (new)"})
			}
		case !srcLocal && dstLocal: // download
			target := filepath.Join(dstP, name)
			if _, err := os.Lstat(target); err == nil {
				if onEvent != nil {
					onEvent(rclone.Event{Level: "debug", Object: name, Msg: "Destination exists, skipping"})
				}
				continue
			}
			body, _, err := e.session.OpenObject(ctx, srcC, srcP+name)
			if err != nil {
				return &rclone.Result{ExitCode: 1}, nil
			}
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(body)
			body.Close()
			if err := os.WriteFile(target, buf.Bytes(), 0o644); err != nil {
				return &rclone.Result{ExitCode: 1}, nil
			}
			if onEvent != nil {
				onEvent(rclone.Event{Level: "info", Object: name, Msg: "Copied (new)"})
			}
		}
	}
	return &rclone.Result{}, nil
}

func TestPreflightUploadProblems(t *testing.T) {
	s, mock := session(t)
	ctx := context.Background()
	seed(mock, "media", "docs/taken.txt", "already here")

	dirA, dirB := t.TempDir(), t.TempDir()
	pathA := writeFile(t, dirA, "same.txt", "from A")
	pathB := writeFile(t, dirB, "same.txt", "from B")
	fresh := writeFile(t, dirA, "fresh.txt", "new bytes")
	taken := writeFile(t, dirB, "taken.txt", "would overwrite")
	hostile := writeFile(t, dirA, "line\nbreak.txt", "newline name")

	report, err := PreflightUpload(ctx, s, "media", "docs/",
		[]string{pathA, pathB, fresh, taken, hostile, filepath.Join(dirA, "missing.txt")})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, p := range report.Problems {
		kinds[p.Kind] = true
		if !p.Reject {
			t.Errorf("upload problem %q should reject", p.Kind)
		}
	}
	for _, want := range []string{"duplicate", "exists", "unreadable"} {
		if !kinds[want] {
			t.Errorf("missing problem kind %q: %+v", want, report.Problems)
		}
	}
	if !report.Rejected() {
		t.Error("preflight with collisions not rejected")
	}
	// The clean and hostile files still classified.
	routes := map[string]string{}
	for _, item := range report.Items {
		routes[item.Name] = item.Route
	}
	if routes["docs/fresh.txt"] != "rclone" || routes["docs/line\nbreak.txt"] != "native" {
		t.Errorf("routes = %v", routes)
	}

	// Execution refuses a rejected preflight outright.
	if _, err := Upload(ctx, s, &fsEngine{s}, "aeronautes_dst:media", "media", report, nil, nil); err == nil {
		t.Error("rejected preflight executed")
	}
}

func TestPreflightDownloadRejections(t *testing.T) {
	s, mock := session(t)
	ctx := context.Background()
	for name, content := range map[string]string{
		"scope/../escape":  "dotdot",
		"scope/ok.txt":     "fine",
		"scope/foo":        "file",
		"scope/foo/child":  "needs foo as a dir",
		"scope/dir/":       "",
		"scope/full/":      "not empty",
		"scope/Readme.txt": "one case",
		"scope/readme.txt": "other case",
		"scope/taken.txt":  "clash below",
	} {
		seed(mock, "media", name, content)
	}
	destDir := t.TempDir()
	writeFile(t, destDir, "taken.txt", "local already")

	report, err := PreflightDownload(ctx, s, "media", "scope/", destDir)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	warned := map[string]bool{}
	for _, p := range report.Problems {
		if p.Reject {
			found[p.Kind] = true
		} else {
			warned[p.Kind] = true
		}
	}
	for _, want := range []string{"path-escape", "file-vs-prefix", "unmaterializable", "local-exists", "case-fold"} {
		if !found[want] {
			t.Errorf("missing rejection %q: %+v", want, report.Problems)
		}
	}
	_ = warned
	if !report.Rejected() {
		t.Error("collisions not rejected")
	}
	// The folder marker became a directory item.
	var dirItem bool
	for _, item := range report.Items {
		if item.Dir && item.RelPath == "dir" {
			dirItem = true
		}
	}
	if !dirItem {
		t.Errorf("folder marker not planned as a directory: %+v", report.Items)
	}
}

func TestUploadExecutes(t *testing.T) {
	s, mock := session(t)
	ctx := context.Background()
	dirA, dirB := t.TempDir(), t.TempDir()
	a := writeFile(t, dirA, "a.txt", "alpha bytes")
	b := writeFile(t, dirB, "b.txt", "bravo")
	hostile := writeFile(t, dirA, "line\nname", "hostile upload")

	report, err := PreflightUpload(ctx, s, "media", "up/", []string{a, b, hostile})
	if err != nil || report.Rejected() {
		t.Fatalf("preflight: %v %+v", err, report.Problems)
	}
	result, err := Upload(ctx, s, &fsEngine{s}, "aeronautes_dst:media", "media", report, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Done != 3 || result.NativeRouted != 1 || len(result.Failures) != 0 {
		t.Fatalf("result = %+v", result)
	}
	for name, want := range map[string]string{"up/a.txt": "alpha bytes", "up/b.txt": "bravo", "up/line\nname": "hostile upload"} {
		d, err := s.ObjectDetail(ctx, "media", name)
		if err != nil || d.ContentLength != int64(len(want)) {
			t.Errorf("%q: %v %+v", name, err, d)
		}
	}
	_ = mock
}

func TestDownloadExecutes(t *testing.T) {
	s, mock := session(t)
	ctx := context.Background()
	seed(mock, "media", "snap/plain.txt", "plain content")
	seed(mock, "media", "snap/nested/deep.bin", "deep bytes!")
	seed(mock, "media", "snap/folder/", "")
	seed(mock, "media", "snap/we ird/name", "hostile dl") // space → native-ish oddity

	destDir := t.TempDir()
	report, err := PreflightDownload(ctx, s, "media", "snap/", destDir)
	if err != nil {
		t.Fatal(err)
	}
	if report.Rejected() {
		t.Fatalf("unexpected rejection: %+v", report.Problems)
	}
	result, err := Download(ctx, s, &fsEngine{s}, "aeronautes_src:media", "media", "snap/", destDir, report, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The uniform namespace policy now rejects every rclone-unsafe name
	// at preflight, so nothing reaches the native download route in v1
	// (its code stays for §65.10's per-target policies).
	if result.Done != 4 || result.NativeRouted != 0 || len(result.Failures) != 0 {
		t.Fatalf("result = %+v (failures %+v)", result, result.Failures)
	}
	for rel, want := range map[string]string{
		"plain.txt":       "plain content",
		"nested/deep.bin": "deep bytes!",
		"we ird/name":     "hostile dl",
	} {
		data, err := os.ReadFile(filepath.Join(destDir, rel))
		if err != nil || string(data) != want {
			t.Errorf("%s: %v %q", rel, err, data)
		}
	}
	if info, err := os.Stat(filepath.Join(destDir, "folder")); err != nil || !info.IsDir() {
		t.Errorf("folder marker not a directory: %v", err)
	}
}

func TestPreflightRejectsCrossPlatformHazards(t *testing.T) {
	// §65.9 item 13: one namespace policy for every target — backslash
	// components (Windows separators), empty segments (local-path
	// collapse), control characters, and a directory sitting at a file
	// target all REJECT on every platform.
	session, mock := session(t)
	seed(mock, "media", "scope/evil\\component", "x")
	seed(mock, "media", "scope/a//b", "x")
	seed(mock, "media", "scope/ctl\x01name", "x")
	seed(mock, "media", "scope/dirhit", "x")
	dest := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dest, "dirhit"), 0o755); err != nil {
		t.Fatal(err)
	}
	report, err := PreflightDownload(context.Background(), session, "media", "scope/", dest)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, p := range report.Problems {
		if p.Reject {
			kinds[p.Kind]++
		}
	}
	if kinds["path-escape"] == 0 || kinds["collision"] == 0 || kinds["unmaterializable"] == 0 || kinds["local-exists"] == 0 {
		t.Fatalf("hazards not all rejected: %+v", report.Problems)
	}
	if !report.Rejected() {
		t.Fatal("report not rejected overall")
	}
}

// The application's own download gives the final name only to a whole read
// (ADR 0122 decision 5): a body the store stops sending part way, or one a
// cancel ends, leaves nothing at the name and no temporary beside it.
func TestNativeDownloadNeverLeavesPartials(t *testing.T) {
	const name, body = "scope/n.bin", "complete-content"
	for _, tc := range []struct {
		name   string
		cancel bool
		code   transfercopy.Code
	}{
		{name: "the store stops part way", code: transfercopy.CodeIO},
		{name: "a cancel part way", cancel: true, code: transfercopy.CodeCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := swifttest.New("test:tester", "testing")
			mock.AddContainer("media", "standard")
			seed(mock, "media", name, body)
			dest := t.TempDir()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			// The headers promise every byte and half are sent. The read is
			// ended only once the temporary exists, so the case is about a
			// read the application had begun writing.
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/media/"+name) {
					mock.ServeHTTP(w, r)
					return
				}
				w.Header().Set("Content-Length", fmt.Sprint(len(body)))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(body[:len(body)/2]))
				w.(http.Flusher).Flush()
				for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
					if entries, _ := os.ReadDir(filepath.Join(dest, "deep")); len(entries) == 1 && strings.HasPrefix(entries[0].Name(), downloadPartPrefix) {
						break
					}
				}
				if tc.cancel {
					cancel()
					<-r.Context().Done()
				}
				panic(http.ErrAbortHandler)
			}))
			t.Cleanup(server.Close)
			s, err := swiftcontrol.New(swiftcontrol.Config{AuthURL: server.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing"})
			if err != nil {
				t.Fatal(err)
			}
			item := DownloadItem{Name: name, RelPath: filepath.Join("deep", "n.bin"), Size: int64(len(body)), Route: "native"}
			outcome := nativeDownload(ctx, s, "media", item, testDestination(t, dest), false)
			if outcome.Result != transfercopy.ResultFailed || outcome.Effect != transfercopy.EffectNotAttempted || outcome.Diagnostic.Code != tc.code || outcome.verification != nil {
				t.Fatalf("a part read was recorded %+v (%v), want failed, not attempted, %s", outcome.Observation, outcome.err(), tc.code)
			}
			if entries, err := os.ReadDir(filepath.Join(dest, "deep")); err != nil || len(entries) != 0 {
				t.Fatalf("a part read left %v in the folder (%v), want nothing at the name and no temporary", entries, err)
			}
			// The folder made for the file stays, and is reported (ADR 0043).
			if !reflect.DeepEqual(outcome.created, []string{"deep"}) {
				t.Fatalf("the folders made were reported as %q, want the one made", outcome.created)
			}
		})
	}
}

func TestDirMarkerEscapesAndAncestorConflicts(t *testing.T) {
	// Follow-up review: a zero-byte "scope/../" folder marker must not
	// become MkdirAll outside the destination, and file-vs-prefix must
	// catch non-adjacent pairs like foo / foo.txt / foo/child.
	s, mock := session(t)
	mock.PutObject("media", "scope/../", swifttest.Object{Bytes: 0, ETag: "d41d8cd98f00b204e9800998ecf8427e"})
	mock.PutObject("media", "scope/../pwned/", swifttest.Object{Bytes: 0, ETag: "d41d8cd98f00b204e9800998ecf8427e"})
	seed(mock, "media", "scope/foo", "a file")
	seed(mock, "media", "scope/foo.txt", "between")
	seed(mock, "media", "scope/foo/child", "conflict")

	dest := t.TempDir()
	report, err := PreflightDownload(context.Background(), s, "media", "scope/", dest)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, p := range report.Problems {
		if p.Reject {
			kinds[p.Kind]++
		}
	}
	if kinds["path-escape"] < 2 {
		t.Fatalf("dot-segment folder markers not rejected: %+v", report.Problems)
	}
	if kinds["file-vs-prefix"] == 0 {
		t.Fatalf("non-adjacent file-vs-prefix missed: %+v", report.Problems)
	}
	// No directory item may point outside the destination.
	for _, item := range report.Items {
		if item.Dir && strings.Contains(item.RelPath, "..") {
			t.Fatalf("escaping directory item survived: %+v", item)
		}
	}
}

// ADR 0122 decision 3: the engine is pointed at the destination folder itself,
// so every file is written at its own name; there is no stage name.
func TestLocalUploadGivesTheEngineTheDestinationItself(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("dst", "standard")
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "alpha")
	writeFile(t, dir, "b.txt", "bravo")
	pre, err := PreflightUpload(context.Background(), s, "dst", "up/",
		[]string{filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")})
	if err != nil || pre.Rejected() {
		t.Fatalf("preflight: %v %+v", err, pre)
	}
	rec := &recordingLocalEngine{inner: &fsEngine{s}}
	if _, err := Upload(context.Background(), s, rec, "aeronautes_dst:dst", "dst", pre, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(rec.dsts) != 1 || rec.dsts[0] != "aeronautes_dst:dst/up" {
		t.Fatalf("engine destinations = %q, want the destination folder once", rec.dsts)
	}
}

type recordingLocalEngine struct {
	inner Engine
	dsts  []string
}

func (r *recordingLocalEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	r.dsts = append(r.dsts, dst)
	return r.inner.Copy(ctx, src, dst, names, onEvent)
}

// The preflight used to HEAD every selected file's destination name, so a
// selection of N files cost N requests before a byte was written, and a
// large one cost more than the transfer it preceded (K-PERF-001). It now
// resolves the whole selection with whichever pattern costs fewer requests,
// and this pins the count so the choice cannot quietly regress.
func TestPreflightUploadDoesNotHeadEveryFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		// alreadyThere is how many objects the destination holds, which is
		// what makes one pattern cheaper than the other.
		alreadyThere int
		files        int
		// wantHeads is how many object HEADs the preflight may issue.
		wantHeads int
	}{
		{name: "many files into a small destination", alreadyThere: 3, files: 40, wantHeads: 0},
		// One name is settled by name: a listing pass costs a page and the
		// empty one that ends it, so it cannot beat a single HEAD.
		{name: "one file into a small destination", alreadyThere: 3, files: 1, wantHeads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := session(t)
			for i := range tc.alreadyThere {
				seed(mock, "media", fmt.Sprintf("docs/there-%03d", i), "already here")
			}
			dir := t.TempDir()
			var paths []string
			for i := range tc.files {
				paths = append(paths, writeFile(t, dir, fmt.Sprintf("send-%03d", i), "bytes"))
			}
			before := len(mock.RequestLog())
			report, err := PreflightUpload(t.Context(), s, "media", "docs/", paths)
			if err != nil || report == nil || report.Rejected() || len(report.Items) != tc.files {
				t.Fatalf("preflight: %+v %v", report, err)
			}
			var heads, listings int
			for _, request := range mock.RequestLog()[before:] {
				switch {
				case strings.HasPrefix(request, "HEAD /v1/AUTH_test/media/"):
					heads++
				case strings.HasPrefix(request, "GET /v1/AUTH_test/media?"):
					listings++
				}
			}
			if heads != tc.wantHeads {
				t.Errorf("the preflight issued %d object HEADs for %d files, want %d", heads, tc.files, tc.wantHeads)
			}
			if tc.wantHeads == 0 && listings == 0 {
				t.Error("no listing either, so nothing resolved the collisions")
			}
		})
	}
}

// The collisions themselves must still be found, whichever pattern is used,
// and the weaker of the two must never report a free name as live.
func TestPreflightUploadFindsCollisionsEitherWay(t *testing.T) {
	for _, files := range []int{1, 40} {
		t.Run(fmt.Sprint(files, " files"), func(t *testing.T) {
			s, mock := session(t)
			dir := t.TempDir()
			var paths []string
			for i := range files {
				name := fmt.Sprintf("send-%03d", i)
				paths = append(paths, writeFile(t, dir, name, "bytes"))
				if i%2 == 0 {
					seed(mock, "media", "docs/"+name, "already here")
				}
			}
			report, err := PreflightUpload(t.Context(), s, "media", "docs/", paths)
			if err != nil {
				t.Fatal(err)
			}
			var collisions int
			for _, problem := range report.Problems {
				if problem.Kind == "exists" {
					collisions++
				}
			}
			if want := (files + 1) / 2; collisions != want {
				t.Fatalf("found %d collisions, want %d: %+v", collisions, want, report.Problems)
			}
		})
	}
}

// Not knowing whether a name is live is not the same as knowing it is free.
// The per-file loop used to refuse the whole preflight when its HEAD failed
// for any reason other than not-found, and that refusal had to move to
// wherever the request is now made. Both patterns are shown refusing,
// because a selection that could not be resolved must not be published as
// though the destination were empty.
func TestPreflightUploadRefusesWhatItCouldNotResolve(t *testing.T) {
	for _, tc := range []struct {
		name string
		// files is chosen so the resolver takes the pattern under test.
		files int
		arm   func(mock *swifttest.Server)
	}{
		// A selection of one is settled by name, so its refusal is the HEAD's;
		// a wider one lists, so its refusal is the listing's. Both paths are
		// exercised, each by a selection that reaches it.
		{name: "a head that failed", files: 1, arm: func(mock *swifttest.Server) { mock.FailNextObjectHEADs(1) }},
		{name: "a listing that stopped", files: 40, arm: func(mock *swifttest.Server) { mock.FailNextListings(1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := session(t)
			seed(mock, "media", "docs/send-000", "already here")
			dir := t.TempDir()
			var paths []string
			for i := range tc.files {
				paths = append(paths, writeFile(t, dir, fmt.Sprintf("send-%03d", i), "bytes"))
			}
			tc.arm(mock)
			report, err := PreflightUpload(t.Context(), s, "media", "docs/", paths)
			if err == nil {
				t.Fatalf("an unresolved destination was admitted: %+v", report)
			}
			if report != nil {
				t.Errorf("a refused preflight returned a report: %+v", report)
			}
		})
	}
}

// A path that can never become an item is this report's own finding, and it
// must survive a destination that cannot be resolved. Hoisting the liveness
// lookup above os.Stat made a missing file's "unreadable" finding disappear
// into a remote error, and turned the report into nothing at all.
func TestPreflightUploadKeepsLocalFindingsWhenTheDestinationFails(t *testing.T) {
	for _, entry := range []struct {
		name      string
		preflight func(ctx context.Context, s *swiftcontrol.Session, paths []string) (*UploadPreflight, error)
	}{
		{name: "creating", preflight: func(ctx context.Context, s *swiftcontrol.Session, paths []string) (*UploadPreflight, error) {
			return PreflightUpload(ctx, s, "media", "docs/", paths)
		}},
		{name: "replacing", preflight: func(ctx context.Context, s *swiftcontrol.Session, paths []string) (*UploadPreflight, error) {
			return PreflightUploadReplacing(ctx, s, "media", "docs/", paths)
		}},
	} {
		for _, tc := range []struct {
			name, kind string
			// make returns the one selected path and how many others join it.
			make func(t *testing.T, dir string) string
		}{
			{name: "a missing file", kind: "unreadable", make: func(t *testing.T, dir string) string {
				return filepath.Join(dir, "missing.txt")
			}},
			{name: "a directory", kind: "not-a-file", make: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, "adir")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				return path
			}},
		} {
			t.Run(entry.name+", "+tc.name, func(t *testing.T) {
				s, mock := session(t)
				dir := t.TempDir()
				paths := []string{tc.make(t, dir)}
				// Every remaining request fails, so a report that depends on
				// one cannot be produced at all.
				mock.FailNextObjectHEADs(50)
				mock.FailNextListings(50)
				before := len(mock.RequestLog())
				report, err := entry.preflight(t.Context(), s, paths)
				if err != nil || report == nil {
					t.Fatalf("a local finding was replaced by a remote failure: %+v %v", report, err)
				}
				if len(report.Problems) != 1 || report.Problems[0].Kind != tc.kind || !report.Problems[0].Reject {
					t.Fatalf("the report does not carry the local finding: %+v", report.Problems)
				}
				if len(report.Items) != 0 {
					t.Errorf("a path that cannot be read became an item: %+v", report.Items)
				}
				// And nothing was asked of the destination on its behalf.
				for _, request := range mock.RequestLog()[before:] {
					if strings.Contains(request, "/media") {
						t.Errorf("an ineligible path cost a destination request: %s", request)
					}
				}
			})
		}
	}
}

// What decides now is the listing itself rather than the container's count,
// which is only an upper bound for the prefix and so made a small prefix in a
// large container look expensive. A selection at or below the floor is settled
// by name; a wider one lists and is abandoned only if the prefix turns out to
// hold more rows than there are names to settle. The costs of each are
// asserted in TestDestinationLivenessChoosesAndBudgets.
func TestPreflightUploadChoosesTheCheaperEstimate(t *testing.T) {
	for _, tc := range []struct {
		name         string
		alreadyThere int
		files        int
		// wantHeads is how many object HEADs the preflight may issue: zero
		// when the listing is estimated cheaper, one per file otherwise.
		wantHeads int
	}{
		{name: "two files stay at the floor", alreadyThere: 3, files: 2, wantHeads: 2},
		{name: "three files list", alreadyThere: 3, files: 3, wantHeads: 0},
		{name: "a single file", alreadyThere: 3, files: 1, wantHeads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := session(t)
			for i := range tc.alreadyThere {
				seed(mock, "media", fmt.Sprintf("docs/there-%03d", i), "already here")
			}
			dir := t.TempDir()
			var paths []string
			for i := range tc.files {
				paths = append(paths, writeFile(t, dir, fmt.Sprintf("send-%03d", i), "bytes"))
			}
			before := len(mock.RequestLog())
			if _, err := PreflightUpload(t.Context(), s, "media", "docs/", paths); err != nil {
				t.Fatal(err)
			}
			var heads int
			for _, request := range mock.RequestLog()[before:] {
				if strings.HasPrefix(request, "HEAD /v1/AUTH_test/media/") {
					heads++
				}
			}
			if heads != tc.wantHeads {
				t.Errorf("issued %d object HEADs for %d files over %d objects, want %d",
					heads, tc.files, tc.alreadyThere, tc.wantHeads)
			}
		})
	}
}

// Container listings lag deletes as well as writes (DESIGN section 15.3), so
// a listing can name an object that is gone. Both outcomes are conservative
// and neither can become an unconfirmed overwrite, which is why this is
// documented and covered rather than defended against.
func TestPreflightUploadTreatsAStaleListingRowConservatively(t *testing.T) {
	for _, tc := range []struct {
		name      string
		replacing bool
	}{
		{name: "creating refuses a collision that has gone"},
		{name: "replacing marks it as one it would replace", replacing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := session(t)
			// Enough rows that the listing is the cheaper estimate, and one
			// row naming an object the store does not hold.
			for i := range 3 {
				seed(mock, "media", fmt.Sprintf("docs/there-%03d", i), "already here")
			}
			mock.StaleListingRows = map[string][]string{"media": {"docs/send-000"}}
			dir := t.TempDir()
			var paths []string
			for i := range 3 {
				paths = append(paths, writeFile(t, dir, fmt.Sprintf("send-%03d", i), "bytes"))
			}
			// The name really is absent, whatever the listing says.
			if _, err := s.ObjectDetail(t.Context(), "media", "docs/send-000"); !swiftcontrol.IsNotFound(err) {
				t.Fatalf("the fixture's stale row names a live object: %v", err)
			}
			report, err := preflightUpload(t.Context(), s, "media", "docs/", SourcesByBaseName(paths), tc.replacing)
			if err != nil {
				t.Fatal(err)
			}
			var exists int
			for _, problem := range report.Problems {
				if problem.Kind == "exists" {
					exists++
					if problem.Name != "docs/send-000" {
						t.Errorf("the wrong name was called a collision: %+v", problem)
					}
					if problem.Reject != !tc.replacing {
						t.Errorf("a stale collision rejects = %v on the replacing = %v path", problem.Reject, tc.replacing)
					}
				}
			}
			if exists != 1 {
				t.Fatalf("the stale row produced %d collisions: %+v", exists, report.Problems)
			}
			var replaces int
			for _, item := range report.Items {
				if item.Replaces {
					replaces++
				}
			}
			// Creating refuses it, so it is not an item at all; replacing
			// admits it marked, which publishes rather than destroys.
			if tc.replacing && (len(report.Items) != 3 || replaces != 1) {
				t.Errorf("the replacing path did not admit it marked: items=%d replaces=%d", len(report.Items), replaces)
			}
			if !tc.replacing && (len(report.Items) != 2 || replaces != 0) {
				t.Errorf("the creating path did not refuse it: items=%d replaces=%d", len(report.Items), replaces)
			}
		})
	}
}

// countingCancel is a context that answers Err nil for the first `passes`
// questions and cancelled thereafter, counting every question. It observes
// the scan from OUTSIDE, through the public entry points, so the production
// code needs no seam: if the loop stops when told, it asks exactly passes+1
// times, and if it carries on it asks more.
//
// A context is asked by whatever holds it, which for the uncancelled control
// includes the listing iterator's own goroutine and the client underneath it,
// so the counter is guarded. The first version was not, and the race detector
// said so: a racy observation is not an observation.
type countingCancel struct {
	context.Context
	mu     sync.Mutex
	passes int
	asked  int
}

func (c *countingCancel) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked++
	if c.asked <= c.passes {
		return nil
	}
	return context.Canceled
}

func (c *countingCancel) Done() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.asked <= c.passes {
		return nil
	}
	closed := make(chan struct{})
	close(closed)
	return closed
}

// questions is how many times the context has been asked.
func (c *countingCancel) questions() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.asked
}

// Deciding everything local before touching the destination removed the
// context-aware request that used to end the selection loop early. A
// cancelled start must still stop between candidates rather than stat every
// remaining path, which is what the large selections this change supports
// would otherwise do on a slow mount.
func TestPreflightUploadStopsScanningWhenCancelled(t *testing.T) {
	for _, entry := range []struct {
		name      string
		preflight func(ctx context.Context, s *swiftcontrol.Session, paths []string) (*UploadPreflight, error)
		// wantItems is how many of the three the ordinary report admits: the
		// creating path refuses the collision, the replacing path marks it.
		wantItems int
	}{
		{name: "creating", wantItems: 2, preflight: func(ctx context.Context, s *swiftcontrol.Session, paths []string) (*UploadPreflight, error) {
			return PreflightUpload(ctx, s, "media", "docs/", paths)
		}},
		{name: "replacing", wantItems: 3, preflight: func(ctx context.Context, s *swiftcontrol.Session, paths []string) (*UploadPreflight, error) {
			return PreflightUploadReplacing(ctx, s, "media", "docs/", paths)
		}},
	} {
		for _, tc := range []struct {
			name string
			// passes is how many candidates the scan is allowed before the
			// context reports cancelled; wantAsked is how many times the scan
			// may ask, which is one per candidate reached. It is only exact
			// while the scan is the only thing running: once the destination
			// is reached the transport consults the context too, which is why
			// the uncancelled control does not count.
			passes, wantAsked int
			cancelled         bool
		}{
			{name: "cancelled at entry", passes: 0, wantAsked: 1, cancelled: true},
			{name: "cancelled after the first candidate", passes: 1, wantAsked: 2, cancelled: true},
			{name: "not cancelled at all", passes: 99, wantAsked: 3},
		} {
			t.Run(entry.name+", "+tc.name, func(t *testing.T) {
				s, mock := session(t)
				seed(mock, "media", "docs/send-001", "already here")
				dir := t.TempDir()
				var paths []string
				for i := range 3 {
					paths = append(paths, writeFile(t, dir, fmt.Sprintf("send-%03d", i), "bytes"))
				}
				ctx := &countingCancel{Context: t.Context(), passes: tc.passes}
				before := len(mock.RequestLog())
				report, err := entry.preflight(ctx, s, paths)
				if tc.cancelled {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("a cancelled scan did not report it: %+v %v", report, err)
					}
					if report != nil {
						t.Errorf("a cancelled scan returned a report: %+v", report)
					}
					// It stopped where it was told: one question per candidate
					// reached, and no more.
					if asked := ctx.questions(); asked != tc.wantAsked {
						t.Errorf("the scan asked %d times, want %d, so it did not stop between candidates", asked, tc.wantAsked)
					}
					if got := len(mock.RequestLog()) - before; got != 0 {
						t.Errorf("a cancelled scan made %d destination requests", got)
					}
					return
				}
				// The control: the ordinary report, unchanged.
				if err != nil || report == nil || len(report.Items) != entry.wantItems || len(report.Problems) != 1 {
					t.Fatalf("the uncancelled report changed: %+v %v", report, err)
				}
				if report.Problems[0].Kind != "exists" || report.Problems[0].Name != "docs/send-001" {
					t.Errorf("the control lost its collision: %+v", report.Problems)
				}
				// The scan asked at least once per candidate. It is not an
				// exact count here, because the destination requests this
				// control does reach consult the context as well.
				if asked := ctx.questions(); asked < tc.wantAsked {
					t.Errorf("the scan asked %d times, fewer than one per candidate (%d)", asked, tc.wantAsked)
				}
			})
		}
	}
}

// The same budget through the PUBLIC routes, both of them, because a review's
// regression was reachable there: a hundred selected names against a prefix of
// two hundred rows the server returns in one response must cost that response
// and the empty page after it, not a HEAD per name. The replacing route is
// included because it admits live names rather than refusing them, so its
// resolution runs over a destination where the selected names ARE there.
func TestPreflightUploadBudgetsOneResponse(t *testing.T) {
	for _, mode := range []struct {
		name string
		run  func(t *testing.T, s *swiftcontrol.Session, paths []string) error
	}{
		{name: "creating", run: func(t *testing.T, s *swiftcontrol.Session, paths []string) error {
			report, err := PreflightUpload(t.Context(), s, "media", "docs/", paths)
			if err != nil || report == nil {
				return fmt.Errorf("preflight: %+v %w", report, err)
			}
			return nil
		}},
		{name: "replacing", run: func(t *testing.T, s *swiftcontrol.Session, paths []string) error {
			report, err := PreflightUploadReplacing(t.Context(), s, "media", "docs/", paths)
			if err != nil || report == nil {
				return fmt.Errorf("replacing preflight: %+v %w", report, err)
			}
			return nil
		}},
	} {
		for _, fixture := range []struct {
			name string
			live bool
		}{{name: "none of the selection is there"}, {name: "half of it is, at the end", live: true}} {
			t.Run(mode.name+"/"+fixture.name, func(t *testing.T) {
				s, mock := session(t)
				for i := range 150 {
					seed(mock, "media", fmt.Sprintf("docs/there-%04d", i), "already here")
				}
				dir := t.TempDir()
				var paths []string
				for i := range 100 {
					paths = append(paths, writeFile(t, dir, fmt.Sprintf("send-%04d", i), "bytes"))
				}
				if fixture.live {
					for i := 50; i < 100; i++ {
						seed(mock, "media", fmt.Sprintf("docs/send-%04d", i), "already here")
					}
				}
				before := len(mock.RequestLog())
				if err := mode.run(t, s, paths); err != nil {
					t.Fatal(err)
				}
				var heads, listings int
				for _, request := range mock.RequestLog()[before:] {
					switch {
					case strings.HasPrefix(request, "HEAD /v1/AUTH_test/media/"):
						heads++
					case strings.HasPrefix(request, "GET /v1/AUTH_test/media?"):
						listings++
					}
				}
				if heads != 0 || listings != 2 {
					t.Errorf("cost %d object HEADs and %d listings, want 0 and 2", heads, listings)
				}
			})
		}
	}
}

// The resolution's own decision, in REQUESTS. A review showed the cost that
// matters is responses, not rows: the client fetches and validates a whole
// response before it yields the first row, so a pass that stops part way
// through one throws away answers already paid for and then pays a HEAD for
// each of them. Every case here asserts a request budget.
func TestDestinationLivenessChoosesAndBudgets(t *testing.T) {
	names := func(n int) []string {
		var out []string
		for i := range n {
			out = append(out, fmt.Sprintf("docs/send-%04d", i))
		}
		return out
	}
	count := func(log []string) (heads, listings int) {
		for _, request := range log {
			switch {
			case strings.HasPrefix(request, "HEAD /v1/AUTH_test/media/"):
				heads++
			case strings.HasPrefix(request, "GET /v1/AUTH_test/media?"):
				listings++
			}
		}
		return heads, listings
	}
	seedRows := func(mock *swifttest.Server, n int) {
		for i := range n {
			seed(mock, "media", fmt.Sprintf("docs/there-%04d", i), "already here")
		}
	}
	t.Run("one response that answers everything costs one more", func(t *testing.T) {
		// That review's own case: a hundred selected names, none of them there, and
		// a prefix of two hundred rows the server returns in ONE response. The
		// rows are already paid for, so they are all read, and the only further
		// request is the empty page that ends the listing.
		s, mock := session(t)
		seedRows(mock, 200)
		before := len(mock.RequestLog())
		live, err := destinationLiveness(t.Context(), s, "media", "docs/", names(100))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names(100) {
			if live[name] {
				t.Fatalf("%s is not there and was reported live", name)
			}
		}
		heads, listings := count(mock.RequestLog()[before:])
		if heads != 0 || listings != 2 {
			t.Errorf("cost %d HEADs and %d listings, want 0 and 2", heads, listings)
		}
	})
	t.Run("names that appear late in that response are still found", func(t *testing.T) {
		// The same shape with fifty of the selected names live at the END of
		// the listing: a pass that stopped early would miss them and then have
		// to ask for each by name.
		s, mock := session(t)
		seedRows(mock, 150)
		for _, name := range names(100)[50:] {
			seed(mock, "media", name, "already here")
		}
		before := len(mock.RequestLog())
		live, err := destinationLiveness(t.Context(), s, "media", "docs/", names(100))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names(100)[50:] {
			if !live[name] {
				t.Errorf("%s is live at the end of the listing and was not found", name)
			}
		}
		heads, listings := count(mock.RequestLog()[before:])
		if heads != 0 || listings != 2 {
			t.Errorf("cost %d HEADs and %d listings, want 0 and 2", heads, listings)
		}
	})
	t.Run("a page exactly at the limit does not change the cost", func(t *testing.T) {
		// The shape of a page decided this, and a prefix of exactly one
		// full page swung the cost from two requests to a hundred and one. Ten
		// thousand rows and one fewer must cost the same, because neither says
		// anything about what follows.
		for _, rows := range []int{listingPageSize - 1, listingPageSize} {
			t.Run(fmt.Sprint(rows), func(t *testing.T) {
				s, mock := session(t)
				seedRows(mock, rows)
				before := len(mock.RequestLog())
				live, err := destinationLiveness(t.Context(), s, "media", "docs/", names(100))
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range names(100) {
					if live[name] {
						t.Fatalf("%s is not there and was reported live", name)
					}
				}
				heads, listings := count(mock.RequestLog()[before:])
				if heads != 0 || listings != 2 {
					t.Errorf("%d rows cost %d HEADs and %d listings, want 0 and 2", rows, heads, listings)
				}
			})
		}
	})
	t.Run("a capped server is bounded by the selection", func(t *testing.T) {
		// One row per response, which ADR 0021 permits, and a prefix far larger
		// than the selection. The pass must stop on its own budget rather than
		// reading the prefix out one row at a time.
		s, mock := session(t)
		mock.ListingPageCap = 1
		seedRows(mock, 500)
		before := len(mock.RequestLog())
		live, err := destinationLiveness(t.Context(), s, "media", "docs/",
			append(names(2), "docs/absent"))
		if err != nil {
			t.Fatal(err)
		}
		if live["docs/send-0000"] || live["docs/absent"] {
			t.Fatalf("nothing selected is there: %+v", live)
		}
		heads, listings := count(mock.RequestLog()[before:])
		if listings != 2 || heads != 3 {
			t.Errorf("a capped server cost %d listings and %d HEADs, want 2 and 3", listings, heads)
		}
	})
	t.Run("the whole resolution stays bounded by the selection", func(t *testing.T) {
		// The general promise: however the server pages, the resolution costs
		// about twice the selection at worst, never the prefix.
		s, mock := session(t)
		mock.ListingPageCap = 1
		seedRows(mock, 5000)
		before := len(mock.RequestLog())
		if _, err := destinationLiveness(t.Context(), s, "media", "docs/", names(10)); err != nil {
			t.Fatal(err)
		}
		heads, listings := count(mock.RequestLog()[before:])
		if total, bound := heads+listings, 2*10; total > bound {
			t.Errorf("ten names cost %d requests (%d listings, %d HEADs), want at most %d",
				total, listings, heads, bound)
		}
	})
	t.Run("a selection at the floor never lists", func(t *testing.T) {
		s, mock := session(t)
		seedRows(mock, 4)
		seed(mock, "media", "docs/send-0000", "already here")
		before := len(mock.RequestLog())
		live, err := destinationLiveness(t.Context(), s, "media", "docs/", names(2))
		if err != nil {
			t.Fatal(err)
		}
		if !live["docs/send-0000"] || live["docs/send-0001"] {
			t.Fatalf("liveness is wrong: %+v", live)
		}
		heads, listings := count(mock.RequestLog()[before:])
		if listings != 0 || heads != 2 {
			t.Errorf("a two-name selection cost %d listings and %d HEADs, want 0 and 2", listings, heads)
		}
	})
	t.Run("a short non-empty page is not exhaustion", func(t *testing.T) {
		// The store answers with fewer rows than the limit and still has more,
		// which ADR 0021 permits. A short page must never be read as the end
		// of the prefix: the selection here is wide enough that the budget
		// never stops the pass, so it runs to the empty page and settles
		// everything from the listing.
		s, mock := session(t)
		mock.ListingPageCap = 1
		seedRows(mock, 3)
		seed(mock, "media", "docs/send-0005", "already here")
		before := len(mock.RequestLog())
		live, err := destinationLiveness(t.Context(), s, "media", "docs/", names(12))
		if err != nil {
			t.Fatal(err)
		}
		if !live["docs/send-0005"] {
			t.Error("a live name in the last row before the empty page was missed")
		}
		heads, listings := count(mock.RequestLog()[before:])
		// Four rows one at a time and then the empty page, with no HEAD: the
		// pass was never stopped, so no short page ended anything.
		if heads != 0 || listings != 5 {
			t.Errorf("cost %d HEADs and %d listings, want 0 and 5", heads, listings)
		}
	})
	t.Run("no listing request is issued twice", func(t *testing.T) {
		s, mock := session(t)
		mock.ListingPageCap = 2
		seedRows(mock, 8)
		before := len(mock.RequestLog())
		if _, err := destinationLiveness(t.Context(), s, "media", "docs/", names(4)); err != nil {
			t.Fatal(err)
		}
		seen := map[string]int{}
		for _, request := range mock.RequestLog()[before:] {
			if strings.HasPrefix(request, "GET /v1/AUTH_test/media?") {
				seen[request]++
			}
		}
		for request, times := range seen {
			if times > 1 {
				t.Errorf("the same listing request was issued %d times: %s", times, request)
			}
		}
	})
	t.Run("a folder's names are listed under the folder, not the request's prefix", func(t *testing.T) {
		// A folder pick's names all sit under the folder and carry further
		// slashes. Whatever else the prefix holds is not theirs to pay for,
		// and a view grouped by delimiter would name no object below the folder.
		s, mock := session(t)
		mock.ListingPageCap = 1
		for i := range 50 {
			seed(mock, "media", fmt.Sprintf("docs/aaa-%04d", i), "already here")
		}
		var folder []string
		for i := range 10 {
			folder = append(folder, fmt.Sprintf("docs/photos/%c/image-%04d", 'a'+rune(i%2), i))
		}
		seed(mock, "media", folder[7], "already here")
		before := len(mock.RequestLog())
		live, err := destinationLiveness(t.Context(), s, "media", "docs/", folder)
		if err != nil {
			t.Fatal(err)
		}
		for i, name := range folder {
			if live[name] != (i == 7) {
				t.Errorf("%s live = %v", name, live[name])
			}
		}
		heads, listings := count(mock.RequestLog()[before:])
		if heads != 0 || listings != 2 {
			t.Errorf("cost %d HEADs and %d listings, want 0 and 2: the one row under the folder, then the empty page", heads, listings)
		}
	})
	t.Run("a head that failed refuses", func(t *testing.T) {
		s, mock := session(t)
		mock.ListingPageCap = 1
		seedRows(mock, 500)
		mock.FailNextObjectHEADs(1)
		if live, err := destinationLiveness(t.Context(), s, "media", "docs/",
			append(names(2), "docs/absent")); err == nil {
			t.Fatalf("a failed HEAD was read as free: %+v", live)
		}
	})
	t.Run("a listing that stopped refuses", func(t *testing.T) {
		s, mock := session(t)
		seedRows(mock, 6)
		mock.FailNextListings(1)
		if live, err := destinationLiveness(t.Context(), s, "media", "docs/", names(6)); err == nil {
			t.Fatalf("a failed listing was read as an empty prefix: %+v", live)
		}
	})
}

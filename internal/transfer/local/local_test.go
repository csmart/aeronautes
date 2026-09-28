package local

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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

// fsEngine is the test engine: local↔mock-swift over the sessions.
// plannedWithIdentities gives a hand-built plan the source identities the
// preflight captures for every planned file, so the engine route can
// verify what the test engine stages against the source (ADR 0070).
func plannedWithIdentities(t *testing.T, session *swiftcontrol.Session, container string, plan *DownloadPreflight) *DownloadPreflight {
	t.Helper()
	if session == nil {
		return plan
	}
	plan.nativeSource = map[string]downloadSourceIdentity{}
	for _, item := range plan.Items {
		if item.Dir {
			continue
		}
		detail, err := session.ObjectDetail(t.Context(), container, item.Name)
		if err != nil {
			t.Fatalf("capture identity of %q: %v", item.Name, err)
		}
		plan.nativeSource[item.Name] = headSourceIdentity(detail)
	}
	return plan
}

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
		case !srcLocal && dstLocal: // download
			body, _, err := e.session.OpenObject(ctx, srcC, srcP+name)
			if err != nil {
				return &rclone.Result{ExitCode: 1}, nil
			}
			target := filepath.Join(dstP, name)
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
			var buf bytes.Buffer
			_, _ = buf.ReadFrom(body)
			body.Close()
			if err := os.WriteFile(target, buf.Bytes(), 0o644); err != nil {
				return &rclone.Result{ExitCode: 1}, nil
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
	if _, err := Upload(ctx, s, &fsEngine{s}, "aeronautes_dst:media", "media", "docs/", "test-job", report, nil, nil); err == nil {
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
	result, err := Upload(ctx, s, &fsEngine{s}, "aeronautes_dst:media", "media", "up/", "test-job", report, nil, nil)
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
	result, err := Download(ctx, s, &fsEngine{s}, "aeronautes_src:media", "media", "snap/", destDir, "test-job", report, nil)
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

func TestNativeDownloadNeverLeavesPartials(t *testing.T) {
	// A read that dies mid-copy must leave NOTHING at the final name;
	// a short read must refuse; a clean copy lands atomically.
	session, mock := session(t)
	seed(mock, "media", "ok.bin", "complete-content")
	dest := t.TempDir()

	// Short read: item.Size larger than the object → refuse + no file.
	destination := testDestination(t, dest)
	outcome := nativeDownload(context.Background(), session, "media",
		DownloadItem{Name: "ok.bin", RelPath: "short.bin", Size: 9999}, destination, filepath.Join(dest, ".aeronautes-part-short"), downloadSourceIdentity{})
	placed, err := outcome.Result == transfercopy.ResultCopied, outcome.err()
	if placed || err == nil || !strings.Contains(err.Error(), "short") {
		t.Fatalf("short download = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "short.bin")); statErr == nil {
		t.Fatal("short download left a file at the final name")
	}
	// Clean copy lands with exact bytes.
	outcome = nativeDownload(context.Background(), session, "media",
		DownloadItem{Name: "ok.bin", RelPath: "ok.bin", Size: int64(len("complete-content"))}, destination, filepath.Join(dest, ".aeronautes-part-ok"), downloadSourceIdentity{})
	closeDownloadPublication(t, outcome)
	if outcome.Result != transfercopy.ResultCopied || outcome.err() != nil {
		t.Fatal(outcome.err())
	}
	data, err := os.ReadFile(filepath.Join(dest, "ok.bin"))
	if err != nil || string(data) != "complete-content" {
		t.Fatalf("landed bytes = %q %v", data, err)
	}
	// No temp litter.
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".aeronautes-part-") {
			t.Fatalf("temp litter %s", e.Name())
		}
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

func TestLocalUploadNeverTargetsLiveName(t *testing.T) {
	// The local analogue of TestEngineNeverReceivesLiveNames: reverting
	// Upload to a live destination would make the runner refuse — this
	// records the engine destinations and asserts every one is staging-
	// scoped.
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
	if _, err := Upload(context.Background(), s, rec, "aeronautes_dst:dst", "dst", "up/", "job-x", pre, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(rec.dsts) == 0 {
		t.Fatal("engine never invoked")
	}
	for _, dst := range rec.dsts {
		if !strings.Contains(dst, "/.aeronautes/stage/") {
			t.Errorf("upload engine received a live destination %q", dst)
		}
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

// The choice between patterns is an estimate built from the container's own
// count, and the arithmetic has to round the way the store pages. An earlier
// version rounded down, so it estimated a full page as free and made a tie
// look like a saving.
func TestPreflightUploadChoosesTheCheaperEstimate(t *testing.T) {
	for _, tc := range []struct {
		name         string
		alreadyThere int
		files        int
		// wantHeads is how many object HEADs the preflight may issue: zero
		// when the listing is estimated cheaper, one per file otherwise.
		wantHeads int
	}{
		{name: "a tie goes to the heads", alreadyThere: 3, files: 2, wantHeads: 2},
		{name: "one more file tips it to the listing", alreadyThere: 3, files: 3, wantHeads: 0},
		{name: "a single file never lists", alreadyThere: 3, files: 1, wantHeads: 1},
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
			report, err := preflightUpload(t.Context(), s, "media", "docs/", paths, tc.replacing)
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

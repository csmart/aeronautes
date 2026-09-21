package local

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

func session(t *testing.T) (*swiftcontrol.Session, *swifttest.Server) {
	t.Helper()
	mock := swifttest.New("test:tester", "testing")
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

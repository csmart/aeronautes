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
	if _, err := Upload(ctx, s, &fsEngine{s}, "aeronautes_dst:media", "media", "docs/", report, nil); err == nil {
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
	for _, want := range []string{"path-escape", "file-vs-prefix", "unmaterializable", "local-exists"} {
		if !found[want] {
			t.Errorf("missing rejection %q: %+v", want, report.Problems)
		}
	}
	if !warned["case-fold"] {
		t.Errorf("case twins should warn, not reject: %+v", report.Problems)
	}
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
	result, err := Upload(ctx, s, &fsEngine{s}, "aeronautes_dst:media", "media", "up/", report, nil)
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
	seed(mock, "media", "snap/we ird//name", "hostile dl") // // → native

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
	if result.Done != 4 || result.NativeRouted != 1 || len(result.Failures) != 0 {
		t.Fatalf("result = %+v (failures %+v)", result, result.Failures)
	}
	for rel, want := range map[string]string{
		"plain.txt":       "plain content",
		"nested/deep.bin": "deep bytes!",
		"we ird/name":     "hostile dl", // filepath.Join collapses //
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

package local

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	"github.com/csmart/aeronautes/internal/swifttest"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

func TestNativeDownloadRefusesReorderedAndMissingRuns(t *testing.T) {
	left, right := bytes.Repeat([]byte("A"), 16), bytes.Repeat([]byte("B"), 16)
	assembled := append(append([]byte(nil), left...), right...)
	ladder := transfercopy.SegmentedLadder(left, right)
	for _, c := range []struct {
		name string
		body []byte
	}{
		{name: "control", body: assembled},
		{name: "permuted_segments", body: append(append([]byte(nil), right...), left...)},
		{name: "missing_with_duplicate", body: append(append([]byte(nil), left...), left...)},
		{name: "short_at_run_boundary", body: append([]byte(nil), left...)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.name != "control" && bytes.Equal(c.body, assembled) {
				t.Fatal("fixture served the ladder's bytes; the GET did not differ")
			}
			if c.name == "permuted_segments" && bytes.Equal(c.body[:len(left)], left) {
				t.Fatal("permuted GET still began with the first run")
			}
			dir := t.TempDir()
			staging := filepath.Join(dir, "temporary")
			if err := os.MkdirAll(staging, 0o755); err != nil {
				t.Fatal(err)
			}
			temp, err := os.CreateTemp(staging, ".aeronautes-part-*")
			if err != nil {
				t.Fatal(err)
			}
			destination := testDestination(t, dir)
			outcome := finishNativeDownload(t.Context(), io.NopCloser(bytes.NewReader(c.body)), temp,
				testDirectory(t, destination, "temporary"),
				nativePublication(t, destination, "temporary", filepath.Base(temp.Name()), ".", "final"),
				int64(len(assembled)), ladder)
			closeDownloadPublication(t, outcome)
			placed, placeErr := os.ReadFile(filepath.Join(dir, "final"))
			if c.name == "control" {
				if outcome.Result != transfercopy.ResultCopied || placeErr != nil || !bytes.Equal(placed, assembled) {
					t.Fatalf("matching runs were not published: %+v %v", outcome.Observation, placeErr)
				}
				return
			}
			if outcome.Result == transfercopy.ResultCopied || outcome.err() == nil {
				t.Fatalf("a GET that differed from the ladder was accepted: %+v", outcome.Observation)
			}
			if outcome.Diagnostic.Code != transfercopy.CodeUnverified {
				t.Errorf("mismatch used code %q, want unverified", outcome.Diagnostic.Code)
			}
			if placeErr == nil {
				t.Fatalf("differing GET was published: %q", placed)
			}
			held, readErr := os.ReadFile(temp.Name())
			if readErr != nil || !bytes.Equal(held, c.body) {
				t.Fatalf("rejected GET was not retained: err=%v content=%q", readErr, held)
			}
			if outcome.Effect != transfercopy.EffectUnresolved {
				t.Errorf("retained residue left effect %v, want unresolved", outcome.Effect)
			}
		})
	}
}

func TestNativeDownloadRefusesASubstitutedSource(t *testing.T) {
	s, mock := session(t)
	seed(mock, "media", "scope\n/file", "abcdefgh")
	root := t.TempDir()
	preflight, err := PreflightDownload(t.Context(), s, "media", "scope\n/", root)
	if err != nil || preflight.Rejected() || len(preflight.Items) != 1 {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	plannedID := preflight.nativeSource[preflight.Items[0].Name]
	if !plannedID.captured || plannedID.etag == "" {
		t.Fatal("transfer set stored no source identity")
	}
	planned := plannedID.etag
	seed(mock, "media", "scope\n/file", "ABCDEFGH")
	after := md5.Sum([]byte("ABCDEFGH"))
	if hex.EncodeToString(after[:]) == planned {
		t.Fatal("replacement kept the planned entity tag")
	}
	report, err := Download(t.Context(), s, nil, "", "media", "scope\n/", root, "fixture", preflight, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Done != 0 || len(report.Failures) != 1 {
		t.Fatalf("substituted source was accepted: %+v", report)
	}
	if _, err := os.Lstat(filepath.Join(root, "file")); !os.IsNotExist(err) {
		t.Fatal("substituted source was published")
	}
	if !strings.Contains(report.Failures[0].Err, "substituted") {
		t.Errorf("failure did not name substitution: %s", report.Failures[0].Err)
	}
}

func TestNativeDownloadRefusesASubstitutedPublication(t *testing.T) {
	mock := swifttest.New("test:tester", "testing")
	root := t.TempDir()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/b") {
			path := filepath.Join(root, "a")
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove published file: %v", err)
			}
			if err := os.WriteFile(path, []byte("replaced"), 0o600); err != nil {
				t.Errorf("replace published file: %v", err)
			}
		}
		mock.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	s, err := swiftcontrol.New(swiftcontrol.Config{
		AuthURL: front.URL + "/auth/v1.0", Username: "test:tester", APIKey: "testing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	mock.AddContainer("media", "standard")
	seed(mock, "media", "scope\n/a", "bytes a")
	seed(mock, "media", "scope\n/b", "bytes b")
	preflight, err := PreflightDownload(t.Context(), s, "media", "scope\n/", root)
	if err != nil || preflight.Rejected() || len(preflight.Items) != 2 {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	report, err := Download(t.Context(), s, nil, "", "media", "scope\n/", root, "fixture", preflight, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Done != 2 || report.Bytes != 14 || report.Verified != 1 || len(report.Failures) != 1 {
		t.Fatalf("publication substitution was not isolated: %+v", report)
	}
	if report.Failures[0].Name != "scope\n/a" {
		t.Errorf("failed name %q, want the substituted publication", report.Failures[0].Name)
	}
	held, readErr := os.ReadFile(filepath.Join(root, "a"))
	if readErr != nil || !bytes.Equal(held, []byte("replaced")) {
		t.Fatalf("substitute was unlinked: err=%v content=%q", readErr, held)
	}
	if data, err := os.ReadFile(filepath.Join(root, "b")); err != nil || string(data) != "bytes b" {
		t.Fatalf("later item was lost: %q %v", data, err)
	}
}

func TestNativeDownloadPublicationCountsBeyondSamples(t *testing.T) {
	s, mock := session(t)
	const count = maxFailures + 1
	for i := range count {
		seed(mock, "media", fmt.Sprintf("scope\n/file-%03d", i), "bytes")
	}
	root := t.TempDir()
	plan, err := PreflightDownload(t.Context(), s, "media", "scope\n/", root)
	if err != nil || plan.Rejected() || len(plan.Items) != count {
		t.Fatalf("preflight: %+v %v", plan, err)
	}
	report, err := Download(t.Context(), s, nil, "", "media", "scope\n/", root, "fixture", plan,
		func(done, _, _ int64) {
			if err := os.Remove(filepath.Join(root, plan.Items[done-1].RelPath)); err != nil {
				t.Fatal(err)
			}
		})
	if err != nil || report.Done != count || report.Bytes != 5*count || report.Verified != 0 || len(report.Failures) != maxFailures {
		t.Fatalf("final verification was limited to failure samples: %+v %v", report, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("confirmation recreated files or retained temporaries: %+v %v", entries, err)
	}
}

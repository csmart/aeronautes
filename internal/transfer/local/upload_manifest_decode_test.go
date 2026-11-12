package local

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

// The engine percent-encodes a staged dynamic manifest's header, so a name
// with a space reaches the commit encoded. The committing record must name
// the decoded prefix, the one the capture recorded and the store lists, or
// the segments of a published object would be left recorded as uncommitted
// residue (F37).
func TestStagedCommitRecordsTheDecodedSegmentPrefix(t *testing.T) {
	const body = "0123456789abcdef"
	mock := swifttest.New("test:tester", "testing")
	mock.AddContainer("media", "standard")
	mock.AddContainer("media_segments", "standard")
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	session := bindingSession(t, server)
	_, item := uploadOne(t, session, "media", "docs/", writeFile(t, t.TempDir(), "my object", body))
	stagedName := ".aeronautes/stage/binding/batch/my object"
	seed(mock, "media_segments", stagedName+"/run/00000000", "01234567")
	seed(mock, "media_segments", stagedName+"/run/00000001", "89abcdef")
	mock.PutObject("media", stagedName, swifttest.Object{Manifest: "media_segments/.aeronautes/stage/binding/batch/my%20object/run/"})

	var records []string
	recovery := func(kind, container, prefix, object string) error {
		records = append(records, kind+" "+container+"/"+prefix)
		return nil
	}
	result := commitStagedUpload(t.Context(), session, "media", stagedName, item, item.Replaces, comparesAgainstLive(t, session, "media", item), recovery)
	if result.Result != transfercopy.ResultCopied {
		t.Fatalf("result = %+v %q", result.Observation, result.label)
	}
	want := "media_segments/" + stagedName + "/run/"
	if len(records) != 2 || records[0] != "committing "+want || records[1] != "committed "+want {
		t.Errorf("recovery records = %q, want committing and committed for %q", records, want)
	}
}

// A dynamic object whose manifest header is encoded downloads: its content
// expectation is built from the decoded prefix's segments, which the store
// assembles, rather than from a raw prefix that lists none (F37).
func TestDownloadOfADynamicObjectWithAnEncodedManifest(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media", "standard")
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "my video/run/00000000", "01234567")
	seed(mock, "media_segments", "my video/run/00000001", "89abcdef")
	mock.PutObject("media", "clips/my video", swifttest.Object{Manifest: "media_segments/my%20video/run/"})
	dest := t.TempDir()
	preflight, err := PreflightDownload(t.Context(), s, "media", "clips/", dest)
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %+v %v", preflight, err)
	}
	report, err := Download(t.Context(), s, &fsEngine{s}, "aeronautes_src:media", "media", "clips/", dest, "decode-job", preflight, nil)
	if err != nil || report.Done != 1 || len(report.Failures) != 0 {
		t.Fatalf("download: %+v %v", report, err)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "my video")); err != nil || string(got) != "0123456789abcdef" {
		t.Errorf("downloaded %q, %v", got, err)
	}
}

// A replaced dynamic manifest's inventory lists the decoded prefix and
// records it as the prefix it can be listed from (F37).
func TestReplacedDynamicInventoryReadsTheDecodedPrefix(t *testing.T) {
	s, mock := session(t)
	mock.AddContainer("media", "standard")
	mock.AddContainer("media_segments", "standard")
	seed(mock, "media_segments", "my subject/run/000000", "part")
	mock.PutObject("media", "docs/object", swifttest.Object{Manifest: "media_segments/my%20subject/run/"})
	detail, err := s.ObjectDetail(t.Context(), "media", "docs/object")
	if err != nil {
		t.Fatal(err)
	}
	inventory := fragmentInventory(t.Context(), s, "media", "docs/object", detail)
	if inventory.Prefix != "media_segments/my subject/run/" || len(inventory.Names) != 1 || inventory.Names[0].Name != "my subject/run/000000" {
		t.Errorf("inventory = %+v, want the decoded prefix and its one segment", inventory)
	}
}

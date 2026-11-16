package local

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/csmart/aeronautes/internal/swifttest"
	"github.com/csmart/aeronautes/internal/systemtest/oracle"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

type countedUploadEngine struct {
	inner Engine
	calls int
}

func (e *countedUploadEngine) Copy(ctx context.Context, src, dst string, names []string, onEvent func(rclone.Event)) (*rclone.Result, error) {
	e.calls++
	return e.inner.Copy(ctx, src, dst, names, onEvent)
}

func uploadWitness(t *testing.T, mock *swifttest.Server) *oracle.Oracle {
	t.Helper()
	server := httptest.NewServer(mock)
	t.Cleanup(server.Close)
	witness, err := oracle.Connect(server.URL+"/auth/v1.0", "test:tester", "testing")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(witness.Close)
	return witness
}

func uploadSnapshot(t *testing.T, witness *oracle.Oracle) *oracle.AccountSnapshot {
	t.Helper()
	snapshot, err := witness.SnapshotContainers("media", "media_segments", "outside")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestUploadRefusesExistingRepresentations(t *testing.T) {
	for _, prefix := range []string{"docs/", "line\n/"} {
		for _, class := range []string{"plain", "dlo", "slo", "symlink"} {
			t.Run(prefix+class, func(t *testing.T) {
				s, mock := session(t)
				path := writeFile(t, t.TempDir(), "object", "new local content")
				seed(mock, "media_segments", "shared/leaf", "old content")
				seed(mock, "outside", "keep", "outside content")
				mock.PutObject("outside", "alias", swifttest.Object{Manifest: "media_segments/shared/"})
				name := prefix + "object"
				switch class {
				case "plain":
					seed(mock, "media", name, "old content")
				case "dlo":
					mock.PutObject("media", name, swifttest.Object{Manifest: "media_segments/shared/"})
				case "slo":
					mock.PutObject("media", name, swifttest.Object{SLO: true, Bytes: 11, Data: []byte("old content"),
						SLOManifestJSON: `[{"name":"/media_segments/shared/leaf","bytes":11}]`})
				case "symlink":
					mock.PutObject("media", name, swifttest.Object{SymlinkTarget: "outside/keep"})
				}
				witness := uploadWitness(t, mock)
				before := uploadSnapshot(t, witness)
				if alias := before.Containers["outside"].Objects["alias"]; alias.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte("old content"))) || len(alias.Segments) != 1 {
					t.Fatalf("outside alias is not independently readable: %+v", alias)
				}
				preflight, err := PreflightUpload(t.Context(), s, "media", prefix, []string{path})
				if err != nil || !preflight.Rejected() || len(preflight.Items) != 0 || len(preflight.Problems) != 1 || preflight.Problems[0].Kind != "exists" {
					t.Fatalf("existing %s was not refused: %+v, %v", class, preflight, err)
				}
				engine := &countedUploadEngine{inner: &fsEngine{s}}
				if report, err := Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", preflight, nil, nil); err == nil || report != nil || engine.calls != 0 {
					t.Errorf("refused upload executed: %+v, %v, calls=%d", report, err, engine.calls)
				}
				if diff := oracle.Diff(before, uploadSnapshot(t, witness)); !diff.Empty() {
					t.Errorf("refused upload changed existing objects or backing data: %s", diff)
				}
			})
		}
	}
}

// ADR 0122: a name another writer created after the preview is written over,
// and the upload reports success; the refusal earlier builds made here is
// gone, and nothing but the one name changes.
func TestNativeUploadWritesOverAPostPreflightWriter(t *testing.T) {
	s, mock := session(t)
	path := writeFile(t, t.TempDir(), "object", "new local content")
	preflight, err := PreflightUpload(t.Context(), s, "media", "line\n/", []string{path})
	if err != nil || preflight.Rejected() || len(preflight.Items) != 1 || preflight.Items[0].Route != "native" {
		t.Fatalf("new native upload was not admitted: %+v, %v", preflight, err)
	}
	seed(mock, "media", "line\n/object", "concurrent writer")
	seed(mock, "outside", "keep", "outside content")
	witness := uploadWitness(t, mock)
	before := uploadSnapshot(t, witness)
	engine := &countedUploadEngine{inner: &fsEngine{s}}
	report, err := Upload(t.Context(), s, engine, "aeronautes_dst:media", "media", preflight, nil, nil)
	if err != nil || report.Done != 1 || len(report.Failures) != 0 || engine.calls != 0 {
		t.Fatalf("report = %+v, %v, engine calls = %d; want the one file done by the application's own PUT", report, err, engine.calls)
	}
	after := uploadSnapshot(t, witness)
	if got := after.Containers["media"].Objects["line\n/object"]; got.Digest != fmt.Sprintf("%x", sha256.Sum256([]byte("new local content"))) {
		t.Fatalf("the name holds %+v, want the uploaded file", got)
	}
	delete(before.Containers["media"].Objects, "line\n/object")
	delete(after.Containers["media"].Objects, "line\n/object")
	if diff := oracle.Diff(before, after); !diff.Empty() {
		t.Errorf("the upload changed something else: %s", diff)
	}
}

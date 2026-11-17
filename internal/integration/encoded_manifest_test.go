package integration

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
	localtransfer "github.com/csmart/aeronautes/internal/transfer/local"
	"github.com/csmart/aeronautes/internal/transfer/rclone"
)

// TestEncodedDynamicManifestRealSwift uploads a segmented file whose name
// carries a space and a percent sign through the pinned engine, which
// percent-encodes the dynamic manifest's header, and downloads it back byte
// for byte, which reads the segments that header names (F37).
func TestEncodedDynamicManifestRealSwift(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("pinned rclone not installed")
	}
	h := harness(t)
	ctx := context.Background()
	// An upload's engine writes each file at its name (ADR 0122 decision 3).
	runner, err := rclone.NewRunner(rclone.Options{ChunkSize: "1Mi", LiveDestination: true, Retries: 1, LowLevelRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	container := h.Container("encoded-dlo")
	if err := h.conn.ContainerCreate(ctx, container, nil); err != nil {
		t.Fatal(err)
	}
	removeAtEnd(t, h, container, container+"_segments")
	const name = "big file%.bin"
	content := append(bytes.Repeat([]byte("0123456789abcdef"), 3<<16), "tail"...)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
		t.Fatal(err)
	}
	engine := transfercopy.BindRunner(runner, []rclone.Remote{{Name: "aeronautes_dst", StorageURL: h.conn.StorageUrl, Token: h.conn.AuthToken}})
	preflight, err := localtransfer.PreflightUploadSources(ctx, h.Session, container, "up/", localtransfer.SourcesByBaseName([]string{filepath.Join(dir, name)}))
	if err != nil || preflight.Rejected() {
		t.Fatalf("preflight: %v %+v", err, preflight)
	}
	if report, err := localtransfer.Upload(ctx, h.Session, engine, "aeronautes_dst:"+container, container, preflight, nil, nil); err != nil || report.Done != 1 {
		t.Fatalf("upload: %v %+v", err, report)
	}
	live, err := h.Session.ObjectDetail(ctx, container, "up/"+name)
	if err != nil || live.Class != swiftcontrol.ClassDLO || live.ContentLength != int64(len(content)) {
		t.Fatalf("live = %+v %v, want a dynamic object of %d bytes", live, err, len(content))
	}

	destDir := t.TempDir()
	source := transfercopy.BindRunner(runner, []rclone.Remote{{Name: "aeronautes_src", StorageURL: h.conn.StorageUrl, Token: h.conn.AuthToken}})
	down, err := localtransfer.PreflightDownload(ctx, h.Session, container, "up/", destDir)
	if err != nil || down.Rejected() {
		t.Fatalf("download preflight: %v %+v", err, down)
	}
	report, err := localtransfer.Download(ctx, h.Session, source, "aeronautes_src:"+container, container, "up/", destDir, down, nil)
	if err != nil || report.Done != 1 || len(report.Failures) != 0 {
		t.Fatalf("download: %v %+v", err, report)
	}
	if got, err := os.ReadFile(filepath.Join(destDir, name)); err != nil || !bytes.Equal(got, content) {
		t.Errorf("downloaded %d bytes (%v), want the %d uploaded", len(got), err, len(content))
	}
}

// removeAtEnd empties and deletes containers a test created, when it ends,
// as the harness teardown does for the whole run. Tests that list the run's
// containers expect only the fixtures, and test files run in name order,
// so a test that sorts before them must not leave its containers behind.
func removeAtEnd(t *testing.T, h *Harness, containers ...string) {
	t.Cleanup(func() {
		ctx := context.Background()
		for _, container := range containers {
			objects, err := h.conn.ObjectsAll(ctx, container, nil)
			if err != nil {
				continue // never created, as a sidecar can be
			}
			for _, object := range objects {
				if err := h.conn.ObjectDelete(ctx, container, object.Name); err != nil {
					t.Errorf("remove %s/%s: %v", container, object.Name, err)
				}
			}
			if err := h.conn.ContainerDelete(ctx, container); err != nil {
				t.Errorf("remove container %s: %v", container, err)
			}
		}
	})
}

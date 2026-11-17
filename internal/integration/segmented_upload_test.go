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

// TestSegmentedUploadKindsRealSwift uploads a file larger than the chunk
// through the pinned engine as a static and as a dynamic large object, and
// downloads each back byte for byte (ADR 0111). The name carries a space
// and a percent sign, which the engine's manifest header encodes and both
// publications have to decode to find the segments.
func TestSegmentedUploadKindsRealSwift(t *testing.T) {
	if _, err := exec.LookPath("rclone"); err != nil {
		t.Skip("pinned rclone not installed")
	}
	h := harness(t)
	ctx := context.Background()
	// An upload's engine writes each file at its name (ADR 0122 decision 3).
	runner, err := rclone.NewRunner(rclone.Options{ChunkSize: "1Mi", LiveDestination: true, IgnoreSize: true, Retries: 1, LowLevelRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	content := bytes.Repeat([]byte("0123456789abcdef"), 3<<16)
	content = append(content, "tail"...)
	const name = "big file%.bin"
	if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []swiftcontrol.ObjectClass{swiftcontrol.ClassSLO, swiftcontrol.ClassDLO} {
		t.Run(string(kind), func(t *testing.T) {
			container := h.Container("up-" + string(kind))
			if err := h.conn.ContainerCreate(ctx, container, nil); err != nil {
				t.Fatal(err)
			}
			removeAtEnd(t, h, container, container+"_segments")
			engine := transfercopy.BindRunner(runner, []rclone.Remote{
				{Name: "aeronautes_dst", StorageURL: h.conn.StorageUrl, Token: h.conn.AuthToken},
			})
			preflight, err := localtransfer.PreflightUploadSources(ctx, h.Session, container, "up/", localtransfer.SourcesByBaseName([]string{filepath.Join(dir, name)}))
			if err != nil || preflight.Rejected() {
				t.Fatalf("preflight: %v %+v", err, preflight)
			}
			preflight.LargeObjects, preflight.ChunkBytes = string(kind), 1<<20
			report, err := localtransfer.Upload(ctx, h.Session, engine, "aeronautes_dst:"+container, container, preflight, nil, nil)
			if err != nil || report.Done != 1 || len(report.Failures) != 0 {
				t.Fatalf("upload: %v %+v", err, report)
			}
			live, err := h.Session.ObjectDetail(ctx, container, "up/"+name)
			if err != nil || live.Class != kind || live.ContentLength != int64(len(content)) {
				t.Fatalf("live object = %+v %v, want a %s of %d bytes", live, err, kind, len(content))
			}

			destDir := t.TempDir()
			source := transfercopy.BindRunner(runner, []rclone.Remote{
				{Name: "aeronautes_src", StorageURL: h.conn.StorageUrl, Token: h.conn.AuthToken},
			})
			down, err := localtransfer.PreflightDownload(ctx, h.Session, container, "up/", destDir)
			if err != nil || down.Rejected() {
				t.Fatalf("download preflight: %v %+v", err, down)
			}
			downReport, err := localtransfer.Download(ctx, h.Session, source, "aeronautes_src:"+container, container, "up/", destDir, down, nil)
			if err != nil || downReport.Done != 1 || len(downReport.Failures) != 0 {
				t.Fatalf("download: %v %+v", err, downReport)
			}
			got, err := os.ReadFile(filepath.Join(destDir, name))
			if err != nil || !bytes.Equal(got, content) {
				t.Errorf("downloaded %d bytes (%v), want the %d uploaded", len(got), err, len(content))
			}
		})
	}
}

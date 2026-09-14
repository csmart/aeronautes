package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	transfercopy "github.com/csmart/aeronautes/internal/transfer/copy"
)

// unplacedAfterCancel is the one record a staged-but-unplaced item carries
// after a cancel, whichever check saw the cancel.
var unplacedAfterCancel = transfercopy.Observation{Result: transfercopy.ResultFailed, Effect: transfercopy.EffectUnresolved,
	Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageCommit, Code: transfercopy.CodeCancelled}}

func TestDownloadCancelledPlacementRecords(t *testing.T) {
	t.Run("loop", func(t *testing.T) {
		source, mock := session(t)
		for _, name := range []string{"a", "b"} {
			seed(mock, "media", "scope/"+name, "bytes "+name)
		}
		root := t.TempDir()
		writeFile(t, root, "keep", "unrelated")
		preflight, err := PreflightDownload(t.Context(), source, "media", "scope/", root)
		if err != nil || preflight.Rejected() || len(preflight.Items) != 2 {
			t.Fatalf("fixture preflight: %+v %v", preflight, err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		executed := map[string]transfercopy.Observation{}
		verified := map[string]transfercopy.Observation{}
		recorder := &DownloadRecorder{
			Admit:   func(DownloadAdmission) error { return nil },
			Execute: func(item DownloadItem, o transfercopy.Observation) error { executed[item.Name] = o; return nil },
			Verify: func(item DownloadItem, o transfercopy.Observation) error {
				verified[item.Name] = o
				cancel()
				return nil
			},
		}
		report, err := Download(ctx, source, &fsEngine{session: source}, "remote:media", "media", "scope/", root, "fixture", preflight, nil, recorder)
		if !errors.Is(err, context.Canceled) || report.Done != 1 || report.Bytes != 7 {
			t.Fatalf("cancel between placements changed the placed outcome: %+v %v", report, err)
		}
		if got := executed["scope/a"]; got.Result != transfercopy.ResultCopied || verified["scope/a"].Result != transfercopy.ResultVerified {
			t.Fatalf("placed item lost its records: %+v %+v", got, verified["scope/a"])
		}
		if got := executed["scope/b"]; got != unplacedAfterCancel {
			t.Fatalf("unplaced item record differs from the placement record: %+v", got)
		}
		if _, exists := verified["scope/b"]; exists {
			t.Fatal("unplaced item acquired a verification record")
		}
		if len(report.Failures) != 1 || report.Failures[0].Name != "scope/b" || report.Failures[0].Stage != "place" || !report.Failures[0].Cancelled {
			t.Fatalf("report failures do not name the unplaced item once: %+v", report.Failures)
		}
		// A cancel removes the partial files this job staged (ADR 0048).
		// The stage itself goes with them, so nothing of this job's own
		// scratch survives in the directory the operator picked.
		if _, err := os.Lstat(filepath.Join(root, ".aeronautes-stage-fixture")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cancel left this job's own stage behind: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(root, "b")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("cancelled placement still placed the file")
		}
		if data, err := os.ReadFile(filepath.Join(root, "a")); err != nil || string(data) != "bytes a" {
			t.Fatal("cancel between placements lost the placed file")
		}
		// Retained describes the disk: the stage is gone, so it is false
		// and there is nothing left to report (ADR 0048).
		if report.StageCleanup == nil || report.StageCleanup.Retained || report.StageCleanup.Issues != 0 {
			t.Fatalf("stage retention after cancel differs: %+v", report.StageCleanup)
		}
	})
	t.Run("placement", func(t *testing.T) {
		root := t.TempDir()
		destination, err := OpenDestinationRoot(root)
		if err != nil {
			t.Fatal(err)
		}
		defer destination.Close()
		stageName := ".aeronautes-stage-fixture"
		stage, err := openDownloadStage(destination, stageName)
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close()
		staged := filepath.Join(root, stageName, "b")
		if err := os.WriteFile(staged, []byte("bytes b"), 0o644); err != nil {
			t.Fatal(err)
		}
		item := DownloadItem{Name: "scope/b", RelPath: "b", Size: 7, Route: "rclone"}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		outcome := placeDownloaded(ctx, destination, stage, stageName, item)
		if outcome.Observation != unplacedAfterCancel || outcome.label != "place" || !errors.Is(outcome.failure, context.Canceled) {
			t.Fatalf("placement cancel record differs from the loop record: %+v", outcome)
		}
		if _, err := os.Lstat(filepath.Join(root, "b")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("cancelled placement placed the file")
		}
		if data, err := os.ReadFile(staged); err != nil || string(data) != "bytes b" {
			t.Fatal("cancelled placement touched the staged bytes")
		}
	})
}

// cancellingBody delivers one chunk, then cancels the context and fails
// the next read the way a request body bound to that context does.
type cancellingBody struct {
	data   []byte
	cancel context.CancelFunc
	after  func()
	sent   bool
}

func (b *cancellingBody) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		return copy(p, b.data), nil
	}
	b.cancel()
	if b.after != nil {
		b.after()
	}
	return 0, context.Canceled
}

func (b *cancellingBody) Close() error { return nil }

func TestDownloadNativeBodyCancellation(t *testing.T) {
	for _, mode := range []string{"removed", "replaced"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, "keep", "unrelated")
			destination, err := OpenDestinationRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer destination.Close()
			item := DownloadItem{Name: "scope/n", RelPath: "n", Size: 64, Route: "native"}
			temporary, err := downloadTemporaryPath(root, item)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			body := &cancellingBody{data: []byte("first half of the object body!!!"), cancel: cancel}
			if mode == "replaced" {
				body.after = func() {
					// Another actor swaps a directory in at the temporary's
					// name while the read is being interrupted.
					if err := os.Remove(temporary); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(temporary, 0o755); err != nil {
						t.Fatal(err)
					}
				}
			}
			outcome := nativeDownloadBody(ctx, body, item, destination, temporary, ladderFor(t, nil))
			want := transfercopy.Observation{Result: transfercopy.ResultFailed, Effect: transfercopy.EffectNotAttempted,
				Diagnostic: transfercopy.Diagnostic{Stage: transfercopy.StageSourceRead, Code: transfercopy.CodeCancelled}}
			if mode == "replaced" {
				want.Effect = transfercopy.EffectUnresolved
			}
			if outcome.Observation != want || outcome.verification != nil || !errors.Is(outcome.failure, context.Canceled) {
				t.Fatalf("interrupted native body record differs: %+v want %+v", outcome, want)
			}
			if (len(outcome.issues) != 0) != (mode == "replaced") {
				t.Fatalf("temporary cleanup issue differs: %+v", outcome.issues)
			}
			if _, err := os.Lstat(filepath.Join(root, "n")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("interrupted native body published its target")
			}
			info, err := os.Lstat(temporary)
			switch mode {
			case "removed":
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("interrupted temporary was not removed: %+v %v", info, err)
				}
			case "replaced":
				if err != nil || !info.IsDir() {
					t.Fatalf("substituted entry was not retained: %+v %v", info, err)
				}
			}
			if data, err := os.ReadFile(filepath.Join(root, "keep")); err != nil || string(data) != "unrelated" {
				t.Fatal("interrupted native body changed unrelated bytes")
			}
		})
	}
}

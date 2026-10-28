package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
)

// TestSegmentDeletionSurvivesAProcessRestart runs the real segment-delete job
// in a child process and kills that process hard (os.Exit, so no cleanup and no
// clean database close) at a chosen boundary. A fresh process then reopens the
// database on its file and runs startup recovery, and the readback shows what
// actually survives a process restart — not an in-process handle reopen.
//
// Two boundaries establish the issued-only versus durably-deleted manifest
// distinction ADR 0095 decision 8 requires: a crash while the manifest DELETE
// is still outstanding leaves the manifest issued with no outcome, recovered as
// unresolved and never fabricated into a deleted outcome; a crash at the first
// segment, immediately after the manifest's deletion is recorded, leaves that
// deleted record standing, the interrupted segment unresolved and the untouched
// segment not attempted.
func TestSegmentDeletionSurvivesAProcessRestart(t *testing.T) {
	const manifestKey = "media/big.slo"
	seg1, seg2 := "parts/job/aa/000001", "parts/job/aa/000002"

	if crashAt := os.Getenv("AERONAUTES_SEGMENT_RESTART_AT"); crashAt != "" {
		runSegmentRestartChild(t, crashAt, os.Getenv("AERONAUTES_SEGMENT_RESTART_DB"))
		return
	}

	for _, tc := range []struct {
		at             string
		manifestResult string // the manifest member's settled execution result
		issued         int64
		deleted        int64
		unresolved     int64
	}{
		{at: "before-manifest", manifestResult: "", issued: 1, deleted: 0, unresolved: 1},
		{at: "after-manifest", manifestResult: "deleted", issued: 2, deleted: 1, unresolved: 1},
	} {
		t.Run(tc.at, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "state.sqlite")
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, self, "-test.run=^TestSegmentDeletionSurvivesAProcessRestart$", "-test.timeout=50s", "-test.v")
			child.Env = append(os.Environ(), "AERONAUTES_SEGMENT_RESTART_AT="+tc.at, "AERONAUTES_SEGMENT_RESTART_DB="+path)
			output, err := child.CombinedOutput()
			if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 99 {
				t.Fatalf("child did not crash at the %s boundary (err=%v):\n%s", tc.at, err, output)
			}

			// A genuinely fresh process (this one ran no job) reopens the
			// database the crashed child left and runs startup recovery.
			database, err := db.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			store := jobs.NewStore(database)
			if _, err := store.FailInterrupted(ctx); err != nil {
				t.Fatal(err)
			}
			var jobID string
			if err := database.Reader().QueryRowContext(ctx, "SELECT id FROM jobs WHERE type=?", string(jobs.TypeDeleteSegments)).Scan(&jobID); err != nil {
				t.Fatalf("the crashed child left no segment-delete job: %v", err)
			}
			settled := map[string]*jobs.RecordedObservation{}
			for cursor := int64(0); ; {
				page, err := store.ReadItems(ctx, jobID, cursor, 200)
				if err != nil {
					t.Fatal(err)
				}
				for i := range page.Items {
					settled[page.Items[i].Key] = page.Items[i].Execution
				}
				if page.NextCursor == 0 {
					break
				}
				cursor = page.NextCursor
			}
			totals, err := store.ReadItemTotals(ctx, jobID)
			if err != nil {
				t.Fatal(err)
			}
			if totals.Admitted != 3 || totals.Issued != tc.issued || totals.Deleted != tc.deleted || totals.Unresolved != tc.unresolved {
				t.Fatalf("recovered totals = %+v, want admitted 3, issued %d, deleted %d, unresolved %d", totals, tc.issued, tc.deleted, tc.unresolved)
			}
			manifest := settled[manifestKey]
			if tc.manifestResult == "deleted" {
				if manifest == nil || manifest.Result != jobs.ItemDeleted {
					t.Fatalf("the manifest's deleted outcome did not survive the restart: %+v", manifest)
				}
			} else if manifest != nil && manifest.Result == jobs.ItemDeleted {
				t.Fatalf("an issued-only manifest was fabricated into a deleted outcome: %+v", manifest)
			}
			// The manifest's segments are listed from a map, so which one the
			// sequential segment phase reaches first is not fixed; the recovered
			// outcomes are asserted by count, not by which segment. No segment is
			// ever fabricated into a deleted outcome, and the segments that were
			// never issued are settled not attempted: both before the manifest is
			// deleted, and the one left untouched after the first is interrupted.
			notAttempted := 0
			for _, seg := range []string{seg1, seg2} {
				s := settled[seg]
				if s != nil && s.Result == jobs.ItemDeleted {
					t.Fatalf("a segment was fabricated into a deleted outcome at the %s boundary: %s %+v", tc.at, seg, s)
				}
				if s != nil && s.Result == jobs.ItemNotAttempted {
					notAttempted++
				}
			}
			wantNotAttempted := 2
			if tc.at == "after-manifest" {
				wantNotAttempted = 1 // the other segment is the interrupted, unresolved one
			}
			if notAttempted != wantNotAttempted {
				t.Fatalf("%d segments settled not attempted at the %s boundary, want %d: %+v", notAttempted, tc.at, wantNotAttempted, settled)
			}
		})
	}
}

// runSegmentRestartChild runs the real segment-delete job against the shared
// database path and crashes the process at the chosen boundary.
func runSegmentRestartChild(t *testing.T, crashAt, path string) {
	t.Helper()
	if path == "" {
		t.Fatal("the child needs a database path")
	}
	h := newHarnessAt(t, path, "")
	h.connect(t)
	bookmark := h.transfer.connections.ActiveBookmarkID()
	service := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.evidence)
	seedSLO(h, "media", "big.slo", "parts", map[string]string{"job/aa/000001": "0123456789", "job/aa/000002": "abcdefghij"})
	reviewed := reviewOf(t, service, bookmark, "media", "big.slo")
	h.mock.BeforeRequest = func(method, reqPath string) {
		switch crashAt {
		case "before-manifest":
			// Crash with the manifest DELETE still outstanding: it is issued,
			// its outcome unrecorded, and it may or may not have been deleted.
			if method == "DELETE" && strings.Contains(reqPath, "/media/big.slo") {
				os.Exit(99)
			}
		case "after-manifest":
			// Crash at the first segment request, immediately after the
			// manifest's deletion has been recorded and before any segment.
			if strings.Contains(reqPath, "/parts/") {
				os.Exit(99)
			}
		}
	}
	if _, err := service.DeleteManifestWithSegments(context.Background(), SegmentsDeleteRequestDTO{
		BookmarkID: bookmark, Container: "media", Name: "big.slo", Confirm: "big.slo", Force: true, Reviewed: reviewed,
	}); err != nil {
		t.Fatal(err)
	}
	// The job runs in the background; the request above returns its id at once.
	// The crash fires inside it; give it time, then fail if it never arrives.
	time.Sleep(30 * time.Second)
	t.Fatalf("the child did not crash at the %s boundary", crashAt)
}

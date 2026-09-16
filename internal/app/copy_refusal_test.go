package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

func TestCopyRefusalJobOutcomes(t *testing.T) {
	for _, outcome := range []string{"all-refused", "mixed", "cancel", "record-write-failure", "record-damaged"} {
		t.Run(outcome, func(t *testing.T) {
			h := newHarness(t)
			h.connect(t)
			source := h.transfer.connections.ActiveBookmarkID()
			dest, mock := secondCluster(t, h)
			h.mock.AddContainer("media", "standard")
			mock.AddContainer("backup", "standard")
			seedObject(h.mock, "media", "a-existing", "source replacement", nil)
			seedObject(mock, "backup", "a-existing", "preserve destination", nil)
			seedObject(mock, "unrelated", "sentinel", "outside bytes", nil)
			witness := mirrorWitness(t, mock, "test2:tester2", "testing2")
			before := mirrorSnapshot(t, witness, "backup", "unrelated")
			var block *engineBarrier
			if outcome == "cancel" || outcome == "record-damaged" {
				block = newEngineBarrier(t)
			}
			h.installEngine(block)
			if outcome == "mixed" || block != nil {
				seedObject(h.mock, "media", "z-new", "new bytes", nil)
			}
			spoolDir := t.TempDir()
			if outcome == "record-write-failure" {
				spoolDir = filepath.Join(spoolDir, "missing")
			}
			// Keep fault injection within this test's private local files.
			for _, variable := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(variable, spoolDir)
			}
			id, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{
				SourceBookmarkID: source, SourceContainer: "media", DestBookmarkID: dest, DestContainer: "backup",
			})
			if err != nil {
				t.Fatal(err)
			}
			if block != nil {
				block.waitUntilEntered(t)
				if outcome == "cancel" {
					if err := h.transfer.CancelJob(t.Context(), id); err != nil {
						t.Fatal(err)
					}
					// Leave the barrier closed so cancellation, not a racing
					// normal return, is what releases the test engine.
				} else {
					files, err := filepath.Glob(filepath.Join(spoolDir, "aeronautes-copy-refusals-*"))
					if err != nil || len(files) != 1 {
						t.Fatalf("expected one owned refusal record: %v, %v", files, err)
					}
					if err := os.Truncate(files[0], 0); err != nil {
						t.Fatal(err)
					}
					block.unblock()
				}
			}
			job := awaitTerminal(t, h, id)
			copied := int64(0)
			if outcome == "mixed" || outcome == "record-damaged" {
				copied = 1
			}
			if job.State != "partial" || job.Counters.OverwritesRefused != 1 || job.Counters.Overwrites != 0 || job.Counters.ObjectsCopied != copied || job.Counters.BytesCopied != 9*copied {
				t.Fatalf("refusal lost its partial outcome or copied totals: %+v", job)
			}
			if !strings.Contains(job.Error, "1 overwrite refused") || !strings.Contains(job.Error, "concurrent changes cannot be protected") {
				t.Errorf("refusal reason missing: %q", job.Error)
			}
			switch outcome {
			case "all-refused", "mixed":
				if job.Counters.ObjectsVerified != copied || job.Counters.ObjectsFailed != 0 {
					t.Errorf("refused item counted as verification or failure: %+v", job.Counters)
				}
			case "cancel":
				if !strings.Contains(job.Error, "context canceled") {
					t.Errorf("later cancellation detail lost: %q", job.Error)
				}
			case "record-write-failure":
				if !strings.Contains(job.Error, "create refusal record") {
					t.Errorf("later persistence error lost: %q", job.Error)
				}
			case "record-damaged":
				if !strings.Contains(job.Error, "verification: refusal record") || job.Counters.ObjectsVerified != 0 {
					t.Errorf("invalid verification presented as evidence: %+v", job)
				}
			}
			awaitJobAudit(t, h, id, "transfer.copy.partial")
			events, err := h.auditSvc.RecentFor(t.Context(), h.bookmarkID(), 20)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, event := range events {
				if event.Operation == "transfer.copy.partial" && strings.Contains(event.Summary, id) {
					found = true
					if event.Result != "verify_mismatch" || !strings.Contains(event.Summary, "1 overwrite refused") {
						t.Errorf("terminal audit lost refusal: %+v", event)
					}
				}
			}
			if !found {
				t.Fatal("missing refusal audit outcome")
			}
			after := mirrorSnapshot(t, witness, "backup", "unrelated")
			if copied == 1 {
				object, ok := after.Containers["backup"].Objects["z-new"]
				if !ok || object.Size != 9 || object.Digest == "" {
					t.Fatalf("eligible new object missing: %+v", object)
				}
				delete(after.Containers["backup"].Objects, "z-new")
			}
			if diff := oracle.Diff(before, after); !diff.Empty() {
				t.Fatalf("refusal changed existing or outside data: %s", diff)
			}
		})
	}
}

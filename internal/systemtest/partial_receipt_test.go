package systemtest

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/db"
	"github.com/csmart/aeronautes/internal/jobs"
)

func TestPartialReceiptRejectsFalseCredit(t *testing.T) {
	if probe := os.Getenv("AERONAUTES_PARTIAL_RECEIPT_PROBE"); probe != "" {
		r := &Receipt{h: &Harness{T: t, ctx: t.Context()}, action: "TransferService.StartCopy", jobID: "missing"}
		switch probe {
		case "start-error":
			r.err = errors.New("start refused")
		case "no-job":
			r.jobID = ""
		case "read-action":
			r.action = "BookmarkService.CredentialStatus"
		case "unknown-action":
			r.action = "MissingService.Start"
		default:
			database, err := db.Open(t.Context(), filepath.Join(t.TempDir(), "receipt.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := database.Close(); err != nil {
					t.Error(err)
				}
			})
			store := jobs.NewStore(database)
			job, err := store.Create(t.Context(), jobs.Job{Type: jobs.TypeCopy})
			if err != nil {
				t.Fatal(err)
			}
			for _, state := range []jobs.State{jobs.StatePlanning, jobs.StateRunning, jobs.StateVerifying, jobs.State(probe)} {
				if err := store.Transition(t.Context(), job.ID, state); err != nil {
					t.Fatal(err)
				}
			}
			r.jobID, r.h.Jobs = job.ID, app.NewJobService(store)
		}
		r.CreditPartial()
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, probe := range []string{"start-error", "no-job", "read-action", "unknown-action", "complete", "failed", "cancelled"} {
		cmd := exec.CommandContext(t.Context(), self, "-test.run=^TestPartialReceiptRejectsFalseCredit$", "-test.timeout=15s")
		cmd.Env = append(os.Environ(), "AERONAUTES_PARTIAL_RECEIPT_PROBE="+probe)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "CreditPartial") || strings.Contains(string(out), `"scenario":"partial"`) || strings.Contains(string(out), "panic:") {
			t.Fatalf("%s receipt was not refused cleanly: %v\n%s", probe, err, out)
		}
	}
}

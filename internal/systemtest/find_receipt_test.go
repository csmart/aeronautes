package systemtest

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

func TestFindReceiptRejectsFalseCredit(t *testing.T) {
	if probe := os.Getenv("AERONAUTES_FIND_RECEIPT_PROBE"); probe != "" {
		h := &Harness{T: t, Find: app.NewFindService(nil)}
		start := &Receipt{h: h, action: "FindService.StartFind", scanID: "scan"}
		status := &Receipt{h: h, action: "FindService.FindStatus", scanID: "scan", findStatus: &app.FindStatusDTO{Done: true}}
		cancel := &Receipt{h: h, action: "FindService.CancelFind", scanID: "scan"}
		ended := make(chan struct{})
		if probe != "cancel-request-live" {
			close(ended)
		}
		switch probe {
		case "complete-start-action", "cancel-start-action":
			start.action = "FindService.FindStatus"
		case "complete-start-error", "cancel-start-error":
			start.err = errors.New("start refused")
		case "complete-no-id", "cancel-no-id":
			start.scanID = ""
		case "complete-no-status":
			status = nil
		case "complete-other-harness":
			status.h = &Harness{T: t}
		case "complete-status-action":
			status.action = "FindService.CancelFind"
		case "complete-other-id":
			status.scanID = "another-scan"
		case "complete-status-error":
			status.err = errors.New("lookup failed")
		case "complete-no-snapshot":
			status.findStatus = nil
		case "complete-running":
			status.findStatus.Done = false
		case "complete-scan-error":
			status.findStatus.Error = "listing failed"
		case "cancel-no-call":
			cancel = nil
		case "cancel-other-harness":
			cancel.h = &Harness{T: t}
		case "cancel-action", "call-action":
			cancel.action = "FindService.FindStatus"
		case "cancel-other-id":
			cancel.scanID = "another-scan"
		case "cancel-error", "call-error":
			cancel.err = errors.New("cancellation failed")
		case "call-no-id":
			cancel.scanID = ""
		}
		switch {
		case strings.HasPrefix(probe, "complete-"):
			start.CreditFindComplete(status)
		case strings.HasPrefix(probe, "cancel-"):
			start.CreditFindCancelled(cancel, ended)
		case strings.HasPrefix(probe, "call-"):
			cancel.CreditFindCancellation()
		}
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, probe := range []string{
		"complete-start-action", "complete-start-error", "complete-no-id", "complete-no-status",
		"complete-other-harness", "complete-status-action", "complete-other-id", "complete-status-error",
		"complete-no-snapshot", "complete-running", "complete-scan-error", "cancel-start-action",
		"cancel-start-error", "cancel-no-id", "cancel-no-call", "cancel-other-harness", "cancel-action",
		"cancel-other-id", "cancel-error", "cancel-request-live", "call-action", "call-error", "call-no-id",
	} {
		cmd := exec.CommandContext(t.Context(), self, "-test.run=^TestFindReceiptRejectsFalseCredit$", "-test.timeout=15s")
		cmd.Env = append(os.Environ(), "AERONAUTES_FIND_RECEIPT_PROBE="+probe)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "credit needs") || strings.Contains(string(out), `"scenario":`) || strings.Contains(string(out), "panic:") {
			t.Fatalf("%s receipt was not refused cleanly: %v\n%s", probe, err, out)
		}
	}
}

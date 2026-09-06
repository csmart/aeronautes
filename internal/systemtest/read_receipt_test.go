package systemtest

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestReadReceiptRejectsFalseCredit(t *testing.T) {
	if probe := os.Getenv("AERONAUTES_READ_RECEIPT_PROBE"); probe != "" {
		r := &Receipt{h: &Harness{T: t}, action: "BookmarkService.CredentialStatus"}
		switch probe {
		case "failed":
			r.err = errors.New("read unavailable")
		case "mutate":
			r.action = "BookmarkService.Save"
		case "unknown":
			r.action = "MissingService.Read"
		}
		r.CreditRead()
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, probe := range []string{"failed", "mutate", "unknown"} {
		cmd := exec.Command(self, "-test.run=^TestReadReceiptRejectsFalseCredit$")
		cmd.Env = append(os.Environ(), "AERONAUTES_READ_RECEIPT_PROBE="+probe)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "not a successful read") || strings.Contains(string(out), "read completed") {
			t.Fatalf("%s receipt was not refused: %v\n%s", probe, err, out)
		}
	}
}

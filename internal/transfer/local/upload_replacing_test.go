package local

import (
	"testing"

	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

// ADR 0081 decision 1: an overwrite happens only when the caller asked for
// one, and asking means naming a different entry point rather than passing
// a value. The two reports are taken over the same selection against the
// same destination, so the only difference between them is which function
// was called.
func TestPreflightUploadReplacingAdmitsALiveNameOnlyWhenNamed(t *testing.T) {
	s, mock := session(t)
	seed(mock, "media", "live/taken", "the bytes already there")
	seed(mock, "outside", "keep", "outside sentinel")
	witness := uploadWitness(t, mock)
	before := uploadSnapshot(t, witness)
	dir := t.TempDir()
	taken := writeFile(t, dir, "taken", "the bytes this job would send")
	free := writeFile(t, dir, "free", "nothing is at this name")
	paths := []string{taken, free}

	refusing, err := PreflightUpload(t.Context(), s, "media", "live/", paths)
	if err != nil {
		t.Fatal(err)
	}
	if !refusing.Rejected() || len(refusing.Items) != 1 || refusing.Items[0].Name != "live/free" ||
		refusing.Items[0].Replaces {
		t.Fatalf("the ordinary preflight admitted a live name: rejected=%v items=%+v", refusing.Rejected(), refusing.Items)
	}
	if len(refusing.Problems) != 1 || refusing.Problems[0].Kind != "exists" || !refusing.Problems[0].Reject {
		t.Fatalf("the ordinary preflight did not refuse the collision: %+v", refusing.Problems)
	}

	replacing, err := PreflightUploadReplacing(t.Context(), s, "media", "live/", paths)
	if err != nil {
		t.Fatal(err)
	}
	if replacing.Rejected() {
		t.Fatalf("the replacing preflight still refuses: %+v", replacing.Problems)
	}
	// The collision is admitted AND still reported: it is the thing the
	// operator acknowledged, not a reason to stop and not a silence.
	if len(replacing.Problems) != 1 || replacing.Problems[0].Kind != "exists" || replacing.Problems[0].Reject ||
		replacing.Problems[0].Name != "live/taken" {
		t.Fatalf("the replacing preflight lost or refused the collision report: %+v", replacing.Problems)
	}
	byName := map[string]UploadItem{}
	for _, item := range replacing.Items {
		byName[item.Name] = item
	}
	if len(byName) != 2 {
		t.Fatalf("the replacing preflight planned %d items: %+v", len(byName), replacing.Items)
	}
	if !byName["live/taken"].Replaces {
		t.Errorf("the live name is not marked as replacing: %+v", byName["live/taken"])
	}
	if byName["live/free"].Replaces {
		t.Errorf("a name that was free is marked as replacing: %+v", byName["live/free"])
	}
	if replacing.TotalBytes != byName["live/taken"].Size+byName["live/free"].Size {
		t.Errorf("the replacing preflight's byte total omits the replaced name: %+v", replacing)
	}

	// Neither report writes anything: a preflight is a read.
	if diff := oracle.Diff(before, uploadSnapshot(t, witness)); !diff.Empty() {
		t.Fatalf("a preflight changed the account: %s", diff)
	}
}

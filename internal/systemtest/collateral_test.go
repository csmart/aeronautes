package systemtest

import (
	"testing"

	"github.com/csmart/aeronautes/internal/systemtest/oracle"
)

// TestCollateralDetection unit-tests the negative-space core without
// SAIO: an ACL flip, a stray object, and a stray content change are all
// caught unless explicitly allowed; a bare container name does NOT
// blanket-allow object changes inside it.
func TestCollateralDetection(t *testing.T) {
	base := func() *oracle.AccountSnapshot {
		return &oracle.AccountSnapshot{Containers: map[string]oracle.ContainerState{
			"c": {Name: "c", Objects: map[string]oracle.ObjectState{
				"keep": {Name: "keep", Digest: "d1"},
			}},
		}}
	}
	// 1. An ACL flip with no allow is collateral.
	before, after := base(), base()
	cc := after.Containers["c"]
	cc.ReadACL = ".r:*"
	after.Containers["c"] = cc
	if s := collateral(before, after, nil); len(s) != 1 {
		t.Fatalf("ACL flip not caught: %v", s)
	}
	// Allowed by the config change string OR the bare container name.
	if s := collateral(before, after, []string{"c: readACL"}); len(s) != 0 {
		t.Fatalf("allowed ACL change still flagged: %v", s)
	}
	if s := collateral(before, after, []string{"c"}); len(s) != 0 {
		t.Fatalf("bare-container allow should cover config: %v", s)
	}

	// 2. A stray added object; a bare container allow must NOT cover it.
	before, after = base(), base()
	after.Containers["c"].Objects["intruder"] = oracle.ObjectState{Name: "intruder", Digest: "d9"}
	if s := collateral(before, after, []string{"c"}); len(s) != 1 {
		t.Fatalf("bare container wrongly blanket-allowed an object add: %v", s)
	}
	if s := collateral(before, after, []string{"c/intruder"}); len(s) != 0 {
		t.Fatalf("explicitly allowed object add still flagged: %v", s)
	}

	// 3. A same-name content change is collateral unless allowed by name.
	before, after = base(), base()
	after.Containers["c"].Objects["keep"] = oracle.ObjectState{Name: "keep", Digest: "d2"}
	if s := collateral(before, after, nil); len(s) != 1 {
		t.Fatalf("content change not caught: %v", s)
	}
	if s := collateral(before, after, []string{"c/keep"}); len(s) != 0 {
		t.Fatalf("allowed content change still flagged: %v", s)
	}

	// 4. An object name containing a colon parses on the exact "container/
	// name: <what>" separator, so its allow ref still matches.
	before, after = base(), base()
	before.Containers["c"].Objects["ts:2026"] = oracle.ObjectState{Name: "ts:2026", Digest: "d1"}
	after.Containers["c"].Objects["ts:2026"] = oracle.ObjectState{Name: "ts:2026", Digest: "d2"}
	if s := collateral(before, after, nil); len(s) != 1 {
		t.Fatalf("colon-name content change not caught: %v", s)
	}
	if s := collateral(before, after, []string{"c/ts:2026"}); len(s) != 0 {
		t.Fatalf("colon-name allow ref failed to match: %v", s)
	}
}

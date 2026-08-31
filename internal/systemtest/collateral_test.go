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
	// A config change is allowed by the bare container name (which covers
	// any config change to THAT exact container).
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

	// 4. A whole new container with an object: allowing the bare container
	// name covers the container add but NOT the object inside it (H-05).
	before = &oracle.AccountSnapshot{Containers: map[string]oracle.ContainerState{}}
	after = &oracle.AccountSnapshot{Containers: map[string]oracle.ContainerState{
		"newc": {Name: "newc", Objects: map[string]oracle.ObjectState{
			"smuggled": {Name: "smuggled", Digest: "d9"},
		}},
	}}
	if s := collateral(before, after, []string{"newc"}); len(s) != 1 {
		t.Fatalf("bare container allow should still flag the object inside a new container: %v", s)
	}
	if s := collateral(before, after, []string{"newc", "newc/smuggled"}); len(s) != 0 {
		t.Fatalf("explicitly allowing container+object still flagged: %v", s)
	}

	// 5. An object name containing a colon still matches its own allow ref
	// (identity is a typed tuple, not a parsed string).
	before, after = base(), base()
	before.Containers["c"].Objects["ts:2026"] = oracle.ObjectState{Name: "ts:2026", Digest: "d1"}
	after.Containers["c"].Objects["ts:2026"] = oracle.ObjectState{Name: "ts:2026", Digest: "d2"}
	if s := collateral(before, after, nil); len(s) != 1 {
		t.Fatalf("colon-name content change not caught: %v", s)
	}
	if s := collateral(before, after, []string{"c/ts:2026"}); len(s) != 0 {
		t.Fatalf("colon-name allow ref failed to match: %v", s)
	}

	// 6. THE F-01 ATTACK: an object literally named "approved: smuggled"
	// (containing the display separator ": ") must NOT be hidden by an
	// allow for a DIFFERENT object "c/approved". A string-parsing helper
	// split "c/approved: smuggled: content" at the first ": " and wrongly
	// matched "c/approved"; typed identity does not.
	before, after = base(), base()
	before.Containers["c"].Objects["approved: smuggled"] = oracle.ObjectState{Name: "approved: smuggled", Digest: "d1"}
	after.Containers["c"].Objects["approved: smuggled"] = oracle.ObjectState{Name: "approved: smuggled", Digest: "dX"}
	if s := collateral(before, after, []string{"c/approved"}); len(s) != 1 {
		t.Fatalf("colon-space name masqueraded as an allowed object: %v", s)
	}
	if s := collateral(before, after, []string{"c/approved: smuggled"}); len(s) != 0 {
		t.Fatalf("the real object's own allow ref failed to match: %v", s)
	}

	// 7. The same collision shape for containers: a config change to a
	// container named "c: other" is not covered by an allow for "c".
	before = &oracle.AccountSnapshot{Containers: map[string]oracle.ContainerState{
		"c: other": {Name: "c: other", ReadACL: ""},
	}}
	after = &oracle.AccountSnapshot{Containers: map[string]oracle.ContainerState{
		"c: other": {Name: "c: other", ReadACL: ".r:*"},
	}}
	if s := collateral(before, after, []string{"c"}); len(s) != 1 {
		t.Fatalf("config change to \"c: other\" masqueraded as allowed container \"c\": %v", s)
	}
	if s := collateral(before, after, []string{"c: other"}); len(s) != 0 {
		t.Fatalf("the real container's own allow failed to match: %v", s)
	}
}

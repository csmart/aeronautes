package main

import "testing"

func TestRecentRefusalClaimIsRequired(t *testing.T) {
	stream := pkgP("pass", tSeg("TestRecent", "pass", claimLine("TestRecent", "AuditService.Recent", "refusal")))
	rep := reconcileOK(t, stream, validOpts("p"))
	if !rep.Valid || !coveredScenarios(rep, "AuditService.Recent")["refusal"] {
		t.Fatalf("passing Recent refusal must earn its required cell: %+v", rep)
	}
}

func TestRecentHappyClaimIsRejected(t *testing.T) {
	stream := pkgP("pass", tSeg("TestRecent", "pass", claimLine("TestRecent", "AuditService.Recent", "happy")))
	mustError(t, stream, validOpts("p"))
}

package scenariocontract

import "testing"

// TestValidatePasses guards the shipped contract: every row is well-formed.
func TestValidatePasses(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatalf("shipped contract does not validate: %v", err)
	}
}

// TestAllIsADefensiveCopy proves a consumer cannot mutate the shared
// contract through the map All returns.
func TestAllIsADefensiveCopy(t *testing.T) {
	m := All()
	before := len(m)
	delete(m, "AppService.About")
	m["Bogus.Injected"] = Action{Kind: Read}
	if s := m["MutateService.UpdateContainer"]; len(s.Scenarios) > 0 {
		s.Scenarios[0] = "tampered"
	}
	again := All()
	if len(again) != before {
		t.Fatalf("All() count changed after caller mutation: got %d want %d", len(again), before)
	}
	if _, ok := again["AppService.About"]; !ok {
		t.Error("caller's delete leaked into the shared contract")
	}
	if _, ok := again["Bogus.Injected"]; ok {
		t.Error("caller's insert leaked into the shared contract")
	}
	if got := again["MutateService.UpdateContainer"].Scenarios[0]; got != "happy" {
		t.Errorf("caller mutated a shared Scenarios slice: first scenario is %q", got)
	}
}

// TestHashIsDeterministic: the same contract hashes the same across calls
// (it feeds the report, so it must be stable).
func TestHashIsDeterministic(t *testing.T) {
	if Hash() != Hash() {
		t.Fatal("Hash is not deterministic")
	}
	if Hash() == "" {
		t.Fatal("Hash is empty")
	}
}

// TestValidateRejectsMalformed exercises each rejection branch against
// synthetic contracts, so the well-formedness guarantee is actually tested
// and not just asserted on the shipped (valid) contract.
func TestValidateRejectsMalformed(t *testing.T) {
	cases := map[string]map[string]Action{
		"unknown kind":     {"X.Y": {Kind: "bogus"}},
		"unknown scenario": {"X.Y": {Kind: Read, Scenarios: []string{"happy", "nope"}}},
		"duplicate":        {"X.Y": {Kind: Read, Scenarios: []string{"happy", "happy"}}},
		"missing happy":    {"X.Y": {Kind: Read, Scenarios: []string{"refusal"}}},
	}
	for name, inv := range cases {
		if err := validateInventory(inv); err == nil {
			t.Errorf("%s: expected a validation error, got nil", name)
		}
	}
	if err := validateInventory(map[string]Action{"X.Y": {Kind: Read}}); err != nil {
		t.Errorf("a well-formed synthetic contract should validate: %v", err)
	}
}

// TestRequires checks the membership helper the emitter validates against.
func TestRequires(t *testing.T) {
	if !Requires("MutateService.UpdateObjectMetadata", "conflict") {
		t.Error("UpdateObjectMetadata should require conflict")
	}
	if Requires("MutateService.UpdateObjectMetadata", "cancel") {
		t.Error("UpdateObjectMetadata should not require cancel")
	}
	if Requires("Nope.NotAnAction", "happy") {
		t.Error("unknown action should require nothing")
	}
	if Requires("AppService.About", "refusal") {
		t.Error("a read action should not require refusal")
	}
}

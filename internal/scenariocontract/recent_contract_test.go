package scenariocontract

import (
	"slices"
	"testing"
)

func TestRecentFloorMatchesPublicRefusal(t *testing.T) {
	action := All()["AuditService.Recent"]
	if action.Kind != Read || !slices.Equal(action.Required(), []string{"refusal"}) {
		t.Fatalf("Recent always refuses by ADR0059: got %+v, want Read with only refusal", action)
	}
}

func TestOnlyRecentMayOmitHappy(t *testing.T) {
	for name, action := range All() {
		t.Run(name, func(t *testing.T) {
			candidate := All()
			action.Scenarios = []string{"refusal"}
			candidate[name] = action
			err := validateInventory(candidate)
			wantValid := name == "AuditService.Recent"
			if (err == nil) != wantValid {
				t.Fatalf("refusal-only row %s: validation error = %v, want valid = %t", name, err, wantValid)
			}
		})
	}
}

func TestRecentRequiresExactRefusalContract(t *testing.T) {
	cases := map[string]Action{
		"default happy":    {Kind: Read},
		"explicit happy":   {Kind: Read, Scenarios: []string{"happy"}},
		"mixed happy":      {Kind: Read, Scenarios: []string{"happy", "refusal"}},
		"missing refusal":  {Kind: Read, Scenarios: []string{"cancel"}},
		"extra scenario":   {Kind: Read, Scenarios: []string{"refusal", "retry"}},
		"duplicate":        {Kind: Read, Scenarios: []string{"refusal", "refusal"}},
		"unknown scenario": {Kind: Read, Scenarios: []string{"refusal", "unknown"}},
	}
	for kind := range knownKinds {
		if kind != Read {
			cases[string(kind)] = Action{Kind: kind, Scenarios: []string{"refusal"}}
		}
	}
	cases["unknown kind"] = Action{Kind: "unknown", Scenarios: []string{"refusal"}}
	for name, action := range cases {
		t.Run(name, func(t *testing.T) {
			candidate := All()
			candidate["AuditService.Recent"] = action
			if err := validateInventory(candidate); err == nil {
				t.Fatalf("invalid Recent contract accepted: %+v", action)
			}
		})
	}
}

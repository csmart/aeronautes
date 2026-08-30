package systemtest

import (
	"encoding/json"
	"testing"
)

// TestEmitScenarioInventory prints the machine-readable inventory contract
// so cmd/scenariocov can build the coverage denominator from the
// `go test -json` stream without importing this test package. It always
// runs (no SAIO needed), so the denominator is present even in the
// non-SAIO job.
func TestEmitScenarioInventory(t *testing.T) {
	t.Logf("%s%s", scenarioInventoryMarker, inventoryJSON())
}

// TestScenarioEmissionShape guards the two emitted line formats: they must
// carry the marker and decode as the structs the tool expects. This is a
// pure format check; the reconciliation logic itself is tested in
// cmd/scenariocov.
func TestScenarioEmissionShape(t *testing.T) {
	var inv map[string]inventoryAction
	if err := json.Unmarshal([]byte(inventoryJSON()), &inv); err != nil {
		t.Fatalf("inventory JSON does not decode: %v", err)
	}
	if len(inv) != len(Inventory) {
		t.Fatalf("inventory JSON has %d actions, want %d", len(inv), len(Inventory))
	}
	for key, a := range inv {
		if _, ok := Inventory[key]; !ok {
			t.Errorf("inventory JSON names unknown action %q", key)
		}
		if len(a.Required) == 0 {
			t.Errorf("inventory JSON action %q has no required scenarios", key)
		}
	}

	var c claimLine
	if err := json.Unmarshal([]byte(claimJSON("TransferService.StartCopy", "happy", "job x complete")), &c); err != nil {
		t.Fatalf("claim JSON does not decode: %v", err)
	}
	if c.Action != "TransferService.StartCopy" || c.Scenario != "happy" {
		t.Fatalf("claim JSON round-trip wrong: %+v", c)
	}
}

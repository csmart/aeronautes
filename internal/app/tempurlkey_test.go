package app

import (
	"errors"
	"testing"

	"github.com/csmart/aeronautes/internal/swiftcontrol"
)

// ADR 0101 decision 2: the record names the slot and what was asked of it for
// every outcome, the unverified and failed ones an operator has to follow up
// included, and never quotes a slot the adapter would refuse.
func TestTempURLKeyFactNamesTheSlotForEveryOutcome(t *testing.T) {
	applied := &swiftcontrol.TempURLKeyResult{Outcome: swiftcontrol.UpdateApplied}
	for _, c := range []struct {
		name   string
		result *swiftcontrol.TempURLKeyResult
		err    error
		slot   string
		clear  bool
		want   string
	}{
		{"applied set", applied, nil, "temp-url-key", false, "set TempURL key slot Temp-Url-Key"},
		{"applied clear", applied, nil, "Temp-URL-Key-2", true, "cleared TempURL key slot Temp-Url-Key-2"},
		{"unverified clear", nil, swiftcontrol.ErrUnverifiedTempURLKeyWrite, "Temp-URL-Key-2", true, "clearing TempURL key slot Temp-Url-Key-2 could not be verified"},
		{"failed set", nil, errors.New("503"), "Temp-URL-Key", false, "setting TempURL key slot Temp-Url-Key failed"},
		{"failed on a slot Swift does not read", nil, errors.New("refused"), "Temp-URL-Key-3\nforged", false, "setting TempURL key slot (unrecognised) failed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, _, summary, _ := tempURLKeyFact(c.result, c.err, c.slot, c.clear); summary != c.want {
				t.Errorf("summary = %q, want %q", summary, c.want)
			}
		})
	}
}

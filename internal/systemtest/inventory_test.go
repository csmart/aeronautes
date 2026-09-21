package systemtest

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
	"github.com/csmart/aeronautes/internal/scenariocontract"
)

// boundServices derives the bridge surface from the single source of
// truth in the app package (app.BoundServices) rather than a second
// handwritten copy that could drift from main.go. main_binding_test.go
// proves main.go binds exactly these constructors (grok war-room P2).
func boundServices() map[string]any {
	m := map[string]any{}
	for _, b := range app.BoundServices() {
		m[b.Name] = b.Prototype
	}
	return m
}

// TestInventoryCoversEveryBoundAction is the §65.10 auto-inventory gate:
// every exported method on a bound service MUST have a contract row, and
// every contract row must name a real bound method. A new bridge action
// cannot ship without declaring its required scenarios. The contract lives
// in internal/scenariocontract so the reconciler reads the same denominator.
func TestInventoryCoversEveryBoundAction(t *testing.T) {
	contract := scenariocontract.All()
	live := map[string]bool{}
	for svcName, svc := range boundServices() {
		typ := reflect.TypeOf(svc)
		for i := 0; i < typ.NumMethod(); i++ {
			method := typ.Method(i)
			key := svcName + "." + method.Name
			// Wails reserves this lifecycle hook in both the runtime and
			// generator; it must not inflate renderer scenario coverage.
			if key == "FindService.ServiceShutdown" {
				if method.Type != reflect.TypeFor[func(*app.FindService) error]() {
					t.Errorf("Find lifecycle hook has unexpected signature: %v", method.Type)
				}
				continue
			}
			live[key] = true
			if _, ok := contract[key]; !ok {
				t.Errorf("bound bridge method %s has no contract row — add it (and its scenarios) to scenariocontract", key)
			}
		}
	}
	for key := range contract {
		if !live[key] {
			t.Errorf("contract row %s names no bound method — remove or fix it", key)
		}
	}
}

// TestInventoryIsWellFormed asserts the contract validates: a known kind, a
// non-empty required set from the known vocabulary with no duplicates, and
// the per-action baseline. The check itself lives in scenariocontract.Validate
// (and is unit-tested there); this keeps the systemtest suite red if the
// shared contract is ever malformed.
func TestInventoryIsWellFormed(t *testing.T) {
	if err := scenariocontract.Validate(); err != nil {
		t.Errorf("scenario contract is malformed: %v", err)
	}
}

// TestInventoryLogsRequiredScenarios logs the static contract — every
// action, its kind, and the scenarios it requires — as a human-readable
// diagnostic. It is NOT the denominator source; cmd/scenariocov imports the
// contract directly.
func TestInventoryLogsRequiredScenarios(t *testing.T) {
	contract := scenariocontract.All()
	kinds := map[scenariocontract.Kind]int{}
	var rows []string
	total := 0
	for key, action := range contract {
		kinds[action.Kind]++
		total += len(action.Required())
		rows = append(rows, key+" ["+string(action.Kind)+"] needs: "+strings.Join(action.Required(), ","))
	}
	sort.Strings(rows)
	t.Logf("ACTION CONTRACT (%d actions: %d read, %d session, %d mutate, %d destructive, %d job; %d required scenarios; hash %s)\n%s",
		len(contract), kinds[scenariocontract.Read], kinds[scenariocontract.Session], kinds[scenariocontract.Mutate],
		kinds[scenariocontract.Destructive], kinds[scenariocontract.Job], total, scenariocontract.Hash(), strings.Join(rows, "\n"))
}

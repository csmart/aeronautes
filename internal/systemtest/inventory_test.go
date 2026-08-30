package systemtest

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// boundServices derives the bridge surface from the single source of
// truth in the app package (app.BoundServices) rather than a second
// handwritten copy that could drift from main.go. main_test.go proves
// main.go binds exactly these constructors (grok war-room P2).
func boundServices() map[string]any {
	m := map[string]any{}
	for _, b := range app.BoundServices() {
		m[b.Name] = b.Prototype
	}
	return m
}

// TestInventoryCoversEveryBoundAction is the §65.10 auto-inventory gate:
// every exported method on a bound service MUST have an inventory row,
// and every inventory row must name a real bound method. A new bridge
// action cannot ship without declaring its required scenarios.
func TestInventoryCoversEveryBoundAction(t *testing.T) {
	live := map[string]bool{}
	for svcName, svc := range boundServices() {
		typ := reflect.TypeOf(svc)
		for i := 0; i < typ.NumMethod(); i++ {
			key := svcName + "." + typ.Method(i).Name
			live[key] = true
			if _, ok := Inventory[key]; !ok {
				t.Errorf("bound bridge method %s has no inventory row — add it (and its scenarios) to Inventory", key)
			}
		}
	}
	for key := range Inventory {
		if !live[key] {
			t.Errorf("inventory row %s names no bound method — remove or fix it", key)
		}
	}
}

// TestInventoryLogsRequiredScenarios logs the static contract — every
// action, its kind, and the scenarios it requires. The COVERAGE numbers
// (what actually ran) are emitted separately by TestMain from the
// evidence-based registry, so they cannot be gamed by a hand map.
func TestInventoryLogsRequiredScenarios(t *testing.T) {
	kinds := map[Kind]int{}
	var rows []string
	total := 0
	for key, action := range Inventory {
		kinds[action.Kind]++
		total += len(action.required())
		rows = append(rows, key+" ["+string(action.Kind)+"] needs: "+strings.Join(action.required(), ","))
	}
	sort.Strings(rows)
	t.Logf("ACTION INVENTORY (%d actions: %d read, %d session, %d mutate, %d destructive, %d job; %d required scenarios)\n%s",
		len(Inventory), kinds[Read], kinds[Session], kinds[Mutate], kinds[Destructive], kinds[Job], total, strings.Join(rows, "\n"))
}

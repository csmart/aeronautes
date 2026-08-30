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

// TestInventoryReportsCoverage emits the coverage report artifact: every
// action, its kind, and its required scenarios. This is the generated
// fact behind "full action coverage", not a claim. Scenario-completion
// tracking (which system tests satisfy which requirement) lands as the
// matrices fill in.
func TestInventoryReportsCoverage(t *testing.T) {
	kinds := map[Kind]int{}
	var rows []string
	for key, action := range Inventory {
		kinds[action.Kind]++
		rows = append(rows, key+" ["+string(action.Kind)+"] needs: "+strings.Join(action.required(), ","))
	}
	sort.Strings(rows)
	t.Logf("ACTION INVENTORY (%d actions: %d read, %d session, %d mutate, %d destructive, %d job)\n%s",
		len(Inventory), kinds[Read], kinds[Session], kinds[Mutate], kinds[Destructive], kinds[Job], strings.Join(rows, "\n"))

	// Validate the Covered map so the numerator cannot be inflated by a
	// bogus entry (codex war-room H-02): every key must name a real
	// action, every scenario must be one that action actually requires,
	// and no scenario may be listed twice.
	var covered int
	for key, scns := range Covered {
		action, ok := Inventory[key]
		if !ok {
			t.Errorf("Covered names %q, which is not an inventoried action", key)
			continue
		}
		req := map[string]bool{}
		for _, r := range action.required() {
			req[r] = true
		}
		seen := map[string]bool{}
		for _, s := range scns {
			if seen[s] {
				t.Errorf("Covered[%q] lists %q twice", key, s)
				continue
			}
			seen[s] = true
			if !req[s] {
				t.Errorf("Covered[%q] lists %q, which is not a required scenario for that action", key, s)
				continue
			}
			covered++ // a genuine, required, non-duplicate coverage claim
		}
	}

	// The reportable coverage artifact (§65.10): required scenarios met
	// vs. still open, per action.
	gaps := CoverageGaps()
	var required int
	for _, action := range Inventory {
		required += len(action.required())
	}
	var gapRows []string
	for key, missing := range gaps {
		gapRows = append(gapRows, key+": "+strings.Join(missing, ","))
	}
	sort.Strings(gapRows)
	t.Logf("SCENARIO COVERAGE: %d/%d required scenarios have a system test; %d actions still have gaps:\n%s",
		covered, required, len(gaps), strings.Join(gapRows, "\n"))
}

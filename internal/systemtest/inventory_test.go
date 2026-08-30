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

// TestInventoryIsWellFormed validates every inventory row so the
// denominator cannot be silently weakened (codex war-room R-02): an
// invalid or omitted Kind yields zero requirements and would mark the
// action complete with no test. Each action must have a KNOWN kind, a
// non-empty required set drawn from the known scenario vocabulary with no
// duplicates, and must at least require the "happy" baseline.
func TestInventoryIsWellFormed(t *testing.T) {
	knownKinds := map[Kind]bool{Read: true, Session: true, Mutate: true, Destructive: true, Job: true}
	knownScenarios := map[string]bool{"happy": true, "refusal": true, "conflict": true, "cancel": true, "partial": true, "retry": true}
	for key, a := range Inventory {
		if !knownKinds[a.Kind] {
			t.Errorf("%s: unknown Kind %q", key, a.Kind)
		}
		req := a.required()
		if len(req) == 0 {
			t.Errorf("%s: zero required scenarios — an invalid Kind or empty override would silently mark it complete", key)
			continue
		}
		seen := map[string]bool{}
		for _, s := range req {
			if !knownScenarios[s] {
				t.Errorf("%s: required scenario %q is not in the known vocabulary", key, s)
			}
			if seen[s] {
				t.Errorf("%s: required scenario %q listed twice", key, s)
			}
			seen[s] = true
		}
		if !seen["happy"] {
			t.Errorf("%s: does not require the baseline 'happy' scenario", key)
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

package systemtest

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/app"
)

// boundServices mirrors the application.NewService list in main.go — the
// exact set whose exported methods the Wails bridge surfaces to the
// webview. If main.go binds a new service, add it here.
func boundServices() map[string]any {
	return map[string]any{
		"AppService":       &app.AppService{},
		"BookmarkService":  &app.BookmarkService{},
		"BrowseService":    &app.BrowseService{},
		"InspectorService": &app.InspectorService{},
		"MutateService":    &app.MutateService{},
		"AuditService":     &app.AuditService{},
		"TransferService":  &app.TransferService{},
		"DeleteService":    &app.DeleteService{},
		"JobService":       &app.JobService{},
		"FindService":      &app.FindService{},
		"SettingsService":  &app.SettingsService{},
		"BenchmarkService": &app.BenchmarkService{},
	}
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
		rows = append(rows, key+" ["+string(action.Kind)+"] needs: "+strings.Join(action.Kind.requiredScenarios(), ","))
	}
	sort.Strings(rows)
	t.Logf("ACTION INVENTORY (%d actions: %d read, %d session, %d mutate, %d destructive)\n%s",
		len(Inventory), kinds[Read], kinds[Session], kinds[Mutate], kinds[Destructive], strings.Join(rows, "\n"))
}

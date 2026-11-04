package app

import (
	"sort"
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/scenariocontract"
)

func forceV1Gate(t *testing.T) {
	t.Helper()
	if !v1GateForced.CompareAndSwap(false, true) {
		t.Fatal("v1 gate already forced; tests that force it must not overlap")
	}
	t.Cleanup(func() { v1GateForced.Store(false) })
}

func assertV1Refusal(t *testing.T, err error, capability string) {
	t.Helper()
	if err == nil {
		t.Fatal("deferred action did not refuse")
	}
	if !strings.Contains(err.Error(), capability) {
		t.Errorf("refusal does not name %s: %v", capability, err)
	}
	if !strings.Contains(err.Error(), "not available in this release") {
		t.Errorf("refusal does not say the build does not offer it: %v", err)
	}
}

// The gate's table must stay exhaustive against the action inventory, so a
// new bridge method cannot land unclassified and silently reachable in a
// v1 build (ADR 0052 decision 3). Classification is "named in the table or
// deliberately kept"; this asserts the table names nothing the inventory
// does not, which is the half a typo or a renamed method breaks.
func TestV1DeferredActionsAreInTheInventory(t *testing.T) {
	inventory := scenariocontract.All()
	var unknown []string
	for action := range v1DeferredActions {
		if _, ok := inventory[action]; !ok {
			unknown = append(unknown, action)
		}
	}
	sort.Strings(unknown)
	if len(unknown) != 0 {
		t.Errorf("deferred table names actions the inventory does not have: %v", unknown)
	}
}

// The other half: every inventory action is either deferred or kept, and
// the kept set is stated here rather than inferred, so adding a bridge
// method forces a decision instead of defaulting to reachable.
func TestEveryBridgeActionIsClassified(t *testing.T) {
	kept := map[string]bool{
		"AppService.About": true, "AppService.Info": true,
		"AuditService.Recent": true, "AuditService.RecentFor": true, "AuditService.Search": true,
		// The §51 export archives the trail to a local file; it gates
		// nothing and lifts nothing, so it is kept.
		"AuditService.ExportAudit": true,
		// The §51/P28 settings export writes config to a local file; it
		// gates nothing and lifts nothing, so it is kept.
		"BackupService.ExportSettings": true,
		// The settings restore merges backed-up configuration; it gates and
		// lifts nothing, so it is kept.
		"BackupService.ImportSettings":     true,
		"BenchmarkService.Latest":          true,
		"BookmarkService.CredentialStatus": true, "BookmarkService.Delete": true,
		"BookmarkService.List": true, "BookmarkService.Save": true,
		"BookmarkService.Verify": true,
		"BrowseService.Account":  true, "BrowseService.Capabilities": true,
		"BrowseService.Connect": true, "BrowseService.Containers": true,
		"BrowseService.Objects":  true,
		"FindService.CancelFind": true, "FindService.FindStatus": true,
		"FindService.StartFind":      true,
		"InspectorService.Container": true, "InspectorService.DLOSegments": true,
		"InspectorService.Object": true, "InspectorService.Prefix": true,
		"InspectorService.SLOManifest": true,
		"JobService.Get":               true, "JobService.History": true,
		"JobService.Items": true, "JobService.ItemTotals": true,
		"JobService.List": true, "JobService.RecoveryStatus": true,
		// ADR 0088's notice read and dismissal are always available: the
		// decision lifts nothing and gates nothing, so both are kept.
		"JobService.InterruptedDisclosures": true, "JobService.DismissInterrupted": true,
		"MutateService.CreateContainer": true, "MutateService.CreateMarker": true,
		// Upload was returned on 16 October 2026, once ADR 0085's comparison
		// completed its path: its three actions are kept rather than deferred.
		"TransferService.PickUploadFiles": true, "TransferService.PreflightUpload": true,
		"TransferService.StartUpload": true,
		// Object deletion was returned on 29 October 2026 (ADR 0052 decision
		// 11): one object (ADR 0092) and the selected set (ADR 0093) are kept.
		"MutateService.DeleteObject": true, "DeleteService.StartSelectedDelete": true,
		// Marker and manifest deletion returned on 1 November 2026 (ADR 0052
		// decision 13) under the broadened gate-lift grant.
		"MutateService.DeleteMarker": true, "MutateService.DeleteManifest": true,
		// Bulk deletion and the manifest-with-segments job returned on 1
		// November 2026 (ADR 0052 decision 14).
		"DeleteService.PreviewDelete": true, "DeleteService.StartDelete": true,
		"DeleteService.CheckSegmentOwnership": true, "DeleteService.DeleteManifestWithSegments": true,
		// Copy and its job re-run were returned on 31 October 2026 (ADR 0052
		// decision 12), once ADR 0087's overwrite and ADR 0096's full-fidelity
		// skip completed copy's path.
		"TransferService.PlanCopy": true, "TransferService.StartCopy": true,
		"TransferService.RerunJob":           true,
		"MutateService.UpdateContainer":      true,
		"MutateService.UpdateObjectMetadata": true,
		"SettingsService.Get":                true, "SettingsService.Set": true,
		"TransferService.CancelJob": true, "TransferService.PickDownloadDir": true,
		"TransferService.PreflightDownload": true, "TransferService.StartDownload": true,
	}
	var unclassified, both []string
	for action := range scenariocontract.All() {
		_, deferred := v1DeferredActions[action]
		switch {
		case deferred && kept[action]:
			both = append(both, action)
		case !deferred && !kept[action]:
			unclassified = append(unclassified, action)
		}
	}
	sort.Strings(unclassified)
	sort.Strings(both)
	if len(unclassified) != 0 {
		t.Errorf("bridge actions are neither deferred nor kept, so a v1 build would offer them by default: %v", unclassified)
	}
	if len(both) != 0 {
		t.Errorf("bridge actions are both deferred and kept: %v", both)
	}
	if got, want := len(v1DeferredActions)+len(kept), len(scenariocontract.All()); got != want {
		t.Errorf("classification covers %d actions, inventory has %d", got, want)
	}
}

// A refusal must name the capability and say the build does not offer it,
// rather than reading as a failure of the action itself.
func TestRefuseIfDeferredNamesTheCapability(t *testing.T) {
	forceV1Gate(t)
	assertV1Refusal(t, refuseIfDeferred("BenchmarkService.StartBenchmark"), "Benchmark")
}

// A kept action must not refuse, or the gate would withdraw v1 itself.
func TestKeptActionsAreNotRefused(t *testing.T) {
	forceV1Gate(t)
	for _, action := range []string{
		"TransferService.StartDownload",
		"TransferService.PreflightDownload",
		"TransferService.PickDownloadDir",
		// Returned on 16 October 2026 (ADR 0052 decision 9). The
		// classification test above catches a name put back into the
		// deferred table; these three are the behavioural half, which is
		// what an operator would experience if it were.
		"TransferService.PickUploadFiles",
		"TransferService.PreflightUpload",
		"TransferService.StartUpload",
		// Returned on 29 October 2026 (ADR 0052 decision 11); the behavioural
		// half of the classification change above.
		"MutateService.DeleteObject",
		"DeleteService.StartSelectedDelete",
		"TransferService.CancelJob",
		"MutateService.UpdateObjectMetadata",
		"MutateService.UpdateContainer",
		"MutateService.CreateContainer",
		"BrowseService.Containers",
		"InspectorService.Object",
	} {
		if err := refuseIfDeferred(action); err != nil {
			t.Errorf("%s is kept in v1 but the gate refused it: %v", action, err)
		}
	}
}

// An action absent from the table is not refused. This states the default
// plainly so nobody reads the gate as a denylist that fails closed: it
// does not, and TestEveryBridgeActionIsClassified is what stops a new
// method relying on that.
func TestUnknownActionIsNotRefused(t *testing.T) {
	forceV1Gate(t)
	if err := refuseIfDeferred("SomeService.MethodThatDoesNotExist"); err != nil {
		t.Errorf("an unclassified action must not refuse through the gate: %v", err)
	}
}

// Existing tests of deferred routes call the service methods. Those tests
// must still reach the implementation, or lifting the gate later has no
// evidence that the routes still work (ADR 0052 decision 4).
func TestDeferredRoutesRemainCallableInTheTestBinary(t *testing.T) {
	if err := refuseIfDeferred("BenchmarkService.StartBenchmark"); err != nil {
		t.Fatalf("deferred routes must stay callable in tests: %v", err)
	}
}

// Each deferred entry point except StartMirror must refuse through the
// gate. These subtests are the named cases the broken-guard controls
// remove one call site against.
func TestV1GateRefusesDeferredEntryPoints(t *testing.T) {
	forceV1Gate(t)
	ctx := t.Context()
	cases := []struct {
		action     string
		capability string
		call       func() error
	}{
		{"BenchmarkService.StartBenchmark", "Benchmark", func() error {
			_, err := (&BenchmarkService{}).StartBenchmark(ctx, BenchmarkRequestDTO{})
			return err
		}},
	}
	if len(cases) != len(v1DeferredActions)-1 {
		t.Fatalf("entry-point cases = %d, want %d deferred actions minus StartMirror", len(cases), len(v1DeferredActions)-1)
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			if _, ok := v1DeferredActions[tc.action]; !ok {
				t.Fatalf("%s is not in the deferred table", tc.action)
			}
			assertV1Refusal(t, tc.call(), tc.capability)
		})
	}
}

// StartMirror is classified deferred but must keep refusing under
// ADR 0020, with or without the v1 gate, so lifting the gate cannot
// enable Mirror.
func TestStartMirrorRefusesIndependentlyOfTheV1Gate(t *testing.T) {
	want := "concurrent changes cannot be protected"
	_, err := (&TransferService{}).StartMirror(t.Context(), MirrorRequestDTO{})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("StartMirror = %v, want ADR 0020 refusal", err)
	}
	if strings.Contains(err.Error(), "not available in this release") {
		t.Fatalf("StartMirror must not be routed through the v1 gate: %v", err)
	}

	forceV1Gate(t)
	_, err = (&TransferService{}).StartMirror(t.Context(), MirrorRequestDTO{})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("StartMirror with the v1 gate forced = %v, want ADR 0020 refusal", err)
	}
	if strings.Contains(err.Error(), "not available in this release") {
		t.Fatalf("StartMirror must not be routed through the v1 gate: %v", err)
	}
	assertV1Refusal(t, refuseIfDeferred("TransferService.StartMirror"), "Mirror")
}

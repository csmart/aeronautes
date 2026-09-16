package app

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// v1DeferredActions names every bridge action a v1 build refuses, keyed
// "Service.Method" to match the action inventory in
// internal/scenariocontract (ADR 0052).
//
// The operator narrowed v1 to browse, inspect, download and metadata
// reading and writing; copy, mirror, upload and bulk delete are deferred
// until each is proven. RETURNING A CAPABILITY IS REMOVING ITS NAMES FROM
// THIS TABLE — no call site changes, and the code behind it is untouched
// precisely so that it still works when that happens.
//
// Two entries are here on BEHAVIOUR rather than on name, and a
// classification by capability name would have shipped both:
// StartBenchmark writes real objects and bulk-deletes them, and RerunJob
// refuses every job type except copy. See TestEveryBridgeActionIsClassified
// for the check that keeps this table exhaustive against the inventory, so
// a new bridge method cannot land unclassified and silently reachable.
//
// A name here is NOT a claim that the route is unsafe to run, nor that
// removing it would be safe. It records that a v1 build does not offer it.
var v1DeferredActions = map[string]string{
	// copy
	"TransferService.PlanCopy":  "Copy",
	"TransferService.StartCopy": "Copy",
	"TransferService.RerunJob":  "Copy",
	// mirror — ADR 0020 refuses this independently and first; the gate
	// must never become the only thing refusing it.
	"TransferService.StartMirror": "Mirror",
	// upload
	"TransferService.PickUploadFiles": "Upload",
	"TransferService.PreflightUpload": "Upload",
	"TransferService.StartUpload":     "Upload",
	// bulk delete, and the read-only actions that exist only to serve it
	"DeleteService.PreviewDelete":              "Bulk delete",
	"DeleteService.StartDelete":                "Bulk delete",
	"DeleteService.CheckSegmentOwnership":      "Bulk delete",
	"DeleteService.DeleteManifestWithSegments": "Bulk delete",
	// destructive halves of the mutation service (ADR 0052 decision 7,
	// the lead author's reading pending the operator). Container creation
	// is kept: it is non-destructive and metadata management is thin
	// without it.
	"MutateService.CreateMarker":   "Marker and manifest editing",
	"MutateService.DeleteMarker":   "Marker and manifest editing",
	"MutateService.DeleteManifest": "Marker and manifest editing",
	// a benchmark writes real objects and bulk-deletes them
	"BenchmarkService.StartBenchmark": "Benchmark",
}

// v1GateForced makes refuseIfDeferred consult the table inside a test
// binary. The default test-binary path lifts the gate so existing tests
// of deferred routes still exercise them (ADR 0052 decision 4). A
// production binary never takes that path: testing.Testing is false.
var v1GateForced atomic.Bool

// refuseIfDeferred returns a refusal when action is deferred in a v1
// build. Every deferred service method except StartMirror calls it
// first, because the service entry point is the bridge boundary: the
// webview can reach a method whatever the interface chooses to display,
// so hiding an affordance protects nothing on its own (ADR 0052
// decision 1). StartMirror is classified here but keeps its ADR 0020
// refusal, so lifting this gate cannot enable Mirror.
func refuseIfDeferred(action string) error {
	if testing.Testing() && !v1GateForced.Load() {
		return nil
	}
	capability, deferred := v1DeferredActions[action]
	if !deferred {
		return nil
	}
	return fmt.Errorf("%s is not available in this release: it is deferred until its safety is proven, and this build does not offer it", capability)
}

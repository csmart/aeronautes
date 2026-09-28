package app

import (
	"fmt"
	"os"
	"sort"
	"strings"
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
	// the operator's decisions of 22 and 23 September 2026). Container
	// creation and marker creation are kept: both are non-destructive.
	"MutateService.DeleteMarker":   "Marker and manifest editing",
	"MutateService.DeleteManifest": "Marker and manifest editing",
	// a benchmark writes real objects and bulk-deletes them
	"BenchmarkService.StartBenchmark": "Benchmark",
}

// EnableEnvVar is the operator's out-of-band switch (ADR 0082). It is
// read from the process environment at startup and nowhere else: never a
// bridge action, never a stored setting, so nothing the webview can reach
// and nothing a page can persuade the application to store changes what
// this process offers.
const EnableEnvVar = "AERONAUTES_ENABLE"

// v1CapabilityTokens maps an operator-facing token to the capability name
// v1DeferredActions uses. A token names a WHOLE capability, so no
// half-capability can be assembled from outside (ADR 0082 decision 3).
//
// Mirror deliberately has no token. ADR 0020 refuses it independently of
// this gate and is checked first, and ADR 0052 decision 5 says that
// refusal must never become a scoping decision, so no value of the
// variable can admit it.
var v1CapabilityTokens = map[string]string{
	"upload":                 "Upload",
	"copy":                   "Copy",
	"bulk-delete":            "Bulk delete",
	"marker-manifest-delete": "Marker and manifest editing",
	"benchmark":              "Benchmark",
}

// enabledCapabilities is one reading of the switch: which deferred
// capabilities this process offers anyway, and which tokens the operator
// asked for that name no capability.
type enabledCapabilities struct {
	offered      map[string]bool
	names        []string
	unrecognised []string
}

// v1Enabled holds that reading. Admission reads this value, never the
// environment, so the switch cannot change under a running process.
var v1Enabled atomic.Pointer[enabledCapabilities]

// EnableCapabilities parses the switch's value and records it for every
// later admission. Tokens are trimmed and matched without regard to case;
// an empty token is skipped; a token that names no capability is ignored
// and returned so the operator is told rather than left wondering why the
// affordance never appeared (ADR 0082 decision 5).
func EnableCapabilities(spec string) (names, unrecognised []string) {
	reading := &enabledCapabilities{offered: map[string]bool{}}
	seenName, seenBad := map[string]bool{}, map[string]bool{}
	for _, raw := range strings.Split(spec, ",") {
		token := strings.TrimSpace(raw)
		if token == "" {
			continue
		}
		capability, known := v1CapabilityTokens[strings.ToLower(token)]
		if !known {
			if !seenBad[token] {
				seenBad[token] = true
				reading.unrecognised = append(reading.unrecognised, token)
			}
			continue
		}
		reading.offered[capability] = true
		if !seenName[capability] {
			seenName[capability] = true
			reading.names = append(reading.names, capability)
		}
	}
	sort.Strings(reading.names)
	v1Enabled.Store(reading)
	return reading.names, reading.unrecognised
}

// ReadEnabledCapabilities takes the switch from the process environment.
// The startup path calls it once; nothing else reads the variable.
func ReadEnabledCapabilities() (names, unrecognised []string) {
	return EnableCapabilities(os.Getenv(EnableEnvVar))
}

// EnabledCapabilitySnapshot reports the current reading for the Info
// document, so the operator can see what this process is running with.
func EnabledCapabilitySnapshot() (names, unrecognised []string) {
	reading := v1Enabled.Load()
	if reading == nil {
		return nil, nil
	}
	return append([]string(nil), reading.names...), append([]string(nil), reading.unrecognised...)
}

// StartupCapabilityLines is what the startup log says about one reading of
// the switch: that this process offers something unproven, and that a token
// the operator wrote names nothing (ADR 0082 decision 5). It returns the
// lines rather than logging them so the sentences can be asserted: a record
// that promises the operator will be told is worth no more than the wording
// nobody checked. Empty when the switch said nothing.
func StartupCapabilityLines(names, unrecognised []string) []string {
	var lines []string
	if len(names) > 0 {
		lines = append(lines, fmt.Sprintf("%s: offering unproven capabilities in this process: %s",
			EnableEnvVar, strings.Join(names, ", ")))
	}
	if len(unrecognised) > 0 {
		lines = append(lines, fmt.Sprintf("%s: ignored, not a capability: %s",
			EnableEnvVar, strings.Join(unrecognised, ", ")))
	}
	return lines
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
	// The operator may offer a deferred capability deliberately, out of
	// band (ADR 0082). The reading is taken at startup; this admission
	// never consults the environment.
	if reading := v1Enabled.Load(); reading != nil && reading.offered[capability] {
		return nil
	}
	return fmt.Errorf("%s is not available in this release: it is deferred until its safety is proven, and this build does not offer it", capability)
}

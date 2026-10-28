package app

import (
	"strings"
	"testing"

	"github.com/csmart/aeronautes/internal/evidence"
	"github.com/csmart/aeronautes/internal/jobs"
)

// deferredEntryPoint is one bridge method a v1 build refuses, called the
// way the webview would call it. The arguments are deliberately the ones
// a hostile caller could supply: the gate is checked before any of them
// is examined, so a call that gets past it is a real admission.
type deferredEntryPoint struct {
	action     string
	capability string
	call       func(t *testing.T, h *harness) error
}

func deferredEntryPoints(t *testing.T, h *harness) []deferredEntryPoint {
	t.Helper()
	deleteSvc := NewDeleteService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, evidence.NewStore(h.db))
	bench := NewBenchmarkService(h.transfer.connections, h.transfer.jobs, h.transfer.runner, h.db)
	entries := []deferredEntryPoint{
		{"TransferService.PlanCopy", "Copy", func(t *testing.T, h *harness) error {
			_, err := h.transfer.PlanCopy(t.Context(), CopyRequestDTO{SourceBookmarkID: h.bookmarkID(), SourceContainer: "fixtures", DestBookmarkID: h.bookmarkID(), DestContainer: "fixtures"})
			return err
		}},
		{"TransferService.StartCopy", "Copy", func(t *testing.T, h *harness) error {
			_, err := h.transfer.StartCopy(t.Context(), CopyRequestDTO{SourceBookmarkID: h.bookmarkID(), SourceContainer: "fixtures", DestBookmarkID: h.bookmarkID(), DestContainer: "fixtures"})
			return err
		}},
		{"TransferService.RerunJob", "Copy", func(t *testing.T, h *harness) error {
			_, err := h.transfer.RerunJob(t.Context(), "any-job")
			return err
		}},
		{"DeleteService.PreviewDelete", "Bulk delete", func(t *testing.T, h *harness) error {
			_, err := deleteSvc.PreviewDelete(t.Context(), DeleteRequestDTO{BookmarkID: h.bookmarkID(), Container: "fixtures"})
			return err
		}},
		{"DeleteService.StartDelete", "Bulk delete", func(t *testing.T, h *harness) error {
			_, err := deleteSvc.StartDelete(t.Context(), DeleteRequestDTO{BookmarkID: h.bookmarkID(), Container: "fixtures"})
			return err
		}},
		{"DeleteService.StartSelectedDelete", "Object deletion", func(t *testing.T, h *harness) error {
			_, err := deleteSvc.StartSelectedDelete(t.Context(), SelectedDeleteRequestDTO{Scope: h.portrait(), Container: "fixtures",
				Members: []ListingRowDTO{{Kind: "object", Name: "empty-object"}}, Confirm: "1"})
			return err
		}},
		{"DeleteService.CheckSegmentOwnership", "Bulk delete", func(t *testing.T, h *harness) error {
			_, err := deleteSvc.CheckSegmentOwnership(t.Context(), SegmentsDeleteRequestDTO{BookmarkID: h.bookmarkID(), Container: "fixtures", Name: "external-dlo"})
			return err
		}},
		{"DeleteService.DeleteManifestWithSegments", "Bulk delete", func(t *testing.T, h *harness) error {
			// A deliberate confirmation mismatch: the gate is checked first, so
			// a deferred build still refuses at the gate, while a switched-on
			// build refuses at the typed confirmation before launching a job,
			// so this gate test leaves no background deletion running.
			_, err := deleteSvc.DeleteManifestWithSegments(t.Context(), SegmentsDeleteRequestDTO{BookmarkID: h.bookmarkID(), Container: "fixtures", Name: "external-dlo", Force: true, Confirm: "mismatch"})
			return err
		}},
		{"MutateService.DeleteMarker", "Marker and manifest deletion", func(t *testing.T, h *harness) error {
			_, err := h.mutate.DeleteMarker(t.Context(), h.portrait(), "fixtures", "implied/", "any")
			return err
		}},
		{"MutateService.DeleteManifest", "Marker and manifest deletion", func(t *testing.T, h *harness) error {
			// The gate refuses at the entry point, before any identity is
			// read, so the fingerprint's value cannot affect this case.
			_, err := h.mutate.DeleteManifest(t.Context(), h.portrait(), "fixtures", "external-dlo", "any")
			return err
		}},
		{"MutateService.DeleteObject", "Object deletion", func(t *testing.T, h *harness) error {
			// Refused before the scope is checked and before any intent is
			// recorded, so the portrait and the fingerprint cannot matter.
			_, err := h.mutate.DeleteObject(t.Context(), h.portrait(), "fixtures", "normal/hello.txt", "any")
			return err
		}},
		{"BenchmarkService.StartBenchmark", "Benchmark", func(t *testing.T, h *harness) error {
			_, err := bench.StartBenchmark(t.Context(), BenchmarkRequestDTO{BookmarkID: h.bookmarkID(), Confirm: "x"})
			return err
		}},
	}
	// Every deferred action except StartMirror, which refuses on ADR 0020's
	// terms and has its own case below. Upload's three actions are absent
	// because the operator returned that capability on 16 October 2026 and
	// the table no longer defers it. The count is asserted against the table
	// so a future return cannot quietly shrink this list on its own, and so
	// a newly deferred action cannot be left unexercised here.
	if len(entries) != len(v1DeferredActions)-1 {
		t.Fatalf("entry points = %d, want %d deferred actions minus StartMirror", len(entries), len(v1DeferredActions)-1)
	}
	return entries
}

// collateral is what a refused call must not have changed: nothing sent
// to the store, no job row, no audit row.
type collateral struct {
	requests, jobRows, auditRows int
}

func takeCollateral(t *testing.T, h *harness) collateral {
	t.Helper()
	list, err := h.transfer.jobs.List(t.Context(), jobs.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	events, err := h.auditSvc.RecentFor(t.Context(), h.bookmarkID(), 200)
	if err != nil {
		t.Fatal(err)
	}
	return collateral{requests: h.mock.Requests(), jobRows: len(list), auditRows: len(events)}
}

// ADR 0082, tier 1: with nothing switched on, every deferred bridge
// method refuses at its own entry point and nothing reaches the store,
// the job store or the audit trail. The gate is forced, because an
// ordinary test binary lifts it and would prove nothing about a shipped
// process.
func TestDeferredEntryPointsRefuseAndTouchNothing(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	forceV1Gate(t)
	restoreSwitch(t)
	EnableCapabilities("")

	for _, entry := range deferredEntryPoints(t, h) {
		t.Run(entry.action, func(t *testing.T) {
			before := takeCollateral(t, h)
			err := entry.call(t, h)
			assertV1Refusal(t, err, entry.capability)
			if after := takeCollateral(t, h); after != before {
				t.Errorf("a refused %s reached something: %+v -> %+v", entry.action, before, after)
			}
		})
	}
}

// One capability switched on admits its own entry points and no others,
// and the refused ones still touch nothing.
func TestSwitchedOnCapabilityAdmitsOnlyItsOwnEntryPoints(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	forceV1Gate(t)
	restoreSwitch(t)
	if names, _ := EnableCapabilities("bulk-delete"); strings.Join(names, "|") != "Bulk delete" {
		t.Fatalf("switch = %v", names)
	}

	for _, entry := range deferredEntryPoints(t, h) {
		t.Run(entry.action, func(t *testing.T) {
			before := takeCollateral(t, h)
			err := entry.call(t, h)
			if entry.capability != "Bulk delete" {
				assertV1Refusal(t, err, entry.capability)
				if after := takeCollateral(t, h); after != before {
					t.Errorf("a refused %s reached something: %+v -> %+v", entry.action, before, after)
				}
				return
			}
			// An admitted entry point runs its own route and may fail on
			// its own terms; what it must not do is refuse for the gate.
			if err != nil && strings.Contains(err.Error(), "not available in this release") {
				t.Errorf("%s still refused although bulk delete is switched on: %v", entry.action, err)
			}
		})
	}
}

// Mirror is refused by ADR 0020 at its own entry point, whatever the
// switch says, and that refusal is not the capability gate's.
func TestMirrorRefusesAtItsEntryPointWhateverTheSwitchSays(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	forceV1Gate(t)
	restoreSwitch(t)

	for _, spec := range []string{"", "mirror", "Mirror", "copy,mirror", "copy", "copy, bulk-delete, benchmark, marker-manifest-delete"} {
		EnableCapabilities(spec)
		before := takeCollateral(t, h)
		_, err := h.transfer.StartMirror(t.Context(), MirrorRequestDTO{
			SourceBookmarkID: h.bookmarkID(), SourceContainer: "fixtures",
			DestBookmarkID: h.bookmarkID(), DestContainer: "fixtures",
		})
		if err == nil {
			t.Fatalf("StartMirror was admitted with %q", spec)
		}
		if strings.Contains(err.Error(), "not available in this release") {
			t.Errorf("StartMirror refused for the capability gate with %q, not for ADR 0020: %v", spec, err)
		}
		if after := takeCollateral(t, h); after != before {
			t.Errorf("a refused StartMirror reached something with %q: %+v -> %+v", spec, before, after)
		}
	}
}

// A token that names no capability admits nothing. Without this case an
// implementation that treated an unrecognised token as enabling
// everything would pass: the reported names would still be empty while
// every entry point had been opened.
func TestUnrecognisedTokenAdmitsNothing(t *testing.T) {
	h := newHarness(t)
	h.connect(t)
	forceV1Gate(t)
	restoreSwitch(t)
	names, unrecognised := EnableCapabilities("uplaod, not-a-capability")
	if len(names) != 0 || len(unrecognised) != 2 {
		t.Fatalf("switch = %v %v", names, unrecognised)
	}
	for _, entry := range deferredEntryPoints(t, h) {
		t.Run(entry.action, func(t *testing.T) {
			before := takeCollateral(t, h)
			assertV1Refusal(t, entry.call(t, h), entry.capability)
			if after := takeCollateral(t, h); after != before {
				t.Errorf("a refused %s reached something: %+v -> %+v", entry.action, before, after)
			}
		})
	}
}

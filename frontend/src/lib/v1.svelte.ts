// Affordances a v1 build offers (ADR 0052).
// Returning a deferred capability is flipping the matching flag here
// and removing its names from the Go deferred table. Download, marker
// creation, upload, object deletion, copy (with its job re-run), marker
// and manifest deletion, and bulk delete are kept (the operator returned
// marker creation on 23 September 2026, upload on 16 October 2026, object
// deletion on 29 October 2026, copy on 31 October 2026, marker and
// manifest deletion on 1 November 2026, and bulk delete on 1 November
// 2026); only benchmark is not.
// LocalTransferDialog serves both of its modes again.

/** The capability names the backend reports, mapped onto the affordances
 * each one covers (ADR 0082). A capability turns on all of its
 * affordances or none, so no half-capability can be assembled here. Job
 * re-run was classified with Copy and returned alongside it. */
const affordancesOf: Record<string, (keyof typeof v1Offers)[]> = {
  "Benchmark": ["benchmark"],
};

/** Offers the capabilities this process reports as enabled, and only
 * those: a name the backend does not report leaves its affordances as
 * the release ships them. Mirror is not here and has no affordance. */
export function applyEnabledCapabilities(names: readonly string[] | undefined) {
  for (const name of names ?? []) {
    for (const affordance of affordancesOf[name] ?? []) {
      v1Offers[affordance] = true;
    }
  }
}

// Reactive, because the process reports its enabled capabilities after
// the first render and the affordances must follow (ADR 0082).
export const v1Offers = $state({
  copy: true,
  upload: true,
  bulkDelete: true,
  markerCreate: true,
  markerDelete: true,
  manifestDelete: true,
  objectDelete: true,
  benchmark: false,
  // Mirror refuses every start (ADR 0020); its comparison preview alone read
  // as broken, so by the operator's decision of 11 November 2026 the copy
  // dialog offers no Mirror tab until Mirror can run.
  mirrorPreview: false,
  rerun: true,
  download: true,
});

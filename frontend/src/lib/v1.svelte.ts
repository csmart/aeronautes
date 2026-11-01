// Affordances a v1 build offers (ADR 0052).
// Returning a deferred capability is flipping the matching flag here
// and removing its names from the Go deferred table. Download, marker
// creation, upload, object deletion and copy (with its job re-run) are
// kept (the operator returned marker creation on 23 September 2026, upload
// on 16 October 2026, object deletion on 29 October 2026, copy on
// 31 October 2026, and marker and manifest deletion on 1 November 2026);
// bulk delete and benchmark are not.
// LocalTransferDialog serves both of its modes again.

/** The capability names the backend reports, mapped onto the affordances
 * each one covers (ADR 0082). A capability turns on all of its
 * affordances or none, so no half-capability can be assembled here. Job
 * re-run was classified with Copy and returned alongside it. */
const affordancesOf: Record<string, (keyof typeof v1Offers)[]> = {
  "Bulk delete": ["bulkDelete"],
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
  bulkDelete: false,
  markerCreate: true,
  markerDelete: true,
  manifestDelete: true,
  objectDelete: true,
  benchmark: false,
  rerun: true,
  download: true,
});

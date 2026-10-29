// Affordances a v1 build offers (ADR 0052).
// Returning a deferred capability is flipping the matching flag here
// and removing its names from the Go deferred table. Download, marker
// creation, upload and object deletion are kept (the operator returned
// marker creation on 23 September 2026, upload on 16 October 2026 and
// object deletion on 29 October 2026); copy, bulk delete, marker deletion,
// manifest deletion, benchmark and re-run are not.
// LocalTransferDialog serves both of its modes again.

/** The capability names the backend reports, mapped onto the affordances
 * each one covers (ADR 0082). A capability turns on all of its
 * affordances or none, so no half-capability can be assembled here. Job
 * re-run is classified with Copy in the backend's table and follows it. */
const affordancesOf: Record<string, (keyof typeof v1Offers)[]> = {
  "Copy": ["copy", "rerun"],
  "Bulk delete": ["bulkDelete"],
  "Marker and manifest deletion": ["markerDelete", "manifestDelete"],
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
  copy: false,
  upload: true,
  bulkDelete: false,
  markerCreate: true,
  markerDelete: false,
  manifestDelete: false,
  objectDelete: true,
  benchmark: false,
  rerun: false,
  download: true,
});

// Affordances a v1 build offers (ADR 0052).
// Returning a deferred capability is flipping the matching flag here
// and removing its names from the Go deferred table. Download and marker
// creation are kept (the operator returned marker creation on
// 23 September 2026); copy, upload, bulk delete, marker deletion,
// manifest deletion, benchmark and re-run are not. LocalTransferDialog
// still serves both modes: only the upload opener is withdrawn.

/** The capability names the backend reports, mapped onto the affordances
 * each one covers (ADR 0082). A capability turns on all of its
 * affordances or none, so no half-capability can be assembled here. Job
 * re-run is classified with Copy in the backend's table and follows it. */
const affordancesOf: Record<string, (keyof typeof v1Offers)[]> = {
  "Upload": ["upload"],
  "Copy": ["copy", "rerun"],
  "Bulk delete": ["bulkDelete"],
  "Marker and manifest editing": ["markerDelete", "manifestDelete"],
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
  upload: false,
  bulkDelete: false,
  markerCreate: true,
  markerDelete: false,
  manifestDelete: false,
  benchmark: false,
  rerun: false,
  download: true,
});

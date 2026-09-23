// Affordances a v1 build offers (ADR 0052).
// Returning a deferred capability is flipping the matching flag here
// and removing its names from the Go deferred table. Download and marker
// creation are kept (the operator returned marker creation on
// 23 September 2026); copy, upload, bulk delete, marker deletion,
// manifest deletion, benchmark and re-run are not. LocalTransferDialog
// still serves both modes: only the upload opener is withdrawn.

export const v1Offers = {
  copy: false,
  upload: false,
  bulkDelete: false,
  markerCreate: true,
  markerDelete: false,
  manifestDelete: false,
  benchmark: false,
  rerun: false,
  download: true,
};

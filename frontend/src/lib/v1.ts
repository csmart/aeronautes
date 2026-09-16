// Affordances a v1 build offers (ADR 0052).
// Returning a deferred capability is flipping the matching flag here
// and removing its names from the Go deferred table. Download is kept;
// copy, upload, bulk delete, marker and manifest editing, benchmark and
// re-run are not. LocalTransferDialog still serves both modes: only the
// upload opener is withdrawn.

export const v1Offers = {
  copy: false,
  upload: false,
  bulkDelete: false,
  markerEdit: false,
  manifestDelete: false,
  benchmark: false,
  rerun: false,
  download: true,
};

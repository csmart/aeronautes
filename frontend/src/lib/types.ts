// Listing and inspection shapes for the browser UI. These mirror the Go
// types in internal/swiftcontrol (ObjectEntry / PrefixEntry, §14) so the
// live wiring commit swaps fixture data for service calls without
// reshaping the components.

/** One container row, §12/§52: classification drives the mode views. */
export interface ContainerEntry {
  name: string;
  objectCount: number;
  bytes: number;
  policy: string;
  /**
   * Segment-store classification (§12.2): confirmed stores are hidden in
   * Logical mode, suspected ones grouped separately; normal containers
   * always show.
   */
  segmentRole: "normal" | "suspected" | "confirmed";
}

/** A real object row (§14.2). Listing rows are cheap classification only. */
export interface ObjectEntry {
  kind: "object";
  name: string;
  bytes: number;
  etag: string;
  lastModified: string;
  contentType: string;
  /** DLO/SLO hint from the listing or fixture; HEAD is authoritative. */
  largeObject?: "dlo" | "slo";
}

/** A virtual-folder row from a delimiter view (§14.1). Whether a marker
 * object backs it is not knowable from a listing — the server folds any
 * boundary object into this row; PrefixDetail answers on selection. */
export interface PrefixEntry {
  kind: "prefix";
  /** Includes the trailing delimiter. */
  prefix: string;
}

export type ListingEntry = ObjectEntry | PrefixEntry;

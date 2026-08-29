// Client-side delimiter view over fixture objects, mirroring the Swift
// semantics the Go layer implements (§14): name-sorted rows, subdirectory
// grouping at "/", a zero-byte application/directory object whose name ends
// in the delimiter merges with its subdir row into one marker-backed
// prefix, and an object that merely *looks* folder-ish stays an object.
// The live wiring replaces calls to this with BrowseService pages; the
// function exists so the static UI browses with correct semantics, not a
// fake filesystem.

import type { ListingEntry, ObjectEntry } from "./types";

/** A raw fixture object: what a deep (no-delimiter) listing would show. */
export interface FixtureObject {
  name: string;
  bytes: number;
  etag: string;
  lastModified: string;
  contentType: string;
  largeObject?: "dlo" | "slo";
}

function isFolderMarker(o: FixtureObject): boolean {
  return (
    o.name.endsWith("/") &&
    o.bytes === 0 &&
    o.contentType === "application/directory"
  );
}

function toObjectEntry(o: FixtureObject): ObjectEntry {
  return {
    kind: "object",
    name: o.name,
    bytes: o.bytes,
    etag: o.etag,
    lastModified: o.lastModified,
    contentType: o.contentType,
    largeObject: o.largeObject,
  };
}

/**
 * One delimiter page of `objects` under `prefix` ("" for the container
 * root; otherwise must end with "/"). Rows come back name-sorted the way
 * the server would return them.
 */
export function delimiterView(
  objects: FixtureObject[],
  prefix: string,
): ListingEntry[] {
  const sorted = [...objects]
    .filter((o) => o.name.startsWith(prefix))
    .sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));

  const out: ListingEntry[] = [];
  const prefixIndex = new Map<string, number>();

  for (const o of sorted) {
    const rest = o.name.slice(prefix.length);
    const slash = rest.indexOf("/");

    if (slash === -1) {
      out.push(toObjectEntry(o));
      continue;
    }

    const trailing = slash === rest.length - 1;
    const marker = trailing && isFolderMarker(o);

    if (trailing && !marker) {
      // Folder-ish name but a real object (§14.1): stays an object row;
      // any children independently imply the prefix row.
      out.push(toObjectEntry(o));
      continue;
    }

    const childPrefix = prefix + rest.slice(0, slash + 1);
    const existing = prefixIndex.get(childPrefix);
    if (existing !== undefined) {
      if (marker) {
        const entry = out[existing];
        if (entry.kind === "prefix") {
          entry.markerBacked = true;
        }
      }
      continue;
    }

    prefixIndex.set(childPrefix, out.length);
    out.push({ kind: "prefix", prefix: childPrefix, markerBacked: marker });
  }

  return out;
}

/** Deep view: every real object under the prefix, markers included (§14). */
export function deepView(
  objects: FixtureObject[],
  prefix: string,
): ObjectEntry[] {
  return [...objects]
    .filter((o) => o.name.startsWith(prefix))
    .sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0))
    .map(toObjectEntry);
}

/** Breadcrumb segments for a prefix path: "a/b/" → ["a/", "a/b/"]. */
export function breadcrumbs(prefix: string): { label: string; prefix: string }[] {
  const crumbs: { label: string; prefix: string }[] = [];
  let at = 0;
  while (at < prefix.length) {
    const slash = prefix.indexOf("/", at);
    if (slash === -1) {
      break;
    }
    crumbs.push({ label: prefix.slice(at, slash), prefix: prefix.slice(0, slash + 1) });
    at = slash + 1;
  }
  return crumbs;
}

/** The parent prefix: "a/b/" → "a/", "a/" → "", "" → "". */
export function parentPrefix(prefix: string): string {
  if (prefix === "") {
    return "";
  }
  const trimmed = prefix.endsWith("/") ? prefix.slice(0, -1) : prefix;
  const slash = trimmed.lastIndexOf("/");
  return slash === -1 ? "" : trimmed.slice(0, slash + 1);
}

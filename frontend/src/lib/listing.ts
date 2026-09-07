// Client-side delimiter view over fixture objects, mirroring real Swift's
// semantics as the Go layer implements them (§14, verified via §57.3):
// name-sorted rows; every name below a child prefix — boundary objects,
// markers, folder-ish real objects included — folds into one subdir row;
// the object at a slash-terminated folder's own name is not a child row. Used by
// the test fake so navigation logic is exercised with correct semantics.

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
 * One delimiter page under a literal `prefix` ("" for the container root).
 * Rows come back name-sorted the way the server would return them.
 */
export function delimiterView(
  objects: FixtureObject[],
  prefix: string,
): ListingEntry[] {
  const sorted = [...objects]
    .filter((o) => o.name.startsWith(prefix))
    .sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));

  const out: ListingEntry[] = [];
  const emitted = new Set<string>();

  for (const o of sorted) {
    if (prefix.endsWith("/") && o.name === prefix) {
      // The folder's own object belongs to PrefixDetail, not its children.
      continue;
    }
    const rest = o.name.slice(prefix.length);
    const slash = rest.indexOf("/");
    if (slash === -1) {
      out.push(toObjectEntry(o));
      continue;
    }
    const childPrefix = prefix + rest.slice(0, slash + 1);
    if (!emitted.has(childPrefix)) {
      emitted.add(childPrefix);
      out.push({ kind: "prefix", prefix: childPrefix });
    }
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

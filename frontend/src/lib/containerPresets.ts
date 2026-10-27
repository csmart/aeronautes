// Common container settings as tick boxes (§19.1 presets, ADR 0090). A
// tick is read from the value the operator can also edit by hand, and
// ticking rewrites only the entries that tick stands for, so the
// structured and the raw view of one value never disagree and nothing
// else in the value is reinterpreted (§19.2).

import type { MetaRow } from "./metadataDiff";

function aclEntries(acl: string): string[] {
  return acl
    .split(",")
    .map((entry) => entry.trim())
    .filter((entry) => entry !== "");
}

/** World read is exactly `.r:*` in any comma position after trimming, the
 * rule the acknowledgement gate and the badges apply (ADR 0071); a
 * referrer grant such as `.r:*.example.com` is not world read. */
export function grantsWorldRead(readAcl: string): boolean {
  return aclEntries(readAcl).includes(".r:*");
}

/** Public listing is `.rlistings` beside world read; alone it grants nothing. */
export function grantsWorldListing(readAcl: string): boolean {
  const entries = aclEntries(readAcl);
  return entries.includes(".r:*") && entries.includes(".rlistings");
}

/** Add or remove the world-read entry. Turning it off also drops
 * `.rlistings`, which grants nothing without it. */
export function withWorldRead(readAcl: string, on: boolean): string {
  const entries = aclEntries(readAcl);
  if (on) {
    return entries.includes(".r:*") ? readAcl : [...entries, ".r:*"].join(",");
  }
  return entries.filter((entry) => entry !== ".r:*" && entry !== ".rlistings").join(",");
}

/** Add or remove the listing entry; the caller offers it only beside world read. */
export function withWorldListing(readAcl: string, on: boolean): string {
  const entries = aclEntries(readAcl);
  if (on) {
    return entries.includes(".rlistings") ? readAcl : [...entries, ".rlistings"].join(",");
  }
  return entries.filter((entry) => entry !== ".rlistings").join(",");
}

/** The static web keys are the `Web-*` container metadata family, matched
 * the way the Go model classifies them: by folded prefix. */
export function isStaticWebKey(key: string): boolean {
  return key.toLowerCase().startsWith("web-");
}

export function hasStaticWeb(rows: MetaRow[]): boolean {
  return rows.some((row) => isStaticWebKey(row.key));
}

/** What a website needs to start serving: the object each directory
 * serves as its page, and listings for directories without one. */
export const staticWebDefaults: readonly MetaRow[] = [
  { key: "Web-Index", value: "index.html" },
  { key: "Web-Listings", value: "true" },
];

/** Ticking adds the defaults unless some `Web-*` key is already there;
 * unticking removes every `Web-*` key and nothing else. */
export function withStaticWeb(rows: MetaRow[], on: boolean): MetaRow[] {
  if (on) {
    return hasStaticWeb(rows) ? rows : [...rows, ...staticWebDefaults.map((row) => ({ ...row }))];
  }
  return rows.filter((row) => !isStaticWebKey(row.key));
}

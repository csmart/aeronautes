// How the bookmark rail arranges many accounts: grouped by the cloud they
// point at, sorted for the eye, filtered on demand, and described on hover.
// The grouping is presentation only. Two bookmarks on one host are two
// remotes with two identities for every safety decision (DESIGN §28.5);
// nothing here feeds a mutation.

import type { BookmarkDTO } from "./api";

export interface BookmarkGroup {
  /** The endpoint the group stands for; also its heading. */
  endpoint: string;
  bookmarks: BookmarkDTO[];
}

/** The cloud a bookmark points at: the auth URL's host, or the raw auth
 * URL when it does not parse, so an odd value still lands in a group. */
export function endpointOf(bookmark: BookmarkDTO): string {
  try {
    return new URL(bookmark.authUrl).host || bookmark.authUrl;
  } catch {
    return bookmark.authUrl;
  }
}

function compareNames(a: string, b: string): number {
  return a.localeCompare(b, undefined, { sensitivity: "base", numeric: true });
}

/** Groups by endpoint, endpoints and the bookmarks within each sorted
 * alphabetically without regard to case; the id breaks a tie between two
 * bookmarks of one name so the order is stable. */
export function groupBookmarks(bookmarks: BookmarkDTO[]): BookmarkGroup[] {
  const members = new Map<string, BookmarkDTO[]>();
  for (const bookmark of bookmarks) {
    const endpoint = endpointOf(bookmark);
    members.set(endpoint, [...(members.get(endpoint) ?? []), bookmark]);
  }
  return [...members.entries()]
    .sort(([a], [b]) => compareNames(a, b))
    .map(([endpoint, group]) => ({
      endpoint,
      bookmarks: [...group].sort((a, b) => compareNames(a.displayName, b.displayName) || a.id.localeCompare(b.id)),
    }));
}

/** A filter matches the display name, the username (the TempAuth
 * `account:user`) or the endpoint, as a case-insensitive substring. */
export function matchesBookmark(bookmark: BookmarkDTO, filter: string): boolean {
  const needle = filter.trim().toLowerCase();
  if (needle === "") {
    return true;
  }
  return [bookmark.displayName, bookmark.username, endpointOf(bookmark)].some((field) =>
    field.toLowerCase().includes(needle),
  );
}

/** The hover text: the name the operator gave, the account it signs in
 * as and where, and the persistent TLS warning when verification is off
 * (K-SEC-005). */
export function hoverText(bookmark: BookmarkDTO): string {
  const lines = [bookmark.displayName, `${bookmark.username} at ${endpointOf(bookmark)}`];
  if (bookmark.tlsMode === "insecure") {
    lines.push("TLS verification DISABLED");
  }
  return lines.join("\n");
}

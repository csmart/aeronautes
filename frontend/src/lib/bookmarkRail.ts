// How the bookmark rail arranges many accounts: sorted for the eye,
// filtered on demand, and described on hover. Two bookmarks on one host are
// two remotes with two identities for every safety decision (DESIGN §28.5);
// nothing here feeds a mutation.

import type { BookmarkDTO } from "./api";

/** The cloud a bookmark points at: the auth URL's host, or the raw auth
 * URL when it does not parse. The filter and the hover text name it. */
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

/** Sorts by display name without regard to case, numbered names the way
 * people read them; the id breaks a tie between two bookmarks of one name
 * so the order is stable. */
export function sortBookmarks(bookmarks: BookmarkDTO[]): BookmarkDTO[] {
  return [...bookmarks].sort((a, b) => compareNames(a.displayName, b.displayName) || a.id.localeCompare(b.id));
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
 * (K-SEC-005). A Keystone user named by ID has no username, so the ID
 * stands in (ADR 0113). */
export function hoverText(bookmark: BookmarkDTO): string {
  const lines = [bookmark.displayName, `${bookmark.username || bookmark.userId || ""} at ${endpointOf(bookmark)}`];
  if (bookmark.tlsMode === "insecure") {
    lines.push("TLS verification DISABLED");
  }
  return lines.join("\n");
}

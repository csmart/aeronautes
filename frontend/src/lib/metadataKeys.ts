// Suggested metadata keys (ADR 0125 decisions 2 and 3): the keys the store
// gives a meaning to for each kind of item, then the keys the operator has
// used on items of the same kind this session has read.

export type MetadataKind = "object" | "container" | "account";

export interface KeySuggestion {
  key: string;
  note: string;
}

const known: Record<MetadataKind, KeySuggestion[]> = {
  container: [
    { key: "Web-Index", note: "file served for a folder, such as index.html (static website)" },
    { key: "Web-Error", note: "suffix of the error pages, such as error.html for 404error.html (static website)" },
    { key: "Web-Listings", note: "true lists a folder that has no index (static website)" },
    { key: "Web-Listings-CSS", note: "stylesheet for those listings (static website)" },
    { key: "Web-Listings-Label", note: "name shown for the container in those listings (static website)" },
    { key: "Web-Directory-Type", note: "content type of objects treated as folders (static website)" },
    { key: "Access-Control-Allow-Origin", note: "origins allowed to make cross-origin requests (CORS)" },
    { key: "Access-Control-Max-Age", note: "seconds a browser may keep a preflight answer (CORS)" },
    { key: "Access-Control-Expose-Headers", note: "response headers a browser may read (CORS)" },
  ],
  object: [{ key: "Mtime", note: "modification time as rclone records it, in seconds since 1970" }],
  account: [],
};

// Keys with editors of their own, never offered here.
const ownEditor = new Set(["temp-url-key", "temp-url-key-2", "quota-bytes", "quota-count"]);

/** Every suggestion for a kind of item, known keys first, without the keys
 * that have editors of their own. */
export function keySuggestions(kind: MetadataKind, seen: string[]): KeySuggestion[] {
  const out = known[kind].filter((s) => !ownEditor.has(s.key.toLowerCase()));
  const listed = new Set(out.map((s) => s.key.toLowerCase()));
  for (const key of seen) {
    const folded = key.toLowerCase();
    if (listed.has(folded) || ownEditor.has(folded)) continue;
    listed.add(folded);
    out.push({ key, note: `used on another ${kind} this session` });
  }
  return out;
}

/** The suggestions holding what was typed anywhere in the key, ignoring
 * case, less the keys the item already has; all of them when nothing is
 * typed. Metadata names are case-insensitive, so the comparison is too. */
export function matchingKeys(all: KeySuggestion[], typed: string, present: string[]): KeySuggestion[] {
  const wanted = typed.trim().toLowerCase();
  const have = new Set(present.map((key) => key.trim().toLowerCase()));
  return all.filter((s) => !have.has(s.key.toLowerCase()) && s.key.toLowerCase().includes(wanted));
}

/** The key split around the first match of what was typed, for marking it. */
export function splitMatch(key: string, typed: string): [string, string, string] {
  const wanted = typed.trim().toLowerCase();
  const at = wanted === "" ? -1 : key.toLowerCase().indexOf(wanted);
  if (at < 0) return [key, "", ""];
  return [key.slice(0, at), key.slice(at, at + wanted.length), key.slice(at + wanted.length)];
}

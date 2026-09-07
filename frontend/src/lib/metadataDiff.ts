// Semantic diff for metadata working copies (§17): what the edit adds,
// changes, and removes relative to the base the working copy was taken
// from. The save always sends the complete intended set; the diff is how
// the operator sees consequences before committing — especially removals,
// which Swift's replace-all POST makes dangerously easy.

export interface MetaDiff {
  added: string[];
  changed: string[];
  removed: string[];
}

function ownEntries(meta: { [key: string]: string | undefined }): [string, string][] {
  return Object.entries(meta).filter((entry): entry is [string, string] => typeof entry[1] === "string");
}

export function diffMeta(
  base: { [key: string]: string | undefined },
  working: { [key: string]: string | undefined },
): MetaDiff {
  const diff: MetaDiff = { added: [], changed: [], removed: [] };
  const baseEntries = ownEntries(base);
  const workingEntries = ownEntries(working);
  const original = new Map(baseEntries.map(([key, value]) => [key.toLowerCase(), value]));
  const intended = new Set(workingEntries.map(([key]) => key.toLowerCase()));
  for (const [key, value] of workingEntries) {
    const folded = key.toLowerCase();
    if (!original.has(folded)) {
      diff.added.push(key);
    } else if (original.get(folded) !== value) {
      diff.changed.push(key);
    }
  }
  for (const [key] of baseEntries) {
    if (!intended.has(key.toLowerCase())) {
      diff.removed.push(key);
    }
  }
  diff.added.sort();
  diff.changed.sort();
  diff.removed.sort();
  return diff;
}

export function isDiffEmpty(diff: MetaDiff): boolean {
  return diff.added.length === 0 && diff.changed.length === 0 && diff.removed.length === 0;
}

/** One editable row of the working copy. */
export interface MetaRow {
  key: string;
  value: string;
}

export function rowsFromMeta(meta: { [key: string]: string | undefined }): MetaRow[] {
  return ownEntries(meta)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([key, value]) => ({ key, value }));
}

/**
 * Unsupported keys must reach backend admission without silently changing
 * the working copy. Only an unused row can be ignored; case variants cannot
 * be represented as distinct Swift metadata keys.
 */
export function metaFromRows(rows: MetaRow[]): { meta: Record<string, string>; duplicate: string | null } {
  const meta: Record<string, string> = Object.create(null);
  const seen = new Set<string>();
  for (const row of rows) {
    const key = row.key;
    if (key === "" && row.value === "") {
      continue;
    }
    const folded = key.toLowerCase();
    if (seen.has(folded)) {
      return { meta: {}, duplicate: key };
    }
    seen.add(folded);
    meta[key] = row.value;
  }
  return { meta, duplicate: null };
}

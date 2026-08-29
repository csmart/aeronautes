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

export function diffMeta(
  base: { [key: string]: string | undefined },
  working: { [key: string]: string | undefined },
): MetaDiff {
  const diff: MetaDiff = { added: [], changed: [], removed: [] };
  for (const [key, value] of Object.entries(working)) {
    if (value === undefined) {
      continue;
    }
    if (!(key in base)) {
      diff.added.push(key);
    } else if (base[key] !== value) {
      diff.changed.push(key);
    }
  }
  for (const key of Object.keys(base)) {
    if (!(key in working) || working[key] === undefined) {
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
  return Object.entries(meta)
    .filter((kv): kv is [string, string] => kv[1] !== undefined)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([key, value]) => ({ key, value }));
}

/**
 * Collapse editor rows back into the intended metadata set. Blank keys
 * are dropped; a duplicated key is an error the editor must surface
 * rather than letting one row silently win.
 */
export function metaFromRows(rows: MetaRow[]): { meta: Record<string, string>; duplicate: string } {
  const meta: Record<string, string> = {};
  const seen = new Set<string>();
  for (const row of rows) {
    const key = row.key.trim();
    if (key === "") {
      continue;
    }
    const folded = key.toLowerCase();
    if (seen.has(folded)) {
      return { meta: {}, duplicate: key };
    }
    seen.add(folded);
    meta[key] = row.value;
  }
  return { meta, duplicate: "" };
}

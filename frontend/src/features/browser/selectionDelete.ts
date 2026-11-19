// The deletion a selection opens (§4.4), shared by the Delete key and the
// working list's button so the two never disagree: several rows open the
// selected-set deletion (ADR 0093 decision 5), one row the flow of its own
// kind (ADR 0092, ADR 0094 decision 5). A link, an implied prefix and a real
// object at a prefix name have none; a prefix's own deletion is the bulk
// deletion with its own dialog.

import type { BrowserState, ShownDeleteKind } from "./browserState.svelte";

interface SingleRowDelete {
  label: string;
  open: (browser: BrowserState) => boolean;
}

const singleRow: Record<ShownDeleteKind, SingleRowDelete> = {
  object: { label: "Delete object…", open: (b) => b.openObjectDelete() },
  marker: { label: "Delete marker…", open: (b) => b.openMarkerDelete() },
  manifest: { label: "Delete manifest…", open: (b) => b.openManifestDelete() },
};

/** The deletion offered for the one selected row, or null. */
export function singleRowDelete(browser: BrowserState): SingleRowDelete | null {
  if (browser.chosenRows.length >= 2) return null;
  const kind = browser.deletableSelectionKind();
  return kind ? singleRow[kind] : null;
}

/** Open the deletion the selection owns; false when it has none. */
export function openSelectionDelete(browser: BrowserState): boolean {
  if (browser.chosenRows.length >= 2) return browser.openSelectedDelete();
  return singleRowDelete(browser)?.open(browser) ?? false;
}

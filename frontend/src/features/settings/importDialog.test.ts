import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { BookmarkDTO, ImportPreview, ImportResult } from "../../lib/api";
import { newFakeApi } from "../browser/fakeApi";
import { ImportState } from "../browser/importState.svelte";
import ImportBookmarksDialog from "./ImportBookmarksDialog.svelte";

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  vi.useRealTimers();
  vi.restoreAllMocks();
});

const preview: ImportPreview = {
  token: "t-1",
  fileName: "rclone.conf",
  rows: [
    { index: 0, name: "fresh", kind: "TempAuth", authUrl: "https://s.example/auth/v1.0", user: "a:u", project: "", region: "", hasSecret: true, state: "importable", reason: "" },
    { index: 1, name: "again", kind: "TempAuth", authUrl: "https://s.example/auth/v1.0", user: "a:u", project: "", region: "", hasSecret: true, state: "duplicate", reason: "a bookmark already has this auth URL, user and project" },
    { index: 2, name: "envy", kind: "Keystone v3", authUrl: "", user: "", project: "", region: "", hasSecret: false, state: "skipped", reason: "takes its credentials from the environment" },
  ],
};

function checkbox(name: string): HTMLInputElement {
  return document.querySelector<HTMLInputElement>(`input[aria-label="Import ${name}"]`)!;
}

// One account whose source holds no password.
const keyless: ImportPreview = {
  token: "t-2",
  fileName: "clouds.yaml",
  rows: [{ index: 0, name: "bare", kind: "Keystone v3", authUrl: "https://k.example/v3", user: "u", project: "p", region: "r", hasSecret: false, state: "importable", reason: "" }],
};

function rename(name: string, to: string) {
  const field = document.querySelector<HTMLInputElement>(`input[aria-label="Name for ${name}"]`)!;
  field.value = to;
  field.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);
  if (!found) throw new Error("Missing button: " + label);
  return found;
}

async function settle() {
  for (let i = 0; i < 8; i++) {
    await Promise.resolve();
    flushSync();
  }
}

// ADR 0114: importable rows start ticked and the others do not, a skipped
// A bookmark as the rail holds it after the import saved it.
function saved(id: string, displayName: string, extra: Partial<BookmarkDTO> = {}): BookmarkDTO {
  return { id, displayName, authUrl: "https://example.test/auth", username: "u", storageUrlOverride: "", authVersion: 1,
    userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: true, ...extra };
}

// row cannot be ticked, the save sends the choice with verification on by
// default, and a row that did not verify offers its editor.
describe("import bookmarks dialog", () => {
  it("imports the ticked rows, follows the verification and offers an edit for a failure", async () => {
    vi.useFakeTimers();
    const api = newFakeApi();
    api.previewImport = async () => preview;
    const pending: ImportResult = { token: "t-1", done: false, outcomes: [{ index: 0, name: "fresh", bookmarkId: "bm-9", verify: "pending", reason: "" }] };
    const done: ImportResult = { token: "t-1", done: true, outcomes: [{ index: 0, name: "fresh", bookmarkId: "bm-9", verify: "failed", reason: "the identity service refused these credentials" }] };
    const save = vi.fn(async () => pending);
    api.saveImport = save;
    api.importStatus = async () => done;
    const changed = vi.fn();
    const imports = new ImportState(api, changed);
    expect(await imports.pick()).toBe("picked");
    const edit = vi.fn();
    mounted.add(mount(ImportBookmarksDialog, { target: document.body, props: { imports, bookmarks: [saved("bm-9", "Prod AU", { verifyFailure: "the identity service refused these credentials" })], onClose: vi.fn(), onEditBookmark: edit } }));
    flushSync();

    expect(checkbox("fresh").checked).toBe(true);
    expect(checkbox("again").checked).toBe(false);
    expect(checkbox("envy").disabled).toBe(true);
    expect(document.body.textContent).toContain("Not imported: it takes its credentials from the environment.");

    // The row goes under the name in its field (ADR 0118 decision 3).
    rename("fresh", " Prod AU ");
    button("Import 1 bookmark").click();
    await settle();
    expect(save).toHaveBeenCalledWith("t-1", [{ index: 0, name: "Prod AU" }], true);
    expect(changed).toHaveBeenCalledTimes(1);
    expect(document.body.textContent).toContain("Imported; verifying…");

    await vi.advanceTimersByTimeAsync(600);
    await settle();
    expect(document.body.textContent).toContain("Imported, but did not verify: the identity service refused these credentials.");
    expect(changed).toHaveBeenCalledTimes(2);
    document.querySelector<HTMLButtonElement>('button[aria-label="Edit fresh"]')!.click();
    expect(edit).toHaveBeenCalledWith("bm-9");
  });

  it("can untick verification and import a duplicate as a second bookmark", async () => {
    const api = newFakeApi();
    api.previewImport = async () => preview;
    const save = vi.fn(async (token: string) => ({ token, done: true, outcomes: [] }));
    api.saveImport = save;
    const imports = new ImportState(api, vi.fn());
    await imports.pick();
    mounted.add(mount(ImportBookmarksDialog, { target: document.body, props: { imports, bookmarks: [], onClose: vi.fn(), onEditBookmark: vi.fn() } }));
    flushSync();

    checkbox("again").click();
    const verify = [...document.querySelectorAll<HTMLInputElement>("label.verify input")][0];
    verify.click();
    flushSync();
    button("Import 2 bookmarks").click();
    await settle();
    expect(save).toHaveBeenCalledWith("t-1", [{ index: 0, name: "fresh" }, { index: 1, name: "again" }], false);
  });

  it("does not import a ticked row whose name is empty", async () => {
    const api = newFakeApi();
    api.previewImport = async () => preview;
    const save = vi.fn(async (token: string) => ({ token, done: true, outcomes: [] }));
    api.saveImport = save;
    const imports = new ImportState(api, vi.fn());
    await imports.pick();
    mounted.add(mount(ImportBookmarksDialog, { target: document.body, props: { imports, bookmarks: [], onClose: vi.fn(), onEditBookmark: vi.fn() } }));
    flushSync();

    rename("fresh", " ");
    expect(button("Import 1 bookmark").disabled).toBe(true);
    expect(document.body.textContent).toContain("Give every ticked row a name.");
    await imports.save();
    expect(save).not.toHaveBeenCalled();
    rename("fresh", "fresh again");
    expect(button("Import 1 bookmark").disabled).toBe(false);
    expect(document.body.textContent).not.toContain("Give every ticked row a name.");
  });

  it("offers the editor for a row imported without a password (ADR 0118 decision 4)", async () => {
    const api = newFakeApi();
    api.previewImport = async () => keyless;
    api.saveImport = async (token) => ({ token, done: true, outcomes: [{ index: 0, name: "bare", bookmarkId: "bm-3", verify: "no_secret", reason: "" }] });
    const imports = new ImportState(api, vi.fn());
    await imports.pick();
    const edit = vi.fn();
    mounted.add(mount(ImportBookmarksDialog, { target: document.body, props: { imports, bookmarks: [saved("bm-3", "bare", { hasCredential: false })], onClose: vi.fn(), onEditBookmark: edit } }));
    flushSync();

    button("Import 1 bookmark").click();
    await settle();
    document.querySelector<HTMLButtonElement>('button[aria-label="Enter key for bare"]')!.click();
    expect(edit).toHaveBeenCalledWith("bm-3");
  });

  // Back from an editor where the bookmark was deleted: the row still says
  // what the import did, and offers no editor for a bookmark that is gone.
  it("offers no editor for a row whose bookmark has been deleted", async () => {
    const api = newFakeApi();
    api.previewImport = async () => preview;
    api.saveImport = async (token) => ({ token, done: true, outcomes: [{ index: 0, name: "fresh", bookmarkId: "bm-9", verify: "failed", reason: "the identity service refused these credentials" }] });
    const imports = new ImportState(api, vi.fn());
    await imports.pick();
    mounted.add(mount(ImportBookmarksDialog, { target: document.body, props: { imports, bookmarks: [], onClose: vi.fn(), onEditBookmark: vi.fn() } }));
    flushSync();
    button("Import 1 bookmark").click();
    await settle();
    expect(document.body.textContent).toContain("Imported, but did not verify");
    expect(document.querySelector('button[aria-label="Edit fresh"]')).toBeNull();
  });
});

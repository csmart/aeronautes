import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { BookmarkDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import BookmarkEditor from "./BookmarkEditor.svelte";
import BookmarkRail from "./BookmarkRail.svelte";
import TransferDialog from "../transfer/TransferDialog.svelte";

// ADR 0118 decision 4: a bookmark with no saved key, from an import of a
// source that held none or from a restored backup, says so and leads to
// where the key is entered instead of failing when it is used.

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of [...mounted].reverse()) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

async function settle() {
  for (let i = 0; i < 8; i++) { await Promise.resolve(); flushSync(); }
}

function bookmark(id: string, hasCredential: boolean): BookmarkDTO {
  return { id, displayName: id, authUrl: "https://swift.example.test/auth/v1.0", username: "acct:user", storageUrlOverride: "", authVersion: 1,
    userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential };
}

function state() {
  const browser = new BrowserState(newFakeApi());
  browser.bookmarks = [bookmark("keyed", true), bookmark("keyless", false)];
  browser.bookmarksLoaded = true;
  return browser;
}

describe("a bookmark with no saved key", () => {
  it("is marked on the rail and opens its editor when chosen, without a connection attempt", () => {
    const browser = state();
    const connect = vi.spyOn(browser, "connect").mockResolvedValue(true);
    const edit = vi.fn();
    mounted.add(mount(BookmarkRail, { target: document.body,
      props: { browser, onAddBookmark: vi.fn(), onEditBookmark: edit, onImportBookmarks: vi.fn(), onOpenSettings: vi.fn() } }));
    flushSync();
    const tile = (name: string) => document.querySelector<HTMLButtonElement>(`button.bookmark[aria-label="${name}"]`)!;

    expect(tile("keyless").querySelector('[aria-label="No saved key"]')).not.toBeNull();
    expect(tile("keyed").querySelector('[aria-label="No saved key"]')).toBeNull();

    tile("keyless").click();
    expect(edit).toHaveBeenCalledExactlyOnceWith(browser.bookmarks[1]);
    expect(connect).not.toHaveBeenCalled();
    tile("keyed").click();
    expect(connect).toHaveBeenCalledExactlyOnceWith("keyed");
    expect(edit).toHaveBeenCalledTimes(1);
  });

  it("opens its editor on the key field, with a note saying why", async () => {
    const browser = state();
    mounted.add(mount(BookmarkEditor, { target: document.body, props: { browser, existing: browser.bookmarks[1], onClose: vi.fn() } }));
    flushSync();
    await settle();
    expect(document.body.textContent).toContain("This bookmark has no saved key. Enter it below to connect.");
    const key = document.querySelector<HTMLInputElement>('input[type="password"]')!;
    expect(document.activeElement).toBe(key);
    expect(key.required).toBe(true);
  });

  it("leaves a keyed bookmark's editor as it was", async () => {
    const browser = state();
    mounted.add(mount(BookmarkEditor, { target: document.body, props: { browser, existing: browser.bookmarks[0], onClose: vi.fn() } }));
    flushSync();
    await settle();
    expect(document.body.textContent).not.toContain("no saved key");
    expect(document.activeElement).not.toBe(document.querySelector('input[type="password"]'));
  });

  it("cannot be chosen as a copy's destination", () => {
    const browser = state();
    browser.connectedBookmarkId = "keyed";
    browser.containerName = "media";
    mounted.add(mount(TransferDialog, { target: document.body, props: { browser, onClose: vi.fn() } }));
    flushSync();
    const options = [...document.querySelectorAll<HTMLOptionElement>('select[aria-label="Destination bookmark"] option')];
    expect(options.map((o) => [o.textContent?.trim(), o.disabled])).toEqual([["keyed", false], ["keyless (no saved key)", true]]);
  });
});

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api } from "../../lib/api";
import { newFakeApi } from "./fakeApi";
import BrowserScreen from "./BrowserScreen.svelte";

const api = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: api }));

const mounted = new Set<ReturnType<typeof mount>>();
beforeEach(() => {
  Object.assign(api, newFakeApi());
});
afterEach(async () => {
  for (const component of [...mounted].reverse()) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

async function settle() {
  for (let i = 0; i < 16; i++) {
    await Promise.resolve();
    flushSync();
  }
}

async function showScreen() {
  mounted.add(mount(BrowserScreen, { target: document.body }));
  flushSync();
  await settle();
}

const welcome = () => document.querySelector('[role="dialog"][aria-label="Welcome"]');
const editor = () => document.querySelector('[role="dialog"][aria-label="Add bookmark"]');

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);
  if (!found) throw new Error("Missing button: " + label);
  return found;
}

describe("first run", () => {
  it("offers to add the first bookmark and opens the editor from there", async () => {
    await showScreen();
    expect(welcome()).not.toBeNull();
    expect(document.body.textContent).toContain("Everything starts from a bookmark");
    button("Add a bookmark").click();
    flushSync();
    expect(welcome()).toBeNull();
    expect(editor()).not.toBeNull();
  });

  it("stays away for the window once declined, even after the list is read again", async () => {
    await showScreen();
    button("Not now").click();
    flushSync();
    expect(welcome()).toBeNull();
    document.querySelector<HTMLButtonElement>('[aria-label="Refresh bookmark list"]')!.click();
    await settle();
    expect(welcome()).toBeNull();
  });

  it("does not appear when a bookmark exists", async () => {
    await api.saveBookmark({ id: "", displayName: "Existing", authUrl: "https://swift.example/auth/v1.0", username: "a:b",
      storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "key");
    await showScreen();
    expect(welcome()).toBeNull();
    expect(document.body.textContent).toContain("Existing");
  });

  it("does not mistake a failed bookmark read for an empty list", async () => {
    api.listBookmarks = vi.fn().mockRejectedValue(new Error("private diagnostic"));
    await showScreen();
    expect(welcome()).toBeNull();
    expect(document.body.textContent).toMatch(/bookmarks?.*unavailable/i);
  });
});

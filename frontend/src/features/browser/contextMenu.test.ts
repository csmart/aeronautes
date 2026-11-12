import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import WorkingList from "./WorkingList.svelte";
import ContainerColumn from "./ContainerColumn.svelte";

// A row's context menu offers the toolbar's own actions for what is
// selected: a right-click selects the row unless it is already part of a
// multi-selection, and the keyboard opens the same menu on the focused row.
const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
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

async function list() {
  const api = newFakeApi();
  const copied = vi.spyOn(api, "copyText");
  const browser = new BrowserState(api);
  const bookmark = await browser.saveBookmark({ id: "", displayName: "Mock", authUrl: "https://example.test/auth",
    username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
  await browser.connect(bookmark.id);
  await browser.openContainer("fixtures");
  await settle();
  mounted.add(mount(WorkingList, { target: document.body, props: { browser } }));
  flushSync();
  const objects = browser.visibleEntries.map((row, i) => ({ row, i })).filter(({ row }) => row.kind === "object");
  return { browser, copied, objects };
}

function rowElement(index: number): HTMLElement {
  return document.querySelectorAll<HTMLElement>('[role="option"]')[index];
}

function rightClick(element: HTMLElement) {
  element.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 2 }));
  element.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 40, clientY: 60 }));
  flushSync();
}

function menuLabels(): string[] {
  return [...document.querySelectorAll('[role="menu"] [role="menuitem"]')].map((b) => b.textContent?.trim() ?? "");
}

describe("row context menu", () => {
  it("selects a right-clicked row and offers its actions, which run as the toolbar's do", async () => {
    const { browser, copied, objects } = await list();
    const target = objects[0];
    rightClick(rowElement(target.i));
    expect(browser.selectedIndex).toBe(target.i);
    expect(menuLabels()).toEqual(expect.arrayContaining(["Download object…", "Copy object…", "Copy name"]));

    const copyName = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((b) => b.textContent?.trim() === "Copy name")!;
    copyName.click();
    await settle();
    expect(copied).toHaveBeenCalledWith(target.row.name);
    expect(document.querySelector('[role="menu"]')).toBeNull();
  });

  it("keeps a multi-selection when one of its rows is right-clicked", async () => {
    const { browser, objects } = await list();
    browser.select(objects[0].i);
    browser.toggleRow(objects[1].i);
    flushSync();
    rightClick(rowElement(objects[1].i));
    expect(browser.chosenRows.length).toBe(2);
    expect(menuLabels()).toEqual(expect.arrayContaining(["Download selected…", "Copy selected…", "Delete selected…"]));
  });

  it("opens from the keyboard on the focused row and closes with Escape", async () => {
    const { browser, objects } = await list();
    browser.select(objects[0].i);
    flushSync();
    const listbox = document.querySelector<HTMLElement>('[role="listbox"]')!;
    listbox.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "F10", shiftKey: true }));
    flushSync();
    expect(document.querySelector('[role="menu"]')).not.toBeNull();
    document.querySelector('[role="menu"]')!.dispatchEvent(new KeyboardEvent("keydown", { bubbles: true, key: "Escape" }));
    flushSync();
    expect(document.querySelector('[role="menu"]')).toBeNull();
  });
});

describe("container row context menu", () => {
  it("opens the container and then the chosen action", async () => {
    const api = newFakeApi();
    const browser = new BrowserState(api);
    const bookmark = await browser.saveBookmark({ id: "", displayName: "Mock", authUrl: "https://example.test/auth",
      username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
    await browser.connect(bookmark.id);
    await settle();
    mounted.add(mount(ContainerColumn, { target: document.body, props: { browser, onCreate: vi.fn() } }));
    flushSync();
    const row = [...document.querySelectorAll<HTMLButtonElement>("button.container-row")].find((b) => b.textContent?.includes("fixtures"))!;
    row.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 10, clientY: 10 }));
    flushSync();
    expect(menuLabels()).toEqual(expect.arrayContaining(["Open", "Copy container…", "Copy name"]));
    expect(browser.containerName).toBeNull();

    [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((b) => b.textContent?.trim() === "Copy container…")!.click();
    await settle();
    expect(browser.containerName).toBe("fixtures");
    expect(browser.transferOpen).toBe(true);
  });
});

import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, ListingRowDTO } from "../../lib/api";
import { v1Offers } from "../../lib/v1.svelte";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import Inspector from "../inspector/Inspector.svelte";
import WorkingList from "./WorkingList.svelte";
import BrowserScreen from "./BrowserScreen.svelte";

// ADR 0093 decision 1: several loaded rows can be selected, with a toggle, a
// range and select all over the loaded rows only, and the inspector
// summarises them without a read.
const api = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: api }));

const shipped = { ...v1Offers };
const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of [...mounted].reverse()) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  vi.restoreAllMocks();
  Object.assign(v1Offers, shipped);
});

async function settle() {
  for (let i = 0; i < 16; i++) { await Promise.resolve(); flushSync(); }
}
function button(label: string, root: ParentNode = document): HTMLButtonElement {
  const found = [...root.querySelectorAll("button")].find((b) => b.textContent?.trim() === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}
function click(element: HTMLElement) { element.click(); flushSync(); }
function names(rows: ListingRowDTO[]): string[] { return rows.map((row) => row.name); }

async function state() {
  const fake = newFakeApi();
  const browser = new BrowserState(fake);
  const bookmark = await browser.saveBookmark({ id: "", displayName: "Mock", authUrl: "https://example.test/auth",
    username: "fixture:user", storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
  await browser.connect(bookmark.id);
  await browser.openContainer("fixtures");
  await settle();
  const root = document.createElement("div");
  document.body.append(root);
  const index = (name: string) => {
    const i = browser.visibleEntries.findIndex((row) => row.name === name);
    if (i < 0) throw new Error(`no row ${name}`);
    return i;
  };
  return { fake, browser, root, bookmark, index };
}

describe("the selection model", () => {
  it("toggles, extends and selects every loaded row, and reads nothing for a summary", async () => {
    const f = await state();
    const detail = vi.spyOn(f.fake, "objectDetail");
    f.browser.select(f.index("empty-object"));
    await settle();
    expect(f.browser.objectDetail?.name).toBe("empty-object");
    expect(detail).toHaveBeenCalledTimes(1);

    f.browser.toggleRow(f.index("external-slo"));
    await settle();
    expect(names(f.browser.chosenRows)).toEqual(["empty-object", "external-slo"]);
    expect(f.browser.selectionCount).toBe(2);
    expect(f.browser.detailIdentity).toBeNull();
    expect(f.browser.objectDetail).toBeNull();
    expect(f.browser.deletableSelectionKind()).toBeNull();
    expect(detail).toHaveBeenCalledTimes(1);

    // Toggling the second row off leaves one row, which is a plain selection again.
    f.browser.toggleRow(f.index("external-slo"));
    await settle();
    expect(f.browser.chosenRows).toEqual([]);
    expect(f.browser.selectionCount).toBe(1);
    expect(f.browser.selectedEntry?.name).toBe("empty-object");
    expect(f.browser.objectDetail?.name).toBe("empty-object");
    expect(detail).toHaveBeenCalledTimes(2);

    // A range runs from the plainly selected row.
    f.browser.extendTo(f.index("fake/"));
    await settle();
    const from = f.index("empty-object");
    const to = f.index("fake/");
    expect(names(f.browser.chosenRows)).toEqual(names(f.browser.visibleEntries.slice(from, to + 1)));
    expect(f.browser.chosenRows.length).toBeGreaterThan(2);

    // Select all is every loaded row and nothing the server still holds.
    f.browser.entriesMarker = "more-on-server";
    f.browser.selectAll();
    await settle();
    expect(f.browser.chosenRows.length).toBe(f.browser.visibleEntries.length);
    expect(f.browser.selectionCount).toBe(f.browser.entries.length);
    expect(detail).toHaveBeenCalledTimes(2);

    f.browser.clearSelection();
    await settle();
    expect(f.browser.selectionCount).toBe(0);
    expect(f.browser.selectedIndex).toBe(-1);
  });

  it("narrows select all to the rows the filter leaves on screen", async () => {
    const f = await state();
    f.browser.filter = "external";
    f.browser.selectAll();
    expect(names(f.browser.chosenRows)).toEqual(["external-dlo", "external-slo"]);
    expect(f.browser.chosenRows.length).toBeLessThan(f.browser.entries.length);
  });

  it("follows identities through a refresh and collapses to the row that is left", async () => {
    const f = await state();
    f.browser.select(f.index("empty-object"));
    f.browser.toggleRow(f.index("external-slo"));
    await settle();
    const real = f.fake.objects;
    f.fake.objects = async (...args) => {
      const page = await real(...args);
      return { ...page, rows: page.rows.filter((row) => row.name !== "external-slo") };
    };
    await f.browser.refreshEntries();
    await settle();
    expect(f.browser.chosenRows).toEqual([]);
    expect(f.browser.selectionCount).toBe(1);
    expect(f.browser.selectedEntry?.name).toBe("empty-object");
    expect(f.browser.objectDetail?.name).toBe("empty-object");
  });
});

describe("the inspector summary", () => {
  it("counts the objects, their listed bytes and the prefix rows from the listing alone", async () => {
    const f = await state();
    const component = mount(Inspector, { target: f.root, props: { browser: f.browser } });
    mounted.add(component);
    f.browser.select(f.index("empty-object"));
    await settle();
    const detail = vi.spyOn(f.fake, "objectDetail");
    f.browser.toggleRow(f.index("external-slo"));
    f.browser.toggleRow(f.index("normal/"));
    await settle();
    const text = (f.root.textContent ?? "").replace(/\s+/g, " ");
    expect(text).toContain("3 rows selected");
    expect(text).toContain("no row is inspected");
    expect(text).toContain("Objects 2");
    expect(text).toContain("Listed bytes 1.02 KB");
    expect(text).toContain("Prefix rows 1");
    expect(text).not.toContain("empty-object");
    expect(detail).not.toHaveBeenCalled();
  });
});

describe("the working list", () => {
  it("toggles with Ctrl, extends with Shift and counts the selection", async () => {
    const f = await state();
    const component = mount(WorkingList, { target: f.root, props: { browser: f.browser } });
    mounted.add(component);
    flushSync(); await settle();
    const row = (name: string) => [...f.root.querySelectorAll<HTMLElement>('[role="option"]')]
      .find((el) => el.querySelector(".name")?.textContent?.trim() === name)!;
    row("empty-object").dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
    flushSync(); await settle();
    row("external-dlo").dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, ctrlKey: true }));
    flushSync(); await settle();
    expect(names(f.browser.chosenRows)).toEqual(["empty-object", "external-dlo"]);
    expect(row("external-dlo").getAttribute("aria-selected")).toBe("true");
    expect(row("empty-object").getAttribute("aria-selected")).toBe("true");
    expect(row("fake/").getAttribute("aria-selected")).toBe("false");
    expect(f.root.querySelector('[role="listbox"]')?.getAttribute("aria-multiselectable")).toBe("true");
    expect(f.root.querySelector(".pane-foot")?.textContent).toContain("2 selected");

    // A Shift click extends from the last toggled row.
    row("fake/").dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, shiftKey: true }));
    flushSync(); await settle();
    expect(names(f.browser.chosenRows)).toEqual(["external-dlo", "external-slo", "fake/"]);
    expect(f.root.querySelector(".pane-foot")?.textContent).toContain("3 selected");
  });
});

describe("the selection shortcuts", () => {
  async function screen(enabled: string[]) {
    Object.assign(api, newFakeApi());
    const info = api.info;
    api.info = async () => ({ ...(await info()), enabledCapabilities: enabled, unrecognisedCapabilities: [] });
    await api.saveBookmark({ id: "", displayName: "Key fixture", authUrl: "https://example.test/auth",
      username: "fixture:user", storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
    const root = document.createElement("div"); document.body.append(root);
    const component = mount(BrowserScreen, { target: root }); mounted.add(component);
    flushSync(); await settle();
    click(document.querySelector<HTMLButtonElement>('[aria-label="Key fixture"]')!); await settle();
    click([...document.querySelectorAll<HTMLButtonElement>(".container-row")]
      .find((b) => b.querySelector(".container-name")?.textContent?.trim() === "fixtures")!);
    await settle();
    const row = (name: string) => [...document.querySelectorAll<HTMLElement>('[role="option"]')]
      .find((el) => el.querySelector(".name")?.textContent?.trim() === name)!;
    const listbox = document.querySelector<HTMLElement>('[role="listbox"]')!;
    return { row, listbox, foot: listbox.closest("section")!.querySelector<HTMLElement>(".pane-foot")! };
  }

  it("selects every loaded row with Ctrl+A and clears with Escape", async () => {
    const s = await screen([]);
    const rows = document.querySelectorAll('[role="option"]').length;
    expect(rows).toBeGreaterThan(2);
    const all = new KeyboardEvent("keydown", { key: "a", ctrlKey: true, bubbles: true, cancelable: true });
    window.dispatchEvent(all); flushSync(); await settle();
    expect(all.defaultPrevented).toBe(true);
    expect(s.foot.textContent).toContain(`${rows} selected`);
    const escape = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    window.dispatchEvent(escape); flushSync(); await settle();
    expect(escape.defaultPrevented).toBe(true);
    expect(s.foot.textContent).toContain("none selected");
    // With nothing selected Escape is left to whoever else wants it.
    const again = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    window.dispatchEvent(again); flushSync();
    expect(again.defaultPrevented).toBe(false);
  });
});

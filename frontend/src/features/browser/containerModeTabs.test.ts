import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api } from "../../lib/api";
import { containersFixture } from "../../lib/fixtures";
import { newFakeApi } from "./fakeApi";
import BrowserScreen from "./BrowserScreen.svelte";

// The Container mode control is a tablist over the working list's container
// column (§52 modes). These cases pin the tab semantics that UI-11/P29
// require of it: one roving tab stop, tab/panel association, horizontal
// arrow and Home/End movement with automatic activation, and PR72's rule
// that a key consumed here never reaches the global browser handler that
// would otherwise move the background working list (§4.4).

const api = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: api }));

const mounted = new Set<ReturnType<typeof mount>>();
beforeEach(() => {
  Object.assign(api, newFakeApi());
  // JSDOM has no layout; keep the production focus action's visibility
  // filtering deterministic, as the keyboard-scope cases do.
  vi.spyOn(HTMLElement.prototype, "offsetParent", "get").mockImplementation(function (this: HTMLElement) {
    return this.closest("[hidden]") ? null : this.parentElement;
  });
});
afterEach(async () => {
  for (const component of [...mounted].reverse()) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

async function settle() {
  for (let i = 0; i < 16; i++) { await Promise.resolve(); flushSync(); }
}
function get<T extends HTMLElement = HTMLElement>(selector: string, root: ParentNode = document): T {
  const found = root.querySelector<T>(selector);
  if (!found) throw new Error(`Missing element: ${selector}`);
  return found;
}
function click(element: HTMLElement) { element.click(); flushSync(); }
function press(target: EventTarget, key: string, init: KeyboardEventInit = {}) {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init });
  target.dispatchEvent(event); flushSync();
  return event;
}
function tablist(): HTMLElement { return get('[role="tablist"][aria-label="Container mode"]'); }
function tabs(): HTMLButtonElement[] { return [...tablist().querySelectorAll<HTMLButtonElement>('[role="tab"]')]; }
function tab(label: string): HTMLButtonElement {
  const found = tabs().find((t) => t.textContent?.trim() === label);
  if (!found) throw new Error(`Missing tab: ${label}`);
  return found;
}
function labels(elements: HTMLElement[]): string[] { return elements.map((e) => e.textContent?.trim() ?? ""); }
function selectedTab(): HTMLButtonElement | undefined { return tabs().find((t) => t.getAttribute("aria-selected") === "true"); }
function listSelection(): string | null { return get('[role="listbox"]').getAttribute("aria-activedescendant"); }
function containerRows(): string[] {
  return [...document.querySelectorAll<HTMLElement>(".container-row .container-name")]
    .map((name) => name.childNodes[0]?.textContent?.trim() ?? "");
}
function selectedContainer(): string | undefined {
  return document.querySelector<HTMLElement>(".container-row.selected .container-name")?.childNodes[0]?.textContent?.trim();
}
const hiddenNames = containersFixture.filter((c) => c.segmentRole === "confirmed").map((c) => c.name);
const suspectedNames = containersFixture.filter((c) => c.segmentRole === "suspected").map((c) => c.name);

async function screen() {
  await api.saveBookmark({ id: "", displayName: "Mode fixture", authUrl: "https://example.test/auth",
    username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
  const root = document.createElement("div"); document.body.append(root);
  const component = mount(BrowserScreen, { target: root }); mounted.add(component);
  flushSync(); await settle();
  click(get('[aria-label="Mode fixture"]')); await settle();
  click([...document.querySelectorAll<HTMLButtonElement>(".container-row")]
    .find((b) => b.querySelector(".container-name")?.textContent?.trim() === "fixtures")!);
  await settle();
  expect(listSelection()).toBeNull();
  expect(labels(tabs())).toEqual(["Logical", "All", "Backstage"]);
  expect(selectedTab()).toBe(tab("Logical"));
}

describe("container mode tabs", () => {
  it("exposes one roving tab stop for the whole mode control", async () => {
    await screen();
    expect(labels(tabs().filter((t) => t.tabIndex !== -1))).toEqual(["Logical"]);
    click(tab("Backstage")); await settle();
    expect(labels(tabs().filter((t) => t.tabIndex !== -1))).toEqual(["Backstage"]);
  });

  it("associates every tab with the single container panel it controls", async () => {
    await screen();
    const controls = new Set(tabs().map((t) => t.getAttribute("aria-controls")));
    expect(controls.size).toBe(1);
    const [panelId] = [...controls];
    const panel = document.getElementById(panelId ?? "");
    expect(panel?.getAttribute("role")).toBe("tabpanel");
    for (const t of tabs()) expect(t.id).not.toBe("");
    expect(new Set(tabs().map((t) => t.id)).size).toBe(3);
    expect(panel?.getAttribute("aria-labelledby")).toBe(tab("Logical").id);
    click(tab("All")); await settle();
    expect(panel?.getAttribute("aria-labelledby")).toBe(tab("All").id);
    expect(panel?.contains(document.querySelector(".container-row"))).toBe(true);
  });

  it("moves selection and focus with the horizontal arrows and wraps at the ends", async () => {
    await screen();
    tab("Logical").focus();
    expect(press(tab("Logical"), "ArrowRight").defaultPrevented).toBe(true); await settle();
    expect(selectedTab()).toBe(tab("All")); expect(document.activeElement).toBe(tab("All"));
    press(tab("All"), "ArrowRight"); await settle();
    expect(selectedTab()).toBe(tab("Backstage")); expect(document.activeElement).toBe(tab("Backstage"));
    press(tab("Backstage"), "ArrowRight"); await settle();
    expect(selectedTab()).toBe(tab("Logical")); expect(document.activeElement).toBe(tab("Logical"));
    press(tab("Logical"), "ArrowLeft"); await settle();
    expect(selectedTab()).toBe(tab("Backstage")); expect(document.activeElement).toBe(tab("Backstage"));
  });

  it("moves to the first and last tab with Home and End", async () => {
    await screen();
    tab("Logical").focus();
    expect(press(tab("Logical"), "End").defaultPrevented).toBe(true); await settle();
    expect(selectedTab()).toBe(tab("Backstage")); expect(document.activeElement).toBe(tab("Backstage"));
    expect(press(tab("Backstage"), "Home").defaultPrevented).toBe(true); await settle();
    expect(selectedTab()).toBe(tab("Logical")); expect(document.activeElement).toBe(tab("Logical"));
  });

  it("consumes its own keys without moving the working list selection", async () => {
    await screen();
    const list = get('[role="listbox"]'); list.focus(); press(list, "ArrowDown"); await settle();
    const before = listSelection(); expect(before).toMatch(/-row-0$/);
    tab("Logical").focus();
    for (const key of ["ArrowRight", "ArrowLeft", "Home", "End"]) {
      const event = press(document.activeElement!, key); await settle();
      expect(event.defaultPrevented).toBe(true);
      expect(tablist().contains(document.activeElement)).toBe(true);
      expect(listSelection()).toBe(before);
    }
  });

  it("leaves the vertical arrows to the working list as before", async () => {
    await screen();
    tab("Logical").focus();
    expect(press(tab("Logical"), "ArrowDown").defaultPrevented).toBe(false); await settle();
    expect(document.activeElement).toBe(get('[role="listbox"]'));
    expect(listSelection()).toMatch(/-row-0$/);
    expect(selectedTab()).toBe(tab("Logical"));
  });

  it("preserves the loaded-row filter and the exact selection across a keyboard mode change", async () => {
    await screen();
    const filter = get<HTMLInputElement>('[placeholder="filter this list…"]');
    filter.value = "external"; filter.dispatchEvent(new Event("input", { bubbles: true })); flushSync(); await settle();
    const list = get('[role="listbox"]'); list.focus(); press(list, "ArrowDown"); await settle();
    const before = listSelection(); expect(before).toMatch(/-row-0$/);
    const visibleBefore = [...document.querySelectorAll('[role="option"] .name')].map((n) => n.textContent?.trim());
    tab("Logical").focus();
    press(tab("Logical"), "ArrowRight"); await settle();
    expect(selectedTab()).toBe(tab("All"));
    press(tab("All"), "ArrowLeft"); await settle();
    expect(selectedTab()).toBe(tab("Logical"));
    expect(filter.value).toBe("external");
    expect(listSelection()).toBe(before);
    expect(selectedContainer()).toBe("fixtures");
    expect([...document.querySelectorAll('[role="option"] .name')].map((n) => n.textContent?.trim())).toEqual(visibleBefore);
  });

  it("lists the containers of the mode reached by keyboard", async () => {
    await screen();
    for (const name of hiddenNames) expect(containerRows()).not.toContain(name);
    for (const name of suspectedNames) expect(containerRows()).toContain(name);
    expect(document.querySelectorAll(".class-chip")).toHaveLength(0);
    tab("Logical").focus();
    press(tab("Logical"), "ArrowRight"); await settle();
    expect(selectedTab()).toBe(tab("All"));
    for (const name of hiddenNames) expect(containerRows()).toContain(name);
    expect(document.querySelectorAll(".class-chip")).toHaveLength(0);
    press(tab("All"), "ArrowRight"); await settle();
    expect(selectedTab()).toBe(tab("Backstage"));
    expect(document.querySelectorAll(".class-chip").length).toBe(hiddenNames.length + suspectedNames.length);
  });

  it("still activates a tab by pointer", async () => {
    await screen();
    click(tab("All")); await settle();
    expect(selectedTab()).toBe(tab("All"));
    for (const name of hiddenNames) expect(containerRows()).toContain(name);
    click(tab("Logical")); await settle();
    expect(selectedTab()).toBe(tab("Logical"));
    for (const name of hiddenNames) expect(containerRows()).not.toContain(name);
  });
});

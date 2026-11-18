import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api } from "../../lib/api";
import { newFakeApi } from "./fakeApi";
import BrowserScreen from "./BrowserScreen.svelte";

// ADR 0125: the inspector's actions under its name, the Containers
// column's filter, and a new folder by name.

const api = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: api }));

const mounted = new Set<ReturnType<typeof mount>>();
beforeEach(() => {
  Object.assign(api, newFakeApi());
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
  for (let i = 0; i < 32; i++) { await Promise.resolve(); flushSync(); }
}
function get<T extends HTMLElement = HTMLElement>(selector: string, root: ParentNode = document): T {
  const found = root.querySelector<T>(selector);
  if (!found) throw new Error(`Missing element: ${selector}`);
  return found;
}
function click(element: HTMLElement) { element.click(); flushSync(); }
// The list selects a row as the pointer goes down on it.
function press(element: HTMLElement) {
  element.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0 })); flushSync();
}
// The transfer buttons name what they act on (ADR 0108: "Download folder…",
// "Copy selected…"); these tests are about which controls exist and what they
// open, so they know them as "Download…" and "Copy to…". The wording itself
// is tested in transferLabels.test.ts.
function transferLabel(text: string): string {
  return text.replace(/^Download (container|folder|object|selected)…$/, "Download…").replace(/^Copy (container|folder|object|selected)…$/, "Copy to…");
}

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")]
    .find((b) => transferLabel(b.textContent?.trim() ?? "") === label || b.getAttribute("aria-label") === label);
  if (!found) throw new Error(`Missing button ${label}`);
  return found;
}

const fixtureBookmark = { id: "", displayName: "Count fixture", authUrl: "https://example.test/auth",
  username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false };

async function openFixtures() {
  await api.saveBookmark(fixtureBookmark, "test-only");
  const component = mount(BrowserScreen, { target: document.body });
  mounted.add(component);
  flushSync(); await settle();
  click(get(`[aria-label="${fixtureBookmark.displayName}"]`)); await settle();
  const row = [...document.querySelectorAll<HTMLButtonElement>(".container-row")]
    .find((r) => r.querySelector(".container-name")?.firstChild?.textContent?.trim() === "fixtures");
  if (!row) throw new Error("fixtures row missing");
  click(row); await settle();
}

describe("inspector actions, the container filter and a new folder", () => {
  it("puts each view's actions above its first card", async () => {
    await openFixtures();
    const before = (label: string) => {
      const action = [...document.querySelectorAll<HTMLButtonElement>(".actions-row button")].find((b) => b.textContent?.trim() === label);
      if (!action) throw new Error(`no ${label} in an actions row`);
      const card = action.closest(".actions-row")!.parentElement!.querySelector(".card")!;
      return Boolean(action.compareDocumentPosition(card) & Node.DOCUMENT_POSITION_FOLLOWING);
    };
    expect(before("Configure container")).toBe(true);
    const file = [...document.querySelectorAll<HTMLElement>(".entry")].find((e) => !e.querySelector(".icon.folder"))!;
    press(file); await settle();
    expect(before("Edit metadata")).toBe(true);
  }, 20_000);

  it("filters the containers by any part of the name", async () => {
    await openFixtures();
    const names = () => [...document.querySelectorAll<HTMLElement>(".container-row .container-name")].map((n) => n.firstChild?.textContent?.trim());
    const filter = get<HTMLInputElement>('input[aria-label="Filter containers"]');
    filter.value = "XTUR"; filter.dispatchEvent(new Event("input", { bubbles: true })); flushSync(); await settle();
    expect(names()).toEqual(["fixtures"]);
    filter.value = "no-such-name"; filter.dispatchEvent(new Event("input", { bubbles: true })); flushSync(); await settle();
    expect(names()).toEqual([]);
    expect(document.body.textContent).toContain(`No loaded container's name holds "no-such-name"`);
  }, 20_000);

  it("creates a folder by name in the folder being browsed", async () => {
    await openFixtures();
    const createMarker = vi.spyOn(api, "createMarker");
    click(button("New folder…")); await settle();
    const dialog = get('[role="dialog"][aria-label="New folder"]');
    const name = get<HTMLInputElement>("input", dialog);
    name.value = "reports/"; name.dispatchEvent(new Event("input", { bubbles: true })); flushSync();
    expect(dialog.textContent).toContain("fixtures/reports/");
    click(button("Create")); await settle();
    expect(createMarker).toHaveBeenCalledWith(expect.anything(), "fixtures", "reports/");
    expect(document.querySelector('[aria-label="New folder"]')).toBeNull();
    expect([...document.querySelectorAll<HTMLElement>(".entry .name")].map((n) => n.textContent)).toContain("reports/");
  }, 20_000);

  // A press in the list's empty space clears the selection, as Escape does.
  it("clears the selection on a press below the rows", async () => {
    await openFixtures();
    const file = [...document.querySelectorAll<HTMLElement>(".entry")].find((e) => !e.querySelector(".icon.folder"))!;
    press(file); await settle();
    expect(document.querySelectorAll('[role="option"][aria-selected="true"]')).toHaveLength(1);
    const list = get('[role="listbox"][aria-label="Objects"]');
    list.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0 })); flushSync(); await settle();
    expect(document.querySelectorAll('[role="option"][aria-selected="true"]')).toHaveLength(0);
  }, 20_000);

  // The actions stay where they are while an editor is open under them.
  it("keeps an object's actions in place while its editor is open", async () => {
    await openFixtures();
    const file = [...document.querySelectorAll<HTMLElement>(".entry")].find((e) => e.textContent?.includes("empty-object"))!;
    press(file); await settle();
    click(button("Edit metadata")); await settle();
    expect(button("Edit metadata").disabled).toBe(true);
    expect(document.querySelector(".actions-row")).not.toBeNull();
    expect(document.querySelector('input[aria-label="metadata key"], .editor')).not.toBeNull();
  }, 20_000);

  // In the Logical view confirmed segment containers are loaded but hidden,
  // so a filter that matches only them says where they are.
  it("says when only hidden segment containers match the filter", async () => {
    await openFixtures();
    const filter = get<HTMLInputElement>('input[aria-label="Filter containers"]');
    filter.value = "external-dlo-segments"; filter.dispatchEvent(new Event("input", { bubbles: true })); flushSync(); await settle();
    expect(document.body.textContent).toContain(`Only segment containers hidden in this view hold "external-dlo-segments"`);
    expect(document.body.textContent).not.toContain("No loaded container's name holds");
  }, 20_000);
});

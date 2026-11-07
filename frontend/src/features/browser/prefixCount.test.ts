import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api } from "../../lib/api";
import { newFakeApi } from "./fakeApi";
import BrowserScreen from "./BrowserScreen.svelte";

// ADR 0072: counting the objects under the open prefix is an explicit,
// stoppable, count-only scan whose result belongs to the prefix it was
// taken for; the download dialog states the selection ceiling before
// Start; the jobs drawer carries the ceiling as a setting.

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
function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label);
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

describe("prefix count", () => {
  it("counts the objects under the open prefix on request and shows when the count was taken", async () => {
    await openFixtures();
    expect(document.querySelector(".count-status")).toBeNull();
    const expected = (await api.objects("bm-1", "fixtures", "", "", 5000)).rows.filter((r) => r.kind === "object").length;
    const all = await api.startFind("fixtures", "", "", "bm-1", true);
    const total = (await api.findStatus(all, "bm-1")).scanned;
    click(button("Count objects")); await settle();
    const status = get(".count-status");
    expect(status.textContent).toContain(`${total} objects under`);
    expect(status.textContent).toContain("counted 20");
    expect(total).toBeGreaterThanOrEqual(expected);
    expect(button("Count objects")).toBeTruthy();
  }, 20_000);

  it("keeps a count with the prefix it was taken for", async () => {
    await openFixtures();
    click(button("Count objects")); await settle();
    expect(document.querySelector(".count-status")).not.toBeNull();
    const prefix = [...document.querySelectorAll<HTMLElement>(".entry")].find((e) => e.textContent?.includes("/"));
    if (prefix) {
      prefix.dispatchEvent(new MouseEvent("dblclick", { bubbles: true })); flushSync(); await settle();
    }
    // Navigating away drops the count from view; it never labels another prefix.
    const input = get<HTMLInputElement>('input[placeholder="server-side prefix…"]');
    input.value = "photos/"; input.dispatchEvent(new Event("change", { bubbles: true })); flushSync(); await settle();
    expect(document.querySelector(".count-status")).toBeNull();
  }, 20_000);

  it("states the download ceiling before Start", async () => {
    await openFixtures();
    click(button("Download…")); await settle();
    const hint = [...document.querySelectorAll<HTMLElement>(".hint")].find((h) => h.textContent?.includes("objects are refused"));
    expect(hint?.textContent).toContain("250,000 objects are refused");
  }, 20_000);

  it("saves the ceiling from the jobs drawer and reports a refused value", async () => {
    await openFixtures();
    const saves: number[] = [];
    const save = api.saveAppSettings;
    api.saveAppSettings = async (settings) => {
      saves.push(settings.downloadObjectCeiling);
      if (settings.downloadObjectCeiling < 1000) throw new Error("download object ceiling 10 out of range 1000..5000000");
      return save(settings);
    };
    click(get("button.bar")); await settle();
    click(button("Refresh settings")); await settle();
    const input = get<HTMLInputElement>("input.ceiling");
    input.value = "12000"; input.dispatchEvent(new Event("change", { bubbles: true })); flushSync(); await settle();
    expect(saves).toEqual([12000]);
    expect(get(".ceiling-status").textContent).toContain("ceiling 12,000 objects");
    input.value = "10"; input.dispatchEvent(new Event("change", { bubbles: true })); flushSync(); await settle();
    expect(get(".ceiling-status").textContent).toContain("out of range");
  }, 20_000);
});

import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import BrowserScreen from "./BrowserScreen.svelte";
import ContainerColumn from "./ContainerColumn.svelte";

// The container list shows what its last read returned. A change made
// elsewhere, by another client or another tool of the operator's, reaches it
// when the operator presses the column's Refresh or comes back to the window
// after a while.

const api = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: api }));

const bookmark = { id: "", displayName: "Refresh fixture", authUrl: "https://example.test/auth",
  username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false };

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of [...mounted].reverse()) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

async function settle() {
  for (let i = 0; i < 16; i++) { await Promise.resolve(); flushSync(); }
}
function rows(root: ParentNode = document): string[] {
  return [...root.querySelectorAll<HTMLElement>(".container-row .container-name")]
    .map((name) => name.childNodes[0]?.textContent?.trim() ?? "");
}

// A store whose "fixtures" container another client deletes when told to.
function store() {
  const fake = newFakeApi();
  const elsewhere = { deleted: false };
  const containers = fake.containers;
  fake.containers = vi.fn(async (...args: Parameters<Api["containers"]>) => {
    const page = await containers(...args);
    return elsewhere.deleted ? { ...page, rows: page.rows.filter((row) => row.name !== "fixtures") } : page;
  });
  return { fake, elsewhere };
}

async function connected() {
  const s = store();
  const browser = new BrowserState(s.fake);
  await browser.connect((await browser.saveBookmark(bookmark, "test-only")).id);
  return { ...s, browser };
}

describe("the container list", () => {
  it("is read again when the window is focused more than half a minute after its last read", async () => {
    const clock = vi.spyOn(Date, "now").mockReturnValue(1_000_000);
    const s = store();
    Object.assign(api, s.fake);
    await api.saveBookmark(bookmark, "test-only");
    mounted.add(mount(BrowserScreen, { target: document.body }));
    flushSync(); await settle();
    document.querySelector<HTMLElement>(`[aria-label="${bookmark.displayName}"]`)!.click();
    flushSync(); await settle();
    expect(rows()).toContain("fixtures");

    s.elsewhere.deleted = true;
    clock.mockReturnValue(1_000_000 + 29_000);
    window.dispatchEvent(new Event("focus")); await settle();
    expect(rows()).toContain("fixtures");

    clock.mockReturnValue(1_000_000 + 31_000);
    window.dispatchEvent(new Event("focus")); await settle();
    expect(rows()).not.toContain("fixtures");
    // The other containers are still listed: the read succeeded.
    expect(document.body.textContent).toContain("Possible backstage · 2");
  });

  it("is left alone on focus once the operator has loaded more than its first page", async () => {
    const clock = vi.spyOn(Date, "now").mockReturnValue(1_000_000);
    const f = await connected();
    // A first page of a thousand rows with more to come, then one more row.
    const scope = { ...f.browser.account!.scope };
    const row = (name: string) => ({ name, count: 0, bytes: 0, class: "normal", settings: null });
    const first = Array.from({ length: 1000 }, (_, i) => row(`c${String(i).padStart(4, "0")}`));
    const list = vi.fn(async (_bookmarkId: string, marker: string) => marker === ""
      ? { rows: first, nextMarker: "c0999", effectiveLimit: 1000, scope }
      : { rows: [row("c1000")], nextMarker: "", effectiveLimit: 1000, scope });
    f.fake.containers = list;
    await f.browser.loadContainers();
    await f.browser.loadMoreContainers();
    expect(f.browser.containers).toHaveLength(1001);
    list.mockClear();

    clock.mockReturnValue(1_000_000 + 60_000);
    await f.browser.refreshContainersOnReturn();
    expect(list).not.toHaveBeenCalled();
    expect(f.browser.containers).toHaveLength(1001);
  });

  it("is re-read by the column's Refresh, with its rows on screen until the answer replaces them", async () => {
    const f = await connected();
    const root = document.createElement("div");
    document.body.append(root);
    mounted.add(mount(ContainerColumn, { target: root, props: { browser: f.browser, onCreate: vi.fn() } }));
    flushSync(); await settle();
    const refresh = root.querySelector<HTMLButtonElement>('[aria-label="Refresh containers"]')!;
    expect(rows(root)).toContain("fixtures");

    f.elsewhere.deleted = true;
    let answer!: () => void;
    const held = new Promise<void>((resolve) => { answer = resolve; });
    const listing = f.fake.containers;
    f.fake.containers = async (...args) => { await held; return listing(...args); };
    refresh.click(); flushSync(); await settle();
    expect(refresh.disabled).toBe(true);
    expect(rows(root)).toContain("fixtures");
    expect(root.textContent).not.toContain("Listing…");

    answer(); await settle();
    expect(refresh.disabled).toBe(false);
    expect(rows(root)).not.toContain("fixtures");
    expect(root.textContent).toContain("Possible backstage · 2");
  });
});

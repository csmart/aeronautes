import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { ListingRowDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import WorkingList from "./WorkingList.svelte";

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

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

async function fixture(prefix = "") {
  const api = newFakeApi();
  const browser = new BrowserState(api);
  const bookmark = await browser.saveBookmark({
    id: "", displayName: "Mock", authUrl: "http://mock/auth/v1.0",
    username: "test:tester", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system",
    caPem: "", hasCa: false, hasCredential: false,
  }, "testing");
  await browser.connect(bookmark.id);
  await browser.openContainer("fixtures");
  const template = browser.entries.find((row) => row.name === "empty-object")!;
  const detail = await api.objectDetail("fixtures", "empty-object", bookmark.id);
  if (prefix !== "") await browser.goTo(prefix);
  const target = document.createElement("div");
  document.body.append(target);
  mounted.add(mount(WorkingList, { target, props: { browser } }));
  await settle();
  const row = (name: string, kind = "object"): ListingRowDTO => ({ ...template, name, kind });
  const enter = async (value: string) => {
    const input = target.querySelector<HTMLInputElement>('input[placeholder="folder path, e.g. logs/2026/"]')!;
    input.value = value;
    input.dispatchEvent(new Event("change", { bubbles: true }));
    await settle();
    return input;
  };
  const scope = { ...browser.account!.scope };
  return { api, browser, scope, target, row, detail, enter };
}

describe("literal browser prefixes", () => {
  it("loads the component styles that preserve object-name whitespace", async () => {
    const f = await fixture();
    expect(getComputedStyle(f.target.querySelector(".name")!).whiteSpace).toBe("pre");
  });

  it.each([
    "", "external", " external", "external ", "  ", "literal%2F",
    "  scope //日本語 %2F", "  scope //日本語 %2F/", "/leading", "/", "repeated//",
  ])("sends the typed prefix %j unchanged", async (prefix) => {
    const f = await fixture("normal/");
    const listing = vi.spyOn(f.api, "objects");
    const input = await f.enter(prefix);
    expect(listing).toHaveBeenCalledExactlyOnceWith(f.browser.connectedBookmarkId!, "fixtures", prefix, "", 1000);
    expect(f.browser.prefix).toBe(prefix);
    expect(input.value).toBe(prefix);
  });

  it.each([
    { prefix: "external", name: "external", label: "external", kind: "object" },
    { prefix: "external", name: "external-dlo", label: "external-dlo", kind: "object" },
    { prefix: "normal/he", name: "normal/hello.txt", label: "hello.txt", kind: "object" },
    { prefix: "  scope //雪 ", name: "  scope //雪 name  ", label: "雪 name  ", kind: "object" },
    { prefix: "  scope //", name: "  scope //雪 name  ", label: "雪 name  ", kind: "object" },
    { prefix: "literal%2", name: "literal%2F/", label: "literal%2F/", kind: "prefix" },
  ])("keeps the complete leaf label for $prefix → $name", async ({ prefix, name, label, kind }) => {
    const f = await fixture();
    f.api.objects = vi.fn().mockResolvedValue({ rows: [f.row(name, kind)], nextMarker: "", effectiveLimit: 1000, scope: f.scope });
    f.api.objectDetail = vi.fn().mockResolvedValue({ ...f.detail, name });
    await f.browser.goTo(prefix);
    await settle();
    expect(f.target.querySelector(".name")?.textContent).toBe(label);
    expect(f.browser.entries[0].name).toBe(name);
    f.target.querySelector<HTMLElement>('[role="option"]')!.dispatchEvent(new Event("pointerdown", { bubbles: true }));
    await settle();
    expect(f.browser.detailIdentity).toEqual({ container: "fixtures", name });
    expect(f.browser.selectedEntry?.name).toBe(name);
    if (kind === "object") expect(f.api.objectDetail).toHaveBeenCalledExactlyOnceWith("fixtures", name, f.browser.connectedBookmarkId);
  });

  it("separates the SLO badge from the exact object label", async () => {
    const f = await fixture();
    const name = "  scope //雪 name  ";
    f.api.objects = vi.fn().mockResolvedValue({ rows: [{ ...f.row(name), sloListed: true }], nextMarker: "", effectiveLimit: 1000, scope: f.scope });
    await f.browser.goTo("  scope //雪");
    await settle();
    expect(f.target.querySelector(".name")?.textContent).toBe("雪 name   SLO");
    expect(f.target.querySelector(".badge")?.textContent).toBe("SLO");
    expect(f.browser.entries[0].name).toBe(name);
  });

  it.each(["external", "  ", "  scope //雪 %2F ", '<img src=x onerror="broken()">'])
    ("shows %j as a literal final filter without a folder button", async (prefix) => {
      const f = await fixture(prefix);
      const partial = prefix.slice(prefix.lastIndexOf("/") + 1);
      const filter = f.target.querySelector(".prefix-filter");
      expect(filter?.textContent).toBe(`Prefix: ${JSON.stringify(partial)}`);
      // Spaces are kept as typed, on the path's one line (ADR 0123).
      expect(getComputedStyle(filter!).whiteSpace).toBe("pre");
      expect(filter?.closest("button")).toBeNull();
      expect(f.target.querySelector(".breadcrumb img")).toBeNull();
      expect([...f.target.querySelectorAll(".breadcrumb button")].map((button) => button.textContent))
        .not.toContain(partial + "/");
    });

  it("keeps spaced and empty directory components navigable", async () => {
    const f = await fixture("  scope //雪");
    const listing = vi.spyOn(f.api, "objects");
    const crumbs = f.target.querySelectorAll<HTMLButtonElement>(".breadcrumb button");
    expect([...crumbs].map((button) => button.textContent)).toEqual(["↑", "fixtures", "  scope ", "␣"]);
    expect(getComputedStyle(crumbs[2]).whiteSpace).toBe("pre");
    crumbs[3].click();
    await settle();
    expect(listing).toHaveBeenLastCalledWith(f.browser.connectedBookmarkId!, "fixtures", "  scope //", "", 1000);
    expect(f.target.querySelector(".prefix-filter")).toBeNull();
    f.target.querySelector<HTMLButtonElement>('[aria-label="Up one level"]')!.click();
    await settle();
    expect(listing).toHaveBeenLastCalledWith(f.browser.connectedBookmarkId!, "fixtures", "  scope /", "", 1000);
  });

  it("uses exact folder activation and Up without turning a filter into a folder", async () => {
    const f = await fixture();
    f.api.objects = vi.fn().mockResolvedValue({ rows: [f.row("  scope //雪/", "prefix")], nextMarker: "", effectiveLimit: 1000, scope: f.scope });
    await f.enter("  scope //雪");
    f.target.querySelector<HTMLElement>('[role="option"]')!.dispatchEvent(new MouseEvent("dblclick", { bubbles: true }));
    await settle();
    expect(f.api.objects).toHaveBeenLastCalledWith(f.browser.connectedBookmarkId!, "fixtures", "  scope //雪/", "", 1000);
    const up = f.target.querySelector<HTMLButtonElement>('[aria-label="Up one level"]')!;
    up.click();
    await settle();
    expect(f.api.objects).toHaveBeenLastCalledWith(f.browser.connectedBookmarkId!, "fixtures", "  scope //", "", 1000);
    await f.enter("  scope //雪");
    up.click();
    await settle();
    expect(f.api.objects).toHaveBeenLastCalledWith(f.browser.connectedBookmarkId!, "fixtures", "  scope //", "", 1000);
    f.target.querySelector<HTMLButtonElement>(".crumb.root")!.click();
    await settle();
    expect(f.api.objects).toHaveBeenLastCalledWith(f.browser.connectedBookmarkId!, "fixtures", "", "", 1000);
    up.click();
    await settle();
    expect(f.browser.containerName).toBeNull();
  });

  it("retains the exact prefix, marker and selection when loading another page", async () => {
    const f = await fixture();
    const prefix = "  scope //雪";
    const marker = "  scope //雪 %2F  ";
    const first = f.row(marker);
    const second = f.row("  scope //雪 next");
    f.api.objects = vi.fn().mockResolvedValueOnce({ rows: [first], nextMarker: marker, effectiveLimit: 1000, scope: f.scope })
      .mockResolvedValue({ rows: [first, second], nextMarker: "", effectiveLimit: 1000, scope: f.scope });
    f.api.objectDetail = vi.fn().mockResolvedValue({ ...f.detail, name: marker });
    await f.enter(prefix);
    f.browser.select(0);
    await settle();
    const identity = f.browser.detailIdentity;
    const detail = f.browser.objectDetail;
    f.target.querySelector<HTMLButtonElement>(".more")!.click();
    await settle();
    expect(f.api.objects).toHaveBeenLastCalledWith(f.browser.connectedBookmarkId!, "fixtures", prefix, marker, 1000);
    expect(f.browser.entries.map((row) => row.name)).toEqual([marker, second.name]);
    expect(f.browser.selectedEntry?.name).toBe(marker);
    expect(f.browser.detailIdentity).toBe(identity);
    expect(f.browser.objectDetail).toBe(detail);
  });

  it.each(["success", "failure"])("retires pending pages and details across literal prefix changes on %s", async (outcome) => {
    const f = await fixture();
    const page = deferred<void>();
    const detail = deferred<void>();
    const oldName = " old name ";
    f.api.objects = vi.fn(async (_bookmarkId, _container, prefix, marker) => {
      if (marker !== "") {
        await page.promise;
        return { rows: [f.row("obsolete")], nextMarker: "", effectiveLimit: 1000, scope: f.scope };
      }
      return { rows: [f.row(prefix === " old" ? oldName : "new name")], nextMarker: "next", effectiveLimit: 1000, scope: f.scope };
    });
    f.api.objectDetail = vi.fn(async () => {
      await detail.promise;
      return { ...f.detail, name: oldName };
    });
    await f.enter(" old");
    f.browser.select(0);
    const pending = f.browser.loadMoreEntries();
    expect(f.browser.selectedEntry?.name).toBe(oldName);
    expect(f.browser.detailLoading).toBe(true);
    expect(f.api.objectDetail).toHaveBeenCalledExactlyOnceWith("fixtures", oldName, f.browser.connectedBookmarkId);
    expect(f.browser.entriesMoreLoading).toBe(true);
    f.browser.filter = "old";
    await f.enter("new");
    expect(f.browser.filter).toBe("");
    expect(f.browser.selectedEntry).toBeNull();
    expect(f.browser.detailIdentity).toBeNull();
    if (outcome === "success") { page.resolve(); detail.resolve(); }
    else { page.reject(new Error("obsolete page")); detail.reject(new Error("obsolete detail")); }
    await pending;
    await settle();
    expect(f.browser.prefix).toBe("new");
    expect(f.browser.entries.map((row) => row.name)).toEqual(["new name"]);
    expect(f.browser.entriesMoreError).toBeNull();
    expect(f.browser.objectDetail).toBeNull();
    expect(f.browser.detailError).toBeNull();
    expect(f.browser.detailLoading).toBe(false);
  });
});

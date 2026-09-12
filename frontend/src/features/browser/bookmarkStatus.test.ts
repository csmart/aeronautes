import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, BookmarkDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import BookmarkEditor from "./BookmarkEditor.svelte";
import BrowserScreen from "./BrowserScreen.svelte";
import BookmarkRail from "./BookmarkRail.svelte";
import { SvelteMap } from "svelte/reactivity";

const live = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: live }));

const mounted = new Set<ReturnType<typeof mount>>();
const releases: (() => void)[] = [];

beforeEach(() => Object.assign(live, newFakeApi()));
afterEach(async () => {
  for (const component of [...mounted].reverse()) await unmount(component);
  mounted.clear();
  for (const release of releases.splice(0)) release();
  await settle();
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

function draft(displayName = "Created λ"): BookmarkDTO {
  return { id: "", displayName, authUrl: "https://swift.example/auth/v1.0",
    username: "fixture:user", storageUrlOverride: "", tlsMode: "system",
    caPem: "", hasCa: false, hasCredential: false };
}

function heldList() {
  let resolve!: (value: BookmarkDTO[]) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<BookmarkDTO[]>((yes, no) => { resolve = yes; reject = no; });
  releases.push(() => resolve([]));
  return { promise, resolve, reject };
}

async function settle() {
  for (let i = 0; i < 16; i++) { await Promise.resolve(); flushSync(); }
}

function showEditor(browser: BrowserState, onClosed = () => {}) {
  const closed = vi.fn(onClosed);
  const component = mount(BookmarkEditor, {
    target: document.body, props: { browser, existing: null, onClose: closed },
  });
  mounted.add(component);
  flushSync();
  fillEditor();
  return closed;
}

function field(selector: string, value: string) {
  const input = document.querySelector<HTMLInputElement>(selector);
  expect(input, selector).not.toBeNull();
  input!.value = value;
  input!.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}

function fillEditor() {
  field('input[placeholder="Prod AU"]', "Created λ");
  field('input[placeholder="https://swift.example/auth/v1.0"]', "https://swift.example/auth/v1.0");
  field('input[placeholder="account:user"]', "fixture:user");
  field('input[type="password"]', "test-only-fixture-key");
}

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")]
    .find((node) => node.textContent?.trim() === label);
  expect(found, `button ${label}`).toBeDefined();
  return found!;
}

function showScreen() {
  const component = mount(BrowserScreen, { target: document.body });
  mounted.add(component);
  flushSync();
}

function railNames(): string[] {
  return [...document.querySelectorAll('.bookmark-row .label')]
    .map((node) => node.textContent ?? "");
}

it("healthy bookmark creation returns its acknowledged identity", async () => {
  const api = newFakeApi();
  api.saveBookmark = vi.fn(api.saveBookmark);
  const state = new BrowserState(api);
  const saved = await state.saveBookmark(draft(), "test-only-fixture-key");
  expect(saved.id).not.toBe("");
  expect(state.bookmarks.map((bookmark) => bookmark.id)).toEqual([saved.id]);
  expect(api.saveBookmark).toHaveBeenCalledOnce();
});

it.each(["new", "existing"])("returns the acknowledged %s save after its list refresh fails", async (mode) => {
  const api = newFakeApi();
  const originalSave = api.saveBookmark;
  const originalList = api.listBookmarks;
  const target = mode === "existing" ? await originalSave(draft(), "test-only-fixture-key") : draft();
  const state = new BrowserState(api);
  await state.loadBookmarks();
  let accepted: BookmarkDTO | undefined;
  api.saveBookmark = vi.fn(async (bookmark, secret) => {
    accepted = await originalSave(bookmark, secret);
    return accepted;
  });
  api.listBookmarks = vi.fn().mockRejectedValue(new Error("fixture list observation unavailable"));
  const result = await state.saveBookmark(target, "test-only-fixture-key")
    .then((value) => ({ value }), (error: Error) => ({ error }));
  console.log("acknowledged-save-read-failure", JSON.stringify({ mode,
    acceptedId: accepted?.id, storedIds: (await originalList()).map((bookmark) => bookmark.id),
    result: "error" in result ? result.error.message : result.value.id }));
  expect(api.saveBookmark).toHaveBeenCalledOnce();
  expect(api.listBookmarks).toHaveBeenCalledOnce();
  expect(result).toEqual({ value: accepted });
});

it("keeps the existing save await until its list observation settles", async () => {
  const api = newFakeApi();
  const read = heldList();
  const originalList = api.listBookmarks;
  api.listBookmarks = vi.fn(() => read.promise);
  const state = new BrowserState(api);
  let finished = false;
  const pending = state.saveBookmark(draft(), "test-only-fixture-key").then((value) => {
    finished = true;
    return value;
  });
  await settle();
  expect(api.listBookmarks).toHaveBeenCalledOnce();
  expect(finished).toBe(false);
  const rows = await originalList();
  read.resolve(rows);
  const saved = await pending;
  expect(saved.id).toBe(rows[0].id);
  expect(finished).toBe(true);
});

it("preserves an actual save rejection without calling List", async () => {
  const api = newFakeApi();
  const failure = new Error("fixture save refused");
  api.saveBookmark = vi.fn().mockRejectedValue(failure);
  api.listBookmarks = vi.fn(api.listBookmarks);
  const state = new BrowserState(api);
  await expect(state.saveBookmark(draft(), "test-only-fixture-key")).rejects.toBe(failure);
  expect(api.saveBookmark).toHaveBeenCalledOnce();
  expect(api.listBookmarks).not.toHaveBeenCalled();
  expect(state.bookmarks).toEqual([]);
});

it("does not invent an acknowledged identity when Save reports an uncertain effect", async () => {
  const api = newFakeApi();
  const originalSave = api.saveBookmark;
  const originalList = api.listBookmarks;
  const uncertain = new Error("fixture bookmark publication unresolved");
  api.saveBookmark = vi.fn(async (bookmark, secret) => {
    await originalSave(bookmark, secret);
    throw uncertain;
  });
  api.listBookmarks = vi.fn(originalList);
  const state = new BrowserState(api);
  await expect(state.saveBookmark(draft(), "test-only-fixture-key")).rejects.toBe(uncertain);
  expect(await originalList()).toHaveLength(1);
  expect(api.saveBookmark).toHaveBeenCalledOnce();
  expect(api.listBookmarks).not.toHaveBeenCalled();
  expect(state.bookmarks).toEqual([]);
});

it("does not repeat empty-id creation after an acknowledged Save and failed List", async () => {
  const api = newFakeApi();
  const originalList = api.listBookmarks;
  api.listBookmarks = vi.fn().mockRejectedValueOnce(new Error("fixture list observation unavailable"))
    .mockImplementation(originalList);
  api.saveBookmark = vi.fn(api.saveBookmark);
  const state = new BrowserState(api);
  const closed = showEditor(state);
  button("Save").click();
  await settle();
  const firstRows = await originalList();
  expect(firstRows).toHaveLength(1);
  const first = { title: document.querySelector('[role="dialog"]')?.getAttribute("aria-label"),
    alert: document.querySelector('[role="alert"]')?.textContent,
    enabledRetry: !button("Save").disabled, storedId: firstRows[0].id };
  button("Save").click();
  await settle();
  const rows = await originalList();
  console.log("bookmark-create-retry", JSON.stringify({ first,
    saveArgumentIds: vi.mocked(api.saveBookmark).mock.calls.map(([bookmark]) => bookmark.id),
    storedIds: rows.map((bookmark) => bookmark.id), closeCalls: closed.mock.calls.length }));
  expect(rows.map((bookmark) => bookmark.id)).toEqual([firstRows[0].id]);
  expect(api.saveBookmark).toHaveBeenCalledOnce();
});

it("shows initial bookmark List failure as unavailable instead of an empty rail", async () => {
  live.listBookmarks = vi.fn().mockRejectedValue(new Error("fixture private list diagnostic"));
  showScreen();
  await settle();
  expect(live.listBookmarks).toHaveBeenCalledOnce();
  expect(railNames()).toEqual([]);
  console.log("bookmark-startup-read-failure", JSON.stringify({ rail: document.querySelector('nav[aria-label="Bookmarks"]')?.textContent,
    alerts: [...document.querySelectorAll('[role="alert"]')].map((node) => node.textContent) }));
  expect(document.body.textContent).not.toContain("fixture private list diagnostic");
  expect(document.body.textContent).toMatch(/bookmarks?.*unavailable/i);
});

it("does not let a late startup List erase a newly saved bookmark", async () => {
  const read = heldList();
  const originalList = live.listBookmarks;
  live.listBookmarks = vi.fn().mockReturnValueOnce(read.promise).mockImplementation(originalList);
  live.saveBookmark = vi.fn(live.saveBookmark);
  showScreen();
  await settle();
  expect(live.listBookmarks).toHaveBeenCalledOnce();
  document.querySelector<HTMLButtonElement>('[title="Add bookmark"]')!.click();
  flushSync();
  fillEditor();
  button("Save").click();
  await settle();
  expect(railNames()).toEqual(["Created λ"]);
  read.resolve([]);
  await settle();
  console.log("bookmark-late-startup-read", JSON.stringify({ railNames: railNames(),
    storedIds: (await originalList()).map((bookmark) => bookmark.id),
    listCalls: vi.mocked(live.listBookmarks).mock.calls.length }));
  expect(railNames()).toEqual(["Created λ"]);
});

it("does not let an older post-save List restore a successfully deleted bookmark", async () => {
  const api = newFakeApi();
  const first = await api.saveBookmark(draft("first"), "test-only-fixture-key");
  const second = await api.saveBookmark(draft("second"), "test-only-fixture-key");
  const state = new BrowserState(api);
  await state.loadBookmarks();
  const originalList = api.listBookmarks;
  const read = heldList();
  api.listBookmarks = vi.fn(() => read.promise);
  const saving = state.saveBookmark({ ...first, displayName: "updated first" }, "");
  await settle();
  const beforeDelete = await originalList();
  await state.deleteBookmark(second.id);
  expect(state.bookmarks.map((bookmark) => bookmark.id)).toEqual([first.id]);
  read.resolve(beforeDelete);
  await saving;
  console.log("bookmark-late-save-list-after-delete", JSON.stringify({
    displayedIds: state.bookmarks.map((bookmark) => bookmark.id),
    storedIds: (await originalList()).map((bookmark) => bookmark.id) }));
  expect(state.bookmarks.map((bookmark) => bookmark.id)).toEqual([first.id]);
});

function heldMutation() {
  let resolve!: () => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<void>((yes, no) => { resolve = yes; reject = no; });
  releases.push(resolve);
  return { promise, resolve, reject };
}

function showRail(state: BrowserState) {
  const component = mount(BookmarkRail, { target: document.body,
    props: { browser: state, onAddBookmark: vi.fn(), onEditBookmark: vi.fn() } });
  mounted.add(component);
  flushSync();
}

function refreshList(): HTMLButtonElement {
  const found = document.querySelector<HTMLButtonElement>('[aria-label="Refresh bookmark list"]');
  expect(found).not.toBeNull();
  return found!;
}

describe("bookmark list status", () => {
  it("distinguishes not loaded, initial loading and successful empty", async () => {
    const api = newFakeApi();
    const read = heldList();
    api.listBookmarks = vi.fn(() => read.promise);
    const state = new BrowserState(api);
    showRail(state);
    expect(document.body.textContent).toContain("Bookmarks not loaded");
    expect(document.body.textContent).not.toContain("No bookmarks saved");
    expect(state.bookmarksLoaded).toBe(false);
    refreshList().click();
    refreshList().click();
    expect(api.listBookmarks).toHaveBeenCalledOnce();
    flushSync();
    expect(state.bookmarksLoading).toBe(true);
    expect(refreshList().disabled).toBe(true);
    expect(document.body.textContent).toContain("Loading bookmarks");
    expect(document.body.textContent).not.toContain("No bookmarks saved");
    refreshList().click();
    expect(api.listBookmarks).toHaveBeenCalledOnce();
    read.resolve([]);
    await settle();
    expect(state.bookmarksLoaded).toBe(true);
    expect(state.bookmarksLoading).toBe(false);
    expect(state.bookmarksStale).toBe(false);
    expect(state.bookmarksError).toBeNull();
    expect(document.body.textContent).toContain("No bookmarks saved");
  });

  it("reports safe initial failure and retries only List", async () => {
    const api = newFakeApi();
    const privateError = new Error("private-key=fixture; /private/bookmarks.sqlite");
    api.listBookmarks = vi.fn().mockRejectedValueOnce(privateError).mockResolvedValueOnce([]);
    api.saveBookmark = vi.fn(api.saveBookmark);
    api.deleteBookmark = vi.fn(api.deleteBookmark);
    api.connect = vi.fn(api.connect);
    const state = new BrowserState(api);
    showRail(state);
    await expect(state.loadBookmarks()).resolves.toBeUndefined();
    flushSync();
    expect(state.bookmarksLoaded).toBe(false);
    expect(state.bookmarksLoading).toBe(false);
    expect(document.querySelector('[role="alert"]')?.textContent).toContain("Bookmarks unavailable");
    expect(document.body.textContent).not.toContain(privateError.message);
    expect(document.body.textContent).not.toContain("No bookmarks saved");
    refreshList().click();
    await settle();
    expect(api.listBookmarks).toHaveBeenCalledTimes(2);
    expect(api.saveBookmark).not.toHaveBeenCalled();
    expect(api.deleteBookmark).not.toHaveBeenCalled();
    expect(api.connect).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("No bookmarks saved");
    expect(document.querySelector('[role="alert"]')).toBeNull();
  });

  it("does not present a retired empty observation as a current empty list", async () => {
    const api = newFakeApi();
    const state = new BrowserState(api);
    await state.loadBookmarks();
    showRail(state);
    expect(document.body.textContent).toContain("No bookmarks saved");
    await expect(state.deleteBookmark("not-in-the-prior-list")).rejects.toThrow("bookmark not found");
    flushSync();
    expect(document.body.textContent).not.toContain("No bookmarks saved");
    expect(document.body.textContent).toContain("Previously loaded list may be out of date");
    expect(refreshList().disabled).toBe(false);
    refreshList().click();
    await settle();
    expect(document.body.textContent).toContain("No bookmarks saved");
  });

  it.each(["empty", "nonempty"])("marks prior %s observations stale during a failed refresh", async (kind) => {
    const api = newFakeApi();
    const rows = kind === "empty" ? [] : [{ ...draft("Previously loaded"), id: "saved" }];
    const read = heldList();
    api.listBookmarks = vi.fn().mockResolvedValueOnce(rows).mockReturnValueOnce(read.promise);
    const state = new BrowserState(api);
    await state.loadBookmarks();
    showRail(state);
    const pending = state.loadBookmarks().catch((error: Error) => error);
    flushSync();
    expect(state.bookmarks).toEqual(rows);
    expect(state.bookmarksStale).toBe(true);
    expect(document.body.textContent).toContain("Previously loaded");
    expect(document.body.textContent).not.toContain("No bookmarks saved");
    read.reject(new Error("private refresh diagnostic"));
    await pending;
    flushSync();
    expect(state.bookmarksLoaded).toBe(true);
    expect(state.bookmarks).toEqual(rows);
    expect(state.bookmarksStale).toBe(true);
    expect(document.querySelector('[role="alert"]')?.textContent).toContain("Bookmarks unavailable");
    expect(document.body.textContent).not.toContain("No bookmarks saved");
    expect(document.body.textContent).not.toContain("private refresh diagnostic");
    expect(refreshList().disabled).toBe(false);
  });

  it.each(["success", "failure"])("ignores an older %s while a newer List is pending", async (outcome) => {
    const api = newFakeApi();
    const old = heldList();
    const current = heldList();
    api.listBookmarks = vi.fn().mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise);
    const state = new BrowserState(api);
    const first = state.loadBookmarks().catch((error: Error) => error);
    const second = state.loadBookmarks();
    if (outcome === "success") old.resolve([{ ...draft("obsolete"), id: "old" }]);
    else old.reject(new Error("obsolete error"));
    await first;
    expect(state.bookmarks).toEqual([]);
    expect(state.bookmarksLoaded).toBe(false);
    expect(state.bookmarksError).toBeNull();
    expect(state.bookmarksLoading).toBe(true);
    const rows = [{ ...draft("current"), id: "new" }];
    current.resolve(rows);
    await second;
    expect(state.bookmarks).toEqual(rows);
    expect(state.bookmarksLoading).toBe(false);
  });

  describe.each(["success", "failure"])("newer List %s", (currentOutcome) => {
    it.each(["success", "failure"])("survives older %s without stale publication", async (oldOutcome) => {
      const api = newFakeApi();
      const old = heldList();
      const rows = [{ ...draft("current"), id: "new" }];
      api.listBookmarks = vi.fn().mockReturnValueOnce(old.promise)
        .mockImplementationOnce(async () => {
          if (currentOutcome === "failure") throw new Error("current read failed");
          return rows;
        });
      const state = new BrowserState(api);
      const first = state.loadBookmarks().catch((error: Error) => error);
      await state.loadBookmarks().catch((error: Error) => error);
      const currentError = state.bookmarksError;
      expect(Boolean(currentError)).toBe(currentOutcome === "failure");
      if (oldOutcome === "success") old.resolve([{ ...draft("obsolete"), id: "old" }]);
      else old.reject(new Error("obsolete read failed"));
      await first;
      expect(state.bookmarks).toEqual(currentOutcome === "success" ? rows : []);
      expect(state.bookmarksLoaded).toBe(currentOutcome === "success");
      expect(state.bookmarksError).toBe(currentError);
      expect(state.bookmarksLoading).toBe(false);
    });
  });
});

describe.each(["save", "delete"] as const)("bookmark %s observation boundaries", (operation) => {
  it.each(["success", "failure"])("retires older List %s at mutation admission", async (readOutcome) => {
    const api = newFakeApi();
    const saved = await api.saveBookmark(draft(), "fixture");
    const state = new BrowserState(api);
    await state.loadBookmarks();
    const old = heldList();
    const effect = heldMutation();
    const originalSave = api.saveBookmark;
    const originalDelete = api.deleteBookmark;
    api.saveBookmark = vi.fn(async (bookmark: BookmarkDTO, secret: string) => { await effect.promise; return originalSave(bookmark, secret); });
    api.deleteBookmark = vi.fn(async (id: string) => { await effect.promise; return originalDelete(id); });
    api.listBookmarks = vi.fn().mockReturnValueOnce(old.promise).mockResolvedValue([]);
    const pendingRead = state.loadBookmarks().catch((error: Error) => error);
    const pendingMutation = operation === "save" ? state.saveBookmark(saved, "") : state.deleteBookmark(saved.id);
    expect(state.bookmarksLoading).toBe(false);
    expect(state.bookmarksStale).toBe(true);
    if (readOutcome === "success") old.resolve([{ ...draft("obsolete"), id: "old" }]);
    else old.reject(new Error("retired observation"));
    await pendingRead;
    expect(state.bookmarks.map((row) => row.id)).toEqual([saved.id]);
    expect(state.bookmarksError).toBeNull();
    effect.resolve();
    await pendingMutation;
  });

  describe.each(["success", "failure"])("effect %s", (effectOutcome) => {
    it.each(["success", "failure"])("retires a List begun during the effect before its late %s", async (readOutcome) => {
      const api = newFakeApi();
      const saved = await api.saveBookmark(draft(), "fixture");
      const state = new BrowserState(api);
      await state.loadBookmarks();
      const during = heldList();
      const effect = heldMutation();
      const failure = new Error("effect may be uncertain");
      const originalSave = api.saveBookmark;
      const originalDelete = api.deleteBookmark;
      const originalList = api.listBookmarks;
      api.saveBookmark = vi.fn(async (bookmark: BookmarkDTO, secret: string) => { await effect.promise; return originalSave(bookmark, secret); });
      api.deleteBookmark = vi.fn(async (id: string) => { await effect.promise; return originalDelete(id); });
      api.listBookmarks = vi.fn().mockReturnValueOnce(during.promise).mockImplementation(originalList);
      const mutation = (operation === "save"
        ? state.saveBookmark({ ...saved, displayName: "changed" }, "") : state.deleteBookmark(saved.id))
        .then((value) => ({ value }), (error: Error) => ({ error }));
      const read = state.loadBookmarks().catch((error: Error) => error);
      expect(state.bookmarksLoading).toBe(true);
      if (effectOutcome === "success") effect.resolve(); else effect.reject(failure);
      const result = await mutation;
      if (effectOutcome === "failure") expect(result).toEqual({ error: failure });
      else expect(result).toHaveProperty("value");
      const settledRows = state.bookmarks.map((row) => ({ ...row }));
      const settledError = state.bookmarksError;
      const settledStale = state.bookmarksStale;
      expect(state.bookmarksLoading).toBe(false);
      if (readOutcome === "success") during.resolve([{ ...saved, displayName: "old snapshot" }]);
      else during.reject(new Error("obsolete read error"));
      await read;
      expect(state.bookmarks).toEqual(settledRows);
      expect(state.bookmarksError).toBe(settledError);
      expect(state.bookmarksStale).toBe(settledStale);
      expect(state.bookmarksLoading).toBe(false);
    });
  });
});

it("does not invent a saved rail row after acknowledged Save and failed List", async () => {
  const api = newFakeApi();
  const storedList = api.listBookmarks;
  api.listBookmarks = vi.fn().mockRejectedValue(new Error("list failed"));
  const state = new BrowserState(api);
  const saved = await state.saveBookmark(draft(), "fixture");
  expect(saved.id).not.toBe("");
  expect((await storedList()).map((row) => row.id)).toEqual([saved.id]);
  expect(state.bookmarks).toEqual([]);
  expect(state.bookmarksLoaded).toBe(false);
  expect(state.bookmarksError).not.toBeNull();
});

it("keeps per-bookmark authority through its failed post-save List await", async () => {
  const api = newFakeApi();
  const saved = await api.saveBookmark(draft(), "fixture");
  const read = heldList();
  api.listBookmarks = vi.fn(() => read.promise);
  api.connect = vi.fn(api.connect);
  const state = new BrowserState(api);
  const saving = state.saveBookmark(saved, "");
  await settle();
  await expect(state.connect(saved.id)).rejects.toThrow("still being changed");
  expect(api.connect).not.toHaveBeenCalled();
  read.reject(new Error("observation failed"));
  expect(await saving).toEqual(saved);
  expect(await state.connect(saved.id)).toBe(true);
  expect(api.connect).toHaveBeenCalledExactlyOnceWith(saved.id);
});

it("keeps a newer List pending when an older Save wrapper finally releases", async () => {
  const api = newFakeApi();
  const old = heldList();
  const current = heldList();
  const originalList = api.listBookmarks;
  api.listBookmarks = vi.fn().mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise);
  const state = new BrowserState(api);
  const saving = state.saveBookmark(draft(), "fixture");
  await settle();
  const reading = state.loadBookmarks();
  old.resolve([]);
  const saved = await saving;
  expect(state.bookmarksLoading).toBe(true);
  current.resolve(await originalList());
  await reading;
  expect(state.bookmarks.map((row) => row.id)).toEqual([saved.id]);
  expect(state.bookmarksStale).toBe(false);
});

it.each(["save", "delete"] as const)("retires a successful during-mutation observation when %s rejects", async (operation) => {
  const api = newFakeApi();
  const saved = await api.saveBookmark(draft(), "fixture");
  const effect = heldMutation();
  api.saveBookmark = vi.fn(async () => { await effect.promise; return saved; });
  api.deleteBookmark = vi.fn(() => effect.promise);
  const state = new BrowserState(api);
  const failure = new Error("uncertain effect");
  const mutation = (operation === "save" ? state.saveBookmark(saved, "") : state.deleteBookmark(saved.id))
    .catch((error: Error) => error);
  await state.loadBookmarks();
  expect(state.bookmarksStale).toBe(false);
  effect.reject(failure);
  expect(await mutation).toBe(failure);
  expect(state.bookmarksStale).toBe(true);
  expect(state.bookmarksLoading).toBe(false);
  expect(state.bookmarks.map((row) => row.id)).toEqual([saved.id]);
});

it.each(["save", "delete"] as const)("retires a post-Save List started during another bookmark's %s", async (operation) => {
  const api = newFakeApi();
  const first = await api.saveBookmark(draft("first"), "fixture");
  const second = await api.saveBookmark(draft("second"), "fixture");
  const state = new BrowserState(api);
  await state.loadBookmarks();
  const effect = heldMutation();
  const old = heldList();
  const originalSave = api.saveBookmark;
  const originalDelete = api.deleteBookmark;
  const originalList = api.listBookmarks;
  api.saveBookmark = vi.fn(async (bookmark, secret) => {
    if (bookmark.id === second.id) await effect.promise;
    return originalSave(bookmark, secret);
  });
  api.deleteBookmark = vi.fn(async (id) => { await effect.promise; return originalDelete(id); });
  api.listBookmarks = vi.fn().mockReturnValueOnce(old.promise).mockImplementation(originalList);
  const changingSecond = operation === "save"
    ? state.saveBookmark({ ...second, displayName: "changed second" }, "") : state.deleteBookmark(second.id);
  const savingFirst = state.saveBookmark({ ...first, displayName: "changed first" }, "");
  await settle();
  const staleSnapshot = await originalList();
  expect(api.listBookmarks).toHaveBeenCalledOnce();
  effect.resolve();
  await changingSecond;
  const settledRows = state.bookmarks.map((row) => ({ ...row }));
  old.resolve(staleSnapshot);
  const savedFirst = await savingFirst;
  expect(savedFirst.id).toBe(first.id);
  expect(state.bookmarks).toEqual(settledRows);
  expect(state.bookmarks.map((row) => row.id)).toEqual(operation === "save" ? [first.id, second.id] : [first.id]);
  expect(api.listBookmarks).toHaveBeenCalledTimes(operation === "save" ? 2 : 1);
  expect(api.saveBookmark).toHaveBeenCalledTimes(operation === "save" ? 2 : 1);
  expect(api.deleteBookmark).toHaveBeenCalledTimes(operation === "save" ? 0 : 1);
});

describe("Save and connect through unavailable List", () => {
  it("retries the known saved ID after both List and Connect fail", async () => {
    const api = newFakeApi();
    const originalList = api.listBookmarks;
    api.listBookmarks = vi.fn().mockRejectedValue(new Error("private list error"));
    api.saveBookmark = vi.fn(api.saveBookmark);
    api.connect = vi.fn().mockRejectedValueOnce(new Error("connect refused")).mockImplementation(api.connect);
    const state = new BrowserState(api);
    const finished = heldMutation();
    const closed = showEditor(state, finished.resolve);
    const submit = () => document.querySelector('form')!
      .dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    submit();
    await vi.waitFor(() => expect(document.querySelector('[role="alert"]')?.textContent).toContain("connect refused"));
    const firstRows = await originalList();
    expect(firstRows).toHaveLength(1);
    expect(closed).not.toHaveBeenCalled();
    expect(document.querySelector('[role="dialog"]')?.getAttribute("aria-label")).toBe("Edit bookmark");
    submit();
    await finished.promise;
    expect(vi.mocked(api.saveBookmark).mock.calls.map(([bookmark]) => bookmark.id)).toEqual(["", firstRows[0].id]);
    expect((await originalList()).map((row) => row.id)).toEqual([firstRows[0].id]);
    expect(api.connect).toHaveBeenCalledTimes(2);
    expect(closed).toHaveBeenCalledOnce();
  });

  it("waits for List then connects the acknowledged identity without creating twice", async () => {
    const api = newFakeApi();
    const read = heldList();
    const originalList = api.listBookmarks;
    api.listBookmarks = vi.fn(() => read.promise);
    api.saveBookmark = vi.fn(api.saveBookmark);
    api.connect = vi.fn(api.connect);
    const state = new BrowserState(api);
    const finished = heldMutation();
    const closed = showEditor(state, finished.resolve);
    document.querySelector('form')!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await settle();
    expect(api.connect).not.toHaveBeenCalled();
    expect(closed).not.toHaveBeenCalled();
    expect(button("Save").disabled).toBe(true);
    read.reject(new Error("private list error"));
    await finished.promise;
    flushSync();
    const rows = await originalList();
    expect(rows).toHaveLength(1);
    expect(api.saveBookmark).toHaveBeenCalledOnce();
    expect(api.connect).toHaveBeenCalledExactlyOnceWith(rows[0].id);
    expect(state.connectedBookmarkId).toBe(rows[0].id);
    expect(closed).toHaveBeenCalledOnce();
    expect(document.body.textContent).not.toContain("private list error");
  });

  it.each(["close", "replace", "scope"])("preserves the existing %s gate after a held failed List", async (ending) => {
    const api = newFakeApi();
    const read = heldList();
    api.listBookmarks = vi.fn(() => read.promise);
    api.saveBookmark = vi.fn(api.saveBookmark);
    api.connect = vi.fn(api.connect);
    const state = new BrowserState(api);
    const props = new SvelteMap<string, BookmarkDTO | null>([["bookmark", null]]);
    const closed = vi.fn();
    const component = mount(BookmarkEditor, { target: document.body, props: { browser: state,
      get existing() { return props.get("bookmark") ?? null; }, onClose: closed } });
    mounted.add(component);
    flushSync();
    fillEditor();
    document.querySelector('form')!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await settle();
    expect(api.saveBookmark).toHaveBeenCalledOnce();
    if (ending === "close") document.querySelector<HTMLElement>('.scrim')!.click();
    else if (ending === "replace") props.set("bookmark", { ...draft("replacement"), id: "replacement" });
    else state.connectedBookmarkId = "other";
    flushSync();
    read.reject(new Error("private list error"));
    await settle();
    expect(api.connect).not.toHaveBeenCalled();
    expect(document.body.textContent).not.toContain("private list error");
    expect(closed).toHaveBeenCalledTimes(ending === "scope" ? 0 : 1);
    if (ending === "scope") {
      expect(document.body.textContent).toContain("Saved. The connection changed");
      expect(document.querySelector('[role="dialog"]')?.getAttribute("aria-label")).toBe("Edit bookmark");
    }
  });
});

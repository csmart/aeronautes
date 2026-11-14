import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, ContainerDetailDTO, ContainerSettingsDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import ContainerColumn from "./ContainerColumn.svelte";

// One fake observer per test document: a test says which rows are visible.
const watched = new Map<Element, (visible: boolean) => void>();
class FakeObserver {
  #callback: IntersectionObserverCallback;
  constructor(callback: IntersectionObserverCallback) {
    this.#callback = callback;
  }
  observe(node: Element) {
    watched.set(node, (visible) => this.#callback(
      [{ target: node, isIntersecting: visible } as IntersectionObserverEntry], this as unknown as IntersectionObserver));
  }
  unobserve(node: Element) {
    watched.delete(node);
  }
  disconnect() {}
}

const mounted = new Set<ReturnType<typeof mount>>();
beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("IntersectionObserver", FakeObserver);
});
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear();
  watched.clear();
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

async function settle() {
  for (let i = 0; i < 8; i++) { await Promise.resolve(); flushSync(); }
}

// A connected browser over the fake account, whose container reads wait for
// the test to answer them.
async function connected(names: string[]) {
  const api: Api = newFakeApi();
  const browser = new BrowserState(api);
  const bookmark = await browser.saveBookmark({ id: "", displayName: "Mock", authUrl: "https://example.test/auth",
    username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
  await browser.connect(bookmark.id);
  const generation = browser.account!.scope.generation;
  browser.containers = names.map((name) => ({ name, count: 1, bytes: 1, class: "normal", settings: null }));
  const replies: { name: string; reply: ReturnType<typeof deferred<ContainerDetailDTO>> }[] = [];
  api.containerDetail = vi.fn((name: string) => {
    const reply = deferred<ContainerDetailDTO>();
    replies.push({ name, reply });
    return reply.promise;
  });
  const root = document.createElement("div");
  document.body.append(root);
  mounted.add(mount(ContainerColumn, { target: root, props: { browser, onCreate: vi.fn() } }));
  flushSync();
  const row = (name: string) => [...root.querySelectorAll<HTMLElement>(".container-row")].find((b) => b.textContent?.includes(name))!;
  const show = (name: string, visible = true) => watched.get(row(name))!(visible);
  const settings = (name: string): ContainerSettingsDTO => ({
    publicRead: false, publicListing: false, referrerGrants: [], sharedWrite: false, staticWeb: false,
    versioningMode: "versions", versioningArchive: "", quota: false, cors: false, readAcl: "", writeAcl: "",
    observedAt: `2026-11-12T00:00:0${name.length % 10}Z`, generation,
  });
  const answer = async (index: number) => {
    const { name, reply } = replies[index];
    reply.resolve({ name, scope: { bookmarkId: bookmark.id, generation }, settings: settings(name) } as ContainerDetailDTO);
    await settle();
  };
  return { api, browser, root, row, show, answer, replies, settings, bookmark, generation };
}

// A row that stays on screen for the dwell time is read; one scrolled past
// sooner is not (ADR 0110 decision 2).
it("reads a row kept on screen and not one scrolled past", async () => {
  const f = await connected(["kept", "passed"]);
  f.show("kept");
  f.show("passed");
  vi.advanceTimersByTime(200);
  f.show("passed", false);
  vi.advanceTimersByTime(200);
  await settle();
  expect(f.replies.map((r) => r.name)).toEqual(["kept"]);
  await f.answer(0);
  expect(f.row("kept").textContent).toContain("versioned");
});

// Never more than four reads at once, and the next starts as one finishes.
it("keeps at most four reads in flight", async () => {
  const names = ["c1", "c2", "c3", "c4", "c5", "c6"];
  const f = await connected(names);
  for (const name of names) f.show(name);
  vi.advanceTimersByTime(300);
  await settle();
  expect(f.replies).toHaveLength(4);
  await f.answer(0);
  expect(f.replies).toHaveLength(5);
});

// A row is read once per bookmark generation, a failed read is not repeated,
// and a running check holds the reads of rows on screen.
it("reads a row once, does not repeat a failure, and waits for a check", async () => {
  const f = await connected(["once", "fails", "later"]);
  f.show("once");
  f.show("fails");
  vi.advanceTimersByTime(300);
  await settle();
  await f.answer(0);
  f.replies[1].reply.reject(new Error("HEAD refused"));
  await settle();
  expect(f.row("fails").textContent).toContain("not read");
  for (const name of ["once", "fails"]) {
    f.show(name, false);
    f.show(name);
  }
  vi.advanceTimersByTime(300);
  await settle();
  expect(f.replies).toHaveLength(2);

  void f.browser.checkSettings();
  await settle();
  const checkReads = f.replies.length;
  f.show("later");
  vi.advanceTimersByTime(300);
  await settle();
  expect(f.replies.length).toBe(checkReads);
});

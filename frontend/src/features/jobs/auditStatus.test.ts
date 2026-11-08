import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { AuditEventDTO } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import JobsDrawer from "./JobsDrawer.svelte";

let drawer: ReturnType<typeof mount> | null = null;
const releasePending: (() => void)[] = [];

afterEach(async () => {
  if (drawer) await unmount(drawer);
  drawer = null;
  for (const release of releasePending.splice(0)) release();
  await Promise.resolve();
  document.body.innerHTML = "";
});

function audit(id: number, operation = "copy.object"): AuditEventDTO {
  return { id, at: "", bookmarkId: "", operation, resource: `object-${id}`, summary: operation,
    result: "applied", transIds: ["tx-owned"] };
}

function page(): AuditEventDTO[] {
  return Array.from({ length: 50 }, (_, index) => audit(100 - index));
}

function deferred() {
  let resolve!: (value: AuditEventDTO[]) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<AuditEventDTO[]>((done, fail) => { resolve = done; reject = fail; });
  releasePending.push(() => resolve([]));
  return { promise, resolve, reject };
}

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")]
    .find((node) => (node.getAttribute("aria-label") ?? node.textContent?.trim()) === label);
  expect(found, `button ${label}`).toBeDefined();
  return found!;
}

function bar(): HTMLButtonElement {
  return document.querySelector<HTMLButtonElement>(".bar")!;
}

function show(browser: BrowserState) {
  drawer = mount(JobsDrawer, { target: document.body, props: { browser } });
  flushSync();
  bar().click();
  flushSync();
  button("Recent changes").click();
  flushSync();
}

function resources(): string[] {
  return [...document.querySelectorAll(".event .resource")].map((node) => node.textContent ?? "");
}

it("distinguishes a pending first query, unavailable read and successful empty retry", async () => {
  const api = newFakeApi();
  const pending = deferred();
  api.auditSearch = vi.fn().mockReturnValueOnce(pending.promise).mockResolvedValueOnce([]);
  const browser = new BrowserState(api);
  show(browser);
  expect(document.querySelector('.audit-status[role="status"]')?.textContent).toContain("Loading audit events");
  expect(document.body.textContent).not.toContain("No matching audit events");
  pending.reject(new Error("secret SQLite password token"));
  await vi.waitFor(() => expect(browser.audit.error).not.toBeNull());
  flushSync();
  expect(document.querySelector('.audit-status [role="alert"]')?.textContent)
    .toBe("Audit events are unavailable. Retry this search.");
  expect(document.body.textContent).not.toContain("secret SQLite");
  expect(document.body.textContent).not.toContain("No matching audit events");
  button("Retry audit search").click();
  await vi.waitFor(() => expect(browser.audit.loaded).toBe(true));
  flushSync();
  expect(document.body.textContent).toContain("No matching audit events");
  expect(document.querySelector('.audit-status [role="alert"]')).toBeNull();
  expect(api.auditSearch).toHaveBeenCalledTimes(2);
  expect(api.auditSearch).toHaveBeenLastCalledWith({
    operationPrefix: "", result: "", bookmarkId: "", beforeId: 0, limit: 50,
  });
});

it.each([{ initial: [] }, { initial: [audit(22)] }])("qualifies earlier $initial as stale after a failed refresh", async ({ initial }) => {
  const api = newFakeApi();
  const pending = deferred();
  api.auditSearch = vi.fn().mockResolvedValueOnce(initial)
    .mockReturnValueOnce(pending.promise).mockResolvedValueOnce([audit(33)]);
  const browser = new BrowserState(api);
  show(browser);
  await vi.waitFor(() => expect(browser.audit.loaded).toBe(true));
  flushSync();
  button("Refresh audit search").click();
  flushSync();
  expect(resources()).toEqual(initial.map((event) => event.resource));
  expect(document.querySelector(".audit-status")?.textContent).toContain("Previously loaded results");
  expect(document.body.textContent).not.toContain("No matching audit events");
  pending.reject(new Error("private refresh failure"));
  await vi.waitFor(() => expect(browser.audit.error).not.toBeNull());
  flushSync();
  expect(resources()).toEqual(initial.map((event) => event.resource));
  expect(document.querySelector(".audit-status")?.textContent).toContain("stale");
  expect(document.body.textContent).not.toContain("No matching audit events");
  button("Retry audit search").click();
  await vi.waitFor(() => expect(browser.audit.error).toBeNull());
  await vi.waitFor(() => expect(browser.audit.loading).toBe(false));
  flushSync();
  expect(resources()).toEqual(["object-33"]);
  expect(document.querySelector(".audit-status")?.textContent).not.toContain("stale");
});

it("keeps filter values literal and clears previous-filter rows before the next read finishes", async () => {
  const api = newFakeApi();
  const pending = deferred();
  api.auditSearch = vi.fn().mockResolvedValueOnce([audit(22)])
    .mockReturnValueOnce(pending.promise).mockResolvedValueOnce([]);
  const browser = new BrowserState(api);
  browser.jobsFilter = "another-account";
  show(browser);
  await vi.waitFor(() => expect(browser.audit.loaded).toBe(true));
  flushSync();
  const operation = document.querySelector<HTMLInputElement>('input[id$="-audit-operation"]')!;
  operation.value = " delete.λ ";
  operation.dispatchEvent(new Event("change", { bubbles: true }));
  flushSync();
  expect(resources()).toEqual([]);
  expect(document.querySelector(".audit-scope")?.textContent).toContain("All accounts");
  expect(document.querySelector(".audit-scope")?.textContent).toContain(" delete.λ ");
  expect(api.auditSearch).toHaveBeenLastCalledWith({
    operationPrefix: " delete.λ ", result: "", bookmarkId: "", beforeId: 0, limit: 50,
  });
  const result = document.querySelector<HTMLSelectElement>('select[id$="-audit-result"]')!;
  result.value = "failed";
  result.dispatchEvent(new Event("change", { bubbles: true }));
  await vi.waitFor(() => expect(browser.audit.loaded).toBe(true));
  pending.resolve([audit(11)]);
  await pending.promise;
  flushSync();
  expect(resources()).toEqual([]);
  expect(api.auditSearch).toHaveBeenLastCalledWith({
    operationPrefix: " delete.λ ", result: "failed", bookmarkId: "", beforeId: 0, limit: 50,
  });
  expect(document.querySelector(".audit-scope")?.textContent).toContain("failed");
});

it("shows not-loaded after a filter edit without dispatching an automatic search", async () => {
  const api = newFakeApi();
  api.auditSearch = vi.fn(async () => [audit(22)]);
  const browser = new BrowserState(api);
  show(browser);
  await vi.waitFor(() => expect(browser.audit.loaded).toBe(true));
  browser.audit.filterOp = "delete.";
  flushSync();
  expect(resources()).toEqual([]);
  expect(document.querySelector(".audit-status")?.textContent).toContain("not been loaded");
  expect(document.body.textContent).not.toContain("No matching audit events");
  expect(api.auditSearch).toHaveBeenCalledTimes(1);
  expect(button("Load audit events").disabled).toBe(false);
});

it("guards busy More controls and retries an unavailable page using the same cursor", async () => {
  const api = newFakeApi();
  const pending = deferred();
  api.auditSearch = vi.fn().mockResolvedValueOnce(page())
    .mockReturnValueOnce(pending.promise).mockResolvedValueOnce([audit(50)]);
  const browser = new BrowserState(api);
  show(browser);
  await vi.waitFor(() => expect(browser.audit.loaded).toBe(true));
  flushSync();
  const more = button("Load more…");
  more.click();
  flushSync();
  expect(more.disabled).toBe(true);
  expect(document.querySelector(".audit-status")?.textContent).toContain("Loading more audit events");
  more.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  expect(api.auditSearch).toHaveBeenCalledTimes(2);
  pending.reject(new Error("private page path"));
  await vi.waitFor(() => expect(browser.audit.moreError).not.toBeNull());
  flushSync();
  expect(resources()).toEqual(page().map((event) => event.resource));
  expect(document.querySelector('.audit-status [role="alert"]')?.textContent)
    .toBe("More audit events are unavailable. Previously loaded results are shown.");
  expect(document.body.textContent).not.toContain("private page path");
  button("Retry audit page").click();
  await vi.waitFor(() => expect(browser.audit.moreLoading).toBe(false));
  flushSync();
  expect(resources()).toEqual([...page().map((event) => event.resource), "object-50"]);
  expect(vi.mocked(api.auditSearch).mock.calls[1]).toEqual(vi.mocked(api.auditSearch).mock.calls[2]);
  expect(document.querySelector(".more")).toBeNull();
});

it("allows first-page refresh while More is pending and ignores the old page failure", async () => {
  const api = newFakeApi();
  const pending = deferred();
  api.auditSearch = vi.fn().mockResolvedValueOnce(page())
    .mockReturnValueOnce(pending.promise).mockResolvedValueOnce([audit(222)]);
  const browser = new BrowserState(api);
  show(browser);
  await vi.waitFor(() => expect(browser.audit.loaded).toBe(true));
  flushSync();
  button("Load more…").click();
  flushSync();
  expect(button("Refresh audit search").disabled).toBe(false);
  button("Refresh audit search").click();
  await vi.waitFor(() => expect(browser.audit.loading).toBe(false));
  pending.reject(new Error("retired More error"));
  await pending.promise.catch(() => undefined);
  flushSync();
  expect(resources()).toEqual(["object-222"]);
  expect(document.querySelector('.audit-status [role="alert"]')).toBeNull();
});

it("keeps invalid page IDs out of the mounted keyed rows", async () => {
  const api = newFakeApi();
  api.auditSearch = vi.fn().mockResolvedValueOnce(page()).mockResolvedValueOnce([audit(50), audit(50)]);
  const browser = new BrowserState(api);
  show(browser);
  await vi.waitFor(() => expect(browser.audit.loaded).toBe(true));
  flushSync();
  button("Load more…").click();
  await vi.waitFor(() => expect(browser.audit.moreError).not.toBeNull());
  flushSync();
  expect(resources()).toEqual(page().map((event) => event.resource));
  expect(button("Retry audit page").disabled).toBe(false);
});

it("counts only the independent Recent slice while Search is filtered and paged", async () => {
  const api = newFakeApi();
  const recent = deferred();
  api.auditRecent = vi.fn(() => recent.promise);
  api.auditSearch = vi.fn().mockResolvedValueOnce(page()).mockResolvedValueOnce([audit(50)]);
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source";
  const refresh = browser.audit.loadRecent();
  browser.audit.filterOp = "copy.";
  browser.jobsFilter = "other-account";
  show(browser);
  await vi.waitFor(() => expect(browser.audit.loaded).toBe(true));
  recent.resolve([audit(300), audit(299)]);
  await refresh;
  flushSync();
  expect(bar().textContent).toContain("2 last loaded recent changes");
  expect(resources()).toEqual(page().map((event) => event.resource));
  button("Load more…").click();
  await vi.waitFor(() => expect(browser.audit.moreLoading).toBe(false));
  flushSync();
  expect(resources()).toHaveLength(51);
  expect(bar().textContent).toContain("2 last loaded recent changes");
  expect(bar().textContent).not.toContain("51 recent");
});

it("reports Recent unavailable in the collapsed summary and retries only the read", async () => {
  const api = newFakeApi();
  const pending = deferred();
  api.auditRecent = vi.fn().mockReturnValueOnce(pending.promise).mockResolvedValueOnce([]);
  api.auditSearch = vi.fn(async () => []);
  api.cancelJob = vi.fn();
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source";
  drawer = mount(JobsDrawer, { target: document.body, props: { browser } });
  flushSync();
  expect(bar().textContent).toContain("Recent changes not loaded");
  const refresh = browser.audit.loadRecent();
  flushSync();
  expect(bar().textContent).toContain("Loading recent changes");
  pending.reject(new Error("private database path"));
  await refresh;
  flushSync();
  expect(bar().textContent).toContain("Recent changes unavailable");
  expect(bar().textContent).not.toContain("0 last loaded");
  expect(document.body.textContent).not.toContain("private database path");
  bar().click();
  flushSync();
  button("Recent changes").click();
  flushSync();
  button("Retry recent changes").click();
  await vi.waitFor(() => expect(browser.audit.recentLoading).toBe(false));
  flushSync();
  expect(bar().textContent).toContain("0 last loaded recent changes");
  expect(browser.audit.recentError).toBe(false);
  expect(api.auditRecent).toHaveBeenCalledTimes(2);
  expect(api.auditRecent).toHaveBeenLastCalledWith(50, "source");
  expect(api.auditSearch).toHaveBeenCalledTimes(1);
  expect(api.cancelJob).not.toHaveBeenCalled();
});

it.each([{ initial: [] }, { initial: [audit(30)] }])("marks the earlier Recent slice $initial stale even when its count was zero", async ({ initial }) => {
  const api = newFakeApi();
  api.auditRecent = vi.fn().mockResolvedValueOnce(initial).mockRejectedValueOnce(new Error("Recent unavailable"));
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source";
  await browser.audit.loadRecent();
  show(browser);
  button("Refresh recent changes").click();
  await vi.waitFor(() => expect(browser.audit.recentError).toBe(true));
  flushSync();
  expect(bar().textContent).toContain("Recent changes unavailable");
  expect(bar().textContent).toContain(`${initial.length} last loaded; stale`);
  expect(document.querySelector(".audit-recent")?.textContent).toContain("stale");
  expect(button("Retry recent changes").disabled).toBe(false);
});

it("keeps a query owned by BrowserState while the drawer is collapsed", async () => {
  const api = newFakeApi();
  const pending = deferred();
  api.auditSearch = vi.fn(() => pending.promise);
  const browser = new BrowserState(api);
  show(browser);
  bar().click();
  flushSync();
  pending.resolve([audit(22)]);
  await vi.waitFor(() => expect(browser.audit.loaded).toBe(true));
  bar().click();
  flushSync();
  expect(resources()).toEqual(["object-22"]);
  expect(api.auditSearch).toHaveBeenCalledTimes(1);
});

it("attributes the recent slice to the connected account, not every account", async () => {
  const api = newFakeApi();
  api.auditRecent = vi.fn(api.auditRecent);
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source";
  await browser.audit.loadRecent();
  show(browser);
  expect(document.querySelector(".audit-recent")?.textContent)
    .toContain("up to 50 for the connected account");
  expect(document.querySelector(".audit-recent")?.textContent)
    .not.toContain("across all accounts");
  expect(api.auditRecent).toHaveBeenLastCalledWith(50, "source");
});

it("shows transaction IDs when present and omits the row when none were recorded", async () => {
  const api = newFakeApi();
  const withIds: AuditEventDTO = { id: 22, at: "", bookmarkId: "", operation: "delete.object",
    resource: "object-22", summary: "deleted", result: "applied", transIds: ["txABC123", "txDEF456"] };
  const noIds: AuditEventDTO = { id: 21, at: "", bookmarkId: "", operation: "settings.update",
    resource: "local", summary: "recorded", result: "applied", transIds: [] };
  // A Swift response can supply no transaction id, which the store records as
  // a single blank entry; that must read as none, not as an empty label.
  const blankId: AuditEventDTO = { id: 20, at: "", bookmarkId: "", operation: "metadata.update",
    resource: "object-20", summary: "updated", result: "applied", transIds: [""] };
  api.auditSearch = vi.fn().mockResolvedValueOnce([withIds, noIds, blankId]);
  const browser = new BrowserState(api);
  show(browser);
  await vi.waitFor(() => expect(browser.audit.loaded).toBe(true));
  flushSync();
  const events = [...document.querySelectorAll(".event")];
  expect(events.length).toBe(3);
  // The event that reached Swift surfaces its transaction IDs for correlation.
  const shown = events[0].querySelector(".trans");
  expect(shown?.textContent).toContain("txABC123");
  expect(shown?.textContent).toContain("txDEF456");
  // An event that recorded none (a local-only change) shows no transaction-ID
  // row, so an empty list reads as none rather than as an unavailable field.
  expect(events[1].querySelector(".trans")).toBeNull();
  // A blank-only list is none in substance, so it shows no row and no bare label.
  expect(events[2].querySelector(".trans")).toBeNull();
});

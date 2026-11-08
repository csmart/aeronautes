import { beforeEach, describe, expect, it, vi } from "vitest";
import { liveApi, type AuditEventDTO, type AuditQueryDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";

const bridge = vi.hoisted(() => ({ search: vi.fn() }));
vi.mock("../../../bindings/github.com/csmart/aeronautes/internal/app", () => ({
  AppService: {}, AuditService: { Search: bridge.search }, BookmarkService: {}, BrowseService: {},
  BenchmarkService: {}, DeleteService: {}, FindService: {}, SettingsService: {}, InspectorService: {},
  JobService: {}, MutateService: {}, TransferService: {},
}));

const queryError = "Audit events are unavailable. Retry this search.";
const pageError = "More audit events are unavailable. Previously loaded results are shown.";

beforeEach(() => vi.resetAllMocks());

function event(id: number, bookmarkId = "alpha"): AuditEventDTO {
  return { id, bookmarkId, at: "", operation: "object.metadata.update", resource: "same/object",
    summary: "metadata applied", result: "applied", transIds: ["tx-observed"] };
}

function page(): AuditEventDTO[] {
  return Array.from({ length: 50 }, (_, index) => event(100 - index));
}

function deferred() {
  let resolve!: (rows: AuditEventDTO[]) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<AuditEventDTO[]>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

function state() {
  return new BrowserState({ ...newFakeApi(), auditSearch: liveApi.auditSearch });
}

function requireBookmark(q: AuditQueryDTO, bookmarkId = "alpha") {
  if (q.bookmarkId !== bookmarkId) throw new Error("query named the wrong bookmark");
}

describe("audit bookmark queries through the live bridge", () => {
  it("keeps explicit all-account mode independent of jobs and the active account", async () => {
    const rows = [event(3, "beta"), event(2), event(1, "")];
    bridge.search.mockResolvedValue(rows);
    const browser = state();
    browser.jobs.bookmarkFilter = "alpha";
    browser.connectedBookmarkId = "beta";
    await browser.audit.search();
    expect(bridge.search).toHaveBeenCalledExactlyOnceWith({
      bookmarkId: "", operationPrefix: "", result: "", beforeId: 0, limit: 50,
    });
    expect(browser.audit.events).toEqual(rows);
    expect(browser.audit.error).toBeNull();
  });

  it.each([null, "other-active-account"])("reads the named local history with active bookmark %s", async (active) => {
    bridge.search.mockImplementation(async (q: AuditQueryDTO) => {
      requireBookmark(q, " alpha%_ ");
      return [event(1, q.bookmarkId)];
    });
    const browser = state();
    browser.connectedBookmarkId = active;
    browser.audit.filterBookmark = " alpha%_ ";
    browser.audit.filterOp = " object.%_λ ";
    browser.audit.filterResult = "failed";
    await browser.audit.search();
    expect(bridge.search).toHaveBeenCalledExactlyOnceWith({
      bookmarkId: " alpha%_ ", operationPrefix: " object.%_λ ", result: "failed", beforeId: 0, limit: 50,
    });
    expect(browser.audit.events.map((row) => row.bookmarkId)).toEqual([" alpha%_ "]);
    expect(browser.audit.error).toBeNull();
  });

  it("keeps the bookmark, literal filters and failed cursor on a page retry", async () => {
    let calls = 0;
    bridge.search.mockImplementation(async (q: AuditQueryDTO) => {
      requireBookmark(q);
      if (++calls === 1) return page();
      if (calls === 2) throw new Error("private local database details");
      return [event(50)];
    });
    const browser = state();
    browser.audit.filterBookmark = "alpha";
    browser.audit.filterOp = "object.metadata.";
    browser.audit.filterResult = "applied";
    await browser.audit.search();
    await browser.audit.search(true);
    expect(browser.audit.events).toEqual(page());
    expect(browser.audit.moreError).toBe(pageError);
    browser.connectedBookmarkId = "beta";
    await browser.audit.search(true);
    expect(browser.audit.events).toEqual([...page(), event(50)]);
    expect(bridge.search.mock.calls[1]).toEqual(bridge.search.mock.calls[2]);
    expect(bridge.search).toHaveBeenLastCalledWith({
      bookmarkId: "alpha", operationPrefix: "object.metadata.", result: "applied", beforeId: 51, limit: 50,
    });
  });

  it.each(["foreign", "empty", "missing"])("rejects a %s bookmark on a first page without claiming empty success", async (kind) => {
    const bad = { ...event(10), bookmarkId: kind === "foreign" ? "beta" : kind === "empty" ? "" : undefined };
    bridge.search.mockResolvedValue([event(11), bad]);
    const browser = state();
    browser.audit.filterBookmark = "alpha";
    await browser.audit.search();
    expect(browser.audit.events).toEqual([]);
    expect(browser.audit.loaded).toBe(false);
    expect(browser.audit.exhausted).toBe(false);
    expect(browser.audit.error).toBe(queryError);
  });

  it.each(["foreign", "empty", "missing"])("rejects a %s bookmark on a later page and retries the unadvanced cursor", async (kind) => {
    const bad = { ...event(49), bookmarkId: kind === "foreign" ? "beta" : kind === "empty" ? "" : undefined };
    bridge.search.mockResolvedValueOnce(page()).mockResolvedValueOnce([event(50), bad])
      .mockResolvedValueOnce([event(50)]);
    const browser = state();
    browser.audit.filterBookmark = "alpha";
    await browser.audit.search();
    await browser.audit.search(true);
    expect(browser.audit.events).toEqual(page());
    expect(browser.audit.moreError).toBe(pageError);
    expect(browser.audit.exhausted).toBe(false);
    await browser.audit.search(true);
    expect(browser.audit.events).toEqual([...page(), event(50)]);
    expect(bridge.search.mock.calls[1]).toEqual(bridge.search.mock.calls[2]);
    expect(bridge.search.mock.calls[2][0]).toMatchObject({ bookmarkId: "alpha", beforeId: 51 });
  });

  it.each(["success", "failure"])("retires an away-and-back bookmark edit before an old %s without another search", async (outcome) => {
    const pending = deferred();
    bridge.search.mockReturnValue(pending.promise);
    const browser = state();
    browser.audit.filterBookmark = "alpha";
    const first = browser.audit.search();
    browser.audit.filterBookmark = "beta";
    browser.audit.filterBookmark = "alpha";
    if (outcome === "success") pending.resolve([event(1)]);
    else pending.reject(new Error("obsolete error"));
    await first;
    expect(browser.audit.events).toEqual([]);
    expect(browser.audit.loaded).toBe(false);
    expect(browser.audit.loading).toBe(false);
    expect(browser.audit.error).toBeNull();
  });

  it.each(["success", "failure"])("discards a held page %s and finally after selecting another bookmark", async (outcome) => {
    const older = deferred();
    const newer = deferred();
    bridge.search.mockResolvedValueOnce(page()).mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);
    const browser = state();
    browser.audit.filterBookmark = "alpha";
    await browser.audit.search();
    const more = browser.audit.search(true);
    browser.audit.filterBookmark = "beta";
    expect(browser.audit.events).toEqual([]);
    expect(browser.audit.loaded).toBe(false);
    expect(browser.audit.moreError).toBeNull();
    const replacement = browser.audit.search();
    if (outcome === "success") older.resolve([event(50)]);
    else older.reject(new Error("obsolete page error"));
    await more;
    expect(browser.audit.loading).toBe(true);
    expect(browser.audit.error).toBeNull();
    expect(browser.audit.moreError).toBeNull();
    expect(browser.audit.events).toEqual([]);
    newer.resolve([event(200, "beta")]);
    await replacement;
    expect(browser.audit.events).toEqual([event(200, "beta")]);
    expect(browser.audit.loading).toBe(false);
  });

  it("keeps a captured historical query when the live connection changes", async () => {
    const pending = deferred();
    bridge.search.mockReturnValue(pending.promise);
    const browser = state();
    browser.audit.filterBookmark = "alpha";
    const read = browser.audit.search();
    browser.connectedBookmarkId = "beta";
    pending.resolve([event(1)]);
    await read;
    expect(browser.audit.events).toEqual([event(1)]);
    expect(bridge.search.mock.calls[0][0].bookmarkId).toBe("alpha");
    expect(browser.audit.error).toBeNull();
  });

  it("keeps the shared fake's bookmark filter consistent with the local store", async () => {
    const api = newFakeApi();
    // A write names the scope of a connected view (ADR 0079).
    const owner = await api.saveBookmark({ id: "", displayName: "Owner", authUrl: "http://mock/auth/v1.0", username: "test:tester", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "testing");
    await api.connect(owner.id);
    await api.createMarker({ bookmarkId: owner.id, generation: 1 }, "fixtures", "audit-owned/");
    const query = { bookmarkId: "", operationPrefix: "", result: "", beforeId: 0, limit: 50 };
    const all = await api.auditSearch(query);
    expect(all).toHaveLength(1);
    expect(all[0].bookmarkId).toBe("");
    expect(await api.auditSearch({ ...query, bookmarkId: "alpha" })).toEqual([]);
  });
});

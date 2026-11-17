import { describe, expect, it, vi } from "vitest";
import type { Api, AuditEventDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";

const queryError = "Audit events are unavailable. Retry this search.";
const pageError = "More audit events are unavailable. Previously loaded results are shown.";

function audit(id: number, operation = "copy.object"): AuditEventDTO {
  return { id, at: "", bookmarkId: "", operation, resource: `object-${id}`, summary: operation,
    result: "applied", transIds: ["tx-owned"] };
}

function page(top = 100): AuditEventDTO[] {
  return Array.from({ length: 50 }, (_, index) => audit(top - index));
}

function ids(events: AuditEventDTO[]): number[] {
  return events.map((event) => event.id);
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

describe("selected audit query", () => {
  it("does not admit More before a successful current query", async () => {
    const api = newFakeApi();
    api.auditSearch = vi.fn(async () => []);
    const browser = new BrowserState(api);
    expect(browser.audit.loaded).toBe(false);
    expect(browser.audit.loading).toBe(false);
    expect(browser.audit.error).toBeNull();
    await browser.audit.search(true);
    expect(api.auditSearch).not.toHaveBeenCalled();
  });

  it("distinguishes initial loading, unavailable and a successful empty retry", async () => {
    const api = newFakeApi();
    const pending = deferred<AuditEventDTO[]>();
    api.auditSearch = vi.fn().mockReturnValueOnce(pending.promise).mockResolvedValueOnce([]);
    const browser = new BrowserState(api);
    const first = browser.audit.search();
    expect(browser.audit.loading).toBe(true);
    expect(browser.audit.loaded).toBe(false);
    expect(browser.audit.exhausted).toBe(false);
    pending.reject(new Error("secret SQLite details"));
    await expect(first).resolves.toBeUndefined();
    expect(browser.audit.loading).toBe(false);
    expect(browser.audit.loaded).toBe(false);
    expect(browser.audit.error).toBe(queryError);
    expect(browser.audit.exhausted).toBe(false);
    await browser.audit.search(true);
    expect(api.auditSearch).toHaveBeenCalledTimes(1);
    await browser.audit.search();
    expect(browser.audit.events).toEqual([]);
    expect(browser.audit.loaded).toBe(true);
    expect(browser.audit.exhausted).toBe(true);
    expect(browser.audit.error).toBeNull();
    await browser.audit.search(true);
    expect(api.auditSearch).toHaveBeenCalledTimes(2);
  });

  it("preserves literal operation and result values with all-account scope", async () => {
    const api = newFakeApi();
    api.auditSearch = vi.fn(async () => []);
    const browser = new BrowserState(api);
    browser.audit.filterOp = " delete.λ ";
    browser.audit.filterResult = "failed";
    browser.jobs.bookmarkFilter = "account-b";
    await browser.audit.search();
    expect(api.auditSearch).toHaveBeenCalledExactlyOnceWith({
      operationPrefix: " delete.λ ", result: "failed", bookmarkId: "", beforeId: 0, limit: 50,
    });
    expect(browser.audit.loaded).toBe(true);
  });

  it.each([{ initial: [] }, { initial: [audit(22)] }])("retains same-query rows through a failed refresh: $initial", async ({ initial }) => {
    const api = newFakeApi();
    const pending = deferred<AuditEventDTO[]>();
    api.auditSearch = vi.fn().mockResolvedValueOnce(initial)
      .mockReturnValueOnce(pending.promise).mockResolvedValueOnce([audit(33)]);
    const browser = new BrowserState(api);
    await browser.audit.search();
    const refresh = browser.audit.search();
    expect(browser.audit.events).toEqual(initial);
    expect(browser.audit.loaded).toBe(true);
    expect(browser.audit.loading).toBe(true);
    pending.reject(new Error("refresh unavailable"));
    await refresh;
    expect(browser.audit.events).toEqual(initial);
    expect(browser.audit.loaded).toBe(true);
    expect(browser.audit.error).toBe(queryError);
    expect(browser.audit.canMore).toBe(false);
    await browser.audit.search();
    expect(ids(browser.audit.events)).toEqual([33]);
    expect(browser.audit.error).toBeNull();
  });

  it.each(["success", "failure"])("ignores an older %s and finally during a newer same-query refresh", async (outcome) => {
    const api = newFakeApi();
    const older = deferred<AuditEventDTO[]>();
    const newer = deferred<AuditEventDTO[]>();
    api.auditSearch = vi.fn().mockResolvedValueOnce(page())
      .mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);
    const browser = new BrowserState(api);
    await browser.audit.search();
    const first = browser.audit.search();
    const second = browser.audit.search();
    if (outcome === "success") older.resolve([]);
    else older.reject(new Error("obsolete private error"));
    await first;
    expect(browser.audit.loading).toBe(true);
    expect(browser.audit.error).toBeNull();
    expect(browser.audit.exhausted).toBe(false);
    expect(browser.audit.events).toEqual(page());
    newer.resolve([audit(101)]);
    await second;
    expect(ids(browser.audit.events)).toEqual([101]);
    expect(browser.audit.loading).toBe(false);
  });

  it.each(["success", "failure"])("ignores an older %s after the newer query has finished", async (outcome) => {
    const api = newFakeApi();
    const older = deferred<AuditEventDTO[]>();
    api.auditSearch = vi.fn().mockReturnValueOnce(older.promise).mockResolvedValueOnce([audit(22)]);
    const browser = new BrowserState(api);
    const first = browser.audit.search();
    await browser.audit.search();
    if (outcome === "success") older.resolve([audit(11)]);
    else older.reject(new Error("obsolete private error"));
    await first;
    expect(ids(browser.audit.events)).toEqual([22]);
    expect(browser.audit.error).toBeNull();
    expect(browser.audit.loading).toBe(false);
  });

  it.each([
    ["filterOp", "copy.", "delete."],
    ["filterResult", "applied", "failed"],
  ] as const)("retires old rows synchronously when %s changes", async (field, previous, next) => {
    const api = newFakeApi();
    api.auditSearch = vi.fn(async () => page());
    const browser = new BrowserState(api);
    browser.audit[field] = previous;
    await browser.audit.search();
    browser.audit[field] = next;
    expect(browser.audit.events).toEqual([]);
    expect(browser.audit.loaded).toBe(false);
    expect(browser.audit.error).toBeNull();
    expect(browser.audit.exhausted).toBe(false);
    await browser.audit.search(true);
    expect(api.auditSearch).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["filterOp", "copy.", "delete."],
    ["filterResult", "applied", "failed"],
  ] as const)("retires pending replies when %s changes away and back without another search", async (field, previous, next) => {
    const api = newFakeApi();
    const pending = deferred<AuditEventDTO[]>();
    api.auditSearch = vi.fn(() => pending.promise);
    const browser = new BrowserState(api);
    browser.audit[field] = previous;
    const first = browser.audit.search();
    browser.audit[field] = next;
    browser.audit[field] = previous;
    pending.resolve([audit(11)]);
    await first;
    expect(api.auditSearch).toHaveBeenCalledTimes(1);
    expect(browser.audit.events).toEqual([]);
    expect(browser.audit.loaded).toBe(false);
    expect(browser.audit.loading).toBe(false);
    expect(browser.audit.error).toBeNull();
  });

  it("ignores failure after a filter edit that has not dispatched a replacement read", async () => {
    const api = newFakeApi();
    const pending = deferred<AuditEventDTO[]>();
    api.auditSearch = vi.fn(() => pending.promise);
    const browser = new BrowserState(api);
    const first = browser.audit.search();
    browser.audit.filterResult = "failed";
    pending.reject(new Error("old scope"));
    await first;
    expect(browser.audit.loaded).toBe(false);
    expect(browser.audit.loading).toBe(false);
    expect(browser.audit.error).toBeNull();
    expect(api.auditSearch).toHaveBeenCalledTimes(1);
  });
});

describe("audit paging", () => {
  it("admits one More request after a legal full first page and appends each ID once", async () => {
    const api = newFakeApi();
    const pending = deferred<AuditEventDTO[]>();
    const initial = page();
    api.auditSearch = vi.fn().mockResolvedValueOnce(initial).mockReturnValue(pending.promise);
    const browser = new BrowserState(api);
    await browser.audit.search();
    expect(ids(browser.audit.events)).toEqual(ids(initial));
    expect(browser.audit.exhausted).toBe(false);
    const first = browser.audit.search(true);
    const second = browser.audit.search(true);
    expect(browser.audit.moreLoading).toBe(true);
    expect(browser.audit.canMore).toBe(false);
    pending.resolve([audit(50), audit(49)]);
    await Promise.all([first, second]);
    expect.soft(api.auditSearch).toHaveBeenCalledTimes(2);
    expect.soft(api.auditSearch).toHaveBeenNthCalledWith(2, {
      operationPrefix: "", result: "", bookmarkId: "", beforeId: 51, limit: 50,
    });
    expect(ids(browser.audit.events)).toEqual([...ids(initial), 50, 49]);
    expect(browser.audit.moreLoading).toBe(false);
  });

  it("retries a failed page with its original cursor and accepts empty exhaustion", async () => {
    const api = newFakeApi();
    api.auditSearch = vi.fn().mockResolvedValueOnce(page())
      .mockRejectedValueOnce(new Error("private page details"))
      .mockResolvedValueOnce(page(50)).mockResolvedValueOnce([]);
    const browser = new BrowserState(api);
    await browser.audit.search();
    await browser.audit.search(true);
    expect(browser.audit.events).toEqual(page());
    expect(browser.audit.moreError).toBe(pageError);
    expect(browser.audit.error).toBeNull();
    expect(browser.audit.exhausted).toBe(false);
    expect(browser.audit.canMore).toBe(true);
    await browser.audit.search(true);
    expect(browser.audit.events).toEqual([...page(), ...page(50)]);
    expect(browser.audit.moreError).toBeNull();
    expect(vi.mocked(api.auditSearch).mock.calls[1]).toEqual(vi.mocked(api.auditSearch).mock.calls[2]);
    await browser.audit.search(true);
    expect(browser.audit.events).toEqual([...page(), ...page(50)]);
    expect(browser.audit.exhausted).toBe(true);
    expect(browser.audit.canMore).toBe(false);
  });

  it.each([
    ["filter", "success"], ["filter", "failure"],
    ["refresh", "success"], ["refresh", "failure"],
  ])("retires a held More %s request before its late %s", async (replacement, outcome) => {
    const api = newFakeApi();
    const older = deferred<AuditEventDTO[]>();
    const newer = deferred<AuditEventDTO[]>();
    api.auditSearch = vi.fn().mockResolvedValueOnce(page())
      .mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);
    const browser = new BrowserState(api);
    await browser.audit.search();
    const first = browser.audit.search(true);
    if (replacement === "filter") browser.audit.filterResult = "failed";
    const second = browser.audit.search();
    if (outcome === "success") older.resolve([audit(50)]);
    else older.reject(new Error("old page failure"));
    await first;
    expect(browser.audit.loading).toBe(true);
    expect(browser.audit.error).toBeNull();
    expect(browser.audit.moreError).toBeNull();
    expect(browser.audit.events).toEqual(replacement === "filter" ? [] : page());
    newer.resolve([audit(222)]);
    await second;
    expect(ids(browser.audit.events)).toEqual([222]);
    expect(browser.audit.moreLoading).toBe(false);
  });

  it.each([
    ["duplicate", [audit(150), audit(150)]],
    ["reversed", [audit(149), audit(150)]],
    ["same cursor", [audit(151)]],
    ["newer ID", [audit(201)]],
    ["zero ID", [audit(0)]],
    ["negative ID", [audit(-1)]],
    ["fractional ID", [audit(1.5)]],
    ["unsafe ID", [audit(Number.MAX_SAFE_INTEGER + 1)]],
    ["oversized", Array.from({ length: 51 }, (_, index) => audit(150 - index))],
  ] as const)("rejects a %s page without repair, append or cursor advancement", async (_name, invalid) => {
    const api = newFakeApi();
    api.auditSearch = vi.fn().mockResolvedValueOnce(page(200))
      .mockResolvedValueOnce(invalid).mockResolvedValueOnce([audit(150)]);
    const browser = new BrowserState(api);
    await browser.audit.search();
    await browser.audit.search(true);
    expect(browser.audit.events).toEqual(page(200));
    expect(browser.audit.moreError).toBe(pageError);
    expect(browser.audit.exhausted).toBe(false);
    await browser.audit.search(true);
    expect(browser.audit.events).toEqual([...page(200), audit(150)]);
    expect(vi.mocked(api.auditSearch).mock.calls[1]).toEqual(vi.mocked(api.auditSearch).mock.calls[2]);
  });

  it.each([
    [audit(2), audit(2)], [audit(1), audit(2)], [audit(0)],
    Array.from({ length: 51 }, (_, index) => audit(100 - index)),
  ])("rejects a malformed initial page without a successful-empty claim: %j", async (...invalid) => {
    const api = newFakeApi();
    api.auditSearch = vi.fn(async () => invalid);
    const browser = new BrowserState(api);
    await browser.audit.search();
    expect(browser.audit.events).toEqual([]);
    expect(browser.audit.loaded).toBe(false);
    expect(browser.audit.error).toBe(queryError);
    expect(browser.audit.exhausted).toBe(false);
  });
});

describe("independent Recent audit cache", () => {
  it("refuses a Recent read that names no bookmark", async () => {
    const api = newFakeApi();
    await expect(api.auditRecent(50, "")).rejects.toThrow(/name the bookmark the read is for/);
    const browser = new BrowserState(api);
    await browser.audit.loadRecent();
    expect(browser.audit.recentError).toBe(true);
    expect(browser.audit.recentLoaded).toBe(false);
  });

  it.each([{ initial: [] }, { initial: [audit(30)] }])("labels a failed Recent refresh unavailable while retaining $initial", async ({ initial }) => {
    const api = newFakeApi();
    const pending = deferred<AuditEventDTO[]>();
    api.auditRecent = vi.fn().mockResolvedValueOnce(initial)
      .mockReturnValueOnce(pending.promise).mockResolvedValueOnce([audit(40)]);
    const browser = new BrowserState(api);
    browser.connectedBookmarkId = "source";
    expect(browser.audit.recentLoaded).toBe(false);
    await browser.audit.loadRecent();
    expect(browser.audit.recentLoaded).toBe(true);
    const refresh = browser.audit.loadRecent();
    expect(browser.audit.recentLoading).toBe(true);
    expect(browser.audit.recentEvents).toEqual(initial);
    pending.reject(new Error("secret audit details"));
    await expect(refresh).resolves.toBeUndefined();
    expect(browser.audit.recentEvents).toEqual(initial);
    expect(browser.audit.recentError).toBe(true);
    expect(browser.audit.recentLoading).toBe(false);
    expect(browser.audit.loaded).toBe(false);
    expect(browser.audit.events).toEqual([]);
    await browser.audit.loadRecent();
    expect(ids(browser.audit.recentEvents)).toEqual([40]);
    expect(browser.audit.recentError).toBe(false);
    expect(api.auditRecent).toHaveBeenLastCalledWith(50, "source");
  });

  it.each(["success", "failure"])("ignores an older Recent %s and finally during a newer read", async (outcome) => {
    const api = newFakeApi();
    const older = deferred<AuditEventDTO[]>();
    const newer = deferred<AuditEventDTO[]>();
    api.auditRecent = vi.fn().mockResolvedValueOnce([audit(30)])
      .mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);
    const browser = new BrowserState(api);
    browser.connectedBookmarkId = "source";
    await browser.audit.loadRecent();
    const first = browser.audit.loadRecent();
    const second = browser.audit.loadRecent();
    if (outcome === "success") older.resolve([]);
    else older.reject(new Error("older Recent error"));
    await first;
    expect(browser.audit.recentLoading).toBe(true);
    expect(browser.audit.recentError).toBe(false);
    expect(ids(browser.audit.recentEvents)).toEqual([30]);
    newer.resolve([audit(40)]);
    await second;
    expect(ids(browser.audit.recentEvents)).toEqual([40]);
    expect(browser.audit.recentLoading).toBe(false);
  });

  it("does not let an older Recent zero replace a newer unavailable read", async () => {
    const api = newFakeApi();
    const older = deferred<AuditEventDTO[]>();
    api.auditRecent = vi.fn().mockReturnValueOnce(older.promise)
      .mockRejectedValueOnce(new Error("current Recent error"));
    const browser = new BrowserState(api);
    browser.connectedBookmarkId = "source";
    const first = browser.audit.loadRecent();
    await browser.audit.loadRecent();
    older.resolve([]);
    await first;
    expect(browser.audit.recentLoaded).toBe(false);
    expect(browser.audit.recentError).toBe(true);
  });

  it.each([
    ["recent-first", "success", "success"], ["recent-first", "failure", "success"],
    ["recent-first", "success", "failure"], ["recent-first", "failure", "failure"],
    ["search-first", "success", "success"], ["search-first", "failure", "success"],
    ["search-first", "success", "failure"], ["search-first", "failure", "failure"],
  ])("keeps independent replies: %s, Recent %s, Search %s", async (order, recentOutcome, searchOutcome) => {
    const api = newFakeApi();
    const recent = deferred<AuditEventDTO[]>();
    const search = deferred<AuditEventDTO[]>();
    api.auditRecent = vi.fn().mockResolvedValueOnce([audit(30)]).mockReturnValueOnce(recent.promise);
    api.auditSearch = vi.fn().mockResolvedValueOnce([audit(22, "delete.container")])
      .mockReturnValueOnce(search.promise);
    const browser = new BrowserState(api);
    browser.connectedBookmarkId = "source";
    browser.audit.filterOp = "delete.";
    await browser.audit.loadRecent();
    await browser.audit.search();
    const refresh = browser.audit.loadRecent();
    const query = browser.audit.search();
    const finishRecent = async () => {
      if (recentOutcome === "success") recent.resolve([audit(40)]);
      else recent.reject(new Error("Recent private error"));
      await refresh;
    };
    const finishSearch = async () => {
      if (searchOutcome === "success") search.resolve([audit(23, "delete.container")]);
      else search.reject(new Error("Search private error"));
      await query;
    };
    if (order === "recent-first") {
      await finishRecent();
      expect(browser.audit.loading).toBe(true);
      expect(ids(browser.audit.events)).toEqual([22]);
      await finishSearch();
    } else {
      await finishSearch();
      expect(browser.audit.recentLoading).toBe(true);
      expect(ids(browser.audit.recentEvents)).toEqual([30]);
      await finishRecent();
    }
    expect(ids(browser.audit.events)).toEqual([searchOutcome === "success" ? 23 : 22]);
    expect(ids(browser.audit.recentEvents)).toEqual([recentOutcome === "success" ? 40 : 30]);
    expect(browser.audit.error).toBe(searchOutcome === "success" ? null : queryError);
    expect(browser.audit.recentError).toBe(recentOutcome === "failure");
    expect(browser.audit.loading).toBe(false);
    expect(browser.audit.recentLoading).toBe(false);
  });

  it("refuses an oversized Recent reply without silently truncating the last loaded cache", async () => {
    const api = newFakeApi();
    api.auditRecent = vi.fn().mockResolvedValueOnce([audit(30)])
      .mockResolvedValueOnce(Array.from({ length: 51 }, (_, index) => audit(100 - index)));
    const browser = new BrowserState(api);
    browser.connectedBookmarkId = "source";
    await browser.audit.loadRecent();
    await browser.audit.loadRecent();
    expect(ids(browser.audit.recentEvents)).toEqual([30]);
    expect(browser.audit.recentError).toBe(true);
  });
});

async function connected(api: Api): Promise<BrowserState> {
  const browser = new BrowserState(api);
  const bookmark = await browser.saveBookmark({
    id: "", displayName: "Mock", authUrl: "http://mock/auth/v1.0", username: "test:tester",
    storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false,
  }, "testing");
  await browser.connect(bookmark.id);
  await browser.openContainer("fixtures");
  return browser;
}

const mutationCases = [
  {
    method: "updateObjectMetadata", operation: "object.metadata.update", returnsResult: true,
    prefix: "normal/", selected: "normal/hello.txt",
    run: (browser: BrowserState) => browser.saveObjectMetadata({
      meta: { Origin: "fixture", Camera: "fx6" }, contentType: "", deleteAt: "",
    }),
  },
  {
    method: "updateContainer", operation: "container.update", returnsResult: true,
    prefix: "", selected: "",
    run: (browser: BrowserState) => browser.saveContainer({
      meta: { Team: "ops" }, readAcl: "", writeAcl: "", versioningMode: "",
      versioningArchive: "", quotaBytes: 0, quotaCount: 0, ackPublic: false,
    }),
  },
  {
    method: "createContainer", operation: "container.create", returnsResult: false,
    prefix: "", selected: "",
    run: (browser: BrowserState) => browser.createContainer({ name: "audit-owned", policy: "gold", meta: {} }, browser.portrait),
  },
  {
    method: "createMarker", operation: "marker.create", returnsResult: false,
    prefix: "", selected: "implied/",
    run: (browser: BrowserState) => browser.createMarker("implied/"),
  },
  {
    method: "deleteMarker", operation: "marker.delete", returnsResult: true,
    prefix: "", selected: "folder-marker/",
    run: (browser: BrowserState) => browser.deleteMarker("folder-marker/", "delete folder-marker/"),
  },
  {
    method: "deleteManifest", operation: "manifest.delete", returnsResult: true,
    prefix: "", selected: "external-dlo",
    run: (browser: BrowserState) => browser.deleteManifest("fixtures", "external-dlo", browser.objectDetail!),
  },
  {
    method: "deleteManifestWithSegments", operation: "segments.delete.start", returnsResult: true,
    prefix: "", selected: "external-dlo",
    run: (browser: BrowserState) => browser.deleteManifestWithSegments({
      bookmarkId: browser.connectedBookmarkId!, container: "fixtures", name: "external-dlo",
      force: true, confirm: "external-dlo", reviewed: { class: "dlo", fingerprint: "dlo:0:etag", length: 0, etag: "" },
    }),
  },
] as const;

async function prepareMutation(browser: BrowserState, scenario: typeof mutationCases[number]) {
  if (scenario.prefix) await browser.goTo(scenario.prefix);
  if (scenario.selected) {
    const index = browser.entries.findIndex((row) => row.name === scenario.selected);
    expect(index).toBeGreaterThanOrEqual(0);
    browser.select(index);
    await vi.waitFor(() => expect(browser.detailLoading).toBe(false));
    expect(browser.detailError).toBeNull();
  } else {
    await vi.waitFor(() => expect(browser.containerDetail).not.toBeNull());
  }
}

describe("audit reads after mutations", () => {
  it.each(mutationCases)("preserves $method outcome after rejected Recent and never repeats its effect on read retry", async (scenario) => {
    const api = newFakeApi();
    const browser = await connected(api);
    await prepareMutation(browser, scenario);
    const readTrail = api.auditRecent;
    const effect = vi.spyOn(api, scenario.method);
    api.auditRecent = vi.fn().mockRejectedValueOnce(new Error("private audit read failure"))
      .mockImplementation(readTrail);
    const result = await scenario.run(browser);
    expect(effect).toHaveBeenCalledTimes(1);
    const originalResult = await effect.mock.results[0].value;
    if (scenario.returnsResult) expect(result).toBe(originalResult);
    else expect(result).toBeUndefined();
    expect(browser.audit.recentError).toBe(true);
    expect(browser.audit.recentLoaded).toBe(true);
    expect(browser.audit.recentEvents).toEqual([]);
    const recorded = await readTrail(50, browser.connectedBookmarkId!);
    expect(recorded).toHaveLength(1);
    expect(recorded[0]).toMatchObject({ operation: scenario.operation, result: "applied" });
    await browser.audit.loadRecent();
    expect(api.auditRecent).toHaveBeenCalledTimes(2);
    expect(effect).toHaveBeenCalledTimes(1);
    expect(browser.audit.recentEvents).toEqual(recorded);
    expect(browser.audit.recentError).toBe(false);
    expect(await readTrail(50, browser.connectedBookmarkId!)).toEqual(recorded);
  });

  it.each(mutationCases)("preserves a real $method rejection without issuing the optional audit read", async (scenario) => {
    const api = newFakeApi();
    const browser = await connected(api);
    await prepareMutation(browser, scenario);
    const readTrail = api.auditRecent;
    const refusal = new Error("mutation refused");
    const effect = vi.spyOn(api, scenario.method).mockRejectedValueOnce(refusal);
    api.auditRecent = vi.fn(readTrail);
    await expect(scenario.run(browser)).rejects.toBe(refusal);
    expect(effect).toHaveBeenCalledTimes(1);
    expect(api.auditRecent).not.toHaveBeenCalled();
    expect(await readTrail(50, browser.connectedBookmarkId!)).toEqual([]);
    expect(browser.audit.recentError).toBe(false);
  });
});

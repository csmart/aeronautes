import { describe, expect, it, vi } from "vitest";
import type { Api, AuditEventDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";

const queryError = "Audit events are unavailable. Retry this search.";
const pageError = "More audit events are unavailable. Previously loaded results are shown.";

function audit(id: number, operation = "copy.object"): AuditEventDTO {
  return { id, at: "", operation, resource: `object-${id}`, summary: operation,
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
    expect(browser.auditLoaded).toBe(false);
    expect(browser.auditLoading).toBe(false);
    expect(browser.auditError).toBeNull();
    await browser.searchAudit(true);
    expect(api.auditSearch).not.toHaveBeenCalled();
  });

  it("distinguishes initial loading, unavailable and a successful empty retry", async () => {
    const api = newFakeApi();
    const pending = deferred<AuditEventDTO[]>();
    api.auditSearch = vi.fn().mockReturnValueOnce(pending.promise).mockResolvedValueOnce([]);
    const browser = new BrowserState(api);
    const first = browser.searchAudit();
    expect(browser.auditLoading).toBe(true);
    expect(browser.auditLoaded).toBe(false);
    expect(browser.auditExhausted).toBe(false);
    pending.reject(new Error("secret SQLite details"));
    await expect(first).resolves.toBeUndefined();
    expect(browser.auditLoading).toBe(false);
    expect(browser.auditLoaded).toBe(false);
    expect(browser.auditError).toBe(queryError);
    expect(browser.auditExhausted).toBe(false);
    await browser.searchAudit(true);
    expect(api.auditSearch).toHaveBeenCalledTimes(1);
    await browser.searchAudit();
    expect(browser.auditEvents).toEqual([]);
    expect(browser.auditLoaded).toBe(true);
    expect(browser.auditExhausted).toBe(true);
    expect(browser.auditError).toBeNull();
    await browser.searchAudit(true);
    expect(api.auditSearch).toHaveBeenCalledTimes(2);
  });

  it("preserves literal operation and result values with all-account scope", async () => {
    const api = newFakeApi();
    api.auditSearch = vi.fn(async () => []);
    const browser = new BrowserState(api);
    browser.auditFilterOp = " delete.λ ";
    browser.auditFilterResult = "failed";
    browser.jobsFilter = "account-b";
    await browser.searchAudit();
    expect(api.auditSearch).toHaveBeenCalledExactlyOnceWith({
      operationPrefix: " delete.λ ", result: "failed", bookmarkId: "", beforeId: 0, limit: 50,
    });
    expect(browser.auditLoaded).toBe(true);
  });

  it.each([{ initial: [] }, { initial: [audit(22)] }])("retains same-query rows through a failed refresh: $initial", async ({ initial }) => {
    const api = newFakeApi();
    const pending = deferred<AuditEventDTO[]>();
    api.auditSearch = vi.fn().mockResolvedValueOnce(initial)
      .mockReturnValueOnce(pending.promise).mockResolvedValueOnce([audit(33)]);
    const browser = new BrowserState(api);
    await browser.searchAudit();
    const refresh = browser.searchAudit();
    expect(browser.auditEvents).toEqual(initial);
    expect(browser.auditLoaded).toBe(true);
    expect(browser.auditLoading).toBe(true);
    pending.reject(new Error("refresh unavailable"));
    await refresh;
    expect(browser.auditEvents).toEqual(initial);
    expect(browser.auditLoaded).toBe(true);
    expect(browser.auditError).toBe(queryError);
    expect(browser.auditCanMore).toBe(false);
    await browser.searchAudit();
    expect(ids(browser.auditEvents)).toEqual([33]);
    expect(browser.auditError).toBeNull();
  });

  it.each(["success", "failure"])("ignores an older %s and finally during a newer same-query refresh", async (outcome) => {
    const api = newFakeApi();
    const older = deferred<AuditEventDTO[]>();
    const newer = deferred<AuditEventDTO[]>();
    api.auditSearch = vi.fn().mockResolvedValueOnce(page())
      .mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);
    const browser = new BrowserState(api);
    await browser.searchAudit();
    const first = browser.searchAudit();
    const second = browser.searchAudit();
    if (outcome === "success") older.resolve([]);
    else older.reject(new Error("obsolete private error"));
    await first;
    expect(browser.auditLoading).toBe(true);
    expect(browser.auditError).toBeNull();
    expect(browser.auditExhausted).toBe(false);
    expect(browser.auditEvents).toEqual(page());
    newer.resolve([audit(101)]);
    await second;
    expect(ids(browser.auditEvents)).toEqual([101]);
    expect(browser.auditLoading).toBe(false);
  });

  it.each(["success", "failure"])("ignores an older %s after the newer query has finished", async (outcome) => {
    const api = newFakeApi();
    const older = deferred<AuditEventDTO[]>();
    api.auditSearch = vi.fn().mockReturnValueOnce(older.promise).mockResolvedValueOnce([audit(22)]);
    const browser = new BrowserState(api);
    const first = browser.searchAudit();
    await browser.searchAudit();
    if (outcome === "success") older.resolve([audit(11)]);
    else older.reject(new Error("obsolete private error"));
    await first;
    expect(ids(browser.auditEvents)).toEqual([22]);
    expect(browser.auditError).toBeNull();
    expect(browser.auditLoading).toBe(false);
  });

  it.each([
    ["auditFilterOp", "copy.", "delete."],
    ["auditFilterResult", "applied", "failed"],
  ] as const)("retires old rows synchronously when %s changes", async (field, previous, next) => {
    const api = newFakeApi();
    api.auditSearch = vi.fn(async () => page());
    const browser = new BrowserState(api);
    browser[field] = previous;
    await browser.searchAudit();
    browser[field] = next;
    expect(browser.auditEvents).toEqual([]);
    expect(browser.auditLoaded).toBe(false);
    expect(browser.auditError).toBeNull();
    expect(browser.auditExhausted).toBe(false);
    await browser.searchAudit(true);
    expect(api.auditSearch).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["auditFilterOp", "copy.", "delete."],
    ["auditFilterResult", "applied", "failed"],
  ] as const)("retires pending replies when %s changes away and back without another search", async (field, previous, next) => {
    const api = newFakeApi();
    const pending = deferred<AuditEventDTO[]>();
    api.auditSearch = vi.fn(() => pending.promise);
    const browser = new BrowserState(api);
    browser[field] = previous;
    const first = browser.searchAudit();
    browser[field] = next;
    browser[field] = previous;
    pending.resolve([audit(11)]);
    await first;
    expect(api.auditSearch).toHaveBeenCalledTimes(1);
    expect(browser.auditEvents).toEqual([]);
    expect(browser.auditLoaded).toBe(false);
    expect(browser.auditLoading).toBe(false);
    expect(browser.auditError).toBeNull();
  });

  it("ignores failure after a filter edit that has not dispatched a replacement read", async () => {
    const api = newFakeApi();
    const pending = deferred<AuditEventDTO[]>();
    api.auditSearch = vi.fn(() => pending.promise);
    const browser = new BrowserState(api);
    const first = browser.searchAudit();
    browser.auditFilterResult = "failed";
    pending.reject(new Error("old scope"));
    await first;
    expect(browser.auditLoaded).toBe(false);
    expect(browser.auditLoading).toBe(false);
    expect(browser.auditError).toBeNull();
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
    await browser.searchAudit();
    expect(ids(browser.auditEvents)).toEqual(ids(initial));
    expect(browser.auditExhausted).toBe(false);
    const first = browser.searchAudit(true);
    const second = browser.searchAudit(true);
    expect(browser.auditMoreLoading).toBe(true);
    expect(browser.auditCanMore).toBe(false);
    pending.resolve([audit(50), audit(49)]);
    await Promise.all([first, second]);
    expect.soft(api.auditSearch).toHaveBeenCalledTimes(2);
    expect.soft(api.auditSearch).toHaveBeenNthCalledWith(2, {
      operationPrefix: "", result: "", bookmarkId: "", beforeId: 51, limit: 50,
    });
    expect(ids(browser.auditEvents)).toEqual([...ids(initial), 50, 49]);
    expect(browser.auditMoreLoading).toBe(false);
  });

  it("retries a failed page with its original cursor and accepts empty exhaustion", async () => {
    const api = newFakeApi();
    api.auditSearch = vi.fn().mockResolvedValueOnce(page())
      .mockRejectedValueOnce(new Error("private page details"))
      .mockResolvedValueOnce(page(50)).mockResolvedValueOnce([]);
    const browser = new BrowserState(api);
    await browser.searchAudit();
    await browser.searchAudit(true);
    expect(browser.auditEvents).toEqual(page());
    expect(browser.auditMoreError).toBe(pageError);
    expect(browser.auditError).toBeNull();
    expect(browser.auditExhausted).toBe(false);
    expect(browser.auditCanMore).toBe(true);
    await browser.searchAudit(true);
    expect(browser.auditEvents).toEqual([...page(), ...page(50)]);
    expect(browser.auditMoreError).toBeNull();
    expect(vi.mocked(api.auditSearch).mock.calls[1]).toEqual(vi.mocked(api.auditSearch).mock.calls[2]);
    await browser.searchAudit(true);
    expect(browser.auditEvents).toEqual([...page(), ...page(50)]);
    expect(browser.auditExhausted).toBe(true);
    expect(browser.auditCanMore).toBe(false);
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
    await browser.searchAudit();
    const first = browser.searchAudit(true);
    if (replacement === "filter") browser.auditFilterResult = "failed";
    const second = browser.searchAudit();
    if (outcome === "success") older.resolve([audit(50)]);
    else older.reject(new Error("old page failure"));
    await first;
    expect(browser.auditLoading).toBe(true);
    expect(browser.auditError).toBeNull();
    expect(browser.auditMoreError).toBeNull();
    expect(browser.auditEvents).toEqual(replacement === "filter" ? [] : page());
    newer.resolve([audit(222)]);
    await second;
    expect(ids(browser.auditEvents)).toEqual([222]);
    expect(browser.auditMoreLoading).toBe(false);
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
    await browser.searchAudit();
    await browser.searchAudit(true);
    expect(browser.auditEvents).toEqual(page(200));
    expect(browser.auditMoreError).toBe(pageError);
    expect(browser.auditExhausted).toBe(false);
    await browser.searchAudit(true);
    expect(browser.auditEvents).toEqual([...page(200), audit(150)]);
    expect(vi.mocked(api.auditSearch).mock.calls[1]).toEqual(vi.mocked(api.auditSearch).mock.calls[2]);
  });

  it.each([
    [audit(2), audit(2)], [audit(1), audit(2)], [audit(0)],
    Array.from({ length: 51 }, (_, index) => audit(100 - index)),
  ])("rejects a malformed initial page without a successful-empty claim: %j", async (...invalid) => {
    const api = newFakeApi();
    api.auditSearch = vi.fn(async () => invalid);
    const browser = new BrowserState(api);
    await browser.searchAudit();
    expect(browser.auditEvents).toEqual([]);
    expect(browser.auditLoaded).toBe(false);
    expect(browser.auditError).toBe(queryError);
    expect(browser.auditExhausted).toBe(false);
  });
});

describe("independent Recent audit cache", () => {
  it.each([{ initial: [] }, { initial: [audit(30)] }])("labels a failed Recent refresh unavailable while retaining $initial", async ({ initial }) => {
    const api = newFakeApi();
    const pending = deferred<AuditEventDTO[]>();
    api.auditRecent = vi.fn().mockResolvedValueOnce(initial)
      .mockReturnValueOnce(pending.promise).mockResolvedValueOnce([audit(40)]);
    const browser = new BrowserState(api);
    expect(browser.recentAuditLoaded).toBe(false);
    await browser.loadAudit();
    expect(browser.recentAuditLoaded).toBe(true);
    const refresh = browser.loadAudit();
    expect(browser.recentAuditLoading).toBe(true);
    expect(browser.recentAuditEvents).toEqual(initial);
    pending.reject(new Error("secret audit details"));
    await expect(refresh).resolves.toBeUndefined();
    expect(browser.recentAuditEvents).toEqual(initial);
    expect(browser.recentAuditError).toBe(true);
    expect(browser.recentAuditLoading).toBe(false);
    expect(browser.auditLoaded).toBe(false);
    expect(browser.auditEvents).toEqual([]);
    await browser.loadAudit();
    expect(ids(browser.recentAuditEvents)).toEqual([40]);
    expect(browser.recentAuditError).toBe(false);
    expect(api.auditRecent).toHaveBeenLastCalledWith(50);
  });

  it.each(["success", "failure"])("ignores an older Recent %s and finally during a newer read", async (outcome) => {
    const api = newFakeApi();
    const older = deferred<AuditEventDTO[]>();
    const newer = deferred<AuditEventDTO[]>();
    api.auditRecent = vi.fn().mockResolvedValueOnce([audit(30)])
      .mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);
    const browser = new BrowserState(api);
    await browser.loadAudit();
    const first = browser.loadAudit();
    const second = browser.loadAudit();
    if (outcome === "success") older.resolve([]);
    else older.reject(new Error("older Recent error"));
    await first;
    expect(browser.recentAuditLoading).toBe(true);
    expect(browser.recentAuditError).toBe(false);
    expect(ids(browser.recentAuditEvents)).toEqual([30]);
    newer.resolve([audit(40)]);
    await second;
    expect(ids(browser.recentAuditEvents)).toEqual([40]);
    expect(browser.recentAuditLoading).toBe(false);
  });

  it("does not let an older Recent zero replace a newer unavailable read", async () => {
    const api = newFakeApi();
    const older = deferred<AuditEventDTO[]>();
    api.auditRecent = vi.fn().mockReturnValueOnce(older.promise)
      .mockRejectedValueOnce(new Error("current Recent error"));
    const browser = new BrowserState(api);
    const first = browser.loadAudit();
    await browser.loadAudit();
    older.resolve([]);
    await first;
    expect(browser.recentAuditLoaded).toBe(false);
    expect(browser.recentAuditError).toBe(true);
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
    browser.auditFilterOp = "delete.";
    await browser.loadAudit();
    await browser.searchAudit();
    const refresh = browser.loadAudit();
    const query = browser.searchAudit();
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
      expect(browser.auditLoading).toBe(true);
      expect(ids(browser.auditEvents)).toEqual([22]);
      await finishSearch();
    } else {
      await finishSearch();
      expect(browser.recentAuditLoading).toBe(true);
      expect(ids(browser.recentAuditEvents)).toEqual([30]);
      await finishRecent();
    }
    expect(ids(browser.auditEvents)).toEqual([searchOutcome === "success" ? 23 : 22]);
    expect(ids(browser.recentAuditEvents)).toEqual([recentOutcome === "success" ? 40 : 30]);
    expect(browser.auditError).toBe(searchOutcome === "success" ? null : queryError);
    expect(browser.recentAuditError).toBe(recentOutcome === "failure");
    expect(browser.auditLoading).toBe(false);
    expect(browser.recentAuditLoading).toBe(false);
  });

  it("refuses an oversized Recent reply without silently truncating the last loaded cache", async () => {
    const api = newFakeApi();
    api.auditRecent = vi.fn().mockResolvedValueOnce([audit(30)])
      .mockResolvedValueOnce(Array.from({ length: 51 }, (_, index) => audit(100 - index)));
    const browser = new BrowserState(api);
    await browser.loadAudit();
    await browser.loadAudit();
    expect(ids(browser.recentAuditEvents)).toEqual([30]);
    expect(browser.recentAuditError).toBe(true);
  });
});

async function connected(api: Api): Promise<BrowserState> {
  const browser = new BrowserState(api);
  const bookmark = await browser.saveBookmark({
    id: "", displayName: "Mock", authUrl: "http://mock/auth/v1.0", username: "test:tester",
    storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false,
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
    run: (browser: BrowserState) => browser.createContainer({ name: "audit-owned", policy: "gold", meta: {} }),
  },
  {
    method: "createMarker", operation: "marker.create", returnsResult: false,
    prefix: "", selected: "implied/",
    run: (browser: BrowserState) => browser.createMarker("implied/"),
  },
  {
    method: "deleteMarker", operation: "marker.delete", returnsResult: false,
    prefix: "", selected: "folder-marker/",
    run: (browser: BrowserState) => browser.deleteMarker("folder-marker/"),
  },
  {
    method: "deleteManifest", operation: "manifest.delete", returnsResult: false,
    prefix: "", selected: "external-dlo",
    run: (browser: BrowserState) => browser.deleteManifest("fixtures", "external-dlo"),
  },
  {
    method: "deleteManifestWithSegments", operation: "segments.delete", returnsResult: true,
    prefix: "", selected: "external-dlo",
    run: (browser: BrowserState) => browser.deleteManifestWithSegments({
      bookmarkId: browser.connectedBookmarkId!, container: "fixtures", name: "external-dlo",
      force: true, confirm: "external-dlo",
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
    expect(browser.recentAuditError).toBe(true);
    expect(browser.recentAuditLoaded).toBe(true);
    expect(browser.recentAuditEvents).toEqual([]);
    const recorded = await readTrail(50);
    expect(recorded).toHaveLength(1);
    expect(recorded[0]).toMatchObject({ operation: scenario.operation, result: "applied" });
    await browser.loadAudit();
    expect(api.auditRecent).toHaveBeenCalledTimes(2);
    expect(effect).toHaveBeenCalledTimes(1);
    expect(browser.recentAuditEvents).toEqual(recorded);
    expect(browser.recentAuditError).toBe(false);
    expect(await readTrail(50)).toEqual(recorded);
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
    expect(await readTrail(50)).toEqual([]);
    expect(browser.recentAuditError).toBe(false);
  });
});

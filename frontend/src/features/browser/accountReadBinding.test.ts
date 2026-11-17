import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import JobItems from "../jobs/JobItems.svelte";
import { liveApi, type FindStatusDTO, type JobDTO } from "../../lib/api";

const bridge = vi.hoisted(() => ({
  object: vi.fn(), prefix: vi.fn(), container: vi.fn(), manifest: vi.fn(), segments: vi.fn(),
  start: vi.fn(), status: vi.fn(), cancel: vi.fn(), job: vi.fn(), items: vi.fn(), totals: vi.fn(),
}));

// Exercise the production Api-to-generated-binding argument order. These
// stand-ins enforce the account checks the Go services apply (ADR 0059).
// The real Wails runtime polls for its drag handlers on a timer for five
// seconds after import, and that timer touches window; once vitest tears
// this file's environment down it throws, failing a run whose tests all
// passed. Nothing here needs the runtime: lib/api reaches it only for the
// clipboard, and the bindings are mocked below.
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() } }));
vi.mock("../../../bindings/github.com/csmart/aeronautes/internal/app", () => ({
  AppService: {}, AuditService: {}, BookmarkService: {}, BrowseService: {}, BenchmarkService: {},
  DeleteService: {}, SettingsService: {}, MutateService: {}, TransferService: {},
  InspectorService: { Object: bridge.object, Prefix: bridge.prefix, Container: bridge.container,
    SLOManifest: bridge.manifest, DLOSegments: bridge.segments },
  FindService: { StartFind: bridge.start, FindStatus: bridge.status, CancelFind: bridge.cancel },
  JobService: { Get: bridge.job, Items: bridge.items, ItemTotals: bridge.totals },
}));

const bookmark = "intended-account";
const scope = { bookmarkId: bookmark, generation: 7 };
const done: FindStatusDTO = { scanned: 1, matches: [{ name: "empty-object", bytes: 0 }],
  truncated: false, done: true, error: "", objectsPerSecond: 1, scope };
const mounted = new Set<ReturnType<typeof mount>>();

beforeEach(() => vi.resetAllMocks());
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  vi.useRealTimers();
});

function requireAccount(actual: string, expected = bookmark) {
  if (!actual || actual !== expected) throw new Error("account scope changed");
}

async function settle() {
  for (let i = 0; i < 24; i++) { await Promise.resolve(); flushSync(); }
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((yes) => { resolve = yes; });
  return { promise, resolve };
}

async function fixture() {
  const samples = newFakeApi();
  bridge.object.mockImplementation(async (account: string, container: string, name: string) => {
    requireAccount(account); return { ...await samples.objectDetail(container, name, account), scope };
  });
  bridge.prefix.mockImplementation(async (account: string, container: string, prefix: string) => {
    requireAccount(account); return { ...await samples.prefixDetail(container, prefix, account), scope };
  });
  bridge.container.mockImplementation(async (account: string, name: string) => {
    requireAccount(account); return { ...await samples.containerDetail(name, account), scope };
  });
  bridge.manifest.mockImplementation(async (account: string, container: string, name: string) => {
    requireAccount(account); return { ...await samples.sloManifest(container, name, account), scope };
  });
  bridge.segments.mockImplementation(async (account: string, manifest: string) => {
    requireAccount(account); return { ...await samples.dloSegments(manifest, account), scope };
  });
  bridge.start.mockImplementation(async (account: string) => { requireAccount(account); return "scan"; });
  bridge.status.mockImplementation(async (account: string) => { requireAccount(account); return done; });
  bridge.cancel.mockImplementation(async (account: string) => { requireAccount(account); });
  const api = { ...samples, objectDetail: liveApi.objectDetail, prefixDetail: liveApi.prefixDetail,
    containerDetail: liveApi.containerDetail, sloManifest: liveApi.sloManifest, dloSegments: liveApi.dloSegments,
    startFind: liveApi.startFind, findStatus: liveApi.findStatus, cancelFind: liveApi.cancelFind,
    objects: async (...args: Parameters<typeof samples.objects>) => ({ ...await samples.objects(...args), scope }) };
  const state = new BrowserState(api);
  state.connectedBookmarkId = bookmark;
  state.account = { name: "AUTH_fixture", containers: 1, objects: 4, bytesUsed: 1,
    quotaBytes: 0, meta: {}, tempUrlKeySlots: [], policyUsage: [], rawHeaders: {}, scope, editableFingerprint: "" };
  await state.openContainer("fixtures");
  await settle();
  return { state, samples };
}

describe("inspector reads through the live bridge", () => {
  it.each(["empty-object", "implied/", "external-slo", "external-dlo"])("reads %s under the intended account", async (name) => {
    const { state } = await fixture();
    expect(state.containerDetail?.scope).toEqual(scope);
    state.select(state.entries.findIndex((row) => row.name === name));
    await settle();
    expect(state.detailError).toBeNull();
    if (name === "implied/") expect(state.prefixDetail?.scope).toEqual(scope);
    else expect(state.objectDetail?.scope).toEqual(scope);
    if (name === "external-slo") expect(state.sloManifest?.entries?.length).toBeGreaterThan(0);
    if (name === "external-dlo") expect(state.dloSegments?.segments?.length).toBeGreaterThan(0);
  });

  it.each(["object", "prefix", "container", "manifest", "segments"] as const)("discards a mismatched %s reply before publishing its payload", async (kind) => {
    const { state, samples } = await fixture();
    const other = { bookmarkId: "other-account", generation: 7 };
    const name = kind === "prefix" ? "implied/" : kind === "manifest" ? "external-slo"
      : kind === "segments" ? "external-dlo" : "empty-object";
    if (kind === "object") bridge.object.mockResolvedValue({ ...await samples.objectDetail("fixtures", name, bookmark), scope: other });
    if (kind === "prefix") bridge.prefix.mockResolvedValue({ ...await samples.prefixDetail("fixtures", name, bookmark), scope: other });
    if (kind === "container") bridge.container.mockResolvedValue({ ...await samples.containerDetail("fixtures", bookmark), scope: other });
    if (kind === "manifest") bridge.manifest.mockResolvedValue({ ...await samples.sloManifest("fixtures", name, bookmark), scope: other });
    if (kind === "segments") bridge.segments.mockResolvedValue({ ...await samples.dloSegments("external-dlo-segments/wedding-video/", bookmark), scope: other });
    if (kind === "container") await state.refreshContainerDetail();
    else { state.select(state.entries.findIndex((row) => row.name === name)); await settle(); }
    const payload = { object: state.objectDetail, prefix: state.prefixDetail, container: state.containerDetail,
      manifest: state.sloManifest, segments: state.dloSegments }[kind];
    expect(bridge[kind]).toHaveBeenCalled();
    expect(payload).toBeNull();
    expect(state.detailLoading).toBe(false);
  });

  it("does not continue an old object read into a new account's segment table", async () => {
    const { state, samples } = await fixture();
    const reply = deferred<Awaited<ReturnType<typeof samples.objectDetail>>>();
    bridge.object.mockImplementation(() => reply.promise);
    state.select(state.entries.findIndex((row) => row.name === "external-slo"));
    state.connectedBookmarkId = "other-account";
    reply.resolve({ ...await samples.objectDetail("fixtures", "external-slo", bookmark), scope });
    await settle();
    expect(bridge.object).toHaveBeenCalledWith(bookmark, "fixtures", "external-slo");
    expect(bridge.manifest).not.toHaveBeenCalled();
    expect(state.objectDetail).toBeNull();
    expect(state.sloManifest).toBeNull();
  });
});

describe("Find reads through the live bridge", () => {
  it("uses the captured account for startup, polling and cancellation", async () => {
    const { state } = await fixture();
    await state.startFind("literal query");
    expect(state.findStatus).toEqual(done);
    expect(bridge.start).toHaveBeenCalledWith(bookmark, "fixtures", "", "literal query", false);
    expect(bridge.status).toHaveBeenCalledWith(bookmark, "scan");
    await state.cancelFind();
    expect(bridge.cancel).toHaveBeenCalledWith(bookmark, "scan");
    expect(state.findNotice).toBe("Cancellation requested.");
  });

  it("cancels a late startup under its original account after navigation", async () => {
    const { state } = await fixture();
    const start = deferred<string>();
    bridge.start.mockImplementation(() => start.promise);
    const finding = state.startFind("query");
    await settle();
    state.connectedBookmarkId = "new-account";
    start.resolve("late-scan");
    await finding;
    expect(bridge.cancel).toHaveBeenCalledWith(bookmark, "late-scan");
    expect(bridge.status).not.toHaveBeenCalled();
    expect(state.findStatus).toBeNull();
  });

  it.each([undefined, null, { bookmarkId: "other-account", generation: 7 }])("refuses a poll with scope %s", async (replyScope) => {
    const { state } = await fixture();
    bridge.start.mockResolvedValue("scan");
    bridge.status.mockResolvedValue({ ...done, scope: replyScope });
    await state.startFind("query");
    expect(state.findStatus?.matches).toEqual([]);
    expect(state.findStatus?.error).toMatch(/scan does not match this connection/);
    expect(state.findRunning).toBe(false);
  });
});

it("the shared fake refuses missing inspector scope and another account's scan", async () => {
  const api = newFakeApi();
  await expect(api.objectDetail("fixtures", "empty-object", "")).rejects.toThrow(/account scope/);
  const id = await api.startFind("fixtures", "", "query", bookmark);
  await expect(api.findStatus(id, "other-account")).rejects.toThrow(/account scope/);
  await expect(api.cancelFind(id, "other-account")).rejects.toThrow(/account scope/);
  expect((await api.findStatus(id, bookmark)).scope?.bookmarkId).toBe(bookmark);
  await api.cancelFind(id, bookmark);
});

function retainedJob(type: "upload" | "download"): JobDTO {
  return { id: "old-job", state: "running", type, source: "source", destination: "destination",
    sourceBookmarkId: type === "download" ? bookmark : "", destBookmarkId: type === "upload" ? bookmark : "",
    createdAt: "2000-01-01T00:00:00Z", startedAt: "", finishedAt: "", engine: "native", settings: "",
    error: "", recoveryRecords: 0, pause: "", counters: { objectsPlanned: 0, objectsCopied: 0, objectsVerified: 0,
      objectsFailed: 0, bytesPlanned: 0, bytesCopied: 0, overwrites: 0, overwritesRefused: 0, objectsSame: 0,
      nativeRouted: 0, objectsDeleted: 0 } };
}

describe("retained local job reads through the live bridge", () => {
  it.each(["upload", "download"] as const)("reads and pages a %s job while disconnected and browsing elsewhere", async (type) => {
    vi.useFakeTimers();
    const job = retainedJob(type);
    bridge.items.mockImplementation(async (account: string, id: string, cursor: string) => {
      requireAccount(account); expect(id).toBe(job.id);
      return { status: "available", items: [], nextCursor: cursor ? "" : "page-2" };
    });
    bridge.totals.mockImplementation(async (account: string, id: string) => {
      requireAccount(account); expect(id).toBe(job.id);
      return { status: "available", items: 0, planned: 0, admitted: 0, failed: 0, conflicts: 0,
        refused: 0, unresolved: 0, unverified: 0, problems: 0 };
    });
    bridge.job.mockImplementation(async (account: string, id: string) => {
      requireAccount(account); expect(id).toBe(job.id); return job;
    });
    const state = new BrowserState({ ...newFakeApi(), job: liveApi.job, jobItems: liveApi.jobItems,
      jobItemTotals: liveApi.jobItemTotals });
    mounted.add(mount(JobItems, { target: document.body, props: { browser: state, job } }));
    await settle();
    expect(document.querySelector('[role="alert"]')).toBeNull();
    expect(document.body.textContent).toContain("No item records have been saved");
    state.connectedBookmarkId = "unrelated-account";
    const next = [...document.querySelectorAll("button")].find((button) => button.textContent === "Next item page")!;
    expect(next.disabled).toBe(false);
    next.click(); await settle();
    expect(bridge.items).toHaveBeenLastCalledWith(bookmark, job.id, "page-2", 100);
    await vi.advanceTimersByTimeAsync(1500); await settle();
    expect(bridge.job).toHaveBeenCalledWith(bookmark, job.id);
    expect(document.querySelector('[role="alert"]')).toBeNull();
    bridge.job.mockResolvedValue({ ...job, sourceBookmarkId: "other", destBookmarkId: "other" });
    await vi.advanceTimersByTimeAsync(1500); await settle();
    expect(document.querySelector('[role="alert"]')?.textContent).toContain("unavailable");
    expect(document.querySelector(".totals")).toBeNull();
  });
});

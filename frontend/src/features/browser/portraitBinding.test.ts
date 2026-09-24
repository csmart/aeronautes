import { beforeEach, expect, it, vi } from "vitest";
import { BrowserState } from "./browserState.svelte";
import { liveApi } from "../../lib/api";

const bridge = vi.hoisted(() => ({ connect: vi.fn(), capabilities: vi.fn(), containers: vi.fn(), recent: vi.fn() }));
vi.mock("../../../bindings/github.com/csmart/aeronautes/internal/app", () => ({
  AppService: {}, BookmarkService: {}, BenchmarkService: {}, DeleteService: {}, SettingsService: {},
  MutateService: {}, TransferService: {}, InspectorService: {}, FindService: {}, JobService: {},
  BrowseService: { Connect: bridge.connect, Capabilities: bridge.capabilities, Containers: bridge.containers },
  AuditService: { RecentFor: bridge.recent },
}));

const alpha = { bookmarkId: "alpha", generation: 1 };
const beta = { bookmarkId: "beta", generation: 1 };
const replaced = { bookmarkId: "alpha", generation: 2 };
const rows = [{ name: "only-alpha", count: 1, bytes: 11, class: "normal" }];
function portrait(scope: unknown = alpha) {
  return { name: "AUTH_alpha", containers: 11, objects: 110, bytesUsed: 1100,
    quotaBytes: 0, meta: {}, tempUrlKeySlots: [], scope };
}
function capabilities(scope: unknown = alpha) {
  return { source: "advertised", maxFileSize: 111, storagePolicies: [{ name: "alpha", default: true }],
    versioningFlags: [], sloSupported: false, scope };
}

beforeEach(() => {
  vi.resetAllMocks();
  bridge.connect.mockResolvedValue(portrait());
  bridge.capabilities.mockResolvedValue(capabilities());
  bridge.containers.mockResolvedValue({ rows, nextMarker: "more-alpha", effectiveLimit: 1000, scope: alpha });
  bridge.recent.mockResolvedValue([]);
});

function expectUnpublished(state: BrowserState) {
  expect(state.connectedBookmarkId).toBeNull();
  expect(state.account).toBeNull();
  expect(state.capabilities).toBeNull();
  expect(state.containers).toEqual([]);
  expect(state.containersMarker).toBe("");
  expect(state.connecting).toBe(false);
  expect(state.connectError).not.toBeNull();
}

it.each(["advertised", "unknown"])("publishes a matching portrait with %s capabilities", async (source) => {
  const caps = source === "advertised" ? capabilities() : {
    source, maxFileSize: 0, storagePolicies: [], versioningFlags: [], sloSupported: false, scope: alpha,
  };
  bridge.capabilities.mockResolvedValue(caps);
  const state = new BrowserState(liveApi);
  expect(await state.connect("alpha")).toBe(true);
  expect(state.account).toEqual(portrait());
  expect(state.capabilities).toEqual(caps);
  expect(state.connectedBookmarkId).toBe("alpha");
  expect(state.containers).toEqual(rows);
  expect(state.containersMarker).toBe("more-alpha");
  expect(state.containersError).toBeNull();
});

it("passes the connecting bookmark through the production capability adapter", async () => {
  const state = new BrowserState(liveApi);
  await state.connect("alpha");
  expect(bridge.connect).toHaveBeenCalledWith("alpha");
  expect(bridge.capabilities).toHaveBeenCalledWith("alpha");
});

it("refuses an empty connecting bookmark before calling the bridge", async () => {
  const state = new BrowserState(liveApi);
  await expect(state.connect("")).rejects.toThrow("Choose a bookmark");
  expectUnpublished(state);
  expect(bridge.connect).not.toHaveBeenCalled();
});

it.each([
  { name: "missing", scope: null },
  { name: "missing generation", scope: { bookmarkId: "alpha" } },
  { name: "non-numeric generation", scope: { ...alpha, generation: "1" } },
  { name: "another bookmark", scope: beta },
  { name: "zero generation", scope: { ...alpha, generation: 0 } },
  { name: "negative generation", scope: { ...alpha, generation: -1 } },
  { name: "fractional generation", scope: { ...alpha, generation: 1.5 } },
  { name: "unsafe generation", scope: { ...alpha, generation: Number.MAX_SAFE_INTEGER + 1 } },
])("rejects a portrait with $name scope before dependent reads", async ({ scope }) => {
  bridge.connect.mockResolvedValue(portrait(scope));
  const state = new BrowserState(liveApi);
  await expect(state.connect("alpha")).rejects.toThrow();
  expectUnpublished(state);
  expect(bridge.capabilities).not.toHaveBeenCalled();
  expect(bridge.containers).not.toHaveBeenCalled();
});

it.each([
  { name: "missing", scope: null },
  { name: "another bookmark", scope: beta },
  { name: "another generation of the same bookmark", scope: replaced },
])("rejects capabilities with $name scope without publishing the attempt", async ({ scope }) => {
  bridge.capabilities.mockResolvedValue({ ...capabilities(scope), maxFileSize: 222 });
  bridge.containers.mockRejectedValue(new Error("the connected account changed; the listing was not read"));
  const state = new BrowserState(liveApi);
  await expect(state.connect("alpha")).rejects.toThrow();
  expectUnpublished(state);
  expect(bridge.containers).not.toHaveBeenCalled();
});

it("keeps a refused capability read distinct from scoped unknown support", async () => {
  const refusal = new Error("the connected account changed");
  bridge.capabilities.mockRejectedValue(refusal);
  const state = new BrowserState(liveApi);
  await expect(state.connect("alpha")).rejects.toBe(refusal);
  expectUnpublished(state);
  expect(bridge.containers).not.toHaveBeenCalled();
});

it.each([
  { name: "missing", scope: null },
  { name: "another bookmark", scope: beta },
  { name: "another generation of the same bookmark", scope: replaced },
])("hides initial rows with $name scope while retaining the accepted pair", async ({ scope }) => {
  bridge.containers.mockResolvedValue({ rows: [{ ...rows[0], name: "wrong-account" }], nextMarker: "wrong-marker", effectiveLimit: 1000, scope });
  const state = new BrowserState(liveApi);
  expect(await state.connect("alpha")).toBe(true);
  expect(state.account).toEqual(portrait());
  expect(state.capabilities).toEqual(capabilities());
  expect(state.connectedBookmarkId).toBe("alpha");
  expect(state.containers).toEqual([]);
  expect(state.containersMarker).toBe("");
  expect(state.containersError).toMatch(/not shown/);
});

it("retains the bound pair when the initial listing itself refuses", async () => {
  const refusal = new Error("the connected account changed; the listing was not read");
  bridge.containers.mockRejectedValue(refusal);
  const state = new BrowserState(liveApi);
  expect(await state.connect("alpha")).toBe(true);
  expect(state.account).toEqual(portrait());
  expect(state.capabilities).toEqual(capabilities());
  expect(state.containers).toEqual([]);
  expect(state.containersMarker).toBe("");
  expect(state.containersError).toBe(refusal.message);
});

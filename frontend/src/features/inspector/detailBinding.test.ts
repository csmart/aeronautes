import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { ContainerEditDTO, MutationResultDTO, PrefixDetailDTO } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import Inspector from "./Inspector.svelte";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
});

async function settle() {
  // Deferred replies choose the order; no scheduling delay stands in for a barrier.
  for (let i = 0; i < 12; i++) { await Promise.resolve(); flushSync(); }
}

const scope = { bookmarkId: "source", generation: 1 };
const objectEdit = { meta: { Team: "ops" }, contentType: "", deleteAt: "" };
const containerEdit: ContainerEditDTO = {
  meta: { Team: "ops" }, readAcl: "", writeAcl: "", versioningMode: "off",
  versioningArchive: "", quotaBytes: 0, quotaCount: 0, ackPublic: false,
};

async function show() {
  const api = newFakeApi();
  const object = await api.objectDetail("fixtures", "empty-object", "source");
  const container = await api.containerDetail("fixtures", "source");
  // Echo the bookmark the caller named, as the backend does: a page states
  // the account that answered it, and the browser applies only its own.
  api.objects = vi.fn(async (bookmarkId: string, _c: string, _p: string, _m: string, _l: number) => ({ rows: [
    { kind: "object", name: "empty-object", bytes: 0, etag: "one", lastModified: "", contentType: "text/plain", sloListed: false },
    { kind: "object", name: "other", bytes: 1, etag: "two", lastModified: "", contentType: "text/plain", sloListed: false },
    { kind: "prefix", name: "implied/", bytes: 0, etag: "", lastModified: "", contentType: "", sloListed: false },
    { kind: "prefix", name: "other/", bytes: 0, etag: "", lastModified: "", contentType: "", sloListed: false },
  ], nextMarker: "", scope: { bookmarkId, generation: 1 } }));
  api.objectDetail = vi.fn(async (at, name) => ({ ...object, name,
    editableFingerprint: `${at}/${name}`, meta: { Source: `${at}/${name}` } }));
  api.containerDetail = vi.fn(async (name) => ({ ...container, name,
    editableFingerprint: name, meta: { Source: name } }));
  api.prefixDetail = vi.fn(async (_at, prefix, bookmarkId) => ({ prefix, markerBacked: false, selfObject: null, scope: { bookmarkId, generation: 1 } }));
  api.connect = vi.fn().mockRejectedValue(new Error("reconnect failed"));
  api.auditRecent = vi.fn().mockResolvedValue([]);
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source";
  await browser.openContainer("fixtures");
  browser.select(0);
  await settle();
  const root = document.createElement("div");
  document.body.append(root);
  mounted.add(mount(Inspector, { target: root, props: { browser } }));
  flushSync();
  return { api, browser, root, object, container };
}

type Fixture = Awaited<ReturnType<typeof show>>;
type Mutation = "object" | "container" | "marker";
function portrait(f: Fixture, kind: Mutation) {
  return kind === "object" ? f.browser.objectDetail
    : kind === "container" ? f.browser.containerDetail : f.browser.prefixDetail;
}

async function prepare(f: Fixture, kind: Mutation) {
  f.browser.select(kind === "marker" ? 2 : kind === "container" ? -1 : 0);
  await settle();
}

function mutation(f: Fixture, kind: Mutation, label: string) {
  const result = deferred<MutationResultDTO>();
  const marker = deferred<PrefixDetailDTO>();
  f.api.updateObjectMetadata = vi.fn(() => result.promise);
  f.api.updateContainer = vi.fn(() => result.promise);
  f.api.createMarker = vi.fn(() => marker.promise);
  const returned: MutationResultDTO = {
    outcome: "applied", mismatch: "", limitation: "", transIds: [label],
    objectAfter: { ...f.object, name: "empty-object", meta: { Completion: label } },
    containerAfter: { ...f.container, name: "fixtures", meta: { Completion: label } },
  };
  const task = kind === "object" ? f.browser.saveObjectMetadata(objectEdit)
    : kind === "container" ? f.browser.saveContainer(containerEdit) : f.browser.createMarker("implied/");
  return { task, returned,
    resolve: () => kind === "marker"
      ? marker.resolve({ prefix: "implied/", markerBacked: true, selfObject: { ...f.object, name: "implied/", meta: { Completion: label } }, scope })
      : result.resolve(returned),
    reject: () => kind === "marker" ? marker.reject(new Error(label)) : result.reject(new Error(label)),
  };
}

const changes = ["selection", "container", "container return", "bookmark", "bookmark return",
  "prefix", "prefix return", "reconnect", "refresh"] as const;
async function change(f: Fixture, kind: Mutation, field: typeof changes[number]) {
  const b = f.browser;
  if (field === "selection") {
    if (kind === "container") await b.openContainer("elsewhere");
    else b.select(kind === "object" ? 1 : 3);
  } else if (field.startsWith("container")) {
    await b.openContainer("elsewhere");
    if (field.endsWith("return")) await b.openContainer("fixtures");
    b.select(kind === "marker" ? 2 : kind === "container" ? -1 : 0);
  } else if (field.startsWith("bookmark")) {
    b.connectedBookmarkId = "other-account";
    if (field.endsWith("return")) b.connectedBookmarkId = "source";
  } else if (field.startsWith("prefix")) {
    b.prefix = "elsewhere/";
    if (field.endsWith("return")) b.prefix = "";
  } else if (field === "reconnect") {
    await expect(b.connect("source")).rejects.toThrow("reconnect failed");
  } else {
    if (kind === "container") await b.refreshContainerDetail();
    else b.refreshDetail();
  }
  await settle();
}

describe.each(["object", "container", "marker"] as const)("%s mutation result ownership", (kind) => {
  it.each(changes)("ignores a result after %s while preserving completion", async (field) => {
    const f = await show();
    await prepare(f, kind);
    const old = mutation(f, kind, "obsolete");
    await change(f, kind, field);
    const current = portrait(f, kind);
    old.resolve();
    const returned = await old.task;
    await settle();
    expect(portrait(f, kind)).toEqual(current);
    expect(f.root.textContent).not.toContain("obsolete");
    if (kind !== "marker") expect(returned).toEqual(old.returned);
    expect(f.api.auditRecent).toHaveBeenCalledOnce();
  });

  it.each([false, true])("keeps the newest same-resource mutation, old first=%s", async (oldFirst) => {
    const f = await show();
    await prepare(f, kind);
    const initial = portrait(f, kind);
    const old = mutation(f, kind, "obsolete");
    const current = mutation(f, kind, "current");
    if (oldFirst) {
      old.resolve(); await old.task;
      expect(portrait(f, kind)).toEqual(initial);
    }
    current.resolve(); await current.task;
    const accepted = portrait(f, kind);
    expect(JSON.stringify(accepted)).toContain("current");
    if (!oldFirst) { old.resolve(); await old.task; }
    expect(portrait(f, kind)).toEqual(accepted);
  });

  it("applies a current result and preserves the exact mutation arguments", async () => {
    const f = await show();
    await prepare(f, kind);
    const pending = mutation(f, kind, "accepted");
    pending.resolve(); await pending.task;
    expect(JSON.stringify(portrait(f, kind))).toContain("accepted");
    if (kind === "object") expect(f.api.updateObjectMetadata).toHaveBeenCalledExactlyOnceWith("fixtures", "empty-object", "fixtures/empty-object", objectEdit);
    if (kind === "container") expect(f.api.updateContainer).toHaveBeenCalledExactlyOnceWith("fixtures", "fixtures", containerEdit);
    if (kind === "marker") expect(f.api.createMarker).toHaveBeenCalledExactlyOnceWith("fixtures", "implied/");
  });

  it("returns a current failure without inventing a successful portrait", async () => {
    const f = await show();
    await prepare(f, kind);
    const before = portrait(f, kind);
    const pending = mutation(f, kind, "write failed");
    const assertion = expect(pending.task).rejects.toThrow("write failed");
    pending.reject(); await assertion;
    expect(portrait(f, kind)).toEqual(before);
  });
});

describe.each(["object", "container", "marker"] as const)("%s detail read ownership", (kind) => {
  it.each(changes.filter((field) => field !== "refresh"))("ignores stale read success/error/finalisation after %s", async (field) => {
    for (const fail of [false, true]) {
      const f = await show();
      await prepare(f, kind);
      const object = deferred<typeof f.object>();
      const container = deferred<typeof f.container>();
      const prefix = deferred<PrefixDetailDTO>();
      if (kind === "object") vi.mocked(f.api.objectDetail).mockImplementationOnce(() => object.promise);
      if (kind === "container") vi.mocked(f.api.containerDetail).mockImplementationOnce(() => container.promise);
      if (kind === "marker") vi.mocked(f.api.prefixDetail).mockImplementationOnce(() => prefix.promise);
      const pending = kind === "container" ? f.browser.refreshContainerDetail() : Promise.resolve(f.browser.refreshDetail());
      await change(f, kind, field);
      const current = portrait(f, kind);
      const loading = f.browser.detailLoading;
      const error = f.browser.detailError;
      const reply = kind === "object" ? object : kind === "container" ? container : prefix;
      if (fail) reply.reject(new Error("obsolete read"));
      else if (kind === "object") object.resolve({ ...f.object, meta: { Completion: "obsolete" } });
      else if (kind === "container") container.resolve({ ...f.container, meta: { Completion: "obsolete" } });
      else prefix.resolve({ prefix: "implied/", markerBacked: true, selfObject: null, scope });
      await pending; await settle();
      expect(portrait(f, kind)).toEqual(current);
      expect(f.browser.detailLoading).toBe(loading);
      expect(f.browser.detailError).toBe(error);
    }
  });

});

it.each([false, true])("a newer container mutation supersedes a pending read, old first=%s", async (oldFirst) => {
  const f = await show();
  await prepare(f, "container");
  const initial = f.browser.containerDetail;
  const read = deferred<typeof f.container>();
  vi.mocked(f.api.containerDetail).mockImplementationOnce(() => read.promise);
  const refresh = f.browser.refreshContainerDetail();
  const save = mutation(f, "container", "current");
  if (oldFirst) {
    read.resolve({ ...f.container, meta: { Completion: "obsolete" } }); await refresh;
    expect(f.browser.containerDetail).toEqual(initial);
  }
  save.resolve(); await save.task;
  if (!oldFirst) { read.resolve({ ...f.container, meta: { Completion: "obsolete" } }); await refresh; }
  expect(f.browser.containerDetail?.meta?.Completion).toBe("current");
});

it.each([false, true])("a repeated open owns container HEAD in either completion order, old first=%s", async (oldFirst) => {
  const f = await show();
  const old = deferred<typeof f.container>();
  const current = deferred<typeof f.container>();
  vi.mocked(f.api.containerDetail).mockImplementationOnce(() => old.promise).mockImplementationOnce(() => current.promise);
  await f.browser.openContainer("fixtures");
  await f.browser.openContainer("fixtures");
  if (oldFirst) { old.resolve({ ...f.container, meta: { Completion: "obsolete" } }); await settle(); expect(f.browser.containerDetail).toBeNull(); }
  current.resolve({ ...f.container, meta: { Completion: "current" } }); await settle();
  if (!oldFirst) { old.resolve({ ...f.container, meta: { Completion: "obsolete" } }); await settle(); }
  expect(f.browser.containerDetail?.meta?.Completion).toBe("current");
});

it.each(["object", "container", "marker"] as const)("refuses a mismatched %s working copy before mutation", async (kind) => {
  const f = await show();
  await prepare(f, kind);
  const result = { outcome: "conflict", objectAfter: null, containerAfter: null, mismatch: "", transIds: [] };
  f.api.updateObjectMetadata = vi.fn().mockResolvedValue(result);
  f.api.updateContainer = vi.fn().mockResolvedValue(result);
  f.api.createMarker = vi.fn().mockResolvedValue(f.browser.prefixDetail);
  if (kind === "object") f.browser.objectDetail = { ...f.browser.objectDetail!, name: "wrong" };
  if (kind === "container") f.browser.containerDetail = { ...f.browser.containerDetail!, name: "wrong" };
  if (kind === "marker") f.browser.prefixDetail = { prefix: "wrong/", markerBacked: false, selfObject: null, scope };
  const task = kind === "object" ? f.browser.saveObjectMetadata(objectEdit)
    : kind === "container" ? f.browser.saveContainer(containerEdit) : f.browser.createMarker("implied/");
  const error = await task.then(() => null, (cause: unknown) => cause);
  expect(f.api.updateObjectMetadata).not.toHaveBeenCalled();
  expect(f.api.updateContainer).not.toHaveBeenCalled();
  expect(f.api.createMarker).not.toHaveBeenCalled();
  expect(error).toBeInstanceOf(Error);
  expect((error as Error).message).toMatch(/current|selected|open/);
});

it.each(["slo", "dlo"] as const)("drops a late %s subordinate response after reconnect", async (kind) => {
  const f = await show();
  const slo = deferred<Awaited<ReturnType<typeof f.api.sloManifest>>>();
  const dlo = deferred<Awaited<ReturnType<typeof f.api.dloSegments>>>();
  f.api.sloManifest = vi.fn(() => slo.promise); f.api.dloSegments = vi.fn(() => dlo.promise);
  vi.mocked(f.api.objectDetail).mockResolvedValue({ ...f.object, class: kind, manifest: "parts/data/" });
  f.browser.refreshDetail(); await settle();
  await expect(f.browser.connect("source")).rejects.toThrow("reconnect failed");
  if (kind === "slo") slo.resolve({ entries: [], scope });
  else dlo.resolve({ container: "parts", prefix: "data/", segments: [], hasMore: false, previewBytes: 0, scope });
  await settle();
  expect(f.browser.sloManifest).toBeNull(); expect(f.browser.dloSegments).toBeNull();
});

it("keeps the selected object's complete working copy for a later save", async () => {
  const f = await show();
  const old = mutation(f, "object", "obsolete");
  f.browser.select(1); await settle();
  old.resolve(); await old.task; await settle();
  expect(f.root.querySelector(".object-title")?.textContent).toBe("other");
  expect(f.browser.objectDetail?.meta).toEqual({ Source: "fixtures/other" });
  f.api.updateObjectMetadata = vi.fn().mockResolvedValue({
    outcome: "applied", objectAfter: f.browser.objectDetail, containerAfter: null, mismatch: "", transIds: [],
  });
  const edit = { meta: { ...f.browser.objectDetail!.meta, Team: "new" }, contentType: "", deleteAt: "" };
  await f.browser.saveObjectMetadata(edit);
  expect(f.api.updateObjectMetadata).toHaveBeenCalledExactlyOnceWith("fixtures", "other", "fixtures/other", edit);
});

it.each(["object", "container"] as const)("preserves current %s conflict and mismatch outcomes", async (kind) => {
  for (const outcome of ["conflict", "verify_mismatch"]) {
    const f = await show();
    await prepare(f, kind);
    const before = portrait(f, kind);
    const result: MutationResultDTO = { outcome, objectAfter: null, containerAfter: null, mismatch: "changed", limitation: "", transIds: ["tx-result"] };
    f.api.updateObjectMetadata = vi.fn().mockResolvedValue(result);
    f.api.updateContainer = vi.fn().mockResolvedValue(result);
    const actual = kind === "object" ? await f.browser.saveObjectMetadata(objectEdit) : await f.browser.saveContainer(containerEdit);
    expect(actual).toEqual(result);
    expect(portrait(f, kind)).toEqual(before);
    expect(f.api.auditRecent).toHaveBeenCalledOnce();
  }
});

it.each(["object", "container", "marker"] as const)("refuses a %s working copy from a previous connection", async (kind) => {
  for (const changeConnection of ["bookmark return", "reconnect"] as const) {
    const f = await show();
    await prepare(f, kind);
    await change(f, kind, changeConnection);
    const result = { outcome: "conflict", objectAfter: null, containerAfter: null, mismatch: "", transIds: [] };
    f.api.updateObjectMetadata = vi.fn().mockResolvedValue(result);
    f.api.updateContainer = vi.fn().mockResolvedValue(result);
    f.api.createMarker = vi.fn().mockResolvedValue(f.browser.prefixDetail);
    const task = kind === "object" ? f.browser.saveObjectMetadata(objectEdit, true)
      : kind === "container" ? f.browser.saveContainer(containerEdit, true) : f.browser.createMarker("implied/");
    const error = await task.then(() => null, (cause: unknown) => cause);
    expect(f.api.updateObjectMetadata).not.toHaveBeenCalled();
    expect(f.api.updateContainer).not.toHaveBeenCalled(); expect(f.api.createMarker).not.toHaveBeenCalled();
    expect(error).toBeInstanceOf(Error);
    expect((error as Error).message).toMatch(/current/);
  }
});

it("allows the same container working copy after navigating into a prefix", async () => {
  const f = await show();
  await f.browser.goTo("nested/");
  const save = mutation(f, "container", "current");
  save.resolve(); await save.task;
  expect(f.browser.containerDetail?.meta?.Completion).toBe("current");
});

it("captures container HEAD after clearing the preceding container's prefix", async () => {
  const f = await show();
  await f.browser.goTo("nested/");
  await f.browser.openContainer("elsewhere"); await settle();
  expect(f.browser.containerDetail?.name).toBe("elsewhere");
  expect(f.browser.prefix).toBe("");
});

it.each(["object", "container", "marker"] as const)("handles a current %s read failure without a stale portrait", async (kind) => {
  const f = await show();
  await prepare(f, kind);
  if (kind === "object") vi.mocked(f.api.objectDetail).mockRejectedValueOnce(new Error("current failure"));
  if (kind === "container") vi.mocked(f.api.containerDetail).mockRejectedValueOnce(new Error("current failure"));
  if (kind === "marker") vi.mocked(f.api.prefixDetail).mockRejectedValueOnce(new Error("current failure"));
  if (kind === "container") await f.browser.refreshContainerDetail();
  else f.browser.refreshDetail();
  await settle();
  expect(portrait(f, kind)).toBeNull();
  if (kind !== "container") {
    expect(f.browser.detailError).toBe("current failure");
    expect(f.browser.detailLoading).toBe(false);
  }
});

it.each(["object", "container", "marker"] as const)("rejects a current %s read with a mismatched resource name", async (kind) => {
  const f = await show();
  await prepare(f, kind);
  if (kind === "object") vi.mocked(f.api.objectDetail).mockResolvedValueOnce({ ...f.object, name: "wrong" });
  if (kind === "container") vi.mocked(f.api.containerDetail).mockResolvedValueOnce({ ...f.container, name: "wrong" });
  if (kind === "marker") vi.mocked(f.api.prefixDetail).mockResolvedValueOnce({ prefix: "wrong/", markerBacked: false, selfObject: null, scope });
  if (kind === "container") await f.browser.refreshContainerDetail();
  else f.browser.refreshDetail();
  await settle();
  expect(portrait(f, kind)).toBeNull();
  if (kind !== "container") expect(f.browser.detailError).toMatch(/does not match/);
});

describe.each(["object", "container", "marker"] as const)("overlapping %s reads", (kind) => {
  it.each([false, true].flatMap((oldFirst) => [false, true].map((fail) => ({ oldFirst, fail }))))(
    "keeps current result/error/finalisation, old first=$oldFirst failure=$fail", async ({ oldFirst, fail }) => {
      const f = await show();
      await prepare(f, kind);
      const objects = [deferred<typeof f.object>(), deferred<typeof f.object>()];
      const containers = [deferred<typeof f.container>(), deferred<typeof f.container>()];
      const prefixes = [deferred<PrefixDetailDTO>(), deferred<PrefixDetailDTO>()];
      vi.mocked(f.api.objectDetail).mockImplementationOnce(() => objects[0].promise).mockImplementationOnce(() => objects[1].promise);
      vi.mocked(f.api.containerDetail).mockImplementationOnce(() => containers[0].promise).mockImplementationOnce(() => containers[1].promise);
      vi.mocked(f.api.prefixDetail).mockImplementationOnce(() => prefixes[0].promise).mockImplementationOnce(() => prefixes[1].promise);
      const read = () => kind === "container" ? f.browser.refreshContainerDetail() : Promise.resolve(f.browser.refreshDetail());
      const old = read(); const current = read();
      const initial = portrait(f, kind);
      const finish = async (index: number) => {
        const reply = kind === "object" ? objects[index] : kind === "container" ? containers[index] : prefixes[index];
        if (index === 0 && fail) reply.reject(new Error("obsolete failure"));
        else if (kind === "object") objects[index].resolve({ ...f.object, meta: { Completion: String(index) } });
        else if (kind === "container") containers[index].resolve({ ...f.container, meta: { Completion: String(index) } });
        else prefixes[index].resolve({ prefix: "implied/", markerBacked: index === 1, selfObject: null, scope });
        await (index === 0 ? old : current); await settle();
      };
      if (oldFirst) {
        await finish(0);
        expect(portrait(f, kind)).toEqual(initial);
        if (kind !== "container") expect(f.browser.detailLoading).toBe(true);
        expect(f.browser.detailError).toBeNull();
      }
      await finish(1);
      const accepted = portrait(f, kind);
      expect(accepted).not.toBeNull();
      if (!oldFirst) await finish(0);
      expect(portrait(f, kind)).toEqual(accepted);
      expect(f.browser.detailError).toBeNull(); expect(f.browser.detailLoading).toBe(false);
    });
});

it.each(["slo", "dlo"] as const)("does not start a %s subordinate read for an obsolete HEAD", async (kind) => {
  const f = await show();
  const head = deferred<typeof f.object>();
  vi.mocked(f.api.objectDetail).mockImplementationOnce(() => head.promise);
  f.api.sloManifest = vi.fn(); f.api.dloSegments = vi.fn();
  f.browser.refreshDetail(); f.browser.connectedBookmarkId = "other-account";
  head.resolve({ ...f.object, class: kind, manifest: "parts/data/" }); await settle();
  expect(f.api.sloManifest).not.toHaveBeenCalled(); expect(f.api.dloSegments).not.toHaveBeenCalled();
  expect(f.browser.objectDetail).toBeNull();
});

it.each(["object", "container", "marker"] as const)("does not publish a current %s mutation reply naming another resource", async (kind) => {
  const f = await show();
  await prepare(f, kind);
  const initial = portrait(f, kind);
  const wrong: MutationResultDTO = {
    outcome: "applied", mismatch: "", limitation: "", transIds: ["tx-wrong"],
    objectAfter: { ...f.object, name: "wrong" }, containerAfter: { ...f.container, name: "wrong" },
  };
  f.api.updateObjectMetadata = vi.fn().mockResolvedValue(wrong);
  f.api.updateContainer = vi.fn().mockResolvedValue(wrong);
  f.api.createMarker = vi.fn().mockResolvedValue({ prefix: "wrong/", markerBacked: true, selfObject: null });
  if (kind === "object") expect(await f.browser.saveObjectMetadata(objectEdit)).toEqual(wrong);
  else if (kind === "container") expect(await f.browser.saveContainer(containerEdit)).toEqual(wrong);
  else await f.browser.createMarker("implied/");
  expect(portrait(f, kind)).toEqual(initial);
});

it.each(["slo", "dlo"] as const)("a mutation retires a pending %s subordinate error and loading state", async (kind) => {
  const f = await show();
  const subordinate = deferred<never>();
  vi.mocked(f.api.objectDetail).mockResolvedValue({ ...f.object, class: kind, manifest: "parts/data/" });
  f.api.sloManifest = vi.fn(() => subordinate.promise); f.api.dloSegments = vi.fn(() => subordinate.promise);
  f.browser.refreshDetail(); await settle();
  expect(f.browser.detailLoading).toBe(true);
  const save = mutation(f, "object", "current");
  expect(f.browser.detailLoading).toBe(false);
  subordinate.reject(new Error("obsolete subordinate failure")); await settle();
  expect(f.browser.detailError).toBeNull();
  save.resolve(); await save.task;
  expect(f.browser.objectDetail?.meta?.Completion).toBe("current");
});

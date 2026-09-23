import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { SegmentOwnershipDTO, SegmentsDeleteResultDTO, PrefixDetailDTO, Api } from "../../lib/api";
import { v1Offers } from "../../lib/v1";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import CreateContainerDialog from "../browser/CreateContainerDialog.svelte";
import ManifestDeleteDialog from "./ManifestDeleteDialog.svelte";
import Inspector from "./Inspector.svelte";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear(); document.body.innerHTML = "";
  v1Offers.markerCreate = true;
  v1Offers.markerDelete = false;
});
async function settle() {
  for (let i = 0; i < 12; i++) { await Promise.resolve(); flushSync(); }
}
function button(root: HTMLElement, label: string) {
  const found = [...root.querySelectorAll("button")].find((b) => b.textContent?.trim() === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}
function click(element: HTMLElement) { element.dispatchEvent(new MouseEvent("click", { bubbles: true })); flushSync(); }
function input(element: HTMLInputElement, value: string) {
  element.value = value; element.dispatchEvent(new Event("input", { bubbles: true })); flushSync();
}
async function setup(name = "external-dlo") {
  const api = newFakeApi();
  const browser = new BrowserState(api);
  const bookmark = await browser.saveBookmark({ id: "", displayName: "Mock", authUrl: "http://mock/auth/v1.0", username: "test:tester", storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "testing");
  await browser.connect(bookmark.id); await browser.openContainer("fixtures");
  browser.select(browser.entries.findIndex((row) => row.name === name)); await settle();
  const root = document.createElement("div"); document.body.append(root);
  const scope = { ...browser.account!.scope };
  return { api, browser, scope, root, bookmark };
}
const ownership: SegmentOwnershipDTO = { class: "dlo", target: "backing/old/", proven: false, segments: 3, inlineEntries: 0 };
const deleted: SegmentsDeleteResultDTO = { manifestDeleted: true, segmentsDeleted: 3, segmentsFailed: 0, basis: "backstage-forced" };

async function manifest() {
  const f = await setup();
  const check = deferred<SegmentOwnershipDTO>();
  f.api.checkSegmentOwnership = vi.fn(() => check.promise);
  const done = vi.fn();
  const component = mount(ManifestDeleteDialog, { target: f.root,
    props: { browser: f.browser, detail: f.browser.objectDetail!, segmentsAt: "backing/old/", onClose: done } });
  mounted.add(component); flushSync(); await settle();
  const request = { bookmarkId: f.bookmark.id, container: "fixtures", name: "external-dlo", force: false, confirm: "" };
  return { ...f, check, done, component, request };
}
async function arm(f: Awaited<ReturnType<typeof manifest>>) {
  f.check.resolve(ownership); await settle();
  click(f.root.querySelector<HTMLInputElement>('input[type="checkbox"]')!);
  input(f.root.querySelector<HTMLInputElement>(".force-confirm")!, "external-dlo");
}
async function changeView(browser: BrowserState, change: string) {
  if (change === "selection") browser.select(browser.entries.findIndex((row) => row.name === "empty-object"));
  else if (change === "selection return") {
    browser.select(browser.entries.findIndex((row) => row.name === "empty-object"));
    browser.select(browser.entries.findIndex((row) => row.name === "external-dlo"));
  } else if (change === "container") await browser.openContainer("photos_segments");
  else if (change === "prefix") browser.prefix = "other/";
  else if (change === "bookmark return") { const bookmark = browser.connectedBookmarkId; browser.connectedBookmarkId = "other"; browser.connectedBookmarkId = bookmark; }
  else browser.connectedBookmarkId = "other";
  await settle();
}

describe("manifest dialog intent", () => {
  it("uses the captured bookmark, container and name for ownership and one forced submission", async () => {
    const f = await manifest();
    expect(f.api.checkSegmentOwnership).toHaveBeenCalledExactlyOnceWith(f.request);
    const reply = deferred<SegmentsDeleteResultDTO>();
    f.api.deleteManifestWithSegments = vi.fn(() => reply.promise);
    await arm(f);
    const submit = button(f.root, "Delete manifest + segments");
    click(submit); click(submit);
    expect(f.api.deleteManifestWithSegments).toHaveBeenCalledExactlyOnceWith({ ...f.request, force: true, confirm: "external-dlo" });
    expect(f.root.querySelector<HTMLInputElement>(".force-confirm")?.disabled).toBe(true);
    reply.resolve(deleted); await settle();
    expect(f.done).toHaveBeenCalledOnce();
  });

  it("refuses an incorrect force confirmation even when a disabled button receives an event", async () => {
    const f = await manifest(); await arm(f);
    f.api.deleteManifestWithSegments = vi.fn().mockResolvedValue(deleted);
    input(f.root.querySelector<HTMLInputElement>(".force-confirm")!, "wrong");
    click(button(f.root, "Delete manifest + segments")); await settle();
    expect(f.api.deleteManifestWithSegments).not.toHaveBeenCalled();
  });

  it.each(["bookmark", "bookmark return", "container", "prefix", "selection", "selection return"])(
    "retires ownership and submission after %s", async (change) => {
      const f = await manifest();
      f.api.deleteManifest = vi.fn().mockResolvedValue(f.browser.objectDetail!);
      await changeView(f.browser, change);
      f.check.resolve(ownership); await settle();
      expect(f.root.querySelector('input[type="checkbox"]')).toBeNull();
      click(button(f.root, "Delete manifest only")); await settle();
      expect(f.api.deleteManifest).not.toHaveBeenCalled();
      expect(f.done).toHaveBeenCalledOnce();
    });

  it.each(["close", "unmount"])("%s retires a pending ownership reply", async (ending) => {
    const f = await manifest();
    if (ending === "close") click(button(f.root, "Cancel"));
    else { await unmount(f.component); mounted.delete(f.component); }
    f.check.resolve(ownership); await settle();
    expect(f.root.querySelector('input[type="checkbox"]')).toBeNull();
    expect(f.done).toHaveBeenCalledTimes(ending === "close" ? 1 : 0);
  });

  it.each(["plain", "segments"])("a current %s deletion reports failure and permits a retry", async (mode) => {
    const f = await manifest();
    f.api.deleteManifest = vi.fn().mockRejectedValue(new Error("current failure"));
    f.api.deleteManifestWithSegments = vi.fn().mockRejectedValue(new Error("current failure"));
    if (mode === "segments") await arm(f);
    const submit = button(f.root, mode === "plain" ? "Delete manifest only" : "Delete manifest + segments");
    click(submit); await settle();
    expect(f.root.textContent).toContain("current failure"); expect(submit.disabled).toBe(false);
    expect(f.done).not.toHaveBeenCalled();
  });

  it.each(["close", "unmount"])("%s retires a pending successful deletion callback", async (ending) => {
    const f = await manifest(); const reply = deferred<SegmentsDeleteResultDTO>();
    f.api.deleteManifestWithSegments = vi.fn(() => reply.promise); await arm(f);
    click(button(f.root, "Delete manifest + segments"));
    if (ending === "close") click(button(f.root, "Cancel"));
    else { await unmount(f.component); mounted.delete(f.component); }
    reply.resolve(deleted); await settle();
    expect(f.done).toHaveBeenCalledTimes(ending === "close" ? 1 : 0);
  });

  it.each(["plain", "segments"])("duplicate %s deletion and its late callback stay retired after closure", async (mode) => {
    const f = await manifest();
    const reply = deferred<SegmentsDeleteResultDTO>();
    const mutate = vi.fn(() => reply.promise);
    f.api.deleteManifestWithSegments = mutate;
    f.api.deleteManifest = vi.fn(async () => { await mutate(); return f.browser.objectDetail!; });
    if (mode === "segments") await arm(f);
    const submit = button(f.root, mode === "plain" ? "Delete manifest only" : "Delete manifest + segments");
    click(submit); click(submit); expect(mutate).toHaveBeenCalledOnce();
    click(button(f.root, "Cancel"));
    reply.reject(new Error("obsolete failure")); await settle();
    expect(f.done).toHaveBeenCalledOnce(); expect(f.root.textContent).not.toContain("obsolete failure");
    expect(submit.disabled).toBe(true);
  });

  it("never opens ownership or admits deletion with an inconsistent initial identity", async () => {
    const f = await setup(); const wrong = { ...f.browser.objectDetail!, name: "other" };
    f.browser.objectDetail = wrong;
    f.api.checkSegmentOwnership = vi.fn().mockResolvedValue(ownership);
    f.api.deleteManifest = vi.fn().mockResolvedValue(wrong);
    const done = vi.fn();
    mounted.add(mount(ManifestDeleteDialog, { target: f.root,
      props: { browser: f.browser, detail: f.browser.objectDetail!, segmentsAt: "backing/old/", onClose: done } }));
    await settle(); click(button(f.root, "Delete manifest only")); await settle();
    expect(f.api.checkSegmentOwnership).not.toHaveBeenCalled(); expect(f.api.deleteManifest).not.toHaveBeenCalled();
  });
});

async function creation() {
  const f = await setup(); const done = vi.fn();
  const component = mount(CreateContainerDialog, { target: f.root, props: { browser: f.browser, onClose: done } });
  mounted.add(component); flushSync();
  input(f.root.querySelector<HTMLInputElement>("input")!, "created");
  const submit = () => { f.root.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); flushSync(); };
  return { ...f, done, component, submit };
}
describe("container creation lifetime", () => {
  it("submits once, freezes the form and opens the new container in its current view", async () => {
    const f = await creation(); const create = f.api.createContainer;
    const reply = deferred<Awaited<ReturnType<Api["createContainer"]>>>();
    f.api.createContainer = vi.fn(() => reply.promise);
    f.submit(); f.submit();
    expect(f.api.createContainer).toHaveBeenCalledExactlyOnceWith({ name: "created", policy: "", meta: {} });
    expect(f.root.querySelector<HTMLInputElement>("input")?.disabled).toBe(true);
    reply.resolve(await create({ name: "created", policy: "", meta: {} })); await settle();
    expect(f.browser.containerName).toBe("created"); expect(f.done).toHaveBeenCalledOnce();
  });

  it("keeps a current failure visible and enables a retry", async () => {
    const f = await creation(); f.api.createContainer = vi.fn().mockRejectedValue(new Error("current failure"));
    f.submit(); await settle();
    expect(f.root.textContent).toContain("current failure"); expect(button(f.root, "Create").disabled).toBe(false);
    expect(f.root.querySelector<HTMLInputElement>("input")?.disabled).toBe(false); expect(f.done).not.toHaveBeenCalled();
  });

  it.each(["bookmark", "bookmark return", "container", "prefix", "selection", "selection return"])(
    "refuses creation after %s even before unmount", async (change) => {
      const f = await creation(); f.api.createContainer = vi.fn().mockResolvedValue(f.browser.containerDetail!);
      await changeView(f.browser, change); f.submit(); await settle();
      expect(f.api.createContainer).not.toHaveBeenCalled(); expect(f.done).toHaveBeenCalledOnce();
    });

  it.each(["close", "unmount"])("%s retires a pending creation callback", async (ending) => {
    const f = await creation(); const reply = deferred<Awaited<ReturnType<Api["createContainer"]>>>();
    f.api.createContainer = vi.fn(() => reply.promise); f.submit();
    if (ending === "close") click(button(f.root, "Cancel"));
    else { await unmount(f.component); mounted.delete(f.component); }
    reply.reject(new Error("obsolete creation failure")); await settle();
    expect(f.root.textContent).not.toContain("obsolete creation failure");
    expect(f.done).toHaveBeenCalledTimes(ending === "close" ? 1 : 0);
  });

  it.each(["close", "unmount"])("%s retires a pending successful creation callback", async (ending) => {
    const f = await creation(); const reply = deferred<Awaited<ReturnType<Api["createContainer"]>>>();
    const result = f.browser.containerDetail!;
    f.api.createContainer = vi.fn(() => reply.promise); f.submit();
    if (ending === "close") click(button(f.root, "Cancel"));
    else { await unmount(f.component); mounted.delete(f.component); }
    reply.resolve(result); await settle();
    expect(f.done).toHaveBeenCalledTimes(ending === "close" ? 1 : 0);
  });
});

describe("marker action ownership", () => {
  it("offers marker creation but not deletion in a v1 build", async () => {
    const f = await setup("implied/");
    mounted.add(mount(Inspector, { target: f.root, props: { browser: f.browser } })); flushSync();
    const labels = () => [...f.root.querySelectorAll("button")].map((b) => b.textContent?.trim());
    expect(labels()).toContain("Create marker object");
    await f.browser.createMarker("implied/"); await settle();
    expect(labels()).not.toContain("Delete marker object");
  });

  it("withholds marker creation when that affordance is not offered", async () => {
    v1Offers.markerCreate = false;
    const f = await setup("implied/");
    mounted.add(mount(Inspector, { target: f.root, props: { browser: f.browser } })); flushSync();
    const labels = () => [...f.root.querySelectorAll("button")].map((b) => b.textContent?.trim());
    expect(labels()).not.toContain("Create marker object");
  });

  describe("when deletion is returned", () => {
    beforeEach(() => { v1Offers.markerDelete = true; });

    it("admits one creation despite duplicate events", async () => {
      const f = await setup("implied/");
      const reply = deferred<PrefixDetailDTO>(); f.api.createMarker = vi.fn(() => reply.promise);
      mounted.add(mount(Inspector, { target: f.root, props: { browser: f.browser } })); flushSync();
      const create = button(f.root, "Create marker object"); click(create); click(create);
      expect(f.api.createMarker).toHaveBeenCalledExactlyOnceWith("fixtures", "implied/");
      reply.resolve({ ...f.browser.prefixDetail!, markerBacked: true }); await settle();
    });

    it.each(["success", "failure"])("a late %s cannot release a newer marker action's busy state", async (outcome) => {
      const f = await setup("implied/");
      const replies = [deferred<PrefixDetailDTO>(), deferred<PrefixDetailDTO>()];
      f.api.createMarker = vi.fn().mockImplementationOnce(() => replies[0].promise).mockImplementationOnce(() => replies[1].promise);
      mounted.add(mount(Inspector, { target: f.root, props: { browser: f.browser } })); flushSync();
      click(button(f.root, "Create marker object"));
      const oldDetail = f.browser.prefixDetail!;
      f.browser.select(f.browser.entries.findIndex((row) => row.name === "empty-object")); await settle();
      f.browser.select(f.browser.entries.findIndex((row) => row.name === "implied/")); await settle();
      const create = button(f.root, "Create marker object"); click(create);
      expect(f.api.createMarker).toHaveBeenCalledTimes(2); expect(create.disabled).toBe(true);
      if (outcome === "success") replies[0].resolve(oldDetail); else replies[0].reject(new Error("obsolete marker failure"));
      await settle(); expect(create.disabled).toBe(true); expect(f.root.textContent).not.toContain("obsolete marker failure");
      replies[1].reject(new Error("current marker failure")); await settle();
      expect(create.disabled).toBe(false); expect(f.root.textContent).toContain("current marker failure");
    });

    it("does not admit deletion from an old marker after a same-bookmark reconnect", async () => {
      const f = await setup("implied/"); await f.browser.createMarker("implied/");
      mounted.add(mount(Inspector, { target: f.root, props: { browser: f.browser } })); flushSync();
      const remove = button(f.root, "Delete marker object");
      f.api.connect = vi.fn().mockRejectedValue(new Error("failed reconnect"));
      f.api.deleteMarker = vi.fn().mockResolvedValue(undefined);
      await expect(f.browser.connect(f.bookmark.id)).rejects.toThrow("failed reconnect");
      click(remove); await settle(); expect(f.api.deleteMarker).not.toHaveBeenCalled();
    });
  });
});

it.each(["external-dlo", "external-slo"])("%s distinguishes unavailable segment details from an empty result", async (name) => {
  const f = await setup(name);
  const dlo = deferred<Awaited<ReturnType<Api["dloSegments"]>>>();
  const slo = deferred<Awaited<ReturnType<Api["sloManifest"]>>>();
  const previousDlo = f.browser.dloSegments; const previousSlo = f.browser.sloManifest;
  vi.spyOn(f.api, "dloSegments").mockImplementationOnce(() => dlo.promise);
  vi.spyOn(f.api, "sloManifest").mockImplementationOnce(() => slo.promise);
  f.browser.refreshDetail(); await settle(); expect(f.browser.detailLoading).toBe(true);
  await f.browser.saveObjectMetadata({ meta: {}, contentType: f.browser.objectDetail!.contentType, deleteAt: "" });
  mounted.add(mount(Inspector, { target: f.root, props: { browser: f.browser } })); flushSync();
  expect(f.root.textContent).toContain("details are unavailable.");
  expect(f.root.textContent).not.toContain("fetching exact manifest");
  expect(f.root.querySelector("table.segments")).toBeNull();
  click(button(f.root, "Reload segment details")); await settle();
  expect(f.root.querySelector("table.segments")).not.toBeNull();
  if (previousDlo) dlo.resolve(previousDlo); if (previousSlo) slo.resolve(previousSlo); await settle();
});

describe("mutation follow-up ownership", () => {
  it("an unmounted inspector does not offer marker deletion in a v1 build", async () => {
    const f = await setup("implied/"); await f.browser.createMarker("implied/");
    const old = mount(Inspector, { target: f.root, props: { browser: f.browser } }); mounted.add(old); flushSync();
    expect([...f.root.querySelectorAll("button")].map((b) => b.textContent?.trim())).not.toContain("Delete marker object");
    await unmount(old); mounted.delete(old);
    mounted.add(mount(Inspector, { target: f.root, props: { browser: f.browser } })); flushSync();
    expect([...f.root.querySelectorAll("button")].map((b) => b.textContent?.trim())).not.toContain("Delete marker object");
  });

  it("an unmounted inspector's marker deletion cannot refresh a replacement inspector", async () => {
    v1Offers.markerDelete = true;
    const f = await setup("implied/"); await f.browser.createMarker("implied/");
    const reply = deferred<void>(); f.api.deleteMarker = vi.fn(() => reply.promise);
    const old = mount(Inspector, { target: f.root, props: { browser: f.browser } }); mounted.add(old); flushSync();
    click(button(f.root, "Delete marker object")); await unmount(old); mounted.delete(old);
    mounted.add(mount(Inspector, { target: f.root, props: { browser: f.browser } })); flushSync();
    const selected = f.browser.detailIdentity;
    reply.resolve(); await settle();
    expect(f.browser.detailIdentity).toBe(selected); expect(button(f.root, "Delete marker object")).toBeDefined();
  });

  it("a closed creation cannot navigate and close a new dialog in the same view", async () => {
    const f = await creation(); const reply = deferred<Awaited<ReturnType<Api["createContainer"]>>>();
    const result = f.browser.containerDetail!;
    f.api.createContainer = vi.fn(() => reply.promise); f.submit(); click(button(f.root, "Cancel"));
    await unmount(f.component); mounted.delete(f.component);
    const done = vi.fn();
    mounted.add(mount(CreateContainerDialog, { target: f.root, props: { browser: f.browser, onClose: done } }));
    flushSync(); reply.resolve(result); await settle();
    expect(done).not.toHaveBeenCalled(); expect(f.browser.containerName).toBe("fixtures");
    expect(button(f.root, "Create").disabled).toBe(false);
  });

  it.each(["plain", "segments"])("a closed %s deletion cannot refresh and close a new dialog in the same view", async (kind) => {
    const f = await manifest(); const reply = deferred<void>(); const detail = f.browser.objectDetail!;
    f.api.deleteManifest = vi.fn(async () => { await reply.promise; return detail; });
    f.api.deleteManifestWithSegments = vi.fn(async () => { await reply.promise; return deleted; });
    if (kind === "segments") await arm(f);
    click(button(f.root, kind === "plain" ? "Delete manifest only" : "Delete manifest + segments"));
    click(button(f.root, "Cancel")); await unmount(f.component); mounted.delete(f.component);
    const done = vi.fn(); const selection = f.browser.detailIdentity;
    mounted.add(mount(ManifestDeleteDialog, { target: f.root,
      props: { browser: f.browser, detail, segmentsAt: "backing/old/", onClose: done } }));
    flushSync(); reply.resolve(); await settle();
    expect(done).not.toHaveBeenCalled(); expect(f.browser.detailIdentity).toBe(selection);
  });

  it.each(["create", "manifest", "segments", "marker"])("%s keeps its outcome and audit but cannot navigate after selection changes", async (kind) => {
    const f = await setup(kind === "marker" ? "implied/" : "external-dlo");
    const gate = deferred<void>();
    const detail = f.browser.objectDetail;
    f.api.createContainer = vi.fn(async () => { await gate.promise; return f.browser.containerDetail!; });
    f.api.deleteManifest = vi.fn(async () => { await gate.promise; return detail!; });
    f.api.deleteManifestWithSegments = vi.fn(async () => { await gate.promise; return deleted; });
    f.api.deleteMarker = vi.fn(() => gate.promise);
    f.api.containers = vi.fn(f.api.containers); f.api.objects = vi.fn(f.api.objects);
    f.api.auditRecent = vi.fn(f.api.auditRecent);
    const pending = kind === "create" ? f.browser.createContainer({ name: "created", policy: "", meta: {} })
      : kind === "manifest" ? f.browser.deleteManifest("fixtures", "external-dlo")
      : kind === "segments" ? f.browser.deleteManifestWithSegments({ bookmarkId: f.bookmark.id, container: "fixtures", name: "external-dlo", force: true, confirm: "external-dlo" })
      : f.browser.deleteMarker("implied/");
    await changeView(f.browser, "selection");
    const selected = f.browser.detailIdentity;
    gate.resolve(); const result = await pending; await settle();
    expect(f.api.auditRecent).toHaveBeenCalledOnce();
    expect(f.api.containers).not.toHaveBeenCalled(); expect(f.api.objects).not.toHaveBeenCalled();
    expect(f.browser.containerName).toBe("fixtures"); expect(f.browser.detailIdentity).toBe(selected);
    if (kind === "segments") expect(result).toEqual(deleted);
  });

  it.each(["create", "manifest", "segments", "marker"])("%s rechecks its scope after the audit wait", async (kind) => {
    const f = await setup(kind === "marker" ? "implied/" : "external-dlo");
    const gate = deferred<Awaited<ReturnType<Api["auditRecent"]>>>();
    f.api.auditRecent = vi.fn(() => gate.promise);
    f.api.createContainer = vi.fn().mockResolvedValue(f.browser.containerDetail!);
    f.api.deleteManifest = vi.fn().mockResolvedValue(f.browser.objectDetail!);
    f.api.deleteManifestWithSegments = vi.fn().mockResolvedValue(deleted);
    f.api.deleteMarker = vi.fn().mockResolvedValue(undefined);
    f.api.containers = vi.fn(f.api.containers); f.api.objects = vi.fn(f.api.objects);
    const pending = kind === "create" ? f.browser.createContainer({ name: "created", policy: "", meta: {} })
      : kind === "manifest" ? f.browser.deleteManifest("fixtures", "external-dlo")
      : kind === "segments" ? f.browser.deleteManifestWithSegments({ bookmarkId: f.bookmark.id, container: "fixtures", name: "external-dlo", force: true, confirm: "external-dlo" })
      : f.browser.deleteMarker("implied/");
    await settle(); expect(f.api.auditRecent).toHaveBeenCalledOnce();
    await changeView(f.browser, "bookmark return");
    gate.resolve([]); await pending; await settle();
    expect(f.api.containers).not.toHaveBeenCalled(); expect(f.api.objects).not.toHaveBeenCalled();
    expect(f.browser.containerName).toBe("fixtures");
  });

  it("creation rechecks scope after its container-list wait and that list cannot publish across navigation", async () => {
    const f = await setup();
    f.api.createContainer = vi.fn().mockResolvedValue(f.browser.containerDetail!);
    const gate = deferred<Awaited<ReturnType<Api["containers"]>>>();
    f.api.containers = vi.fn(() => gate.promise);
    const pending = f.browser.createContainer({ name: "created", policy: "", meta: {} });
    await settle(); expect(f.api.containers).toHaveBeenCalledOnce();
    await f.browser.openContainer("photos_segments");
    const containers = f.browser.containers;
    gate.resolve({ rows: [], nextMarker: "obsolete", scope: { bookmarkId: 'bm', generation: 0 } }); await pending;
    expect(f.browser.containerName).toBe("photos_segments"); expect(f.browser.containers).toEqual(containers);
    expect(f.browser.containersMarker).not.toBe("obsolete");
  });

  it.each(["manifest", "segments"])("a %s operation for a different explicit target cannot refresh this selection", async (kind) => {
    const f = await setup(); const selection = f.browser.detailIdentity;
    f.api.deleteManifest = vi.fn().mockResolvedValue(f.browser.objectDetail!);
    f.api.deleteManifestWithSegments = vi.fn().mockResolvedValue(deleted);
    f.api.objects = vi.fn(f.api.objects);
    const req = { bookmarkId: "other", container: "different", name: "foreign", force: true, confirm: "foreign" };
    if (kind === "manifest") await f.browser.deleteManifest(req.container, req.name);
    else {
      expect(await f.browser.deleteManifestWithSegments(req)).toEqual(deleted);
      expect(f.api.deleteManifestWithSegments).toHaveBeenCalledExactlyOnceWith(req);
    }
    expect(f.api.objects).not.toHaveBeenCalled(); expect(f.browser.detailIdentity).toBe(selection);
  });

  it("segment deletion refreshes the current captured prefix only when the manifest was deleted", async () => {
    const f = await setup();
    f.api.objects = vi.fn(f.api.objects);
    const req = { bookmarkId: f.bookmark.id, container: "fixtures", name: "external-dlo", force: true, confirm: "external-dlo" };
    const partial = { ...deleted, manifestDeleted: false, segmentsFailed: 1 };
    f.api.deleteManifestWithSegments = vi.fn().mockResolvedValueOnce(partial).mockResolvedValueOnce(deleted);
    expect(await f.browser.deleteManifestWithSegments(req)).toEqual(partial);
    expect(f.api.objects).not.toHaveBeenCalled();
    expect(await f.browser.deleteManifestWithSegments(req)).toEqual(deleted);
    expect(f.api.objects).toHaveBeenCalledExactlyOnceWith(f.browser.connectedBookmarkId!, "fixtures", "", "", 1000);
    expect(f.browser.detailIdentity).toBeNull();
  });
});

describe("initial list reply ownership", () => {
  it.each(["rows", "failure"])("newer container reads own %s even when old reads finish last", async (outcome) => {
    const f = await setup();
    const old = deferred<Awaited<ReturnType<Api["containers"]>>>();
    f.api.containers = vi.fn().mockImplementationOnce(() => old.promise)
      .mockResolvedValueOnce({ rows: [{ name: "current", count: 2, bytes: 3, class: "normal" }], nextMarker: "current", scope: f.scope });
    const pending = f.browser.loadContainers(); await f.browser.loadContainers();
    if (outcome === "rows") old.resolve({ rows: [], nextMarker: "obsolete", scope: { bookmarkId: 'bm', generation: 0 } });
    else old.reject(new Error("obsolete containers"));
    await pending;
    expect(f.browser.containers[0]?.name).toBe("current"); expect(f.browser.containersMarker).toBe("current");
    expect(f.browser.containersError).toBeNull();
  });

  it.each(["rows", "failure"])("scope changes retire container %s", async (outcome) => {
    const f = await setup(); const rows = f.browser.containers;
    const old = deferred<Awaited<ReturnType<Api["containers"]>>>(); f.api.containers = vi.fn(() => old.promise);
    const pending = f.browser.loadContainers(); await changeView(f.browser, "bookmark return");
    if (outcome === "rows") old.resolve({ rows: [], nextMarker: "obsolete", scope: { bookmarkId: 'bm', generation: 0 } });
    else old.reject(new Error("obsolete containers"));
    await pending;
    expect(f.browser.containers).toEqual(rows); expect(f.browser.containersMarker).not.toBe("obsolete");
    expect(f.browser.containersError).toBeNull();
  });

  it.each(["rows", "failure"])("scope changes retire initial object %s and its loading callback", async (outcome) => {
    const f = await setup(); const rows = f.browser.entries;
    const old = deferred<Awaited<ReturnType<Api["objects"]>>>(); f.api.objects = vi.fn(() => old.promise);
    const pending = f.browser.goTo(""); await changeView(f.browser, "bookmark return");
    if (outcome === "rows") old.resolve({ rows: [], nextMarker: "obsolete", scope: { bookmarkId: 'bm', generation: 0 } });
    else old.reject(new Error("obsolete objects"));
    await pending;
    expect(f.browser.entries).toEqual(rows); expect(f.browser.entriesMarker).not.toBe("obsolete");
    expect(f.browser.entriesError).toBeNull(); expect(f.browser.entriesLoading).toBe(true);
  });
});

it("filter reanchoring preserves an editor but selecting the same name again ends it", async () => {
  const f = await setup(); mounted.add(mount(Inspector, { target: f.root, props: { browser: f.browser } })); flushSync();
  click(button(f.root, "Edit metadata"));
  f.browser.filter = "external-dlo"; flushSync();
  expect([...f.root.querySelectorAll("button")].some((b) => b.textContent?.trim() === "Save")).toBe(true);
  f.browser.select(0); await settle();
  expect([...f.root.querySelectorAll("button")].some((b) => b.textContent?.trim() === "Save")).toBe(false);
});

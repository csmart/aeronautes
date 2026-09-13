import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { DownloadPreflight, PickedDirDTO, PickedFilesDTO, UploadPreflight } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import LocalTransferDialog from "./LocalTransferDialog.svelte";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

async function settle() {
  for (let i = 0; i < 8; i++) { await Promise.resolve(); flushSync(); }
}

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
});

type Mode = "upload" | "download";
type Stage = "picker" | "preflight" | "ready";
function show(mode: Mode) {
  const api = newFakeApi();
  const files: ReturnType<typeof deferred<PickedFilesDTO | null>>[] = [];
  const dirs: ReturnType<typeof deferred<PickedDirDTO | null>>[] = [];
  const uploads: ReturnType<typeof deferred<UploadPreflight>>[] = [];
  const downloads: ReturnType<typeof deferred<DownloadPreflight>>[] = [];
  api.pickUploadFiles = vi.fn(() => { const p = deferred<PickedFilesDTO | null>(); files.push(p); return p.promise; });
  api.pickDownloadDir = vi.fn(() => { const p = deferred<PickedDirDTO | null>(); dirs.push(p); return p.promise; });
  api.preflightUpload = vi.fn(() => { const p = deferred<UploadPreflight>(); uploads.push(p); return p.promise; });
  api.preflightDownload = vi.fn(() => { const p = deferred<DownloadPreflight>(); downloads.push(p); return p.promise; });
  const started = deferred<string>();
  api.startUpload = vi.fn(() => started.promise);
  api.startDownload = vi.fn(() => started.promise);
  api.connect = vi.fn().mockRejectedValue(new Error("reconnect failed"));
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source";
  browser.containerName = "media";
  browser.prefix = "selected/";
  browser.localTransfer = mode;
  const root = document.createElement("div"); document.body.append(root);
  const onClose = vi.fn();
  const component = mount(LocalTransferDialog, { target: root, props: {
    browser, get mode() { return browser.localTransfer ?? mode; }, onClose,
  } });
  mounted.add(component); flushSync();
  const pickButton = () => root.querySelector<HTMLButtonElement>("footer button")!;
  const startButton = () => [...root.querySelectorAll<HTMLButtonElement>("footer button")].find((b) => b.textContent?.trim().startsWith("Start "));
  const choose = () => { pickButton().click(); flushSync(); };
  const resolvePicker = async (index = 0) => {
    if (mode === "upload") files[index].resolve({ token: "files-" + index, files: ["a.txt"] });
    else dirs[index].resolve({ token: "dir-" + index, dir: "chosen directory " + index });
    await settle();
  };
  const resolvePreflight = async (index = 0, reject = false, warning = false) => {
    const problems = reject || warning ? [{ name: "a.txt", kind: "collision", detail: "reported issue", reject }] : [];
    if (mode === "upload") uploads[index].resolve({
      items: [{ path: "", name: "selected/a.txt", size: 5, route: "rclone" }], problems, totalBytes: 5,
    });
    else downloads[index].resolve({
      items: [{ name: "selected/a.txt", relPath: "a.txt", size: 5, route: "rclone", dir: false }],
      problems, totalBytes: 5, objects: 1,
    });
    await settle();
  };
  const rejectReply = async (stage: "picker" | "preflight", index = 0) => {
    const reply = stage === "picker" ? (mode === "upload" ? files[index] : dirs[index])
      : (mode === "upload" ? uploads[index] : downloads[index]);
    reply.reject(new Error("obsolete reply error")); await settle();
  };
  const reach = async (stage: Stage) => {
    choose();
    if (stage !== "picker") await resolvePicker();
    if (stage === "ready") await resolvePreflight();
  };
  return { api, browser, root, onClose, component, files, dirs, uploads, downloads, started, mode,
    pickButton, startButton, choose, resolvePicker, resolvePreflight, rejectReply, reach };
}

type Fixture = ReturnType<typeof show>;
function expectDisarmed(f: Fixture) {
  expect(f.startButton()?.disabled ?? true).toBe(true);
  expect(f.root.querySelector(".report")).toBeNull();
  expect(f.api.startUpload).not.toHaveBeenCalled();
  expect(f.api.startDownload).not.toHaveBeenCalled();
}

function changeScope(f: Fixture, field: "bookmark" | "container" | "prefix", revert = false) {
  if (field === "bookmark") {
    f.browser.connectedBookmarkId = "elsewhere";
    if (revert) f.browser.connectedBookmarkId = "source";
  } else if (field === "container") {
    f.browser.containerName = "elsewhere";
    if (revert) f.browser.containerName = "media";
  } else {
    f.browser.prefix = "elsewhere/";
    if (revert) f.browser.prefix = "selected/";
  }
  flushSync();
}

describe.each(["upload", "download"] as const)("%s preflight binding", (mode) => {
  it.each((["bookmark", "container", "prefix"] as const).flatMap((field) => [false, true].flatMap((revert) =>
    (["picker", "preflight", "ready"] as Stage[]).map((stage) => ({ field, revert, stage })),
  )))("invalidates $field change, revert=$revert, stage=$stage", async ({ field, revert, stage }) => {
    const f = show(mode); await f.reach(stage);
    changeScope(f, field, revert);
    if (stage === "picker") {
      await f.resolvePicker();
      expect(f.api.preflightUpload).not.toHaveBeenCalled();
      expect(f.api.preflightDownload).not.toHaveBeenCalled();
    } else if (stage === "preflight") await f.resolvePreflight();
    expectDisarmed(f);
    expect(f.pickButton().disabled).toBe(false);
  });

  it.each(["picker", "preflight", "ready"] as Stage[])("invalidates the same-bookmark reconnect at %s", async (stage) => {
    const f = show(mode); await f.reach(stage);
    await expect(f.browser.connect("source")).rejects.toThrow("reconnect failed");
    expect([f.browser.connectedBookmarkId, f.browser.containerName, f.browser.prefix]).toEqual([null, null, ""]);
    f.browser.connectedBookmarkId = "source"; f.browser.containerName = "media"; f.browser.prefix = "selected/";
    if (stage === "picker") {
      await f.resolvePicker();
      expect(f.api.preflightUpload).not.toHaveBeenCalled();
      expect(f.api.preflightDownload).not.toHaveBeenCalled();
    } else if (stage === "preflight") await f.resolvePreflight();
    await settle(); expectDisarmed(f);
  });

  it.each((["picker", "preflight", "ready"] as Stage[]).flatMap((stage) => [false, true].map((revert) => ({ stage, revert }))))(
    "invalidates mode changes, revert=$revert, stage=$stage", async ({ stage, revert }) => {
      const f = show(mode); await f.reach(stage);
      f.browser.localTransfer = mode === "upload" ? "download" : "upload";
      if (revert) f.browser.localTransfer = mode;
      flushSync();
      if (stage === "picker") {
        await f.resolvePicker();
        expect(f.api.preflightUpload).not.toHaveBeenCalled();
        expect(f.api.preflightDownload).not.toHaveBeenCalled();
      } else if (stage === "preflight") await f.resolvePreflight();
      expectDisarmed(f);
    },
  );

  it("consumes and starts the exact scope, settings and opaque token once", async () => {
    const f = show(mode); await f.reach("ready");
    const start = f.startButton()!; expect(start.disabled).toBe(false);
    start.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    start.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    flushSync();
    const expected = { bookmarkId: "source", container: "media", prefix: "selected/",
      settings: { chunkSize: "", transfers: 0, checkers: 0, tpsLimit: 0, bwLimit: "" },
      ...(mode === "upload" ? { selectionToken: "files-0" } : { destDirToken: "dir-0" }),
    };
    expect(mode === "upload" ? f.api.preflightUpload : f.api.preflightDownload).toHaveBeenCalledExactlyOnceWith(expected);
    expect(mode === "upload" ? f.api.startUpload : f.api.startDownload).toHaveBeenCalledExactlyOnceWith(expected);
    f.started.reject(new Error("start outcome unavailable")); await settle();
    expect(f.root.textContent).toContain("start outcome unavailable");
    expect(f.startButton()?.disabled ?? true).toBe(true);
    expect(f.root.querySelector(".report")).toBeNull();
  });

  it.each(["scope", "mode", "mode return", "close reopen"])("rechecks %s in Start before reactive invalidation", async (change) => {
    const f = show(mode); await f.reach("ready");
    const start = f.startButton()!;
    if (change === "scope") f.browser.prefix = "elsewhere/";
    else {
      f.browser.localTransfer = change === "close reopen" ? null : mode === "upload" ? "download" : "upload";
      if (change !== "mode") f.browser.localTransfer = mode;
    }
    start.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(f.api.startUpload).not.toHaveBeenCalled(); expect(f.api.startDownload).not.toHaveBeenCalled();
    flushSync();
  });

  it.each((["picker", "preflight"] as const).flatMap((stage) => ["success", "failure"].map((outcome) => ({ stage, outcome }))))(
    "only the newest attempt owns $stage $outcome and busy state", async ({ stage, outcome }) => {
    const f = show(mode); await f.reach(stage);
    changeScope(f, "prefix");
    expect(f.pickButton().disabled).toBe(false);
    f.choose();
    if (outcome === "failure") await f.rejectReply(stage);
    else if (stage === "picker") await f.resolvePicker();
    else await f.resolvePreflight();
    expect(mode === "upload" ? f.api.preflightUpload : f.api.preflightDownload).toHaveBeenCalledTimes(stage === "picker" ? 0 : 1);
    expect(f.pickButton().disabled).toBe(true);
    expect(f.root.textContent).not.toContain("obsolete");
    expectDisarmed(f);
    await f.resolvePicker(1);
    await f.resolvePreflight(stage === "picker" ? 0 : 1);
    expect(f.startButton()?.disabled).toBe(false);
  });

  it("ignores an old successful preflight after the replacement report rejects", async () => {
    const f = show(mode); await f.reach("preflight");
    changeScope(f, "prefix"); expect(f.pickButton().disabled).toBe(false);
    f.choose(); await f.resolvePicker(1); await f.resolvePreflight(1, true);
    await f.resolvePreflight(0);
    expect(f.root.textContent).toContain("Rejected");
    expect(f.startButton()?.disabled ?? true).toBe(true);
  });

  it.each(["button", "scrim", "state"])("ends the picker lifetime on %s close", async (route) => {
    const f = show(mode); await f.reach("picker");
    if (route === "button") f.root.querySelector<HTMLButtonElement>('button[aria-label="Close"]')!.click();
    else if (route === "scrim") f.root.querySelector<HTMLElement>(".scrim")!.click();
    else { f.browser.localTransfer = null; f.browser.localTransfer = mode; }
    flushSync();
    await f.resolvePicker();
    expect(f.api.preflightUpload).not.toHaveBeenCalled(); expect(f.api.preflightDownload).not.toHaveBeenCalled();
    expectDisarmed(f);
  });

  it.each(["success", "failure"])("ignores late preflight %s after closing", async (outcome) => {
    const f = show(mode); await f.reach("preflight");
    f.root.querySelector<HTMLButtonElement>('button[aria-label="Close"]')!.click();
    if (outcome === "success") await f.resolvePreflight(); else await f.rejectReply("preflight");
    expectDisarmed(f); expect(f.root.textContent).not.toContain("obsolete");
  });

  it("does not preflight an unmounted picker or arm a reopened dialog", async () => {
    const old = show(mode); await old.reach("picker");
    await unmount(old.component); mounted.delete(old.component);
    const current = show(mode); await current.reach("picker");
    await old.resolvePicker();
    expect(old.api.preflightUpload).not.toHaveBeenCalled(); expect(old.api.preflightDownload).not.toHaveBeenCalled();
    expectDisarmed(current);
    await current.resolvePicker(); await current.resolvePreflight();
    expect(current.startButton()?.disabled).toBe(false);
  });

  it.each(["cancel", "error"])("cannot reuse the old report after picker %s", async (outcome) => {
    const f = show(mode); await f.reach("ready"); f.choose();
    if (outcome === "cancel") {
      if (mode === "upload") f.files[1].resolve(null); else f.dirs[1].resolve(null);
      await settle();
    } else await f.rejectReply("picker", 1);
    expectDisarmed(f); expect(f.pickButton().disabled).toBe(false);
  });

  it("shows a current rejection without enabling Start", async () => {
    const f = show(mode); await f.reach("preflight"); await f.resolvePreflight(0, true);
    expect(f.root.textContent).toContain("Rejected");
    expect(f.root.textContent).toContain("reported issue");
    expect(f.startButton()?.disabled ?? true).toBe(true);
  });

  it("allows a current warning and displays the accepted job", async () => {
    const f = show(mode); await f.reach("preflight"); await f.resolvePreflight(0, false, true);
    expect(f.root.textContent).toContain("reported issue");
    f.startButton()!.click(); flushSync();
    f.started.resolve("accepted-job"); await settle();
    expect(f.root.textContent).toContain("Job accepted-job started");
    expect(f.root.querySelector(".report")).toBeNull();
  });

  it("keeps a failed preflight disarmed and allows a fresh attempt", async () => {
    const f = show(mode); await f.reach("preflight"); await f.rejectReply("preflight");
    expectDisarmed(f); expect(f.root.textContent).toContain("obsolete reply error");
    f.choose(); await f.resolvePicker(1); await f.resolvePreflight(1);
    expect(f.startButton()?.disabled).toBe(false);
    expect(f.root.textContent).not.toContain("obsolete");
  });

  it("refuses a second picker while the current picker is pending", async () => {
    const f = show(mode); await f.reach("picker");
    f.pickButton().dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(mode === "upload" ? f.api.pickUploadFiles : f.api.pickDownloadDir).toHaveBeenCalledTimes(1);
    await f.resolvePicker(); await f.resolvePreflight();
    expect(f.startButton()?.disabled).toBe(false);
  });

  it("refuses picking and Start throughout reconnect", async () => {
    const f = show(mode); await f.reach("ready");
    const start = f.startButton()!;
    const reconnect = deferred<never>(); f.api.connect = vi.fn(() => reconnect.promise);
    const pending = f.browser.connect("source").catch((error: Error) => error.message);
    start.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    f.pickButton().dispatchEvent(new MouseEvent("click", { bubbles: true })); flushSync();
    expectDisarmed(f);
    expect(mode === "upload" ? f.api.pickUploadFiles : f.api.pickDownloadDir).toHaveBeenCalledTimes(1);
    reconnect.reject(new Error("failed reconnect")); expect(await pending).toBe("failed reconnect"); await settle();
    expect(f.pickButton().disabled).toBe(true);
  });

  it.each(["success", "failure"])("ignores a late Start %s after scope changes", async (outcome) => {
    const f = show(mode); await f.reach("ready"); f.startButton()!.click(); flushSync();
    changeScope(f, "prefix");
    expect(f.pickButton().disabled).toBe(true);
    f.pickButton().dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(mode === "upload" ? f.api.pickUploadFiles : f.api.pickDownloadDir).toHaveBeenCalledTimes(1);
    if (outcome === "success") f.started.resolve("obsolete-job"); else f.started.reject(new Error("obsolete Start error"));
    await settle();
    expect(f.root.textContent).not.toContain("obsolete");
    expect(f.pickButton().disabled).toBe(false);
    expect(f.startButton()?.disabled ?? true).toBe(true);
  });
});

it.each([
  { token: "files", files: null }, { token: "files", files: [] }, { token: "", files: ["a.txt"] },
])("refuses an empty upload selection %j", async (selection) => {
  const f = show("upload"); f.choose(); f.files[0].resolve(selection); await settle();
  expect(f.api.preflightUpload).not.toHaveBeenCalled(); expectDisarmed(f);
  expect(f.pickButton().disabled).toBe(false);
});

it("refuses an empty directory token", async () => {
  const f = show("download"); f.choose(); f.dirs[0].resolve({ token: "", dir: "display only" }); await settle();
  expect(f.api.preflightDownload).not.toHaveBeenCalled(); expectDisarmed(f);
  expect(f.pickButton().disabled).toBe(false);
});

it.each(["upload", "download"] as const)("forwards the complete captured %s request without rebuilding its scope", async (mode) => {
  const api = newFakeApi();
  api.preflightUpload = vi.fn().mockResolvedValue({ items: [], problems: [], totalBytes: 0 });
  api.preflightDownload = vi.fn().mockResolvedValue({ items: [], problems: [], totalBytes: 0, objects: 0 });
  api.startUpload = vi.fn().mockResolvedValue("job"); api.startDownload = vi.fn().mockResolvedValue("job");
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "another"; browser.containerName = "another"; browser.prefix = "another/";
  const scope = { bookmarkId: "captured", container: "media", prefix: "  exact/%\n",
    settings: { chunkSize: "2Mi", transfers: 3, checkers: 4, tpsLimit: 5, bwLimit: "6M" },
  };
  if (mode === "upload") {
    const req = { ...scope, selectionToken: "files" };
    await browser.preflightUpload(req); await browser.startUpload(req);
    expect(api.preflightUpload).toHaveBeenCalledExactlyOnceWith(req);
    expect(api.startUpload).toHaveBeenCalledExactlyOnceWith(req);
  } else {
    const req = { ...scope, destDirToken: "directory" };
    await browser.preflightDownload(req); await browser.startDownload(req);
    expect(api.preflightDownload).toHaveBeenCalledExactlyOnceWith(req);
    expect(api.startDownload).toHaveBeenCalledExactlyOnceWith(req);
  }
});

// ADR 0041: the pick is bound to the scope the dialog captured before
// opening it, so a late navigation cannot retarget a selection already
// being made.
describe.each(["upload", "download"] as Mode[])("%s selection scope", (mode) => {
  it("passes the captured scope to the picker", async () => {
    const f = show(mode);
    f.choose();
    const picker = mode === "upload" ? f.api.pickUploadFiles : f.api.pickDownloadDir;
    expect(picker).toHaveBeenCalledExactlyOnceWith({ bookmarkId: "source", container: "media", prefix: "selected/" });
  });

  it("does not retarget a pick that is already open", async () => {
    const f = show(mode);
    f.choose();
    changeScope(f, "container");
    changeScope(f, "prefix");
    await settle();
    const picker = mode === "upload" ? f.api.pickUploadFiles : f.api.pickDownloadDir;
    expect(picker).toHaveBeenCalledExactlyOnceWith({ bookmarkId: "source", container: "media", prefix: "selected/" });
  });
});

import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, AppSettingsDTO, DownloadPreflightView, PickedDirDTO, PickedFilesDTO, UploadPreflight } from "../../lib/api";
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
function show(mode: Mode, adjust?: (api: Api, browser: BrowserState) => void) {
  const api = newFakeApi();
  const files: ReturnType<typeof deferred<PickedFilesDTO | null>>[] = [];
  const dirs: ReturnType<typeof deferred<PickedDirDTO | null>>[] = [];
  const uploads: ReturnType<typeof deferred<UploadPreflight>>[] = [];
  const downloads: ReturnType<typeof deferred<DownloadPreflightView>>[] = [];
  api.pickUploadFiles = vi.fn(() => { const p = deferred<PickedFilesDTO | null>(); files.push(p); return p.promise; });
  const folders: ReturnType<typeof deferred<PickedFilesDTO | null>>[] = [];
  api.pickUploadDirectory = vi.fn(() => { const p = deferred<PickedFilesDTO | null>(); folders.push(p); return p.promise; });
  api.pickDownloadDir = vi.fn(() => { const p = deferred<PickedDirDTO | null>(); dirs.push(p); return p.promise; });
  api.preflightUpload = vi.fn(() => { const p = deferred<UploadPreflight>(); uploads.push(p); return p.promise; });
  api.preflightDownload = vi.fn(() => { const p = deferred<DownloadPreflightView>(); downloads.push(p); return p.promise; });
  const started = deferred<string>();
  api.startUpload = vi.fn(() => started.promise);
  api.startDownload = vi.fn(() => started.promise);
  api.connect = vi.fn().mockRejectedValue(new Error("reconnect failed"));
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source";
  browser.containerName = "media";
  browser.prefix = "selected/";
  browser.localTransfer = mode;
  adjust?.(api, browser);
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
    if (mode === "upload") files[index].resolve({ token: "files-" + index, files: ["a.txt"], folder: "", skipped: 0, skippedSample: [], emptyDirectories: 0 });
    else dirs[index].resolve({ token: "dir-" + index, dir: "chosen directory " + index });
    await settle();
  };
  const resolvePreflight = async (index = 0, reject = false, warning = false, largeObjects = false) => {
    const problems = reject || warning ? [{ name: "a.txt", kind: "collision", detail: "reported issue", reject }] : [];
    if (mode === "upload") uploads[index].resolve({
      items: [{ path: "", name: "selected/a.txt", size: 5, route: "rclone" }], problems, totalBytes: 5,
      largeObjectsAsDlo: largeObjects,
    });
    else downloads[index].resolve({
      itemSamples: [{ name: "selected/a.txt", relPath: "a.txt", size: 5, route: "rclone", dir: false }],
      problemSamples: problems, totalBytes: 5, objects: 1, problemCount: problems.length, rejected: reject,
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
  const folderButton = () => [...root.querySelectorAll<HTMLButtonElement>("footer button")].find((b) => b.textContent?.trim() === "Choose folder…");
  const chooseFolder = () => { folderButton()!.click(); flushSync(); };
  return { api, browser, root, onClose, component, files, folders, dirs, uploads, downloads, started, mode,
    pickButton, startButton, folderButton, choose, chooseFolder, resolvePicker, resolvePreflight, rejectReply, reach };
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
    const expected = { bookmarkId: "source", container: "media", prefix: "selected/", profile: "auto",
      settings: { chunkSize: "", transfers: 0, checkers: 0, tpsLimit: 0, bwLimit: "" , bufferSize: ""},
      // The dialog never acknowledges an overwrite, and the exact-forwarding
      // assertion is where that stays visible (ADR 0081 decision 1).
      ...(mode === "upload"
        ? { selectionToken: "files-0", acknowledgeOverwrite: false }
        : { destDirToken: "dir-0" }),
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
    const reveal = vi.spyOn(f.browser.jobs, "reveal");
    [...f.root.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Show in jobs")!.click();
    expect(f.onClose).toHaveBeenCalled();
    expect(reveal).toHaveBeenCalledWith("accepted-job");
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
  { token: "files", files: null, folder: "", skipped: 0, skippedSample: [], emptyDirectories: 0 }, { token: "files", files: [], folder: "", skipped: 0, skippedSample: [], emptyDirectories: 0 }, { token: "", files: ["a.txt"], folder: "", skipped: 0, skippedSample: [], emptyDirectories: 0 },
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

it("counts download objects from the first-class field, not the sample", async () => {
  const f = show("download"); f.choose(); await f.resolvePicker();
  f.downloads[0].resolve({
    itemSamples: [{ name: "selected/a.txt", relPath: "a.txt", size: 5, route: "rclone", dir: false }],
    problemSamples: [], totalBytes: 1025, objects: 205, problemCount: 0, rejected: false,
  });
  await settle();
  expect(f.root.textContent).toContain("205");
  expect(f.root.textContent).not.toMatch(/\b1 objects\b/);
  expect(f.startButton()?.disabled).toBe(false);
});

it("arms download Start from rejected, not from sampled problems", async () => {
  const f = show("download"); f.choose(); await f.resolvePicker();
  f.downloads[0].resolve({
    itemSamples: [], problemSamples: [], totalBytes: 0, objects: 205, problemCount: 201, rejected: true,
  });
  await settle();
  expect(f.root.textContent).toContain("Rejected");
  expect(f.root.textContent).toContain("201 problems (0 shown)");
  expect(f.startButton()?.disabled ?? true).toBe(true);
});

it("does not claim a truncated problem list when every sampled row is on screen", async () => {
  const f = show("download"); f.choose(); await f.resolvePicker();
  const problemSamples = [
    ...Array.from({ length: 3 }, (_, i) => ({ name: "r" + i, kind: "collision", detail: "reject", reject: true })),
    ...Array.from({ length: 7 }, (_, i) => ({ name: "w" + i, kind: "portability", detail: "warn", reject: false })),
  ];
  f.downloads[0].resolve({
    itemSamples: [], problemSamples, totalBytes: 0, objects: 10, problemCount: 10, rejected: true,
  });
  await settle();
  expect(f.root.querySelectorAll(".problem")).toHaveLength(10);
  expect(f.root.textContent).not.toMatch(/\d+ problems \(\d+ shown\)/);
  expect(f.startButton()?.disabled ?? true).toBe(true);
});

it("discloses a truncated warning sample without rebuilding the count from it", async () => {
  const f = show("download"); f.choose(); await f.resolvePicker();
  const problemSamples = Array.from({ length: 200 }, (_, i) => (
    { name: "w" + i, kind: "portability", detail: "warn", reject: false }
  ));
  f.downloads[0].resolve({
    itemSamples: [{ name: "selected/a.txt", relPath: "a.txt", size: 5, route: "rclone", dir: false }],
    problemSamples, totalBytes: 5, objects: 7000, problemCount: 5000, rejected: false,
  });
  await settle();
  expect(f.root.querySelectorAll(".problem")).toHaveLength(200);
  expect(f.root.textContent).toContain("7000");
  expect(f.root.textContent).toContain("5000 problems (200 shown)");
  expect(f.startButton()?.disabled).toBe(false);
});

it("names the destination large-object placement for an upload that segments", async () => {
  const f = show("upload");
  await f.reach("preflight");
  await f.resolvePreflight(0, false, false, true);
  expect(f.root.querySelector(".note")?.textContent).toContain("destination DLOs");
});

it("omits the large-object note when no selected file would segment", async () => {
  const f = show("upload");
  await f.reach("ready");
  expect(f.root.querySelector(".note")).toBeNull();
  expect(f.root.textContent).not.toContain("destination DLOs");
});

it("never shows the large-object note for a download", async () => {
  const f = show("download");
  await f.reach("ready");
  expect(f.root.querySelector(".note")).toBeNull();
});

it.each(["upload", "download"] as const)("forwards the complete captured %s request without rebuilding its scope", async (mode) => {
  const api = newFakeApi();
  api.preflightUpload = vi.fn().mockResolvedValue({ items: [], problems: [], totalBytes: 0, largeObjectsAsDlo: false });
  api.preflightDownload = vi.fn().mockResolvedValue({ itemSamples: [], problemSamples: [], totalBytes: 0, objects: 0, problemCount: 0, rejected: false });
  api.startUpload = vi.fn().mockResolvedValue("job"); api.startDownload = vi.fn().mockResolvedValue("job");
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "another"; browser.containerName = "another"; browser.prefix = "another/";
  const scope = { bookmarkId: "captured", container: "media", prefix: "  exact/%\n", profile: "aggressive",
    settings: { chunkSize: "2Mi", transfers: 3, checkers: 4, tpsLimit: 5, bwLimit: "6M" , bufferSize: ""},
  };
  if (mode === "upload") {
    const req = { ...scope, selectionToken: "files", acknowledgeOverwrite: false };
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

function setField(f: Fixture, selector: string, value: string) {
  const element = f.root.querySelector<HTMLInputElement | HTMLSelectElement>(selector)!;
  element.value = value;
  element.dispatchEvent(new Event(element instanceof HTMLSelectElement ? "change" : "input", { bubbles: true }));
  flushSync();
}

// §38.3: the per-job settings an operator chooses are what the job is asked
// to run with. A download segments nothing, so it offers and sends no chunk.
describe.each(["upload", "download"] as const)("%s per-job settings", (mode) => {
  it("sends the chosen profile and overrides with the check and the start", async () => {
    const f = show(mode);
    expect(f.root.querySelector('input[placeholder="5Gi (default)"]') !== null).toBe(mode === "upload");
    setField(f, ".advanced select", "aggressive");
    setField(f, 'input[placeholder="4 (default)"]', "6");
    setField(f, 'input[placeholder="unlimited"]', "10M");
    if (mode === "upload") setField(f, 'input[placeholder="5Gi (default)"]', "64Mi");
    await f.reach("ready");
    f.startButton()!.click(); flushSync();
    const sent = expect.objectContaining({
      profile: "aggressive",
      settings: { chunkSize: mode === "upload" ? "64Mi" : "", transfers: 6, checkers: 0, tpsLimit: 0, bwLimit: "10M", bufferSize: "" },
    });
    expect(mode === "upload" ? f.api.preflightUpload : f.api.preflightDownload).toHaveBeenCalledExactlyOnceWith(sent);
    expect(mode === "upload" ? f.api.startUpload : f.api.startDownload).toHaveBeenCalledExactlyOnceWith(sent);
  });

  it("asks for a new check when a setting changes after the report", async () => {
    const f = show(mode); await f.reach("ready");
    setField(f, 'input[placeholder="4 (default)"]', "6");
    expectDisarmed(f);
  });

  // The application's first settings read may still be running when the
  // dialog opens; its saved default must still reach the job.
  async function showWithHeldSettings() {
    const settings = deferred<AppSettingsDTO>();
    const saved = await newFakeApi().appSettings();
    const f = show(mode, (api, browser) => {
      api.appSettings = () => settings.promise;
      void browser.settings.load();
    });
    const arrive = async () => { settings.resolve({ ...saved, defaultProfile: "aggressive" }); await settle(); };
    return { f, arrive };
  }

  it("adopts the saved default profile when the settings arrive after it opened", async () => {
    const { f, arrive } = await showWithHeldSettings();
    expect(f.root.querySelector<HTMLSelectElement>(".advanced select")!.value).toBe("auto");
    await arrive();
    expect(f.root.querySelector<HTMLSelectElement>(".advanced select")!.value).toBe("aggressive");
    await f.reach("ready");
    f.startButton()!.click(); flushSync();
    const sent = expect.objectContaining({ profile: "aggressive" });
    expect(mode === "upload" ? f.api.preflightUpload : f.api.preflightDownload).toHaveBeenCalledExactlyOnceWith(sent);
    expect(mode === "upload" ? f.api.startUpload : f.api.startDownload).toHaveBeenCalledExactlyOnceWith(sent);
  });

  it("keeps a profile the operator chose before the settings arrived", async () => {
    const { f, arrive } = await showWithHeldSettings();
    setField(f, ".advanced select", "balanced");
    await arrive();
    expect(f.root.querySelector<HTMLSelectElement>(".advanced select")!.value).toBe("balanced");
  });
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

// ADR 0081: an upload may replace what is live, on an acknowledgement the
// operator gave and a confirmation the BACKEND raises. These cases cover the
// page's whole part in that: offering the question, carrying the answer, and
// never being the thing that authorises the replacement.
describe("upload replacement", () => {
  const liveName = "selected/a.txt";
  // The kind the Go preflight actually reports for a live destination name.
  const collision = (reject: boolean) => ({
    name: liveName, kind: "exists",
    detail: reject
      ? "already live at the destination; an upload replaces nothing unless you ask it to"
      : "already live at the destination; this upload will replace it",
    reject,
  });
  const replaceButton = (f: Fixture) =>
    [...f.root.querySelectorAll<HTMLButtonElement>("footer button")]
      .find((b) => b.textContent?.trim().startsWith("Replace "));
  // What a reader sees: the markup wraps these sentences across source lines,
  // and a browser collapses that run of whitespace into one space.
  const visible = (f: Fixture) => (f.root.textContent ?? "").replace(/\s+/g, " ");

  // The replacing preflight's reply: the collision comes back as a warning
  // and the item is marked as replacing, which is what the Go route reports
  // once the acknowledgement is set.
  async function resolveReplacing(f: Fixture) {
    f.uploads[1].resolve({
      items: [{ path: "", name: liveName, size: 5, route: "rclone", replaces: true }],
      problems: [collision(false)], totalBytes: 5, largeObjectsAsDlo: false,
    });
    await settle();
  }

  async function reachCollision(f: Fixture, extra: { name: string; kind: string; detail: string; reject: boolean }[] = []) {
    f.choose();
    await f.resolvePicker();
    f.uploads[0].resolve({
      items: [], problems: [collision(true), ...extra], totalBytes: 0, largeObjectsAsDlo: false,
    });
    await settle();
    return f;
  }

  it("offers the replacement and says who will ask about it", async () => {
    const f = await reachCollision(show("upload"));
    expect(f.root.querySelector(".bad")?.textContent).toContain("Rejected");
    expect(f.startButton()).toBeUndefined();
    expect(replaceButton(f)?.textContent?.trim()).toBe("Replace 1 existing object…");
    const text = visible(f);
    expect(text).toContain("That name is already in use at the destination");
    expect(text).toContain("on its own dialog");
    expect(text).toContain("not on this page");
  });

  it("re-runs the preflight with the acknowledgement on the same selection", async () => {
    const f = await reachCollision(show("upload"));
    replaceButton(f)!.click();
    await settle();
    expect(f.api.pickUploadFiles).toHaveBeenCalledOnce();
    expect(f.api.preflightUpload).toHaveBeenCalledTimes(2);
    const first = vi.mocked(f.api.preflightUpload).mock.calls[0][0];
    const second = vi.mocked(f.api.preflightUpload).mock.calls[1][0];
    expect(first.acknowledgeOverwrite).toBe(false);
    expect(second.acknowledgeOverwrite).toBe(true);
    expect(second.selectionToken).toBe(first.selectionToken);
    expect({ ...second, acknowledgeOverwrite: false }).toEqual(first);
  });

  // Split from the sentence case below on purpose: the ONE thing that must
  // hold is that the request the backend is asked to start is the
  // acknowledged one, since that is what makes it raise the question. An
  // assertion about what the page says must not be able to fire first and
  // leave this unchecked.
  it("carries the acknowledgement into the start", async () => {
    const f = await reachCollision(show("upload"));
    replaceButton(f)!.click();
    await settle();
    await resolveReplacing(f);
    f.startButton()!.click();
    await settle();
    expect(f.api.startUpload).toHaveBeenCalledOnce();
    expect(vi.mocked(f.api.startUpload).mock.calls[0][0]).toEqual(
      { ...vi.mocked(f.api.preflightUpload).mock.calls[1][0] });
    expect(vi.mocked(f.api.startUpload).mock.calls[0][0].acknowledgeOverwrite).toBe(true);
  });

  it("says what the acknowledged plan will do and stops offering the question", async () => {
    const f = await reachCollision(show("upload"));
    replaceButton(f)!.click();
    await settle();
    await resolveReplacing(f);
    expect(visible(f)).toContain("will REPLACE what is at 1 name.");
    expect(visible(f)).toContain("naming the account and what it observed");
    expect(replaceButton(f)).toBeUndefined();
    expect(f.startButton()).toBeDefined();
  });

  // A collision is a question. Anything else rejecting is not, and a report
  // holding both must not become answerable because one of its problems was.
  it("does not offer a replacement when something else also rejects", async () => {
    const f = await reachCollision(show("upload"), [{
      name: "b.txt", kind: "unreadable", detail: "cannot be read", reject: true,
    }]);
    expect(replaceButton(f)).toBeUndefined();
    expect(f.startButton()).toBeUndefined();
    expect(visible(f)).not.toContain("on its own dialog");
    expect(f.api.preflightUpload).toHaveBeenCalledOnce();
  });

  // The offer belongs to the scope it was computed under, like every other
  // stage of this dialog.
  it("withdraws the offer when the scope moves under it", async () => {
    const f = await reachCollision(show("upload"));
    changeScope(f, "container");
    await settle();
    expect(replaceButton(f)).toBeUndefined();
    expect(f.root.querySelector(".report")).toBeNull();
    expect(f.api.preflightUpload).toHaveBeenCalledOnce();
  });

  // A download never asks this question: its own refusal to overwrite is
  // ADR 0045's and is not an acknowledgeable one.
  it("never offers a replacement in download mode", async () => {
    const f = show("download");
    f.choose();
    await f.resolvePicker();
    f.downloads[0].resolve({
      itemSamples: [], problemSamples: [{ name: "a.txt", kind: "exists", detail: "already exists", reject: true }],
      totalBytes: 0, objects: 0, problemCount: 1, rejected: true,
    });
    await settle();
    expect(replaceButton(f)).toBeUndefined();
    expect(f.startButton()).toBeUndefined();
  });
});

// ADR 0103: the folder pick runs the same preflight under its own token and
// shows the folder's summary beside the report; a folder with nothing to upload
// shows the summary alone and preflights nothing. Download has no folder button.
describe("folder upload", () => {
  it("preflights the folder's selection and shows its summary", async () => {
    const f = show("upload");
    f.chooseFolder();
    expect(f.api.pickUploadDirectory).toHaveBeenCalledExactlyOnceWith({ bookmarkId: "source", container: "media", prefix: "selected/" });
    f.folders[0].resolve({ token: "folder-0", files: ["photos/a.txt", "photos/b/c.txt"], folder: "photos", skipped: 2, skippedSample: ["x", "y"], emptyDirectories: 1 });
    await settle();
    expect(f.api.preflightUpload).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ selectionToken: "folder-0" }));
    await f.resolvePreflight();
    // The folder lands as a folder of its own name under the prefix.
    expect(f.root.textContent).toContain('Folder "photos": 2 files to upload into media/selected/photos/; 2 entries skipped');
    expect(f.root.textContent).toContain("1 empty directory is not uploaded");
    expect(f.startButton()?.disabled).toBe(false);
    // Starting spends the selection, so nothing goes on describing it.
    f.startButton()!.click();
    flushSync();
    expect(f.root.textContent).not.toContain('Folder "photos"');
  });

  // ADR 0103 decision 6: what the walk left out comes back in the report as
  // rows that reject nothing, so they render among the warnings, the count of
  // those not named included, and the upload can still start.
  it("renders what the walk left out among the warnings and still starts", async () => {
    const f = show("upload");
    f.chooseFolder();
    f.folders[0].resolve({ token: "folder-0", files: ["photos/a.txt"], folder: "photos", skipped: 3, skippedSample: ["x", "sub/y"], emptyDirectories: 1 });
    await settle();
    const skipped = "a link or other special entry, which a folder upload never follows; not uploaded";
    f.uploads[0].resolve({
      items: [{ path: "", name: "selected/photos/a.txt", size: 5, route: "rclone" }], totalBytes: 5, largeObjectsAsDlo: false,
      problems: [
        { name: "photos/x", kind: "skipped", detail: skipped, reject: false },
        { name: "photos/sub/y", kind: "skipped", detail: skipped, reject: false },
        { name: "photos", kind: "skipped", detail: "3 entries were skipped in all; the first 2 are named above", reject: false },
        { name: "photos", kind: "empty-directories", detail: "1 empty directory produces no object: the store has no directories", reject: false },
      ],
    });
    await settle();
    const rows = [...f.root.querySelectorAll(".warn-row")].map((row) => row.textContent!.replace(/\s+/g, " ").trim());
    expect(rows).toEqual([
      `skipped photos/x ${skipped}`,
      `skipped photos/sub/y ${skipped}`,
      "skipped photos 3 entries were skipped in all; the first 2 are named above",
      "empty-directories photos 1 empty directory produces no object: the store has no directories",
    ]);
    expect(f.root.textContent).not.toContain("Rejected");
    expect(f.startButton()?.disabled).toBe(false);
  });

  it("shows a folder that holds nothing to upload and starts nothing", async () => {
    const f = show("upload");
    f.chooseFolder();
    f.folders[0].resolve({ token: "", files: [], folder: "hollow", skipped: 1, skippedSample: ["link"], emptyDirectories: 1 });
    await settle();
    expect(f.api.preflightUpload).not.toHaveBeenCalled();
    // Nothing to upload is a warning, not a hint the operator can miss.
    const notice = f.root.querySelector(".folder.callout.warning");
    expect(notice?.textContent).toContain('Nothing to upload: folder "hollow" holds no files');
    expect(f.startButton()).toBeUndefined();
  });

  it("offers no folder pick for a download", () => {
    const f = show("download");
    expect(f.folderButton()).toBeUndefined();
  });
});

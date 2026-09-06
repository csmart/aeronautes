import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { BookmarkDTO, CopyPlanDTO, DeletePreviewDTO } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import TransferDialog from "./TransferDialog.svelte";
import DeleteDialog from "./DeleteDialog.svelte";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

const copyPlan: CopyPlanDTO = {
  summary: {
    new: 1, same: 0, changed: 0, unknown: 0, destOnly: 0,
    transferObjects: 1, transferBytes: 5, overwrites: 0, nativeRouted: 0, destOnlyBytes: 0,
  },
  dataPath: "current comparison", overwriteBytes: 0,
  versionedDestination: false, largeObjectsAsDlo: false,
};
const deletePlan: DeletePreviewDTO = {
  objects: 1, bytes: 5, possibleManifests: 0, strategy: "current deletion",
};

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
});

async function settle() {
  // Reply ordering is selected by deferred promises, never by a timer.
  for (let i = 0; i < 8; i++) {
    await Promise.resolve();
    flushSync();
  }
}

function input(element: HTMLInputElement | HTMLSelectElement, value: string) {
  element.value = value;
  element.dispatchEvent(new Event(element.tagName === "SELECT" ? "change" : "input", { bubbles: true }));
  flushSync();
}

function clickEvenIfDisabled(element: HTMLButtonElement) {
  element.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  flushSync();
}

type Kind = "copy" | "delete";
function show(kind: Kind) {
  const api = newFakeApi();
  const copies: ReturnType<typeof deferred<CopyPlanDTO>>[] = [];
  const deletes: ReturnType<typeof deferred<DeletePreviewDTO>>[] = [];
  api.planCopy = vi.fn(() => {
    const reply = deferred<CopyPlanDTO>(); copies.push(reply); return reply.promise;
  });
  api.previewDelete = vi.fn(() => {
    const reply = deferred<DeletePreviewDTO>(); deletes.push(reply); return reply.promise;
  });
  const startReply = deferred<string>();
  api.startCopy = vi.fn(() => startReply.promise);
  api.startDelete = vi.fn(() => startReply.promise);
  api.startMirror = vi.fn().mockRejectedValue(new Error("Mirror is unavailable"));
  api.connect = vi.fn().mockRejectedValue(new Error("reconnect failed"));
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source";
  browser.containerName = "media";
  browser.prefix = "selected/";
  const bookmark = (id: string): BookmarkDTO => ({
    id, displayName: id, authUrl: "https://swift.invalid/auth/v1.0", username: "test:tester",
    storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: true,
  });
  browser.bookmarks = [bookmark("source"), bookmark("destination")];
  const root = document.createElement("div");
  document.body.append(root);
  const onClose = vi.fn();
  const component = kind === "copy"
    ? mount(TransferDialog, { target: root, props: { browser, onClose } })
    : mount(DeleteDialog, { target: root, props: { browser, onClose } });
  mounted.add(component);
  flushSync();
  if (kind === "copy") input(root.querySelector<HTMLInputElement>('input[placeholder="container"]')!, "backup");

  const button = (label: string) => [...root.querySelectorAll("button")].find((b) => b.textContent?.trim() === label)!;
  const previewButton = () => root.querySelector<HTMLButtonElement>("footer button")!;
  const startButton = () => root.querySelector<HTMLButtonElement>("footer button:last-child")!;
  const preview = () => { previewButton().click(); flushSync(); };
  const arm = () => {
    const confirm = root.querySelector<HTMLInputElement>(".confirm input");
    if (confirm) input(confirm, browser.containerName ?? "");
  };
  const resolve = async (index = 0, label = "current") => {
    if (kind === "copy") copies[index].resolve({ ...copyPlan, dataPath: label });
    else deletes[index].resolve({ ...deletePlan, strategy: label });
    await settle();
  };
  const reject = async (index = 0) => {
    if (kind === "copy") copies[index].reject(new Error("obsolete preview error"));
    else deletes[index].reject(new Error("obsolete preview error"));
    await settle();
  };
  return { kind, api, browser, root, onClose, component, copies, deletes, startReply, button,
    previewButton, startButton, preview, arm, resolve, reject };
}

type Fixture = ReturnType<typeof show>;
const scopeChanges = ["bookmark", "container", "prefix"] as const;
function changeScope(f: Fixture, field: typeof scopeChanges[number], revert: boolean) {
  if (field === "bookmark") {
    f.browser.connectedBookmarkId = "destination";
    if (revert) f.browser.connectedBookmarkId = "source";
  } else if (field === "container") {
    f.browser.containerName = "elsewhere";
    if (revert) f.browser.containerName = "media";
  } else {
    f.browser.prefix = "elsewhere/";
    if (revert) f.browser.prefix = "selected/";
  }
  // Change-and-revert deliberately happens before a reactive effect can run.
  flushSync();
}

describe.each(["copy", "delete"] as const)("%s preview binding", (kind) => {
  it.each(scopeChanges.flatMap((field) => [false, true].flatMap((revert) =>
    ["pending", "ready"].map((phase) => ({ field, revert, phase })),
  )))("disarms on $field change, revert=$revert, phase=$phase", async ({ field, revert, phase }) => {
    const f = show(kind);
    f.preview();
    if (phase === "ready") { await f.resolve(); f.arm(); }
    changeScope(f, field, revert);
    if (phase === "pending") await f.resolve();
    f.arm();
    expect(f.startButton().disabled).toBe(true);
    expect(f.root.querySelector(".preview")).toBeNull();
    clickEvenIfDisabled(f.startButton());
    expect(f.api.startCopy).not.toHaveBeenCalled();
    expect(f.api.startDelete).not.toHaveBeenCalled();
  });

  it.each(["pending", "ready"])("disarms on same-bookmark reconnect even if the old location returns, phase=%s", async (phase) => {
    const f = show(kind);
    f.preview();
    if (phase === "ready") { await f.resolve(); f.arm(); }
    await expect(f.browser.connect("source")).rejects.toThrow("reconnect failed");
    expect([f.browser.connectedBookmarkId, f.browser.containerName, f.browser.prefix]).toEqual([null, null, ""]);
    f.browser.connectedBookmarkId = "source"; f.browser.containerName = "media"; f.browser.prefix = "selected/";
    if (phase === "pending") await f.resolve();
    await settle();
    f.arm();
    expect(f.startButton().disabled).toBe(true);
    expect(f.root.querySelector(".preview")).toBeNull();
  });

  it("refuses Preview and Start throughout a reconnect attempt", async () => {
    const f = show(kind);
    f.preview(); await f.resolve(); f.arm();
    const reconnect = deferred<never>();
    f.api.connect = vi.fn(() => reconnect.promise);
    const pending = f.browser.connect("source").catch((error: Error) => error.message);
    flushSync();
    expect(f.previewButton().disabled).toBe(true);
    clickEvenIfDisabled(f.previewButton()); clickEvenIfDisabled(f.startButton());
    expect(kind === "copy" ? f.api.planCopy : f.api.previewDelete).toHaveBeenCalledTimes(1);
    expect(f.api.startCopy).not.toHaveBeenCalled();
    expect(f.api.startDelete).not.toHaveBeenCalled();
    reconnect.reject(new Error("failed reconnect"));
    expect(await pending).toBe("failed reconnect"); await settle();
    expect(f.previewButton().disabled).toBe(true);
    expect(f.startButton().disabled).toBe(true);
  });

  it("starts the exact previewed request once and consumes the preview on error", async () => {
    const f = show(kind);
    f.preview();
    await f.resolve();
    f.arm();
    expect(f.startButton().disabled).toBe(false);
    const start = f.startButton();
    clickEvenIfDisabled(start);
    clickEvenIfDisabled(start);
    if (kind === "copy") {
      expect(f.api.startCopy).toHaveBeenCalledExactlyOnceWith(vi.mocked(f.api.planCopy).mock.calls[0][0]);
    } else {
      expect(f.api.startDelete).toHaveBeenCalledExactlyOnceWith({
        ...vi.mocked(f.api.previewDelete).mock.calls[0][0], confirm: "media",
      });
    }
    f.startReply.reject(new Error("start outcome unavailable"));
    await settle();
    expect(f.root.textContent).toContain("start outcome unavailable");
    expect(f.startButton().disabled).toBe(true);
    clickEvenIfDisabled(f.startButton());
    expect(kind === "copy" ? f.api.startCopy : f.api.startDelete).toHaveBeenCalledTimes(1);
  });

  it("refuses synthetic Start before any preview", () => {
    const f = show(kind);
    clickEvenIfDisabled(f.startButton());
    expect(f.api.startCopy).not.toHaveBeenCalled();
    expect(f.api.startDelete).not.toHaveBeenCalled();
  });

  it("checks the current scope in Start before the invalidation effect runs", async () => {
    const f = show(kind);
    f.preview(); await f.resolve(); f.arm();
    const start = f.startButton();
    f.browser.prefix = "elsewhere/";
    start.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    expect(f.api.startCopy).not.toHaveBeenCalled();
    expect(f.api.startDelete).not.toHaveBeenCalled();
    flushSync();
    expect(f.startButton().disabled).toBe(true);
  });

  it("does not launch a second preview or Start while its preview is pending", async () => {
    const f = show(kind);
    f.preview();
    clickEvenIfDisabled(f.previewButton());
    clickEvenIfDisabled(f.startButton());
    expect(kind === "copy" ? f.api.planCopy : f.api.previewDelete).toHaveBeenCalledTimes(1);
    expect(f.api.startCopy).not.toHaveBeenCalled();
    expect(f.api.startDelete).not.toHaveBeenCalled();
    await f.resolve(); f.arm();
    expect(f.startButton().disabled).toBe(false);
  });

  it("shows the current Start result and keeps the consumed preview unavailable", async () => {
    const f = show(kind);
    f.preview(); await f.resolve(); f.arm();
    f.startButton().click(); flushSync();
    f.startReply.resolve("accepted-job"); await settle();
    expect(f.root.textContent).toContain("Job accepted-job started");
    expect(f.root.querySelector(".preview")).toBeNull();
    expect(kind === "copy" ? f.api.startCopy : f.api.startDelete).toHaveBeenCalledTimes(1);
  });

  it.each(["success", "failure"])("keeps a late Start %s out of a changed intent", async (outcome) => {
    const f = show(kind);
    f.preview(); await f.resolve(); f.arm();
    f.startButton().click(); flushSync();
    changeScope(f, "prefix", false);
    clickEvenIfDisabled(f.previewButton());
    expect(kind === "copy" ? f.api.planCopy : f.api.previewDelete).toHaveBeenCalledTimes(1);
    if (outcome === "success") f.startReply.resolve("obsolete-job");
    else f.startReply.reject(new Error("obsolete start error"));
    await settle();
    expect(f.root.textContent).not.toContain("obsolete");
    expect(f.startButton().disabled).toBe(true);
    expect(f.previewButton().disabled).toBe(false);
  });

  it.each(["success", "failure"])("ignores late %s and finalisation while a newer preview waits", async (outcome) => {
    const f = show(kind);
    f.preview();
    changeScope(f, "prefix", false);
    expect(f.previewButton().disabled).toBe(false);
    f.preview();
    if (outcome === "success") await f.resolve(0, "obsolete");
    else await f.reject(0);
    expect(f.previewButton().disabled).toBe(true);
    expect(f.startButton().disabled).toBe(true);
    expect(f.root.querySelector(".preview")).toBeNull();
    expect(f.root.textContent).not.toContain("obsolete");
    await f.resolve(1, "newest");
    f.arm();
    expect(f.root.textContent).toContain("newest");
    expect(f.startButton().disabled).toBe(false);
  });

  it.each(["success", "failure"])("keeps a newer completed preview after a late %s", async (outcome) => {
    const f = show(kind);
    f.preview();
    changeScope(f, "prefix", false);
    expect(f.previewButton().disabled).toBe(false);
    f.preview();
    await f.resolve(1, "newest");
    if (outcome === "success") await f.resolve(0, "obsolete");
    else await f.reject(0);
    f.arm();
    expect(f.root.textContent).toContain("newest");
    expect(f.root.textContent).not.toContain("obsolete");
    expect(f.startButton().disabled).toBe(false);
  });

  it("clears an earlier preview before retry and leaves a failed preview disarmed", async () => {
    const f = show(kind);
    f.preview(); await f.resolve(); f.arm();
    f.preview();
    expect(f.startButton().disabled).toBe(true);
    expect(f.root.querySelector(".preview")).toBeNull();
    await f.reject(1);
    expect(f.root.textContent).toContain("obsolete preview error");
    expect(f.startButton().disabled).toBe(true);
  });

  it.each(["button", "scrim"].flatMap((route) => ["success", "failure"].map((outcome) => ({ route, outcome }))))(
    "ends its lifetime immediately on $route close with late $outcome", async ({ route, outcome }) => {
    const f = show(kind);
    f.preview();
    if (route === "button") f.root.querySelector<HTMLButtonElement>('button[aria-label="Close"]')!.click();
    else f.root.querySelector<HTMLElement>(".scrim")!.click();
    expect(f.onClose).toHaveBeenCalledOnce();
    // The parent's unmount may follow later in the event turn.
    if (outcome === "success") await f.resolve();
    else await f.reject();
    f.arm();
    expect(f.root.querySelector(".preview")).toBeNull();
    expect(f.root.textContent).not.toContain("obsolete");
    clickEvenIfDisabled(f.startButton());
    expect(f.api.startCopy).not.toHaveBeenCalled();
    expect(f.api.startDelete).not.toHaveBeenCalled();
  });

  it("cannot publish after unmount or arm a reopened dialog", async () => {
    const old = show(kind);
    old.preview();
    await unmount(old.component); mounted.delete(old.component);
    const current = show(kind);
    current.preview();
    await old.resolve();
    expect(old.root.textContent).toBe("");
    expect(current.startButton().disabled).toBe(true);
    await current.resolve(); current.arm();
    expect(current.startButton().disabled).toBe(false);
    expect(old.api.startCopy).not.toHaveBeenCalled();
    expect(old.api.startDelete).not.toHaveBeenCalled();
  });
});

const copyInputs = [
  { name: "destination bookmark", selector: ".fields select", value: "destination", original: "source" },
  { name: "destination container", selector: 'input[placeholder="container"]', value: "elsewhere", original: "backup" },
  { name: "destination prefix", selector: 'input[placeholder="prefix/ (optional)"]', value: "elsewhere/", original: "" },
  { name: "profile", selector: ".advanced select", value: "custom", original: "auto" },
  { name: "chunk size", selector: 'input[placeholder="5Gi (default)"]', value: "2Mi", original: "" },
  { name: "transfers", selector: 'input[placeholder="4 (default)"]', value: "2", original: "" },
  { name: "bandwidth", selector: 'input[placeholder="unlimited"]', value: "1M", original: "" },
];

it.each(copyInputs.flatMap((field) => [false, true].map((revert) => ({ ...field, revert }))))(
  "Copy disarms on $name input before blur, revert=$revert", async ({ selector, value, original, revert }) => {
    const f = show("copy"); f.preview();
    const element = f.root.querySelector<HTMLInputElement | HTMLSelectElement>(selector)!;
    input(element, value);
    if (revert) input(element, original);
    await f.resolve();
    expect(f.startButton().disabled).toBe(true);
    expect(f.root.querySelector(".preview")).toBeNull();
  },
);

it.each(["pending", "ready"])("Copy mode change and return requires a new preview, phase=%s", async (phase) => {
  const f = show("copy"); f.preview();
  if (phase === "ready") await f.resolve();
  f.button("Mirror").click(); flushSync();
  expect(f.button("Mirror unavailable").disabled).toBe(true);
  f.button("Copy").click(); flushSync();
  if (phase === "pending") await f.resolve();
  expect(f.startButton().disabled).toBe(true);
  expect(f.api.startMirror).not.toHaveBeenCalled();
  f.preview(); await f.resolve(1);
  expect(f.startButton().disabled).toBe(false);
});

it.each(["empty", "delete_container"])("Delete requires new confirmation after changing to %s and back", async (mode) => {
  const f = show("delete"); f.preview(); await f.resolve(); f.arm();
  f.root.querySelector<HTMLInputElement>(`input[value="${mode}"]`)!.click(); flushSync();
  f.root.querySelector<HTMLInputElement>('input[value="prefix"]')!.click(); flushSync();
  f.preview(); await f.resolve(1);
  expect(f.root.querySelector<HTMLInputElement>(".confirm input")!.value).toBe("");
  expect(f.startButton().disabled).toBe(true);
  f.arm(); expect(f.startButton().disabled).toBe(false);
});

it("Delete ignores a prefix preview after changing to delete the container", async () => {
  const f = show("delete"); f.preview();
  f.root.querySelector<HTMLInputElement>('input[value="delete_container"]')!.click(); flushSync();
  await f.resolve(); f.arm();
  expect(f.button("Delete container").disabled).toBe(true);
  expect(f.root.querySelector(".preview")).toBeNull();
});

it.each(["empty", "delete_container"])("Delete ignores a pending preview after changing to %s and back", async (mode) => {
  const f = show("delete"); f.preview();
  f.root.querySelector<HTMLInputElement>(`input[value="${mode}"]`)!.click(); flushSync();
  f.root.querySelector<HTMLInputElement>('input[value="prefix"]')!.click(); flushSync();
  await f.resolve(); f.arm();
  expect(f.startButton().disabled).toBe(true);
  expect(f.root.querySelector(".preview")).toBeNull();
});

it.each(["", "other", " media "])("Delete refuses a synthetic Start with confirmation %j", async (confirmation) => {
  const f = show("delete"); f.preview(); await f.resolve();
  input(f.root.querySelector<HTMLInputElement>(".confirm input")!, confirmation);
  clickEvenIfDisabled(f.startButton());
  expect(f.api.startDelete).not.toHaveBeenCalled();
});

it("Copy submits the complete previewed settings and both scopes", async () => {
  const f = show("copy");
  for (const field of copyInputs) input(f.root.querySelector<HTMLInputElement | HTMLSelectElement>(field.selector)!, field.value);
  f.preview(); await f.resolve();
  f.startButton().click(); flushSync();
  const expected = {
    sourceBookmarkId: "source", sourceContainer: "media", sourcePrefix: "selected/",
    destBookmarkId: "destination", destContainer: "elsewhere", destPrefix: "elsewhere/",
    profile: "custom", settings: { chunkSize: "2Mi", transfers: 2, checkers: 0, tpsLimit: 0, bwLimit: "1M" },
  };
  expect(f.api.planCopy).toHaveBeenCalledExactlyOnceWith(expected);
  expect(f.api.startCopy).toHaveBeenCalledExactlyOnceWith(expected);
  f.startReply.resolve("accepted-job"); await settle();
});

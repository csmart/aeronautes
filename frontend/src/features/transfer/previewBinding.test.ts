import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, AppSettingsDTO, BookmarkDTO, ContainerSettingsDTO, CopyPlanDTO, DeletePreviewDTO } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import TransferDialog from "./TransferDialog.svelte";
import { v1Offers } from "../../lib/v1.svelte";

// These cases exercise the Mirror comparison the release does not offer
// (v1Offers.mirrorPreview), so they turn it on for themselves.
beforeEach(() => { v1Offers.mirrorPreview = true; });
afterEach(() => { v1Offers.mirrorPreview = false; });
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
  objects: 1, bytes: 5, possibleManifests: 0, requestCost: 1, strategy: "current deletion",
  archiveOf: [], archiveUnestablished: "",
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
function show(kind: Kind, factsLoader?: () => Promise<ContainerSettingsDTO | null>, adjust?: (api: Api, browser: BrowserState) => void) {
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
    storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: true,
  });
  browser.bookmarks = [bookmark("source"), bookmark("destination")];
  if (factsLoader) browser.containerFacts = factsLoader;
  // The delete dialog is opened for one job (ADR 0121): the prefix being
  // browsed, unless a case opens it for one of the container's two.
  browser.deleteOpen = "prefix";
  adjust?.(api, browser);
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
    const words = browser.deleteOpen === "prefix" ? `${browser.containerName}/${browser.prefix}`
      : browser.deleteOpen === "delete_container" ? `delete ${browser.containerName}` : browser.containerName ?? "";
    if (confirm) input(confirm, words);
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
        ...vi.mocked(f.api.previewDelete).mock.calls[0][0], confirm: "media/selected/",
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
  { name: "destination prefix", selector: 'input[placeholder="folder path/ (optional)"]', value: "elsewhere/", original: "" },
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

// ADR 0121: the dialog is opened for one job and offers no choice of job. Its
// heading and its button name the job, and each job has its own words, so the
// bare name arms no job, the words for an emptying never arm the container's
// deletion, nor that deletion's words an emptying.
it.each([
  ["prefix", "Delete prefix", "media/selected/", ["media", "empty media", "delete media"]],
  ["empty", "Empty container", "empty media", ["media", "delete media", "media/selected/"]],
  ["delete_container", "Delete container", "delete media", ["media", "empty media", "media/selected/"]],
] as const)("Delete opened for %s names that job, offers no other and asks for its own words", async (job, heading, words, others) => {
  const f = show("delete", undefined, (_api, browser) => { browser.deleteOpen = job; });
  expect(f.root.querySelector("h2")?.textContent?.trim()).toBe(heading);
  expect(f.root.querySelector('input[type="radio"]')).toBeNull();
  f.preview();
  expect(vi.mocked(f.api.previewDelete).mock.calls[0][0]).toMatchObject({ mode: job, prefix: job === "prefix" ? "selected/" : "" });
  await f.resolve();
  const confirm = f.root.querySelector<HTMLInputElement>(".confirm input")!;
  expect(confirm.placeholder).toBe(words);
  for (const other of others) {
    input(confirm, other);
    expect(f.startButton().disabled).toBe(true);
  }
  input(confirm, words);
  expect(f.startButton().disabled).toBe(false);
  expect(f.startButton().textContent?.trim()).toBe(heading);
  f.startButton().click(); flushSync();
  expect(f.api.startDelete).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ mode: job, confirm: words }));
});

// "media" alone is the container name, which does not arm a prefix.
it.each(["", "other", " media ", "media", "selected/"])("Delete refuses a synthetic Start with confirmation %j", async (confirmation) => {
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
    profile: "custom", acknowledgeOverwrite: false,
    settings: { chunkSize: "2Mi", transfers: 2, checkers: 0, tpsLimit: 0, bwLimit: "1M", bufferSize: "" },
  };
  expect(f.api.planCopy).toHaveBeenCalledExactlyOnceWith(expected);
  expect(f.api.startCopy).toHaveBeenCalledExactlyOnceWith(expected);
  f.startReply.resolve("accepted-job"); await settle();
});

// The application's first settings read may still be running when the dialog
// opens; the saved default profile must still reach the copy.
it("Copy adopts the saved default profile when the settings arrive after it opened", async () => {
  const settings = deferred<AppSettingsDTO>();
  const saved = await newFakeApi().appSettings();
  const f = show("copy", undefined, (api, browser) => {
    api.appSettings = () => settings.promise;
    void browser.settings.load();
  });
  const profile = f.root.querySelector<HTMLSelectElement>(".advanced select")!;
  expect(profile.value).toBe("auto");
  settings.resolve({ ...saved, defaultProfile: "aggressive" }); await settle();
  expect(profile.value).toBe("aggressive");
  f.preview(); await f.resolve();
  expect(f.api.planCopy).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ profile: "aggressive" }));
});

it("the delete preview states one request for each object and that a later write is what goes", async () => {
  const f = show("delete");
  f.deletes.length = 0;
  f.preview();
  f.deletes[0].resolve({ ...deletePlan, objects: 12, bytes: 1000, possibleManifests: 2, requestCost: 12, strategy: "per-name" });
  await settle();
  const cost = f.root.querySelector(".cost")!.textContent!.replace(/\s+/g, " ");
  expect(cost).toContain("Each object is deleted by one request: 12 requests");
  expect((f.root.textContent ?? "").replace(/\s+/g, " ")).toContain("if something else has written to a name since this preview, that is what goes");
});

// ADR 0102 decision 3: the dialog says whose archive the container is, from the
// preview's own scan, and says nothing when nobody archives into it.
it("the delete preview names the containers that archive into this one", async () => {
  const f = show("delete");
  f.deletes.length = 0;
  f.preview();
  f.deletes[0].resolve({ ...deletePlan, archiveOf: ["photos"] });
  await settle();
  expect(f.root.textContent).toContain("This container is the versions archive of photos");

  f.browser.prefix = "elsewhere/"; flushSync();
  f.deletes.length = 0;
  f.preview();
  f.deletes[0].resolve({ ...deletePlan });
  await settle();
  expect(f.root.textContent).not.toContain("versions archive");
});

it("Delete waits for the versioning facts to settle before enabling or accepting Start", async () => {
  const facts = deferred<ContainerSettingsDTO | null>();
  const f = show("delete", () => facts.promise);
  f.preview();
  await f.resolve();
  f.arm();
  // The versioning facts are still loading: the dialog says so, Start stays
  // disabled, and a synthetic click is refused, so the destructive request
  // cannot be sent before its consequence has been disclosed.
  expect(f.root.textContent).toContain("Checking how this container keeps versions");
  expect(f.startButton().disabled).toBe(true);
  clickEvenIfDisabled(f.startButton());
  expect(f.api.startDelete).not.toHaveBeenCalled();
  // Once the facts settle — here as the unknown result, which still discloses a
  // consequence — Start becomes available and sends the previewed request.
  facts.resolve(null);
  await settle();
  expect(f.root.textContent).toContain("could not be read; assume the content is destroyed");
  expect(f.startButton().disabled).toBe(false);
  f.startButton().click();
  flushSync();
  expect(f.api.startDelete).toHaveBeenCalledOnce();
});

// The start button stays disabled until a preview exists and the name is
// typed in it; before the preview, the dialog says so.
it("Delete says to preview first until a preview exists", async () => {
  const f = show("delete");
  expect(f.root.querySelector(".next-step")?.textContent).toContain("Preview first");
  expect(f.root.querySelector(".confirm input")).toBeNull();
  f.preview(); await f.resolve();
  expect(f.root.querySelector(".next-step")).toBeNull();
  expect(f.root.querySelector(".confirm input")).not.toBeNull();
});

// The operator missed that Preview comes first: the highlight marks the next
// step, Preview until a preview is shown, then the start.
it.each(["copy", "delete"] as const)("%s highlights Preview, then the start", async (kind) => {
  const f = show(kind);
  const startClass = kind === "copy" ? "primary" : "danger";
  expect(f.previewButton().classList.contains("primary")).toBe(true);
  expect(f.startButton().classList.contains(startClass)).toBe(false);
  f.preview(); await f.resolve();
  expect(f.previewButton().classList.contains("primary")).toBe(false);
  expect(f.startButton().classList.contains(startClass)).toBe(true);
});

// A delete-container job that completes takes the browser back to the
// account; the dialog that started it closes instead of offering a new
// deletion of a container that is gone (0.5.0 test run).
it("Delete keeps its started job through a scope change and closes when its container is left", async () => {
  const f = show("delete", undefined, (_api, browser) => { browser.deleteOpen = "delete_container"; });
  f.preview(); await f.resolve(); f.arm();
  f.startButton().click(); flushSync();
  f.startReply.resolve("deleting-job"); await settle();
  expect(f.root.textContent).toContain("Job deleting-job started");
  // A started dialog only reports the job: what it says of the scope before
  // a start would read as an offer to delete more (0.5.0 test run).
  expect(f.root.querySelector("h2")?.textContent?.trim()).toBe("Deletion started");
  expect(f.root.querySelector(".scope")).toBeNull();
  expect(f.root.textContent).toContain("Deleting container media.");
  // When the drawer's poll sees the job complete, the dialog says it is done.
  f.browser.jobs.list = [{ id: "deleting-job", type: "delete_container", state: "complete", source: "media/", destination: "(deleted)",
    sourceBookmarkId: "source", destBookmarkId: "", createdAt: "", startedAt: "", finishedAt: "", engine: "native", settings: "", error: "",
    recoveryRecords: 0, pause: "", counters: { objectsPlanned: 3, objectsCopied: 3, objectsVerified: 0, objectsFailed: 0, bytesPlanned: 0,
      bytesCopied: 0, overwrites: 0, overwritesRefused: 0, nativeRouted: 0, objectsDeleted: 3 } }];
  flushSync();
  expect(f.root.querySelector("h2")?.textContent?.trim()).toBe("Deletion complete");
  expect(f.root.textContent).toMatch(/Deleted\s*container media:\s*3\s*objects\./);
  f.browser.prefix = "elsewhere/"; flushSync();
  expect(f.root.textContent).toContain("Job deleting-job started");
  expect(f.onClose).not.toHaveBeenCalled();
  f.browser.containerName = null; flushSync();
  expect(f.onClose).toHaveBeenCalled();
});

// A deletion removes manifests and leaves the segments of large objects,
// which this application and rclone keep in a second container named after
// the first. When a container of that name exists the dialog says so, and
// when its job has deleted the open container it stays to say that one is
// still there and to offer the way to it (0.5.0 test run). The name is all
// that is known, so the dialog neither calls its objects this container's
// segments nor says to delete it.
it("Delete names a container named for the open one's segments and offers it once its job is done", async () => {
  const row = (name: string, count: number, bytes: number) => ({ name, count, bytes, class: "normal", settings: null });
  const f = show("delete", undefined, (_api, browser) => {
    browser.containers = [row("media", 4, 4_000_000), row("media_segments", 3, 3_000_000)];
    browser.deleteOpen = "delete_container";
  });
  const open = vi.spyOn(f.browser, "openContainer").mockResolvedValue();
  const text = () => (f.root.textContent ?? "").replace(/\s+/g, " ");
  expect(text()).toContain("A container named media_segments exists, with 3 objects and 3.00 MB as listed.");
  // What a deletion takes from it is not known until a preview has counted
  // the large objects, so nothing is said of it before then.
  expect(text()).not.toContain("removes nothing from it");
  expect(text()).not.toMatch(/delete that container|free the space/);

  f.preview(); await f.resolve(); f.arm();
  expect(text()).toContain("A deletion here removes nothing from it.");
  f.startButton().click(); flushSync();
  f.startReply.resolve("deleting-job"); await settle();
  expect(f.button("Open that container")).toBeUndefined();
  // The job completes and the browser leaves the deleted container.
  f.browser.jobs.list = [{ id: "deleting-job", type: "delete_container", state: "complete", source: "media/", destination: "(deleted)",
    sourceBookmarkId: "source", destBookmarkId: "", createdAt: "", startedAt: "", finishedAt: "", engine: "native", settings: "", error: "",
    recoveryRecords: 0, pause: "", counters: { objectsPlanned: 4, objectsCopied: 4, objectsVerified: 0, objectsFailed: 0, bytesPlanned: 0,
      bytesCopied: 0, overwrites: 0, overwritesRefused: 0, nativeRouted: 0, objectsDeleted: 4 } }];
  f.browser.containerName = null; flushSync();
  expect(f.onClose).not.toHaveBeenCalled();
  expect(f.root.querySelector("h2")?.textContent?.trim()).toBe("Deletion complete");
  expect(text()).toContain("media_segments still exists, with 3 objects and 3.00 MB as listed; this deletion removed nothing from it.");

  f.button("Open that container").click(); flushSync();
  expect(f.onClose).toHaveBeenCalledTimes(1);
  expect(open).toHaveBeenCalledExactlyOnceWith("media_segments");
});

// ADR 0119 and ADR 0120: deleting each large object's segments with it is
// the operator's choice for one job. The option is offered only where the
// preview counted a possible manifest and the container keeps no versions,
// starts ticked, returns to ticked with a new preview, and changes the words
// to type, so the words for one kind of deletion never start the other.
it("Delete takes segments unless the option is unticked, and each choice has its own words", async () => {
  const facts = (versioningMode: string) => async () => ({ versioningMode, versioningArchive: "archive" }) as ContainerSettingsDTO;
  const manifests = { ...deletePlan, possibleManifests: 2 };
  const option = (f: Fixture) => f.root.querySelector<HTMLInputElement>('.option input[type="checkbox"]');
  const confirm = (f: Fixture) => f.root.querySelector<HTMLInputElement>(".confirm input")!;

  const none = show("delete", facts("off"));
  none.preview(); await none.resolve();
  expect(option(none)).toBeNull();
  // With no large object in scope nothing is asked for under another name.
  expect(confirm(none).placeholder).toBe("media/selected/");

  const versioned = show("delete", facts("history_location"));
  versioned.preview(); versioned.deletes[0].resolve(manifests); await settle();
  expect(option(versioned)).toBeNull();
  expect(versioned.root.textContent).toContain("Their segments cannot be deleted with them here: this container keeps versions");

  const f = show("delete", facts("off"));
  f.preview(); f.deletes[0].resolve(manifests); await settle();
  expect(option(f)!.checked).toBe(true);
  expect(confirm(f).placeholder).toBe("media/selected/ and segments");
  expect(f.root.textContent).toContain("one that does will lose its data");
  expect(f.root.textContent).not.toContain("Deletion is manifest-only");
  // The plain words do not arm a deletion that takes segments.
  f.arm();
  expect(f.startButton().disabled).toBe(true);
  clickEvenIfDisabled(f.startButton());
  expect(f.api.startDelete).not.toHaveBeenCalled();

  // Unticking changes the words, clears what was typed and says what stays.
  option(f)!.click(); flushSync();
  expect(confirm(f).value).toBe("");
  expect(confirm(f).placeholder).toBe("media/selected/");
  expect(f.root.textContent).toContain("Deletion is manifest-only");

  // A new preview returns the option to its starting value.
  f.preview(); f.deletes[1].resolve(manifests); await settle();
  expect(option(f)!.checked).toBe(true);
  input(confirm(f), "media/selected/ and segments");
  f.startButton().click(); flushSync();
  expect(f.api.startDelete).toHaveBeenCalledExactlyOnceWith({
    ...vi.mocked(f.api.previewDelete).mock.calls[0][0], confirm: "media/selected/ and segments", segments: true,
  });
  expect(vi.mocked(f.api.previewDelete).mock.calls[0][0].segments).toBe(false);

  // Unticked, the job is asked for the manifests alone, by the plain words.
  const plain = show("delete", facts("off"));
  plain.preview(); plain.deletes[0].resolve(manifests); await settle();
  option(plain)!.click(); flushSync();
  plain.arm();
  plain.startButton().click(); flushSync();
  expect(plain.api.startDelete).toHaveBeenCalledExactlyOnceWith({
    ...vi.mocked(plain.api.previewDelete).mock.calls[0][0], confirm: "media/selected/", segments: false,
  });
});

// ADR 0120 decision 2: an archived version and the object it came from can
// name the same segments, so where the preview names a container that keeps
// its versions here, or could not establish that none does, the option is
// offered unticked, says why, and is the operator's to tick.
it.each([
  ["names an owner", { archiveOf: ["photos"] }],
  ["could not cover the account", { archiveUnestablished: "the account holds more containers than the scan reads" }],
])("Delete starts the segments option unticked when the preview %s", async (_case, archive) => {
  const f = show("delete", async () => ({ versioningMode: "off", versioningArchive: "" }) as ContainerSettingsDTO);
  f.preview(); f.deletes[0].resolve({ ...deletePlan, possibleManifests: 1, ...archive }); await settle();
  const option = f.root.querySelector<HTMLInputElement>('.option input[type="checkbox"]')!;
  const confirm = f.root.querySelector<HTMLInputElement>(".confirm input")!;
  expect(option.checked).toBe(false);
  expect(confirm.placeholder).toBe("media/selected/");
  expect(f.root.textContent).toContain("It starts unticked here");
  option.click(); flushSync();
  expect(confirm.placeholder).toBe("media/selected/ and segments");
});

// After a job that took segments the notice no longer says the deletion
// removed nothing from the segments container, or quotes a count from
// before the job.
it("Delete says the job's records list what it deleted from the segments container", async () => {
  const row = (name: string, count: number, bytes: number) => ({ name, count, bytes, class: "normal", settings: null });
  const f = show("delete", async () => ({ versioningMode: "off", versioningArchive: "" }) as ContainerSettingsDTO, (_api, browser) => {
    browser.containers = [row("media", 4, 4_000_000), row("media_segments", 3, 3_000_000)];
  });
  f.preview(); f.deletes[0].resolve({ ...deletePlan, possibleManifests: 1 }); await settle();
  const option = f.root.querySelector<HTMLInputElement>('.option input[type="checkbox"]')!;
  expect(f.root.textContent).toContain("this deletion also deletes the segments of its large objects");
  option.click(); flushSync();
  expect(f.root.textContent).toContain("A deletion here removes nothing from it.");
  option.click(); flushSync();
  expect(f.root.textContent).not.toContain("A deletion here removes nothing from it.");
  input(f.root.querySelector<HTMLInputElement>(".confirm input")!, "media/selected/ and segments");
  f.startButton().click(); flushSync();
  f.startReply.resolve("deleting-job"); await settle();
  f.browser.jobs.list = [{ id: "deleting-job", type: "delete_prefix", state: "complete", source: "media/selected/", destination: "(deleted)",
    sourceBookmarkId: "source", destBookmarkId: "", createdAt: "", startedAt: "", finishedAt: "", engine: "native", settings: "", error: "",
    recoveryRecords: 0, pause: "", counters: { objectsPlanned: 4, objectsCopied: 4, objectsVerified: 0, objectsFailed: 0, bytesPlanned: 0,
      bytesCopied: 0, overwrites: 0, overwritesRefused: 0, nativeRouted: 0, objectsDeleted: 4 } }];
  flushSync();
  const text = (f.root.textContent ?? "").replace(/\s+/g, " ");
  expect(text).toContain("4 objects, the segments of large objects among them.");
  expect(text).toContain("media_segments still exists. The job's records list each segment deleted from it");
  expect(text).not.toContain("removed nothing from it");
  expect(text).not.toContain("3 objects");
});

it("Delete says nothing of segments when no container is named for them", () => {
  const f = show("delete");
  expect(f.root.textContent).not.toContain("_segments");
});

// A folder selected at the container's top is what the prefix deletion
// acts on, and the confirmation names it (ADR 0095 decision 7).
it("Delete prefix acts on the folder selected at the container's top", async () => {
  const f = show("delete", undefined, (_api, browser) => {
    browser.prefix = "";
    browser.entries = [{ kind: "prefix", name: "files/", bytes: 0, etag: "", lastModified: "", contentType: "", sloListed: false }];
    browser.select(0);
  });
  expect(f.root.querySelector("h2")?.textContent?.trim()).toBe("Delete prefix");
  expect(f.root.textContent).toMatch(/Everything under\s*media\/files\//);
  f.preview();
  expect(vi.mocked(f.api.previewDelete).mock.calls[0][0]).toMatchObject({ mode: "prefix", prefix: "files/" });
  await f.resolve();
  const confirm = f.root.querySelector<HTMLInputElement>(".confirm input")!;
  input(confirm, "media");
  expect(f.startButton().disabled).toBe(true);
  input(confirm, "media/files/");
  expect(f.startButton().disabled).toBe(false);
});

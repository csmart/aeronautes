import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { ListingRowDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import WorkingList from "./WorkingList.svelte";
import TransferDialog from "../transfer/TransferDialog.svelte";
import LocalTransferDialog from "../transfer/LocalTransferDialog.svelte";

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
});

function row(kind: "object" | "prefix", name: string): ListingRowDTO {
  return { kind, name, bytes: 1, etag: "", lastModified: "", contentType: "", sloListed: false };
}

// A folder (or the container's top) holding one subfolder and three objects.
function browserAt(prefix: string) {
  const api = newFakeApi();
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source";
  browser.containerName = "media";
  browser.prefix = prefix;
  browser.entries = [row("prefix", `${prefix}sub/`), row("object", `${prefix}a`), row("object", `${prefix}b`), row("object", `${prefix}c`)];
  return { api, browser };
}

function transferButtons(browser: BrowserState) {
  const root = document.createElement("div");
  document.body.append(root);
  mounted.add(mount(WorkingList, { target: root, props: { browser } }));
  flushSync();
  const find = (word: string) => [...root.querySelectorAll<HTMLButtonElement>(".actions button")]
    .find((b) => b.textContent?.trim().startsWith(word))!;
  return { copy: find("Copy"), download: find("Download") };
}

// Copy and Download say what they act on, and act on it (ADR 0108 decision 1).
it.each([
  { name: "the container's top, nothing selected", prefix: "", pick: [] as number[], label: "container", source: "", members: [] as string[] },
  { name: "a folder, nothing selected", prefix: "files/", pick: [], label: "folder", source: "files/", members: [] },
  { name: "one object selected", prefix: "files/", pick: [1], label: "object", source: "files/", members: ["files/a"] },
  { name: "one folder row selected", prefix: "files/", pick: [0], label: "folder", source: "files/sub/", members: [] },
  { name: "two objects chosen", prefix: "files/", pick: [1, 2], label: "selected", source: "files/", members: ["files/a", "files/b"] },
])("labels and acts on $name", ({ prefix, pick, label, source, members }) => {
  const { browser } = browserAt(prefix);
  if (pick.length > 0) browser.select(pick[0]);
  for (const index of pick.slice(1)) browser.toggleRow(index);
  const buttons = transferButtons(browser);
  expect(buttons.copy.textContent?.trim()).toBe(`Copy ${label}…`);
  expect(buttons.download.textContent?.trim()).toBe(`Download ${label}…`);
  expect(buttons.copy.disabled).toBe(false);
  expect(browser.transferSource.prefix).toBe(source);
  expect(browser.transferSource.members).toEqual(members);
});

// Folder rows in a selection are not members. What the window says about the
// folders left out, before either dialog opens, is in
// selectionFolderWarning.test.ts.
it("leaves folder rows out of a selection's members", () => {
  const { browser } = browserAt("files/");
  browser.select(0);
  browser.toggleRow(1);
  expect(browser.transferSource).toMatchObject({ kind: "selected", members: ["files/a"], excludedFolders: 1 });
});

// The copy dialog names the selected objects and sends exactly them.
it("copies the chosen objects and says which", async () => {
  const { api, browser } = browserAt("files/");
  browser.select(1);
  browser.toggleRow(2);
  api.planCopy = vi.fn(api.planCopy);
  const root = document.createElement("div");
  document.body.append(root);
  mounted.add(mount(TransferDialog, { target: root, props: { browser, onClose: vi.fn() } }));
  flushSync();
  expect(root.querySelector('[aria-label="Source scope"]')?.textContent).toContain("2 selected objects: a, b");
  const container = root.querySelector<HTMLInputElement>('input[placeholder="container"]')!;
  container.value = "backup";
  container.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
  [...root.querySelectorAll<HTMLButtonElement>("footer button")].find((b) => b.textContent?.trim() === "Preview")!.click();
  flushSync();
  expect(vi.mocked(api.planCopy).mock.calls[0][0]).toMatchObject({ sourcePrefix: "files/", members: ["files/a", "files/b"], destContainer: "backup" });
});

// A preview that found fewer of the selected objects than were chosen says
// how many are gone, and the copy can still start: it copies the rest and
// ends partial (ADR 0108 decision 3).
it("says in the preview how many selected objects are gone", async () => {
  const { api, browser } = browserAt("files/");
  browser.select(1);
  browser.toggleRow(2);
  const plan = api.planCopy;
  api.planCopy = vi.fn(async (req) => ({ ...(await plan(req)), missingMembers: 1 }));
  const root = document.createElement("div");
  document.body.append(root);
  mounted.add(mount(TransferDialog, { target: root, props: { browser, onClose: vi.fn() } }));
  flushSync();
  const container = root.querySelector<HTMLInputElement>('input[placeholder="container"]')!;
  container.value = "backup";
  container.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
  const button = (label: string) => [...root.querySelectorAll<HTMLButtonElement>("footer button")].find((b) => b.textContent?.trim() === label)!;
  button("Preview").click();
  for (let i = 0; i < 8; i++) { await Promise.resolve(); flushSync(); }
  const notes = () => root.querySelector(".preview .notes")?.textContent?.replace(/\s+/g, " ");
  expect(notes()).toContain("1 of the selected objects was no longer at the source; the others are copied");
  expect(button("Start copy").disabled).toBe(false);

  // With every selected object gone, nothing is copied, and the note says so.
  api.planCopy = vi.fn(async (req) => ({ ...(await plan(req)), missingMembers: 2 }));
  button("Preview").click();
  for (let i = 0; i < 8; i++) { await Promise.resolve(); flushSync(); }
  expect(notes()).toContain("2 of the selected objects were no longer at the source; nothing is copied");
  expect(button("Start copy").disabled).toBe(false);
});

// A download follows the selection the same way; an upload, with the same
// rows chosen, still goes into the folder being browsed and names no members.
it.each(["download", "upload"] as const)("sends the chosen objects only for a download (%s)", async (mode) => {
  const { api, browser } = browserAt("files/");
  browser.select(1);
  browser.toggleRow(2);
  browser.localTransfer = mode;
  api.pickDownloadDir = vi.fn().mockResolvedValue({ token: "dir", dir: "chosen" });
  api.pickUploadFiles = vi.fn().mockResolvedValue({ token: "files", files: ["x"], folder: "", skipped: 0, skippedSample: [], emptyDirectories: 0 });
  api.preflightDownload = vi.fn().mockResolvedValue({ itemSamples: [], problemSamples: [], totalBytes: 0, objects: 2, problemCount: 0, rejected: false, replaceable: 0, replacing: 0 });
  api.preflightUpload = vi.fn().mockResolvedValue({ items: [], problems: [], totalBytes: 0, largeObjects: "", largeObjectsOrigin: "" });
  const root = document.createElement("div");
  document.body.append(root);
  mounted.add(mount(LocalTransferDialog, { target: root, props: { browser, mode, onClose: vi.fn() } }));
  flushSync();
  root.querySelector<HTMLButtonElement>("footer button")!.click();
  for (let i = 0; i < 8; i++) { await Promise.resolve(); flushSync(); }
  if (mode === "download") {
    expect(root.querySelector("h2")?.textContent).toContain("Download 2 selected objects from media/files/");
    expect(vi.mocked(api.preflightDownload).mock.calls[0][0]).toMatchObject({ prefix: "files/", members: ["files/a", "files/b"] });
  } else {
    expect(vi.mocked(api.preflightUpload).mock.calls[0][0]).toMatchObject({ prefix: "files/" });
    expect((vi.mocked(api.preflightUpload).mock.calls[0][0] as { members?: string[] }).members).toBeUndefined();
  }
});

// One folder row selected downloads that folder as its own scope.
it("downloads a selected folder as its own scope", async () => {
  const { api, browser } = browserAt("files/");
  browser.select(0);
  browser.localTransfer = "download";
  api.pickDownloadDir = vi.fn().mockResolvedValue({ token: "dir", dir: "chosen" });
  api.preflightDownload = vi.fn().mockResolvedValue({ itemSamples: [], problemSamples: [], totalBytes: 0, objects: 2, problemCount: 0, rejected: false, replaceable: 0, replacing: 0 });
  const root = document.createElement("div");
  document.body.append(root);
  mounted.add(mount(LocalTransferDialog, { target: root, props: { browser, mode: "download", onClose: vi.fn() } }));
  flushSync();
  root.querySelector<HTMLButtonElement>("footer button")!.click();
  for (let i = 0; i < 8; i++) { await Promise.resolve(); flushSync(); }
  const request = vi.mocked(api.preflightDownload).mock.calls[0][0];
  expect(request.prefix).toBe("files/sub/");
  expect(request.members).toBeUndefined();
});

// A selection changed to folder rows alone while a dialog is open names no
// objects, and an empty member list would mean the whole folder, so neither
// dialog can preview or check it (ADR 0108 decision 1).
it("refuses a selection of folders alone in an open dialog", () => {
  const { browser } = browserAt("files/");
  browser.entries = [row("prefix", "files/sub/"), row("prefix", "files/other/"), row("object", "files/a"), row("object", "files/b")];
  browser.select(2);
  browser.toggleRow(3);
  const copyRoot = document.createElement("div");
  const downloadRoot = document.createElement("div");
  document.body.append(copyRoot, downloadRoot);
  mounted.add(mount(TransferDialog, { target: copyRoot, props: { browser, onClose: vi.fn() } }));
  mounted.add(mount(LocalTransferDialog, { target: downloadRoot, props: { browser, mode: "download", onClose: vi.fn() } }));
  flushSync();
  const container = copyRoot.querySelector<HTMLInputElement>('input[placeholder="container"]')!;
  container.value = "backup";
  container.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
  const preview = () => [...copyRoot.querySelectorAll<HTMLButtonElement>("footer button")].find((b) => b.textContent?.trim() === "Preview")!;
  const choose = () => downloadRoot.querySelector<HTMLButtonElement>("footer button")!;
  expect(preview().disabled).toBe(false);
  expect(choose().disabled).toBe(false);
  browser.select(0);
  browser.toggleRow(1);
  flushSync();
  expect(preview().disabled).toBe(true);
  expect(choose().disabled).toBe(true);
});

// The members are part of the request, so changing the selection after a
// preview disarms it, as any change of scope does.
it("disarms a copy preview when the selection changes", async () => {
  const { browser } = browserAt("files/");
  browser.select(1);
  browser.toggleRow(2);
  const root = document.createElement("div");
  document.body.append(root);
  mounted.add(mount(TransferDialog, { target: root, props: { browser, onClose: vi.fn() } }));
  flushSync();
  const container = root.querySelector<HTMLInputElement>('input[placeholder="container"]')!;
  container.value = "backup";
  container.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
  const button = (label: string) => [...root.querySelectorAll<HTMLButtonElement>("footer button")].find((b) => b.textContent?.trim() === label)!;
  button("Preview").click();
  for (let i = 0; i < 8; i++) { await Promise.resolve(); flushSync(); }
  expect(button("Start copy").disabled).toBe(false);
  browser.toggleRow(3);
  flushSync();
  expect(button("Start copy").disabled).toBe(true);
});

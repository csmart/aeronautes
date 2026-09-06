import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import TransferDialog from "./TransferDialog.svelte";

let dialog: ReturnType<typeof mount> | null = null;

afterEach(async () => {
  if (dialog) await unmount(dialog);
  dialog = null;
  document.body.innerHTML = "";
});

function button(label: string): HTMLButtonElement | undefined {
  return [...document.querySelectorAll("button")].find((b) => b.textContent?.trim() === label);
}

function show() {
  const api = newFakeApi();
  api.startCopy = vi.fn().mockResolvedValue("copy-job");
  api.startMirror = vi.fn().mockResolvedValue("unsafe-mirror-job");
  api.planCopy = vi.fn(api.planCopy);
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source";
  browser.containerName = "media";
  dialog = mount(TransferDialog, { target: document.body, props: { browser, onClose: vi.fn() } });
  flushSync();
  const container = document.querySelector<HTMLInputElement>('input[placeholder="container"]')!;
  container.value = "backup";
  container.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
  return api;
}

it("keeps Mirror comparison available without an execution or confirmation flow", async () => {
  const api = show();
  button("Mirror")!.click();
  flushSync();
  expect(document.body.textContent).toContain("Mirror is unavailable");
  expect(document.body.textContent).toContain("concurrent changes");
  button("Preview")!.click();
  await vi.waitFor(() => expect(document.querySelector(".preview")).not.toBeNull());
  expect(api.planCopy).toHaveBeenCalledOnce();
  expect(document.body.textContent).toContain("destination-only objects");
  expect(document.body.textContent).not.toContain("to arm the mirror");
  expect(document.body.textContent).not.toContain("will be overwritten");
  expect(document.querySelector(".mirror-confirm")).toBeNull();
  expect(button("Start mirror")).toBeUndefined();
  const unavailable = button("Mirror unavailable")!;
  expect(unavailable.disabled).toBe(true);
  unavailable.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  flushSync();
  expect(api.startMirror).not.toHaveBeenCalled();
  expect(api.startCopy).not.toHaveBeenCalled();
});

it("cannot turn a Copy preview into a Mirror start and still allows Copy", async () => {
  const api = show();
  button("Preview")!.click();
  await vi.waitFor(() => expect(button("Start copy")?.disabled).toBe(false));
  button("Mirror")!.click();
  flushSync();
  expect(button("Start mirror")).toBeUndefined();
  expect(button("Mirror unavailable")?.disabled).toBe(true);
  button("Copy")!.click();
  flushSync();
  button("Start copy")!.click();
  await vi.waitFor(() => expect(api.startCopy).toHaveBeenCalledOnce());
  expect(api.startMirror).not.toHaveBeenCalled();
});

it("makes the fixture API refuse Mirror without manufacturing a job", async () => {
  const api = newFakeApi();
  const before = await api.jobs("", false, 50);
  await expect(api.startMirror({
    sourceBookmarkId: "a", sourceContainer: "src", sourcePrefix: "",
    destBookmarkId: "b", destContainer: "dst", destPrefix: "", confirm: "dst", profile: "",
    settings: { chunkSize: "", transfers: 0, checkers: 0, tpsLimit: 0, bwLimit: "" },
  })).rejects.toThrow("Mirror is unavailable");
  expect(await api.jobs("", false, 50)).toEqual(before);
});

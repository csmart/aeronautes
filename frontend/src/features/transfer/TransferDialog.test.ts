import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import TransferDialog from "./TransferDialog.svelte";
import { v1Offers } from "../../lib/v1.svelte";

// These cases exercise the Mirror comparison the release does not offer
// (v1Offers.mirrorPreview), so they turn it on for themselves.
beforeEach(() => { v1Offers.mirrorPreview = true; });
afterEach(() => { v1Offers.mirrorPreview = false; });

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

it("distinguishes refused overwrites from uncertain comparison candidates", async () => {
  show();
  expect(document.body.textContent).toMatch(/refused unless you\s+choose to replace/);
  button("Preview")!.click();
  await vi.waitFor(() => expect(document.querySelector(".preview")).not.toBeNull());
  const text = document.body.textContent!;
  expect(text).toMatch(/1\s+overwrite refused/);
  expect(text).toMatch(/1 uncertain object\s+will be checked/);
  expect(text).toContain("unequal existing destinations are refused");
  expect(text).toContain("listed bytes in comparison");
  expect(text).toContain("listed bytes in refused changed objects");
  expect(text).not.toContain("will be overwritten");
  expect(text).not.toContain("to transfer");
});

it("lets the operator acknowledge replacing the existing destinations", async () => {
  const api = show();
  button("Preview")!.click();
  await vi.waitFor(() => expect(document.querySelector(".preview")).not.toBeNull());
  // Refusal is the default: the acknowledged wording is not shown yet.
  expect(document.body.textContent).toMatch(/1\s+overwrite refused/);
  expect(document.body.textContent).not.toContain("will REPLACE");
  // Opt into replacing. The flag only records the request; the authorising
  // question is raised later, by the backend, on a native dialog.
  button("Replace existing objects…")!.click();
  flushSync();
  expect(document.body.textContent).toContain("will REPLACE");
  expect(document.body.textContent).toMatch(/1\s+to replace/);
  expect(document.body.textContent).toContain("unequal existing destinations are replaced once you confirm");
  expect(document.body.textContent).toContain("changed objects to replace");
  // The affordance is one-way until a fresh preview; it does not linger.
  expect(button("Replace existing objects…")).toBeUndefined();
  button("Start copy")!.click();
  await vi.waitFor(() => expect(api.startCopy).toHaveBeenCalledOnce());
  expect(vi.mocked(api.startCopy).mock.calls[0][0].acknowledgeOverwrite).toBe(true);
});

it("drops a pending overwrite acknowledgement when the scope changes", async () => {
  const api = show();
  button("Preview")!.click();
  await vi.waitFor(() => expect(document.querySelector(".preview")).not.toBeNull());
  button("Replace existing objects…")!.click();
  flushSync();
  expect(document.body.textContent).toContain("will REPLACE");
  // Editing the destination retires the acknowledged preview rather than
  // carrying the answer across to a scope the operator never saw.
  const container = document.querySelector<HTMLInputElement>('input[placeholder="container"]')!;
  container.value = "elsewhere";
  container.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
  expect(document.querySelector(".preview")).toBeNull();
  expect(button("Start copy")?.disabled).toBe(true);
  // A fresh preview starts unacknowledged again.
  button("Preview")!.click();
  await vi.waitFor(() => expect(document.querySelector(".preview")).not.toBeNull());
  expect(document.body.textContent).not.toContain("will REPLACE");
  button("Start copy")!.click();
  await vi.waitFor(() => expect(api.startCopy).toHaveBeenCalledOnce());
  expect(vi.mocked(api.startCopy).mock.calls[0][0].acknowledgeOverwrite).toBe(false);
});

it("keeps overwrite refusal visible for a versioned destination", async () => {
  show();
  const container = document.querySelector<HTMLInputElement>('input[placeholder="container"]')!;
  container.value = "versioned";
  container.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
  button("Preview")!.click();
  await vi.waitFor(() => expect(document.querySelector(".preview")).not.toBeNull());
  expect(document.body.textContent).toContain("refused unless you choose to replace them");
  expect(document.body.textContent).not.toContain("accumulate versions");
});

it("states versioning as an observation, not a retention promise, when replacing", async () => {
  show();
  const container = document.querySelector<HTMLInputElement>('input[placeholder="container"]')!;
  container.value = "versioned";
  container.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
  button("Preview")!.click();
  await vi.waitFor(() => expect(document.querySelector(".preview")).not.toBeNull());
  button("Replace existing objects…")!.click();
  flushSync();
  const text = document.body.textContent!;
  expect(text).toContain("reported versioning");
  expect(text).toContain("is not verified here");
  // ADR 0081 decision 8: the preview must not promise the prior version survives.
  expect(text).not.toContain("keeps its prior version");
});

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

it("cannot turn a Copy preview into a Mirror start and allows a fresh Copy preview", async () => {
  const api = show();
  button("Preview")!.click();
  await vi.waitFor(() => expect(button("Start copy")?.disabled).toBe(false));
  button("Mirror")!.click();
  flushSync();
  expect(button("Start mirror")).toBeUndefined();
  expect(button("Mirror unavailable")?.disabled).toBe(true);
  button("Copy")!.click();
  flushSync();
  expect(button("Start copy")?.disabled).toBe(true);
  button("Preview")!.click();
  await vi.waitFor(() => expect(button("Start copy")?.disabled).toBe(false));
  button("Start copy")!.click();
  await vi.waitFor(() => expect(api.startCopy).toHaveBeenCalledOnce());
  expect(api.startMirror).not.toHaveBeenCalled();
});

// Mirror refuses every start, so by the operator's decision of 11 November
// 2026 the release's copy dialog offers no Mirror tab; its title says Copy.
it("offers no Mirror tab while the release does not offer Mirror", () => {
  v1Offers.mirrorPreview = false;
  const browser = new BrowserState(newFakeApi());
  const root = document.createElement("div");
  document.body.append(root);
  dialog = mount(TransferDialog, { target: root, props: { browser, onClose: () => {} } });
  flushSync();
  expect(root.querySelector('[role="tablist"]')).toBeNull();
  expect(root.querySelector("h2")?.textContent).toBe("Copy");
});

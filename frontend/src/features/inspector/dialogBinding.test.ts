import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { MutationResultDTO } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import Inspector from "./Inspector.svelte";
import ObjectEditPanel from "./ObjectEditPanel.svelte";
import ContainerEditPanel from "./ContainerEditPanel.svelte";

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
});
async function settle() {
  for (let i = 0; i < 12; i++) { await Promise.resolve(); flushSync(); }
}
function button(root: HTMLElement, label: string) {
  const found = [...root.querySelectorAll("button")].find((b) => b.textContent?.trim() === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}
async function show(kind: "object" | "container") {
  const api = newFakeApi();
  const browser = new BrowserState(api);
  browser.connectedBookmarkId = "source";
  browser.account = { name: "AUTH_source", containers: 1, objects: 1, bytesUsed: 0,
    quotaBytes: 0, meta: {}, tempUrlKeySlots: [], policyUsage: [], rawHeaders: {}, scope: { bookmarkId: "source", generation: 1 }, editableFingerprint: "" };
  await browser.loadContainers();
  await browser.openContainer("fixtures");
  if (kind === "object") browser.select(browser.entries.findIndex((e) => e.name === "empty-object"));
  await settle();
  const root = document.createElement("div"); document.body.append(root);
  mounted.add(mount(Inspector, { target: root, props: { browser } })); flushSync();
  return { api, browser, root };
}

it.each(["object", "container"] as const)("an obsolete %s save cannot close a newer editor", async (kind) => {
  const f = await show(kind);
  const old = deferred<MutationResultDTO>();
  const object = f.browser.objectDetail;
  const container = f.browser.containerDetail;
  const label = kind === "object" ? "Edit metadata" : "Configure container";
  f.api.updateObjectMetadata = vi.fn(() => old.promise);
  f.api.updateContainer = vi.fn(() => old.promise);
  button(f.root, label).click(); flushSync();
  button(f.root, "Save").click(); await settle();
  if (kind === "object") f.browser.select(f.browser.entries.findIndex((e) => e.name === "external-dlo"));
  else await f.browser.openContainer("photos_segments");
  await settle();
  button(f.root, label).click(); flushSync();
  expect([...f.root.querySelectorAll(".card-title")].some((title) => title.textContent?.trim() === label)).toBe(true);
  old.resolve({ outcome: "applied", objectAfter: object, containerAfter: container, accountAfter: null, mismatch: "", limitation: "", transIds: [] });
  await settle();
  expect([...f.root.querySelectorAll("button")].some((b) => b.textContent?.trim() === "Save")).toBe(true);
});

it("a refreshed container portrait cannot silently replace the editor baseline", async () => {
  const f = await show("container");
  const old = f.browser.containerDetail!;
  f.api.updateContainer = vi.fn().mockResolvedValue({ outcome: "conflict", objectAfter: null, containerAfter: null, mismatch: "", transIds: [] });
  button(f.root, "Configure container").click(); flushSync();
  button(f.root, "Save").click(); await settle();
  const refresh = deferred<typeof old>();
  f.api.containerDetail = vi.fn(() => refresh.promise);
  button(f.root, "Reload theirs").click(); flushSync();
  button(f.root, "Configure container").click(); flushSync();
  refresh.resolve({ ...old, meta: { Preserve: "new metadata" }, editableFingerprint: "new-fingerprint" });
  await settle();
  vi.mocked(f.api.updateContainer).mockClear();
  const save = [...f.root.querySelectorAll("button")].find((b) => b.textContent?.trim() === "Save");
  save?.dispatchEvent(new MouseEvent("click", { bubbles: true })); await settle();
  expect(f.api.updateContainer).not.toHaveBeenCalled();
});

type Kind = "object" | "container";
async function showEditor(kind: Kind) {
  const f = await show(kind);
  const root = document.createElement("div"); document.body.append(root);
  const done = vi.fn();
  const component = kind === "object"
    ? mount(ObjectEditPanel, { target: root, props: { browser: f.browser, detail: f.browser.objectDetail!, onDone: done } })
    : mount(ContainerEditPanel, { target: root, props: { browser: f.browser, detail: f.browser.containerDetail!, onDone: done } });
  mounted.add(component); flushSync();
  const replies: ReturnType<typeof deferred<MutationResultDTO>>[] = [];
  const mutate = vi.fn((..._args: unknown[]) => { const reply = deferred<MutationResultDTO>(); replies.push(reply); return reply.promise; });
  f.api.updateObjectMetadata = mutate; f.api.updateContainer = mutate;
  const result = (outcome: string): MutationResultDTO => ({
    outcome, mismatch: outcome === "verify_mismatch" ? "current mismatch" : "", limitation: "", transIds: [],
    objectAfter: outcome === "applied" ? f.browser.objectDetail : null,
    containerAfter: outcome === "applied" ? f.browser.containerDetail : null,
    accountAfter: null,
  });
  const click = (label: string) => { button(root, label).dispatchEvent(new MouseEvent("click", { bubbles: true })); flushSync(); };
  return { ...f, root, done, component, replies, mutate, result, click };
}

describe.each(["object", "container"] as const)("%s edit lifetime", (kind) => {
  it.each(["applied", "conflict", "verify_mismatch", "failure"])("handles a current %s reply", async (outcome) => {
    const f = await showEditor(kind);
    f.click("Save");
    expect(f.mutate).toHaveBeenCalledOnce();
    expect(f.root.querySelector<HTMLFieldSetElement>("fieldset")?.disabled).toBe(true);
    if (outcome === "failure") f.replies[0].reject(new Error("current failure"));
    else f.replies[0].resolve(f.result(outcome));
    await settle();
    if (outcome === "applied") expect(f.done).toHaveBeenCalledOnce();
    else {
      expect(f.done).not.toHaveBeenCalled();
      expect(button(f.root, "Save").disabled).toBe(false);
      expect(f.root.querySelector<HTMLFieldSetElement>("fieldset")?.disabled).toBe(false);
      if (outcome === "conflict") expect(button(f.root, "Apply mine anyway")).toBeDefined();
      else expect(f.root.textContent).toContain(outcome === "failure" ? "current failure" : "current mismatch");
    }
  });

  it("admits one Save and one explicit forced retry despite duplicate events", async () => {
    const f = await showEditor(kind);
    const save = button(f.root, "Save");
    save.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    save.dispatchEvent(new MouseEvent("click", { bubbles: true })); flushSync();
    expect(f.mutate).toHaveBeenCalledOnce();
    f.replies[0].resolve(f.result("conflict")); await settle();
    const force = button(f.root, "Apply mine anyway");
    force.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    force.dispatchEvent(new MouseEvent("click", { bubbles: true })); flushSync();
    expect(f.mutate).toHaveBeenCalledTimes(2);
    expect(f.mutate.mock.calls[1][kind === "object" ? 3 : 2]).toBe("");
    f.replies[1].resolve(f.result("applied")); await settle();
    expect(f.done).toHaveBeenCalledOnce();
  });

  it.each(["applied", "conflict", "verify_mismatch", "failure"])("close immediately retires a pending %s reply", async (outcome) => {
    const f = await showEditor(kind);
    f.click("Save");
    // Keep the component mounted to exercise the gap before the parent unmounts it.
    f.click("Cancel");
    expect(f.done).toHaveBeenCalledOnce();
    if (outcome === "failure") f.replies[0].reject(new Error("obsolete failure"));
    else f.replies[0].resolve(f.result(outcome));
    await settle();
    expect(f.done).toHaveBeenCalledOnce();
    expect(f.root.textContent).not.toContain("obsolete failure");
    expect(f.root.textContent).not.toContain("current mismatch");
    expect(f.root.querySelector(".conflict")).toBeNull();
    expect([...f.root.querySelectorAll("button")].find((b) => b.classList.contains("primary"))?.disabled).toBe(true);
  });

  it.each(["applied", "failure"])("unmount retires a pending %s callback", async (outcome) => {
    const f = await showEditor(kind);
    f.click("Save");
    await unmount(f.component); mounted.delete(f.component);
    if (outcome === "failure") f.replies[0].reject(new Error("obsolete failure"));
    else f.replies[0].resolve(f.result(outcome));
    await settle(); expect(f.done).not.toHaveBeenCalled();
  });

  it.each(["applied", "conflict", "verify_mismatch", "failure"])("a scope change retires a mounted editor's pending %s callback", async (outcome) => {
    const f = await showEditor(kind); const result = f.result(outcome);
    f.click("Save"); f.browser.connectedBookmarkId = "other"; flushSync();
    if (outcome === "failure") f.replies[0].reject(new Error("obsolete failure"));
    else f.replies[0].resolve(result);
    await settle();
    expect(f.done).not.toHaveBeenCalled(); expect(f.root.textContent).not.toContain("obsolete failure");
    expect(f.root.textContent).not.toContain("current mismatch"); expect(f.root.querySelector(".conflict")).toBeNull();
    expect(f.root.textContent).toContain("Saving…");
  });

  it.each(["bookmark", "bookmark return", "prefix", "reconnect"])("does not admit after %s changes the editor intent", async (change) => {
    const f = await showEditor(kind);
    if (change.startsWith("bookmark")) {
      f.browser.connectedBookmarkId = "other";
      if (change.endsWith("return")) f.browser.connectedBookmarkId = "source";
    } else if (change === "prefix") f.browser.prefix = "another/";
    else {
      f.api.connect = vi.fn().mockRejectedValue(new Error("failed reconnect"));
      await expect(f.browser.connect("source")).rejects.toThrow("failed reconnect");
    }
    f.click("Save"); await settle();
    expect(f.mutate).not.toHaveBeenCalled();
    expect(button(f.root, "Save").disabled).toBe(true);
  });

  it("refuses a forced retry after the displayed baseline changes", async () => {
    const f = await showEditor(kind);
    f.click("Save"); f.replies[0].resolve(f.result("conflict")); await settle();
    if (kind === "object") f.browser.objectDetail = { ...f.browser.objectDetail!, editableFingerprint: "new" };
    else f.browser.containerDetail = { ...f.browser.containerDetail!, editableFingerprint: "new" };
    f.click("Apply mine anyway"); await settle();
    expect(f.mutate).toHaveBeenCalledOnce();
  });
});

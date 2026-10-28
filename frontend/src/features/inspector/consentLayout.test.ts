// UI-13 consent layout: content, interaction and arrangement contracts only.
// This environment reports no layout geometry, so containment and control
// visibility are proved by the native replay in
// docs/experiments/2026-09-10-consent-layout.md, never here.
import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { SegmentOwnershipDTO } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import ManifestDeleteDialog from "./ManifestDeleteDialog.svelte";
import { v1Offers } from "../../lib/v1.svelte";

const shipped = { ...v1Offers };

const LONG_TARGET =
  "backing/long-prefix/" + "p".repeat(520) + "/segments/";
const LONG_ERROR =
  "the provider refused the request: " + "e".repeat(600) + " (transaction 0123456789abcdef)";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  Object.assign(v1Offers, shipped);
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
function click(element: HTMLElement) {
  element.dispatchEvent(new MouseEvent("click", { bubbles: true })); flushSync();
}
function type(element: HTMLInputElement, value: string) {
  element.value = value; element.dispatchEvent(new Event("input", { bubbles: true })); flushSync();
}
const ownership: SegmentOwnershipDTO =
  { class: "dlo", target: LONG_TARGET, proven: false, segments: 3, inlineEntries: 0, reviewed: { class: "dlo", fingerprint: "dlo:0:etag", length: 0, etag: "", namespace: LONG_TARGET } };

// The dialog opens through the browser state (ADR 0094 decision 5), and the
// ownership check behind the with-segments option is asked only once that
// option is taken, so a case that needs it takes it here.
async function dialog(segments = true) {
  v1Offers.manifestDelete = true; v1Offers.bulkDelete = true;
  const api = newFakeApi();
  const browser = new BrowserState(api);
  const bookmark = await browser.saveBookmark({ id: "", displayName: "Mock", authUrl: "http://mock/auth/v1.0", username: "test:tester", storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "testing");
  await browser.connect(bookmark.id); await browser.openContainer("fixtures");
  browser.select(browser.entries.findIndex((row) => row.name === "external-dlo"));
  await settle();
  const root = document.createElement("div"); document.body.append(root);
  const check = deferred<SegmentOwnershipDTO>();
  api.checkSegmentOwnership = vi.fn(() => check.promise);
  const done = vi.fn();
  expect(browser.openManifestDelete()).toBe(true);
  const component = mount(ManifestDeleteDialog, { target: root, props: { browser, onClose: done } });
  mounted.add(component); flushSync(); await settle();
  if (segments) {
    click(root.querySelector<HTMLInputElement>('input[type="checkbox"]')!); await settle();
    check.resolve(ownership); await settle();
  }
  return { api, browser, root, done, check };
}
const consentLabel = (root: HTMLElement) => root.querySelector<HTMLElement>(".segments-opt span")!;
const body = (root: HTMLElement) => root.querySelector<HTMLElement>(".body")!;

describe("manifest consent content", () => {
  it("renders the complete literal backing target in the consent label and the survival text", async () => {
    const f = await dialog(false);
    // The survival text names where the data stays, from the manifest shown.
    expect(f.root.querySelector(".keeps")!.textContent).toContain(f.browser.objectDetail!.manifest);
    click(f.root.querySelector<HTMLInputElement>('input[type="checkbox"]')!); await settle();
    f.check.resolve(ownership); await settle();
    expect(consentLabel(f.root).textContent).toContain(LONG_TARGET);
    // No truncation, ellipsis or collapsing of the literal target.
    expect(consentLabel(f.root).textContent).not.toContain("…");
  });

  it("keeps the complete refusal message when an operation is refused", async () => {
    const f = await dialog(false);
    f.api.deleteManifest = vi.fn().mockRejectedValue(new Error(LONG_ERROR));
    click(button(f.root, "Delete manifest only"));
    await settle();
    const error = f.root.querySelector<HTMLElement>(".error")!;
    expect(error.textContent).toBe(LONG_ERROR);
    expect(error.getAttribute("role")).toBe("alert");
    expect(f.done).not.toHaveBeenCalled();
  });
});

describe("manifest consent interaction", () => {
  it("toggles the force disclosure and arms the action only on an exact confirmation", async () => {
    const f = await dialog(false);
    expect(f.root.querySelector(".force-confirm")).toBeNull();
    click(f.root.querySelector<HTMLInputElement>('input[type="checkbox"]')!);
    await settle();
    f.check.resolve(ownership); await settle();
    const confirm = f.root.querySelector<HTMLInputElement>(".force-confirm")!;
    expect(f.root.querySelector(".force-warn")).not.toBeNull();
    expect(button(f.root, "Delete manifest + segments").disabled).toBe(true);
    type(confirm, "external-dl");
    expect(button(f.root, "Delete manifest + segments").disabled).toBe(true);
    type(confirm, "external-dlo");
    expect(button(f.root, "Delete manifest + segments").disabled).toBe(false);
  });

  it("closes on Cancel without calling any deletion", async () => {
    const f = await dialog(false);
    f.api.deleteManifest = vi.fn();
    f.api.deleteManifestWithSegments = vi.fn();
    click(button(f.root, "Cancel"));
    await settle();
    expect(f.done).toHaveBeenCalledOnce();
    expect(f.api.deleteManifest).not.toHaveBeenCalled();
    expect(f.api.deleteManifestWithSegments).not.toHaveBeenCalled();
  });
});

describe("manifest consent arrangement", () => {
  // Arrangement is DOM containment, not geometry: it guards which elements the
  // bounded region owns. Whether they are visible is proved natively.
  it("keeps the title and the actions outside the scrolled body", async () => {
    const f = await dialog();
    f.api.deleteManifestWithSegments = vi.fn().mockRejectedValue(new Error(LONG_ERROR));
    type(f.root.querySelector<HTMLInputElement>(".force-confirm")!, "external-dlo");
    click(button(f.root, "Delete manifest + segments"));
    await settle();
    const scrolled = body(f.root);
    for (const selector of [".what", ".segments-opt", ".force-warn", ".force-confirm", ".error"]) {
      expect(scrolled.querySelector(selector), selector).not.toBeNull();
    }
    const title = f.root.querySelector(".title")!;
    const actions = f.root.querySelector(".actions")!;
    expect(scrolled.contains(title)).toBe(false);
    expect(scrolled.contains(actions)).toBe(false);
    expect(actions.contains(button(f.root, "Cancel"))).toBe(true);
  });
});

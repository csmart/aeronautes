import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, ContainerSettingsDTO, MutationResultDTO, ObjectDetailDTO } from "../../lib/api";
import { applyEnabledCapabilities, v1Offers } from "../../lib/v1.svelte";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import Inspector from "../inspector/Inspector.svelte";
import ObjectDeleteDialog from "../inspector/ObjectDeleteDialog.svelte";
import ManifestDeleteDialog from "../inspector/ManifestDeleteDialog.svelte";
import BrowserScreen from "./BrowserScreen.svelte";

// ADR 0094 decision 5: the marker and the manifest deletions are owned by the
// browser state like the object deletion, share its readiness rule, and are
// opened by the Delete key for the row kind they own.
const api = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: api }));

const shipped = { ...v1Offers };
const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of [...mounted].reverse()) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  vi.restoreAllMocks();
  Object.assign(v1Offers, shipped);
});

async function settle() {
  for (let i = 0; i < 16; i++) { await Promise.resolve(); flushSync(); }
}
function labels(root: ParentNode = document): string[] {
  return [...root.querySelectorAll("button")].map((b) => b.textContent?.trim() ?? "");
}
function button(label: string, root: ParentNode = document): HTMLButtonElement {
  const found = [...root.querySelectorAll("button")].find((b) => b.textContent?.trim() === label);
  if (!found) throw new Error(`Missing button: ${label}`);
  return found;
}
function click(element: HTMLElement) { element.click(); flushSync(); }
const applied: MutationResultDTO = { outcome: "applied", mismatch: "", limitation: "", transIds: ["tx", "tx2"], objectAfter: null, containerAfter: null };

async function state(name: string) {
  const fake = newFakeApi();
  const browser = new BrowserState(fake);
  const bookmark = await browser.saveBookmark({ id: "", displayName: "Mock", authUrl: "https://example.test/auth",
    username: "fixture:user", storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
  await browser.connect(bookmark.id);
  await browser.openContainer("fixtures");
  browser.select(browser.entries.findIndex((row) => row.name === name));
  await settle();
  const root = document.createElement("div");
  document.body.append(root);
  return { fake, browser, root, bookmark };
}

async function markerDialog() {
  const f = await state("folder-marker/");
  const done = vi.fn();
  if (!f.browser.openMarkerDelete()) throw new Error("no marker is inspected");
  const detail = f.browser.objectDelete!.detail;
  const component = mount(ObjectDeleteDialog, { target: f.root, props: { browser: f.browser, onClose: done } });
  mounted.add(component);
  flushSync(); await settle();
  return { ...f, done, detail };
}

async function manifestDialog() {
  const f = await state("external-dlo");
  const done = vi.fn();
  if (!f.browser.openManifestDelete()) throw new Error("no manifest is inspected");
  const detail = f.browser.objectDelete!.detail;
  const component = mount(ManifestDeleteDialog, { target: f.root, props: { browser: f.browser, onClose: done } });
  mounted.add(component);
  flushSync(); await settle();
  return { ...f, done, detail };
}

describe("the inspector's marker affordance", () => {
  it("offers the marker delete only while the capability is enabled, and never a one-click deletion", async () => {
    const f = await state("folder-marker/");
    const component = mount(Inspector, { target: f.root, props: { browser: f.browser } });
    mounted.add(component);
    flushSync(); await settle();
    expect(labels(f.root)).not.toContain("Delete marker…");
    expect(labels(f.root)).not.toContain("Delete marker object");

    applyEnabledCapabilities(["Marker and manifest deletion"]);
    flushSync(); await settle();
    expect(labels(f.root)).toContain("Delete marker…");
    expect(labels(f.root)).not.toContain("Delete marker object");
    f.fake.deleteMarker = vi.fn(async () => applied);
    click(button("Delete marker…", f.root)); await settle();
    expect(f.browser.objectDelete?.kind).toBe("marker");
    expect(f.fake.deleteMarker).not.toHaveBeenCalled();
  });
});

describe("the marker delete dialog", () => {
  it("binds the marker's own identity, says only the marker goes, and sends one request", async () => {
    const f = await markerDialog();
    expect(f.root.textContent).toContain("The marker object at folder-marker/ will be deleted, and only it.");
    expect(f.root.textContent).toContain("the objects under it stay");
    f.fake.deleteMarker = vi.fn(async () => applied);
    // The request names the scope the prefix detail was read under and the
    // identity of its own object; the view moves on after the success.
    const scope = f.browser.prefixDetail!.scope;
    click(button("Delete marker", f.root));
    await settle();
    expect(f.fake.deleteMarker).toHaveBeenCalledExactlyOnceWith(scope, "fixtures", "folder-marker/", f.detail.contentFingerprint);
    expect(f.done).toHaveBeenCalledOnce();
  });

  it("cannot confirm until the versioning consequence is known or explicitly unknown", async () => {
    const f = await state("folder-marker/");
    let settleFacts!: (facts: ContainerSettingsDTO | null) => void;
    vi.spyOn(f.browser, "containerFacts").mockReturnValue(new Promise((resolve) => { settleFacts = resolve; }));
    f.fake.deleteMarker = vi.fn(async () => applied);
    expect(f.browser.openMarkerDelete()).toBe(true);
    const component = mount(ObjectDeleteDialog, { target: f.root, props: { browser: f.browser, onClose: vi.fn() } });
    mounted.add(component);
    flushSync(); await settle();
    const confirm = button("Delete marker", f.root);
    expect(confirm.disabled).toBe(true);
    click(confirm); await settle();
    expect(f.fake.deleteMarker).not.toHaveBeenCalled();
    settleFacts(null); await settle();
    expect(f.root.textContent).toContain("could not be read");
    expect(confirm.disabled).toBe(false);
    click(confirm); await settle();
    expect(f.fake.deleteMarker).toHaveBeenCalledOnce();
  });

  it("shows a refused delete with both identities and stays open", async () => {
    const f = await markerDialog();
    f.fake.deleteMarker = vi.fn(async () => {
      throw new Error("fixtures/folder-marker/ is not what you were shown: it now reports a plain of 9 bytes with entity tag ffff, so nothing was deleted (expected identity shown-id, found other-id)");
    });
    click(button("Delete marker", f.root)); await settle();
    expect(f.root.textContent).toContain("expected identity shown-id, found other-id");
    expect(f.done).not.toHaveBeenCalled();
    expect(labels(f.root)).toContain("Delete marker");
  });

  it("closes without a request when the selection moves", async () => {
    const f = await markerDialog();
    f.fake.deleteMarker = vi.fn(async () => applied);
    f.browser.select(f.browser.entries.findIndex((row) => row.name === "external-dlo")); await settle();
    expect(f.done).toHaveBeenCalledOnce();
    expect(f.fake.deleteMarker).not.toHaveBeenCalled();
  });

  it("shows an observed successor instead of a plain success", async () => {
    const f = await markerDialog();
    const successor: ObjectDetailDTO = { ...f.detail, contentLength: 9, etag: "900150983cd24fb0d6963f7d28e17f72", contentFingerprint: "other" };
    f.fake.deleteMarker = vi.fn(async () => ({ ...applied, objectAfter: successor }));
    click(button("Delete marker", f.root)); await settle();
    expect(f.root.textContent).toContain("Something stands at the name now");
    expect(f.root.textContent).toContain("900150983cd24fb0d6963f7d28e17f72");
    expect(labels(f.root)).not.toContain("Delete marker");
    expect(f.done).not.toHaveBeenCalled();
  });
});

describe("the manifest delete dialog", () => {
  it("cannot confirm until the versioning consequence is known or explicitly unknown", async () => {
    const f = await state("external-dlo");
    let settleFacts!: (facts: ContainerSettingsDTO | null) => void;
    vi.spyOn(f.browser, "containerFacts").mockReturnValue(new Promise((resolve) => { settleFacts = resolve; }));
    f.fake.deleteManifest = vi.fn(async () => applied);
    expect(f.browser.openManifestDelete()).toBe(true);
    const component = mount(ManifestDeleteDialog, { target: f.root, props: { browser: f.browser, onClose: vi.fn() } });
    mounted.add(component);
    flushSync(); await settle();
    expect(f.root.textContent).toContain("Checking how this container keeps versions");
    const confirm = button("Delete manifest only", f.root);
    expect(confirm.disabled).toBe(true);
    click(confirm); await settle();
    expect(f.fake.deleteManifest).not.toHaveBeenCalled();
    settleFacts(null); await settle();
    expect(f.root.textContent).toContain("could not be read");
    expect(confirm.disabled).toBe(false);
    click(confirm); await settle();
    expect(f.fake.deleteManifest).toHaveBeenCalledOnce();
  });

  it("never asks the ownership check on the manifest-only path", async () => {
    const f = await manifestDialog();
    f.fake.checkSegmentOwnership = vi.fn();
    f.fake.deleteManifest = vi.fn(async () => applied);
    expect(f.root.querySelector('input[type="checkbox"]')).toBeNull();
    click(button("Delete manifest only", f.root)); await settle();
    expect(f.fake.checkSegmentOwnership).not.toHaveBeenCalled();
    expect(f.fake.deleteManifest).toHaveBeenCalledExactlyOnceWith(f.detail.scope, "fixtures", "external-dlo", f.detail.contentFingerprint);
    expect(f.done).toHaveBeenCalledOnce();
  });

  it("shows an observed successor instead of a plain success", async () => {
    const f = await manifestDialog();
    const successor: ObjectDetailDTO = { ...f.detail, class: "plain", manifest: "", contentLength: 17, etag: "00000000000000000000000000000017", contentFingerprint: "other" };
    f.fake.deleteManifest = vi.fn(async () => ({ ...applied, objectAfter: successor }));
    click(button("Delete manifest only", f.root)); await settle();
    expect(f.root.textContent).toContain("Something stands at the name now");
    expect(f.root.textContent).toContain("plain object");
    expect(labels(f.root)).not.toContain("Delete manifest only");
    expect(f.done).not.toHaveBeenCalled();
  });

  it("closes without a request when the selection moves", async () => {
    const f = await manifestDialog();
    f.fake.deleteManifest = vi.fn(async () => applied);
    f.browser.select(f.browser.entries.findIndex((row) => row.name === "empty-object")); await settle();
    expect(f.done).toHaveBeenCalledOnce();
    expect(f.fake.deleteManifest).not.toHaveBeenCalled();
  });
});

describe("the Delete key", () => {
  async function screen(enabled: string[], name: string) {
    Object.assign(api, newFakeApi());
    const info = api.info;
    api.info = async () => ({ ...(await info()), enabledCapabilities: enabled, unrecognisedCapabilities: [] });
    api.deleteMarker = vi.fn(async () => applied);
    api.deleteManifest = vi.fn(async () => applied);
    api.deleteObject = vi.fn(async () => applied);
    await api.saveBookmark({ id: "", displayName: "Key fixture", authUrl: "https://example.test/auth",
      username: "fixture:user", storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
    const root = document.createElement("div"); document.body.append(root);
    const component = mount(BrowserScreen, { target: root }); mounted.add(component);
    flushSync(); await settle();
    click(document.querySelector<HTMLButtonElement>('[aria-label="Key fixture"]')!); await settle();
    click([...document.querySelectorAll<HTMLButtonElement>(".container-row")]
      .find((b) => b.querySelector(".container-name")?.textContent?.trim() === "fixtures")!);
    await settle();
    const row = [...document.querySelectorAll<HTMLElement>('[role="option"]')]
      .find((el) => el.querySelector(".name")?.textContent?.trim() === name)!;
    row.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })); flushSync(); await settle();
    const listbox = document.querySelector<HTMLElement>('[role="listbox"]')!;
    listbox.dispatchEvent(new KeyboardEvent("keydown", { key: "Delete", bubbles: true, cancelable: true })); flushSync(); await settle();
  }

  it("opens the marker delete for a marker-backed prefix and deletes nothing by itself", async () => {
    await screen(["Marker and manifest deletion"], "folder-marker/");
    expect(document.querySelector('[aria-label="Delete marker"]')).not.toBeNull();
    expect(api.deleteMarker).not.toHaveBeenCalled();
  });

  it("opens the manifest delete for a manifest and deletes nothing by itself", async () => {
    await screen(["Marker and manifest deletion"], "external-dlo");
    expect(document.querySelector('[aria-label="Delete manifest"]')).not.toBeNull();
    expect(api.deleteManifest).not.toHaveBeenCalled();
    expect(api.checkSegmentOwnership === undefined || !(api.checkSegmentOwnership as unknown as { mock?: unknown }).mock).toBe(true);
  });

  it("does nothing for a marker or a manifest without the capability", async () => {
    await screen(["Object deletion"], "folder-marker/");
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
    await screen(["Object deletion"], "external-dlo");
    expect(document.querySelector('[role="alertdialog"]')).toBeNull();
  });
});

describe("links have no deletion flow", () => {
  // ADR 0094 decision 5: a link in either representation is excluded from
  // every affordance, the Delete key included, and the inspector says why.
  it("a link to a marker opens nothing and the inspector explains", async () => {
    const f = await state("folder-marker/");
    applyEnabledCapabilities(["Marker and manifest deletion"]);
    const pd = f.browser.prefixDetail!;
    f.browser.prefixDetail = { ...pd, selfObject: { ...pd.selfObject!, followedSymlink: true } };
    expect(f.browser.deletableSelectionKind()).toBeNull();
    expect(f.browser.openMarkerDelete()).toBe(false);
    expect(f.browser.objectDelete).toBeNull();
    const component = mount(Inspector, { target: f.root, props: { browser: f.browser } });
    mounted.add(component);
    flushSync(); await settle();
    expect(labels(f.root)).not.toContain("Delete marker…");
    expect(f.root.textContent).toContain("symbolic link to a marker");
  });

  it("a followed link to a manifest opens nothing and the inspector explains", async () => {
    const f = await state("external-dlo");
    applyEnabledCapabilities(["Marker and manifest deletion"]);
    const detail = f.browser.objectDetail!;
    f.browser.objectDetail = { ...detail, followedSymlink: true };
    expect(f.browser.deletableSelectionKind()).toBeNull();
    expect(f.browser.openManifestDelete()).toBe(false);
    expect(f.browser.objectDelete).toBeNull();
    const component = mount(Inspector, { target: f.root, props: { browser: f.browser } });
    mounted.add(component);
    flushSync(); await settle();
    expect(labels(f.root)).not.toContain("Delete manifest…");
    expect(f.root.textContent).toContain("symbolic link to the manifest");
  });

  it("the manifest dialog does not promise that the name becomes unreadable", async () => {
    const f = await manifestDialog();
    expect(f.root.textContent).not.toContain("reads of this name will fail");
    expect(f.root.textContent).toContain("follows how this container keeps versions");
  });
});

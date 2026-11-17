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
function arm(root: ParentNode, words = "delete folder-marker/") {
  const input = root.querySelector<HTMLInputElement>(".confirm input");
  if (!input) throw new Error("no confirmation field");
  input.value = words;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}
const applied: MutationResultDTO = { outcome: "applied", mismatch: "", limitation: "", transIds: ["tx", "tx2"], objectAfter: null, containerAfter: null, accountAfter: null };

async function state(name: string) {
  const fake = newFakeApi();
  const browser = new BrowserState(fake);
  const bookmark = await browser.saveBookmark({ id: "", displayName: "Mock", authUrl: "https://example.test/auth",
    username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
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
  it("offers the marker delete through a confirmation, never a one-click deletion", async () => {
    const f = await state("folder-marker/");
    const component = mount(Inspector, { target: f.root, props: { browser: f.browser } });
    mounted.add(component);
    flushSync(); await settle();
    // Marker deletion is a returned capability (ADR 0052 decision 13), so the
    // affordance is offered; it opens the confirmation and never deletes on
    // the click itself.
    expect(labels(f.root)).toContain("Delete marker…");
    expect(labels(f.root)).not.toContain("Delete marker object");
    f.fake.deleteMarker = vi.fn(async () => applied);
    click(button("Delete marker…", f.root)); await settle();
    expect(f.browser.objectDelete?.kind).toBe("marker");
    expect(f.fake.deleteMarker).not.toHaveBeenCalled();
  });
});

describe("the marker delete dialog", () => {
  it("binds the marker's own name, says only the marker goes, and sends one request", async () => {
    const f = await markerDialog();
    expect(f.root.textContent).toContain("The marker object at folder-marker/ will be deleted, and only it.");
    expect(f.root.textContent).toContain("the objects under it stay");
    f.fake.deleteMarker = vi.fn(async () => applied);
    // The request names the scope the prefix detail was read under and the
    // prefix; the view moves on after the success.
    const scope = f.browser.prefixDetail!.scope;
    arm(f.root, "folder-marker/");
    expect(button("Delete marker", f.root).disabled).toBe(true);
    arm(f.root);
    click(button("Delete marker", f.root));
    await settle();
    expect(f.fake.deleteMarker).toHaveBeenCalledExactlyOnceWith(scope, "fixtures", "folder-marker/", "delete folder-marker/");
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
    arm(f.root);
    expect(confirm.disabled).toBe(true);
    click(confirm); await settle();
    expect(f.fake.deleteMarker).not.toHaveBeenCalled();
    settleFacts(null); await settle();
    expect(f.root.textContent).toContain("could not be read");
    expect(confirm.disabled).toBe(false);
    click(confirm); await settle();
    expect(f.fake.deleteMarker).toHaveBeenCalledOnce();
  });

  it("shows the store's refusal and stays open", async () => {
    const f = await markerDialog();
    f.fake.deleteMarker = vi.fn(async () => {
      throw new Error("marker delete refused by the store with HTTP 403; nothing was deleted");
    });
    arm(f.root);
    click(button("Delete marker", f.root)); await settle();
    expect(f.root.textContent).toContain("refused by the store with HTTP 403; nothing was deleted");
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

  // ADR 0120 decision 3: the segments option starts ticked where the
  // container keeps no versions, and asks its ownership check then.
  // Unticked, the deletion is the manifest-only one and no other.
  it("starts with the segments option ticked and deletes the manifest only once it is unticked", async () => {
    const f = await state("external-dlo");
    f.fake.checkSegmentOwnership = vi.fn(f.fake.checkSegmentOwnership);
    f.fake.deleteManifestWithSegments = vi.fn();
    f.fake.deleteManifest = vi.fn(async () => applied);
    const done = vi.fn();
    expect(f.browser.openManifestDelete()).toBe(true);
    const detail = f.browser.objectDelete!.detail;
    mounted.add(mount(ManifestDeleteDialog, { target: f.root, props: { browser: f.browser, onClose: done } }));
    flushSync(); await settle();
    const option = f.root.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
    expect(option.checked).toBe(true);
    expect(f.fake.checkSegmentOwnership).toHaveBeenCalledOnce();
    expect(labels(f.root)).toContain("Delete manifest + segments");
    expect(button("Delete manifest + segments", f.root).disabled).toBe(true);
    click(option); await settle();
    click(button("Delete manifest only", f.root)); await settle();
    expect(f.fake.deleteManifestWithSegments).not.toHaveBeenCalled();
    expect(f.fake.deleteManifest).toHaveBeenCalledExactlyOnceWith(detail.scope, "fixtures", "external-dlo");
    expect(done).toHaveBeenCalledOnce();
  });

  // ADR 0120 decision 4: a kept version still names its segments, so the
  // option is not offered where the container keeps versions, or where that
  // could not be read, and the dialog says which.
  it.each([
    ["keeps history", { versioningMode: "history_location", versioningArchive: "archive" } as ContainerSettingsDTO, "this container keeps versions, and a kept version still uses its segments"],
    ["keeps versions", { versioningMode: "versions_location", versioningArchive: "archive" } as ContainerSettingsDTO, "this container keeps versions, and a kept version still uses its segments"],
    ["cannot be read", null, "whether this container keeps versions could not be read"],
  ])("does not offer the segments option where the container %s", async (_case, facts, reason) => {
    const f = await state("external-dlo");
    vi.spyOn(f.browser, "containerFacts").mockResolvedValue(facts);
    f.fake.checkSegmentOwnership = vi.fn();
    expect(f.browser.openManifestDelete()).toBe(true);
    mounted.add(mount(ManifestDeleteDialog, { target: f.root, props: { browser: f.browser, onClose: vi.fn() } }));
    flushSync(); await settle();
    expect(f.root.querySelector('input[type="checkbox"]')).toBeNull();
    expect(f.root.textContent).toContain(`Its segments cannot be deleted with it here: ${reason}`);
    expect(f.fake.checkSegmentOwnership).not.toHaveBeenCalled();
    expect(button("Delete manifest only", f.root).disabled).toBe(false);
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
      username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
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

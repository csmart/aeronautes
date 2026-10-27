import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, ContainerSettingsDTO, MutationResultDTO, ObjectDetailDTO } from "../../lib/api";
import { applyEnabledCapabilities, v1Offers } from "../../lib/v1.svelte";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import Inspector from "../inspector/Inspector.svelte";
import ObjectDeleteDialog from "../inspector/ObjectDeleteDialog.svelte";
import BrowserScreen from "./BrowserScreen.svelte";

// BrowserScreen reaches the backend through the live seam; the screen-level
// cases point it at a fresh fake per case.
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
const applied: MutationResultDTO = { outcome: "applied", mismatch: "", limitation: "", transIds: ["tx"], objectAfter: null, containerAfter: null };

async function state(name = "empty-object") {
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

async function dialog(name = "empty-object") {
  const f = await state(name);
  const done = vi.fn();
  const detail = f.browser.objectDetail!;
  const component = mount(ObjectDeleteDialog, { target: f.root, props: { browser: f.browser, detail, container: "fixtures", onClose: done } });
  mounted.add(component);
  flushSync();
  await settle();
  return { ...f, done, detail };
}

describe("the inspector's delete affordance", () => {
  it("offers Delete object for a plain object only while the capability is enabled, and never for a manifest", async () => {
    const f = await state();
    const component = mount(Inspector, { target: f.root, props: { browser: f.browser } });
    mounted.add(component);
    flushSync(); await settle();
    expect(labels(f.root)).not.toContain("Delete object…");

    applyEnabledCapabilities(["Object deletion"]);
    flushSync(); await settle();
    expect(labels(f.root)).toContain("Delete object…");

    f.browser.select(f.browser.entries.findIndex((row) => row.name === "external-dlo"));
    await settle();
    expect(labels(f.root)).not.toContain("Delete object…");
  });
});

describe("the object delete dialog", () => {
  it("sends one request with the scope and identity it was opened for, then closes on a plain success", async () => {
    const f = await dialog();
    f.fake.deleteObject = vi.fn(async () => applied);
    click(button("Delete object", f.root));
    await settle();
    expect(f.fake.deleteObject).toHaveBeenCalledExactlyOnceWith(f.detail.scope, "fixtures", "empty-object", f.detail.contentFingerprint);
    expect(f.done).toHaveBeenCalledOnce();
  });

  it("closes without a request when the selection moves", async () => {
    const f = await dialog();
    f.fake.deleteObject = vi.fn(async () => applied);
    f.browser.select(f.browser.entries.findIndex((row) => row.name === "external-dlo"));
    await settle();
    expect(f.done).toHaveBeenCalledOnce();
    expect(f.fake.deleteObject).not.toHaveBeenCalled();
  });

  it("shows a refused delete with both identities and stays open", async () => {
    const f = await dialog();
    f.fake.deleteObject = vi.fn(async () => {
      throw new Error("fixtures/normal/hello.txt is not what you were shown: it now reports a plain of 40 bytes with entity tag ffff, so nothing was deleted (expected identity shown-id, found other-id)");
    });
    click(button("Delete object", f.root));
    await settle();
    expect(f.root.textContent).toContain("expected identity shown-id, found other-id");
    expect(f.done).not.toHaveBeenCalled();
    expect(labels(f.root)).toContain("Delete object");
  });

  it("shows an observed successor instead of a plain success", async () => {
    const f = await dialog();
    const successor: ObjectDetailDTO = { ...f.detail, contentLength: 3, etag: "900150983cd24fb0d6963f7d28e17f72", contentFingerprint: "other" };
    f.fake.deleteObject = vi.fn(async () => ({ ...applied, objectAfter: successor }));
    click(button("Delete object", f.root));
    await settle();
    expect(f.root.textContent).toContain("Something stands at the name now");
    expect(f.root.textContent).toContain("900150983cd24fb0d6963f7d28e17f72");
    expect(f.root.textContent).toContain("archived predecessor or a concurrent write");
    expect(labels(f.root)).not.toContain("Delete object");
    expect(f.done).not.toHaveBeenCalled();
    click(button("Close", f.root));
    expect(f.done).toHaveBeenCalledOnce();
  });

  it("states what the delete does under the container's versioning mode", async () => {
    const f = await state();
    const facts: ContainerSettingsDTO = { publicRead: false, publicListing: false, referrerGrants: [], sharedWrite: false, staticWeb: false,
      versioningMode: "versions_location", versioningArchive: "the-archive", quota: false, cors: false, readAcl: "", writeAcl: "", observedAt: "", generation: 1 };
    vi.spyOn(f.browser, "containerFacts").mockResolvedValue(facts);
    const component = mount(ObjectDeleteDialog, { target: f.root, props: { browser: f.browser, detail: f.browser.objectDetail!, container: "fixtures", onClose: vi.fn() } });
    mounted.add(component);
    flushSync(); await settle();
    expect(f.root.textContent).toContain("keeps versions in the-archive");
    expect(f.root.textContent).toContain("the name reverts to it");
  });
});

describe("the Delete key", () => {
  async function screen(enabled: string[]) {
    Object.assign(api, newFakeApi());
    const info = api.info;
    api.info = async () => ({ ...(await info()), enabledCapabilities: enabled, unrecognisedCapabilities: [] });
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
      .find((el) => el.querySelector(".name")?.textContent?.trim() === "empty-object")!;
    row.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })); flushSync(); await settle();
    const listbox = document.querySelector<HTMLElement>('[role="listbox"]')!;
    listbox.dispatchEvent(new KeyboardEvent("keydown", { key: "Delete", bubbles: true, cancelable: true })); flushSync(); await settle();
  }

  it("opens the delete dialog for a selected plain object and deletes nothing by itself", async () => {
    await screen(["Object deletion"]);
    expect(document.querySelector('[aria-label="Delete object"]')).not.toBeNull();
    expect(api.deleteObject).not.toHaveBeenCalled();
  });

  it("does nothing without the capability", async () => {
    await screen([]);
    expect(document.querySelector('[aria-label="Delete object"]')).toBeNull();
    expect(api.deleteObject).not.toHaveBeenCalled();
  });
});

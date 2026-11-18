import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount, type Component } from "svelte";
import { applyEnabledCapabilities, v1Offers } from "../../lib/v1.svelte";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import WorkingList from "./WorkingList.svelte";
import ObjectDeleteDialog from "../inspector/ObjectDeleteDialog.svelte";
import ManifestDeleteDialog from "../inspector/ManifestDeleteDialog.svelte";
import SelectedDeleteDialog from "../inspector/SelectedDeleteDialog.svelte";
import DeleteDialog from "../transfer/DeleteDialog.svelte";

// ADR 0117: wherever the open container is, or may be, a segment store, the
// listing and every delete confirmation opened there say so, and an
// ordinary container shows nothing.

const named = "This container is named like a segment store.";
const known = "This container is a segment store.";
const consequence = "Objects here may be segments of larger objects, and deleting one can break the object it belongs to.";

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

async function connected(fake = newFakeApi()) {
  const browser = new BrowserState(fake);
  const bookmark = await browser.saveBookmark({ id: "", displayName: "Mock", authUrl: "https://example.test/auth",
    username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
  await browser.connect(bookmark.id);
  const root = document.createElement("div");
  document.body.append(root);
  return { browser, root };
}

// The fixture's main container holds every kind of object the dialogs
// delete, so the store's replies about it carry the class under test.
async function inFixtures(containerClass: string) {
  const fake = newFakeApi();
  const { containers, containerDetail } = fake;
  fake.containers = async (...args) => {
    const page = await containers(...args);
    return { ...page, rows: page.rows.map((row) => (row.name === "fixtures" ? { ...row, class: containerClass } : row)) };
  };
  fake.containerDetail = async (...args) => ({ ...(await containerDetail(...args)), class: containerClass });
  const f = await connected(fake);
  await f.browser.openContainer("fixtures");
  await settle();
  const index = (name: string) => f.browser.entries.findIndex((row) => row.name === name);
  return { ...f, index };
}

function show<Props extends Record<string, unknown>>(component: Component<Props>, root: HTMLElement, props: Props): string {
  mounted.add(mount(component, { target: root, props }));
  flushSync();
  return (root.textContent ?? "").replace(/\s+/g, " ");
}

describe("the listing's caution", () => {
  async function banners(open: (browser: BrowserState) => Promise<void>): Promise<string[]> {
    const f = await connected();
    await open(f.browser);
    await settle();
    show(WorkingList, f.root, { browser: f.browser });
    await settle();
    return [...f.root.querySelectorAll(".mark.warning")].map((b) => (b.getAttribute("aria-label") ?? "").replace(/\s+/g, " ").trim());
  }

  it("names a container that is only named like a segment store", async () => {
    expect(await banners((browser) => browser.openContainer("photos_segments"))).toEqual([`${named} ${consequence}`]);
  });

  it("names a container known to be a segment store", async () => {
    expect(await banners((browser) => browser.openContainer("external-dlo-segments"))).toEqual([`${known} ${consequence}`]);
  });

  it("says nothing in an ordinary container", async () => {
    expect(await banners((browser) => browser.openContainer("fixtures"))).toEqual([]);
  });

  it("leaves the inspector's jump its own notice, which names the manifest", async () => {
    const shown = await banners((browser) => browser.openBackstage("external-dlo-segments", "", "fixtures/external-dlo"));
    expect(shown).toHaveLength(1);
    expect(shown[0]).toContain("segments of fixtures/external-dlo");
  });
});

describe("the delete confirmations' caution", () => {
  const dialogs: [string, (f: Awaited<ReturnType<typeof inFixtures>>) => Promise<string>][] = [
    ["object", async (f) => {
      f.browser.select(f.index("empty-object"));
      await settle();
      if (!f.browser.openObjectDelete()) throw new Error("nothing deletable is inspected");
      return show(ObjectDeleteDialog, f.root, { browser: f.browser, onClose: vi.fn() });
    }],
    ["manifest", async (f) => {
      f.browser.select(f.index("external-dlo"));
      await settle();
      if (!f.browser.openManifestDelete()) throw new Error("no manifest is inspected");
      return show(ManifestDeleteDialog, f.root, { browser: f.browser, onClose: vi.fn() });
    }],
    ["selected set", async (f) => {
      applyEnabledCapabilities(["Object deletion"]);
      f.browser.select(f.index("empty-object"));
      f.browser.toggleRow(f.index("external-slo"));
      await settle();
      if (!f.browser.openSelectedDelete()) throw new Error("no selection to delete");
      return show(SelectedDeleteDialog, f.root, { browser: f.browser, onClose: vi.fn() });
    }],
    ["prefix and container", async (f) => {
      f.browser.deleteOpen = "empty";
      return show(DeleteDialog, f.root, { browser: f.browser, onClose: vi.fn() });
    }],
  ];

  it.each(dialogs)("stands in the %s delete of a container named like a segment store", async (_, open) => {
    expect(await open(await inFixtures("suspected"))).toContain(`${named} ${consequence}`);
  });

  it.each(dialogs)("is absent from the %s delete of an ordinary container", async (_, open) => {
    const text = await open(await inFixtures("normal"));
    expect(text).toMatch(/delet/i);
    expect(text).not.toContain("may be segments of larger objects");
  });
});

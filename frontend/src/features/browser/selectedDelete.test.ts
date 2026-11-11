import { afterEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api, ContainerSettingsDTO, ListingRowDTO } from "../../lib/api";
import { applyEnabledCapabilities, v1Offers } from "../../lib/v1.svelte";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import Inspector from "../inspector/Inspector.svelte";
import WorkingList from "./WorkingList.svelte";
import SelectedDeleteDialog from "../inspector/SelectedDeleteDialog.svelte";
import BrowserScreen from "./BrowserScreen.svelte";

// ADR 0093 decisions 1 and 5: several loaded rows can be selected, the
// inspector summarises them without a read, and the selected-set deletion is
// one dialog around a snapshot, confirmed by the typed count, built from that
// snapshot alone and ended without deleting when the view no longer matches.
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
function type(root: ParentNode, value: string) {
  const input = root.querySelector<HTMLInputElement>(".confirm input");
  if (!input) throw new Error("no confirmation field");
  input.value = value;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}
function names(rows: ListingRowDTO[]): string[] { return rows.map((row) => row.name); }

async function state() {
  const fake = newFakeApi();
  const browser = new BrowserState(fake);
  const bookmark = await browser.saveBookmark({ id: "", displayName: "Mock", authUrl: "https://example.test/auth",
    username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
  await browser.connect(bookmark.id);
  await browser.openContainer("fixtures");
  await settle();
  const root = document.createElement("div");
  document.body.append(root);
  const index = (name: string) => {
    const i = browser.visibleEntries.findIndex((row) => row.name === name);
    if (i < 0) throw new Error(`no row ${name}`);
    return i;
  };
  return { fake, browser, root, bookmark, index };
}

describe("the selection model", () => {
  it("toggles, extends and selects every loaded row, and reads nothing for a summary", async () => {
    const f = await state();
    const detail = vi.spyOn(f.fake, "objectDetail");
    f.browser.select(f.index("empty-object"));
    await settle();
    expect(f.browser.objectDetail?.name).toBe("empty-object");
    expect(detail).toHaveBeenCalledTimes(1);

    f.browser.toggleRow(f.index("external-slo"));
    await settle();
    expect(names(f.browser.chosenRows)).toEqual(["empty-object", "external-slo"]);
    expect(f.browser.selectionCount).toBe(2);
    expect(f.browser.detailIdentity).toBeNull();
    expect(f.browser.objectDetail).toBeNull();
    expect(f.browser.deletableSelectionKind()).toBeNull();
    expect(detail).toHaveBeenCalledTimes(1);

    // Toggling the second row off leaves one row, which is a plain selection again.
    f.browser.toggleRow(f.index("external-slo"));
    await settle();
    expect(f.browser.chosenRows).toEqual([]);
    expect(f.browser.selectionCount).toBe(1);
    expect(f.browser.selectedEntry?.name).toBe("empty-object");
    expect(f.browser.objectDetail?.name).toBe("empty-object");
    expect(detail).toHaveBeenCalledTimes(2);

    // A range runs from the plainly selected row.
    f.browser.extendTo(f.index("fake/"));
    await settle();
    const from = f.index("empty-object");
    const to = f.index("fake/");
    expect(names(f.browser.chosenRows)).toEqual(names(f.browser.visibleEntries.slice(from, to + 1)));
    expect(f.browser.chosenRows.length).toBeGreaterThan(2);

    // Select all is every loaded row and nothing the server still holds.
    f.browser.entriesMarker = "more-on-server";
    f.browser.selectAll();
    await settle();
    expect(f.browser.chosenRows.length).toBe(f.browser.visibleEntries.length);
    expect(f.browser.selectionCount).toBe(f.browser.entries.length);
    expect(detail).toHaveBeenCalledTimes(2);

    f.browser.clearSelection();
    await settle();
    expect(f.browser.selectionCount).toBe(0);
    expect(f.browser.selectedIndex).toBe(-1);
  });

  it("narrows select all to the rows the filter leaves on screen", async () => {
    const f = await state();
    f.browser.filter = "external";
    f.browser.selectAll();
    expect(names(f.browser.chosenRows)).toEqual(["external-dlo", "external-slo"]);
    expect(f.browser.chosenRows.length).toBeLessThan(f.browser.entries.length);
  });

  it("follows identities through a refresh and collapses to the row that is left", async () => {
    const f = await state();
    f.browser.select(f.index("empty-object"));
    f.browser.toggleRow(f.index("external-slo"));
    await settle();
    const real = f.fake.objects;
    f.fake.objects = async (...args) => {
      const page = await real(...args);
      return { ...page, rows: page.rows.filter((row) => row.name !== "external-slo") };
    };
    await f.browser.refreshEntries();
    await settle();
    expect(f.browser.chosenRows).toEqual([]);
    expect(f.browser.selectionCount).toBe(1);
    expect(f.browser.selectedEntry?.name).toBe("empty-object");
    expect(f.browser.objectDetail?.name).toBe("empty-object");
  });
});

describe("the inspector summary", () => {
  it("counts the objects, their listed bytes and the prefix rows from the listing alone", async () => {
    const f = await state();
    const component = mount(Inspector, { target: f.root, props: { browser: f.browser } });
    mounted.add(component);
    f.browser.select(f.index("empty-object"));
    await settle();
    const detail = vi.spyOn(f.fake, "objectDetail");
    f.browser.toggleRow(f.index("external-slo"));
    f.browser.toggleRow(f.index("normal/"));
    await settle();
    const text = (f.root.textContent ?? "").replace(/\s+/g, " ");
    expect(text).toContain("3 rows selected");
    expect(text).toContain("no row is inspected");
    expect(text).toContain("Objects 2");
    expect(text).toContain("Listed bytes 1.02 KB");
    expect(text).toContain("Prefix rows 1");
    expect(text).not.toContain("empty-object");
    expect(detail).not.toHaveBeenCalled();
  });
});

describe("the working list", () => {
  it("toggles with Ctrl, extends with Shift, counts the selection and offers the delete", async () => {
    const f = await state();
    const component = mount(WorkingList, { target: f.root, props: { browser: f.browser } });
    mounted.add(component);
    flushSync(); await settle();
    const row = (name: string) => [...f.root.querySelectorAll<HTMLElement>('[role="option"]')]
      .find((el) => el.querySelector(".name")?.firstChild?.textContent?.trim() === name)!;
    row("empty-object").dispatchEvent(new PointerEvent("pointerdown", { bubbles: true }));
    flushSync(); await settle();
    row("external-dlo").dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, ctrlKey: true }));
    flushSync(); await settle();
    expect(names(f.browser.chosenRows)).toEqual(["empty-object", "external-dlo"]);
    expect(row("external-dlo").getAttribute("aria-selected")).toBe("true");
    expect(row("empty-object").getAttribute("aria-selected")).toBe("true");
    expect(row("fake/").getAttribute("aria-selected")).toBe("false");
    expect(f.root.querySelector('[role="listbox"]')?.getAttribute("aria-multiselectable")).toBe("true");
    expect(f.root.querySelector(".pane-foot")?.textContent).toContain("2 selected");
    // Returned to the release on 29 October 2026 (ADR 0052 decision 11): the
    // selected-set delete is offered by default, with nothing switched on.
    expect(labels(f.root)).toContain("Delete selected…");

    // A Shift click extends from the last toggled row.
    row("fake/").dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, shiftKey: true }));
    flushSync(); await settle();
    expect(names(f.browser.chosenRows)).toEqual(["external-dlo", "external-slo", "fake/"]);

    expect(labels(f.root)).toContain("Delete selected…");
    click(button("Delete selected…", f.root));
    expect(f.browser.selectedDelete?.members.map((m) => m.name)).toEqual(["external-dlo", "external-slo"]);
    expect(f.browser.selectedDelete?.excludedPrefixes).toEqual(["fake/"]);
  });

  async function list() {
    const f = await state();
    const component = mount(WorkingList, { target: f.root, props: { browser: f.browser } });
    mounted.add(component);
    flushSync(); await settle();
    const row = (name: string) => {
      const found = [...f.root.querySelectorAll<HTMLElement>('[role="option"]')]
        .find((el) => el.querySelector(".name")?.firstChild?.textContent?.trim() === name);
      if (!found) throw new Error(`Missing rendered row ${name}`);
      return found;
    };
    const pick = async (name: string, ctrlKey = false, shiftKey = false) => {
      row(name).dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, ctrlKey, shiftKey }));
      flushSync(); await settle();
    };
    return { ...f, row, pick, foot: () => f.root.querySelector(".pane-foot")?.textContent ?? "" };
  }

  it("shows a toggled-off member as not selected although it keeps the focus", async () => {
    const f = await list();
    await f.pick("empty-object");
    await f.pick("external-dlo", true);
    await f.pick("external-slo", true);
    await f.pick("external-dlo", true);
    expect(names(f.browser.chosenRows)).toEqual(["empty-object", "external-slo"]);
    expect(f.browser.openSelectedDelete()).toBe(true);
    expect(f.browser.selectedDelete?.members.map((m) => m.name)).toEqual(["empty-object", "external-slo"]);
    expect(f.foot()).toContain("2 selected");
    expect(f.row("empty-object").getAttribute("aria-selected")).toBe("true");
    expect(f.row("external-slo").getAttribute("aria-selected")).toBe("true");
    expect(f.row("external-dlo").getAttribute("aria-selected")).toBe("false");
    expect(f.root.querySelector('[role="listbox"]')?.getAttribute("aria-activedescendant")).toBe(f.row("external-dlo").id);
  });

  it("toggles the sole selected row off with Ctrl", async () => {
    const f = await list();
    await f.pick("empty-object");
    expect(f.browser.selectionCount).toBe(1);
    expect(f.row("empty-object").getAttribute("aria-selected")).toBe("true");
    await f.pick("empty-object", true);
    expect(f.browser.selectionCount).toBe(0);
    expect(f.browser.selectedIndex).toBe(-1);
    expect(f.row("empty-object").getAttribute("aria-selected")).toBe("false");
    expect(f.foot()).toContain("none selected");
  });

  it("keeps the range anchor through a refresh that leaves the rows unchanged", async () => {
    const f = await list();
    await f.pick("empty-object");
    await f.pick("fake/", false, true);
    expect(names(f.browser.chosenRows)).toEqual(["empty-object", "external-dlo", "external-slo", "fake/"]);
    await f.browser.refreshEntries();
    await settle();
    await f.pick("external-dlo", false, true);
    expect(names(f.browser.chosenRows)).toEqual(["empty-object", "external-dlo"]);
  });
});

describe("the selected-set dialog", () => {
  async function dialog(facts: ContainerSettingsDTO | null | "real" = "real") {
    const f = await state();
    applyEnabledCapabilities(["Object deletion"]);
    if (facts !== "real") vi.spyOn(f.browser, "containerFacts").mockResolvedValue(facts);
    f.browser.select(f.index("empty-object"));
    f.browser.toggleRow(f.index("external-slo"));
    f.browser.toggleRow(f.index("normal/"));
    await settle();
    if (!f.browser.openSelectedDelete()) throw new Error("no selection to delete");
    const op = f.browser.selectedDelete!;
    const snapshot = JSON.parse(JSON.stringify(op.members)) as ListingRowDTO[];
    const done = vi.fn();
    const component = mount(SelectedDeleteDialog, { target: f.root, props: { browser: f.browser, onClose: done } });
    mounted.add(component);
    flushSync(); await settle();
    return { ...f, op, snapshot, done };
  }

  it("lists the members, the prefix rows left out, the ceiling and the versioning consequence, and confirms only with the typed count", async () => {
    const facts: ContainerSettingsDTO = { publicRead: false, publicListing: false, referrerGrants: [], sharedWrite: false, staticWeb: false,
      versioningMode: "versions_location", versioningArchive: "the-archive", quota: false, cors: false, readAcl: "", writeAcl: "", observedAt: "", generation: 1 };
    const f = await dialog(facts);
    const text = (f.root.textContent ?? "").replace(/\s+/g, " ");
    expect(text).toContain("Delete 2 selected objects");
    expect(text).toContain("empty-object");
    expect(text).toContain("external-slo");
    expect(text).toContain("Not included: 1 prefix row");
    expect(text).toContain("normal/");
    expect(text).toContain("at most 250,000 objects");
    expect(text).toContain("keeps versions in the-archive");
    expect(text).toContain("changed since is left as it is");
    expect(text).toContain("read again first and what that read finds is what is deleted");
    const start = vi.spyOn(f.fake, "startSelectedDelete");
    const confirm = button("Delete 2 objects", f.root);
    expect(confirm.disabled).toBe(true);
    type(f.root, "3");
    expect(confirm.disabled).toBe(true);
    click(confirm);
    await settle();
    expect(start).not.toHaveBeenCalled();
    type(f.root, "2");
    expect(confirm.disabled).toBe(false);
    click(confirm);
    await settle();
    expect(start).toHaveBeenCalledOnce();
    expect(start.mock.calls[0][0]).toEqual({ scope: { bookmarkId: f.bookmark.id, generation: 1 }, container: "fixtures", members: f.snapshot, confirm: "2" });
    expect(f.op.status).toBe("done");
    expect(f.op.jobId).toBe("job-1");
    expect(f.root.textContent).toContain("Deletion started");
    expect(f.root.textContent).toContain("job-1");
    expect(f.browser.selectionCount).toBe(0);
    expect(f.done).not.toHaveBeenCalled();
    await f.browser.refreshEntries();
    await settle();
    expect(names(f.browser.entries)).not.toContain("empty-object");
    expect(names(f.browser.entries)).not.toContain("external-slo");
    expect(names(f.browser.entries)).toContain("external-dlo");
    expect(names(f.browser.entries)).toContain("normal/");
    click(button("Close", f.root));
    expect(f.done).toHaveBeenCalledOnce();
  });

  it("states the operator's selection ceiling, the download object ceiling setting", async () => {
    const f = await state();
    applyEnabledCapabilities(["Object deletion"]);
    await f.browser.settings.load();
    await f.browser.settings.save({ ...f.browser.settings.document!, downloadObjectCeiling: 1000, browserRetentionCeiling: 2000 });
    f.browser.select(f.index("empty-object"));
    f.browser.toggleRow(f.index("external-slo"));
    await settle();
    expect(f.browser.openSelectedDelete()).toBe(true);
    expect(f.browser.selectedDelete?.ceiling).toBe(1000);
    const component = mount(SelectedDeleteDialog, { target: f.root, props: { browser: f.browser, onClose: vi.fn() } });
    mounted.add(component);
    flushSync(); await settle();
    expect((f.root.textContent ?? "").replace(/\s+/g, " ")).toContain("at most 1,000 objects; the ceiling is set in Settings");
  });

  it("cannot confirm until the versioning consequence is known or explicitly unknown", async () => {
    const f = await state();
    applyEnabledCapabilities(["Object deletion"]);
    let settleFacts!: (facts: ContainerSettingsDTO | null) => void;
    vi.spyOn(f.browser, "containerFacts").mockReturnValue(new Promise((resolve) => { settleFacts = resolve; }));
    const start = vi.spyOn(f.fake, "startSelectedDelete");
    f.browser.select(f.index("empty-object"));
    f.browser.toggleRow(f.index("external-slo"));
    await settle();
    f.browser.openSelectedDelete();
    const component = mount(SelectedDeleteDialog, { target: f.root, props: { browser: f.browser, onClose: vi.fn() } });
    mounted.add(component);
    flushSync(); await settle();
    expect(f.root.textContent).toContain("Checking how this container keeps versions");
    type(f.root, "2");
    const confirm = button("Delete 2 objects", f.root);
    expect(confirm.disabled).toBe(true);
    click(confirm);
    await settle();
    expect(start).not.toHaveBeenCalled();
    settleFacts(null);
    await settle();
    expect(f.root.textContent).toContain("could not be read");
    expect(confirm.disabled).toBe(false);
  });

  it("ends the confirmation without deleting when the view moves", async () => {
    const f = await dialog(null);
    const start = vi.spyOn(f.fake, "startSelectedDelete");
    type(f.root, "2");
    await f.browser.openContainer("slo-parts");
    await settle();
    expect(f.root.textContent).toContain("Nothing was deleted");
    expect(f.root.textContent).toContain("the view moved");
    expect(labels(f.root)).toEqual(["Close"]);
    expect(start).not.toHaveBeenCalled();
    expect(f.op.status).toBe("pending");
  });

  it("ends the confirmation when the selection changes to other names with the same count, for good", async () => {
    const f = await dialog(null);
    const start = vi.spyOn(f.fake, "startSelectedDelete");
    type(f.root, "2");
    f.browser.toggleRow(f.index("external-dlo"));
    f.browser.toggleRow(f.index("external-slo"));
    await settle();
    expect(names(f.browser.chosenRows)).toEqual(["empty-object", "external-dlo", "normal/"]);
    expect(f.root.textContent).toContain("the selection changed");
    expect(labels(f.root)).toEqual(["Close"]);
    // The original rows selected again do not re-arm a snapshot the operator has not re-read.
    f.browser.toggleRow(f.index("external-slo"));
    f.browser.toggleRow(f.index("external-dlo"));
    await settle();
    expect(f.browser.selectedDeleteProblem()).toBeNull();
    expect(labels(f.root)).toEqual(["Close"]);
    expect(start).not.toHaveBeenCalled();
  });

  it("ends the confirmation when a refresh changes one member's listed entity tag with the same count", async () => {
    const f = await dialog(null);
    const start = vi.spyOn(f.fake, "startSelectedDelete");
    type(f.root, "2");
    const real = f.fake.objects;
    f.fake.objects = async (...args) => {
      const page = await real(...args);
      return { ...page, rows: page.rows.map((row) => row.name === "external-slo" ? { ...row, etag: "replaced-since-listed" } : row) };
    };
    await f.browser.refreshEntries();
    await settle();
    expect(f.browser.selectionCount).toBe(3);
    expect(f.root.textContent).toContain("external-slo changed since it was listed");
    expect(labels(f.root)).toEqual(["Close"]);
    expect(start).not.toHaveBeenCalled();
  });

  it("shows a refused start and stays open for another try", async () => {
    const f = await dialog(null);
    f.fake.startSelectedDelete = vi.fn(async () => { throw new Error("the members could not be recorded, so nothing was sent"); });
    type(f.root, "2");
    click(button("Delete 2 objects", f.root));
    await settle();
    expect(f.root.textContent).toContain("nothing was sent");
    expect(f.op.status).toBe("pending");
    expect(labels(f.root)).toContain("Delete 2 objects");
    expect(f.browser.selectionCount).toBe(3);
  });
});

describe("the Delete key and the selection shortcuts", () => {
  async function screen(enabled: string[]) {
    Object.assign(api, newFakeApi());
    const info = api.info;
    api.info = async () => ({ ...(await info()), enabledCapabilities: enabled, unrecognisedCapabilities: [] });
    api.startSelectedDelete = vi.fn(async () => "job-screen");
    await api.saveBookmark({ id: "", displayName: "Key fixture", authUrl: "https://example.test/auth",
      username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
    const root = document.createElement("div"); document.body.append(root);
    const component = mount(BrowserScreen, { target: root }); mounted.add(component);
    flushSync(); await settle();
    click(document.querySelector<HTMLButtonElement>('[aria-label="Key fixture"]')!); await settle();
    click([...document.querySelectorAll<HTMLButtonElement>(".container-row")]
      .find((b) => b.querySelector(".container-name")?.textContent?.trim() === "fixtures")!);
    await settle();
    const row = (name: string) => [...document.querySelectorAll<HTMLElement>('[role="option"]')]
      .find((el) => el.querySelector(".name")?.textContent?.trim() === name)!;
    const listbox = document.querySelector<HTMLElement>('[role="listbox"]')!;
    return { row, listbox, foot: listbox.closest("section")!.querySelector<HTMLElement>(".pane-foot")! };
  }

  it("opens the selected-set dialog for several rows and deletes nothing by itself", async () => {
    const s = await screen(["Object deletion"]);
    s.row("empty-object").dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })); flushSync(); await settle();
    s.row("external-dlo").dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, ctrlKey: true })); flushSync(); await settle();
    expect(s.foot.textContent).toContain("2 selected");
    s.listbox.dispatchEvent(new KeyboardEvent("keydown", { key: "Delete", bubbles: true, cancelable: true })); flushSync(); await settle();
    expect(document.querySelector('[aria-label="Delete selected objects"]')).not.toBeNull();
    expect(document.querySelector('[aria-label="Delete object"]')).toBeNull();
    expect(api.startSelectedDelete).not.toHaveBeenCalled();
  });

  it("opens the selected-set dialog in an ordinary build with nothing switched on", async () => {
    // Returned on 29 October 2026 (ADR 0052 decision 11): the Delete key
    // reaches the dialog with no token, and still deletes nothing by itself.
    const s = await screen([]);
    s.row("empty-object").dispatchEvent(new PointerEvent("pointerdown", { bubbles: true })); flushSync(); await settle();
    s.row("external-dlo").dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, ctrlKey: true })); flushSync(); await settle();
    s.listbox.dispatchEvent(new KeyboardEvent("keydown", { key: "Delete", bubbles: true, cancelable: true })); flushSync(); await settle();
    expect(document.querySelector('[aria-label="Delete selected objects"]')).not.toBeNull();
    expect(api.startSelectedDelete).not.toHaveBeenCalled();
  });

  it("selects every loaded row with Ctrl+A and clears with Escape", async () => {
    const s = await screen([]);
    const rows = document.querySelectorAll('[role="option"]').length;
    expect(rows).toBeGreaterThan(2);
    const all = new KeyboardEvent("keydown", { key: "a", ctrlKey: true, bubbles: true, cancelable: true });
    window.dispatchEvent(all); flushSync(); await settle();
    expect(all.defaultPrevented).toBe(true);
    expect(s.foot.textContent).toContain(`${rows} selected`);
    const escape = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    window.dispatchEvent(escape); flushSync(); await settle();
    expect(escape.defaultPrevented).toBe(true);
    expect(s.foot.textContent).toContain("none selected");
    // With nothing selected Escape is left to whoever else wants it.
    const again = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true });
    window.dispatchEvent(again); flushSync();
    expect(again.defaultPrevented).toBe(false);
  });
});

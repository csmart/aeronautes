import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { Api } from "../../lib/api";
import { newFakeApi } from "./fakeApi";
import BrowserScreen from "./BrowserScreen.svelte";

// ADR 0108 decision 1, as amended on 15 November 2026: a selection's folder
// rows are not copied or downloaded, so a selection that holds one is told
// so before either dialog opens. These go through the whole screen, from a
// press of the toolbar or a row's menu to whichever dialog is then open.

const api = vi.hoisted(() => ({} as Api));
vi.mock("../../lib/api", () => ({ liveApi: api }));

const mounted = new Set<ReturnType<typeof mount>>();
beforeEach(() => {
  Object.assign(api, newFakeApi());
  vi.spyOn(HTMLElement.prototype, "offsetParent", "get").mockImplementation(function (this: HTMLElement) {
    return this.closest("[hidden]") ? null : this.parentElement;
  });
});
afterEach(async () => {
  for (const component of [...mounted].reverse()) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

async function settle() {
  for (let i = 0; i < 32; i++) { await Promise.resolve(); flushSync(); }
}

const fixtureBookmark = { id: "", displayName: "Selection fixture", authUrl: "https://example.test/auth",
  username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false };

// The fixtures container's top: six folder rows and some objects.
async function openFixtures() {
  await api.saveBookmark(fixtureBookmark, "test-only");
  mounted.add(mount(BrowserScreen, { target: document.body }));
  flushSync(); await settle();
  document.querySelector<HTMLElement>(`[aria-label="${fixtureBookmark.displayName}"]`)!.click(); await settle();
  [...document.querySelectorAll<HTMLButtonElement>(".container-row")]
    .find((r) => r.querySelector(".container-name")?.firstChild?.textContent?.trim() === "fixtures")!.click();
  await settle();
  const rows = [...document.querySelectorAll<HTMLElement>('[aria-label="Objects"] [role="option"]')];
  return {
    folders: rows.filter((r) => r.querySelector(".icon.folder")),
    objects: rows.filter((r) => !r.querySelector(".icon.folder")),
  };
}

const nameOf = (row: HTMLElement) => row.querySelector(".name")!.textContent!.trim();

// A plain click for the first row and a Ctrl click for each one after it.
function select(rows: HTMLElement[]) {
  rows.forEach((row, i) => row.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0, ctrlKey: i > 0 })));
  flushSync();
}

type Action = "Copy" | "Download";
type Entry = "toolbar" | "menu";

async function press(action: Action, word: string, entry: Entry, onRow?: HTMLElement) {
  const label = `${action} ${word}…`;
  if (entry === "menu") {
    onRow!.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 2 }));
    onRow!.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true, clientX: 40, clientY: 60 }));
    flushSync();
  }
  const scope = entry === "menu" ? '[role="menu"] [role="menuitem"]' : ".actions button";
  [...document.querySelectorAll<HTMLButtonElement>(scope)].find((b) => b.textContent?.trim() === label)!.click();
  await settle();
}

const warning = () => document.querySelector<HTMLElement>('[role="alertdialog"][aria-label="Selected folders are left out"]');
const warningText = () => warning()!.textContent!.replace(/\s+/g, " ");
const warningButtons = () => [...warning()!.querySelectorAll<HTMLButtonElement>(".actions button")].map((b) => b.textContent!.trim());
const dialogOf = (action: Action) => document.querySelector<HTMLElement>(`[role="dialog"][aria-label="${action === "Copy" ? "Transfer" : "Download"}"]`);

async function pressIn(root: HTMLElement, label: string) {
  [...root.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.trim() === label)!.click();
  await settle();
}

// What a copy or a download would send; none of it may leave while only the
// warning has been read.
function requests() {
  return [vi.spyOn(api, "planCopy"), vi.spyOn(api, "startCopy"), vi.spyOn(api, "pickDownloadDir"),
    vi.spyOn(api, "preflightDownload"), vi.spyOn(api, "startDownload")];
}

const cases = (["Copy", "Download"] as const).flatMap((action) => (["toolbar", "menu"] as const).map((entry) => ({ action, entry })));

describe("a selection holding a folder beside objects", () => {
  it.each(cases)("is warned before $action from the $entry, and goes on with the objects only", async ({ action, entry }) => {
    const { folders, objects } = await openFixtures();
    const sent = requests();
    const done = action === "Copy" ? "copied" : "downloaded";
    const verb = action.toLowerCase();
    select([objects[0], folders[0]]);

    await press(action, "selected", entry, objects[0]);
    expect(dialogOf(action)).toBeNull();
    const text = warningText();
    expect(text).toContain(`1 selected folder will not be ${done}`);
    expect(text).toContain(`This selection holds 1 folder beside 1 object: ${nameOf(folders[0])}.`);
    expect(text).toContain(`That folder and everything in it will not be ${done}.`);
    expect(text).toContain(`To ${verb} one folder with everything in it, select that folder alone and use “${action} folder…”.`);
    expect(text).toContain(`To ${verb} everything here, close this, press Escape to clear the selection and use “${action} container…”.`);
    expect(warningButtons()).toEqual(["Cancel", `${action} the 1 object only…`]);

    // Cancelling opens nothing and sends nothing.
    await pressIn(warning()!, "Cancel");
    expect(warning()).toBeNull();
    expect(dialogOf(action)).toBeNull();
    for (const request of sent) expect(request).not.toHaveBeenCalled();

    // Going on opens that action's own dialog, for the object alone.
    await press(action, "selected", entry, objects[0]);
    await pressIn(warning()!, `${action} the 1 object only…`);
    expect(warning()).toBeNull();
    const opened = dialogOf(action)!.textContent!.replace(/\s+/g, " ");
    expect(opened).toContain(action === "Copy" ? `1 selected object: ${nameOf(objects[0])}` : "Download 1 selected object from fixtures/");
  });

  it("states how many folders are left out when it does not name them all", async () => {
    const { folders, objects } = await openFixtures();
    expect(folders.length).toBe(6);
    select([objects[0], ...folders]);
    await press("Copy", "selected", "toolbar");
    const text = warningText();
    expect(text).toContain("6 selected folders will not be copied");
    expect(text).toContain(`This selection holds 6 folders beside 1 object: ${folders.slice(0, 5).map(nameOf).join(", ")}, and 1 more not named here.`);
    expect(text).not.toContain(nameOf(folders[5]));
    expect(text).toContain("Those folders and everything in them will not be copied.");
  });
});

describe("a selection of folders alone", () => {
  it.each(["Copy", "Download"] as const)("is told why %s cannot take it, and can only close", async (action) => {
    const { folders } = await openFixtures();
    const sent = requests();
    select([folders[0], folders[1]]);
    await press(action, "selected", "toolbar");
    const text = warningText();
    expect(text).toContain(`Several folders cannot be ${action === "Copy" ? "copied" : "downloaded"} in one go yet`);
    expect(text).toContain(`This selection holds 2 folders and no objects: ${nameOf(folders[0])}, ${nameOf(folders[1])}.`);
    expect(warningButtons()).toEqual(["Close"]);
    await pressIn(warning()!, "Close");
    expect(warning()).toBeNull();
    expect(dialogOf(action)).toBeNull();
    for (const request of sent) expect(request).not.toHaveBeenCalled();
  });
});

describe("a selection that leaves no folder out", () => {
  it.each([
    { name: "objects alone", word: "selected", pick: (f: HTMLElement[], o: HTMLElement[]) => [o[0], o[1]] },
    { name: "one folder alone", word: "folder", pick: (f: HTMLElement[]) => [f[0]] },
    { name: "nothing", word: "container", pick: () => [] as HTMLElement[] },
  ])("opens the dialog with no warning for $name", async ({ word, pick }) => {
    const { folders, objects } = await openFixtures();
    select(pick(folders, objects));
    await press("Copy", word, "toolbar");
    expect(warning()).toBeNull();
    expect(dialogOf("Copy")).not.toBeNull();
  });
});

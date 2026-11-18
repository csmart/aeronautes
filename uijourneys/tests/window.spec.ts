import { test, type Locator, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { randomBytes } from "node:crypto";
import { existsSync, mkdirSync, readdirSync, readFileSync } from "node:fs";
import { basename, join } from "node:path";
import { control } from "../control.ts";
import { connect, expect, finish, localFolder, objectRow, openContainer, openFolder, ownContainer, writeFiles } from "../helpers.ts";

// Test plan steps 17 and 18b: the status bar names the bookmark, and the
// About link and dialog name the build. The revision the dialog shows is
// compared with the checkout the run built from.
test("the status bar and the About dialog", async ({ page }, info) => {
  const name = await connect(page, info.project.name);
  const status = page.locator("footer.status");
  await expect(status.getByText(`connected to ${name}`, { exact: true })).toBeVisible();
  const about = status.getByRole("button", { name: /^Aeronautes \S+$/ });
  await expect(about).toBeVisible();
  await about.click();
  const dialog = page.getByRole("dialog", { name: "About Aeronautes" });
  const version = dialog.locator("p.version");
  await expect(version).toHaveText(/^Version (dev|\d+\.\d+\.\d+) \((dev )?[0-9a-f]{12}(, (may be )?modified)?\)$/);
  const revision = execFileSync("git", ["rev-parse", "HEAD"], { encoding: "utf8" }).trim();
  await expect(version).toContainText(`${revision.slice(0, 12)}`);
  await expect(version.locator("span")).toHaveAttribute("title", revision);
  await dialog.getByRole("button", { name: "Close" }).click();
});

// Test plan step 19f: an object's and a folder's menus offer exactly the
// toolbar's actions for that row, and each opens the dialog the toolbar
// opens for it. Every dialog is closed unsent. "Copy name" is chosen, but
// what it puts on the clipboard cannot be read: the server build's
// clipboard is a no-op.
test("an object's and a folder's context menus", async ({ page }, info) => {
  const container = await ownContainer(info.project.name, "ui-rowmenu");
  await openContainer(page, info.project.name, container);
  const folder = localFolder();
  writeFiles(folder, [16], 1);
  mkdirSync(join(folder, "inner"));
  writeFiles(join(folder, "inner"), [32], 2);
  await uploadFolder(page, folder);
  await openFolder(page, basename(folder));

  const file = objectRow(page, "file00.bin");
  const inner = page.getByRole("listbox", { name: "Objects" }).getByRole("option", { name: /^inner\/ / });
  const download = page.getByRole("dialog", { name: "Download" });
  const copy = page.getByRole("dialog", { name: "Transfer" });

  await expectMenu(page, file, "file00.bin", ["Download object…", "Copy object…", "Copy name", "Delete object…"]);
  await sameAsToolbar(page, file, "file00.bin", "Download object…", download, download.getByRole("button", { name: "Close" }));
  await sameAsToolbar(page, file, "file00.bin", "Copy object…", copy, copy.getByRole("button", { name: "Close" }).first());
  const objectDelete = page.getByRole("alertdialog", { name: "Delete object" });
  await sameAsToolbar(page, file, "file00.bin", "Delete object…", objectDelete, objectDelete.getByRole("button", { name: "Cancel" }));

  await expectMenu(page, inner, "inner/", ["Open", "Download folder…", "Copy folder…", "Copy name", "Delete prefix…"]);
  await sameAsToolbar(page, inner, "inner/", "Download folder…", download, download.getByRole("button", { name: "Close" }));
  await sameAsToolbar(page, inner, "inner/", "Copy folder…", copy, copy.getByRole("button", { name: "Close" }).first());
  const prefixDelete = page.getByRole("dialog", { name: "Delete" });
  await sameAsToolbar(page, inner, "inner/", "Delete prefix…", prefixDelete, prefixDelete.getByRole("button", { name: "Close" }).first());

  await inner.click({ button: "right" });
  await menuOf(page, "inner/").getByRole("menuitem", { name: "Copy name" }).click();
  await expect(menuOf(page, "inner/")).toHaveCount(0);
  await inner.click({ button: "right" });
  await menuOf(page, "inner/").getByRole("menuitem", { name: "Open" }).click();
  await expect(page.getByRole("navigation", { name: "Path" }).getByRole("button", { name: "inner" })).toBeVisible();
  await expect(objectRow(page, "file00.bin")).toBeVisible();
});

// 0.1.0 test plan step 6: Find in container finds a name by part of it,
// anywhere in the container, and "#" with nothing selected counts the
// folder being browsed.
test("find a name by part of it, and count a folder", async ({ page }, info) => {
  const container = await ownContainer(info.project.name, "ui-find");
  await openContainer(page, info.project.name, container);
  const folder = localFolder();
  writeFiles(folder, [16, 16, 16], 1);
  await uploadFolder(page, folder);
  const name = basename(folder);

  await openFolder(page, name);
  const count = page.getByRole("button", { name: "Count objects" });
  await expect(count).toHaveAttribute("title", new RegExp(`^Count the objects in ${name}/:`));
  await count.click();
  await expect(page.getByRole("status").filter({ hasText: /^\d[\d,]* objects? in / })).toHaveText(`3 objects in ${name}/`);

  await page.getByRole("button", { name: "Up one level" }).click();
  await page.getByRole("button", { name: "Find in container" }).click();
  await page.getByLabel("Find query").fill("ILE01");
  await page.getByRole("button", { name: "Find", exact: true }).click();
  const matches = page.locator(".matches").getByRole("button");
  await expect(matches).toHaveCount(1);
  await expect(matches).toHaveText(new RegExp(`^${name}/file01\\.bin`));
  await matches.click();
  await expect(page.getByRole("navigation", { name: "Path" }).getByRole("button", { name })).toBeVisible();
  await expect(objectRow(page, "file01.bin")).toHaveAttribute("aria-selected", "true");
});

// Test plan step 18a: a public container's red "Public" mark sits at the
// right of the path's row, before its two tools, with the whole notice in
// its hover bubble, and the list starts at the same height whether the
// container is public or not.
test("the public mark, and the list's height beside it", async ({ page }, info) => {
  const target = info.project.name;
  const open = await ownContainer(target, "ui-public");
  const closed = await ownContainer(target, "ui-private");
  for (const container of [closed, open]) {
    await openContainer(page, target, container);
    await page.getByRole("button", { name: "New folder…" }).click();
    const dialog = page.getByRole("dialog", { name: "New folder" });
    await dialog.getByLabel("Name").fill("journey-folder");
    await dialog.getByRole("button", { name: "Create" }).click();
    await expect(dialog).toHaveCount(0);
  }
  await page.getByRole("button", { name: "Configure container" }).click();
  await page.getByLabel("Anyone can read objects").check();
  await page.getByLabel(`I understand this makes ${open} publicly readable.`).check();
  await control.answer("public", true);
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("button", { name: "Configure container" })).toBeEnabled();

  await openContainer(page, target, open);
  const mark = page.getByRole("note", { name: /^Public: / });
  await expect(mark).toHaveText("Public");
  const notice = (await mark.getAttribute("aria-label"))!;
  expect(notice).toContain(`anyone with the URL can read objects in ${open}`);
  const path = (await page.getByRole("navigation", { name: "Path" }).boundingBox())!;
  const refresh = (await page.getByRole("button", { name: "Refresh listing" }).boundingBox())!;
  const markBox = (await mark.boundingBox())!;
  expect(markBox.x, "the mark is after the path").toBeGreaterThanOrEqual(path.x + path.width);
  expect(markBox.x + markBox.width, "the mark is before the tools").toBeLessThanOrEqual(refresh.x);
  expect(Math.abs(markBox.y + markBox.height / 2 - (refresh.y + refresh.height / 2)), "the mark is on the tools' row").toBeLessThan(refresh.height);
  await mark.hover();
  await expect(page.getByRole("tooltip")).toHaveText(notice);
  const list = page.getByRole("listbox", { name: "Objects" });
  await expect(list).toBeVisible();
  const publicTop = (await list.boundingBox())!.y;

  await page.getByRole("button", { name: new RegExp(`^${closed} `) }).click();
  await expect(page.getByRole("navigation", { name: "Path" }).getByRole("button", { name: closed })).toBeVisible();
  await expect(page.getByRole("note", { name: /^Public: / })).toHaveCount(0);
  await expect(list).toBeVisible();
  expect((await list.boundingBox())!.y, "the list's top beside a private container").toBe(publicTop);
});

// Test plan step 10, on the mock: settings and bookmarks are backed up to
// a file through the folder picker, with no key in it; a bookmark deleted
// afterwards comes back from the restore, through the file picker, with no
// saved key. The key is one no file could hold by chance.
test("back up the settings with no key, and restore a bookmark without one", async ({ page }, info) => {
  test.skip(info.project.name !== "mock", "bookmarks are made in the editor on the mock only");
  const key = `journey-key-${randomBytes(12).toString("hex")}`;
  const name = "journey backup";
  await connect(page, "mock");
  await saveBookmark(page, name, key);

  const into = localFolder();
  const settings = await openSettings(page);
  await control.asked();
  await control.pickFolder(into);
  await settings.getByRole("button", { name: "Back up settings…" }).click();
  await expect(settings.getByText(/^Backed up settings and \d+ bookmarks? to /)).toBeVisible();
  expect(await control.asked()).toEqual([{ kind: "pick folder", title: "Choose a folder to save the settings backup", text: "" }]);
  const files = readdirSync(into);
  expect(files).toHaveLength(1);
  const backup = join(into, files[0]);
  const text = readFileSync(backup, "utf8");
  expect(text, "the backup holds the bookmark").toContain(`"${name}"`);
  expect(text.includes(key), "the backup holds the bookmark's key").toBe(false);
  await settings.getByRole("button", { name: "Close settings" }).click();

  await deleteBookmark(page, name);
  const again = await openSettings(page);
  await control.pickFiles([backup]);
  await again.getByRole("button", { name: "Restore settings…" }).click();
  await expect(again.getByRole("status").filter({ hasText: /^Restored/ })).toBeVisible();
  await again.getByRole("button", { name: "Close settings" }).click();

  const tile = page.getByRole("navigation", { name: "Bookmarks" }).getByRole("button", { name, exact: true });
  await expect(tile.getByRole("img", { name: "No saved key" })).toBeVisible();
  await tile.click();
  await expect(page.getByLabel("API key")).toHaveValue("");
  await page.getByRole("button", { name: "Cancel" }).click();
  await deleteBookmark(page, name);
});

// Test plan step 19a, on the mock: the run's application keeps its
// settings and its database in the fresh XDG folders the runner gave it.
// A changed setting is in settings.json as JSON, and neither file holds the
// key of a bookmark saved meanwhile. The mock's own key, "testing", is an
// ordinary word, so a key no file could hold by chance is saved instead.
test("the settings file and the database hold no key", async ({ page }, info) => {
  test.skip(info.project.name !== "mock", "bookmarks are made in the editor on the mock only");
  const state = join(process.env.AERONAUTES_PRIVATE!, "state");
  const settingsFile = join(state, "config", "aeronautes", "settings.json");
  const database = join(state, "data", "aeronautes", "aeronautes.sqlite");
  const key = `journey-key-${randomBytes(12).toString("hex")}`;
  const name = "journey files";
  await connect(page, "mock");
  await saveBookmark(page, name, key);

  const settings = await openSettings(page);
  const queue = settings.getByLabel("Queue depth");
  await queue.fill("7");
  await queue.press("Tab");
  await expect(settings.getByText("7 waiting at most")).toBeVisible();
  await expect.poll(() => JSON.parse(readFileSync(settingsFile, "utf8")).maxQueuedJobs).toBe(7);

  expect(existsSync(database), "the database").toBe(true);
  for (const file of [settingsFile, database, `${database}-wal`]) {
    if (!existsSync(file)) continue;
    expect(readFileSync(file).includes(key), `${basename(file)} holds the key`).toBe(false);
  }

  await queue.fill("16");
  await queue.press("Tab");
  await expect(settings.getByText("16 waiting at most")).toBeVisible();
  await settings.getByRole("button", { name: "Close settings" }).click();
  await deleteBookmark(page, name);
});

async function uploadFolder(page: Page, folder: string) {
  await page.getByRole("button", { name: "Upload…" }).click();
  const dialog = page.getByRole("dialog", { name: "Upload" });
  await control.pickFolder(folder);
  await dialog.getByRole("button", { name: "Choose folder…" }).click();
  await dialog.getByRole("button", { name: "Start upload" }).click();
  await finish(page, dialog);
  await page.getByRole("button", { name: "Refresh listing" }).click();
}

function menuOf(page: Page, row: string): Locator {
  return page.getByRole("menu", { name: `Actions for ${row}` });
}

// expectMenu requires the row's menu to hold exactly the items, once the
// row has been read: until then neither the menu nor the toolbar offers
// the row's own deletion, which is the last item.
async function expectMenu(page: Page, row: Locator, name: string, items: string[]) {
  await row.click();
  await expect(page.getByRole("group", { name: "Actions" }).getByRole("button", { name: items.at(-1) })).toBeVisible();
  await row.click({ button: "right" });
  await expect(menuOf(page, name).getByRole("menuitem")).toHaveText(items);
  await page.keyboard.press("Escape");
  await expect(menuOf(page, name)).toHaveCount(0);
}

// sameAsToolbar opens a dialog from the row's menu and then from the
// toolbar's button of the same name, with the row selected both times, and
// requires the two to say the same.
async function sameAsToolbar(page: Page, row: Locator, name: string, action: string, dialog: Locator, close: Locator) {
  await row.click({ button: "right" });
  await menuOf(page, name).getByRole("menuitem", { name: action }).click();
  const fromMenu = await whatItSays(dialog);
  await close.click();
  await expect(dialog).toHaveCount(0);
  await page.getByRole("group", { name: "Actions" }).getByRole("button", { name: action }).click();
  expect(await whatItSays(dialog), `${action} from the toolbar`).toEqual(fromMenu);
  await close.click();
  await expect(dialog).toHaveCount(0);
}

// A dialog is told apart by its heading and, for a copy, its source.
async function whatItSays(dialog: Locator): Promise<string[]> {
  const heading = dialog.locator("h2").first();
  await expect(heading).toBeVisible();
  const said = [await heading.innerText()];
  const source = dialog.getByRole("group", { name: "Source scope" });
  if (await source.count() > 0) said.push(await source.innerText());
  return said;
}

async function openSettings(page: Page): Promise<Locator> {
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Settings" });
  await expect(dialog).toBeVisible();
  return dialog;
}

// saveBookmark saves a bookmark of the mock's account with the given key
// without connecting, so the key need not be the mock's.
async function saveBookmark(page: Page, name: string, key: string) {
  const rail = page.getByRole("navigation", { name: "Bookmarks" });
  await rail.getByTitle("Add bookmark").click();
  await page.getByLabel("Display name").fill(name);
  await page.getByLabel("Auth URL").fill(process.env.AERONAUTES_MOCK!);
  await page.getByLabel("Username").fill("test:tester");
  await page.getByLabel("API key").fill(key);
  await page.getByRole("button", { name: "Save", exact: true }).click();
  await expect(rail.getByRole("button", { name, exact: true })).toBeVisible();
}

async function deleteBookmark(page: Page, name: string) {
  const rail = page.getByRole("navigation", { name: "Bookmarks" });
  await rail.getByRole("button", { name: `Edit ${name}` }).click();
  await page.getByRole("button", { name: "Delete…" }).click();
  await page.getByRole("button", { name: "Really delete" }).click();
  await expect(rail.getByRole("button", { name, exact: true })).toHaveCount(0);
}

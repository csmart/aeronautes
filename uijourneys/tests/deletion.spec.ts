import { test, type Locator, type Page } from "@playwright/test";
import { basename } from "node:path";
import { control } from "../control.ts";
import {
  expect, finish, inspector, lag, localFolder, objectRow, openContainer, openFolder, openUpload, ownContainer, writeFiles,
} from "../helpers.ts";

// What each deletion leaves is read by the run's own session, which lists
// the containers the run made, within the bound a real store's listing can
// lag (lag in helpers.ts).
// Three chunks at the upload's chunk size, so the file uploads in three
// segments.
const large = 3 << 20;

// Test plan step 8c: a prefix marker goes alone, and a large object's
// manifest goes without its segments once the option is unticked.
test("delete a marker and a manifest, and keep what they hold", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-marker", true);
  const sidecar = `${container}_segments`;
  await openContainer(page, target, container);
  const folder = basename(await uploadFolder(page, [16, large]));
  await expect.poll(async () => (await control.names(target, sidecar)).length, lag).toBe(3);
  const segments = await control.names(target, sidecar);

  // "New folder…" over the uploaded folder's name puts a marker at it.
  await page.getByRole("button", { name: "New folder…" }).click();
  const create = page.getByRole("dialog", { name: "New folder" });
  await create.getByLabel("Name").fill(folder);
  await create.getByRole("button", { name: "Create" }).click();
  await expect(create).toHaveCount(0);
  await expect.poll(() => control.names(target, container), lag).toContain(`${folder}/`);

  await page.getByRole("listbox", { name: "Objects" }).getByRole("option", { name: new RegExp(`^${folder}/ `) }).click();
  await inspector(page).getByRole("button", { name: "Delete marker…" }).click();
  const markerDialog = page.getByRole("alertdialog", { name: "Delete marker" });
  await expect(markerDialog.getByText("This is a directory marker: the objects under it stay")).toBeVisible();
  const words = markerDialog.getByLabel(/to confirm/);
  await expect(words).toHaveAttribute("placeholder", `delete ${folder}/`);
  // The marker's bare name is not the words.
  await words.fill(`${folder}/`);
  await expect(markerDialog.getByRole("button", { name: "Delete marker" })).toBeDisabled();
  await words.fill(`delete ${folder}/`);
  await markerDialog.getByRole("button", { name: "Delete marker" }).click();
  await expect(markerDialog).toHaveCount(0);
  await expect.poll(async () => (await control.names(target, container)).sort(), lag)
    .toEqual([`${folder}/file00.bin`, `${folder}/file01.bin`]);

  await openFolder(page, folder);
  await objectRow(page, "file01.bin").click();
  await inspector(page).getByRole("button", { name: "Delete manifest…" }).click();
  const manifestDialog = page.getByRole("alertdialog", { name: "Delete manifest" });
  const option = manifestDialog.getByRole("checkbox");
  await expect(option).toBeChecked();
  await expect(manifestDialog.getByText(/segment objects? survives?/)).toHaveCount(0);
  await option.uncheck();
  // Unticked, the dialog says the segments stay.
  await expect(manifestDialog.getByText(/^(Every referenced segment object survives|Referenced segment objects survive)/)).toBeVisible();
  await manifestDialog.getByRole("button", { name: "Delete manifest only" }).click();
  await expect(manifestDialog).toHaveCount(0);
  await expect.poll(() => control.names(target, container), lag).toEqual([`${folder}/file00.bin`]);
  expect((await control.names(target, sidecar)).sort(), "the manifest's segments").toEqual(segments.sort());
});

// Test plan steps 8d and 18j: with rows selected only their deletion is
// offered, and pressing the list's empty space gives the place's back; each
// of the container's three deletions refuses words other than its own; and
// emptying, carried through, empties the container and keeps it, whose
// counts then reach 0 without a refresh.
test("refuse the wrong words for each container deletion, and empty a container", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-empty");
  await openContainer(page, target, container);
  const folder = basename(await uploadFolder(page, [16, 32, 64]));

  const actions = page.getByRole("group", { name: "Actions" });
  await openFolder(page, folder);
  await objectRow(page, "file00.bin").click();
  await objectRow(page, "file01.bin").click({ modifiers: ["ControlOrMeta"] });
  await expect(actions.getByRole("button", { name: "Delete selected…" })).toBeVisible();
  await expect(actions.getByRole("button", { name: "Delete prefix…" })).toHaveCount(0);
  await pressBelowTheRows(page);
  await expect(actions.getByRole("button", { name: "Delete prefix…" })).toBeVisible();
  await expect(actions.getByRole("button", { name: "Delete selected…" })).toHaveCount(0);
  await expect(page.getByRole("listbox", { name: "Objects" }).getByRole("option", { selected: true })).toHaveCount(0);

  await page.getByRole("button", { name: "Up one level" }).click();
  await page.getByRole("listbox", { name: "Objects" }).getByRole("option", { name: new RegExp(`^${folder}/ `) }).click();
  await expect(actions.getByRole("button", { name: "Empty container…" })).toHaveCount(0);
  await pressBelowTheRows(page);
  await expect(actions.getByRole("button", { name: "Empty container…" })).toBeVisible();
  await expect(actions.getByRole("button", { name: "Delete container…" })).toBeVisible();

  // The prefix is armed by "delete" and its scope, never by either alone.
  await page.getByRole("listbox", { name: "Objects" }).getByRole("option", { name: new RegExp(`^${folder}/ `) }).click();
  await actions.getByRole("button", { name: "Delete prefix…" }).click();
  await expectRefused(page, "Delete prefix", [container, `${container}/${folder}/`]);
  await pressBelowTheRows(page);
  await actions.getByRole("button", { name: "Delete container…" }).click();
  await expectRefused(page, "Delete container", [container, `empty ${container}`]);
  await actions.getByRole("button", { name: "Empty container…" }).click();
  const dialog = await expectRefused(page, "Empty container", [container, `delete ${container}`]);
  // Nothing so far was deleted.
  await expect.poll(async () => (await control.names(target, container)).length, lag).toBe(3);

  await expect(dialog.getByText(/^3 objects · /)).toBeVisible();
  const confirm = dialog.getByLabel(/to arm/);
  await confirm.fill(`empty ${container}`);
  await dialog.getByRole("button", { name: "Empty container" }).click();
  await expect(dialog.getByRole("heading", { name: "Deletion complete" })).toBeVisible({ timeout: 8 * 60 * 1000 });
  await expect(dialog.getByText(`Deleted the contents of container ${container}: 3 objects.`)).toBeVisible();
  await dialog.getByRole("button", { name: "Close" }).last().click();
  await expect.poll(() => control.names(target, container), lag).toEqual([]);

  // Staying in the container, its counts follow without a refresh.
  const half = { timeout: 30_000 };
  const objects = inspector(page).locator(".kv", { has: page.getByText("Objects", { exact: true }) }).locator(".kv-value");
  await expect(objects).toHaveText("0", half);
  // The row's count comes from the account's listing, which a real store
  // updates in the background, sometimes after the application's last
  // re-read (F77); the inspector's count above is the container's own.
  if (target === "mock") {
    await expect(page.getByRole("button", { name: new RegExp(`^${container} .* 0 objects$`) })).toBeVisible(half);
  }
  await expect(page.getByRole("navigation", { name: "Path" }).getByRole("button", { name: container })).toBeVisible();
});

// Test plan step 19p, unticked: a prefix deletion without the segments
// option is typed without "and segments", and leaves the segments.
test("delete a prefix with the segments option unticked", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-unticked", true);
  const sidecar = `${container}_segments`;
  await openContainer(page, target, container);
  const folder = basename(await uploadFolder(page, [large]));
  await expect.poll(async () => (await control.names(target, sidecar)).length, lag).toBe(3);
  const segments = await control.names(target, sidecar);
  const kept = await putNeighbours(target, container, folder);

  await page.getByRole("button", { name: "Refresh listing" }).click();
  await page.getByRole("listbox", { name: "Objects" }).getByRole("option", { name: new RegExp(`^${folder}/ `) }).click();
  await page.getByRole("button", { name: "Delete prefix…" }).click();
  const dialog = page.getByRole("dialog", { name: "Delete" });
  await dialog.getByRole("button", { name: "Preview" }).click();
  const option = dialog.getByLabel("Also delete the segments of large objects");
  const confirm = dialog.getByLabel(/to arm/);
  await expect(option).toBeChecked();
  await expect(confirm).toHaveAttribute("placeholder", `delete ${container}/${folder}/ and segments`);
  await confirm.fill(`delete ${container}/${folder}/ and segments`);
  await option.uncheck();
  await expect(confirm).toHaveAttribute("placeholder", `delete ${container}/${folder}/`);
  await expect(confirm).toHaveValue("");
  await expect(dialog.getByText(/Deletion is manifest-only/)).toBeVisible();
  await confirm.fill(`delete ${container}/${folder}/`);
  await dialog.getByRole("button", { name: "Delete prefix" }).click();
  await finish(page, dialog);

  await expect.poll(async () => (await control.names(target, container)).sort(), lag).toEqual(kept);
  expect((await control.names(target, sidecar)).sort(), "the large object's segments").toEqual(segments.sort());
});

// Test plan step 8f: from the Backstage view, one large object goes with
// its segments by the force path, which is armed only by its whole name and
// says that ownership is advisory.
test("delete a large object with its segments from Backstage", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-force", true);
  const sidecar = `${container}_segments`;
  await openContainer(page, target, container);
  const folder = basename(await uploadFolder(page, [large]));
  await expect.poll(async () => (await control.names(target, sidecar)).length, lag).toBe(3);

  await page.getByRole("tab", { name: "Backstage" }).click();
  await page.getByRole("button", { name: new RegExp(`^${container} `) }).click();
  await openFolder(page, folder);
  await objectRow(page, "file00.bin").click();
  await inspector(page).getByRole("button", { name: "Delete manifest…" }).click();
  const dialog = page.getByRole("alertdialog", { name: "Delete manifest" });
  await expect(dialog.getByRole("checkbox")).toBeChecked();
  await expect(dialog.getByText(/Backstage override\. Ownership evidence\s+can identify likely-owned segments but cannot yet prove/)).toBeVisible();
  const button = dialog.getByRole("button", { name: "Delete manifest + segments" });
  const name = dialog.getByLabel("Type the object name to arm it:");
  await expect(button).toBeDisabled();
  // The name within its folder is not the object's whole name.
  await name.fill("file00.bin");
  await expect(button).toBeDisabled();
  await name.fill(`${folder}/file00.bin`);
  await button.click();
  await expect(dialog).toHaveCount(0);

  await expect.poll(() => control.names(target, sidecar), lag).toEqual([]);
  await expect.poll(() => control.names(target, container), lag).toEqual([]);
  await page.getByRole("tab", { name: "Logical" }).click();
});

// uploadFolder uploads a new local folder of files of the given sizes into
// the open container, with a chunk size that segments the large ones, and
// returns the local folder.
async function uploadFolder(page: Page, sizes: number[]): Promise<string> {
  const folder = localFolder();
  writeFiles(folder, sizes, 1);
  const dialog = await openUpload(page);
  await control.pickFolder(folder);
  await dialog.getByRole("button", { name: "Choose folder…" }).click();
  await dialog.getByRole("button", { name: "Start upload" }).click();
  await finish(page, dialog);
  await page.getByRole("button", { name: "Refresh listing" }).click();
  return folder;
}

// putNeighbours writes, from the run's own session, the objects a deletion
// of the folder's prefix must leave: one in a folder whose name starts as
// the folder's does, and one at the container's top. Without them a
// container that held only the prefix could not tell a deletion of the
// prefix from one of the whole container. It returns their names, sorted.
async function putNeighbours(target: string, container: string, folder: string): Promise<string[]> {
  const names = [`${folder}x/file00.bin`, "top.bin"];
  for (const name of names) await control.put(target, container, name, "a neighbour that stays");
  return names.sort();
}

// pressBelowTheRows presses the list where no row is, as a person clicks
// under the last row.
async function pressBelowTheRows(page: Page) {
  const list = page.getByRole("listbox", { name: "Objects" });
  const box = (await list.boundingBox())!;
  await list.click({ position: { x: 20, y: box.height - 10 } });
}

// expectRefused previews the deletion the dialog was opened for and checks
// that each of the wrong words leaves its button disabled, then returns the
// dialog with the field cleared.
async function expectRefused(page: Page, action: string, wrong: string[]): Promise<Locator> {
  const dialog = page.getByRole("dialog", { name: "Delete" });
  await expect(dialog.getByRole("heading", { name: action, exact: true })).toBeVisible();
  await dialog.getByRole("button", { name: "Preview" }).click();
  const confirm = dialog.getByLabel(/to arm/);
  await expect(confirm).toBeVisible();
  const button = dialog.getByRole("button", { name: action, exact: true });
  for (const words of wrong) {
    await confirm.fill(words);
    await expect(button, `typed "${words}"`).toBeDisabled();
  }
  await confirm.fill("");
  if (action !== "Empty container") {
    await dialog.getByRole("button", { name: "Close" }).first().click();
    await expect(dialog).toHaveCount(0);
  }
  return dialog;
}

// Test plan step 8e, and ADR 0122: a deletion deletes whatever a name holds
// when its request arrives. An object written over by another program
// between the preview and the start is deleted with the rest, and the dialog
// says beforehand that this is what happens.
test("delete a prefix whose object changed after the preview", async ({ page }, info) => {
  const container = await ownContainer(info.project.name, "ui-changed");
  const folder = localFolder();
  const names = [...writeFiles(folder, [16, 32, 64], 1).keys()];
  await openContainer(page, info.project.name, container);
  await page.getByRole("button", { name: "Upload…" }).click();
  const upload = page.getByRole("dialog", { name: "Upload" });
  await control.pickFolder(folder);
  await upload.getByRole("button", { name: "Choose folder…" }).click();
  await upload.getByRole("button", { name: "Start upload" }).click();
  await finish(page, upload);
  const kept = await putNeighbours(info.project.name, container, basename(folder));

  await page.getByRole("button", { name: "Refresh listing" }).click();
  await openFolder(page, folder);
  await page.getByRole("button", { name: "Delete prefix…" }).click();
  const dialog = page.getByRole("dialog", { name: "Delete" });
  await dialog.getByRole("button", { name: "Preview" }).click();
  await expect(dialog).toContainText("if something else has written to a name since this preview, that is what goes");
  const confirm = dialog.getByLabel(/to arm/);
  await expect(confirm).toBeVisible();

  // Another program writes over one object after the preview.
  const changed = `${basename(folder)}/${names[1]}`;
  await control.put(info.project.name, container, changed, "written by another program");
  await confirm.fill((await confirm.getAttribute("placeholder"))!);
  await dialog.getByRole("button", { name: "Delete prefix" }).click();
  await finish(page, dialog);
  await expect.poll(async () => (await control.names(info.project.name, container)).sort(), lag).toEqual(kept);
});

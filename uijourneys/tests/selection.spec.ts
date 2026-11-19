import { test, type Locator, type Page } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";
import { control } from "../control.ts";
import {
  digestOf, expect, finish, lag, localFolder, objectRow, openContainer, openFolder, openUpload, ownContainer, uploadFolder, writeFiles,
} from "../helpers.ts";

// A folder is uploaded with a file and a folder inside it, so that one
// listing shows both. Selecting rows there must leave the toolbar and the
// list where they were. Then ADR 0108 decision 7: a selected folder is taken
// with everything in it, so a selection of the file and the folder is copied
// and downloaded whole, the folder's name kept, and nothing opens first.
test("a selection of a file and a folder is copied, downloaded and deleted with the folder's contents", async ({ page }, info) => {
  const container = await ownContainer(info.project.name, "ui-selection");
  const folder = localFolder();
  const top = writeFiles(folder, [16], 1);
  mkdirSync(join(folder, "inner"));
  const nested = writeFiles(join(folder, "inner"), [32], 2);

  await openContainer(page, info.project.name, container);
  await page.getByRole("button", { name: "Upload…" }).click();
  const upload = page.getByRole("dialog", { name: "Upload" });
  await control.pickFolder(folder);
  await upload.getByRole("button", { name: "Choose folder…" }).click();
  await upload.getByRole("button", { name: "Start upload" }).click();
  await finish(page, upload);

  await page.getByRole("button", { name: "Refresh listing" }).click();
  await openFolder(page, folder);
  const inner = page.getByRole("listbox", { name: "Objects" }).getByRole("option", { name: /^inner\/ / });
  await expect(inner).toBeVisible();

  // Selecting a row changes what the toolbar's buttons read, and adds the
  // row's own deletion once the row has been read. Neither may move the
  // buttons or the list under them (found by hand: the buttons shuffled and
  // the list jumped on every click).
  const actions = page.getByRole("group", { name: "Actions" });
  const steady = [
    page.getByRole("listbox", { name: "Objects" }),
    actions.getByRole("button", { name: "Upload…" }),
    page.getByRole("button", { name: "Count objects" }),
    // The deletions' slot: what is in it follows the selection, its place does not.
    page.locator(".deletions .steady.bulk"),
  ];
  const places = () => Promise.all(steady.map((part) => part.boundingBox()));
  const before = await places();
  // The count beside the refresh counts the one folder selected, and its
  // answer sits on the path's row, so it moves nothing either (ADR 0123).
  await inner.click();
  const count = page.getByRole("button", { name: "Count objects" });
  await expect(count).toHaveAttribute("title", new RegExp(`^Count the objects in ${basename(folder)}/inner/:`));
  await count.click();
  await expect(page.getByRole("status").filter({ hasText: /^\d[\d,]* objects? in / })).toHaveText(`1 object in ${basename(folder)}/inner/`);
  expect(await places(), "with a count shown").toEqual(before);
  await objectRow(page, "file00.bin").click();
  await expect(actions.getByRole("button", { name: "Copy object…" })).toBeVisible();
  await expect(actions.getByRole("button", { name: "Delete object…" })).toBeVisible();
  // With a row selected only its own deletion is offered (ADR 0125 decision 7).
  await expect(actions.getByRole("button", { name: "Delete prefix…" })).toHaveCount(0);
  expect(await places(), "with one object selected and read").toEqual(before);
  await inner.click({ modifiers: ["ControlOrMeta"] });
  await expect(actions.getByRole("button", { name: "Delete selected…" })).toBeVisible();
  expect(await places(), "with two rows selected").toEqual(before);

  await page.getByRole("button", { name: "Copy selected…" }).click();
  const copy = page.getByRole("dialog", { name: "Transfer" });
  await expect(copy.getByRole("group", { name: "Source scope" })).toContainText("1 object and 1 folder selected: file00.bin, inner/");
  await copy.getByLabel("Destination container").fill(container);
  await copy.getByLabel("Destination prefix").fill("copied/");
  await copy.getByRole("button", { name: "Preview" }).click();
  await copy.getByRole("button", { name: "Start copy" }).click();
  await finish(page, copy);

  // The download takes the same selection into a local folder.
  await page.getByRole("button", { name: "Download selected…" }).click();
  const download = page.getByRole("dialog", { name: "Download" });
  await expect(download.getByRole("heading", { name: /Download 1 object and 1 folder from / })).toBeVisible();
  const into = localFolder();
  await control.pickFolder(into);
  await download.getByRole("button", { name: "Choose directory…" }).click();
  await download.getByRole("button", { name: "Start download" }).click();
  await finish(page, download);
  for (const [name, digest] of top) expect(digestOf(join(into, name)), name).toBe(digest);
  for (const [name, digest] of nested) expect(digestOf(join(into, "inner", name)), `inner/${name}`).toBe(digest);

  // Neighbours the deletion below must leave: a name that starts as the
  // selected object's does, a folder whose name starts as the selected
  // folder's does, and an object at the container's top. With the originals
  // under the uploaded folder, they tell a deletion of the selection from
  // one of a prefix or of the whole container.
  const neighbours = ["copied/file00.bin.old", "copied/innerx/file00.bin", "top.bin"];
  for (const name of neighbours) await control.put(info.project.name, container, name, "a neighbour that stays");

  // The copy kept the folder's name beside the file.
  await page.getByRole("button", { name: "Up one level" }).click();
  await page.getByRole("button", { name: "Refresh listing" }).click();
  await openFolder(page, "copied");
  await expect(objectRow(page, "file00.bin")).toBeVisible();
  await openFolder(page, "inner");
  await expect(objectRow(page, "file00.bin")).toBeVisible();

  // ADR 0093 decisions 8 and 9: the same selection of the copy's file and
  // folder is deleted as one job, the folder with everything in it, confirmed
  // by typing the word delete and the number of selected rows.
  await page.getByRole("button", { name: "Up one level" }).click();
  await objectRow(page, "file00.bin").click();
  await page.getByRole("listbox", { name: "Objects" }).getByRole("option", { name: /^inner\/ / }).click({ modifiers: ["ControlOrMeta"] });
  await page.getByRole("button", { name: "Delete selected…" }).click();
  const deletion = page.getByRole("alertdialog", { name: "Delete selected" });
  await expect(deletion.getByRole("heading", { name: "Delete 1 object and 1 folder" })).toBeVisible();
  await expect(deletion.getByText(/inner\/: 1 object, 32 B now/)).toBeVisible();
  const confirm = deletion.getByLabel(/to confirm/);
  await expect(confirm).toHaveAttribute("placeholder", "delete 2");
  await confirm.fill("delete 2");
  await deletion.getByRole("button", { name: "Delete 1 object and 1 folder" }).click();
  await finishSelectedDelete(page, deletion);
  const originals = [...top.keys()].map((name) => `${basename(folder)}/${name}`)
    .concat([...nested.keys()].map((name) => `${basename(folder)}/inner/${name}`));
  await expect.poll(async () => (await control.names(info.project.name, container)).sort(), lag)
    .toEqual([...originals, ...neighbours].sort());
});

// ADR 0093 decision 1: with a filter typed, Ctrl+A takes only the rows the
// filter shows, and the Delete key opens their deletion, which counts only
// those. Escape closes it with nothing deleted; carried through, it deletes
// exactly the rows that were shown.
test("delete the rows a filter shows, selected with Ctrl+A and the Delete key", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-filtered-delete");
  const folder = localFolder();
  const kept = ["keep-a.bin", "keep-b.bin"];
  const shown = ["drop-1.bin", "drop-2.bin", "drop-3.bin"];
  for (const name of [...kept, ...shown]) writeFileSync(join(folder, name), name);
  await openContainer(page, target, container);
  await uploadFolder(page, folder);
  const everything = [...kept, ...shown].map((name) => `${basename(folder)}/${name}`).sort();

  await page.getByRole("button", { name: "Refresh listing" }).click();
  await openFolder(page, folder);
  await page.getByPlaceholder("filter this list…").fill("drop");
  const rows = page.getByRole("listbox", { name: "Objects" }).getByRole("option");
  await expect(rows).toHaveCount(shown.length);
  // Ctrl+A is the list's only while no field has the focus, as for a person
  // who clicks a row after typing the filter.
  const selectShownAndPressDelete = async () => {
    await objectRow(page, shown[0]).click();
    await page.keyboard.press("ControlOrMeta+a");
    await expect(page.getByRole("listbox", { name: "Objects" }).getByRole("option", { selected: true })).toHaveCount(shown.length);
    await page.keyboard.press("Delete");
  };
  await selectShownAndPressDelete();
  const deletion = page.getByRole("alertdialog", { name: "Delete selected" });
  await expect(deletion.getByRole("heading", { name: `Delete ${shown.length} objects` })).toBeVisible();
  await expect(deletion.getByRole("list", { name: "Selected objects" }).getByRole("listitem"))
    .toHaveText(shown.map((name) => `${basename(folder)}/${name}`));
  const confirm = deletion.getByLabel(/to confirm/);
  await expect(confirm).toHaveAttribute("placeholder", `delete ${shown.length}`);
  await page.keyboard.press("Escape");
  await expect(deletion).toHaveCount(0);
  expect((await control.names(target, container)).sort(), "after Escape").toEqual(everything);

  await selectShownAndPressDelete();
  await confirm.fill(`delete ${shown.length}`);
  await deletion.getByRole("button", { name: `Delete ${shown.length} objects` }).click();
  await finishSelectedDelete(page, deletion);
  await expect.poll(async () => (await control.names(target, container)).sort(), lag)
    .toEqual(kept.map((name) => `${basename(folder)}/${name}`));
});

// ADR 0119 and 0120 for a selection: a large object and a plain file
// selected together and deleted with the segments option lose that large
// object's segments and nothing else; another large object beside them
// keeps its own.
test("delete a selected large object with its segments, and keep another's", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-selected-segments", true);
  const sidecar = `${container}_segments`;
  const folder = localFolder();
  // A plain file and two of three chunks each at the upload's chunk size.
  writeFiles(folder, [16, 3 << 20, 3 << 20], 1);
  await openContainer(page, target, container);
  await uploadFolder(page, folder, await openUpload(page));
  const f = basename(folder);
  await expect.poll(async () => (await control.names(target, sidecar)).length, lag).toBe(6);
  const segments = await control.names(target, sidecar);
  // Each large object's segments are filed under its own name.
  const deletedSegments = segments.filter((name) => name.startsWith(`${f}/file01.bin/`));
  expect(deletedSegments, "the first large object's segments").toHaveLength(3);

  await page.getByRole("button", { name: "Refresh listing" }).click();
  await openFolder(page, folder);
  await objectRow(page, "file00.bin").click();
  await objectRow(page, "file01.bin").click({ modifiers: ["ControlOrMeta"] });
  await page.getByRole("button", { name: "Delete selected…" }).click();
  const deletion = page.getByRole("alertdialog", { name: "Delete selected" });
  await expect(deletion.getByRole("heading", { name: "Delete 2 objects" })).toBeVisible();
  // With no folder selected nothing reads whether this container is another's
  // archive, so the option starts unticked and says why (ADR 0120 decision 2).
  const option = deletion.getByRole("checkbox", { name: "Also delete the segments of large objects" });
  await expect(option).not.toBeChecked();
  await expect(deletion.getByText(/^It starts unticked here: whether this container keeps another container's versions was not read/)).toBeVisible();
  await option.check();
  const confirm = deletion.getByLabel(/to confirm/);
  await expect(confirm).toHaveAttribute("placeholder", "delete 2 and segments");
  await confirm.fill("delete 2 and segments");
  await deletion.getByRole("button", { name: "Delete 2 objects" }).click();
  await finishSelectedDelete(page, deletion);

  await expect.poll(() => control.names(target, container), lag).toEqual([`${f}/file02.bin`]);
  await expect.poll(async () => (await control.names(target, sidecar)).sort(), lag)
    .toEqual(segments.filter((name) => !deletedSegments.includes(name)).sort());
});

// finishSelectedDelete follows a started selected-set deletion to its end in
// the drawer and requires that it completed. Its dialog names the job in a
// sentence of its own, not as the other dialogs do.
async function finishSelectedDelete(page: Page, deletion: Locator): Promise<void> {
  const started = await deletion.getByText(/is job \S+\./).textContent();
  const id = /is job (\S+)\./.exec(started!)![1];
  await deletion.getByRole("button", { name: "Show in jobs" }).click();
  const row = page.locator("div.job", { has: page.getByRole("button", { name: `Item details for job ${id}`, exact: true }) });
  await expect(row.getByText(/^(complete|partial|cancelled|failed)$/)).toHaveText("complete", { timeout: 8 * 60 * 1000 });
  await page.getByRole("button", { name: /▾ hide/ }).click();
}

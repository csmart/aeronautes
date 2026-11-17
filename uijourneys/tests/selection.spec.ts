import { test } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { control } from "../control.ts";
import { digestOf, expect, finish, localFolder, objectRow, openContainer, openFolder, ownContainer, writeFiles } from "../helpers.ts";

// A folder is uploaded with a file and a folder inside it, so that one
// listing shows both. Selecting rows there must leave the toolbar and the
// list where they were. Then ADR 0108 decision 7: a selected folder is taken
// with everything in it, so a selection of the file and the folder is copied
// and downloaded whole, the folder's name kept, and nothing opens first.
test("a selection of a file and a folder is copied and downloaded with the folder's contents", async ({ page }, info) => {
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
    actions.getByRole("button", { name: "Count objects" }),
    actions.getByRole("button", { name: "Delete prefix…" }),
  ];
  const places = () => Promise.all(steady.map((part) => part.boundingBox()));
  const before = await places();
  await objectRow(page, "file00.bin").click();
  await expect(actions.getByRole("button", { name: "Copy object…" })).toBeVisible();
  await expect(actions.getByRole("button", { name: "Delete object…" })).toBeVisible();
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

  // The copy kept the folder's name beside the file.
  await page.getByRole("button", { name: "Up one level" }).click();
  await page.getByRole("button", { name: "Refresh listing" }).click();
  await openFolder(page, "copied");
  await expect(objectRow(page, "file00.bin")).toBeVisible();
  await openFolder(page, "inner");
  await expect(objectRow(page, "file00.bin")).toBeVisible();
});

import { test, type Locator, type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { basename, join } from "node:path";
import { control } from "../control.ts";
import {
  account, connect, digestOf, expect, finish, inspector, jobIds, lag, localFolder, objectRow, openContainer, openFolder, openUpload, ownContainer, uploadFolder, writeFiles,
} from "../helpers.ts";

// Copies run through the window between the run's own containers. What a
// copy wrote is read by the run's own session (control.names), by
// downloading it and comparing digests, or after connecting again.

// Two small files and one three chunks long at the 1 MiB chunk size, so it
// uploads, and copies, in segments.
const sizes = [1024, 64 * 1024, 3 << 20];

async function uploadFiles(page: Page, files: string[]): Promise<void> {
  const dialog = await openUpload(page);
  await control.pickFiles(files);
  await dialog.getByRole("button", { name: "Choose files…" }).click();
  await dialog.getByRole("button", { name: "Start upload" }).click();
  await finish(page, dialog);
}

// openCopy opens the copy dialog from the toolbar button of that name and
// fills the destination; the chunk size is the upload's, so a large object
// is written in segments at the destination too.
async function openCopy(page: Page, button: string, destContainer: string, destPrefix?: string): Promise<Locator> {
  await page.getByRole("button", { name: button }).click();
  const dialog = page.getByRole("dialog", { name: "Transfer" });
  await dialog.getByText("Per-job settings").click();
  await dialog.getByLabel("Chunk size").fill("1Mi");
  await dialog.getByLabel("Destination container").fill(destContainer);
  if (destPrefix !== undefined) await dialog.getByLabel("Destination prefix").fill(destPrefix);
  return dialog;
}

function count(dialog: Locator, label: string): Locator {
  return dialog.locator(".counts .count").filter({ hasText: new RegExp(`^\\s*\\d+\\s+${label}\\s*$`) });
}

test.describe("copy", () => {
  // 0.1.0 steps 7a and 7b: a copy creates every object with its bytes and
  // its custom metadata, and the same copy run again copies nothing.
  test("copy a container with a large object and its metadata, then again", async ({ page }, info) => {
    const target = info.project.name;
    const source = await ownContainer(target, "ui-copy-from", true);
    const dest = await ownContainer(target, "ui-copy-to", true);
    const folder = localFolder();
    const digests = writeFiles(folder, sizes, 1);
    await openContainer(page, target, source);
    await uploadFiles(page, [...digests.keys()].map((name) => join(folder, name)));

    await page.getByRole("button", { name: "Refresh listing" }).click();
    await objectRow(page, "file00.bin").click();
    await page.getByRole("button", { name: "Edit metadata" }).click();
    await page.getByRole("button", { name: "+ add key" }).click();
    await page.getByLabel("metadata key").last().fill("journey");
    await page.getByLabel("metadata value").last().fill("copied-with-it");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("button", { name: "Edit metadata" })).toBeVisible();

    await openContainer(page, target, source);
    let copy = await openCopy(page, "Copy container…", dest);
    await copy.getByRole("button", { name: "Preview" }).click();
    await expect(count(copy, "new")).toHaveText(/^\s*3\s+new/);
    await copy.getByRole("button", { name: "Start copy" }).click();
    await finish(page, copy);
    await expect.poll(async () => (await control.names(target, dest)).sort(), lag).toEqual([...digests.keys()].sort());

    const into = localFolder();
    await openContainer(page, target, dest);
    await page.getByRole("button", { name: "Download container…" }).click();
    const download = page.getByRole("dialog", { name: "Download" });
    await control.pickFolder(into);
    await download.getByRole("button", { name: "Choose directory…" }).click();
    await download.getByRole("button", { name: "Start download" }).click();
    await finish(page, download);
    for (const [name, digest] of digests) expect(digestOf(join(into, name)), name).toBe(digest);

    // Read on a fresh page, from the store, not from the page's memory.
    await openContainer(page, target, dest);
    await objectRow(page, "file00.bin").click();
    const metadata = inspector(page).locator("section", { has: page.getByRole("heading", { name: "User metadata" }) });
    await expect(metadata).toContainText("Journey");
    await expect(metadata).toContainText("copied-with-it");

    // The same copy again: nothing is new, and the job copies nothing.
    await openContainer(page, target, source);
    copy = await openCopy(page, "Copy container…", dest);
    await copy.getByRole("button", { name: "Preview" }).click();
    await expect(count(copy, "new")).toHaveText(/^\s*0\s+new/);
    await copy.getByRole("button", { name: "Start copy" }).click();
    const details = await finish(page, copy);
    expect(details).toMatch(/\b0(\/\d+)? obj · 0 B · 3 already the same\b/);
    await expect.poll(async () => (await control.names(target, dest)).sort(), lag).toEqual([...digests.keys()].sort());
  });

  // Test plan steps 18h and 18e: a folder copied up to the container's top
  // with the destination path cleared lands beside itself, and a copy whose
  // destination is inside its source is refused in plain words.
  test("copy a folder up to the top, and refuse a copy into its own source", async ({ page }, info) => {
    const target = info.project.name;
    const container = await ownContainer(target, "ui-copy-up");
    const folder = localFolder();
    const files = [...writeFiles(folder, [16, 32], 1).keys()];
    const name = basename(folder);
    await openContainer(page, target, container);
    await uploadFolder(page, folder, await openUpload(page));

    await page.getByRole("button", { name: "Refresh listing" }).click();
    await page.getByRole("option", { name: new RegExp(`^${name}/ `) }).click();
    await page.getByRole("button", { name: "Copy folder…" }).click();
    const copy = page.getByRole("dialog", { name: "Transfer" });
    await expect(copy.getByLabel("Destination prefix")).toHaveValue(`${name}/`);
    await copy.getByLabel("Destination container").fill(container);
    await copy.getByLabel("Destination prefix").fill("");
    await copy.getByRole("button", { name: "Preview" }).click();
    await expect(count(copy, "new")).toHaveText(/^\s*2\s+new/);
    await copy.getByRole("button", { name: "Start copy" }).click();
    await finish(page, copy);
    // The files are at the top, the folder still holds the originals, and
    // nothing else is there: no marker or empty name at the top.
    await expect.poll(async () => (await control.names(target, container)).sort(), lag)
      .toEqual([...files, ...files.map((file) => `${name}/${file}`)].sort());

    // The whole container copied into one of its folders, or into a new one,
    // would read what it writes.
    const overlap = (prefix: string) =>
      `the source and destination overlap: the destination, prefix "${prefix}" of container "${container}", is inside the source, ` +
      `the whole of container "${container}", in the same account, so the copy could copy its own output; ` +
      "choose another container, or a destination folder outside the source folder";
    await openContainer(page, target, container);
    for (const prefix of [`${name}/`, "inner/"]) {
      const top = await openCopy(page, "Copy container…", container, prefix);
      await top.getByRole("button", { name: "Preview" }).click();
      await expect(top.getByRole("alert")).toHaveText(overlap(prefix));
      await expect(top.getByRole("button", { name: "Start copy" })).toBeDisabled();
      await top.getByRole("button", { name: "Close" }).first().click();
    }
  });

  // Test plan step 19r: two folders selected alone copy whole into a new
  // folder; a folder and a file copied into that folder itself are refused
  // before anything starts.
  test("copy two selected folders, and refuse a selection copied into itself", async ({ page }, info) => {
    const target = info.project.name;
    const container = await ownContainer(target, "ui-copy-folders");
    const folder = localFolder();
    mkdirSync(join(folder, "alpha"));
    mkdirSync(join(folder, "beta"));
    writeFiles(join(folder, "alpha"), [16], 1);
    writeFiles(join(folder, "beta"), [32], 2);
    writeFiles(folder, [48], 3);
    const name = basename(folder);
    await openContainer(page, target, container);
    await uploadFolder(page, folder, await openUpload(page));
    await page.getByRole("button", { name: "Refresh listing" }).click();
    await openFolder(page, folder);

    const list = page.getByRole("listbox", { name: "Objects" });
    const alpha = list.getByRole("option", { name: /^alpha\/ / });
    const beta = list.getByRole("option", { name: /^beta\/ / });
    await alpha.click();
    await beta.click({ modifiers: ["ControlOrMeta"] });
    await page.getByRole("button", { name: "Copy selected…" }).click();
    const copy = page.getByRole("dialog", { name: "Transfer" });
    await expect(copy.getByRole("group", { name: "Source scope" })).toContainText("2 folders selected: alpha/, beta/");
    await copy.getByLabel("Destination container").fill(container);
    await copy.getByLabel("Destination prefix").fill("both/");
    await copy.getByRole("button", { name: "Preview" }).click();
    await copy.getByRole("button", { name: "Start copy" }).click();
    await finish(page, copy);
    await expect.poll(async () => (await control.names(target, container)).filter((object) => object.startsWith("both/")).sort(), lag)
      .toEqual(["both/alpha/file00.bin", "both/beta/file00.bin"]);

    // The file is picked first and the folder second, so a refusal that
    // named the first picked rather than the first listed would show.
    const rows = await list.getByRole("option").allInnerTexts();
    const first = rows.findIndex((row) => row.startsWith("alpha/")) < rows.findIndex((row) => row.startsWith("file00.bin"))
      ? `${name}/alpha/alpha/`
      : `${name}/alpha/file00.bin`;
    const jobsBefore = await jobIds(page);
    await objectRow(page, "file00.bin").click();
    await alpha.click({ modifiers: ["ControlOrMeta"] });
    await page.getByRole("button", { name: "Copy selected…" }).click();
    await copy.getByLabel("Destination container").fill(container);
    await copy.getByLabel("Destination prefix").fill(`${name}/alpha/`);
    await copy.getByRole("button", { name: "Preview" }).click();
    await expect(copy.getByRole("alert")).toHaveText(
      `the copy would write "${first}" inside the selected folder "${name}/alpha/", so it could replace an object the copy still reads; ` +
      "copy to another folder or container");
    await expect(copy.getByRole("button", { name: "Start copy" })).toBeDisabled();
    await copy.getByRole("button", { name: "Close" }).first().click();
    expect(await jobIds(page), "no job started").toEqual(jobsBefore);
  });

  // Test plan step 19g: the destination field suggests the account's
  // containers, and a missing one can be created from the preview. A
  // container the window creates is not one the run's ownership record
  // knows, so teardown cannot remove it; on the mock, which the run starts
  // empty of everything it makes, that is harmless, so this runs there only.
  test("suggest destination containers, and create a missing one", async ({ page }, info) => {
    test.skip(info.project.name !== "mock", "the window creates a container teardown does not know, so the mock only");
    const target = info.project.name;
    const source = await ownContainer(target, "ui-copy-suggest");
    const other = await ownContainer(target, "ui-copy-suggested");
    const bookmark = await account(target);
    const missing = `${other}-made-by-the-window`;
    await openContainer(page, target, source);
    const copy = await openCopy(page, "Copy container…", "");
    await expect(copy.locator(`datalist option[value="${other}"]`)).toHaveCount(1);
    await copy.getByLabel("Destination container").fill(missing);
    await copy.getByRole("button", { name: "Preview" }).click();
    await expect(copy.getByRole("status").filter({ hasText: "does not exist on" })).toContainText(
      `The container ${missing} does not exist on ${bookmark}.`);
    await expect(copy.getByRole("button", { name: "Start copy" })).toBeDisabled();
    await copy.getByRole("button", { name: `Create container ${missing} on ${bookmark}` }).click();
    await expect(count(copy, "new")).toBeVisible();
    await expect(copy.getByRole("button", { name: "Start copy" })).toBeEnabled();
    await copy.getByRole("button", { name: "Close" }).first().click();

    // A fresh connection lists it.
    await connect(page, target);
    await page.getByLabel("Filter containers").fill(missing);
    await expect(page.getByRole("button", { name: new RegExp(`^${missing} `) })).toBeVisible();
  });
});

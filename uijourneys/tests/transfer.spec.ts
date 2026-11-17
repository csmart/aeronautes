import { test } from "@playwright/test";
import { basename, join } from "node:path";
import { control } from "../control.ts";
import {
  armDelete, connect, digestOf, expect, expectInside, finish, inspector, localFolder, objectRow, openContainer, openFolder, openUpload,
  ownContainer, typedDelete, writeFiles,
} from "../helpers.ts";

// One container per target carries a folder through upload, replacement,
// download, edits and deletion, so each step starts from what the one
// before it left. The third file is three chunks long, so it uploads in
// segments.
const sizes = [1024, 64 * 1024, 3 << 20];
const segmented = "file02.bin";
// A pattern matches the raw text, where the sentence can break across lines.
const disclosure = (kind: string) =>
  new RegExp(`^Files larger than the chunk size upload in segments as ${kind} large objects`.replaceAll(" ", "\\s+"));
const inspectorKind = { static: "Static Large Object (SLO)", dynamic: "Dynamic Large Object (DLO)" };

test.describe.serial("transfer", () => {
  let container = "";
  let folder = "";
  let digests = new Map<string, string>();
  let published: "static" | "dynamic" = "static";
  // Where the folder is downloaded, so a second download can find it there.
  let into = "";

  test.beforeAll(async ({}, info) => {
    container = await ownContainer(info.project.name, "ui-transfer", true);
    folder = localFolder();
  });

  test("upload a folder with a segmented file and browse it", async ({ page }, info) => {
    digests = writeFiles(folder, sizes, 1);
    await openContainer(page, info.project.name, container);
    const dialog = await openUpload(page);
    await control.pickFolder(folder);
    await dialog.getByRole("button", { name: "Choose folder…" }).click();
    // ADR 0111 decision 10: a store that does not return static manifests
    // gets dynamic ones. A connection that has not yet learned which kind of
    // store it is previews static, and the job records the kind it
    // published, so the object must be the kind the job recorded.
    const previewed = /\sstatic\s/.test((await dialog.getByText(disclosure("(static|dynamic)")).textContent())!) ? "static" : "dynamic";
    await dialog.getByRole("button", { name: "Start upload" }).click();
    const recorded = /largeObjects\s*(static|dynamic)/.exec(await finish(page, dialog))?.[1];
    expect(recorded, "the job's recorded large-object kind").toMatch(/^(static|dynamic)$/);
    published = recorded as "static" | "dynamic";
    if (previewed === "dynamic") {
      expect(published, "a dynamic preview publishes dynamic").toBe("dynamic");
    }

    await page.getByRole("button", { name: "Refresh listing" }).click();
    await openFolder(page, folder);
    for (const name of digests.keys()) {
      await expect(objectRow(page, name)).toBeVisible();
    }
    await objectRow(page, segmented).click();
    await expect(inspector(page).getByText(inspectorKind[published], { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Up one level" }).click();
    const folderRow = page.getByRole("option", { name: new RegExp(`^${basename(folder)}/ `) });
    await expect(folderRow).toBeVisible();

    // A folder copies as a folder: for the store's own folder row the copy
    // dialog starts the destination path as the folder's name. Nothing is
    // sent; the dialog is closed unsent.
    await folderRow.click();
    await page.getByRole("button", { name: "Copy folder…" }).click();
    const copy = page.getByRole("dialog", { name: "Transfer" });
    await expect(copy.getByLabel("Destination prefix")).toHaveValue(`${basename(folder)}/`);
    await copy.getByRole("button", { name: "Close" }).first().click();
  });

  test("upload it again and confirm the overwrite", async ({ page }, info) => {
    digests = writeFiles(folder, sizes, 2);
    await openContainer(page, info.project.name, container);
    const dialog = await openUpload(page);
    await control.pickFolder(folder);
    await dialog.getByRole("button", { name: "Choose folder…" }).click();
    await dialog.getByRole("button", { name: `Replace ${sizes.length} existing objects…` }).click();
    await expect(dialog.getByText(new RegExp(`This upload may REPLACE what is at ${sizes.length} names, each only where it differs from your file\\.`.replaceAll(" ", "\\s+")))).toBeVisible();
    // The connection has learned what the store returns, so this preview
    // names the kind the first upload published.
    await expect(dialog.getByText(disclosure(published))).toBeVisible();
    await control.answer("overwrite", true);
    await dialog.getByRole("button", { name: "Start upload" }).click();
    await finish(page, dialog);
  });

  test("download it back", async ({ page }, info) => {
    into = localFolder();
    await openContainer(page, info.project.name, container);
    await page.getByRole("button", { name: "Download container…" }).click();
    const dialog = page.getByRole("dialog", { name: "Download" });
    await control.pickFolder(into);
    await dialog.getByRole("button", { name: "Choose directory…" }).click();
    await dialog.getByRole("button", { name: "Start download" }).click();
    await finish(page, dialog);
    // The second round's bytes coming back proves the replacement wrote.
    for (const [name, digest] of digests) {
      expect(digestOf(join(into, basename(folder), name)), name).toBe(digest);
    }
  });

  // ADR 0122 decision 12: a download into the same folder is refused until
  // replacing is confirmed, and then the engine compares each file as rclone
  // copy does. Every file is what the last download wrote, size and time, so
  // nothing is replaced and the job says each is already the same.
  test("download it again over the same files and confirm the replacement", async ({ page }, info) => {
    await openContainer(page, info.project.name, container);
    await page.getByRole("button", { name: "Download container…" }).click();
    const dialog = page.getByRole("dialog", { name: "Download" });
    await control.pickFolder(into);
    await dialog.getByRole("button", { name: "Choose directory…" }).click();
    await dialog.getByRole("button", { name: "Replace existing files…" }).click();
    await expect(dialog.getByText(new RegExp(`This download may REPLACE ${sizes.length} files in the folder`.replaceAll(" ", "\\s+")))).toBeVisible();
    await control.answer("overwrite", true);
    await dialog.getByRole("button", { name: "Start download" }).click();
    const details = await finish(page, dialog);
    expect(details).toContain(`${sizes.length} already the same`);
    for (const [name, digest] of digests) {
      expect(digestOf(join(into, basename(folder), name)), name).toBe(digest);
    }
  });

  test("edit an object's metadata", async ({ page }, info) => {
    await openContainer(page, info.project.name, container);
    await openFolder(page, folder);
    await objectRow(page, "file00.bin").click();
    await page.getByRole("button", { name: "Edit metadata" }).click();
    // An uploaded object already carries one key, its file's modification
    // time, so the key added is the last row.
    await page.getByRole("button", { name: "+ add key" }).click();
    await page.getByLabel("metadata key").last().fill("journey");
    await page.getByLabel("metadata value").last().fill("round-2");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("button", { name: "Edit metadata" })).toBeVisible();

    // Read back from the store after a reload, not from the page's memory.
    await openContainer(page, info.project.name, container);
    await openFolder(page, folder);
    await objectRow(page, "file00.bin").click();
    const metadata = inspector(page).locator("section", { has: page.getByRole("heading", { name: "User metadata" }) });
    await expect(metadata).toContainText("Journey");
    await expect(metadata).toContainText("round-2");
    await expect(metadata).toContainText("Mtime");
  });

  test("make the container public and private again", async ({ page }, info) => {
    const everyone = "Anyone with the URL can read objects in this container";
    await openContainer(page, info.project.name, container);
    await page.getByRole("button", { name: "Configure container" }).click();
    await page.getByLabel("Anyone can read objects").check();
    await page.getByLabel(`I understand this makes ${container} publicly readable.`).check();
    await control.answer("public", true);
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("button", { name: "Configure container" })).toBeVisible();
    await openContainer(page, info.project.name, container);
    await expect(inspector(page).getByText(everyone)).toBeVisible();

    await page.getByRole("button", { name: "Configure container" }).click();
    await page.getByLabel("Anyone can read objects").uncheck();
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("button", { name: "Configure container" })).toBeVisible();
    await openContainer(page, info.project.name, container);
    await expect(inspector(page).getByText("Read ACL", { exact: true })).toBeVisible();
    await expect(inspector(page).getByText(everyone)).toHaveCount(0);
  });

  test("delete an object, the prefix and the container", async ({ page }, info) => {
    await openContainer(page, info.project.name, container);
    await openFolder(page, folder);
    await objectRow(page, "file00.bin").click();
    await inspector(page).getByRole("button", { name: "Delete object…" }).click();
    await page.getByRole("alertdialog", { name: "Delete object" }).getByRole("button", { name: "Delete object" }).click();
    await expect(objectRow(page, "file00.bin")).toHaveCount(0);

    // The folder still holds the segmented file. The option that deletes its
    // segments after its manifest starts ticked (ADR 0120), and the words to
    // type name it.
    await page.getByRole("button", { name: "Delete prefix…" }).click();
    await typedDelete(page, "Delete prefix", "ticked");
    await openContainer(page, info.project.name, container);
    await expect(page.getByRole("listbox", { name: "Objects" }).getByRole("option")).toHaveCount(0);

    // The segments were in the container named after this one, which still
    // exists. The dialog says a container of that name exists, and once the
    // open container is deleted it stays to offer the way there. This run
    // knows that container was only its own upload's, so it deletes it
    // afterwards.
    const segments = `${container}_segments`;
    await page.getByRole("button", { name: "Delete container…" }).click();
    const dialog = page.getByRole("dialog", { name: "Delete" });
    await expect(dialog.getByText(`A container named ${segments} exists`)).toBeVisible();
    await armDelete(page, "Delete container");
    await expect(dialog.getByRole("heading", { name: "Deletion complete" })).toBeVisible({ timeout: 8 * 60 * 1000 });
    await expect(dialog.getByText(`${segments} still exists`)).toBeVisible();
    await expectInside(dialog);
    await dialog.getByRole("button", { name: "Open that container" }).click();
    await expect(page.getByRole("navigation", { name: "Path" }).getByRole("button", { name: segments })).toBeVisible();

    // The deletion's preview lists that container afresh and counts what it
    // holds: nothing, because the prefix deletion took the upload's
    // segments. A listing can lag the deletes on a real store, so the
    // preview is asked again within a bound; a listing still loading in the
    // window would say nothing either way.
    await page.getByRole("button", { name: "Delete container…" }).click();
    await expect(async () => {
      await dialog.getByRole("button", { name: "Preview" }).click();
      await expect(dialog.getByText(/^0 objects · /)).toBeVisible({ timeout: 5000 });
    }).toPass({ timeout: 60_000 });
    await typedDelete(page, "Delete container");
    await connect(page, info.project.name);
    await page.getByRole("tab", { name: "All" }).click();
    await expect(page.getByRole("button", { name: new RegExp(`^${container}`) })).toHaveCount(0);
  });
});

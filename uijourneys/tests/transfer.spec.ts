import { test, type Page } from "@playwright/test";
import { basename, join } from "node:path";
import { control } from "../control.ts";
import { connect, digestOf, expect, finish, localFolder, openContainer, ownContainer, writeFiles } from "../helpers.ts";

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
    await expect(page.getByRole("option", { name: new RegExp(`^${basename(folder)}/ `) })).toBeVisible();
  });

  test("upload it again and confirm the overwrite", async ({ page }, info) => {
    digests = writeFiles(folder, sizes, 2);
    await openContainer(page, info.project.name, container);
    const dialog = await openUpload(page);
    await control.pickFolder(folder);
    await dialog.getByRole("button", { name: "Choose folder…" }).click();
    await dialog.getByRole("button", { name: `Replace ${sizes.length} existing objects…` }).click();
    await expect(dialog.getByText(`This upload will REPLACE what is at ${sizes.length} names.`)).toBeVisible();
    // The connection has learned what the store returns, so this preview
    // names the kind the first upload published.
    await expect(dialog.getByText(disclosure(published))).toBeVisible();
    await control.answer("overwrite", true);
    await dialog.getByRole("button", { name: "Start upload" }).click();
    await finish(page, dialog);
  });

  test("download it back", async ({ page }, info) => {
    const into = localFolder();
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

  test("edit an object's metadata", async ({ page }, info) => {
    await openContainer(page, info.project.name, container);
    await openFolder(page, folder);
    await objectRow(page, "file00.bin").click();
    await page.getByRole("button", { name: "Edit metadata" }).click();
    await page.getByRole("button", { name: "+ add key" }).click();
    await page.getByLabel("metadata key").fill("journey");
    await page.getByLabel("metadata value").fill("round-2");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("button", { name: "Edit metadata" })).toBeVisible();

    // Read back from the store after a reload, not from the page's memory.
    await openContainer(page, info.project.name, container);
    await openFolder(page, folder);
    await objectRow(page, "file00.bin").click();
    const metadata = inspector(page).locator("section", { has: page.getByRole("heading", { name: "User metadata" }) });
    await expect(metadata).toContainText("Journey");
    await expect(metadata).toContainText("round-2");
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

    await page.getByRole("button", { name: "Delete prefix…" }).click();
    await typedDelete(page, "Delete prefix");
    await openContainer(page, info.project.name, container);
    await expect(page.getByRole("listbox", { name: "Objects" }).getByRole("option")).toHaveCount(0);

    await page.getByRole("button", { name: "Empty or delete container…" }).click();
    await page.getByRole("dialog", { name: "Delete" }).getByRole("radio", { name: /^Delete container/ }).check();
    await typedDelete(page, "Delete container");
    await connect(page, info.project.name);
    await expect(page.getByRole("button", { name: new RegExp(`^${container} `) })).toHaveCount(0);
  });
});

// openUpload opens the upload dialog with a chunk size small enough that
// the largest file is segmented.
async function openUpload(page: Page) {
  await page.getByRole("button", { name: "Upload…" }).click();
  const dialog = page.getByRole("dialog", { name: "Upload" });
  await dialog.getByText("Per-job settings").click();
  await dialog.getByLabel("Chunk size").fill("1Mi");
  return dialog;
}

async function openFolder(page: Page, folder: string) {
  await page.getByRole("listbox", { name: "Objects" }).getByRole("option", { name: new RegExp(`^${basename(folder)}/ `) }).dblclick();
  await expect(page.getByRole("navigation", { name: "Path" }).getByRole("button", { name: basename(folder) })).toBeVisible();
}

function objectRow(page: Page, name: string) {
  return page.getByRole("listbox", { name: "Objects" }).getByRole("option", { name: new RegExp(`^${name} `) });
}

function inspector(page: Page) {
  return page.getByRole("complementary", { name: "Inspector" });
}

// typedDelete previews a deletion, types the word the dialog asks for, as
// the operator reads it there, and waits for the job.
async function typedDelete(page: Page, action: string) {
  const dialog = page.getByRole("dialog", { name: "Delete" });
  await dialog.getByRole("button", { name: "Preview" }).click();
  const confirm = dialog.getByLabel(/to arm/);
  await confirm.fill((await confirm.getAttribute("placeholder"))!);
  await dialog.getByRole("button", { name: action }).click();
  await finish(page, dialog);
}

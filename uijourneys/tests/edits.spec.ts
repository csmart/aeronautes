import { test, type Locator, type Page } from "@playwright/test";
import { join } from "node:path";
import { control } from "../control.ts";
import {
  digestOf, expect, finish, inspector, localFolder, objectRow, openContainer, openFolder, openUpload, ownContainer, sha256, uploadFolder,
  writeFiles,
} from "../helpers.ts";

// Edits made through the inspector, each read back over a fresh connection
// and, where bytes are at stake, by downloading them and comparing digests.

const inspectorKind = { static: "Static Large Object (SLO)", dynamic: "Dynamic Large Object (DLO)" };

// A metadata edit is a POST, which replaces every user key; for a dynamic
// large object it must also carry the manifest header, or the object turns
// into an empty plain one and its bytes are lost (DESIGN §57, DLO
// preservation). Both kinds are edited, each as the job recorded it.
test("edit a large object's metadata, keeping its kind and its bytes", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-edit-large", true);
  for (const [round, choice] of (["slo", "dlo"] as const).entries()) {
    const folder = localFolder();
    const digest = writeFiles(folder, [3 << 20], round + 1).get("file00.bin")!;
    await openContainer(page, target, container);
    const details = await uploadFolder(page, folder, await openUpload(page, choice));
    const kind = /largeObjects\s*(static|dynamic)/.exec(details)?.[1] as "static" | "dynamic";
    expect(kind, "the job's recorded large-object kind").toMatch(/^(static|dynamic)$/);

    await page.getByRole("button", { name: "Refresh listing" }).click();
    await openFolder(page, folder);
    await objectRow(page, "file00.bin").click();
    await addKey(page, "journey", choice);
    await page.getByRole("button", { name: "Save" }).click();
    await expect(inspector(page).getByRole("button", { name: "Edit metadata" })).toBeVisible();

    await openContainer(page, target, container);
    await openFolder(page, folder);
    await objectRow(page, "file00.bin").click();
    await expect(inspector(page).getByText(inspectorKind[kind], { exact: true })).toBeVisible();
    await expect(userMetadata(page)).toContainText(choice);
    expect(await downloadObject(page, "file00.bin"), `the ${choice} object's bytes`).toBe(digest);
  }
});

// ContainerEditPanel sends the whole form on every save, so each save must
// start from what the one before it wrote. The three saves follow one
// another without the page being loaded again, which is where a form built
// from a stale copy of the container would drop the label.
test("each save of a container's settings keeps the others", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-edit-keep");
  const configure = page.getByRole("button", { name: "Configure container" });
  await openContainer(page, target, container);
  await configure.click();
  await page.getByRole("button", { name: "+ add key" }).click();
  await page.getByLabel("metadata key").last().fill("Web-Listings-Label");
  await page.getByLabel("metadata value").last().fill("kept");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(configure).toBeEnabled();

  await configure.click();
  await page.getByLabel("Anyone can read objects").check();
  await page.getByLabel(`I understand this makes ${container} publicly readable.`).check();
  await control.answer("public", true);
  await page.getByRole("button", { name: "Save" }).click();
  await expect(configure).toBeEnabled();
  await expect(inspector(page).getByText("Anyone with the URL can read objects in this container")).toBeVisible();

  await configure.click();
  await page.getByLabel("Anyone can read objects").uncheck();
  await page.getByRole("button", { name: "Save" }).click();
  await expect(configure).toBeEnabled();

  await openContainer(page, target, container);
  await expect(inspector(page).getByText("Anyone with the URL can read objects in this container")).toHaveCount(0);
  await expect(userMetadata(page).locator(".kv", { hasText: "Web-Listings-Label" })).toContainText("kept");
});

// Another program changes the object while its editor is open: the save is
// refused and writes nothing. "Reload theirs" then shows the other
// program's version; in a second round "Apply mine anyway" writes the edit
// over it, onto the other program's bytes. The run's own writes carry no
// metadata, so each one changes what the editor's fingerprint covers.
test("an edit that conflicts with another writer is refused, then applied on purpose", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-edit-conflict");
  const name = "note.txt";
  await control.put(target, container, name, "first");

  for (const [round, resolve] of (["Reload theirs", "Apply mine anyway"] as const).entries()) {
    const theirs = `theirs ${round}`;
    await openObject(page, target, container, name);
    await addKey(page, "journey", "before");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(inspector(page).getByRole("button", { name: "Edit metadata" })).toBeVisible();

    await openObject(page, target, container, name);
    await inspector(page).getByRole("button", { name: "Edit metadata" }).click();
    await page.getByLabel("metadata value").last().fill("mine");
    await control.put(target, container, name, theirs);
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("This object changed since you loaded it. Your edit was not written.")).toBeVisible();
    await page.getByRole("button", { name: resolve }).click();
    await expect(inspector(page).getByRole("button", { name: "Edit metadata" })).toBeVisible();

    await openObject(page, target, container, name);
    // The edit button shows once the object's own HEAD has been read.
    await expect(inspector(page).getByRole("button", { name: "Edit metadata" })).toBeVisible();
    if (resolve === "Reload theirs") {
      // The other program's write carried no keys, so none is listed.
      await expect(userMetadata(page)).toHaveCount(0);
    } else {
      await expect(userMetadata(page).locator(".kv", { hasText: "Journey" })).toContainText("mine");
    }
    expect(await downloadObject(page, name), "the other program's bytes").toBe(sha256(Buffer.from(theirs)));
  }
});

// The native chooser answers a person closing it with nothing chosen and
// no error. The dialog drops what an earlier pick chose as each new pick
// begins (LocalTransferDialog's pickAndPreflight), so after a closed
// chooser there is nothing to start, and nothing is uploaded.
test("closing the folder chooser leaves nothing chosen", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-pick-closed");
  const folder = localFolder();
  writeFiles(folder, [16], 1);
  await openContainer(page, target, container);
  await control.asked();
  await page.getByRole("button", { name: "Upload…" }).click();
  const dialog = page.getByRole("dialog", { name: "Upload" });
  const start = dialog.getByRole("button", { name: "Start upload" });
  await control.pickFolder(folder);
  await dialog.getByRole("button", { name: "Choose folder…" }).click();
  await expect(start).toBeVisible();

  await control.cancelPick();
  await dialog.getByRole("button", { name: "Choose folder…" }).click();
  await expect(dialog.getByText(/^Choose files to upload into this prefix/)).toBeVisible();
  await expect(start).toHaveCount(0);
  await expect(dialog.getByRole("alert")).toHaveCount(0);
  expect((await control.asked()).map((asked) => asked.kind)).toEqual(["pick folder", "pick folder"]);
  await dialog.getByRole("button", { name: "Close" }).click();
  expect(await control.names(target, container)).toEqual([]);
});

function userMetadata(page: Page): Locator {
  return inspector(page).locator("section", { has: page.getByRole("heading", { name: "User metadata" }) });
}

async function openObject(page: Page, target: string, container: string, name: string): Promise<void> {
  await openContainer(page, target, container);
  await objectRow(page, name).click();
}

async function addKey(page: Page, key: string, value: string): Promise<void> {
  await inspector(page).getByRole("button", { name: "Edit metadata" }).click();
  await page.getByRole("button", { name: "+ add key" }).click();
  await page.getByLabel("metadata key").last().fill(key);
  await page.getByLabel("metadata value").last().fill(value);
}

// downloadObject downloads the selected object into a new folder and
// returns the SHA-256 of what arrived.
async function downloadObject(page: Page, name: string): Promise<string> {
  await page.getByRole("button", { name: "Download object…" }).click();
  const dialog = page.getByRole("dialog", { name: "Download" });
  const into = localFolder();
  await control.pickFolder(into);
  await dialog.getByRole("button", { name: "Choose directory…" }).click();
  await dialog.getByRole("button", { name: "Start download" }).click();
  await finish(page, dialog);
  return digestOf(join(into, name));
}

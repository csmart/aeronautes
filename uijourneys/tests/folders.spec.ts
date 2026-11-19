import { test } from "@playwright/test";
import { mkdirSync, symlinkSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";
import { control } from "../control.ts";
import { expect, finish, lag, localFolder, openContainer, ownContainer } from "../helpers.ts";

// 0.5.0 step 13 (ADR 0103): a folder upload takes each regular file under
// its relative path, leaves out links and empty directories and says so,
// and refuses a folder of more than 10,000 files at the pick.
test("upload a folder with nested files, a link and an empty directory", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-folders");
  const folder = localFolder();
  const name = basename(folder);
  writeFileSync(join(folder, "top.bin"), "top\n");
  mkdirSync(join(folder, "inner", "deep"), { recursive: true });
  writeFileSync(join(folder, "inner", "deep", "nested.bin"), "nested\n");
  symlinkSync(join(folder, "top.bin"), join(folder, "a-link"));
  mkdirSync(join(folder, "empty"));

  await openContainer(page, target, container);
  await page.getByRole("button", { name: "Upload…" }).click();
  const dialog = page.getByRole("dialog", { name: "Upload" });
  await control.pickFolder(folder);
  await dialog.getByRole("button", { name: "Choose folder…" }).click();
  await expect(dialog.locator("p.folder")).toHaveText(
    `Folder "${name}": 2 files to upload into ${container}/${name}/; 1 entry skipped `
    + "(links, other special entries and a download's own partial files are left out); 1 empty directory is not uploaded.");
  // The report names the first entries the walk left out.
  await expect(dialog.locator(".problem", { hasText: "Skipped" })).toContainText(`${name}/a-link`);
  await expect(dialog.locator(".problem", { hasText: "Empty folders" })).toContainText("1 empty directory produces no object");
  await dialog.getByRole("button", { name: "Start upload" }).click();
  await finish(page, dialog);

  // Read by the run's own session: the regular files under their relative
  // paths, and nothing for the link or the empty directory.
  await expect.poll(async () => (await control.names(target, container)).sort(), lag).toEqual([`${name}/inner/deep/nested.bin`, `${name}/top.bin`]);
});

test("a folder of more than 10,000 files is refused at the pick", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-folders-many");
  const folder = localFolder();
  for (let i = 0; i <= 10_000; i++) writeFileSync(join(folder, `f${i}`), "");

  await openContainer(page, target, container);
  await page.getByRole("button", { name: "Upload…" }).click();
  const dialog = page.getByRole("dialog", { name: "Upload" });
  await control.pickFolder(folder);
  await dialog.getByRole("button", { name: "Choose folder…" }).click();
  await expect(dialog.getByRole("alert")).toHaveText("the folder holds more than 10000 files; choose a smaller folder or upload it in parts");
  await expect(dialog.getByRole("button", { name: "Start upload" })).toHaveCount(0);
});

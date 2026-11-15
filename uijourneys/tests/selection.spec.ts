import { test } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { control } from "../control.ts";
import { expect, expectInside, finish, localFolder, objectRow, openContainer, openFolder, ownContainer, writeFiles } from "../helpers.ts";

// ADR 0108 decision 1: a selection's folders are not copied, so a selection
// holding a folder beside a file is told so before the copy dialog opens.
// The folder is uploaded with a file and a folder inside it, so that one
// listing shows both; the copy itself is cancelled, and nothing is sent.
test("a selection holding a folder is warned before a copy", async ({ page }, info) => {
  const container = await ownContainer(info.project.name, "ui-selection");
  const folder = localFolder();
  writeFiles(folder, [16], 1);
  mkdirSync(join(folder, "inner"));
  writeFiles(join(folder, "inner"), [16], 1);

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
  await objectRow(page, "file00.bin").click();
  await inner.click({ modifiers: ["ControlOrMeta"] });

  await page.getByRole("button", { name: "Copy selected…" }).click();
  const warning = page.getByRole("alertdialog", { name: "Selected folders are left out" });
  await expect(warning.getByRole("heading", { name: "1 selected folder will not be copied" })).toBeVisible();
  await expect(warning).toContainText("This selection holds 1 folder beside 1 object: inner/.");
  await expect(warning).toContainText("That folder and everything in it will not be copied.");
  await expect(warning.getByRole("button", { name: "Copy the 1 object only…" })).toBeVisible();
  await expectInside(warning);

  await warning.getByRole("button", { name: "Cancel" }).click();
  await expect(warning).toHaveCount(0);
  await expect(page.getByRole("dialog", { name: "Transfer" })).toHaveCount(0);
});

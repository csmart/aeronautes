import { test, type Locator, type Page } from "@playwright/test";
import { basename } from "node:path";
import { control } from "../control.ts";
import { expect, localFolder, openContainer, ownContainer, writeFiles } from "../helpers.ts";

// ADR 0116 through the window, on every target: a folder uploaded into the
// "/" prefix makes names that start with "/", and the top of the container
// must show the "/" folder holding them, also on a store whose own top
// listing leaves it out.
test("see the slash folder at the top of a container", async ({ page }, info) => {
  const container = await ownContainer(info.project.name, "ui-slash");
  const folder = localFolder();
  writeFiles(folder, [16], 1);
  await openContainer(page, info.project.name, container);
  await goTo(page, "/");
  await page.getByRole("button", { name: "Upload…" }).click();
  const dialog = page.getByRole("dialog", { name: "Upload" });
  await control.pickFolder(folder);
  await dialog.getByRole("button", { name: "Choose folder…" }).click();
  await dialog.getByRole("button", { name: "Start upload" }).click();
  await expect(dialog.getByText(/^Job \S+ started\.$/)).toBeVisible();
  await dialog.getByRole("button", { name: "Close" }).last().click();

  // The upload is one small file; the listing shows it once it is written.
  await openContainer(page, info.project.name, container);
  const objects = page.getByRole("listbox", { name: "Objects" });
  const slashFolder = objects.getByRole("option", { name: /^\/ / });
  await refreshUntilListed(page, slashFolder);
  await slashFolder.dblclick();
  await refreshUntilListed(page, objects.getByRole("option", { name: new RegExp(`^${basename(folder)}/ `) }));
});

// On a real store one listing can lag another: the top listing can show the
// "/" folder before the listing inside it holds the upload. So a row the
// upload should have made is looked for across refreshes.
async function refreshUntilListed(page: Page, row: Locator) {
  await expect(async () => {
    await page.getByRole("button", { name: "Refresh listing" }).click();
    await expect(row).toBeVisible({ timeout: 2000 });
  }).toPass({ timeout: 60_000 });
}

async function goTo(page: Page, prefix: string) {
  const field = page.getByRole("textbox", { name: "Prefix" });
  await field.fill(prefix);
  await field.press("Enter");
}

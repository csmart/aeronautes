import { test, type Page } from "@playwright/test";
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
  await expect(async () => {
    await page.getByRole("button", { name: "Refresh listing" }).click();
    await expect(objects.getByRole("option", { name: /^\/ / })).toBeVisible({ timeout: 2000 });
  }).toPass({ timeout: 60_000 });
  await objects.getByRole("option", { name: /^\/ / }).dblclick();
  await expect(objects.getByRole("option", { name: new RegExp(`^${basename(folder)}/ `) })).toBeVisible();
});

async function goTo(page: Page, prefix: string) {
  const field = page.getByRole("textbox", { name: "Prefix" });
  await field.fill(prefix);
  await field.press("Enter");
}

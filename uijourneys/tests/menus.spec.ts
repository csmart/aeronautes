import { test } from "@playwright/test";
import { connect, expect, ownContainer } from "../helpers.ts";

// A container row's context menu in a real browser: a right-click opens the
// menu, and choosing Copy container opens that container and the copy
// dialog for it. Nothing is written; the dialog is closed unsent.
test("open a copy from a container's context menu", async ({ page }, info) => {
  const container = await ownContainer(info.project.name, "ui-menu");
  await connect(page, info.project.name);
  await page.getByRole("button", { name: new RegExp(`^${container} `) }).click({ button: "right" });
  const menu = page.getByRole("menu", { name: `Actions for container ${container}` });
  await menu.getByRole("menuitem", { name: "Copy container…" }).click();
  const dialog = page.getByRole("dialog", { name: "Transfer" });
  await expect(dialog.getByRole("heading", { name: "Copy" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Path" }).getByRole("button", { name: container })).toBeVisible();
  await dialog.getByRole("button", { name: "Close" }).first().click();
});

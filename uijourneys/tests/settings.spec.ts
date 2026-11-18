import { test } from "@playwright/test";
import { connect, expect } from "../helpers.ts";

// ADR 0126 decisions 3 and 4 (found by the operator): the settings can be
// filtered, and reset to this version's defaults after asking.
test("filter the settings and reset them to the defaults", async ({ page }, info) => {
  test.skip(info.project.name !== "mock", "settings are the window's own, driven against the mock only");
  await connect(page, "mock");
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Settings" });

  await dialog.getByLabel("Filter settings").fill("connection");
  await expect(dialog.getByLabel("Connection cap")).toBeVisible();
  await expect(dialog.getByLabel("Browser retention")).toHaveCount(0);
  await expect(dialog.getByRole("heading", { name: "Jobs" })).toHaveCount(0);
  await dialog.getByLabel("Filter settings").fill("");

  const queue = dialog.getByLabel("Queue depth");
  await queue.fill("3");
  await queue.press("Tab");
  await expect(dialog.getByText("3 waiting at most")).toBeVisible();
  await expect(dialog.getByText(/Jobs at once: up to \d running and 3 waiting/)).toBeVisible();

  await dialog.getByRole("button", { name: "Reset to defaults…" }).click();
  await dialog.getByRole("button", { name: "Reset every setting" }).click();
  await expect(dialog.getByText("Settings reset to this version's defaults.")).toBeVisible();
  await expect(dialog.getByText("16 waiting at most")).toBeVisible();
  await dialog.getByRole("button", { name: "Close settings" }).click();
});

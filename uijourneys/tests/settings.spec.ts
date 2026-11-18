import { test } from "@playwright/test";
import { connect, expect } from "../helpers.ts";

// ADR 0126 decisions 3 and 4 (found by the operator): the settings can be
// filtered, and reset to this version's defaults after asking; ADR 0127: the
// log file is kept from Settings, and the reset turns it off.
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

  // ADR 0127 decision 3: the log file is kept from Settings, and its path
  // is shown so it can be found.
  await dialog.getByLabel("Keep a log file").click();
  await expect(dialog.getByText("being written")).toBeVisible();
  await expect(dialog.locator(".log-path .path")).toHaveText(/aeronautes\.log$/);

  await dialog.getByRole("button", { name: "Reset to defaults…" }).click();
  await dialog.getByRole("button", { name: "Reset every setting" }).click();
  await expect(dialog.getByText("Settings reset to this version's defaults.")).toBeVisible();
  await expect(dialog.getByText("16 waiting at most")).toBeVisible();
  await expect(dialog.getByLabel("Keep a log file")).not.toBeChecked();
  await dialog.getByRole("button", { name: "Close settings" }).click();
});

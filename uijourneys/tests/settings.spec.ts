import { test, type Page } from "@playwright/test";
import { existsSync, readFileSync, statSync } from "node:fs";
import { createServer } from "node:net";
import { join } from "node:path";
import { connect, expect } from "../helpers.ts";

// ADR 0126 decisions 3 and 4 (found by the operator): the settings can be
// filtered, and reset to this version's defaults after asking; ADR 0127: the
// log file is kept from Settings, and the reset turns it off.
test("filter the settings and reset them to the defaults", async ({ page }, info) => {
  test.skip(info.project.name !== "mock", "settings are the window's own, driven against the mock only");
  await connect(page, "mock");
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Settings" });
  let reset = false;
  try {
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

    // ADR 0127 decision 3: the log file is kept from Settings, in the state
    // folder, and its path is shown so it can be found. What a sign-in that
    // gets no answer writes is added to the file while it is kept, and
    // nothing once it is turned off.
    await dialog.getByLabel("Keep a log file").click();
    await expect(dialog.getByText("being written")).toBeVisible();
    await dialog.getByLabel("Log level").selectOption("debug");
    await expect(dialog.getByText("debug in force", { exact: true })).toBeVisible();
    const logFile = (await dialog.locator(".log-path .path").textContent())!;
    expect(logFile).toBe(join(process.env.AERONAUTES_PRIVATE!, "state", "state", "aeronautes", "aeronautes.log"));
    expect(existsSync(logFile), "the log file").toBe(true);
    const kept = statSync(logFile).size;
    await dialog.getByRole("button", { name: "Close settings" }).click();
    await signInWithNoAnswer(page);
    await expect.poll(() => readFileSync(logFile, "utf8").slice(kept)).toMatch(/sign-in got no answer/);

    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await dialog.getByRole("button", { name: "Reset to defaults…" }).click();
    await dialog.getByRole("button", { name: "Reset every setting" }).click();
    await expect(dialog.getByText("Settings reset to this version's defaults.")).toBeVisible();
    reset = true;
    await expect(dialog.getByText("16 waiting at most")).toBeVisible();
    await expect(dialog.getByLabel("Keep a log file")).not.toBeChecked();
    await expect(dialog.getByText("info in force", { exact: true })).toBeVisible();
    await dialog.getByRole("button", { name: "Close settings" }).click();
    const stopped = statSync(logFile).size;
    await signInWithNoAnswer(page);
    expect(statSync(logFile).size, "the log file after it was turned off").toBe(stopped);
  } finally {
    // Later journeys share these settings, and a kept debug log.
    if (!reset) {
      await page.goto("/");
      await page.getByRole("button", { name: "Settings", exact: true }).click();
      await dialog.getByRole("button", { name: "Reset to defaults…" }).click();
      await dialog.getByRole("button", { name: "Reset every setting" }).click();
      await expect(dialog.getByText("Settings reset to this version's defaults.")).toBeVisible();
    }
  }
});

// signInWithNoAnswer verifies a new bookmark's details against a loopback
// port nothing listens on, which the sign-in retries with a warning in the
// log, and leaves the bookmark unsaved.
async function signInWithNoAnswer(page: Page): Promise<void> {
  const server = createServer();
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const port = (server.address() as { port: number }).port;
  await new Promise((resolve) => server.close(resolve));
  await page.getByRole("navigation", { name: "Bookmarks" }).getByTitle("Add bookmark", { exact: true }).click();
  const editor = page.getByRole("form", { name: "Add bookmark" });
  await editor.getByLabel("Display name").fill("journey no answer");
  await editor.getByRole("textbox", { name: /^Auth URL/ }).fill(`http://127.0.0.1:${port}/auth/v1.0`);
  await editor.getByLabel("Username").fill("test:tester");
  await editor.getByLabel(/^API key/).fill("testing");
  await editor.getByRole("button", { name: "Verify" }).click();
  await expect(editor.getByRole("alert")).toBeVisible({ timeout: 2 * 60 * 1000 });
  await editor.getByRole("button", { name: "Cancel" }).click();
}

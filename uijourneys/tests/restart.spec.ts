import { test, type Page } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { control } from "../control.ts";
import { expect, forgetAccounts, localFolder, openContainer, ownContainer } from "../helpers.ts";

// Test plan step 9: what the next start says about a job that was running
// when a run ended. Quitting as closing the window does cancels the job
// cleanly, so it is a cancelled job with its record and nothing is
// disclosed; a run that ends with no shutdown at all, as a crash or a power
// cut ends it, leaves the job unfinished, and the next start discloses it,
// saying what it knows about what was and was not done, and dismissing that
// keeps the job's record.
//
// A restart loses the session's keys and the run's record of what it made
// on an account, which would leave a real cloud's containers behind, so
// these journeys run only in a mock-only run.
test.describe.serial("a run that ends with a job running", () => {
  test.beforeEach(async ({}, info) => {
    test.skip(info.project.name !== "mock" || !!process.env.AERONAUTES_CLOUDS, "restarting is driven in a mock-only run");
    test.setTimeout(300_000);
  });

  test("quitting cancels the running job and keeps its record", async ({ page }) => {
    const container = await startLongUpload(page, "ui-quit");
    const before = await control.started();
    await control.quit();
    await reopen(page, before);
    await page.getByRole("button", { name: /jobs & changes/ }).click();
    const row = page.locator("div.job").filter({ hasText: `→ ${container}/` });
    await expect(row.getByText("cancelled", { exact: true })).toBeVisible();
    await expect(page.getByRole("region", { name: "Interrupted jobs from a previous run" })).toHaveCount(0);
    await page.getByRole("button", { name: /▾ hide/ }).click();
    await giveTheMockItsKey(page);
  });

  test("a run ended with no shutdown is disclosed at the next start", async ({ page }) => {
    const container = await startLongUpload(page, "ui-crash");
    const before = await control.started();
    await control.crash();
    await reopen(page, before);
    await page.getByRole("button", { name: /jobs & changes/ }).click();
    const disclosure = page.getByRole("region", { name: "Interrupted jobs from a previous run" });
    await expect(disclosure.getByText("An earlier run was interrupted. Its data and records are kept; nothing was cleaned up.")).toBeVisible();
    const entry = disclosure.getByRole("listitem").filter({ hasText: `→ ${container}/` });
    await expect(entry.getByText("Upload", { exact: true })).toBeVisible();
    await expect(entry.getByText(/^(Work had begun when the run ended|Nothing is recorded as attempted)/)).toBeVisible();
    await entry.getByRole("button", { name: "Dismiss" }).click();
    await expect(disclosure).toHaveCount(0);
    // Dismissing keeps the job's record.
    await page.getByRole("tab", { name: "Job history" }).click();
    const history = page.getByRole("region", { name: "Job history" });
    await expect(history.getByRole("article").filter({ hasText: `"${container}/"` })).toHaveCount(1);
    await page.getByRole("button", { name: /▾ hide/ }).click();
    await giveTheMockItsKey(page);
  });
});

// startLongUpload starts an upload of enough files that it is still
// running when the run is ended, and returns its container.
async function startLongUpload(page: Page, name: string): Promise<string> {
  const container = await ownContainer("mock", name);
  const folder = localFolder();
  for (let i = 0; i < 2000; i++) writeFileSync(join(folder, `${String(i).padStart(4, "0")}.txt`), `${i}`);
  await openContainer(page, "mock", container);
  await page.getByRole("button", { name: "Upload…" }).click();
  const upload = page.getByRole("dialog", { name: "Upload" });
  await control.pickFolder(folder);
  await upload.getByRole("button", { name: "Choose folder…" }).click();
  await upload.getByRole("button", { name: "Start upload" }).click();
  await expect(upload.getByText(/^Job \S+ started\.$/)).toBeVisible();
  return container;
}

// reopen waits for the application to have started again, not merely to
// answer, since the old start answers for a moment after it is told to
// stop, and then loads the page from the new one.
async function reopen(page: Page, before: string): Promise<void> {
  forgetAccounts();
  await expect.poll(() => control.started().catch(() => before), { timeout: 60_000 }).not.toBe(before);
  await expect(async () => {
    await page.goto("/");
    await expect(page.getByRole("navigation", { name: "Bookmarks" })).toBeVisible({ timeout: 2000 });
  }).toPass({ timeout: 60_000 });
}

// The started-again application's session store no longer holds the key
// its bookmark records as saved, so the bookmark is given the key again in
// its editor, as later journeys expect it to connect.
async function giveTheMockItsKey(page: Page): Promise<void> {
  const tile = page.getByRole("navigation", { name: "Bookmarks" }).getByRole("button", { name: "journey mock", exact: true });
  await page.getByRole("button", { name: "Edit journey mock" }).click();
  await page.getByLabel("API key").fill("testing");
  await page.getByRole("button", { name: "Save & connect" }).click();
  await expect(tile).toHaveAttribute("aria-pressed", "true");
}

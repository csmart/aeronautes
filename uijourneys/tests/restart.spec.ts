import { test, type Page } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { basename, join } from "node:path";
import { control } from "../control.ts";
import {
  expect, forgetAccounts, inspector, localFolder, objectRow, openContainer, openFolder, ownContainer, uploadFolder, writeFiles,
} from "../helpers.ts";

// Test plan step 9: what the next start says about a job that was running
// when a run ended. Quitting as closing the window does stops the job
// cleanly: it ends cancelled, or partial when part of a file had landed and
// that file is recorded as not finished (ADR 0122 decision 7), keeps its
// record, and nothing is disclosed; a run that ends with no shutdown at all, as a crash or a power
// cut ends it, leaves the job unfinished, and the next start discloses it,
// saying what it knows about what was and was not done, and dismissing that
// keeps the job's record.
//
// A restart loses the session's keys and the run's record of what it made
// on an account, which would leave a real cloud's containers behind, so
// these journeys run only in a mock-only run.

// Whether this test restarted the application, so its key is put back
// however the test ends; a keyless mock bookmark would fail every journey
// after it.
let restarted = false;
test.beforeEach(async ({}, info) => {
  test.skip(info.project.name !== "mock" || !!process.env.AERONAUTES_CLOUDS, "restarting is driven in a mock-only run");
  test.setTimeout(300_000);
  restarted = false;
});
test.afterEach(async ({ page }) => {
  if (restarted) await giveTheMockItsKey(page);
});

test.describe.serial("a run that ends with a job running", () => {
  test("quitting stops the running job and keeps its record", async ({ page }) => {
    const container = await startLongUpload(page, "ui-quit");
    const before = await control.started();
    restarted = true;
    await control.quit();
    await reopen(page, before);
    await page.getByRole("button", { name: /jobs & changes/ }).click();
    const row = page.locator("div.job").filter({ hasText: `→ ${container}/` });
    await expect(row.getByText(/^(cancelled|partial)$/)).toBeVisible();
    // The notice is read once, when the page loads; reloading and waiting
    // for the job list again makes its absence a read, not a race.
    await page.reload();
    await page.getByRole("button", { name: /jobs & changes/ }).click();
    await expect(row.getByText(/^(cancelled|partial)$/)).toBeVisible();
    await expect(page.getByRole("region", { name: "Interrupted jobs from a previous run" })).toHaveCount(0);
    await page.getByRole("button", { name: /▾ hide/ }).click();
  });

  test("a run ended with no shutdown is disclosed at the next start", async ({ page }) => {
    const container = await startLongUpload(page, "ui-crash");
    const before = await control.started();
    restarted = true;
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
  });

});

// Test plan C2, as far as a browser can take it: a metadata save and a
// quit as closing the window does, at once and a little later. The quit
// waits for a write it admitted, so the next start finds the store and
// Recent changes in step: the save is in both or in neither. Pressing
// the window's own close button at a chosen moment needs a person.
test("quitting during a metadata save keeps the store and Recent changes in step", async ({ page }) => {
  const container = await ownContainer("mock", "ui-quit-write");
  const folder = localFolder();
  writeFiles(folder, [16], 1);
  await openContainer(page, "mock", container);
  await uploadFolder(page, folder);
  const resource = `${container}/${basename(folder)}/file00.bin`;
  for (const offset of [0, 100, 300]) {
    const key = `Round-${offset}`;
    await openObject(page, container, folder);
    await inspector(page).getByRole("button", { name: "Edit metadata" }).click();
    await page.getByRole("button", { name: "+ add key" }).click();
    await page.getByLabel("metadata key").last().fill(key);
    await page.getByLabel("metadata value").last().fill("saved");
    // The save's record names how many keys it set, which tells this
    // round's record from an earlier one's.
    const keys = await page.getByLabel("metadata key").count();
    const before = await control.started();
    restarted = true;
    await page.getByRole("button", { name: "Save", exact: true }).click();
    if (offset) await page.waitForTimeout(offset);
    const quitAt = Date.now();
    await control.quit();
    await reopen(page, before);
    expect(Date.now() - quitAt, "quit and started again").toBeLessThan(30_000);
    await giveTheMockItsKey(page);

    await openObject(page, container, folder);
    const stored = (await inspector(page).locator(".kv", { hasText: key }).count()) > 0;
    await page.getByRole("button", { name: /jobs & changes/ }).click();
    await page.getByRole("tab", { name: "Recent changes" }).click();
    await expect(page.getByText("Last loaded results for these filters.")).toBeVisible();
    const recorded = page.locator(".event", { hasText: resource })
      .filter({ hasText: "object.metadata.update" }).filter({ hasText: `set ${keys} metadata keys` }).filter({ hasText: "applied" });
    await expect(recorded, `round ${offset}: the store ${stored ? "holds" : "does not hold"} the save`).toHaveCount(stored ? 1 : 0);
    await page.getByRole("button", { name: /▾ hide/ }).click();
  }
});

// openObject opens the one object of the journey's folder, with its
// details read afresh from the store.
async function openObject(page: Page, container: string, folder: string): Promise<void> {
  await openContainer(page, "mock", container);
  await openFolder(page, folder);
  await objectRow(page, "file00.bin").click();
  await expect(inspector(page).getByRole("button", { name: "Edit metadata" })).toBeEnabled();
}

// startLongUpload starts an upload that is still running when the run is
// ended, and returns its container: a job on the mock finishes in moments,
// so a bandwidth limit holds a 2 MiB file for about a minute.
async function startLongUpload(page: Page, name: string): Promise<string> {
  const container = await ownContainer("mock", name);
  const folder = localFolder();
  writeFileSync(join(folder, "slow.bin"), Buffer.alloc(2 << 20, 7));
  await openContainer(page, "mock", container);
  await page.getByRole("button", { name: "Upload…" }).click();
  const upload = page.getByRole("dialog", { name: "Upload" });
  await upload.getByText("Per-job settings").click();
  await upload.getByLabel("Bandwidth").fill("32k");
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
  await expect(async () => {
    await page.goto("/");
    await expect(page.getByRole("navigation", { name: "Bookmarks" })).toBeVisible({ timeout: 2000 });
  }).toPass({ timeout: 60_000 });
  const tile = page.getByRole("navigation", { name: "Bookmarks" }).getByRole("button", { name: "journey mock", exact: true });
  await page.getByRole("button", { name: "Edit journey mock", exact: true }).click();
  await page.getByLabel("API key").fill("testing");
  await page.getByRole("button", { name: "Save & connect" }).click();
  await expect(tile).toHaveAttribute("aria-pressed", "true");
}

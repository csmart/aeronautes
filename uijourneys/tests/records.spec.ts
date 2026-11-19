import { test, type Locator, type Page } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";
import { control } from "../control.ts";
import {
  digestOf, expect, finish, lag, localFolder, openContainer, ownContainer, sha256, terminalJobLabel, uploadFolder, writeFiles,
} from "../helpers.ts";

// ADR 0104 through the window for the two routes that write to the store:
// an upload and a copy paused at a unit boundary hold what the first unit
// wrote and nothing of the next, as the run's own session lists it, and
// once resumed write every object once, as their item records and the
// digests of a download of what they wrote say. The jobs journey pauses a
// download, which writes only to the local disk.
//
// A unit of an upload is one source directory, and of a copy 500 names, so
// the upload is of two folders and the copy of 501 names; a bandwidth limit
// holds the first unit back long enough to ask for the pause during it.
const slow = "128k";
const big = Buffer.alloc(2 << 20, 7);

test.describe("pause and resume what writes to the store", () => {
  test("pause an upload of two folders between them and resume it", async ({ page }, info) => {
    const target = info.project.name;
    const container = await ownContainer(target, "ui-pause-upload");
    const folder = localFolder();
    const digests = new Map<string, string>();
    for (const dir of ["a", "b"]) {
      mkdirSync(join(folder, dir));
      writeFileSync(join(folder, dir, "big.bin"), big);
      writeFileSync(join(folder, dir, "started.txt"), "unit started\n");
      digests.set(`${dir}/big.bin`, sha256(big));
      digests.set(`${dir}/started.txt`, digestOf(join(folder, dir, "started.txt")));
    }
    const name = basename(folder);

    await openContainer(page, target, container);
    await page.getByRole("button", { name: "Upload…" }).click();
    const dialog = page.getByRole("dialog", { name: "Upload" });
    await dialog.getByText("Per-job settings").click();
    await dialog.getByLabel("Bandwidth").fill(slow);
    await dialog.getByLabel("Transfers", { exact: true }).fill("2");
    await control.pickFolder(folder);
    await dialog.getByRole("button", { name: "Choose folder…" }).click();
    await dialog.getByRole("button", { name: "Start upload" }).click();
    const id = await jobOf(dialog);
    try {
      // Either directory may be first. Its small file witnesses entry into
      // the unit while the large file leaves time to request the pause.
      await expect.poll(async () => (await control.names(target, container)).length, lag).toBeGreaterThan(0);
      await pauseAndResume(page, id, async () => {
        const groups = ["a", "b"].map((dir) => [`${name}/${dir}/big.bin`, `${name}/${dir}/started.txt`]);
        await expect(async () => {
          expect(groups).toContainEqual((await control.names(target, container)).sort());
        }).toPass(lag);
      });
      await expect(badge(jobRow(page, id))).toHaveText("complete", { timeout: 4 * 60 * 1000 });
      const records = await itemRecords(page, id);
      await expect(total(records, "Recorded items")).toHaveText("4");
      for (const file of digests.keys()) {
        await expect(records.locator("article.item-record", { hasText: `${name}/${file}` })).toHaveCount(1);
        await expect(records.locator("article.item-record", { hasText: `${name}/${file}` }).getByText(/^Execution: copied · /)).toBeVisible();
      }
    } finally {
      await cancelIfRunning(page, id);
    }

    await expect.poll(async () => (await control.names(target, container)).sort(), lag)
      .toEqual([...digests.keys()].map((file) => `${name}/${file}`).sort());
    const into = await downloadContainer(page, target, container);
    for (const [file, digest] of digests) expect(digestOf(join(into, name, file)), file).toBe(digest);
  });

  test("pause a copy of 501 names between its batches and resume it", async ({ page }, info) => {
    const target = info.project.name;
    const source = await ownContainer(target, "ui-pause-copy-from");
    const dest = await ownContainer(target, "ui-pause-copy-to");
    const folder = localFolder();
    const digests = writeFiles(folder, Array<number>(500).fill(16), 1);
    // The large file sorts first, so the first batch is the slow one.
    writeFileSync(join(folder, "a-big.bin"), big);
    digests.set("a-big.bin", sha256(big));
    const name = basename(folder);
    await openContainer(page, target, source);
    await uploadFolder(page, folder);

    await openContainer(page, target, source);
    await page.getByRole("button", { name: "Copy container…" }).click();
    const copy = page.getByRole("dialog", { name: "Transfer" });
    await copy.getByText("Per-job settings").click();
    await copy.getByLabel("Bandwidth").fill(slow);
    await copy.getByLabel("Destination container").fill(dest);
    await copy.getByRole("button", { name: "Preview" }).click();
    await copy.getByRole("button", { name: "Start copy" }).click();
    const id = await jobOf(copy);
    try {
      // A copy plans as it lists and parks at the head of its first batch
      // when asked before it, so the pause waits for that batch to be
      // writing.
      await expect.poll(async () => (await control.names(target, dest)).length, lag).toBeGreaterThan(0);
      await pauseAndResume(page, id, async () => {
        await expect.poll(async () => (await control.names(target, dest)).length, lag).toBe(500);
      });
      await expect(badge(jobRow(page, id))).toHaveText("complete", { timeout: 4 * 60 * 1000 });
      const records = await itemRecords(page, id);
      await expect(total(records, "Recorded items")).toHaveText("501");
      await expect(total(records, "Failed names")).toHaveText("0");
    } finally {
      await cancelIfRunning(page, id);
    }

    await expect.poll(async () => (await control.names(target, dest)).length, lag).toBe(501);
    const into = await downloadContainer(page, target, dest);
    for (const [file, digest] of digests) expect(digestOf(join(into, name, file)), file).toBe(digest);
  });
});

// pauseAndResume pauses a running job, waits for it to park, runs the
// journey's check of what the first unit left, and resumes it.
async function pauseAndResume(page: Page, id: string, whilePaused: () => Promise<void>): Promise<void> {
  await openDrawer(page);
  const row = jobRow(page, id);
  await expect(badge(row)).toHaveText("running");
  await row.getByRole("button", { name: `Pause job ${id}` }).click();
  await expect(row.getByText("Pauses when the current batch finishes, unless it is the last.")).toBeVisible();
  await expect(badge(row)).toHaveText("paused", { timeout: 2 * 60 * 1000 });
  await expect(row.getByText("Paused. Nothing more is transferred until it is resumed.")).toBeVisible();
  await whilePaused();
  // Still parked once the check is done, so nothing the check saw was a
  // moment before the next unit started.
  await expect(badge(row)).toHaveText("paused");
  await row.getByRole("button", { name: `Resume job ${id}` }).click();
}

async function jobOf(dialog: Locator): Promise<string> {
  const started = await dialog.getByText(/^Job \S+ started\.$/).textContent();
  await dialog.getByRole("button", { name: "Close" }).first().click();
  return started!.split(" ")[1];
}

function jobRow(page: Page, id: string): Locator {
  return page.locator("div.job", { has: page.getByRole("button", { name: `Item details for job ${id}`, exact: true }) });
}

function badge(row: Locator): Locator {
  return row.locator(".badge");
}

async function openDrawer(page: Page): Promise<void> {
  const bar = page.getByRole("button", { name: /jobs & changes$|▾ hide$/ });
  if ((await bar.getAttribute("aria-expanded")) !== "true") await bar.click();
  await expect(page.getByRole("tab", { name: "Jobs" })).toBeVisible();
}

// itemRecords opens a finished job's item details, as the drawer keeps
// them, once their counts have been read.
async function itemRecords(page: Page, id: string): Promise<Locator> {
  await openDrawer(page);
  await page.getByRole("button", { name: `Item details for job ${id}`, exact: true }).click();
  const records = page.getByRole("region", { name: "Retained item records" });
  await expect(records.getByText(/^Last observed job state: (complete|partial)\.$/)).toBeVisible();
  await expect(total(records, "Recorded items")).toBeVisible();
  return records;
}

function total(records: Locator, label: string): Locator {
  return records.locator(".totals div", { has: records.page().getByText(label, { exact: true }) }).locator("dd");
}

// A failed journey must not leave a paused or slowed job holding a slot
// that the journeys after it need.
async function cancelIfRunning(page: Page, id: string): Promise<void> {
  await page.goto("/");
  await openDrawer(page);
  const row = jobRow(page, id);
  if (terminalJobLabel.test((await badge(row).textContent()) ?? "")) return;
  await row.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(badge(row)).toHaveText(terminalJobLabel, { timeout: 2 * 60 * 1000 });
}

async function downloadContainer(page: Page, target: string, container: string): Promise<string> {
  const into = localFolder();
  await openContainer(page, target, container);
  await page.getByRole("button", { name: "Download container…" }).click();
  const dialog = page.getByRole("dialog", { name: "Download" });
  await control.pickFolder(into);
  await dialog.getByRole("button", { name: "Choose directory…" }).click();
  await dialog.getByRole("button", { name: "Start download" }).click();
  await finish(page, dialog);
  return into;
}

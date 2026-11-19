import { test, type Locator, type Page } from "@playwright/test";
import { readdirSync, statSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";
import { control } from "../control.ts";
import { armDelete, digestOf, expect, finish, localFolder, openContainer, ownContainer, sha256, uploadFolder, writeFiles } from "../helpers.ts";

// Jobs on the mock finish in moments, so a job that has to be caught running
// is held back by its own per-job bandwidth limit: the 2 MiB file at a few
// KiB a second takes about a minute. The mock alone is used, because a real
// store's speed decides nothing here and the waits would only grow.
const slow = "32k";
const big = "a-big.bin";

// files writes small files of the given sizes and the big one into a new
// folder and returns each name's digest.
function files(small: number[]): { folder: string; digests: Map<string, string> } {
  const folder = localFolder();
  const digests = writeFiles(folder, small, 1);
  const body = Buffer.alloc(2 << 20, 7);
  writeFileSync(join(folder, big), body);
  digests.set(big, sha256(body));
  return { folder, digests };
}

// slowed opens the dialog's per-job settings and limits its bandwidth.
async function slowed(dialog: Locator): Promise<void> {
  await dialog.getByText("Per-job settings").click();
  await dialog.getByLabel("Bandwidth").fill(slow);
}

// started reads the job a dialog started and closes the dialog.
async function started(dialog: Locator): Promise<string> {
  const text = await dialog.getByText(/^Job \S+ started\.$/).textContent();
  await dialog.getByRole("button", { name: "Close" }).first().click();
  return text!.split(" ")[1];
}

async function startSlowUpload(page: Page, folder: string): Promise<string> {
  await page.getByRole("button", { name: "Upload…" }).click();
  const dialog = page.getByRole("dialog", { name: "Upload" });
  await slowed(dialog);
  await control.pickFolder(folder);
  await dialog.getByRole("button", { name: "Choose folder…" }).click();
  await dialog.getByRole("button", { name: "Start upload" }).click();
  return started(dialog);
}

function badge(page: Page, id: string): Locator {
  return page.locator("div.job", { has: page.getByRole("button", { name: `Item details for job ${id}`, exact: true }) }).locator(".badge");
}

async function openDrawer(page: Page): Promise<void> {
  const bar = page.getByRole("button", { name: /jobs & changes$|▾ hide$/ });
  if ((await bar.getAttribute("aria-expanded")) !== "true") await bar.click();
  await expect(page.getByRole("tab", { name: "Jobs" })).toBeVisible();
}

// cancel cancels a job from the drawer, from a fresh page so that it also
// serves a finally, and waits for it to end. A job that has already ended is
// left as it is.
async function cancel(page: Page, id: string): Promise<void> {
  await page.goto("/");
  await openDrawer(page);
  const row = page.locator("div.job", { has: page.getByRole("button", { name: `Item details for job ${id}`, exact: true }) });
  if (!/^(complete|partial|cancelled|cancelled, incomplete|failed)$/.test((await badge(page, id).textContent()) ?? "")) {
    await row.getByRole("button", { name: "Cancel", exact: true }).click();
  }
  await expect(badge(page, id)).toHaveText(/^(complete|partial|cancelled|cancelled, incomplete|failed)$/, { timeout: 2 * 60 * 1000 });
}

// expectCancelledPartway checks that a job cancelled after some of its files
// landed reads as cancelled and incomplete, not as a failure (ADR 0129): the
// file it was writing is counted incomplete and nothing is counted failed.
async function expectCancelledPartway(page: Page, id: string): Promise<void> {
  await expect(badge(page, id)).toHaveText("cancelled, incomplete");
  const row = page.locator("div.job", { has: page.getByRole("button", { name: `Item details for job ${id}`, exact: true }) });
  await expect(row.locator(".counts")).toContainText(/\d+ incomplete/);
  await expect(row.locator(".counts")).not.toContainText("failed");
}

// filesIn lists every file under a folder by its path inside it.
function filesIn(dir: string): string[] {
  return readdirSync(dir, { recursive: true, encoding: "utf8" }).filter((path) => statSync(join(dir, path)).isFile()).sort();
}

test.describe("cancelled jobs", () => {
  test.beforeEach(({}, info) => {
    test.skip(info.project.name !== "mock", "a job is held running by its bandwidth limit, against the mock only");
  });

  // What a cancelled upload wrote is whole: each object it left has its
  // file's bytes, read back by a download, and it left nothing else. It is
  // cancelled once the small files are in the store and the big one is still
  // being sent, so the job wrote something and reads "cancelled,
  // incomplete": a transfer reads plain "cancelled" only when nothing landed.
  test("a cancelled upload leaves only whole objects", async ({ page }, info) => {
    const target = info.project.name;
    const container = await ownContainer(target, "ui-stopped-upload");
    const { folder, digests } = files([16, 32]);
    const f = basename(folder);
    await openContainer(page, target, container);
    const id = await startSlowUpload(page, folder);
    await expect.poll(async () => (await control.names(target, container)).length).toBe(2);
    await cancel(page, id);
    await expectCancelledPartway(page, id);

    const left = await control.names(target, container);
    for (const name of left) expect([...digests.keys()].map((n) => `${f}/${n}`), "an object the upload left").toContain(name);
    expect(left, "the big file was still being sent").not.toContain(`${f}/${big}`);

    await openContainer(page, target, container);
    await page.getByRole("button", { name: "Download container…" }).click();
    const download = page.getByRole("dialog", { name: "Download" });
    const into = localFolder();
    await control.pickFolder(into);
    await download.getByRole("button", { name: "Choose directory…" }).click();
    await download.getByRole("button", { name: "Start download" }).click();
    await finish(page, download);
    expect(filesIn(into), "what was downloaded").toEqual([...left].sort());
    for (const name of left) expect(digestOf(join(into, name)), name).toBe(digests.get(name.slice(f.length + 1)));
  });

  // ADR 0122 decision 5: a cancelled download removes nothing it finished,
  // and what it left is whole, as for the upload; only the engine's temporary file, whose name
  // ends ".partial", may be there besides.
  test("a cancelled download leaves only whole files", async ({ page }, info) => {
    const target = info.project.name;
    const container = await ownContainer(target, "ui-stopped-download");
    const { folder, digests } = files([16, 32]);
    const f = basename(folder);
    await openContainer(page, target, container);
    await uploadFolder(page, folder);

    await openContainer(page, target, container);
    await page.getByRole("button", { name: "Download container…" }).click();
    const dialog = page.getByRole("dialog", { name: "Download" });
    await slowed(dialog);
    const into = localFolder();
    await control.pickFolder(into);
    await dialog.getByRole("button", { name: "Choose directory…" }).click();
    await dialog.getByRole("button", { name: "Start download" }).click();
    const id = await started(dialog);
    // Cancelled once the small files are written and the big one is not.
    await expect.poll(() => filesIn(into).filter((path) => !path.endsWith(".partial")).length).toBe(2);
    await cancel(page, id);
    await expectCancelledPartway(page, id);

    const left = filesIn(into);
    expect(left, "the big file was still being written").not.toContain(join(f, big));
    for (const path of left) {
      if (path.endsWith(".partial")) continue;
      expect([...digests.keys()].map((name) => join(f, name)), "a file the download left").toContain(path);
      expect(digestOf(join(into, path)), path).toBe(digests.get(basename(path)));
    }
  });

  // ADR 0105 decision 3: while an upload writes into a container, emptying
  // it and deleting it are refused at the start, naming the upload's job,
  // and nothing in it is deleted.
  test("refuse to empty or delete a container an upload is writing into", async ({ page }, info) => {
    const target = info.project.name;
    const container = await ownContainer(target, "ui-busy-container");
    for (const name of ["already/here.bin", "top.bin"]) await control.put(target, container, name, "written before the upload");
    const before = (await control.names(target, container)).sort();
    // The big file alone, so the upload writes nothing until it ends and the
    // container's listing stays as it was while the deletions are refused.
    const { folder } = files([]);
    await openContainer(page, target, container);
    const id = await startSlowUpload(page, folder);
    try {
      await openDrawer(page);
      await expect(badge(page, id)).toHaveText("running");
      const refusal = `another job is using that part of the container: job ${id} is writing into container ${container}; ` +
        "wait for it to finish or cancel it before deleting there";
      for (const action of ["Empty container", "Delete container"]) {
        await openContainer(page, target, container);
        await page.getByRole("button", { name: `${action}…` }).click();
        await armDelete(page, action);
        const dialog = page.getByRole("dialog", { name: "Delete" });
        await expect(dialog.getByRole("alert"), action).toHaveText(refusal);
        await expect(dialog.getByText(/^Job \S+ started\.$/)).toHaveCount(0);
        await dialog.getByRole("button", { name: "Close" }).first().click();
        expect((await control.names(target, container)).sort(), `after "${action}"`).toEqual(before);
      }
      await openDrawer(page);
      await expect(badge(page, id)).toHaveText("running");
    } finally {
      await cancel(page, id);
    }
  });
});

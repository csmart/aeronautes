import { test, type Locator, type Page } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";
import { control } from "../control.ts";
import {
  addMockBookmark, connect, deleteBookmark, digestOf, escapeRegExp, expect, finish, inspector, jobIds, lag, localFolder, objectRow, openContainer, openFolder, ownContainer, sha256, typedDelete, uploadFolder, writeFiles,
} from "../helpers.ts";

// The jobs drawer, the job limits in Settings and the per-job settings,
// through the window. Jobs on the mock finish in moments, so a job that has
// to stay running is a download held back by its own per-job bandwidth
// limit: a 2 MiB object at a few KiB a second takes long enough for the
// next steps, and it is cancelled once they are done.
const slow = "32k";
// A download hands the engine at most 500 names at a time (ADR 0104), so a
// container of 501 names is two units, and a pause asked during the first
// parks the job between them. The large file sorts first, so the bandwidth
// limit holds the first unit back.
const big = "a-big.bin";

type Many = { target: string; container: string; folder: string; digests: Map<string, string> };
let many: Many | null = null;

// manyObjects uploads the 501 names once per worker; a worker started
// after a failure makes them again in a container of its own, since a
// label names one container for the whole run.
async function manyObjects(page: Page, target: string): Promise<Many> {
  if (many?.target === target) return many;
  const container = await ownContainer(target, `ui-jobs-many-${process.pid}`);
  const folder = localFolder();
  const digests = writeFiles(folder, Array<number>(500).fill(16), 1);
  const body = Buffer.alloc(2 << 20, 7);
  writeFileSync(join(folder, big), body);
  digests.set(big, sha256(body));
  await openContainer(page, target, container);
  await uploadFolder(page, folder);
  many = { target, container, folder, digests };
  return many;
}

// startDownload downloads the open container into a folder, slowed by a
// bandwidth limit when one is given, and returns the dialog once its start
// was answered, with a job or with a refusal.
async function startDownload(page: Page, into: string, bandwidth?: string): Promise<Locator> {
  await page.getByRole("button", { name: "Download container…" }).click();
  const dialog = page.getByRole("dialog", { name: "Download" });
  if (bandwidth) {
    await dialog.getByText("Per-job settings").click();
    await dialog.getByLabel("Bandwidth").fill(bandwidth);
  }
  await control.pickFolder(into);
  await dialog.getByRole("button", { name: "Choose directory…" }).click();
  await dialog.getByRole("button", { name: "Start download" }).click();
  await expect(dialog.getByText(/^Job \S+ started\.$/).or(dialog.getByRole("alert"))).toBeVisible();
  return dialog;
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

async function cancel(page: Page, id: string): Promise<void> {
  await openDrawer(page);
  const row = jobRow(page, id);
  if (/^(complete|partial|cancelled|failed)$/.test((await badge(row).textContent()) ?? "")) return;
  await row.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(badge(row)).toHaveText(/^(complete|partial|cancelled|failed)$/, { timeout: 2 * 60 * 1000 });
}

// cancelAll cancels what a journey left running from a fresh page, whatever
// state the journey left the window in, so the next journey has the slots.
async function cancelAll(page: Page, ids: string[]): Promise<void> {
  await page.goto("/");
  for (const id of ids) await cancel(page, id);
}

type Limits = { budget: string; queue: string; cap: string; profile: string };

// setLimits sets the job and connection limits and the default profile in
// Settings, as given, and returns what they were.
async function setLimits(page: Page, wanted: Partial<Limits>): Promise<Limits> {
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Settings" });
  const budget = dialog.getByLabel("Active-job budget");
  const queue = dialog.getByLabel("Queue depth");
  const cap = dialog.getByLabel("Connection cap");
  const profile = dialog.getByLabel("Default performance profile");
  const before = { budget: await budget.inputValue(), queue: await queue.inputValue(), cap: await cap.inputValue(), profile: await profile.inputValue() };
  if (wanted.budget) {
    await budget.selectOption(wanted.budget);
    await expect(budget).toHaveValue(wanted.budget);
  }
  for (const [field, value] of [[queue, wanted.queue], [cap, wanted.cap]] as const) {
    if (!value) continue;
    await field.fill(value);
    await field.press("Tab");
    await expect(field).toHaveValue(value);
  }
  if (wanted.profile) {
    await profile.selectOption(wanted.profile);
    await expect(profile).toHaveValue(wanted.profile);
  }
  if (wanted.queue) await expect(dialog.getByText(`${wanted.queue} waiting at most`)).toBeVisible();
  if (wanted.cap) {
    await expect(dialog.getByText(`${wanted.cap} ${wanted.cap === "1" ? "connection" : "connections"} per host on the next connect`)).toBeVisible();
  }
  await dialog.getByRole("button", { name: "Close settings" }).click();
  return before;
}

// restoreLimits puts the settings back from a fresh page, whatever state
// the journey left the window in, so a failure does not change the
// settings for the journeys after it.
async function restoreLimits(page: Page, target: string, before: Limits): Promise<void> {
  await connect(page, target);
  await setLimits(page, before);
}

function expectDigests(dir: string, digests: Map<string, string>): void {
  for (const [name, digest] of digests) expect(digestOf(join(dir, name)), name).toBe(digest);
}

test.describe("jobs", () => {
  // 0.1.0 step 7e: a completed copy offers a re-run and a running one does
  // not; the re-run is a fresh job that plans the source as it is now.
  test("re-run a completed copy as a fresh job", async ({ page }, info) => {
    const target = info.project.name;
    const { container: held } = await manyObjects(page, target);
    const source = await ownContainer(target, "ui-jobs-rerun-from");
    const dest = await ownContainer(target, "ui-jobs-rerun-to");
    const first = localFolder();
    writeFiles(first, [16], 1);
    await openContainer(page, target, source);
    await uploadFolder(page, first);

    await openContainer(page, target, source);
    await page.getByRole("button", { name: "Copy container…" }).click();
    const copy = page.getByRole("dialog", { name: "Transfer" });
    await copy.getByLabel("Destination container").fill(dest);
    await copy.getByRole("button", { name: "Preview" }).click();
    await copy.getByRole("button", { name: "Start copy" }).click();
    const copied = (await copy.getByText(/^Job \S+ started\.$/).textContent())!.split(" ")[1];
    await finish(page, copy);

    // A running job, held back by its bandwidth limit, offers pause and
    // cancel and no re-run.
    await openContainer(page, target, held);
    const running = await jobOf(await startDownload(page, localFolder(), slow));
    try {
      // The source changes before the re-run, which must copy what it holds now.
      const second = localFolder();
      writeFiles(second, [32], 2);
      await openContainer(page, target, source);
      await uploadFolder(page, second);

      await openDrawer(page);
      await expect(badge(jobRow(page, running))).toHaveText("running");
      await expect(jobRow(page, running).getByRole("button", { name: `Pause job ${running}` })).toBeVisible();
      await expect(jobRow(page, running).getByRole("button", { name: "Re-run" })).toHaveCount(0);
      const before = await jobIds(page);
      await jobRow(page, copied).getByRole("button", { name: "Re-run" }).click();
      await expect.poll(async () => (await jobIds(page)).filter((id) => !before.includes(id))).toHaveLength(1);
      const rerun = (await jobIds(page)).filter((id) => !before.includes(id));
      expect(rerun, "one fresh job").toHaveLength(1);
      expect(rerun[0]).not.toBe(copied);
      await expect(badge(jobRow(page, rerun[0]))).toHaveText("complete", { timeout: 2 * 60 * 1000 });
      await expect(badge(jobRow(page, copied))).toHaveText("complete");
      await expect.poll(async () => (await control.names(target, dest)).sort(), lag)
        .toEqual([`${basename(first)}/file00.bin`, `${basename(second)}/file00.bin`].sort());
    } finally {
      await cancelAll(page, [running]);
    }
  });

  // 0.1.0 step 4b as the 0.5.0 plan amends it, and step 18k's overlap: with
  // one job running and one waiting, a third start is refused naming both
  // counts; a download into a folder inside a running one's is refused
  // naming that job.
  test("refuse a start past the queue, and a download inside a running one", async ({ page }, info) => {
    const target = info.project.name;
    const { container } = await manyObjects(page, target);
    await connect(page, target);
    const before = await setLimits(page, { budget: "1", queue: "1" });
    const started: string[] = [];
    try {
      await openContainer(page, target, container);
      const outer = localFolder();
      started.push(await jobOf(await startDownload(page, outer, slow)));

      // A folder inside the running download's is refused at the preview,
      // with that job named in the whole reason.
      const inner = join(outer, "inner");
      mkdirSync(inner);
      await page.getByRole("button", { name: "Download container…" }).click();
      const overlap = page.getByRole("dialog", { name: "Download" });
      await control.pickFolder(inner);
      await overlap.getByRole("button", { name: "Choose directory…" }).click();
      await expect(overlap.getByRole("heading", { name: "Rejected — nothing was written" })).toBeVisible();
      const problem = overlap.locator(".problem").filter({ hasText: "Overlaps a running download" });
      await expect(problem.locator(".detail")).toHaveText(new RegExp(
        `^the running download job ${started[0]}, of .+, is writing into ${escapeRegExp(outer)}, which holds or is inside this folder; ` +
        "wait for it to finish, cancel it, or choose a folder outside it$"));
      await expect(overlap.getByRole("button", { name: "Start download" })).toHaveCount(0);
      await overlap.getByRole("button", { name: "Close" }).first().click();

      started.push(await jobOf(await startDownload(page, localFolder())));
      await openDrawer(page);
      await expect(badge(jobRow(page, started[0]))).toHaveText("running");
      await expect(badge(jobRow(page, started[1]))).toHaveText("queued");
      const jobs = await jobIds(page);
      const third = await startDownload(page, localFolder());
      await expect(third.getByRole("alert")).toHaveText(
        "the job queue is full: 1 running and 1 waiting; wait for one to finish or cancel one");
      await third.getByRole("button", { name: "Close" }).first().click();
      expect(await jobIds(page), "the refused start made no job").toEqual(jobs);

      // Cancelling the waiting job makes room for one more.
      await cancel(page, started[1]);
      started.push(await jobOf(await startDownload(page, localFolder())));
      await openDrawer(page);
      await expect(badge(jobRow(page, started[2]))).toHaveText("queued");
    } finally {
      await cancelAll(page, started.reverse());
      await restoreLimits(page, target, before);
    }
  });

  // Test plan step 14: with two running at once, a third download waits;
  // pausing a running one parks it after its unit and lets the waiting one
  // start, and once resumed every download completes with every file.
  test("pause, resume and run downloads at once", async ({ page }, info) => {
    const target = info.project.name;
    const { container, digests } = await manyObjects(page, target);
    await connect(page, target);
    const before = await setLimits(page, { budget: "2" });
    const into = [localFolder(), localFolder(), localFolder()];
    const ids: string[] = [];
    try {
      await openContainer(page, target, container);
      for (const dir of into) ids.push(await jobOf(await startDownload(page, dir, "128k")));
      await openDrawer(page);
      const [paused, other, waiting] = ids.map((id) => jobRow(page, id));
      await expect(badge(paused)).toHaveText("running");
      await expect(badge(other)).toHaveText("running");
      await expect(badge(waiting)).toHaveText("queued");

      await paused.getByRole("button", { name: `Pause job ${ids[0]}` }).click();
      await expect(paused.getByText("Pauses when the current batch finishes, unless it is the last.")).toBeVisible();
      await expect(badge(paused)).toHaveText("paused", { timeout: 2 * 60 * 1000 });
      await expect(paused.getByText("Paused. Nothing more is transferred until it is resumed.")).toBeVisible();
      await expect(badge(waiting)).not.toHaveText("queued");

      await paused.getByRole("button", { name: `Resume job ${ids[0]}` }).click();
      for (const row of [paused, other, waiting]) {
        await expect(badge(row)).toHaveText("complete", { timeout: 4 * 60 * 1000 });
      }
    } finally {
      await cancelAll(page, [...ids].reverse());
      await restoreLimits(page, target, before);
    }
    for (const dir of into) expectDigests(join(dir, basename(many!.folder)), digests);
  });

  // Test plan step 18c: "Show in jobs" from a dialog, with the drawer's
  // account filter on another account, opens the Jobs tab on the job with
  // the filter back on all accounts. The mock has one account, so the other
  // is a second bookmark of it, made in the editor and deleted afterwards.
  test("show a copy in jobs with another account filtered", async ({ page }, info) => {
    test.skip(info.project.name !== "mock", "the second bookmark is made in the editor, against the mock only");
    const target = info.project.name;
    const source = await ownContainer(target, "ui-jobs-show-from");
    const dest = await ownContainer(target, "ui-jobs-show-to");
    const other = "journey mock other";
    await connect(page, target);
    await addMockBookmark(page, other);
    try {
      await openContainer(page, target, source);
      const folder = localFolder();
      writeFiles(folder, [16], 1);
      await uploadFolder(page, folder);

      await openDrawer(page);
      await page.locator(".tabs .filter select").selectOption({ label: other });
      await page.getByRole("button", { name: "Copy container…" }).click();
      const copy = page.getByRole("dialog", { name: "Transfer" });
      await copy.getByLabel("Destination container").fill(dest);
      await copy.getByRole("button", { name: "Preview" }).click();
      await copy.getByRole("button", { name: "Start copy" }).click();
      const id = (await copy.getByText(/^Job \S+ started\.$/).textContent())!.split(" ")[1];
      await copy.getByRole("button", { name: "Show in jobs" }).click();
      await expect(copy).toHaveCount(0);
      await expect(page.getByRole("tab", { name: "Jobs" })).toHaveAttribute("aria-selected", "true");
      await expect(page.locator(".tabs .filter select")).toHaveValue("");
      await expect(page.getByRole("button", { name: `Item details for job ${id}`, exact: true })).toHaveAttribute("aria-expanded", "true");
      await expect(badge(jobRow(page, id))).toHaveText("complete", { timeout: 2 * 60 * 1000 });
    } finally {
      await page.goto("/");
      await deleteBookmark(page, other);
    }
  });

  // Test plan step 18d: the recovery notice is one line with the rest under
  // "Details", rows alternate in tint, and a failed job's error is one line
  // in its row and whole in its details, which "Copy details" copies. A job
  // fails when nothing was attempted, which a copy whose source container
  // was deleted, in a second window, between its preview and its start
  // gives; teardown skips a container a journey already deleted.
  test("read the drawer's notice, tints and a failed job", async ({ page, context }, info) => {
    const target = info.project.name;
    const source = await ownContainer(target, "ui-jobs-failed-from");
    const dest = await ownContainer(target, "ui-jobs-failed-to");
    await openContainer(page, target, source);
    await page.getByRole("button", { name: "Copy container…" }).click();
    const copy = page.getByRole("dialog", { name: "Transfer" });
    await copy.getByLabel("Destination container").fill(dest);
    await copy.getByRole("button", { name: "Preview" }).click();
    await expect(copy.getByRole("button", { name: "Start copy" })).toBeEnabled();

    const other = await context.newPage();
    await openContainer(other, target, source);
    await other.getByRole("button", { name: "Delete container…" }).click();
    await typedDelete(other, "Delete container");
    await other.close();

    await copy.getByRole("button", { name: "Start copy" }).click();
    const id = (await copy.getByText(/^Job \S+ started\.$/).textContent())!.split(" ")[1];
    await copy.getByRole("button", { name: "Show in jobs" }).click();
    const row = jobRow(page, id);
    await expect(badge(row)).toHaveText("failed", { timeout: 2 * 60 * 1000 });

    const notice = page.locator(".recovery[role=status]");
    await expect(notice.locator("p").first()).toHaveText(
      // A pattern matches the raw text, where the sentence breaks across lines.
      new RegExp("^\\d+ recovery records? kept for \\d+ finished jobs? in all accounts; what they track on the store is unchecked\\.$".replaceAll(" ", "\\s+")));
    await expectOneLine(notice.locator("p").first());
    const more = notice.getByText("These count records, not objects", { exact: false });
    await expect(more).toBeHidden();
    await notice.getByText("Details", { exact: true }).click();
    await expect(more).toBeVisible();

    // Neighbouring rows differ in tint; the pointer is kept off them, since
    // a hovered row takes a tint of its own.
    await page.mouse.move(0, 0);
    const rows = page.locator(".rows > div.job");
    expect(await rows.count()).toBeGreaterThanOrEqual(2);
    const tints = await rows.evaluateAll((jobs) => jobs.slice(0, 3).map((job) => getComputedStyle(job).backgroundColor));
    expect(tints[0], "first and second rows").not.toBe(tints[1]);
    if (tints.length > 2) expect(tints[0], "first and third rows").toBe(tints[2]);

    // "Show in jobs" opened the row, whose details hold the error whole;
    // closed, the row keeps it on one line.
    const details = row.getByRole("button", { name: `Item details for job ${id}`, exact: true });
    await expect(details).toHaveAttribute("aria-expanded", "true");
    const whole = row.locator("p.full-error");
    await expect(whole).not.toHaveText("");
    const error = (await whole.textContent())!;
    await row.getByRole("button", { name: "Copy details" }).click();
    await expect(row.getByRole("status").filter({ hasText: /^Copied\.$/ })).toBeVisible();
    await details.click();
    await expect(whole).toHaveCount(0);
    const line = row.locator("p.one-line");
    await expect(line).toHaveText(error);
    await expectOneLine(line);
  });

  // Test plan step 15: an upload's and a download's own profile and
  // transfers are recorded as set for the job, and what the profile or the
  // default chose says so.
  test("record per-job settings for an upload and a download", async ({ page }, info) => {
    const target = info.project.name;
    const container = await ownContainer(target, "ui-jobs-settings");
    const folder = localFolder();
    writeFiles(folder, [16, 32], 1);
    await openContainer(page, target, container);

    const chosen = async (dialog: Locator) => {
      await dialog.getByText("Per-job settings").click();
      await dialog.getByLabel("Profile").selectOption("aggressive");
      await dialog.getByLabel("Transfers").fill("6");
    };
    await page.getByRole("button", { name: "Upload…" }).click();
    const up = page.getByRole("dialog", { name: "Upload" });
    await chosen(up);
    await control.pickFolder(folder);
    await up.getByRole("button", { name: "Choose folder…" }).click();
    await up.getByRole("button", { name: "Start upload" }).click();
    const uploaded = await finish(page, up);

    await openContainer(page, target, container);
    await page.getByRole("button", { name: "Download container…" }).click();
    const down = page.getByRole("dialog", { name: "Download" });
    await chosen(down);
    const into = localFolder();
    await control.pickFolder(into);
    await down.getByRole("button", { name: "Choose directory…" }).click();
    await down.getByRole("button", { name: "Start download" }).click();
    const downloaded = await finish(page, down);

    for (const details of [uploaded, downloaded]) {
      expect(details).toMatch(/\nprofile\naggressive\n/);
      expect(details).toMatch(/\ntransfers\n6\nset for this job\n/);
      expect(details).toMatch(/\ncheckers\n\d+\nfrom profile\n/);
      expect(details).toMatch(/\nbufferSize\n\S+\ndefault\n/);
    }
    expect(uploaded).toMatch(/\nchunkSize\n\S+\ncalculated\n/);
    expect(downloaded).not.toMatch(/chunkSize/);
  });

  // Test plan step 16: the default profile in Settings is where the upload
  // dialog's profile starts.
  test("start the upload dialog at the default profile", async ({ page }, info) => {
    const target = info.project.name;
    const container = await ownContainer(target, "ui-jobs-profile");
    await connect(page, target);
    const before = await setLimits(page, { profile: "balanced" });
    try {
      // A fresh page reads the saved setting rather than the one in memory.
      await openContainer(page, target, container);
      await page.getByRole("button", { name: "Upload…" }).click();
      const dialog = page.getByRole("dialog", { name: "Upload" });
      await dialog.getByText("Per-job settings").click();
      await expect(dialog.getByLabel("Profile")).toHaveValue("balanced");
      await dialog.getByRole("button", { name: "Close" }).first().click();
    } finally {
      await restoreLimits(page, target, before);
    }
  });

  // 0.1.0 step 4c: with the connection cap at 1, a session opened afterwards
  // still browses and inspects. A session is opened once per bookmark and
  // kept, so the capped one is a new bookmark of the mock's account, made in
  // the editor. The objects are uploaded first through the usual bookmark,
  // so the capped one holds no job and can be deleted afterwards.
  test("browse and inspect under a connection cap of one", async ({ page }, info) => {
    test.skip(info.project.name !== "mock", "the capped session's bookmark is made in the editor, against the mock only");
    const target = info.project.name;
    const container = await ownContainer(target, "ui-jobs-capped");
    const capped = "journey mock capped";
    const folder = localFolder();
    const names = [...writeFiles(folder, [16, 32, 64], 1).keys()];
    await openContainer(page, target, container);
    await uploadFolder(page, folder);
    const before = await setLimits(page, { cap: "1" });
    try {
      await addMockBookmark(page, capped);
      await page.getByRole("button", { name: new RegExp(`^${container} `) }).click();
      await openFolder(page, folder);
      for (const name of names) {
        await objectRow(page, name).click();
        await expect(inspector(page).getByRole("heading", { name: new RegExp(`${name}$`) })).toBeVisible();
        await expect(inspector(page).getByRole("button", { name: "Copy ETag" })).toBeVisible();
      }
    } finally {
      await page.goto("/");
      await deleteBookmark(page, capped);
      await restoreLimits(page, target, before);
    }
  });
});

// expectOneLine checks that a paragraph is drawn as a single line.
async function expectOneLine(paragraph: Locator): Promise<void> {
  const { height, line } = await paragraph.evaluate((element) => {
    const style = getComputedStyle(element);
    const line = Number.parseFloat(style.lineHeight) || Number.parseFloat(style.fontSize) * 1.25;
    // A callout's padding is not a line of text, so only its content counts.
    const padding = Number.parseFloat(style.paddingTop) + Number.parseFloat(style.paddingBottom);
    return { height: element.clientHeight - padding, line };
  });
  expect(height, "drawn on one line").toBeLessThan(line * 1.6);
}

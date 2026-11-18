import { expect, type Locator, type Page } from "@playwright/test";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { control } from "./control.ts";

const bookmarks = new Map<string, string>();

// account opens the run's own session on the target once, which a container
// the run creates there needs, and returns the name of its bookmark.
export async function account(target: string): Promise<string> {
  if (!bookmarks.has(target)) {
    const opened = await control.account(target);
    bookmarks.set(target, opened.bookmark || "journey mock");
  }
  return bookmarks.get(target)!;
}

// forgetAccounts drops what the runner knows of the accounts it opened, for
// after the application was quit and started again: that start has no
// record of them, so the next container opens its account afresh.
export function forgetAccounts(): void {
  bookmarks.clear();
}

// ownContainer creates a container the run owns on the target.
export async function ownContainer(target: string, name: string, segmented = false): Promise<string> {
  await account(target);
  return (await control.container(target, name, segmented)).container;
}

// connect opens the target's account in the application, as the operator
// does from the rail. The mock's bookmark is made through the editor; a real
// cloud's is made in Go, so its password never reaches the page.
export async function connect(page: Page, target: string): Promise<string> {
  const name = await account(target);
  await page.goto("/");
  const tile = page.getByRole("navigation", { name: "Bookmarks" }).getByRole("button", { name, exact: true });
  const add = page.getByRole("button", { name: "Add a bookmark" });
  // The rail fills after the page loads, so wait for one or the other
  // before deciding whether the mock's bookmark still has to be made.
  await expect(tile.or(add).first()).toBeVisible();
  if (target === "mock" && !(await tile.isVisible())) {
    await add.click();
    await page.getByLabel("Display name").fill(name);
    await page.getByLabel("Auth URL").fill(process.env.AERONAUTES_MOCK!);
    await page.getByLabel("Username").fill("test:tester");
    await page.getByLabel("API key").fill("testing");
    await page.getByRole("button", { name: "Save & connect" }).click();
  } else {
    await tile.click();
  }
  // The tile is pressed only once its account has connected; the column's
  // heading is on screen either way.
  await expect(tile).toHaveAttribute("aria-pressed", "true");
  return name;
}

// openContainer connects and opens one of the run's containers. A row's
// name is the container's followed by its size, which tells it apart from
// its segment sidecar, whose name starts the same way.
export async function openContainer(page: Page, target: string, container: string): Promise<void> {
  await connect(page, target);
  await page.getByRole("button", { name: new RegExp(`^${escapeRegExp(container)} `) }).click();
  await expect(page.getByRole("navigation", { name: "Path" }).getByRole("button", { name: container })).toBeVisible();
}

// finish waits for the job a dialog started to end, requires that it
// completed, and returns the text of its details as the drawer shows them,
// which include what the job recorded it did. The job's row is found by its
// details button, the one control in the row named after the job.
export async function finish(page: Page, dialog: Locator): Promise<string> {
  const started = await dialog.getByText(/^Job \S+ started\.$/).textContent();
  const id = started!.split(" ")[1];
  await dialog.getByRole("button", { name: "Show in jobs" }).click();
  const row = page.locator("div.job", { has: page.getByRole("button", { name: `Item details for job ${id}`, exact: true }) });
  const ended = row.getByText(/^(complete|partial|cancelled|failed)$/);
  await expect(ended).toBeVisible({ timeout: 8 * 60 * 1000 });
  await expect(ended).toHaveText("complete");
  const details = await row.innerText();
  await page.getByRole("button", { name: /▾ hide/ }).click();
  return details;
}

// writeFiles writes files of the given sizes into a folder, with contents
// that differ for each round, and returns each name's digest.
export function writeFiles(dir: string, sizes: number[], round: number): Map<string, string> {
  const digests = new Map<string, string>();
  sizes.forEach((size, i) => {
    const name = `file${String(i).padStart(2, "0")}.bin`;
    const body = Buffer.alloc(size, (i * 31 + round * 17 + 7) % 251);
    writeFileSync(join(dir, name), body);
    digests.set(name, sha256(body));
  });
  return digests;
}

export function localFolder(): string {
  return mkdtempSync(join(tmpdir(), "aeronautes-ui-"));
}

export function sha256(body: Buffer): string {
  return createHash("sha256").update(body).digest("hex");
}

export function digestOf(path: string): string {
  return sha256(readFileSync(path));
}

// expectInside checks that every button and text field of a dialog lies
// within the dialog's own width. A row of buttons too wide for its dialog
// pushes some of them out past its edge, where they still work and so pass
// every other check (found by hand in the 0.5.0 test run).
export async function expectInside(dialog: Locator): Promise<void> {
  const frame = (await dialog.boundingBox())!;
  for (const control of await dialog.locator("button, input, select, textarea").all()) {
    if (!(await control.isVisible())) continue;
    const box = (await control.boundingBox())!;
    const name = (await control.getAttribute("aria-label")) ?? (await control.innerText().catch(() => "")) ?? "";
    expect(box.x, `left edge of "${name}"`).toBeGreaterThanOrEqual(frame.x);
    expect(box.x + box.width, `right edge of "${name}"`).toBeLessThanOrEqual(frame.x + frame.width);
  }
}

// openUpload opens the upload dialog with a chunk size small enough that a
// file of a few MiB is segmented, and with the kind of large object to
// publish when the journey chooses one.
export async function openUpload(page: Page, largeObjects?: "slo" | "dlo"): Promise<Locator> {
  await page.getByRole("button", { name: "Upload…" }).click();
  const dialog = page.getByRole("dialog", { name: "Upload" });
  await dialog.getByText("Per-job settings").click();
  await dialog.getByLabel("Chunk size").fill("1Mi");
  if (largeObjects) {
    await dialog.getByLabel("Large objects").selectOption(largeObjects);
  }
  return dialog;
}

export async function openFolder(page: Page, folder: string): Promise<void> {
  await page.getByRole("listbox", { name: "Objects" }).getByRole("option", { name: new RegExp(`^${basename(folder)}/ `) }).dblclick();
  await expect(page.getByRole("navigation", { name: "Path" }).getByRole("button", { name: basename(folder) })).toBeVisible();
}

export function objectRow(page: Page, name: string): Locator {
  return page.getByRole("listbox", { name: "Objects" }).getByRole("option", { name: new RegExp(`^${name} `) });
}

export function inspector(page: Page): Locator {
  return page.getByRole("complementary", { name: "Inspector" });
}

// armDelete previews a deletion, checks how the option that takes large
// objects' segments stands without touching it, types the words the dialog
// then asks for, as the operator reads them there, and starts it.
export async function armDelete(page: Page, action: string, segments: "ticked" | "not offered" = "not offered"): Promise<void> {
  const dialog = page.getByRole("dialog", { name: "Delete" });
  await dialog.getByRole("button", { name: "Preview" }).click();
  const confirm = dialog.getByLabel(/to arm/);
  await expect(confirm).toBeVisible();
  // The dialog is opened on one job, the action's, and its button names it.
  await expect(dialog.getByRole("heading", { name: action, exact: true })).toBeVisible();
  const option = dialog.getByLabel("Also delete the segments of large objects");
  if (segments === "ticked") {
    await expect(option).toBeChecked();
    await expect(confirm).toHaveAttribute("placeholder", / and segments$/);
  } else {
    await expect(option).toHaveCount(0);
  }
  // Every deletion is typed with its verb; emptying keeps its own.
  await expect(confirm).toHaveAttribute("placeholder", action === "Empty container" ? /^empty / : /^delete /);
  await confirm.fill((await confirm.getAttribute("placeholder"))!);
  await dialog.getByRole("button", { name: action }).click();
}

// typedDelete starts a deletion that way and waits for its job in the drawer.
export async function typedDelete(page: Page, action: string, segments: "ticked" | "not offered" = "not offered"): Promise<void> {
  await armDelete(page, action, segments);
  await finish(page, page.getByRole("dialog", { name: "Delete" }));
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export { expect };

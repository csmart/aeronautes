import { test, type Page } from "@playwright/test";
import { basename, join } from "node:path";
import { control } from "../control.ts";
import {
  account, connect, digestOf, escapeRegExp, expect, finish, inspector, lag, localFolder, objectRow, openContainer, openUpload, ownContainer,
  uploadFolder, writeFiles,
} from "../helpers.ts";

// These journeys need both clouds, so they run once, under the last one.
// The mock has one account: a second bookmark of it reaches the same
// containers, so none of this would mean anything there.
test.beforeEach(({}, info) => {
  test.skip(info.project.name !== "swift", "runs once, under the last cloud");
});

// Switching accounts from the rail must show each account's own containers
// and nothing of the other's.
test("switch between the two clouds", async ({ page }) => {
  const ceph = await ownContainer("ceph", "ui-switch");
  const swift = await ownContainer("swift", "ui-switch");
  const toSwift = await account("swift");
  const toCeph = await account("ceph");

  await connect(page, "ceph");
  await shows(page, ceph, swift);
  await page.getByRole("navigation", { name: "Bookmarks" }).getByRole("button", { name: toSwift, exact: true }).click();
  await shows(page, swift, ceph);
  await page.getByRole("navigation", { name: "Bookmarks" }).getByRole("button", { name: toCeph, exact: true }).click();
  await shows(page, ceph, swift);
});

// A copy from one account to the other, chosen in the dialog's Destination
// bookmark. Its destination starts as the account the window is on, so the
// dialog is opened once on each side of a switch first: a destination kept
// from the dialog's last opening would copy into the wrong account.
test("copy a folder from one cloud to the other", async ({ page }) => {
  const source = await ownContainer("ceph", "ui-across-from", true);
  const dest = await ownContainer("swift", "ui-across-to", true);
  const toCeph = await account("ceph");
  const toSwift = await account("swift");
  const folder = localFolder();
  const digests = writeFiles(folder, [1024, 3 << 20], 1);
  const names = [...digests.keys()].map((name) => `${basename(folder)}/${name}`).sort();

  await openContainer(page, "ceph", source);
  await uploadFolder(page, folder, await openUpload(page));
  await expect.poll(async () => (await control.names("ceph", source)).sort(), lag).toEqual(names);
  await expectDestination(page, toCeph);
  await switchTo(page, toSwift, dest);
  await expectDestination(page, toSwift);
  await switchTo(page, toCeph, source);

  await page.getByRole("button", { name: "Copy container…" }).click();
  const copy = page.getByRole("dialog", { name: "Transfer" });
  await copy.getByText("Per-job settings").click();
  await copy.getByLabel("Chunk size").fill("1Mi");
  await copy.getByLabel("Destination bookmark").selectOption({ label: toSwift });
  await copy.getByLabel("Destination container").fill(dest);
  await copy.getByRole("button", { name: "Preview" }).click();
  await expect(copy.locator(".counts .count").filter({ hasText: /^\s*\d+\s+new\s*$/ })).toHaveText(/^\s*2\s+new/);
  await copy.getByRole("button", { name: "Start copy" }).click();
  await finish(page, copy);
  await expect.poll(async () => (await control.names("swift", dest)).sort(), lag).toEqual(names);
  expect((await control.names("ceph", source)).sort(), "the source after the copy").toEqual(names);

  const into = localFolder();
  await openContainer(page, "swift", dest);
  await page.getByRole("button", { name: "Download container…" }).click();
  const download = page.getByRole("dialog", { name: "Download" });
  await control.pickFolder(into);
  await download.getByRole("button", { name: "Choose directory…" }).click();
  await download.getByRole("button", { name: "Start download" }).click();
  await finish(page, download);
  for (const [name, digest] of digests) expect(digestOf(join(into, basename(folder), name)), name).toBe(digest);
});

// The same container name on both accounts is two containers: an object
// deleted from one after switching to it is gone from that one only. Each
// account's container is the run's own; the second is given the first's
// exact name, which neither account held before.
test("delete from a container of the same name on the other cloud", async ({ page }) => {
  const name = await ownContainer("ceph", "ui-same-name");
  await account("swift");
  await control.sameNameAs("swift", name);
  for (const target of ["ceph", "swift"]) {
    await control.put(target, name, "kept.txt", target);
    await control.put(target, name, "deleted.txt", target);
  }

  await openContainer(page, "ceph", name);
  await expect(objectRow(page, "deleted.txt")).toBeVisible();
  await switchTo(page, await account("swift"), name);
  await objectRow(page, "deleted.txt").click();
  await inspector(page).getByRole("button", { name: "Delete object…" }).click();
  const dialog = page.getByRole("alertdialog", { name: "Delete object" });
  const words = dialog.getByLabel(/to confirm/);
  await words.fill((await words.getAttribute("placeholder"))!);
  await dialog.getByRole("button", { name: "Delete object" }).click();
  await expect(objectRow(page, "deleted.txt")).toHaveCount(0);

  await expect.poll(() => control.names("swift", name), lag).toEqual(["kept.txt"]);
  expect((await control.names("ceph", name)).sort(), "the other cloud's container").toEqual(["deleted.txt", "kept.txt"]);
});

async function shows(page: Page, present: string, absent: string) {
  await expect(page.getByRole("button", { name: new RegExp(`^${present} `) })).toBeVisible();
  await expect(page.getByRole("button", { name: new RegExp(`^${absent} `) })).toHaveCount(0);
}

// switchTo switches account from the rail, without loading the page again,
// and opens a container there.
async function switchTo(page: Page, bookmark: string, container: string) {
  await page.getByRole("navigation", { name: "Bookmarks" }).getByRole("button", { name: bookmark, exact: true }).click();
  await expect(page.getByText(`connected to ${bookmark}`, { exact: true })).toBeVisible();
  await page.getByRole("button", { name: new RegExp(`^${escapeRegExp(container)} `) }).click();
  await expect(page.getByRole("navigation", { name: "Path" }).getByRole("button", { name: container })).toBeVisible();
}

// expectDestination opens the copy dialog on the open container, checks the
// bookmark its destination starts on, and closes it unsent.
async function expectDestination(page: Page, bookmark: string) {
  await page.getByRole("button", { name: "Copy container…" }).click();
  const copy = page.getByRole("dialog", { name: "Transfer" });
  await expect(copy.getByLabel("Destination bookmark").locator("option:checked")).toHaveText(bookmark);
  await copy.getByRole("button", { name: "Close" }).first().click();
  await expect(copy).toHaveCount(0);
}

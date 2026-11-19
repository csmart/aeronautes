import { test, type Locator, type Page } from "@playwright/test";
import { readdirSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";
import { control } from "../control.ts";
import {
  digestOf, escapeRegExp, expect, finish, inspector, lag, localFolder, openContainer, openFolder, ownContainer, sha256, uploadFolder,
} from "../helpers.ts";

// K-TEST-001: names that are hard to carry, through the window. Each is a
// character a URL, a query string or a form treats as its own (a space,
// "%", "#", "?", "+"), a non-ASCII name in its decomposed Unicode form, and a
// long name. The composed form of the same name is left out: the engine
// compares names after normalising them, as rclone does, so a folder holding
// both forms uploads only one of them and the job ends partial.
const questioned = "what?.bin";
const names = [
  "with space.bin", "100%.bin", "hash#tag.bin", questioned, "a+b.bin", "café".normalize("NFD") + ".bin", `${"long-".repeat(39)}name.bin`,
];

test("upload, browse, download and delete names that are hard to encode", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-hard-names");
  const folder = localFolder();
  const digests = new Map<string, string>();
  for (const name of names) {
    const body = Buffer.from(`the file called ${name}`);
    writeFileSync(join(folder, name), body);
    digests.set(name, sha256(body));
  }
  expect(readdirSync(folder).sort(), "the local folder holds every name").toEqual([...names].sort());
  const f = basename(folder);
  const stored = (list: string[]) => list.map((name) => `${f}/${name}`).sort();

  await openContainer(page, target, container);
  await uploadFolder(page, folder);
  await expect.poll(async () => (await control.names(target, container)).sort(), lag).toEqual(stored(names));

  // Each name is a row of its own in the window, and opens in the inspector.
  await page.getByRole("button", { name: "Refresh listing" }).click();
  await openFolder(page, folder);
  for (const name of names) {
    await row(page, name).click();
    await expect(inspector(page).getByRole("heading", { name: new RegExp(`${escapeRegExp(name)}$`) })).toBeVisible();
  }

  // A download is held to names every platform can hold, so "?", which
  // Windows forbids, refuses the folder's download before anything is
  // written, naming the file.
  await page.getByRole("button", { name: "Up one level" }).click();
  const refused = await chooseDownload(page, f, localFolder());
  await expect(refused.getByRole("heading", { name: "Rejected — nothing was written" })).toBeVisible();
  await expect(refused.getByText(`path component "${questioned}" holds a character Windows forbids in a name; refused on every platform`)).toBeVisible();
  await expect(refused.getByRole("button", { name: "Start download" })).toHaveCount(0);
  await refused.getByRole("button", { name: "Close" }).first().click();

  // That object is deleted by its name through the window, and only it goes.
  await openFolder(page, folder);
  await row(page, questioned).click();
  await inspector(page).getByRole("button", { name: "Delete object…" }).click();
  const dialog = page.getByRole("alertdialog", { name: "Delete object" });
  const words = dialog.getByLabel(/to confirm/);
  await expect(words).toHaveAttribute("placeholder", `delete ${f}/${questioned}`);
  await words.fill(`delete ${f}/${questioned}`);
  await dialog.getByRole("button", { name: "Delete object" }).click();
  await expect(dialog).toHaveCount(0);
  const left = names.filter((name) => name !== questioned);
  await expect.poll(async () => (await control.names(target, container)).sort(), lag).toEqual(stored(left));

  // The rest come back under their own names with their own bytes.
  await page.getByRole("button", { name: "Up one level" }).click();
  const into = localFolder();
  const download = await chooseDownload(page, f, into);
  await download.getByRole("button", { name: "Start download" }).click();
  await finish(page, download);
  expect(readdirSync(join(into, f)).sort(), "the downloaded folder holds every name and nothing else").toEqual([...left].sort());
  for (const name of left) expect(digestOf(join(into, f, name)), name).toBe(digests.get(name));
});

// row finds a listed object by its exact name, which a pattern built from it
// as text would misread.
function row(page: Page, name: string): Locator {
  return page.getByRole("listbox", { name: "Objects" }).getByRole("option", { name: new RegExp(`^${escapeRegExp(name)} `) });
}

// chooseDownload opens the download of a folder at the container's top and
// chooses the local folder it goes into, and returns the dialog.
async function chooseDownload(page: Page, folder: string, into: string): Promise<Locator> {
  await page.getByRole("option", { name: new RegExp(`^${escapeRegExp(folder)}/ `) }).click();
  await page.getByRole("button", { name: "Download folder…" }).click();
  const dialog = page.getByRole("dialog", { name: "Download" });
  await control.pickFolder(into);
  await dialog.getByRole("button", { name: "Choose directory…" }).click();
  return dialog;
}

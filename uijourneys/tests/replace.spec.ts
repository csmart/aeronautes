import { test, type Locator, type Page } from "@playwright/test";
import { appendFileSync, mkdirSync, readdirSync, writeFileSync } from "node:fs";
import { basename, join, relative } from "node:path";
import { control } from "../control.ts";
import {
  account, digestOf, escapeRegExp, expect, finish, jobIds, lag, localFolder, objectRow, openContainer, openFolder, ownContainer, sha256, writeFiles,
} from "../helpers.ts";

// Replacing what is already there, through the window (ADR 0081, ADR 0122
// decisions 2, 4 and 12): the page refuses a collision until replacing is
// asked for, the application then asks its own question, and the answer
// decides whether anything is written. One container carries a folder of
// three files through uploads, a copy to a second container, and downloads,
// so each step starts from what the one before it left. The question's
// words are read through control.asked(), as a person reads them.
const nativeNote = "This question comes from the application, not from the page.";
const declined = "the replacement was not confirmed; nothing was written";
// A pattern matches the page's raw text, where a sentence can break across lines.
const words = (sentence: string) => new RegExp(escapeRegExp(sentence).replaceAll(" ", "\\s+"));

test.describe.serial("replace", () => {
  let container = "";
  let copies = "";
  let folder = "";
  let name = "";
  let binding = "";
  // Each relative name's digest as the store should now hold it.
  const digests = new Map<string, string>();
  // file00.bin as first uploaded, which the copy wrote at the destination.
  let copied = "";

  test.beforeAll(async ({}, info) => {
    const target = info.project.name;
    container = await ownContainer(target, "ui-replace");
    copies = await ownContainer(target, "ui-replace-to");
    binding = `${escapeRegExp(await account(target))} \\(${target === "mock" ? "AUTH_test" : "[^()]+"}\\)`;
    folder = localFolder();
    name = basename(folder);
    for (const [file, digest] of writeFiles(folder, [16, 32], 1)) digests.set(file, digest);
    mkdirSync(join(folder, "sub"));
    for (const [file, digest] of writeFiles(join(folder, "sub"), [64], 1)) digests.set(`sub/${file}`, digest);
    copied = digests.get("file00.bin")!;
  });

  // 0.5.0 step 19u. The transfer journey replaces every file of a folder;
  // this one replaces none, then one.
  test("upload the same folder again: the same files stay, one changed file is replaced", async ({ page }, info) => {
    await openContainer(page, info.project.name, container);
    let dialog = await pickFolder(page, folder);
    await dialog.getByRole("button", { name: "Start upload" }).click();
    await finish(page, dialog);

    dialog = await pickFolder(page, folder);
    await expect(dialog.getByRole("heading", { name: "Rejected — nothing was written" })).toBeVisible();
    await expect(dialog.getByRole("button", { name: "Start upload" })).toHaveCount(0);
    await dialog.getByRole("button", { name: "Replace 3 existing objects…" }).click();
    await expect(dialog.getByText(words("This upload may REPLACE what is at 3 names, each only where it differs from your file."))).toBeVisible();
    await control.asked();
    await control.answer("overwrite", true);
    await dialog.getByRole("button", { name: "Start upload" }).click();
    expect(await finish(page, dialog)).toContain("3 already the same");

    const [question] = await overwriteQuestions();
    expect(question.title).toBe("Replace up to 3 objects?");
    const lines = question.text.split("\n");
    expect(lines[0]).toMatch(new RegExp(`^3 objects already in ${escapeRegExp(container)} may be REPLACED in ${binding}\\.$`));
    expect(lines[1]).toBe(`Replacing — ${[...digests.keys()].map((file) => `${name}/${file}`).join(", ")}`);
    expect(lines[2]).toMatch(/^Compared — each one is replaced only when it differs, as rclone copy decides: /);
    expect(lines[3]).toBe("Versioning observed — none: what is replaced is not kept");
    expect(lines[4]).toMatch(new RegExp(`^Segments — where a large object is replaced, the transfer engine REMOVES its old segments in ${escapeRegExp(container)}_segments `));
    expect(lines[5]).toBe(nativeNote);

    // The changed file's bytes are read back by the download journey below.
    appendFileSync(join(folder, "file01.bin"), "a changed line\n");
    digests.set("file01.bin", digestOf(join(folder, "file01.bin")));
    dialog = await pickFolder(page, folder);
    await dialog.getByRole("button", { name: "Replace 3 existing objects…" }).click();
    await control.answer("overwrite", true);
    await dialog.getByRole("button", { name: "Start upload" }).click();
    const details = await finish(page, dialog);
    expect(details).toContain("2 already the same");
    expect(details).not.toContain("3 already the same");
    await overwriteQuestions();
  });

  test("copy the container to another of the run's containers", async ({ page }, info) => {
    await openContainer(page, info.project.name, container);
    const copy = await previewCopy(page, copies);
    await copy.getByRole("button", { name: "Start copy" }).click();
    await finish(page, copy);
    await expect.poll(async () => (await control.names(info.project.name, copies)).sort(), lag)
      .toEqual([...digests.keys()].map((file) => `${name}/${file}`).sort());
  });

  // 0.1.0 step 2a-iii.
  test("upload one file onto an existing name: refused, declined, then replaced", async ({ page }, info) => {
    const target = info.project.name;
    const replacement = join(localFolder(), "file00.bin");
    writeFileSync(replacement, Buffer.alloc(100, 9));

    await openContainer(page, target, container);
    await openFolder(page, folder);
    let dialog = await pickFiles(page, replacement);
    await expect(dialog.getByRole("heading", { name: "Rejected — nothing was written" })).toBeVisible();
    await expect(dialog.locator(".problem", { hasText: "Already exists" })).toContainText("file00.bin");
    await expect(dialog.getByText(words("the application will ask you to confirm on its own dialog — not on this page — before anything is written."))).toBeVisible();
    await expect(dialog.getByRole("button", { name: "Start upload" })).toHaveCount(0);
    await dialog.getByRole("button", { name: "Replace 1 existing object…" }).click();
    // The collision comes back as a warning rather than a rejection.
    await expect(dialog.getByRole("heading", { name: "Rejected — nothing was written" })).toHaveCount(0);
    await expect(dialog.locator(".problem", { hasText: "Already exists" })).toContainText("file00.bin");
    await expect(dialog.getByText(words("This upload may REPLACE what is at 1 name, each only where it differs from your file."))).toBeVisible();
    await control.asked();
    await control.answer("overwrite", false);
    await dialog.getByRole("button", { name: "Start upload" }).click();
    await expect(dialog.getByText(declined)).toBeVisible();
    await expect(dialog.getByText(/^Job \S+ started\.$/)).toHaveCount(0);

    const [question] = await overwriteQuestions();
    expect(question.title).toBe("Replace up to 1 object?");
    const lines = question.text.split("\n");
    expect(lines[0]).toMatch(new RegExp(`^1 object already in ${escapeRegExp(`${container}/${name}/`)} may be REPLACED in ${binding}\\.$`));
    expect(lines[1]).toBe(`Replacing — ${name}/file00.bin`);
    expect(lines[3]).toBe("Versioning observed — none: what is replaced is not kept");
    expect(lines[4]).toMatch(new RegExp(`^Segments — where a large object is replaced, the transfer engine REMOVES its old segments under ${escapeRegExp(`${name}/`)} in ${escapeRegExp(container)}_segments `));
    expect(lines[5]).toBe(nativeNote);
    expect(await downloadObject(page, target, container, name, "file00.bin"), "after declining").toBe(digests.get("file00.bin"));

    await openContainer(page, target, container);
    await openFolder(page, folder);
    dialog = await pickFiles(page, replacement);
    await dialog.getByRole("button", { name: "Replace 1 existing object…" }).click();
    await control.answer("overwrite", true);
    await dialog.getByRole("button", { name: "Start upload" }).click();
    await finish(page, dialog);
    await overwriteQuestions();
    digests.set("file00.bin", digestOf(replacement));
    expect(await downloadObject(page, target, container, name, "file00.bin"), "after replacing").toBe(digests.get("file00.bin"));
  });

  // 0.1.0 step 7d and 0.5.0 step 19s: the source's file00.bin was replaced
  // above, so the copy's destination now differs from it.
  test("copy over a changed object: refused unasked, declined, then replaced", async ({ page }, info) => {
    const target = info.project.name;
    await openContainer(page, target, container);
    let copy = await previewCopy(page, copies);
    await expect(copy.locator(".count", { hasText: "overwrite refused" })).toHaveText(/^1\s+overwrite refused$/);
    await copy.getByRole("button", { name: "Start copy" }).click();
    const refused = await settle(page, copy);
    expect(refused.state).toBe("partial");
    expect(refused.details).toContain("1 overwrite refused");
    // The job's own record of the refused name says why it was left.
    await page.getByRole("button", { name: /jobs & changes/ }).click();
    // "Show in jobs" opened the job's details already.
    const details = page.getByRole("button", { name: `Item details for job ${refused.id}`, exact: true });
    if ((await details.getAttribute("aria-expanded")) !== "true") await details.click();
    const records = page.getByRole("region", { name: "Retained item records" });
    await expect(records.getByText("Last observed job state: partial.")).toBeVisible();
    await expect(records.locator(".totals div", { hasText: /^Refused/ }).locator("dd")).toHaveText("1");
    const record = records.locator("article.item-record", { hasText: `${name}/file00.bin` });
    await expect(record).toHaveCount(1);
    await expect(record.locator("p.observation", { hasText: "Execution:" })).toHaveText(/^\s*Execution:\s+refused · effects: not attempted\s+· planning: unsupported\s*$/);
    await page.getByRole("button", { name: /▾ hide/ }).click();
    expect(await downloadObject(page, target, copies, name, "file00.bin"), "after the refused overwrite").toBe(copied);

    await openContainer(page, target, container);
    const jobs = await jobIds(page);
    copy = await previewCopy(page, copies);
    await copy.getByRole("button", { name: "Replace existing objects…" }).click();
    await expect(copy.getByText(words("This copy will REPLACE what still differs at the destination."))).toBeVisible();
    await control.asked();
    await control.answer("overwrite", false);
    await copy.getByRole("button", { name: "Start copy" }).click();
    await expect(copy.getByText(declined)).toBeVisible();
    await expect(copy.getByText(/^Job \S+ started\.$/)).toHaveCount(0);

    const [question] = await overwriteQuestions();
    expect(question.title).toBe("Replace 1 object?");
    const lines = question.text.split("\n");
    expect(lines[0]).toMatch(new RegExp(`^1 object already in ${escapeRegExp(copies)} in ${binding} will be REPLACED by a copy from ${binding}\\.$`));
    expect(lines[1]).toBe(`Replacing — ${name}/file00.bin`);
    expect(lines[2]).toBe("Versioning observed — none: what is replaced is not kept");
    expect(lines[3]).toMatch(/^Segments — /);
    expect(lines[4]).toBe(nativeNote);
    await copy.getByRole("button", { name: "Close" }).first().click();
    expect(await jobIds(page), "jobs after declining").toEqual(jobs);
    expect(await downloadObject(page, target, copies, name, "file00.bin"), "after declining").toBe(copied);

    await openContainer(page, target, container);
    copy = await previewCopy(page, copies);
    await copy.getByRole("button", { name: "Replace existing objects…" }).click();
    await control.answer("overwrite", true);
    await copy.getByRole("button", { name: "Start copy" }).click();
    await finish(page, copy);
    await overwriteQuestions();
    expect(await downloadObject(page, target, copies, name, "file00.bin"), "after replacing").toBe(digests.get("file00.bin"));
  });

  // 0.5.0 step 19t. Confirming over files that are all the same is the
  // transfer journey's; this one declines, replaces one changed file, and
  // meets a file that appears after the preview.
  test("download into a folder: refused, declined, one changed file replaced, one that appears kept", async ({ page }, info) => {
    const target = info.project.name;
    const into = localFolder();
    await openContainer(page, target, container);
    let dialog = await pickDownload(page, into);
    await dialog.getByRole("button", { name: "Start download" }).click();
    await finish(page, dialog);
    // The bytes the upload journeys above left in the store.
    expect(filesUnder(into)).toEqual([...digests.keys()].map((file) => `${name}/${file}`).sort());
    for (const [file, digest] of digests) expect(digestOf(join(into, name, file)), file).toBe(digest);

    const small = join(into, name, "file00.bin");
    appendFileSync(small, "a line of my own\n");
    const mine = digestOf(small);
    dialog = await pickDownload(page, into);
    await expect(dialog.getByRole("heading", { name: "Rejected — nothing was written" })).toBeVisible();
    for (const file of digests.keys()) await expect(dialog.locator(".problem", { hasText: `${name}/${file}` }).first()).toBeVisible();
    await expect(dialog.getByRole("button", { name: "Start download" })).toHaveCount(0);
    await dialog.getByRole("button", { name: "Replace existing files…" }).click();
    await expect(dialog.getByText(words("This download may REPLACE 3 files in the folder, each only where it differs from the object. A replaced file is not kept."))).toBeVisible();
    await control.asked();
    await control.answer("overwrite", false);
    await dialog.getByRole("button", { name: "Start download" }).click();
    await expect(dialog.getByText(declined)).toBeVisible();
    await expect(dialog.getByText(/^Job \S+ started\.$/)).toHaveCount(0);
    expect(digestOf(small), "my change after declining").toBe(mine);

    const [question] = await overwriteQuestions();
    expect(question.title).toBe("Replace up to 3 local files?");
    const lines = question.text.split("\n");
    expect(lines[0]).toMatch(new RegExp(`^3 files already in the folder ${escapeRegExp(into)} may be REPLACED by the objects of ${escapeRegExp(container)} in ${binding}\\.$`));
    expect(lines[1]).toBe(`Replacing — ${[...digests.keys()].map((file) => `${name}/${file}`).join(", ")}`);
    expect(lines[2]).toMatch(/^Compared — each one is replaced only when it differs, as rclone copy decides: /);
    expect(lines[3]).toBe("A replaced file is gone: it is not kept or moved to the trash.");
    expect(lines[4]).toBe(nativeNote);

    await dialog.getByRole("button", { name: "Close" }).first().click();
    dialog = await pickDownload(page, into);
    await dialog.getByRole("button", { name: "Replace existing files…" }).click();
    await control.answer("overwrite", true);
    await dialog.getByRole("button", { name: "Start download" }).click();
    const details = await finish(page, dialog);
    expect(details).toContain("2 already the same");
    await overwriteQuestions();
    for (const [file, digest] of digests) expect(digestOf(join(into, name, file)), file).toBe(digest);

    // A file put in the folder between the preview and the start is not
    // replaced. The plan has the job end partial for that name; the start
    // checks the folder again first and refuses the whole download, so the
    // file is kept, nothing is written and no job starts.
    const fresh = localFolder();
    dialog = await pickDownload(page, fresh);
    await expect(dialog.getByRole("button", { name: "Start download" })).toBeVisible();
    mkdirSync(join(fresh, name));
    const appeared = join(fresh, name, "file01.bin");
    writeFileSync(appeared, "my own file\n");
    await dialog.getByRole("button", { name: "Start download" }).click();
    await expect(dialog.getByRole("alert")).toHaveText("preflight found problems; resolve them and retry (nothing was written)");
    await expect(dialog.getByText(/^Job \S+ started\.$/)).toHaveCount(0);
    expect(digestOf(appeared), "the file that appeared").toBe(sha256(Buffer.from("my own file\n")));
    expect(filesUnder(fresh), "what the folder holds").toEqual([`${name}/file01.bin`]);
  });
});

async function pickFolder(page: Page, folder: string): Promise<Locator> {
  await page.getByRole("button", { name: "Upload…" }).click();
  const dialog = page.getByRole("dialog", { name: "Upload" });
  await control.pickFolder(folder);
  await dialog.getByRole("button", { name: "Choose folder…" }).click();
  return dialog;
}

async function pickFiles(page: Page, file: string): Promise<Locator> {
  await page.getByRole("button", { name: "Upload…" }).click();
  const dialog = page.getByRole("dialog", { name: "Upload" });
  await control.pickFiles([file]);
  await dialog.getByRole("button", { name: "Choose files…" }).click();
  return dialog;
}

async function pickDownload(page: Page, into: string): Promise<Locator> {
  await page.getByRole("button", { name: "Download container…" }).click();
  const dialog = page.getByRole("dialog", { name: "Download" });
  await control.pickFolder(into);
  await dialog.getByRole("button", { name: "Choose directory…" }).click();
  return dialog;
}

async function previewCopy(page: Page, destination: string): Promise<Locator> {
  await page.getByRole("button", { name: "Copy container…" }).click();
  const copy = page.getByRole("dialog", { name: "Transfer" });
  await copy.getByLabel("Destination container").fill(destination);
  await copy.getByRole("button", { name: "Preview" }).click();
  await expect(copy.getByRole("button", { name: "Start copy" })).toBeEnabled();
  return copy;
}

// The overwrite questions asked since the last read, which also clears
// what the pickers were asked.
async function overwriteQuestions(): Promise<{ title: string; text: string }[]> {
  const questions = (await control.asked()).filter((asked) => asked.kind === "overwrite");
  expect(questions).toHaveLength(1);
  return questions;
}

// downloadObject downloads one object of a folder into a fresh
// folder and returns its digest: the bytes the store holds at that name.
async function downloadObject(page: Page, target: string, container: string, folder: string, file: string): Promise<string> {
  const into = localFolder();
  await openContainer(page, target, container);
  await openFolder(page, folder);
  await objectRow(page, file).click();
  await page.getByRole("button", { name: "Download object…" }).click();
  const dialog = page.getByRole("dialog", { name: "Download" });
  await control.pickFolder(into);
  await dialog.getByRole("button", { name: "Choose directory…" }).click();
  await dialog.getByRole("button", { name: "Start download" }).click();
  await finish(page, dialog);
  return digestOf(join(into, file));
}

// settle is finish for a job that may end partial: its id, its state and
// the text of its details as the drawer shows them.
async function settle(page: Page, dialog: Locator): Promise<{ id: string; state: string; details: string }> {
  const started = await dialog.getByText(/^Job \S+ started\.$/).textContent();
  const id = started!.split(" ")[1];
  await dialog.getByRole("button", { name: "Show in jobs" }).click();
  const row = page.locator("div.job", { has: page.getByRole("button", { name: `Item details for job ${id}`, exact: true }) });
  const ended = row.getByText(/^(complete|partial|cancelled|failed)$/);
  await expect(ended).toBeVisible({ timeout: 8 * 60 * 1000 });
  const state = (await ended.textContent())!;
  const details = await row.innerText();
  await page.getByRole("button", { name: /▾ hide/ }).click();
  return { id, state, details };
}

function filesUnder(dir: string): string[] {
  return readdirSync(dir, { recursive: true, withFileTypes: true })
    .filter((entry) => entry.isFile())
    .map((entry) => relative(dir, join(entry.parentPath, entry.name)))
    .sort();
}


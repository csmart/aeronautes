import { test } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { basename, join } from "node:path";
import { control } from "../control.ts";
import {
  account, digestOf, expect, finish, inspector, localFolder, objectRow, openContainer, openFolder, ownContainer, sha256, writeFiles,
} from "../helpers.ts";

// The words a person reads on the native surfaces the page cannot draw
// (ADR 0073 option B, ADR 0081 decision 3): each chooser's title names the
// bookmark, its account, the container and the prefix the pick binds to,
// and the public-read question names the container and the account.

// The account is the storage URL's last segment. The mock's is fixed; a
// real cloud's is its own, so there it is matched rather than named.
function accountOf(target: string): string {
  return target === "mock" ? "AUTH_test" : "[^()]+";
}

function escaped(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

test("the choosers' titles name the bookmark, its account, the container and the prefix", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-titles");
  const binding = `${escaped(await account(target))} \\(${accountOf(target)}\\)`;
  const folder = localFolder();
  writeFiles(folder, [16], 1);
  const loose = join(localFolder(), "picked.bin");
  const body = Buffer.alloc(48, 5);
  writeFileSync(loose, body);

  await openContainer(page, target, container);
  await control.asked();
  await page.getByRole("button", { name: "Upload…" }).click();
  let dialog = page.getByRole("dialog", { name: "Upload" });
  await control.pickFolder(folder);
  await dialog.getByRole("button", { name: "Choose folder…" }).click();
  await dialog.getByRole("button", { name: "Start upload" }).click();
  await finish(page, dialog);
  let asked = await control.asked();
  expect(asked).toHaveLength(1);
  expect(asked[0].kind).toBe("pick folder");
  expect(asked[0].title).toMatch(new RegExp(`^Upload into ${binding}, ${escaped(container)} — choose a folder…$`));

  // 2a-ii: files chosen inside a prefix arrive under it with their bytes.
  await page.getByRole("button", { name: "Refresh listing" }).click();
  await openFolder(page, folder);
  const prefix = `${basename(folder)}/`;
  await page.getByRole("button", { name: "Upload…" }).click();
  dialog = page.getByRole("dialog", { name: "Upload" });
  await control.pickFiles([loose]);
  await dialog.getByRole("button", { name: "Choose files…" }).click();
  await dialog.getByRole("button", { name: "Start upload" }).click();
  await finish(page, dialog);
  asked = await control.asked();
  expect(asked).toHaveLength(1);
  expect(asked[0].kind).toBe("pick files");
  expect(asked[0].title).toMatch(new RegExp(`^Upload into ${binding}, ${escaped(container + "/" + prefix)} — choose files…$`));
  expect(await control.names(target, container)).toContain(`${prefix}picked.bin`);

  // 2a: the download chooser for an object in that prefix.
  await page.getByRole("button", { name: "Refresh listing" }).click();
  await objectRow(page, "picked.bin").click();
  await page.getByRole("button", { name: "Download object…" }).click();
  dialog = page.getByRole("dialog", { name: "Download" });
  const into = localFolder();
  await control.pickFolder(into);
  await dialog.getByRole("button", { name: "Choose directory…" }).click();
  await dialog.getByRole("button", { name: "Start download" }).click();
  await finish(page, dialog);
  asked = await control.asked();
  expect(asked).toHaveLength(1);
  expect(asked[0].kind).toBe("pick folder");
  expect(asked[0].title).toMatch(new RegExp(`^Download from ${binding}, ${escaped(container + "/" + prefix)} into…$`));
  expect(digestOf(join(into, "picked.bin")), "the uploaded file's bytes").toBe(sha256(body));
});

// 2b's refusal and its words. Making a container public when the question
// is answered yes is the transfer journey's; this one answers no.
test("the public-read question names the container and account, and no leaves it private", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-private");
  await openContainer(page, target, container);
  await control.asked();
  await page.getByRole("button", { name: "Configure container" }).click();
  await page.getByLabel("Anyone can read objects").check();
  await page.getByLabel(`I understand this makes ${container} publicly readable.`).check();
  await control.answer("public", false);
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText(`making ${container} publicly readable was not confirmed; nothing was written`)).toBeVisible();

  const asked = await control.asked();
  expect(asked).toHaveLength(1);
  expect(asked[0].kind).toBe("public");
  expect(asked[0].title).toBe("Make this container public?");
  expect(asked[0].text).toMatch(new RegExp(
    `^Anyone on the internet will be able to read every object in ${escaped(container)} \\(${accountOf(target)}\\)\\.\\n`
    + "This question comes from the application, not from the page\\.$"));

  // Read after connecting again, not from the panel the refusal left open.
  await openContainer(page, target, container);
  await expect(inspector(page).getByText("Read ACL", { exact: true })).toBeVisible();
  await expect(inspector(page).getByText("Anyone with the URL can read objects in this container")).toHaveCount(0);
  await expect(page.getByRole("note", { name: /^Public: / })).toHaveCount(0);
});

import { test, type Locator, type Page } from "@playwright/test";
import { randomBytes } from "node:crypto";
import { readdirSync, readFileSync } from "node:fs";
import { basename, join } from "node:path";
import { control } from "../control.ts";
import {
  account, addMockBookmark, connect, deleteBookmark, expect, finish, inspector, localFolder, objectRow, openContainer, openFolder, ownContainer, writeFiles,
} from "../helpers.ts";

// Test plan step 11a. Account metadata is written only on the mock: a real
// account's edit has to carry every key someone else set, which layer 1
// does with its own key.
test("edit the account's metadata and find the write in Recent changes", async ({ page }, info) => {
  test.skip(info.project.name !== "mock", "account metadata is written on the mock only");
  const key = `Journey-${randomBytes(4).toString("hex")}`;
  const name = `journey audit ${randomBytes(4).toString("hex")}`;
  await connect(page, "mock");
  // A new bookmark attributes exactly one write to this journey, even
  // when other journeys have already changed the same mock account.
  await addMockBookmark(page, name);
  try {
    await inspector(page).getByRole("button", { name: "Edit metadata" }).click();
    await page.getByRole("button", { name: "+ add key" }).click();
    await page.getByLabel("metadata key").last().fill(key);
    await page.getByLabel("metadata value").last().fill("account-value");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(inspector(page).getByRole("button", { name: "Edit metadata" })).toBeEnabled();

    // Read back over a fresh connection, not from the page's memory.
    await page.reload();
    const tile = page.getByRole("navigation", { name: "Bookmarks" }).getByRole("button", { name, exact: true });
    await tile.click();
    await expect(tile).toHaveAttribute("aria-pressed", "true");
    const metadata = card(page, "Account metadata");
    await expect(metadata.locator(".kv", { hasText: key })).toContainText("account-value");

    await page.getByRole("button", { name: /jobs & changes/ }).click();
    await page.getByRole("tab", { name: "Recent changes" }).click();
    const bookmark = page.getByRole("combobox", { name: "Bookmark", exact: true });
    const option = bookmark.getByRole("option", { name: new RegExp(`^${name} \\(`) });
    await bookmark.selectOption((await option.getAttribute("value"))!);
    const bookmarkId = await bookmark.inputValue();
    const operation = page.getByRole("textbox", { name: "Operation prefix" });
    await operation.fill("account.meta");
    await operation.press("Tab");
    const result = page.getByRole("combobox", { name: "Result", exact: true });
    await result.selectOption("applied");
    const write = page.locator(".event");
    await expect(write).toHaveCount(1);
    await expect(write.locator(".op")).toHaveText("account.metadata.update");
    await expect(write.locator(".audit-bookmark")).toHaveText(`${name} (${bookmarkId})`);

    const exportRows = async () => {
      const into = localFolder();
      await control.pickFolder(into);
      await page.getByRole("button", { name: "Export log…" }).click();
      await expect(page.getByText(/^Exported \d+ events? to /)).toBeVisible();
      const files = readdirSync(into).filter((file) => file.endsWith(".jsonl"));
      expect(files, "one completed export").toHaveLength(1);
      return readFileSync(join(into, files[0]), "utf8").split("\n").filter(Boolean).map((line) => JSON.parse(line));
    };
    await result.selectOption("failed");
    await expect(page.getByText("No matching audit events.")).toBeVisible();
    expect(await exportRows(), "the export respects the empty result filter").toEqual([]);
    await result.selectOption("applied");
    await expect(write).toHaveCount(1);
    await operation.fill("no-such-operation.");
    await operation.press("Tab");
    await expect(page.getByText("No matching audit events.")).toBeVisible();
    await operation.fill("account.meta");
    await operation.press("Tab");
    await expect(write).toHaveCount(1);
    expect(await exportRows()).toEqual([expect.objectContaining({
      bookmarkId, operation: "account.metadata.update", resource: "account", result: "applied",
    })]);
  } finally {
    await page.goto("/");
    await deleteBookmark(page, name);
  }
});

// Test plan step 11b: the account's cards and its raw headers, which start
// closed and show a TempURL key's value as "[redacted]"; the container's
// and the object's raw headers start closed too. Only the mock's account
// is given a key, and its own is cleared afterwards.
test("read the account's cards and every pane's raw headers", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-raw");
  await connect(page, target);
  // Every target reports what the account holds under each storage policy
  // its containers use; the mock's containers use "standard".
  // Counts are shortened on a large account ("2.1K objects").
  await expect(card(page, "Storage policies").locator(".kv").first()).toContainText(/ · [\d.,]+[KMG]? objects? · [\d.,]+[KMG]? containers?\s*$/);
  const accountHeaders = await openRawHeaders(page);
  await expect(accountHeaders).toContainText(/X-Account-Container-Count: \d/i);

  if (target === "mock") {
    const secret = randomBytes(16).toString("hex");
    await setKey(page, "Temp-URL-Key", secret);
    try {
      await connect(page, target);
      const headers = await openRawHeaders(page);
      await expect(headers).toContainText(/X-Account-Meta-Temp-Url-Key: \[redacted\]/i);
      await expect(headers).not.toContainText(secret);
    } finally {
      // The mock's account is shared by every journey after this one.
      await connect(page, target);
      await inspector(page).getByRole("button", { name: "Clear Temp-URL-Key…" }).click();
      await inspector(page).getByRole("button", { name: "Clear key" }).click();
      await expect(inspector(page).getByRole("button", { name: "Set Temp-URL-Key…" })).toBeVisible();
    }
  }

  await openContainer(page, target, container);
  const folder = localFolder();
  writeFiles(folder, [16], 1);
  await page.getByRole("button", { name: "Upload…" }).click();
  const upload = page.getByRole("dialog", { name: "Upload" });
  await control.pickFolder(folder);
  await upload.getByRole("button", { name: "Choose folder…" }).click();
  await upload.getByRole("button", { name: "Start upload" }).click();
  await finish(page, upload);
  await page.getByRole("button", { name: "Refresh listing" }).click();
  await expect(await openRawHeaders(page)).toContainText(/X-Container-Object-Count: \d/i);
  await openFolder(page, basename(folder));
  await objectRow(page, "file00.bin").click();
  await expect(await openRawHeaders(page)).toContainText(/Etag: \S/i);
});

// Test plan step 12a, on the mock only: journeys never write a real
// account's TempURL keys. A key is set, then rotated by setting the second
// slot and clearing the first; a fresh connection shows which slots are
// set, and the raw headers show the second only as "[redacted]".
test("set a container's TempURL key and rotate it to the second slot", async ({ page }, info) => {
  test.skip(info.project.name !== "mock", "TempURL keys are written on the mock only");
  const container = await ownContainer("mock", "ui-tempurl");
  const first = randomBytes(16).toString("hex");
  const second = randomBytes(16).toString("hex");
  await openContainer(page, "mock", container);
  await setKey(page, "Temp-URL-Key", first);
  await setKey(page, "Temp-URL-Key-2", second);
  await inspector(page).getByRole("button", { name: "Clear Temp-URL-Key…" }).click();
  await inspector(page).getByRole("button", { name: "Clear key" }).click();

  await openContainer(page, "mock", container);
  const keys = inspector(page).getByRole("region", { name: "TempURL keys" });
  await expect(keys.getByRole("button", { name: "Set Temp-URL-Key…" })).toBeVisible();
  await expect(keys.getByRole("button", { name: "Replace Temp-URL-Key-2…" })).toBeVisible();
  const headers = await openRawHeaders(page);
  await expect(headers).toContainText(/X-Container-Meta-Temp-Url-Key-2: \[redacted\]/i);
  await expect(headers).not.toContainText(/X-Container-Meta-Temp-Url-Key:/i);
  await expect(headers).not.toContainText(first);
  await expect(headers).not.toContainText(second);

  // The container editor sends the whole form, which shows no key, so a
  // save of another setting must leave the key where it is.
  await page.getByRole("button", { name: "Configure container" }).click();
  await page.getByLabel("Allowed origins").fill("https://journey.example");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("button", { name: "Configure container" })).toBeEnabled();
  await openContainer(page, "mock", container);
  await expect(keys.getByRole("button", { name: "Replace Temp-URL-Key-2…" })).toBeVisible();
  await expect(await openRawHeaders(page)).toContainText("X-Container-Meta-Access-Control-Allow-Origin: https://journey.example");
});

// Test plan steps 12b and 19c: allowed CORS origins are read back over a
// fresh connection, and the container's row then carries a "cors" badge
// whose tooltip names the setting and when it was observed, still there
// after switching to another bookmark and back.
test("give a container CORS origins and keep its badge across a switch", async ({ page }, info) => {
  const target = info.project.name;
  const container = await ownContainer(target, "ui-cors");
  const origin = "https://journey.example";
  await openContainer(page, target, container);
  await page.getByRole("button", { name: "Configure container" }).click();
  await page.getByLabel("Allowed origins").fill(origin);
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("button", { name: "Configure container" })).toBeEnabled();

  const name = await connect(page, target);
  await page.getByRole("button", { name: new RegExp(`^${container} `) }).click();
  await expect(inspector(page).locator(".kv", { has: page.getByText("CORS", { exact: true }) })).toContainText("configured");
  await expect(await openRawHeaders(page)).toContainText(`X-Container-Meta-Access-Control-Allow-Origin: ${origin}`);
  await page.getByRole("button", { name: "Configure container" }).click();
  await expect(page.getByLabel("Allowed origins")).toHaveValue(origin);
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  const badge = containerRow(page, container).getByText("cors", { exact: true });
  await expect(badge).toHaveAttribute("title", /^CORS configured · observed \S/);

  // Local runs switch to the disposable mock; only a cloud run has selected
  // another provider account. The mock's two bookmarks name one account.
  const other = target === "mock" || target === "saio"
    ? await secondMockBookmark(page) : await account(target === "ceph" ? "swift" : "ceph");
  // A cloud's bookmark is made in Go when its account is first opened,
  // which can be after this page loaded its rail.
  await page.reload();
  const rail = page.getByRole("navigation", { name: "Bookmarks" });
  await rail.getByRole("button", { name: other, exact: true }).click();
  await expect(page.getByText(`connected to ${other}`, { exact: true })).toBeVisible();
  await rail.getByRole("button", { name, exact: true }).click();
  await expect(page.getByText(`connected to ${name}`, { exact: true })).toBeVisible();
  await expect(badge).toHaveAttribute("title", /^CORS configured · observed \S/);
});

test("save and clear container quotas and a versions location", async ({ page }, info) => {
  test.skip(info.project.name !== "saio", "the local Swift fixture offers quotas and version archives");
  const target = info.project.name;
  const container = await ownContainer(target, "ui-quota-version");
  const archive = await ownContainer(target, "ui-quota-archive");
  // Go owns the store's quota rules. This journey protects the form's
  // fields and their readback without duplicating enforcement scenarios.
  for (const enabled of [true, false]) {
    const bytes = enabled ? "8192" : "0";
    const count = enabled ? "7" : "0";
    const mode = enabled ? "versions_location" : "off";
    await openContainer(page, target, container);
    await page.getByRole("button", { name: "Configure container" }).click();
    await page.getByLabel("Quota bytes (0 = none)").fill(bytes);
    await page.getByLabel("Quota objects (0 = none)").fill(count);
    await page.getByRole("combobox", { name: "Versioning" }).selectOption(mode);
    if (enabled) await page.getByLabel("Archive container").fill(archive);
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByRole("button", { name: "Configure container" })).toBeVisible();

    await openContainer(page, target, container);
    const headers = await openRawHeaders(page);
    if (enabled) {
      await expect(headers).toContainText(/X-Container-Meta-Quota-Bytes: 8192/i);
      await expect(headers).toContainText(/X-Container-Meta-Quota-Count: 7/i);
      await expect(headers).toContainText(new RegExp(`X-Versions-Location: ${archive}`, "i"));
    } else {
      await expect(headers).not.toContainText(/X-Container-Meta-Quota-(Bytes|Count):/i);
      await expect(headers).not.toContainText(/X-(Versions|History)-Location:/i);
    }
    await page.getByRole("button", { name: "Configure container" }).click();
    await expect(page.getByLabel("Quota bytes (0 = none)")).toHaveValue(bytes);
    await expect(page.getByLabel("Quota objects (0 = none)")).toHaveValue(count);
    await expect(page.getByRole("combobox", { name: "Versioning" })).toHaveValue(mode);
    if (enabled) await expect(page.getByLabel("Archive container")).toHaveValue(archive);
    else await expect(page.getByLabel("Archive container")).toHaveCount(0);
    await page.getByRole("button", { name: "Cancel", exact: true }).click();
  }
});

// Test plan step 18b, the inspector's half: an ordinary container has no
// segment classification card, and the TempURL card says in one line what
// a key does, with the rotation under its own heading.
test("an ordinary container's inspector", async ({ page }, info) => {
  const container = await ownContainer(info.project.name, "ui-ordinary");
  await openContainer(page, info.project.name, container);
  await expect(inspector(page).getByRole("button", { name: "Configure container" })).toBeVisible();
  await expect(inspector(page).getByRole("heading", { name: "Segment classification" })).toHaveCount(0);
  const keys = inspector(page).getByRole("region", { name: "TempURL keys" });
  await expect(keys.locator("p.teach").first()).toHaveText("Either key signs temporary URLs. Values are never shown.");
  const rotation = keys.getByText("Set the other slot, move your signers to it, then clear this one.");
  await expect(rotation).toBeHidden();
  await keys.getByText("How to rotate a key").click();
  await expect(rotation).toBeVisible();
});

function card(page: Page, title: string): Locator {
  return inspector(page).locator("section", { has: page.getByRole("heading", { name: title, exact: true }) });
}

function containerRow(page: Page, container: string): Locator {
  return page.getByRole("button", { name: new RegExp(`^${container} `) });
}

// openRawHeaders checks that the pane's raw headers start closed, opens
// them, and returns what they show.
async function openRawHeaders(page: Page): Promise<Locator> {
  const raw = inspector(page).locator("details", { hasText: "Raw headers" });
  const shown = raw.locator("pre");
  await expect(raw).toBeVisible();
  await expect(shown).toBeHidden();
  await raw.getByText("Raw headers", { exact: true }).click();
  await expect(shown).toBeVisible();
  return shown;
}

async function setKey(page: Page, slot: string, secret: string) {
  await inspector(page).getByRole("button", { name: `Set ${slot}…` }).click();
  const form = inspector(page).getByRole("form", { name: `Set ${slot}` });
  await form.getByLabel(`New key for ${slot}`).fill(secret);
  await form.getByRole("button", { name: "Set key" }).click();
  await expect(form).toHaveCount(0);
  await expect(inspector(page).getByRole("button", { name: `Replace ${slot}…` })).toBeVisible();
}

// secondMockBookmark makes a second bookmark of the mock's account through
// the editor, once per run, and returns its name.
async function secondMockBookmark(page: Page): Promise<string> {
  const name = "journey mock second";
  const rail = page.getByRole("navigation", { name: "Bookmarks" });
  if (await rail.getByRole("button", { name, exact: true }).count() === 0) {
    await addMockBookmark(page, name, { key: "testing", connect: false });
  }
  return name;
}

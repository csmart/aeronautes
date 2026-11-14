import { test } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { control } from "../control.ts";
import { connect, expect, expectInside, localFolder } from "../helpers.ts";

// ADR 0114 through the window: an rclone.conf with one account the mock
// accepts and one whose key it refuses. Driven against the mock only, whose
// key is a fixed test key, so no real secret is written to disk.
test("import bookmarks from an rclone config", async ({ page }, info) => {
  test.skip(info.project.name !== "mock", "the import is driven against the mock only");
  const refused = "the identity service refused these credentials";
  const conf = join(localFolder(), "rclone.conf");
  writeFileSync(conf, [
    "[journey imported]", "type = swift", "user = test:tester", "key = testing", `auth = ${process.env.AERONAUTES_MOCK}`, "",
    "[journey refused]", "type = swift", "user = test:tester", "key = not-the-key", `auth = ${process.env.AERONAUTES_MOCK}`, "",
  ].join("\n"));

  // Connecting first leaves a bookmark saved; with none, the welcome
  // would cover Settings when this journey runs by itself.
  await connect(page, "mock");
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await control.pickFiles([conf]);
  await page.getByRole("button", { name: "Import bookmarks…" }).click();
  const dialog = page.getByRole("dialog", { name: "Import bookmarks" });
  // An earlier journey may have bookmarked the same account, which leaves
  // these rows unticked as duplicates; the operator ticks them either way.
  await dialog.getByLabel("Import journey imported").check();
  await dialog.getByLabel("Import journey refused").check();
  await expect(dialog.getByLabel("Verify after import (signs in and reads each account; nothing is changed)")).toBeChecked();
  await dialog.getByRole("button", { name: "Import 2 bookmarks" }).click();
  await expect(dialog.getByText("Imported and verified.")).toBeVisible();
  await expect(dialog.getByText(`Imported, but did not verify: ${refused}.`)).toBeVisible();
  await dialog.locator("footer").getByRole("button", { name: "Close" }).click();

  const rail = page.getByRole("navigation", { name: "Bookmarks" });
  await expect(rail.getByRole("img", { name: `Did not verify: ${refused}` })).toBeVisible();
  const imported = rail.getByRole("button", { name: "journey imported", exact: true });
  await imported.click();
  await expect(imported).toHaveAttribute("aria-pressed", "true");
});

// ADR 0118 through the window: the import is started from the rail, a row
// is saved under the name typed for it, and a row whose source holds no key
// becomes a bookmark that is marked, opens its editor when chosen, and
// connects once the key is entered there.
test("import from the rail, name a bookmark and give a keyless one its key", async ({ page }, info) => {
  test.skip(info.project.name !== "mock", "the import is driven against the mock only");
  const conf = join(localFolder(), "rclone.conf");
  writeFileSync(conf, [
    "[journey source]", "type = swift", "user = test:tester", "key = testing", `auth = ${process.env.AERONAUTES_MOCK}`, "",
    "[journey keyless]", "type = swift", "user = test:tester", `auth = ${process.env.AERONAUTES_MOCK}`, "",
  ].join("\n"));

  // Connecting first leaves a bookmark saved, so the welcome is not in the
  // way of the rail whichever journey ran before this one.
  await connect(page, "mock");
  const rail = page.getByRole("navigation", { name: "Bookmarks" });
  await control.pickFiles([conf]);
  await rail.getByTitle("Import bookmarks from an rclone.conf or a clouds.yaml").click();
  const dialog = page.getByRole("dialog", { name: "Import bookmarks" });
  await dialog.getByLabel("Import journey source").check();
  await dialog.getByLabel("Import journey keyless").check();
  await dialog.getByLabel("Name for journey source").fill("journey named");
  await expectInside(dialog);
  await dialog.getByRole("button", { name: "Import 2 bookmarks" }).click();
  await expect(dialog.getByText("Imported and verified.")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Enter key for journey keyless" })).toBeVisible();
  await expectInside(dialog);
  await dialog.locator("footer").getByRole("button", { name: "Close" }).click();

  await expect(rail.getByRole("button", { name: "journey named", exact: true })).toBeVisible();
  await expect(rail.getByRole("button", { name: "journey source", exact: true })).toHaveCount(0);
  const keyless = rail.getByRole("button", { name: "journey keyless", exact: true });
  await expect(keyless.getByRole("img", { name: "No saved key" })).toBeVisible();
  await keyless.click();
  await expect(page.getByText("This bookmark has no saved key. Enter it below to connect.")).toBeVisible();
  await expect(page.getByLabel("API key")).toBeFocused();
  await page.getByLabel("API key").fill("testing");
  await page.getByRole("button", { name: "Save & connect" }).click();
  await expect(keyless).toHaveAttribute("aria-pressed", "true");
  await expect(keyless.getByRole("img", { name: "No saved key" })).toHaveCount(0);
});

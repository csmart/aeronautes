import { test } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { control } from "../control.ts";
import { expect, localFolder } from "../helpers.ts";

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

  await page.goto("/");
  await page.getByRole("button", { name: "Settings" }).click();
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
  await rail.getByRole("button", { name: "journey imported", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Containers" })).toBeVisible();
});

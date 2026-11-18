import { test } from "@playwright/test";
import { control } from "../control.ts";
import { expect, finish, localFolder, objectRow, openContainer, openFolder, ownContainer, writeFiles } from "../helpers.ts";

// ADR 0074 decision 6: with the retention ceiling at its least, 1,000, a
// folder of 1,001 objects shows its first 1,000, then the next page shows
// the one after them in place of the rest, and back to the start shows the
// first again (found by the operator: the list stopped at the ceiling with
// nothing to press).
test("page a folder past the retention ceiling and back", async ({ page }, info) => {
  test.skip(info.project.name !== "mock", "a thousand uploads are driven against the mock only");
  test.setTimeout(300_000);
  const container = await ownContainer(info.project.name, "ui-paging");
  const folder = localFolder();
  const names = [...writeFiles(folder, Array(1001).fill(1), 1).keys()];
  // The store lists names in byte order.
  names.sort((a, b) => Buffer.compare(Buffer.from(a), Buffer.from(b)));

  await openContainer(page, info.project.name, container);
  await page.getByRole("button", { name: "Upload…" }).click();
  const upload = page.getByRole("dialog", { name: "Upload" });
  await control.pickFolder(folder);
  await upload.getByRole("button", { name: "Choose folder…" }).click();
  await upload.getByRole("button", { name: "Start upload" }).click();
  await finish(page, upload);

  const retention = async (rows: string, shown: string) => {
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    const field = page.getByLabel("Browser retention");
    await field.fill(rows);
    await field.press("Tab");
    await expect(page.getByText(`keeps ${shown} rows`)).toBeVisible();
    await page.getByRole("button", { name: "Close settings" }).click();
  };
  await retention("1000", "1,000");
  try {
    await page.getByRole("button", { name: "Refresh listing" }).click();
    await openFolder(page, folder);
    const foot = page.locator(".pane-foot");
    await expect(foot.getByText(/1,000 rows loaded, the retention ceiling/)).toBeVisible();
    await objectRow(page, names[0]).click();

    await foot.getByRole("button", { name: "Next 1,000 →" }).click();
    await expect(foot.getByText("rows 1,001 to 1,001")).toBeVisible();
    await expect(objectRow(page, names[1000])).toBeVisible();
    await expect(objectRow(page, names[0])).toHaveCount(0);
    await expect(foot.getByText("none selected")).toBeVisible();

    await foot.getByRole("button", { name: "← Back to the start" }).click();
    await expect(objectRow(page, names[0])).toBeVisible();
    await expect(foot.getByText(/1,000 rows loaded/)).toBeVisible();
  } finally {
    await retention("100000", "100,000");
  }
});

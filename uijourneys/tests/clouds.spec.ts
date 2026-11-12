import { test, type Page } from "@playwright/test";
import { account, connect, expect, ownContainer } from "../helpers.ts";

// Switching accounts from the rail must show each account's own containers
// and nothing of the other's. It needs both clouds, so it runs once, under
// the last one.
test("switch between the two clouds", async ({ page }, info) => {
  test.skip(info.project.name !== "swift", "runs once, under the last cloud");
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

async function shows(page: Page, present: string, absent: string) {
  await expect(page.getByRole("button", { name: new RegExp(`^${present} `) })).toBeVisible();
  await expect(page.getByRole("button", { name: new RegExp(`^${absent} `) })).toHaveCount(0);
}

import { test, type Locator, type Page } from "@playwright/test";
import { connect, expect, ownContainer } from "../helpers.ts";

// ADR 0117 through the window, on every target: a container named like a
// segment store sits behind the "Possible backstage" heading, and its
// listing and its delete dialog say what its objects may be. The container
// is the sidecar name the run claimed, made empty through the Create
// dialog, so teardown removes it; nothing is deleted here.
test("find a container named like a segment store behind its heading", async ({ page }, info) => {
  const sidecar = `${await ownContainer(info.project.name, "ui-backstage", true)}_segments`;
  await connect(page, info.project.name);
  await page.getByTitle("Create container").click();
  const create = page.getByRole("dialog", { name: "Create container" });
  await create.getByLabel("Name").fill(sidecar);
  await create.getByRole("button", { name: "Create" }).click();
  await expect(create).toBeHidden();

  // Open the group once to see that the store lists the new container.
  const heading = page.getByRole("button", { name: /^Possible backstage · [1-9]/ });
  const row = page.getByRole("button", { name: new RegExp(`^${sidecar} `) });
  await refreshUntilListed(page, heading);
  await heading.click();
  await refreshUntilListed(page, row);

  // A window opened afresh starts with the group closed.
  await connect(page, info.project.name);
  await refreshUntilListed(page, heading);
  await expect(heading).toHaveAttribute("aria-expanded", "false");
  await expect(row).toBeHidden();
  await heading.click();
  await refreshUntilListed(page, row);
  await row.click();
  await expect(page.getByRole("navigation", { name: "Path" }).getByRole("button", { name: sidecar })).toBeVisible();
  const caution = "This container is named like a segment store. Objects here may be segments of larger objects, and deleting one can break the object it belongs to.";
  await expect(page.getByText(caution)).toBeVisible();

  await row.click({ button: "right" });
  await page.getByRole("menuitem", { name: "Empty or delete container…" }).click();
  const dialog = page.getByRole("dialog", { name: "Delete" });
  await expect(dialog.getByText(caution)).toBeVisible();
  await dialog.getByRole("button", { name: "Close" }).click();
});

// The window reads the container list when it connects. A container made
// afterwards outside the window, here by the run's own session, shows once
// the column's Refresh is pressed.
test("refresh the container list after a change made elsewhere", async ({ page }, info) => {
  await connect(page, info.project.name);
  const container = await ownContainer(info.project.name, "ui-refresh");
  const row = page.getByRole("button", { name: new RegExp(`^${container} `) });
  await expect(row).toBeHidden();
  await refreshUntilListed(page, row);
});

// A store's container listing can lag a create, so what the create should
// have listed is looked for across refreshes.
async function refreshUntilListed(page: Page, listed: Locator) {
  await expect(async () => {
    await page.getByRole("button", { name: "Refresh containers" }).click();
    await expect(listed).toBeVisible({ timeout: 2000 });
  }).toPass({ timeout: 60_000 });
}

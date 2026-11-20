import { test, type Locator, type Page, type Request } from "@playwright/test";
import { connect, expect, openContainer, ownContainer, typedDelete } from "../helpers.ts";

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
  // The caution is a mark on the path's row, its sentence on hover (ADR 0123).
  await expect(page.getByRole("note", { name: caution })).toHaveText("Possible segment store");

  await row.click({ button: "right" });
  await page.getByRole("menuitem", { name: "Empty container…" }).click();
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

// Test plan C5 as far as a browser can take it: coming back to the window
// re-reads the container list once it is more than 30 seconds old, so a
// container made elsewhere, here by the run's own session, is listed and
// one deleted elsewhere, here in a second window, leaves the list, without
// anything pressed. The page's clock is moved on rather than waited out,
// and the window's focus event is fired as the browser fires it when the
// window is clicked back into. The native smoke checks X11 focus delivery;
// the release plan retains the host check for Wayland.
test("coming back to the window after 30 seconds lists a change made elsewhere", async ({ page, context }, info) => {
  await page.clock.install();
  await connect(page, info.project.name);
  const container = await ownContainer(info.project.name, "ui-return");
  const row = page.getByRole("button", { name: new RegExp(`^${container} `) });
  const comeBack = () => page.evaluate(() => window.dispatchEvent(new Event("focus")));
  // A list read moments ago is kept.
  await comeBack();
  await page.waitForTimeout(1000);
  await expect(row).toBeHidden();
  await returnUntil(page, comeBack, () => expect(row).toBeVisible({ timeout: 2000 }));

  const other = await context.newPage();
  await openContainer(other, info.project.name, container);
  await other.getByRole("button", { name: "Delete container…" }).click();
  await typedDelete(other, "Delete container");
  await other.close();
  await expect(row).toBeVisible();
  await returnUntil(page, comeBack, () => expect(row).toBeHidden({ timeout: 2000 }));
});

// returnUntil moves the page's clock past the 30 seconds and comes back to
// the window until the list shows what is expected, since a store's
// container listing can lag a change. The page's clock also runs on in
// real time, so the tries stop well within 30 real seconds, or the list
// would be old enough without the clock being moved.
async function returnUntil(page: Page, comeBack: () => Promise<unknown>, listed: () => Promise<void>) {
  await expect(async () => {
    await page.clock.fastForward("00:31");
    await comeBack();
    await listed();
  }).toPass({ timeout: 12_000 });
}

// The column's Scan reads every loaded container's settings, one request
// each. In the All view every loaded container has a row, so the count the
// scan reaches is compared with the rows, and no row may be left marked as
// not read.
test("scan every container in the All view, stop and start again", async ({ page }, info) => {
  const container = await ownContainer(info.project.name, "ui-scan");
  await connect(page, info.project.name);
  await page.getByRole("tab", { name: "All" }).click();
  const rows = page.getByRole("tabpanel").getByRole("button", { name: / objects$/ });
  await expect(rows.first()).toBeVisible();
  await page.getByRole("button", { name: "Scan containers" }).click();
  const done = page.getByRole("status").filter({ hasText: /^\s*scanned (\d+) of \1\s*$/ });
  await expect(done).toBeVisible({ timeout: 2 * 60 * 1000 });
  const scanned = Number(/scanned (\d+)/.exec((await done.textContent())!)![1]);
  expect(scanned, "the scan counted every listed container").toBe(await rows.count());
  await expect(page.getByText("not read", { exact: true })).toHaveCount(0);

  // Hold one real detail request so Stop remains available even on a fast
  // local store. No reply is fabricated and only this run's name is held.
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  let held: Request | undefined;
  await page.route("**/wails/runtime", async (route) => {
    const args = route.request().postDataJSON()?.args?.args;
    if (!held && args?.length === 2 && args[1] === container) {
      held = route.request();
      await gate;
    }
    await route.continue();
  });
  try {
    await page.getByRole("button", { name: "Scan containers" }).click();
    await expect.poll(() => !!held).toBe(true);
    await page.getByRole("button", { name: "Stop the scan" }).click();
    const stopped = page.getByRole("status").filter({ hasText: /\(stopped\)$/ });
    await expect(stopped).toBeVisible();
    await expect(page.getByRole("button", { name: "Scan containers" })).toBeEnabled();
    const reply = page.waitForResponse((response) => response.request() === held);
    release();
    await (await reply).finished();
    await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
    await expect(stopped).toBeVisible();
  } finally {
    release();
    await page.unrouteAll({ behavior: "wait" });
  }
  await page.getByRole("button", { name: "Scan containers" }).click();
  await expect(done).toBeVisible({ timeout: 2 * 60 * 1000 });
  await expect(done).toContainText(`scanned ${scanned} of ${scanned}`);
});

// A store's container listing can lag a create, so what the create should
// have listed is looked for across refreshes.
async function refreshUntilListed(page: Page, listed: Locator) {
  await expect(async () => {
    await page.getByRole("button", { name: "Refresh containers" }).click();
    await expect(listed).toBeVisible({ timeout: 2000 });
  }).toPass({ timeout: 60_000 });
}

// ADR 0125 decisions 5 and 6 through the window, on every target: a folder
// is created by name in the run's own container and listed, and the
// Containers column finds the run's container by part of its name.
test("create a folder by name and filter the containers", async ({ page }, info) => {
  const container = await ownContainer(info.project.name, "ui-folder");
  await openContainer(page, info.project.name, container);
  await page.getByRole("button", { name: "New folder…" }).click();
  const dialog = page.getByRole("dialog", { name: "New folder" });
  await dialog.getByLabel("Name").fill("journey-folder");
  await expect(dialog).toContainText(`${container}/journey-folder/`);
  await dialog.getByRole("button", { name: "Create" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole("listbox", { name: "Objects" }).getByRole("option", { name: /^journey-folder\/ / })).toBeVisible();

  const filter = page.getByLabel("Filter containers");
  await filter.fill(container.slice(-10).toUpperCase());
  await expect(page.getByRole("button", { name: new RegExp(`^${container} `) })).toBeVisible();
  await filter.fill("no-such-container-anywhere");
  await expect(page.getByText(`No loaded container's name holds "no-such-container-anywhere"`)).toBeVisible();
  await filter.fill("");
});

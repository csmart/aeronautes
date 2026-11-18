import { test } from "@playwright/test";
import { control } from "../control.ts";
import { connect, expect, expectInside, finish, localFolder, ownContainer, writeFiles } from "../helpers.ts";

// The bookmark editor is driven only against the mock, with its fixed test
// key; a real cloud's bookmark is made in Go so no password reaches the page
// (ADR 0112 decision 4).
test("create a bookmark in the editor and connect", async ({ page }, info) => {
  test.skip(info.project.name !== "mock", "the editor is driven against the mock only");
  await page.goto("/");
  await page.getByRole("button", { name: "Add a bookmark" }).click();
  await page.getByLabel("Display name").fill("journey mock");
  await page.getByLabel("Auth URL").fill(process.env.AERONAUTES_MOCK!);
  await page.getByLabel("Username").fill("test:tester");
  await page.getByLabel("API key").fill("testing");
  await page.getByRole("button", { name: "Save & connect" }).click();
  await expect(page.getByText("fixtures", { exact: true }).first()).toBeVisible();
});

// ADR 0118 decision 1: a second account on the same cluster starts from a
// copy of the first's details and needs only its own key. The mock has one
// account, so the copy signs in as the same user.
test("duplicate a bookmark and connect with the copy", async ({ page }, info) => {
  test.skip(info.project.name !== "mock", "the editor is driven against the mock only");
  await connect(page, "mock");
  await page.getByRole("button", { name: "Edit journey mock" }).click();
  await expectInside(page.getByRole("form", { name: "Edit bookmark" }));
  await page.getByRole("button", { name: "Duplicate…" }).click();
  await expect(page.getByText("Copied from journey mock.")).toBeVisible();
  await expectInside(page.getByRole("form", { name: "Add bookmark" }));
  await expect(page.getByLabel("Display name")).toHaveValue("journey mock copy");
  await expect(page.getByLabel("Auth URL")).toHaveValue(process.env.AERONAUTES_MOCK!);
  await expect(page.getByLabel("Username")).toHaveValue("test:tester");
  await expect(page.getByLabel("API key")).toHaveValue("");
  await page.getByLabel("API key").fill("testing");
  await page.getByRole("button", { name: "Save & connect" }).click();
  const rail = page.getByRole("navigation", { name: "Bookmarks" });
  await expect(rail.getByRole("button", { name: "journey mock copy", exact: true })).toHaveAttribute("aria-pressed", "true");
  await expect(rail.getByRole("button", { name: "journey mock", exact: true })).toHaveAttribute("aria-pressed", "false");
  await expect(page.getByText("fixtures", { exact: true }).first()).toBeVisible();
});

// ADR 0128 (found by a journey): a bookmark that has run a job can be
// deleted, and the job stays in the history. The database refused the
// deletion while any job named the bookmark, and the editor showed the
// store's raw foreign-key error.
test("delete a bookmark that has run a job, keeping the job", async ({ page }, info) => {
  test.skip(info.project.name !== "mock", "the editor is driven against the mock only");
  await connect(page, "mock");
  const name = "journey mock to delete";
  await page.getByTitle("Add bookmark", { exact: true }).click();
  await page.getByLabel("Display name").fill(name);
  await page.getByLabel("Auth URL").fill(process.env.AERONAUTES_MOCK!);
  await page.getByLabel("Username").fill("test:tester");
  await page.getByLabel("API key").fill("testing");
  await page.getByRole("button", { name: "Save & connect" }).click();
  const rail = page.getByRole("navigation", { name: "Bookmarks" });
  await expect(rail.getByRole("button", { name, exact: true })).toHaveAttribute("aria-pressed", "true");

  // The run's own container is made on the account both bookmarks reach.
  const container = await ownContainer("mock", "ui-bookmark-delete");
  const folder = localFolder();
  writeFiles(folder, [16], 1);
  await page.getByRole("button", { name: "Refresh containers" }).click();
  await page.getByRole("button", { name: new RegExp(`^${container} `) }).click();
  await page.getByRole("button", { name: "Upload…" }).click();
  const upload = page.getByRole("dialog", { name: "Upload" });
  await control.pickFolder(folder);
  await upload.getByRole("button", { name: "Choose folder…" }).click();
  await upload.getByRole("button", { name: "Start upload" }).click();
  await finish(page, upload);

  await rail.getByRole("button", { name: `Edit ${name}`, exact: true }).click();
  await page.getByRole("button", { name: "Delete…" }).click();
  await page.getByRole("button", { name: "Really delete" }).click();
  await expect(rail.getByRole("button", { name, exact: true })).toHaveCount(0);

  await page.getByRole("button", { name: /jobs & changes/ }).click();
  await page.getByRole("tab", { name: "Job history" }).click();
  await expect(page.getByRole("region", { name: "Job history" }).getByRole("article").filter({ hasText: `"${container}/"` })).toHaveCount(1);
  await page.getByRole("button", { name: /▾ hide/ }).click();
});

import { test, expect } from "@playwright/test";

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

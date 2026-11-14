import { test } from "@playwright/test";
import { connect, expect } from "../helpers.ts";

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
  await page.getByRole("button", { name: "Duplicate…" }).click();
  await expect(page.getByText("Copied from journey mock.")).toBeVisible();
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

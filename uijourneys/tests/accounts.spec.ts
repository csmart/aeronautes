import { test, type Locator, type Page } from "@playwright/test";
import { createServer, type AddressInfo } from "node:net";
import { deleteBookmark, expect } from "../helpers.ts";

// Bookmarks made and changed in the editor, against the mock only: its key
// is a fixed test key, and a real cloud's password never reaches the page
// (ADR 0112 decision 4). Each journey deletes the bookmarks it made, from a
// fresh page, because every spec shares one application, and makes none
// other: the bookmark journeys after these start from an empty rail.

test.beforeEach(({}, info) => {
  test.skip(info.project.name !== "mock", "the editor is driven against the mock only");
});

// ADR 0113: an Identity URL ending in v3 is the token endpoint's base, and
// one that does not is asked for its versions, whose v3 link is used only
// at the same address. The mock answers /v3/auth/tokens for its one user
// and serves no version document, so the second URL is refused in words.
test("make a Keystone v3 bookmark in the editor, and refuse a URL with no v3", async ({ page }) => {
  const mock = new URL(process.env.AERONAUTES_MOCK!).origin;
  const made: string[] = [];
  try {
    const editor = await startKeystoneBookmark(page, "journey keystone", `${mock}/v3`);
    made.push("journey keystone");
    await editor.getByRole("button", { name: "Save & connect" }).click();
    await expectConnected(page, "journey keystone");

    const refused = await startKeystoneBookmark(page, "journey keystone no v3", `${mock}/identity`);
    made.push("journey keystone no v3");
    await refused.getByRole("button", { name: "Save & connect" }).click();
    await expect(refused.getByRole("alert")).toContainText(
      `the Identity URL ${mock}/identity does not offer Keystone v3 at the same address`);
    await refused.getByRole("button", { name: "Cancel" }).click();
  } finally {
    await cleanUp(page, made);
  }
});

// A bookmark keeps a session once it has connected, so an edit to its user
// has to sign in afresh: a wrong user is refused even though the old
// session still works, and the right one connects again.
test("change a bookmark's user: a wrong one is refused, the right one connects", async ({ page }) => {
  const name = "journey mock user";
  try {
    await startBookmark(page, name, process.env.AERONAUTES_MOCK!);
    await bookmarkEditor(page).getByRole("button", { name: "Save & connect" }).click();
    await expectConnected(page, name);
    const editor = await editBookmark(page, name);
    await editor.getByLabel("Username", { exact: true }).fill("test:nobody");
    await editor.getByLabel("API key").fill("testing");
    await editor.getByRole("button", { name: "Save & connect" }).click();
    await expect(editor.getByRole("alert")).toBeVisible();

    await editor.getByLabel("Username", { exact: true }).fill("test:tester");
    await editor.getByLabel("API key").fill("testing");
    await editor.getByRole("button", { name: "Save & connect" }).click();
    await expect(editor).toHaveCount(0);
    // Connected again from a fresh page, with what was saved.
    await page.goto("/");
    await page.getByRole("navigation", { name: "Bookmarks" }).getByRole("button", { name, exact: true }).click();
    await expectConnected(page, name);
    await expect((await editBookmark(page, name)).getByLabel("Username", { exact: true })).toHaveValue("test:tester");
  } finally {
    await cleanUp(page, [name]);
  }
});

// A store nobody answers at is refused in plain words, promptly: the
// connection is refused at once on a closed loopback port, so a wait of
// more than a few seconds is a hang.
test("a bookmark whose store is unreachable is refused promptly", async ({ page }) => {
  const name = "journey unreachable";
  const port = await closedPort();
  try {
    const editor = await startBookmark(page, name, `http://127.0.0.1:${port}/auth/v1.0`);
    await editor.getByRole("button", { name: "Save & connect" }).click();
    await expect(editor.getByRole("alert")).toContainText("connection refused", { timeout: 15_000 });
    await expect(editor.getByRole("button", { name: "Save & connect" })).toBeEnabled();
    await editor.getByRole("button", { name: "Cancel" }).click();
  } finally {
    await cleanUp(page, [name]);
  }
});

// openNewBookmark opens the editor for a new bookmark from a fresh page, by
// the rail's button or, on an empty rail, the welcome's.
async function openNewBookmark(page: Page): Promise<Locator> {
  const welcome = await loadRail(page);
  if (await welcome.isVisible()) await welcome.click();
  else await page.getByRole("navigation", { name: "Bookmarks" }).getByTitle("Add bookmark", { exact: true }).click();
  return bookmarkEditor(page);
}

// loadRail loads a fresh page and waits for the rail to fill, which it does
// after the page loads: with a bookmark's tile, or with the welcome's add
// button when there is none, which it returns.
async function loadRail(page: Page): Promise<Locator> {
  await page.goto("/");
  const welcome = page.getByRole("button", { name: "Add a bookmark" });
  const tile = page.getByRole("navigation", { name: "Bookmarks" }).locator("button[aria-pressed]");
  await expect(tile.or(welcome).first()).toBeVisible();
  return welcome;
}

// startBookmark fills a TempAuth bookmark of the mock's user and key.
async function startBookmark(page: Page, name: string, authURL: string): Promise<Locator> {
  const editor = await openNewBookmark(page);
  await editor.getByLabel("Display name").fill(name);
  await editor.getByLabel("Auth URL").fill(authURL);
  await editor.getByLabel("Username", { exact: true }).fill("test:tester");
  await editor.getByLabel("API key").fill("testing");
  return editor;
}

async function startKeystoneBookmark(page: Page, name: string, identityURL: string): Promise<Locator> {
  const editor = await openNewBookmark(page);
  await editor.getByLabel("Display name").fill(name);
  await editor.getByLabel("Auth type").selectOption({ label: "Keystone v3" });
  await editor.getByLabel(/^Identity URL/).fill(identityURL);
  await editor.getByLabel("Username", { exact: true }).fill("test:tester");
  await editor.getByLabel("Password").fill("testing");
  await editor.getByLabel("Project", { exact: true }).fill("test");
  await editor.getByLabel("Region", { exact: true }).fill("RegionOne");
  return editor;
}

async function editBookmark(page: Page, name: string): Promise<Locator> {
  await page.getByRole("navigation", { name: "Bookmarks" }).getByRole("button", { name: `Edit ${name}`, exact: true }).click();
  return bookmarkEditor(page);
}

// bookmarkEditor is the editor whether it adds or edits: a new bookmark's
// editor turns into the saved one's once "Save & connect" has saved it, and
// stays open on a refused connection with the reason.
function bookmarkEditor(page: Page): Locator {
  return page.getByRole("form", { name: /^(Add|Edit) bookmark$/ });
}

async function expectConnected(page: Page, name: string): Promise<void> {
  const tile = page.getByRole("navigation", { name: "Bookmarks" }).getByRole("button", { name, exact: true });
  await expect(tile).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByText("fixtures", { exact: true }).first()).toBeVisible();
}

// cleanUp deletes, from a fresh page, each of these bookmarks that was
// saved, so a journey that failed early still leaves the rail as it was.
async function cleanUp(page: Page, names: string[]): Promise<void> {
  await loadRail(page);
  const rail = page.getByRole("navigation", { name: "Bookmarks" });
  for (const name of names) {
    if (await rail.getByRole("button", { name: `Edit ${name}`, exact: true }).count() > 0) await deleteBookmark(page, name);
  }
}

// closedPort is a loopback port that was free a moment ago, so nothing
// listens there.
async function closedPort(): Promise<number> {
  const server = createServer();
  await new Promise<void>((listening) => server.listen(0, "127.0.0.1", listening));
  const { port } = server.address() as AddressInfo;
  await new Promise<void>((closed) => server.close(() => closed()));
  return port;
}

import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import BookmarkRail from "./BookmarkRail.svelte";

let rail: ReturnType<typeof mount> | null = null;

afterEach(async () => {
  if (rail) await unmount(rail);
  rail = null;
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

function button(label: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")].find(
    (node) => (node.getAttribute("aria-label") ?? node.textContent?.trim()) === label,
  );
  expect(found, `button ${label}`).toBeDefined();
  return found!;
}

function mountRail(browser: BrowserState) {
  rail = mount(BookmarkRail, {
    target: document.body,
    props: { browser, onAddBookmark: () => {}, onEditBookmark: () => {} },
  });
  flushSync();
}

it("backs up settings and reports the file, noting passwords are excluded", async () => {
  const api = newFakeApi();
  api.exportSettings = vi.fn(async () => ({
    bookmarkCount: 2,
    directory: "/home/operator/exports",
    fileName: "aeronautes-settings-x.json",
  }));
  const browser = new BrowserState(api);
  mountRail(browser);

  button("Back up settings…").click();
  await vi.waitFor(() => {
    expect(document.body.textContent).toContain(
      "Backed up settings and 2 bookmarks to /home/operator/exports/aeronautes-settings-x.json",
    );
    expect(document.body.textContent).toContain("Passwords are not included");
  });
  expect(api.exportSettings).toHaveBeenCalledTimes(1);
});

it("reports a cancelled backup without an error", async () => {
  const api = newFakeApi();
  api.exportSettings = vi.fn(async () => null);
  const browser = new BrowserState(api);
  mountRail(browser);

  button("Back up settings…").click();
  await vi.waitFor(() =>
    expect(document.body.textContent).toContain("Backup cancelled; nothing was written."),
  );
});

it("reports a failed backup as an error", async () => {
  const api = newFakeApi();
  api.exportSettings = vi.fn(async () => {
    throw new Error("disk full");
  });
  const browser = new BrowserState(api);
  mountRail(browser);

  button("Back up settings…").click();
  await vi.waitFor(() =>
    expect(document.body.textContent).toContain("Your settings could not be backed up."),
  );
});

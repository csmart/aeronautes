import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import BookmarkRail from "./BookmarkRail.svelte";
import type { AppSettingsDTO } from "../../lib/api";

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

it("restores settings and reports what was added, noting passwords are re-entered", async () => {
  const api = newFakeApi();
  api.importSettings = vi.fn(async () => ({
    settingsApplied: true,
    bookmarksTotal: 2,
    bookmarksCreated: 1,
    bookmarksSkipped: 1,
    bookmarksUnresolved: 0,
    complete: true,
    detail: "",
  }));
  const browser = new BrowserState(api);
  mountRail(browser);

  button("Restore settings…").click();
  await vi.waitFor(() => {
    expect(document.body.textContent).toContain("Restored your settings and added 1 bookmark");
    expect(document.body.textContent).toContain("1 already present was left unchanged");
    expect(document.body.textContent).toContain("Re-enter each account's password");
  });
  expect(api.importSettings).toHaveBeenCalledTimes(1);
});

it("reports a cancelled restore without an error", async () => {
  const api = newFakeApi();
  api.importSettings = vi.fn(async () => null);
  const browser = new BrowserState(api);
  mountRail(browser);

  button("Restore settings…").click();
  await vi.waitFor(() =>
    expect(document.body.textContent).toContain("Restore cancelled; nothing was changed."),
  );
});

it("reports a refused restore with the reason it was refused", async () => {
  const api = newFakeApi();
  api.importSettings = vi.fn(async () => {
    throw new Error("unsupported backup version 2; this build restores version 1");
  });
  const browser = new BrowserState(api);
  mountRail(browser);

  button("Restore settings…").click();
  await vi.waitFor(() =>
    expect(document.body.textContent).toContain("unsupported backup version 2"),
  );
});


it("refreshes cached settings after a restore so a later change keeps them", async () => {
  const api = newFakeApi();
  const base = (await api.appSettings())!;
  const stale: AppSettingsDTO = { ...base, railWidthPx: 300, maxActiveJobs: 16 };
  const restored: AppSettingsDTO = { ...stale, maxActiveJobs: 4 };
  api.importSettings = vi.fn(async () => ({
    settingsApplied: true,
    bookmarksTotal: 0,
    bookmarksCreated: 0,
    bookmarksSkipped: 0,
    bookmarksUnresolved: 0,
    complete: true,
    detail: "",
  }));
  api.appSettings = vi.fn(async () => restored);
  const saved: AppSettingsDTO[] = [];
  api.saveAppSettings = vi.fn(async (s) => {
    saved.push(s);
    return s;
  });
  const browser = new BrowserState(api);
  browser.settings.document = stale;
  browser.settings.status = "current";
  mountRail(browser);

  button("Restore settings…").click();
  await vi.waitFor(() => expect(browser.settings.restoring).toBe(false));

  // The cached document now holds the restored values, not the stale ones.
  expect(browser.settings.document?.maxActiveJobs).toBe(4);

  // A later rail resize therefore persists the restored document, so it cannot
  // silently undo the restore.
  await browser.settings.saveRailWidth(220);
  const last = saved.at(-1)!;
  expect(last.maxActiveJobs).toBe(4);
  expect(last.railWidthPx).toBe(220);
});

it("reports a partial restore with what still needs doing", async () => {
  const api = newFakeApi();
  api.importSettings = vi.fn(async () => ({
    settingsApplied: true,
    bookmarksTotal: 3,
    bookmarksCreated: 1,
    bookmarksSkipped: 0,
    bookmarksUnresolved: 0,
    complete: false,
    detail: 'bookmark 2 ("B") could not be restored',
  }));
  const browser = new BrowserState(api);
  mountRail(browser);

  button("Restore settings…").click();
  await vi.waitFor(() => {
    expect(document.body.textContent).toContain("Restored your settings and added 1 bookmark");
    expect(document.body.textContent).toContain("not yet restored");
    expect(document.body.textContent).toContain("could not be restored");
  });
});

it("reports an unconfirmed bookmark separately from unrestored ones", async () => {
  const api = newFakeApi();
  api.importSettings = vi.fn(async () => ({
    settingsApplied: true,
    bookmarksTotal: 1,
    bookmarksCreated: 0,
    bookmarksSkipped: 0,
    bookmarksUnresolved: 1,
    complete: false,
    detail: "bookmark 1 could not be confirmed",
  }));
  const browser = new BrowserState(api);
  mountRail(browser);

  button("Restore settings…").click();
  await vi.waitFor(() =>
    expect(document.body.textContent).toContain(
      "could not be confirmed; reload when local state is available",
    ),
  );
  // A row whose commit is unconfirmed must not be reported as one known not to
  // have been restored.
  expect(document.body.textContent).not.toContain("1 of 1 not yet restored");
});

it("reconciles cached settings when a restore could not confirm them", async () => {
  const api = newFakeApi();
  const base = (await api.appSettings())!;
  const stale: AppSettingsDTO = { ...base, maxActiveJobs: 16 };
  const durable: AppSettingsDTO = { ...base, maxActiveJobs: 4 };
  api.importSettings = vi.fn(async () => ({
    settingsApplied: false,
    bookmarksTotal: 0,
    bookmarksCreated: 0,
    bookmarksSkipped: 0,
    bookmarksUnresolved: 0,
    complete: false,
    detail: "the settings could not be confirmed; reload to see what was applied",
  }));
  // The backend durably applied the settings despite the uncertain reply.
  api.appSettings = vi.fn(async () => durable);
  const saved: AppSettingsDTO[] = [];
  api.saveAppSettings = vi.fn(async (s) => {
    saved.push(s);
    return s;
  });
  const browser = new BrowserState(api);
  browser.settings.document = stale;
  browser.settings.status = "current";
  mountRail(browser);

  button("Restore settings…").click();
  await vi.waitFor(() => expect(browser.settings.restoring).toBe(false));
  expect(document.body.textContent).toContain("could not be confirmed");

  // The cache was reconciled to the durable value, so a later rail resize
  // persists it rather than writing the stale document back.
  expect(browser.settings.document?.maxActiveJobs).toBe(4);
  await browser.settings.saveRailWidth(240);
  expect(saved.at(-1)!.maxActiveJobs).toBe(4);
});

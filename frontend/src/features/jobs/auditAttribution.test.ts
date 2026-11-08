import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { AuditEventDTO, BookmarkDTO } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import JobsDrawer from "./JobsDrawer.svelte";

let drawer: ReturnType<typeof mount> | null = null;
const releasePending: (() => void)[] = [];

afterEach(async () => {
  if (drawer) await unmount(drawer);
  drawer = null;
  for (const release of releasePending.splice(0)) release();
  await Promise.resolve();
  document.body.innerHTML = "";
});

function event(id: number, bookmarkId: string): AuditEventDTO {
  return { id, bookmarkId, at: "", operation: "object.metadata.update", resource: "same-container/same-object",
    summary: "metadata applied", result: "applied", transIds: ["tx-observed"] };
}

function bookmark(id: string, displayName = "Same display name"): BookmarkDTO {
  return { id, displayName, authUrl: "http://mock/auth/v1.0", username: "test:tester",
    storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false };
}

function selector(): HTMLSelectElement {
  const select = document.querySelector<HTMLSelectElement>('select[id$="-audit-bookmark"]');
  expect(select, "audit bookmark selector").not.toBeNull();
  return select!;
}

async function show(browser: BrowserState) {
  drawer = mount(JobsDrawer, { target: document.body, props: { browser } });
  flushSync();
  document.querySelector<HTMLButtonElement>(".bar")!.click();
  flushSync();
  const changes = [...document.querySelectorAll<HTMLButtonElement>("button")]
    .find((button) => button.textContent?.trim() === "Recent changes");
  expect(changes).toBeDefined();
  changes!.click();
  await vi.waitFor(() => expect(browser.audit.loaded).toBe(true));
  flushSync();
}

it("keeps identical resources visibly attributable with duplicate, unavailable and absent bookmark labels", async () => {
  const api = newFakeApi();
  api.auditSearch = vi.fn().mockResolvedValue([event(4, "beta"), event(3, "alpha"), event(2, "gone"), event(1, "")]);
  const browser = new BrowserState(api);
  browser.bookmarks = [bookmark("alpha"), bookmark("beta")];
  browser.connectedBookmarkId = "unrelated-active";
  await show(browser);
  const rows = [...document.querySelectorAll(".event")];
  expect(rows).toHaveLength(4);
  expect(rows.map((row) => row.querySelector(".resource")?.textContent)).toEqual(Array(4).fill("same-container/same-object"));
  expect(rows[0].textContent).toContain("beta");
  expect(rows[1].textContent).toContain("alpha");
  expect(rows[0].textContent).toContain("Same display name");
  expect(rows[1].textContent).toContain("Same display name");
  expect(rows[2].textContent).toContain("gone");
  expect(rows[2].textContent?.toLowerCase()).toContain("unavailable");
  expect(rows[3].textContent).toContain("No recorded bookmark");
  expect(rows.some((row) => row.textContent?.includes("unrelated-active"))).toBe(false);
  expect(document.querySelector(".audit-scope")?.textContent).toContain("All accounts");
  expect(document.body.textContent).toContain("current bookmarks");
});

it("offers and retains an unavailable historical bookmark without requiring a connection", async () => {
  const api = newFakeApi();
  let resolve!: (rows: AuditEventDTO[]) => void;
  const pending = new Promise<AuditEventDTO[]>((done) => { resolve = done; });
  releasePending.push(() => resolve([]));
  api.auditSearch = vi.fn().mockResolvedValueOnce([event(2, "gone"), event(1, "alpha")])
    .mockReturnValueOnce(pending).mockResolvedValueOnce([]);
  const browser = new BrowserState(api);
  browser.bookmarks = [bookmark("alpha")];
  browser.jobsFilter = "jobs-bookmark";
  await show(browser);
  expect(browser.connectedBookmarkId).toBeNull();
  expect(document.querySelector('label[for$="-audit-bookmark"]')?.textContent).toBe("Bookmark");
  const select = selector();
  expect(select.disabled).toBe(false);
  expect([...select.options].map((option) => option.value)).toEqual(["", "alpha", "gone"]);
  select.value = "gone";
  select.dispatchEvent(new Event("change", { bubbles: true }));
  flushSync();
  expect(document.querySelectorAll(".event")).toHaveLength(0);
  expect(api.auditSearch).toHaveBeenLastCalledWith({ bookmarkId: "gone", operationPrefix: "", result: "", beforeId: 0, limit: 50 });
  browser.bookmarks = [];
  flushSync();
  expect(selector().value).toBe("gone");
  expect([...selector().options].some((option) => option.value === "gone")).toBe(true);
  resolve([]);
  await vi.waitFor(() => expect(browser.audit.loaded).toBe(true));
  flushSync();
  expect(selector().value).toBe("gone");
  expect(document.querySelector(".audit-scope")?.textContent).toContain("gone");
  expect(document.querySelector(".audit-scope")?.textContent).not.toContain("All accounts");
  expect(browser.jobsFilter).toBe("jobs-bookmark");
  selector().value = "";
  selector().dispatchEvent(new Event("change", { bubbles: true }));
  await vi.waitFor(() => expect(browser.audit.loading).toBe(false));
  flushSync();
  expect(api.auditSearch).toHaveBeenLastCalledWith({ bookmarkId: "", operationPrefix: "", result: "", beforeId: 0, limit: 50 });
  expect(document.querySelector(".audit-scope")?.textContent).toContain("All accounts");
});

it("shows a rejected named page as unavailable without leaking the other bookmark's row", async () => {
  const api = newFakeApi();
  api.auditSearch = vi.fn().mockResolvedValueOnce([event(1, "alpha")])
    .mockResolvedValueOnce([{ ...event(2, "beta"), resource: "other-account-private-resource" }]);
  const browser = new BrowserState(api);
  browser.bookmarks = [bookmark("alpha")];
  await show(browser);
  selector().value = "alpha";
  selector().dispatchEvent(new Event("change", { bubbles: true }));
  await vi.waitFor(() => expect(browser.audit.loading).toBe(false));
  flushSync();
  expect(browser.audit.loaded).toBe(false);
  expect(document.querySelector('.audit-status [role="alert"]')?.textContent).toBe("Audit events are unavailable. Retry this search.");
  expect(document.body.textContent).not.toContain("other-account-private-resource");
  expect(document.body.textContent).not.toContain("No matching audit events");
  expect(document.querySelectorAll(".event")).toHaveLength(0);
});

it("keeps the retained ID visible as the current bookmark name changes or disappears", async () => {
  const api = newFakeApi();
  api.auditSearch = vi.fn().mockResolvedValue([event(1, "alpha")]);
  const browser = new BrowserState(api);
  browser.bookmarks = [bookmark("alpha", "Original name")];
  await show(browser);
  expect(document.querySelector(".event")?.textContent).toContain("alpha");
  browser.bookmarks = [bookmark("alpha", "New name")];
  flushSync();
  expect(document.querySelector(".event")?.textContent).toContain("New name");
  expect(document.querySelector(".event")?.textContent).toContain("alpha");
  browser.bookmarks = [];
  flushSync();
  expect(document.querySelector(".event")?.textContent).toContain("alpha");
  expect(document.querySelector(".event")?.textContent?.toLowerCase()).toContain("unavailable");
  expect(api.auditSearch).toHaveBeenCalledTimes(1);
});

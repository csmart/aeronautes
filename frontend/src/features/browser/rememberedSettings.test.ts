import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { ContainerSettingsDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import ContainerColumn from "./ContainerColumn.svelte";
import WorkingList from "./WorkingList.svelte";

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
});

async function settle() {
  for (let i = 0; i < 8; i++) { await Promise.resolve(); flushSync(); }
}

// Remembered settings from an earlier connection are muted and dated, a
// current fact replaces them, and they never raise the public banner
// (ADR 0110 decision 4).
it("shows remembered settings muted and never as the banner", async () => {
  const api = newFakeApi();
  const browser = new BrowserState(api);
  const bookmark = await browser.saveBookmark({ id: "", displayName: "Mock", authUrl: "https://example.test/auth",
    username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
  await browser.connect(bookmark.id);
  const generation = browser.account!.scope.generation;
  const remembered: ContainerSettingsDTO = {
    publicRead: true, publicListing: false, referrerGrants: [], sharedWrite: false, staticWeb: false,
    versioningMode: "off", versioningArchive: "", quota: false, cors: false, readAcl: ".r:*", writeAcl: "",
    observedAt: "2026-11-01T00:00:00Z", generation: generation - 1,
  };
  browser.containers = [{ name: "seen", count: 1, bytes: 1, class: "normal", settings: null, lastSeen: remembered }];
  const column = document.createElement("div");
  document.body.append(column);
  mounted.add(mount(ContainerColumn, { target: column, props: { browser, onCreate: vi.fn() } }));
  flushSync();
  const badge = () => column.querySelector<HTMLElement>(".container-row .setting-badge");
  expect(badge()?.textContent).toBe("public");
  expect(badge()?.classList.contains("muted")).toBe(true);
  expect(badge()?.title).toContain("last seen 2026-11-01T00:00:00Z, not read under this connection");

  browser.containerName = "seen";
  const list = document.createElement("div");
  document.body.append(list);
  mounted.add(mount(WorkingList, { target: list, props: { browser } }));
  flushSync();
  expect(list.textContent).not.toContain("anyone with the URL");

  api.containerDetail = vi.fn(async (name: string) => ({
    name, scope: { bookmarkId: bookmark.id, generation },
    settings: { ...remembered, publicRead: false, readAcl: "", versioningMode: "versions", observedAt: "2026-11-12T00:00:00Z", generation },
  }) as never);
  await browser.checkSettings();
  await settle();
  expect(badge()?.textContent).toBe("versioned");
  expect(badge()?.classList.contains("muted")).toBe(false);
});

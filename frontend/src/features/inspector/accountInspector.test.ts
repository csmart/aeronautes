import { afterEach, expect, it } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import type { Api, BookmarkDTO } from "../../lib/api";
import Inspector from "./Inspector.svelte";

const mounted = new Set<ReturnType<typeof mount>>();
afterEach(async () => {
  for (const component of mounted) await unmount(component);
  mounted.clear();
  document.body.innerHTML = "";
});

async function settle() {
  for (let i = 0; i < 12; i++) {
    await Promise.resolve();
    flushSync();
  }
}

function bookmark(): BookmarkDTO {
  return {
    id: "", displayName: "Mock", authUrl: "http://mock/auth/v1.0", username: "test:tester",
    storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "",
    tlsMode: "system", caPem: "", hasCa: false, hasCredential: false,
  };
}

async function show(adjust?: (api: Api) => void, container?: string) {
  const api = newFakeApi();
  adjust?.(api);
  const browser = new BrowserState(api);
  const saved = await browser.saveBookmark(bookmark(), "testing");
  await browser.connect(saved.id);
  if (container) await browser.openContainer(container);
  await settle();
  const root = document.createElement("div");
  document.body.append(root);
  mounted.add(mount(Inspector, { target: root, props: { browser } }));
  flushSync();
  const card = (title: string) =>
    [...root.querySelectorAll<HTMLElement>(".card")].find((c) => c.querySelector(".card-title")?.textContent?.trim() === title) ?? null;
  return { root, card };
}

// DESIGN §53 lists policy totals and the raw headers for the account, as the
// container and object panes have theirs. The raw view is for diagnosing what
// the store said, so in every pane it starts closed; the backend has already
// replaced the TempURL key values.
it("shows the account's usage by storage policy and its raw headers, closed", async () => {
  const { card } = await show();
  const policies = card("Storage policies");
  expect(policies).not.toBeNull();
  const rows = [...policies!.querySelectorAll(".kv")].map((row) => row.textContent?.replace(/\s+/g, " ").trim());
  expect(rows).toHaveLength(2);
  expect(rows[0]).toMatch(/^gold .* · 20 objects · 4 containers$/);
  expect(rows[1]).toMatch(/^standard /);

  const raw = card("Raw headers");
  expect(raw?.tagName).toBe("DETAILS");
  expect((raw as HTMLDetailsElement).open).toBe(false);
  expect(raw!.textContent).toContain("X-Trans-Id: tx-fixture-account");
  expect(raw!.textContent).toContain("X-Account-Meta-Temp-Url-Key: [redacted]");
});

it("leaves the policy card out when the store reports no policy", async () => {
  // The account arrives with the connect and again on every read.
  const { card } = await show((api) => {
    const connect = api.connect.bind(api);
    const read = api.account.bind(api);
    api.connect = async (bookmarkId) => ({ ...(await connect(bookmarkId)), policyUsage: [] });
    api.account = async (bookmarkId) => ({ ...(await read(bookmarkId)), policyUsage: [] });
  });
  expect(card("Storage policies")).toBeNull();
  expect(card("Raw headers")).not.toBeNull();
});

it("starts the container's raw headers closed too", async () => {
  const { card } = await show(undefined, "fixtures");
  const raw = card("Raw headers");
  expect(raw?.tagName).toBe("DETAILS");
  expect((raw as HTMLDetailsElement).open).toBe(false);
});

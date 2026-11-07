import { expect, it } from "vitest";
import type { Api } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";

function save(api: Api, name: string) {
  return api.saveBookmark({ id: "", displayName: name, authUrl: `http://${name}/auth/v1.0`, username: "test:tester",
    storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "fixture-key");
}

it("the fake requires the active bookmark and keeps its generation across reconnects", async () => {
  const api = newFakeApi();
  const alpha = await save(api, "alpha");
  const beta = await save(api, "beta");
  await expect(api.connect("")).rejects.toThrow();
  await expect(api.capabilities(alpha.id)).rejects.toThrow();
  const first = await api.connect(alpha.id);
  expect(first.scope).toEqual({ bookmarkId: alpha.id, generation: 1 });
  expect((await api.capabilities(alpha.id)).scope).toEqual(first.scope);
  expect((await api.connect(alpha.id)).scope).toEqual(first.scope);
  await expect(api.capabilities("")).rejects.toThrow();
  await api.connect(beta.id);
  await expect(api.capabilities(alpha.id)).rejects.toThrow("account changed");
  expect((await api.capabilities(beta.id)).scope).toEqual({ bookmarkId: beta.id, generation: 1 });
});

it.each(["save", "delete"])("the fake invalidates the active portrait on bookmark %s", async (operation) => {
  const api = newFakeApi();
  const bookmark = await save(api, "alpha");
  await api.connect(bookmark.id);
  if (operation === "save") await api.saveBookmark(bookmark, "replacement-key");
  else await api.deleteBookmark(bookmark.id);
  await expect(api.capabilities(bookmark.id)).rejects.toThrow();
  if (operation === "save") {
    expect((await api.connect(bookmark.id)).scope).toEqual({ bookmarkId: bookmark.id, generation: 2 });
  } else {
    await expect(api.connect(bookmark.id)).rejects.toThrow();
  }
});

it("a same-bookmark re-save and reconnect cannot publish newer initial rows beside the old pair", async () => {
  const api = newFakeApi();
  const bookmark = await save(api, "alpha");
  const readCapabilities = api.capabilities;
  api.capabilities = async (bookmarkId) => {
    const captured = await readCapabilities(bookmarkId);
    // This bridge activity does not change the BrowserState's local lifetime.
    await api.saveBookmark({ ...bookmark, authUrl: "http://beta/auth/v1.0" }, "replacement-key");
    await api.connect(bookmarkId);
    return captured;
  };
  const state = new BrowserState(api);
  expect(await state.connect(bookmark.id)).toBe(true);
  expect(state.account?.scope).toEqual({ bookmarkId: bookmark.id, generation: 1 });
  expect(state.capabilities?.scope).toEqual(state.account?.scope);
  expect(state.connectedBookmarkId).toBe(bookmark.id);
  expect(state.containers).toEqual([]);
  expect(state.containersMarker).toBe("");
  expect(state.containersError).toMatch(/not shown/);
  const newer = await api.containers(bookmark.id, "", 100);
  expect(newer.scope).toEqual({ bookmarkId: bookmark.id, generation: 2 });
  expect(newer.rows.length).toBeGreaterThan(0);
});

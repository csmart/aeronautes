import { describe, expect, it, vi } from "vitest";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";

async function fixture() {
  const api = newFakeApi();
  const saved = await api.saveBookmark({ id: "", displayName: "Count", authUrl: "https://example.test/auth",
      username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
  const state = new BrowserState(api);
  await state.loadBookmarks();
  await state.connect(saved.id);
  await state.openContainer("fixtures");
  return { api, state, saved };
}

function barrier() {
  let release!: () => void;
  const promise = new Promise<void>((resolve) => { release = resolve; });
  return { promise, release };
}

describe("prefix count state", () => {
  it("counts through a count-only scan and keeps the result for the scope", async () => {
    const { state } = await fixture();
    expect(state.prefixCount).toBeNull();
    await state.startCount();
    expect(state.countRunning).toBe(false);
    expect(state.prefixCount?.done).toBe(true);
    expect(state.prefixCount?.scanned).toBeGreaterThan(0);
    expect(state.countedAt).not.toBeNull();
    await state.goTo("photos/");
    expect(state.prefixCount).toBeNull();
  });

  it("retires a held poll on navigation, but not when Find closes", async () => {
    const { api, state, saved } = await fixture();
    const entered = barrier(), reply = barrier();
    const read = api.findStatus;
    const start = vi.spyOn(api, "startFind");
    const cancel = vi.spyOn(api, "cancelFind");
    api.findStatus = async (id, bookmarkId) => {
      const status = await read(id, bookmarkId);
      entered.release();
      await reply.promise;
      return status;
    };
    const pending = state.startCount();
    await entered.promise;
    state.closeFind();
    expect(state.countRunning).toBe(true);
    expect(cancel).not.toHaveBeenCalled();
    await state.goTo("photos/");
    reply.release();
    await pending;
    expect(state.countRunning).toBe(false);
    expect(state.prefixCount).toBeNull();
    expect(cancel).toHaveBeenCalledWith(await start.mock.results[0].value, saved.id);
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it("cancels a startup reply that arrives after navigation", async () => {
    const { api, state, saved } = await fixture();
    const entered = barrier(), reply = barrier();
    const start = api.startFind;
    let id = "";
    api.startFind = async (...args) => {
      id = await start(...args);
      entered.release();
      await reply.promise;
      return id;
    };
    const cancel = vi.spyOn(api, "cancelFind");
    const pending = state.startCount();
    await entered.promise;
    await state.goTo("photos/");
    reply.release();
    await pending;
    expect(state.countRunning).toBe(false);
    expect(state.prefixCount).toBeNull();
    expect(cancel).toHaveBeenCalledWith(id, saved.id);
  });

  it("does not retarget a new count while waiting for the previous cancellation", async () => {
    const { api, state } = await fixture();
    await state.startCount();
    const entered = barrier(), reply = barrier();
    api.cancelFind = async () => { entered.release(); await reply.promise; };
    const start = vi.spyOn(api, "startFind");
    const pending = state.startCount();
    await entered.promise;
    await state.goTo("photos/");
    reply.release();
    await pending;
    expect(start).not.toHaveBeenCalled();
    expect(state.countRunning).toBe(false);
    expect(state.prefixCount).toBeNull();
  });
});

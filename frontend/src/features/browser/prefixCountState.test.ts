import { describe, expect, it } from "vitest";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";

describe("prefix count state", () => {
  it("counts through a count-only scan and keeps the result for the scope", async () => {
    const api = newFakeApi();
    const saved = await api.saveBookmark({ id: "", displayName: "Count", authUrl: "https://example.test/auth",
      username: "fixture:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "test-only");
    const state = new BrowserState(api);
    await state.loadBookmarks();
    await state.connect(saved.id);
    await state.openContainer("fixtures");
    expect(state.prefixCount).toBeNull();
    await state.startCount();
    expect(state.countRunning).toBe(false);
    expect(state.prefixCount?.done).toBe(true);
    expect(state.prefixCount?.scanned).toBeGreaterThan(0);
    expect(state.countedAt).not.toBeNull();
    await state.goTo("photos/");
    expect(state.prefixCount).toBeNull();
  });
});

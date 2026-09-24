import { describe, expect, it } from "vitest";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";

// ADR 0079: a write names the scope its view was built under. When the
// connection has moved since (a separate caller re-saved the bookmark and
// reconnected, so the generation is 2 while this view holds 1), the write
// is refused with the named message and nothing changes; the same write
// under the current portrait proceeds.
const REFUSAL = "the connected account changed; nothing was written";

async function fixture() {
  const api = newFakeApi();
  const state = new BrowserState(api);
  const bookmark = await state.saveBookmark({ id: "", displayName: "Mock", authUrl: "http://mock/auth/v1.0", username: "test:tester", storageUrlOverride: "", tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "testing");
  await state.connect(bookmark.id);
  await state.openContainer("fixtures");
  expect(state.portrait).toEqual({ bookmarkId: bookmark.id, generation: 1 });
  return { api, state, bookmark };
}

// A separate API caller re-saves the bookmark and reconnects; this view's
// portrait and details are left as they were read.
async function moveConnection(f: Awaited<ReturnType<typeof fixture>>) {
  await f.api.saveBookmark(f.bookmark, "re-entered valid key");
  const current = await f.api.connect(f.bookmark.id);
  expect(current.scope).toEqual({ bookmarkId: f.bookmark.id, generation: 2 });
}

describe("caller-named mutation binding", () => {
  it("refuses a container edit whose detail was read under an earlier generation, then applies it under the current one", async () => {
    const f = await fixture();
    await f.state.refreshContainerDetail();
    const before = f.state.containerDetail;
    expect(before?.scope).toEqual({ bookmarkId: f.bookmark.id, generation: 1 });
    await moveConnection(f);
    await expect(f.state.saveContainer({ ...(before!.settings as object), meta: { edited: "yes" } } as never)).rejects.toThrow(REFUSAL);
    expect(f.state.containerDetail).toBe(before);
    // The view reconnects and reads the detail under the current generation.
    await f.state.connect(f.bookmark.id);
    await f.state.openContainer("fixtures");
    await f.state.refreshContainerDetail();
    expect(f.state.containerDetail?.scope).toEqual({ bookmarkId: f.bookmark.id, generation: 2 });
    const result = await f.state.saveContainer({ ...(f.state.containerDetail!.settings as object), meta: { edited: "yes" } } as never);
    expect(result.outcome).toBe("applied");
  });

  it("refuses an object metadata edit whose detail was read under an earlier generation", async () => {
    const f = await fixture();
    const index = f.state.entries.findIndex((entry) => entry.kind === "object");
    expect(index).toBeGreaterThanOrEqual(0);
    await f.state.select(index);
    const detail = f.state.objectDetail;
    expect(detail?.scope).toEqual({ bookmarkId: f.bookmark.id, generation: 1 });
    await moveConnection(f);
    await expect(f.state.saveObjectMetadata({ meta: { edited: "yes" }, contentType: detail!.contentType, deleteAt: "" })).rejects.toThrow(REFUSAL);
    expect(f.state.objectDetail).toBe(detail);
  });

  it("refuses a container creation under the portrait captured before the connection moved", async () => {
    const f = await fixture();
    const captured = f.state.portrait;
    await moveConnection(f);
    await expect(f.state.createContainer({ name: "late", policy: "", meta: {} }, captured)).rejects.toThrow(REFUSAL);
    expect(f.state.containers.some((c) => c.name === "late")).toBe(false);
    await expect(f.state.createContainer({ name: "late", policy: "", meta: {} }, null)).rejects.toThrow(/connection changed/);
  });

  it("refuses a marker creation whose prefix detail was read under an earlier generation", async () => {
    const f = await fixture();
    const index = f.state.entries.findIndex((entry) => entry.kind === "prefix");
    expect(index).toBeGreaterThanOrEqual(0);
    await f.state.select(index);
    const prefix = f.state.prefixDetail;
    expect(prefix?.scope).toEqual({ bookmarkId: f.bookmark.id, generation: 1 });
    await moveConnection(f);
    await expect(f.state.createMarker(prefix!.prefix)).rejects.toThrow(REFUSAL);
    expect(f.state.prefixDetail).toBe(prefix);
  });
});

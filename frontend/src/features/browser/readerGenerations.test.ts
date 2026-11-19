import { describe, expect, it } from "vitest";
import { flushSync } from "svelte";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import type { ListingScope } from "../../lib/api";

// ADR 0075: the six live readers (object, prefix and container detail, SLO
// manifest, DLO segments, Find status) accept a reply only under the
// bookmark and generation of the portrait captured when the read started.
// The fake serves a new generation once the bookmark is re-saved, as the
// product does on a rekey, so a reply from the reconnected generation
// reaches a browser still holding generation 1.

async function settle() {
  for (let i = 0; i < 24; i++) { await Promise.resolve(); flushSync(); }
}

async function connected() {
  const api = newFakeApi();
  const state = new BrowserState(api);
  const bookmark = await state.saveBookmark({ id: "", displayName: "Reader generation fixture",
    authUrl: "http://mock/auth/v1.0", username: "test:tester", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "",
    tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "testing");
  expect(await state.connect(bookmark.id)).toBe(true);
  await state.openContainer("fixtures");
  await settle();
  expect(state.account?.scope).toEqual({ bookmarkId: bookmark.id, generation: 1 });
  return { api, state, bookmark };
}

type Fixture = Awaited<ReturnType<typeof connected>>;

async function reconnectElsewhere(f: Fixture): Promise<ListingScope> {
  // A separate API caller re-saves the bookmark and reconnects; this
  // browser's portrait and lifetime are left alone.
  await f.api.saveBookmark(f.bookmark, "re-entered valid key");
  const current = await f.api.connect(f.bookmark.id);
  expect(current.scope).toEqual({ bookmarkId: f.bookmark.id, generation: 2 });
  expect(f.state.account?.scope).toEqual({ bookmarkId: f.bookmark.id, generation: 1 });
  return { ...current.scope };
}

function entryIndex(f: Fixture, name: string): number {
  const index = f.state.entries.findIndex((entry) => entry.name === name);
  expect(index).toBeGreaterThanOrEqual(0);
  return index;
}

describe("live reader generation acceptance", () => {
  it.each([false, true])("discards container facts after switching bookmarks (switch back: %s)", async (switchBack) => {
    const f = await connected();
    const other = await f.state.saveBookmark({ ...f.bookmark, id: "", displayName: "Other account" }, "testing");
    const detail = await f.api.containerDetail("fixtures", f.bookmark.id);
    const old = { ...detail, settings: { ...detail.settings, versioningMode: "history_location", versioningArchive: "old-archive" } };
    let release!: () => void;
    const held = new Promise<void>((resolve) => { release = resolve; });
    const read = f.api.containerDetail;
    f.api.containerDetail = async (name, bookmarkId) => {
      if (name === "fixtures" && bookmarkId === f.bookmark.id) {
        await held;
        return old;
      }
      return read(name, bookmarkId);
    };
    f.state.containers = f.state.containers.map((row) => ({ ...row, settings: null }));
    const pending = f.state.containerFacts("fixtures");
    await f.state.connect(other.id);
    if (switchBack) await f.state.connect(f.bookmark.id);
    expect(f.state.account?.scope.generation).toBe(1);
    const before = f.state.containers.find((row) => row.name === "fixtures")?.settings;
    release();
    expect(await pending).toBeNull();
    expect(f.state.containers.find((row) => row.name === "fixtures")?.settings).toEqual(before);
  });

  it("publishes container facts while the original connection is current", async () => {
    const f = await connected();
    f.state.containers = f.state.containers.map((row) => ({ ...row, settings: null }));
    const facts = await f.state.containerFacts("fixtures");
    expect(facts?.generation).toBe(1);
    expect(f.state.containers.find((row) => row.name === "fixtures")?.settings).toEqual(facts);
  });

  it("accepts detail, manifest and segment replies of the held generation", async () => {
    const f = await connected();
    f.state.select(entryIndex(f, "external-slo")); await settle();
    expect(f.state.detailError).toBeNull();
    expect(f.state.objectDetail?.name).toBe("external-slo");
    expect(f.state.sloManifest?.scope).toEqual(f.state.account?.scope);
    f.state.select(entryIndex(f, "external-dlo")); await settle();
    expect(f.state.detailError).toBeNull();
    expect(f.state.dloSegments?.scope).toEqual(f.state.account?.scope);
    f.state.select(entryIndex(f, "implied/")); await settle();
    expect(f.state.detailError).toBeNull();
    expect(f.state.prefixDetail?.scope).toEqual(f.state.account?.scope);
  });

  it("refuses an object detail from the reconnected generation and shows no body", async () => {
    const f = await connected();
    await reconnectElsewhere(f);
    f.state.select(entryIndex(f, "external-slo")); await settle();
    expect(f.state.objectDetail).toBeNull();
    expect(f.state.sloManifest).toBeNull();
    expect(f.state.detailLoading).toBe(false);
    expect(f.state.detailError).toBe("The object detail does not match this connection and was not shown. Reconnect and try again.");
  });

  it("refuses a prefix detail from the reconnected generation", async () => {
    const f = await connected();
    await reconnectElsewhere(f);
    f.state.select(entryIndex(f, "implied/")); await settle();
    expect(f.state.prefixDetail).toBeNull();
    expect(f.state.detailError).toBe("The prefix detail does not match this connection and was not shown. Reconnect and try again.");
  });

  it("refuses a manifest from another generation without opening the table", async () => {
    const f = await connected();
    const detail = f.api.objectDetail;
    const manifest = f.api.sloManifest;
    // The detail itself answers the held generation; only the manifest that
    // follows it comes from a later one.
    f.api.sloManifest = async (container, name, bookmarkId) => {
      const m = await manifest(container, name, bookmarkId);
      return { ...m, scope: { ...m.scope, generation: m.scope.generation + 1 } };
    };
    f.state.select(entryIndex(f, "external-slo")); await settle();
    expect(f.state.objectDetail?.name).toBe("external-slo");
    expect(f.state.sloManifest).toBeNull();
    expect(f.state.detailError).toBe("The manifest does not match this connection and was not shown. Reconnect and try again.");
    f.api.objectDetail = detail;
  });

  it("refuses a segment preview from another generation without opening the table", async () => {
    const f = await connected();
    const segments = f.api.dloSegments;
    f.api.dloSegments = async (manifest, bookmarkId) => {
      const s = await segments(manifest, bookmarkId);
      return { ...s, scope: { ...s.scope, generation: s.scope.generation + 1 } };
    };
    f.state.select(entryIndex(f, "external-dlo")); await settle();
    expect(f.state.objectDetail?.name).toBe("external-dlo");
    expect(f.state.dloSegments).toBeNull();
    expect(f.state.detailError).toBe("The segment preview does not match this connection and was not shown. Reconnect and try again.");
  });

  it.each<[string, (valid: ListingScope) => unknown]>([
    ["undefined", () => undefined],
    ["null", () => null],
    ["another bookmark", (valid) => ({ ...valid, bookmarkId: "other-account" })],
    ["generation zero", (valid) => ({ ...valid, generation: 0 })],
    ["a fractional generation", (valid) => ({ ...valid, generation: 1.5 })],
    ["a missing generation", (valid) => ({ bookmarkId: valid.bookmarkId })],
  ])("refuses an object detail whose scope is %s", async (_name, malform) => {
    const f = await connected();
    const detail = f.api.objectDetail;
    f.api.objectDetail = async (container, name, bookmarkId) => {
      const d = await detail(container, name, bookmarkId);
      return { ...d, scope: malform(d.scope) as ListingScope };
    };
    f.state.select(entryIndex(f, "external-slo")); await settle();
    expect(f.state.objectDetail).toBeNull();
    expect(f.state.detailError).toMatch(/object detail does not match this connection/);
  });

  it("refuses a container detail from the reconnected generation", async () => {
    const f = await connected();
    await reconnectElsewhere(f);
    await f.state.refreshContainerDetail(); await settle();
    expect(f.state.containerDetail).toBeNull();
    expect(f.state.containerDetailError).toBe("the container detail does not match this connection and was not shown; reconnect and try again");
    expect(f.state.openContainerSettings).toBeNull();
  });

  it("ends a scan whose status comes from the reconnected generation as failed", async () => {
    const f = await connected();
    await reconnectElsewhere(f);
    await f.state.startFind("query"); await settle();
    expect(f.state.findRunning).toBe(false);
    expect(f.state.findStatus?.done).toBe(true);
    expect(f.state.findStatus?.matches).toEqual([]);
    expect(f.state.findStatus?.error).toBe("The scan does not match this connection and was not shown. Reconnect and try again.");
  });

  it("ends a count whose status comes from the reconnected generation as failed", async () => {
    const f = await connected();
    await reconnectElsewhere(f);
    await f.state.startCount(); await settle();
    expect(f.state.countRunning).toBe(false);
    expect(f.state.countStatus?.error).toBe("The count does not match this connection and was not shown. Reconnect and try again.");
  });
});

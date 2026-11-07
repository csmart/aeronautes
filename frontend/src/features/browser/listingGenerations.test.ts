import { describe, expect, it, vi } from "vitest";
import { flushSync } from "svelte";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";
import type { ContainerPage, ListingScope, ObjectPage } from "../../lib/api";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

async function settle() {
  for (let i = 0; i < 24; i++) { await Promise.resolve(); flushSync(); }
}

async function connected() {
  const api = newFakeApi();
  const state = new BrowserState(api);
  const bookmark = await state.saveBookmark({ id: "", displayName: "Generation fixture",
    authUrl: "http://mock/auth/v1.0", username: "test:tester", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "",
    tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "testing");
  let servedScope: ListingScope = { bookmarkId: bookmark.id, generation: 1 };
  const connect = api.connect;
  api.connect = async (id) => {
    const account = await connect(id);
    servedScope = { ...account.scope };
    return account;
  };
  const object = (await api.objects(bookmark.id, "fixtures", "", "", 100)).rows
    .find((row) => row.kind === "object")!;
  // Explicit service replies keep the browser test independent of the fake's
  // own scope mapper, which has a separate regression below.
  api.containers = async (id) => {
    expect(id).toBe(servedScope.bookmarkId);
    return { rows: [{ name: "fixtures", count: servedScope.generation, bytes: 11, class: "normal", settings: null }],
      nextMarker: "fixtures", effectiveLimit: 1000, scope: { ...servedScope } };
  };
  api.objects = async (id) => {
    expect(id).toBe(servedScope.bookmarkId);
    return { rows: [{ ...object, name: "first", bytes: servedScope.generation }],
      nextMarker: "first", effectiveLimit: 1000, scope: { ...servedScope } };
  };
  expect(await state.connect(bookmark.id)).toBe(true);
  await state.openContainer("fixtures");
  await settle();
  expect(state.account?.scope).toEqual({ bookmarkId: bookmark.id, generation: 1 });
  expect(state.capabilities?.scope).toEqual(state.account?.scope);
  return { api, state, bookmark, object };
}

type Fixture = Awaited<ReturnType<typeof connected>>;
type Read = "loadContainers" | "loadMoreContainers" | "goTo" | "loadMoreEntries";
const reads: Read[] = ["loadContainers", "loadMoreContainers", "goTo", "loadMoreEntries"];

function heldRead(f: Fixture, method: Read) {
  const containers = method === "loadContainers" || method === "loadMoreContainers";
  const more = method === "loadMoreContainers" || method === "loadMoreEntries";
  const containerReply = deferred<ContainerPage>();
  const objectReply = deferred<ObjectPage>();
  const names = () => (containers ? f.state.containers : f.state.entries).map((row) => row.name);
  const marker = () => containers ? f.state.containersMarker : f.state.entriesMarker;
  const before = { names: names(), marker: marker() };
  if (containers) f.api.containers = vi.fn(f.api.containers).mockImplementationOnce(() => containerReply.promise);
  else f.api.objects = vi.fn(f.api.objects).mockImplementationOnce(() => objectReply.promise);
  const operation = method === "goTo" ? f.state.goTo("") : f.state[method]();
  const pending = operation.then(() => null, (error: Error) => error);
  const error = () => containers ? [f.state.containersError, f.state.containersMoreError]
    : [f.state.entriesError, f.state.entriesMoreError];
  return {
    pending, names, marker, error, more, before,
    loading: () => containers ? [f.state.containersLoading, f.state.containersMoreLoading]
      : [f.state.entriesLoading, f.state.entriesMoreLoading],
    request: () => containers ? vi.mocked(f.api.containers).mock.calls.at(-1)
      : vi.mocked(f.api.objects).mock.calls.at(-1),
    resolve: (scope: unknown, name = "z-later", nextMarker = "z-later") => {
      // A malformed transport reply must be rejected at runtime, despite the
      // static API type promising a scope.
      const replyScope = scope as ListingScope;
      if (containers) containerReply.resolve({ rows: [{ name, count: 1, bytes: 11, class: "normal", settings: null }],
        nextMarker, effectiveLimit: 1000, scope: replyScope });
      else objectReply.resolve({ rows: [{ ...f.object, name }], nextMarker, effectiveLimit: 1000, scope: replyScope });
    },
    reject: (error: Error) => containers ? containerReply.reject(error) : objectReply.reject(error),
    refusedNames: method === "goTo" ? [] : before.names,
    refusedMarker: more ? before.marker : "",
  };
}

async function reconnectElsewhere(f: Fixture, replace: boolean): Promise<ListingScope> {
  // A separate API caller leaves this browser's portrait and lifetime alone.
  if (replace) await f.api.saveBookmark(f.bookmark, "re-entered valid key");
  const current = await f.api.connect(f.bookmark.id);
  expect(current.scope).toEqual({ bookmarkId: f.bookmark.id, generation: replace ? 2 : 1 });
  expect(f.state.account?.scope).toEqual({ bookmarkId: f.bookmark.id, generation: 1 });
  return { ...current.scope };
}

const malformedScopes: { name: string; scope: (valid: ListingScope) => unknown }[] = [
  { name: "missing", scope: () => undefined },
  { name: "null", scope: () => null },
  { name: "missing bookmark", scope: () => ({ generation: 1 }) },
  { name: "empty bookmark", scope: (valid) => ({ ...valid, bookmarkId: "" }) },
  { name: "another bookmark", scope: (valid) => ({ ...valid, bookmarkId: "another" }) },
  { name: "missing generation", scope: (valid) => ({ bookmarkId: valid.bookmarkId }) },
  { name: "nonnumeric generation", scope: (valid) => ({ ...valid, generation: "1" }) },
  { name: "zero generation", scope: (valid) => ({ ...valid, generation: 0 }) },
  { name: "negative generation", scope: (valid) => ({ ...valid, generation: -1 }) },
  { name: "fractional generation", scope: (valid) => ({ ...valid, generation: 1.5 }) },
  { name: "NaN generation", scope: (valid) => ({ ...valid, generation: NaN }) },
  { name: "infinite generation", scope: (valid) => ({ ...valid, generation: Infinity }) },
  { name: "unsafe generation", scope: (valid) => ({ ...valid, generation: Number.MAX_SAFE_INTEGER + 1 }) },
];

describe.each(reads)("later %s generation", (method) => {
  it("publishes a delayed matching reply after an unchanged-generation reconnect", async () => {
    const f = await connected();
    const scope = await reconnectElsewhere(f, false);
    const held = heldRead(f, method);
    held.resolve(scope);
    expect(await held.pending).toBeNull();
    expect(held.names()).toContain("z-later");
    expect(held.marker()).toBe("z-later");
    expect(held.error()).toEqual([null, null]);
    expect(held.loading()).toEqual([false, false]);
    expect(f.state.account?.scope).toEqual(scope);
  });

  it.each(["rows", "cursor", "error"])("refuses a newer generation beneath the retained portrait: %s", async (assertion) => {
    const f = await connected();
    const revision = f.state.scopeRevision;
    const portrait = f.state.account;
    const scope = await reconnectElsewhere(f, true);
    expect(f.state.scopeRevision).toBe(revision);
    const held = heldRead(f, method);
    held.resolve(scope);
    const result = await held.pending;
    expect(f.state.account).toBe(portrait);
    expect(f.state.account?.scope.generation).toBe(1);
    if (assertion === "rows") expect(held.names()).toEqual(held.refusedNames);
    else if (assertion === "cursor") expect(held.marker()).toBe(held.refusedMarker);
    else {
      expect(held.error()[held.more ? 1 : 0]).toEqual(expect.stringMatching(/does not match this connection and was not shown\. Reconnect/));
      expect(held.loading()).toEqual([false, false]);
      if (held.more) expect(result).toBeInstanceOf(Error);
      else expect(result).toBeNull();
    }
  });

  it("accepts a captured old reply after an external re-save", async () => {
    const f = await connected();
    const captured = { ...f.state.account!.scope };
    const held = heldRead(f, method);
    await reconnectElsewhere(f, true);
    held.resolve(captured);
    expect(await held.pending).toBeNull();
    expect(held.names()).toContain("z-later");
    expect(held.marker()).toBe("z-later");
    expect(held.error()).toEqual([null, null]);
    expect(f.state.account?.scope).toEqual(captured);
    expect(f.state.capabilities?.scope).toEqual(captured);
  });

  it.each(["reply", "error"])("discards an old delayed %s after this browser re-saves and reconnects", async (outcome) => {
    const f = await connected();
    const captured = { ...f.state.account!.scope };
    const held = heldRead(f, method);
    await f.state.saveBookmark(f.bookmark, "re-entered valid key");
    expect(await f.state.connect(f.bookmark.id)).toBe(true);
    await f.state.openContainer("fixtures");
    await settle();
    const current = { names: held.names(), marker: held.marker(), error: held.error(), loading: held.loading() };
    if (outcome === "reply") held.resolve(captured);
    else held.reject(new Error("old generation refused"));
    expect(await held.pending).toBeNull();
    expect(f.state.account?.scope).toEqual({ bookmarkId: f.bookmark.id, generation: 2 });
    expect(held.names()).toEqual(current.names);
    expect(held.marker()).toBe(current.marker);
    expect(held.error()).toEqual(current.error);
    expect(held.loading()).toEqual(current.loading);
  });

  it.each(malformedScopes)("refuses $name scope with no row or cursor publication", async ({ scope }) => {
    const f = await connected();
    const held = heldRead(f, method);
    held.resolve(scope(f.state.account!.scope));
    const result = await held.pending;
    expect.soft(held.names()).toEqual(held.refusedNames);
    expect.soft(held.marker()).toBe(held.refusedMarker);
    expect.soft(held.error()[held.more ? 1 : 0]).toEqual(expect.stringMatching(/was not shown/));
    expect.soft(held.loading()).toEqual([false, false]);
    if (held.more) expect.soft(result).toBeInstanceOf(Error);
  });

  it("reports a current service refusal through the existing failure path", async () => {
    const f = await connected();
    const held = heldRead(f, method);
    const failure = new Error("current service refusal");
    held.reject(failure);
    const result = await held.pending;
    expect(held.error()[held.more ? 1 : 0]).toBe(failure.message);
    expect(held.names()).toEqual(held.refusedNames);
    expect(held.marker()).toBe(held.refusedMarker);
    expect(held.loading()).toEqual([false, false]);
    expect(result).toBe(held.more ? failure : null);
  });
});

it.each<Read>(["loadMoreContainers", "loadMoreEntries"])("%s retries a refused page at the exact cursor", async (method) => {
  const f = await connected();
  const held = heldRead(f, method);
  const request = held.request();
  held.resolve({ ...f.state.account!.scope, generation: 2 });
  expect(await held.pending).toBeInstanceOf(Error);
  expect(held.names()).toEqual(held.before.names);
  expect(held.marker()).toBe(held.before.marker);
  const retry = heldRead(f, method);
  expect(retry.request()).toEqual(request);
  retry.resolve(f.state.account!.scope);
  expect(await retry.pending).toBeNull();
  expect(retry.names()).toContain("z-later");
  expect(retry.marker()).toBe("z-later");
  expect(retry.error()).toEqual([null, null]);
});

it.each(["containers", "objects"] as const)("the shared fake %s listing follows the connected generation", async (kind) => {
  const api = newFakeApi();
  const bookmark = await api.saveBookmark({ id: "", displayName: "Fake scope",
    authUrl: "http://mock/auth/v1.0", username: "test:tester", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "",
    tlsMode: "system", caPem: "", hasCa: false, hasCredential: false }, "testing");
  const first = await api.connect(bookmark.id);
  const read = () => kind === "containers" ? api.containers(bookmark.id, "", 100)
    : api.objects(bookmark.id, "fixtures", "", "", 100);
  expect((await read()).scope).toEqual(first.scope);
  await api.saveBookmark(bookmark, "replacement-key");
  const second = await api.connect(bookmark.id);
  expect(second.scope).toEqual({ bookmarkId: bookmark.id, generation: 2 });
  expect((await read()).scope).toEqual(second.scope);
});

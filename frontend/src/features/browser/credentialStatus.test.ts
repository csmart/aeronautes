import { describe, expect, it, vi } from "vitest";
import type { BookmarkDTO, CredentialStatusDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";

const empty: CredentialStatusDTO = { retired: 0, unfinished: 0, saving: 0, unverified: 0, cleanupReason: "" };
const retained: CredentialStatusDTO = { retired: 2, unfinished: 1, saving: 0, unverified: 1, cleanupReason: "" };
const bookmark: BookmarkDTO = {
  id: "", displayName: "test", authUrl: "https://swift.example/auth/v1.0",
  username: "account:user", storageUrlOverride: "", authVersion: 1, userDomain: "", projectName: "", projectDomain: "", region: "", tlsMode: "system",
  caPem: "", hasCa: false, hasCredential: false,
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

describe("credential cleanup status", () => {
  it("reports a failed read as unknown and lets a later refresh recover", async () => {
    const api = newFakeApi();
    api.credentialStatus = vi.fn().mockResolvedValueOnce(retained)
      .mockRejectedValueOnce(new Error("private backend details"))
      .mockResolvedValueOnce(empty);
    const state = new BrowserState(api);
    await state.loadCredentialStatus();
    expect(state.credentialStatus).toEqual(retained);
    await state.loadCredentialStatus();
    expect(state.credentialStatus).toBeNull();
    expect(state.credentialStatusError).toBe(true);
    expect(state.credentialStatusLoading).toBe(false);
    await state.loadCredentialStatus();
    expect(state.credentialStatusError).toBe(false);
    expect(state.credentialStatus).toEqual(empty);
  });

  it.each(["success", "failure"])("ignores an older %s after a newer read", async (outcome) => {
    const api = newFakeApi();
    const old = deferred<CredentialStatusDTO>();
    api.credentialStatus = vi.fn().mockReturnValueOnce(old.promise).mockResolvedValueOnce(retained);
    const state = new BrowserState(api);
    const first = state.loadCredentialStatus();
    await state.loadCredentialStatus();
    if (outcome === "success") old.resolve(empty);
    else old.reject(new Error("old read failed"));
    await first;
    expect(state.credentialStatus).toEqual(retained);
    expect(state.credentialStatusError).toBe(false);
    expect(state.credentialStatusLoading).toBe(false);
  });

  it.each(["save", "delete"])("invalidates an older read as soon as %s starts", async (operation) => {
    const api = newFakeApi();
    const old = deferred<CredentialStatusDTO>();
    const mutation = deferred<void>();
    api.credentialStatus = vi.fn().mockReturnValueOnce(old.promise).mockResolvedValueOnce(retained);
    api.saveBookmark = async () => { await mutation.promise; return { ...bookmark, id: "saved" }; };
    api.deleteBookmark = async () => mutation.promise;
    const state = new BrowserState(api);
    const read = state.loadCredentialStatus();
    const change = operation === "save" ? state.saveBookmark(bookmark, "new-key") : state.deleteBookmark("saved");
    old.resolve(empty);
    await read;
    expect(state.credentialStatus).toBeNull();
    expect(state.credentialStatusLoading).toBe(true);
    mutation.resolve();
    await change;
    await vi.waitFor(() => expect(state.credentialStatus).toEqual(retained));
  });

  it.each(["save", "delete"])("refreshes after a refused %s without replacing its error", async (operation) => {
    const api = newFakeApi();
    const failure = new Error("publication outcome unresolved");
    api.saveBookmark = async () => { throw failure; };
    api.deleteBookmark = async () => { throw failure; };
    api.credentialStatus = vi.fn().mockResolvedValue(retained);
    const state = new BrowserState(api);
    const change = operation === "save" ? state.saveBookmark(bookmark, "new-key") : state.deleteBookmark("saved");
    await expect(change).rejects.toBe(failure);
    await vi.waitFor(() => expect(state.credentialStatus).toEqual(retained));
    expect(api.credentialStatus).toHaveBeenCalledOnce();
  });

  it("does not fail or delay successful mutations when the status read is unavailable", async () => {
    const api = newFakeApi();
    const status = deferred<CredentialStatusDTO>();
    api.credentialStatus = vi.fn().mockReturnValue(status.promise);
    const state = new BrowserState(api);
    const saved = await state.saveBookmark(bookmark, "new-key");
    expect(saved.id).not.toBe("");
    expect(state.bookmarks).toHaveLength(1);
    await state.deleteBookmark(saved.id);
    expect(state.bookmarks).toHaveLength(0);
    status.reject(new Error("status unavailable"));
    await vi.waitFor(() => expect(state.credentialStatusError).toBe(true));
    expect(state.credentialStatus).toBeNull();
  });
});

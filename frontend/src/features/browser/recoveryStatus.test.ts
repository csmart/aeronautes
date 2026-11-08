import { describe, expect, it, vi } from "vitest";
import type { RecoveryStatusDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";

const empty: RecoveryStatusDTO = { jobs: 0, records: 0 };
const retained: RecoveryStatusDTO = { jobs: 7, records: 21 };

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

describe("recovery record status", () => {
  it("turns a failed read into unknown and recovers on a later success", async () => {
    const api = newFakeApi();
    api.recoveryStatus = vi.fn().mockResolvedValueOnce(retained)
      .mockRejectedValueOnce(new Error("private database details")).mockResolvedValueOnce(empty);
    const state = new BrowserState(api);
    await state.jobs.loadRecoveryStatus();
    expect(state.jobs.recoveryStatus).toEqual(retained);
    await state.jobs.loadRecoveryStatus();
    expect(state.jobs.recoveryStatus).toBeNull();
    expect(state.jobs.recoveryError).toBe(true);
    expect(state.jobs.recoveryLoading).toBe(false);
    await state.jobs.loadRecoveryStatus();
    expect(state.jobs.recoveryStatus).toEqual(empty);
    expect(state.jobs.recoveryError).toBe(false);
  });

  it.each(["success", "failure"])("ignores an older %s after a newer read", async (outcome) => {
    const api = newFakeApi();
    const old = deferred<RecoveryStatusDTO>();
    api.recoveryStatus = vi.fn().mockReturnValueOnce(old.promise).mockResolvedValueOnce(retained);
    const state = new BrowserState(api);
    const first = state.jobs.loadRecoveryStatus();
    await state.jobs.loadRecoveryStatus();
    if (outcome === "success") old.resolve(empty);
    else old.reject(new Error("old read failed"));
    await first;
    expect(state.jobs.recoveryStatus).toEqual(retained);
    expect(state.jobs.recoveryError).toBe(false);
    expect(state.jobs.recoveryLoading).toBe(false);
  });

  it("does not let an older zero replace a newer unavailable result", async () => {
    const api = newFakeApi();
    const old = deferred<RecoveryStatusDTO>();
    api.recoveryStatus = vi.fn().mockReturnValueOnce(old.promise)
      .mockRejectedValueOnce(new Error("database unavailable"));
    const state = new BrowserState(api);
    const first = state.jobs.loadRecoveryStatus();
    await state.jobs.loadRecoveryStatus();
    old.resolve(empty);
    await first;
    expect(state.jobs.recoveryStatus).toBeNull();
    expect(state.jobs.recoveryError).toBe(true);
  });

  it("keeps the last successful counts during a refresh without clearing a newer pending read", async () => {
    const api = newFakeApi();
    const first = deferred<RecoveryStatusDTO>();
    const second = deferred<RecoveryStatusDTO>();
    api.recoveryStatus = vi.fn().mockResolvedValueOnce(retained)
      .mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const state = new BrowserState(api);
    await state.jobs.loadRecoveryStatus();
    const older = state.jobs.loadRecoveryStatus();
    const latest = state.jobs.loadRecoveryStatus();
    first.resolve(empty);
    await older;
    expect(state.jobs.recoveryStatus).toEqual(retained);
    expect(state.jobs.recoveryLoading).toBe(true);
    second.resolve(empty);
    await latest;
    expect(state.jobs.recoveryLoading).toBe(false);
    expect(state.jobs.recoveryStatus).toEqual(empty);
  });

  it("keeps global counts independent of the recent-job request and account filter", async () => {
    const api = newFakeApi();
    api.jobs = vi.fn().mockRejectedValue(new Error("recent jobs unavailable"));
    api.recoveryStatus = vi.fn().mockResolvedValue(retained);
    const state = new BrowserState(api);
    state.jobs.bookmarkFilter = "different-account";
    await Promise.all([state.jobs.load(), state.jobs.loadRecoveryStatus()]);
    expect(api.jobs).toHaveBeenCalledWith("different-account", false, 50);
    expect(api.recoveryStatus).toHaveBeenCalledWith();
    expect(state.jobs.error).not.toBeNull();
    expect(state.jobs.recoveryStatus).toEqual(retained);
  });
});

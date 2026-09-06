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
    await state.loadRecoveryStatus();
    expect(state.recoveryStatus).toEqual(retained);
    await state.loadRecoveryStatus();
    expect(state.recoveryStatus).toBeNull();
    expect(state.recoveryStatusError).toBe(true);
    expect(state.recoveryStatusLoading).toBe(false);
    await state.loadRecoveryStatus();
    expect(state.recoveryStatus).toEqual(empty);
    expect(state.recoveryStatusError).toBe(false);
  });

  it.each(["success", "failure"])("ignores an older %s after a newer read", async (outcome) => {
    const api = newFakeApi();
    const old = deferred<RecoveryStatusDTO>();
    api.recoveryStatus = vi.fn().mockReturnValueOnce(old.promise).mockResolvedValueOnce(retained);
    const state = new BrowserState(api);
    const first = state.loadRecoveryStatus();
    await state.loadRecoveryStatus();
    if (outcome === "success") old.resolve(empty);
    else old.reject(new Error("old read failed"));
    await first;
    expect(state.recoveryStatus).toEqual(retained);
    expect(state.recoveryStatusError).toBe(false);
    expect(state.recoveryStatusLoading).toBe(false);
  });

  it("does not let an older zero replace a newer unavailable result", async () => {
    const api = newFakeApi();
    const old = deferred<RecoveryStatusDTO>();
    api.recoveryStatus = vi.fn().mockReturnValueOnce(old.promise)
      .mockRejectedValueOnce(new Error("database unavailable"));
    const state = new BrowserState(api);
    const first = state.loadRecoveryStatus();
    await state.loadRecoveryStatus();
    old.resolve(empty);
    await first;
    expect(state.recoveryStatus).toBeNull();
    expect(state.recoveryStatusError).toBe(true);
  });

  it("keeps the last successful counts during a refresh without clearing a newer pending read", async () => {
    const api = newFakeApi();
    const first = deferred<RecoveryStatusDTO>();
    const second = deferred<RecoveryStatusDTO>();
    api.recoveryStatus = vi.fn().mockResolvedValueOnce(retained)
      .mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const state = new BrowserState(api);
    await state.loadRecoveryStatus();
    const older = state.loadRecoveryStatus();
    const latest = state.loadRecoveryStatus();
    first.resolve(empty);
    await older;
    expect(state.recoveryStatus).toEqual(retained);
    expect(state.recoveryStatusLoading).toBe(true);
    second.resolve(empty);
    await latest;
    expect(state.recoveryStatusLoading).toBe(false);
    expect(state.recoveryStatus).toEqual(empty);
  });

  it("keeps global counts independent of the recent-job request and account filter", async () => {
    const api = newFakeApi();
    api.jobs = vi.fn().mockRejectedValue(new Error("recent jobs unavailable"));
    api.recoveryStatus = vi.fn().mockResolvedValue(retained);
    const state = new BrowserState(api);
    state.jobsFilter = "different-account";
    await Promise.all([state.loadJobs(), state.loadRecoveryStatus()]);
    expect(api.jobs).toHaveBeenCalledWith("different-account", false, 50);
    expect(api.recoveryStatus).toHaveBeenCalledWith();
    expect(state.jobsError).not.toBeNull();
    expect(state.recoveryStatus).toEqual(retained);
  });
});

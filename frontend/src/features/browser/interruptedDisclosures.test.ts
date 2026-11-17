import { describe, expect, it, vi } from "vitest";
import type { InterruptedDisclosuresDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";

function page(total: number, ids: string[] = []): InterruptedDisclosuresDTO {
  return {
    total,
    entries: ids.map((id) => ({
      jobId: id, type: "download", source: "src", destination: "dst",
      createdAt: "2026-10-19T00:00:00Z", interruptedAt: "2026-10-19T00:00:01Z",
      interruptedState: "queued", attempted: false, itemsCaptured: false,
      items: { items: 0, admitted: 0, copied: 0, verified: 0, copiedBytes: 0, failed: 0, conflicts: 0, refused: 0, unresolved: 0, unverified: 0, deleted: 0, unsettled: 0 },
      recoveryRecords: 0, placesLocalResidue: true,
    })),
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

describe("interrupted-job notice state", () => {
  it("reads the notice; a failed read is unknown rather than empty", async () => {
    const api = newFakeApi();
    const notice = page(2, ["a", "b"]);
    api.interruptedDisclosures = vi.fn().mockResolvedValueOnce(notice)
      .mockRejectedValueOnce(new Error("private db detail")).mockResolvedValueOnce(page(0));
    const state = new BrowserState(api);
    await state.jobs.loadInterrupted();
    expect(state.jobs.interrupted).toEqual(notice);
    expect(state.jobs.interruptedError).toBe(false);
    await state.jobs.loadInterrupted();
    expect(state.jobs.interrupted).toBeNull();
    expect(state.jobs.interruptedError).toBe(true);
    await state.jobs.loadInterrupted();
    expect(state.jobs.interrupted).toEqual(page(0));
    expect(state.jobs.interruptedError).toBe(false);
  });

  it("dismisses the named notices and reloads so the banner updates", async () => {
    const api = newFakeApi();
    api.dismissInterrupted = vi.fn().mockResolvedValue({ dismissed: 1 });
    api.interruptedDisclosures = vi.fn().mockResolvedValue(page(1, ["b"]));
    const state = new BrowserState(api);
    await state.jobs.dismissInterrupted(["a"]);
    expect(api.dismissInterrupted).toHaveBeenCalledWith(["a"]);
    expect(state.jobs.dismissError).toBe(false);
    expect(state.jobs.interrupted).toEqual(page(1, ["b"]));
  });

  it("keeps the notice standing when a dismissal fails (decision 7)", async () => {
    const api = newFakeApi();
    api.dismissInterrupted = vi.fn().mockRejectedValue(new Error("write unavailable"));
    api.interruptedDisclosures = vi.fn();
    const state = new BrowserState(api);
    await state.jobs.dismissInterrupted(["a"]);
    expect(state.jobs.dismissError).toBe(true);
    expect(api.interruptedDisclosures).not.toHaveBeenCalled();
  });

  it.each(["success", "failure"])("ignores an older %s read after a newer one", async (outcome) => {
    const api = newFakeApi();
    const old = deferred<InterruptedDisclosuresDTO>();
    const latest = page(3, ["x", "y", "z"]);
    api.interruptedDisclosures = vi.fn().mockReturnValueOnce(old.promise).mockResolvedValueOnce(latest);
    const state = new BrowserState(api);
    const first = state.jobs.loadInterrupted();
    await state.jobs.loadInterrupted();
    expect(state.jobs.interrupted).toEqual(latest);
    if (outcome === "success") old.resolve(page(1, ["old"]));
    else old.reject(new Error("late"));
    await first;
    expect(state.jobs.interrupted).toEqual(latest);
    expect(state.jobs.interruptedError).toBe(false);
  });
});

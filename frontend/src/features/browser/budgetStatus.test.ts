import { afterEach, expect, it, vi } from "vitest";
import type { AppSettingsDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi, settingsRefusal } from "./fakeApi";

const original: AppSettingsDTO = {
  maxActiveJobs: 2, defaultProfile: "balanced", auditRetentionDays: 17, jobRetentionDays: 11, downloadObjectCeiling: 250_000,
  browserRetentionCeiling: 100_000, maxQueuedJobs: 16, maxConnsPerHost: 16,
  reclaimReplacedFragments: true, gpuAcceleration: true, railWidthPx: 0,
};
const releasePending: (() => void)[] = [];

afterEach(async () => {
  for (const release of releasePending.splice(0)) release();
  await Promise.resolve();
});

function deferred() {
  let resolve!: (value: AppSettingsDTO) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<AppSettingsDTO>((done, fail) => { resolve = done; reject = fail; });
  releasePending.push(() => resolve({ ...original }));
  return { promise, resolve, reject };
}

function fixture() {
  const api = newFakeApi();
  api.appSettings = vi.fn(async () => ({ ...original }));
  api.saveAppSettings = vi.fn(async (settings) => ({ ...settings }));
  return { api, browser: new BrowserState(api) };
}

it("loads the complete settings document without a write", async () => {
  const { api, browser } = fixture();
  await browser.settings.load();
  expect(browser.settings.document).toEqual(original);
  expect(api.appSettings).toHaveBeenCalledExactlyOnceWith();
  expect(api.saveAppSettings).not.toHaveBeenCalled();
});

it("awaits the acknowledged document and preserves every other field", async () => {
  const { api, browser } = fixture();
  const reply = deferred();
  api.saveAppSettings = vi.fn(() => reply.promise);
  await browser.settings.load();
  const requested = { ...original, maxActiveJobs: 4 };
  const saving = browser.settings.save(requested);
  expect(browser.settings.document).toEqual(original);
  expect(api.saveAppSettings).toHaveBeenCalledExactlyOnceWith(requested);
  reply.resolve(requested);
  await saving;
  expect(browser.settings.document).toEqual(requested);
});

it.each([false, true])("qualifies a rejected Get and explicitly recovers it (cached %s)", async (cached) => {
  const { api, browser } = fixture();
  if (cached) await browser.settings.load();
  api.appSettings = vi.fn().mockRejectedValueOnce(new Error("private settings read detail"))
    .mockResolvedValueOnce({ ...original, maxActiveJobs: 3 });
  await browser.settings.load();
  expect(browser.settings.document).toEqual(cached ? original : null);
  expect(browser.settings.status, "BUDGET-READ-STATUS").toBe(cached ? "stale" : "unavailable");
  expect(browser.settings.canSave, "BUDGET-READ-ADMISSION").toBe(false);
  expect(browser.settings.operation).toBeNull();
  await browser.settings.load();
  expect(browser.settings.document).toEqual({ ...original, maxActiveJobs: 3 });
  expect(browser.settings.status).toBe("current");
  expect(browser.settings.canSave).toBe(true);
  expect(api.saveAppSettings).not.toHaveBeenCalled();
});

it.each([false, true])("one pending Get excludes another Get and all Sets (cached %s)", async (cached) => {
  const { api, browser } = fixture();
  if (cached) await browser.settings.load();
  const reply = deferred();
  api.appSettings = vi.fn(() => reply.promise);
  const reading = browser.settings.load();
  const duplicate = browser.settings.load();
  const refusal = browser.settings.save({ ...original, maxActiveJobs: 4 }).catch((error: unknown) => error);
  expect.soft(api.appSettings, "BUDGET-GET-OWNER").toHaveBeenCalledTimes(1);
  expect.soft(api.saveAppSettings, "BUDGET-GET-BLOCKS-SET").not.toHaveBeenCalled();
  reply.resolve({ ...original, maxActiveJobs: 3 });
  await Promise.all([reading, duplicate]);
  expect(await refusal).toBeInstanceOf(Error);
  expect(browser.settings.document).toEqual({ ...original, maxActiveJobs: 3 });
  expect(browser.settings.operation).toBeNull();
  expect(browser.settings.canSave).toBe(true);
});

it("one pending Set excludes a Get and duplicate Set until acknowledgement", async () => {
  const { api, browser } = fixture();
  await browser.settings.load();
  const reply = deferred();
  api.saveAppSettings = vi.fn(() => reply.promise);
  const saving = browser.settings.save({ ...original, maxActiveJobs: 4 });
  const reading = browser.settings.load();
  const duplicate = browser.settings.save({ ...original, maxActiveJobs: 3 }).catch((error: unknown) => error);
  expect.soft(api.appSettings, "BUDGET-SET-BLOCKS-GET").toHaveBeenCalledTimes(1);
  expect.soft(api.saveAppSettings, "BUDGET-SET-OWNER").toHaveBeenCalledTimes(1);
  expect.soft(browser.settings.attempt).toEqual({ value: 4, state: "pending" });
  reply.resolve({ ...original, maxActiveJobs: 4 });
  await Promise.all([saving, reading]);
  expect(await duplicate).toBeInstanceOf(Error);
  expect(browser.settings.document).toEqual({ ...original, maxActiveJobs: 4 });
  expect(browser.settings.attempt).toEqual({ value: 4, state: "acknowledged" });
  expect(browser.settings.operation).toBeNull();
});

it.each([false, true])("uncertain Set requires an explicit successful Get (applied %s)", async (applied) => {
  const { api, browser } = fixture();
  let provider = { ...original };
  const failure = new Error("owned lost or refused Set reply");
  api.appSettings = vi.fn(async () => ({ ...provider }));
  api.saveAppSettings = vi.fn(async (settings) => {
    if (applied) provider = { ...settings };
    throw failure;
  });
  await browser.settings.load();
  await expect(browser.settings.save({ ...original, maxActiveJobs: 4 })).rejects.toBe(failure);
  expect(browser.settings.document).toEqual(original);
  expect(browser.settings.attempt, "BUDGET-UNCERTAIN").toEqual({ value: 4, state: "unconfirmed" });
  expect(browser.settings.status).toBe("unconfirmed");
  expect(browser.settings.operation).toBeNull();
  await expect(browser.settings.save({ ...original, maxActiveJobs: 3 })).rejects.toBeInstanceOf(Error);
  expect(api.saveAppSettings, "BUDGET-RECOVERY-REQUIRED").toHaveBeenCalledTimes(1);
  api.appSettings = vi.fn().mockRejectedValueOnce(new Error("private Get failure"))
    .mockResolvedValueOnce({ ...provider });
  await browser.settings.load();
  expect(browser.settings.canSave).toBe(false);
  expect(browser.settings.status).toBe("unconfirmed");
  expect(browser.settings.attempt?.state).toBe("unconfirmed");
  await browser.settings.load();
  expect(browser.settings.document).toEqual(provider);
  expect(browser.settings.canSave).toBe(true);
  expect(browser.settings.status).toBe("current");
  expect(browser.settings.attempt?.state, "BUDGET-READ-NOT-ACK").toBe("unconfirmed");
  expect(api.saveAppSettings).toHaveBeenCalledTimes(1);
  api.saveAppSettings = vi.fn(async (settings) => ({ ...settings }));
  await browser.settings.save({ ...browser.settings.document!, maxActiveJobs: 3 });
  expect(browser.settings.document).toEqual({ ...provider, maxActiveJobs: 3 });
  expect(browser.settings.attempt).toEqual({ value: 3, state: "acknowledged" });
});

// ADR 0105 decision 7. The service's own refusal is an answer: nothing was
// stored, so the document read before the save stays current and the next
// change needs no refresh, unlike a save whose reply never came.
it("keeps the settings current when the service refuses a save", async () => {
  const { api, browser } = fixture();
  await browser.settings.load();
  api.saveAppSettings = newFakeApi().saveAppSettings;
  await expect(browser.settings.save({ ...original, maxActiveJobs: 5 })).rejects.toBeInstanceOf(Error);
  expect(browser.settings.attempt).toEqual({
    value: 5, state: "refused", reason: "max active jobs 5 is out of range: choose 1 to 4",
  });
  expect(browser.settings.document).toEqual(original);
  expect(browser.settings.status).toBe("current");
  expect(browser.settings.canSave).toBe(true);

  // The bridge names a panic in the service as it names a returned error,
  // and a panic may come after the write. Only the service's own refusal
  // keeps the settings current; this one is unknown until read again.
  await browser.settings.load();
  api.saveAppSettings = vi.fn(async () => {
    const panic = new Error("SettingsService.Set: panic: runtime error");
    panic.name = "RuntimeError";
    throw panic;
  });
  await expect(browser.settings.save({ ...original, maxActiveJobs: 4 })).rejects.toBeInstanceOf(Error);
  expect(browser.settings.attempt).toEqual({ value: 4, state: "unconfirmed" });
  expect(browser.settings.status).toBe("unconfirmed");
  expect(browser.settings.canSave).toBe(false);

  // A save that leaves the budget alone says nothing about the budget,
  // whichever way it ends.
  await browser.settings.load();
  api.saveAppSettings = vi.fn(async () => { throw settingsRefusal("download object ceiling 5 out of range"); });
  await expect(browser.settings.save({ ...original, downloadObjectCeiling: 5 })).rejects.toBeInstanceOf(Error);
  expect(browser.settings.attempt).toBeNull();
  expect(browser.settings.canSave).toBe(true);
});

it("refuses a cold or stale write without issuing it", async () => {
  const { api, browser } = fixture();
  await expect(browser.settings.save(original), "BUDGET-COLD-WRITE").rejects.toBeInstanceOf(Error);
  expect(api.saveAppSettings).not.toHaveBeenCalled();
  await browser.settings.load();
  api.appSettings = vi.fn(async () => { throw new Error("owned stale Get"); });
  await browser.settings.load();
  await expect(browser.settings.save(original), "BUDGET-STALE-WRITE").rejects.toBeInstanceOf(Error);
  expect(api.saveAppSettings).not.toHaveBeenCalled();
});

it("publishes the acknowledged reply rather than guessing it from the input", async () => {
  const { api, browser } = fixture();
  await browser.settings.load();
  const accepted = { ...original, maxActiveJobs: 3 };
  api.saveAppSettings = vi.fn(async () => accepted);
  await browser.settings.save({ ...original, maxActiveJobs: 4 });
  expect(browser.settings.document).toEqual(accepted);
  expect(browser.settings.status).toBe("current");
  expect(browser.settings.attempt).toEqual({ value: 4, state: "acknowledged" });
});

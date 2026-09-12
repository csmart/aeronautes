import { afterEach, expect, it, vi } from "vitest";
import type { AppSettingsDTO } from "../../lib/api";
import { BrowserState } from "./browserState.svelte";
import { newFakeApi } from "./fakeApi";

const original: AppSettingsDTO = {
  maxActiveJobs: 2, defaultProfile: "balanced", auditRetentionDays: 17, jobRetentionDays: 11,
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
  await browser.loadAppSettings();
  expect(browser.appSettings).toEqual(original);
  expect(api.appSettings).toHaveBeenCalledExactlyOnceWith();
  expect(api.saveAppSettings).not.toHaveBeenCalled();
});

it("awaits the acknowledged document and preserves every other field", async () => {
  const { api, browser } = fixture();
  const reply = deferred();
  api.saveAppSettings = vi.fn(() => reply.promise);
  await browser.loadAppSettings();
  const requested = { ...original, maxActiveJobs: 4 };
  const saving = browser.saveAppSettings(requested);
  expect(browser.appSettings).toEqual(original);
  expect(api.saveAppSettings).toHaveBeenCalledExactlyOnceWith(requested);
  reply.resolve(requested);
  await saving;
  expect(browser.appSettings).toEqual(requested);
});

it.each([false, true])("qualifies a rejected Get and explicitly recovers it (cached %s)", async (cached) => {
  const { api, browser } = fixture();
  if (cached) await browser.loadAppSettings();
  api.appSettings = vi.fn().mockRejectedValueOnce(new Error("private settings read detail"))
    .mockResolvedValueOnce({ ...original, maxActiveJobs: 8 });
  await browser.loadAppSettings();
  expect(browser.appSettings).toEqual(cached ? original : null);
  expect(browser.appSettingsStatus, "BUDGET-READ-STATUS").toBe(cached ? "stale" : "unavailable");
  expect(browser.canSaveAppSettings, "BUDGET-READ-ADMISSION").toBe(false);
  expect(browser.appSettingsOperation).toBeNull();
  await browser.loadAppSettings();
  expect(browser.appSettings).toEqual({ ...original, maxActiveJobs: 8 });
  expect(browser.appSettingsStatus).toBe("current");
  expect(browser.canSaveAppSettings).toBe(true);
  expect(api.saveAppSettings).not.toHaveBeenCalled();
});

it.each([false, true])("one pending Get excludes another Get and all Sets (cached %s)", async (cached) => {
  const { api, browser } = fixture();
  if (cached) await browser.loadAppSettings();
  const reply = deferred();
  api.appSettings = vi.fn(() => reply.promise);
  const reading = browser.loadAppSettings();
  const duplicate = browser.loadAppSettings();
  const refusal = browser.saveAppSettings({ ...original, maxActiveJobs: 4 }).catch((error: unknown) => error);
  expect.soft(api.appSettings, "BUDGET-GET-OWNER").toHaveBeenCalledTimes(1);
  expect.soft(api.saveAppSettings, "BUDGET-GET-BLOCKS-SET").not.toHaveBeenCalled();
  reply.resolve({ ...original, maxActiveJobs: 8 });
  await Promise.all([reading, duplicate]);
  expect(await refusal).toBeInstanceOf(Error);
  expect(browser.appSettings).toEqual({ ...original, maxActiveJobs: 8 });
  expect(browser.appSettingsOperation).toBeNull();
  expect(browser.canSaveAppSettings).toBe(true);
});

it("one pending Set excludes a Get and duplicate Set until acknowledgement", async () => {
  const { api, browser } = fixture();
  await browser.loadAppSettings();
  const reply = deferred();
  api.saveAppSettings = vi.fn(() => reply.promise);
  const saving = browser.saveAppSettings({ ...original, maxActiveJobs: 4 });
  const reading = browser.loadAppSettings();
  const duplicate = browser.saveAppSettings({ ...original, maxActiveJobs: 8 }).catch((error: unknown) => error);
  expect.soft(api.appSettings, "BUDGET-SET-BLOCKS-GET").toHaveBeenCalledTimes(1);
  expect.soft(api.saveAppSettings, "BUDGET-SET-OWNER").toHaveBeenCalledTimes(1);
  expect.soft(browser.appSettingsAttempt).toEqual({ value: 4, state: "pending" });
  reply.resolve({ ...original, maxActiveJobs: 4 });
  await Promise.all([saving, reading]);
  expect(await duplicate).toBeInstanceOf(Error);
  expect(browser.appSettings).toEqual({ ...original, maxActiveJobs: 4 });
  expect(browser.appSettingsAttempt).toEqual({ value: 4, state: "acknowledged" });
  expect(browser.appSettingsOperation).toBeNull();
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
  await browser.loadAppSettings();
  await expect(browser.saveAppSettings({ ...original, maxActiveJobs: 4 })).rejects.toBe(failure);
  expect(browser.appSettings).toEqual(original);
  expect(browser.appSettingsAttempt, "BUDGET-UNCERTAIN").toEqual({ value: 4, state: "unconfirmed" });
  expect(browser.appSettingsStatus).toBe("unconfirmed");
  expect(browser.appSettingsOperation).toBeNull();
  await expect(browser.saveAppSettings({ ...original, maxActiveJobs: 8 })).rejects.toBeInstanceOf(Error);
  expect(api.saveAppSettings, "BUDGET-RECOVERY-REQUIRED").toHaveBeenCalledTimes(1);
  api.appSettings = vi.fn().mockRejectedValueOnce(new Error("private Get failure"))
    .mockResolvedValueOnce({ ...provider });
  await browser.loadAppSettings();
  expect(browser.canSaveAppSettings).toBe(false);
  expect(browser.appSettingsStatus).toBe("unconfirmed");
  expect(browser.appSettingsAttempt?.state).toBe("unconfirmed");
  await browser.loadAppSettings();
  expect(browser.appSettings).toEqual(provider);
  expect(browser.canSaveAppSettings).toBe(true);
  expect(browser.appSettingsStatus).toBe("current");
  expect(browser.appSettingsAttempt?.state, "BUDGET-READ-NOT-ACK").toBe("unconfirmed");
  expect(api.saveAppSettings).toHaveBeenCalledTimes(1);
  api.saveAppSettings = vi.fn(async (settings) => ({ ...settings }));
  await browser.saveAppSettings({ ...browser.appSettings!, maxActiveJobs: 8 });
  expect(browser.appSettings).toEqual({ ...provider, maxActiveJobs: 8 });
  expect(browser.appSettingsAttempt).toEqual({ value: 8, state: "acknowledged" });
});

it("refuses a cold or stale write without issuing it", async () => {
  const { api, browser } = fixture();
  await expect(browser.saveAppSettings(original), "BUDGET-COLD-WRITE").rejects.toBeInstanceOf(Error);
  expect(api.saveAppSettings).not.toHaveBeenCalled();
  await browser.loadAppSettings();
  api.appSettings = vi.fn(async () => { throw new Error("owned stale Get"); });
  await browser.loadAppSettings();
  await expect(browser.saveAppSettings(original), "BUDGET-STALE-WRITE").rejects.toBeInstanceOf(Error);
  expect(api.saveAppSettings).not.toHaveBeenCalled();
});

it("publishes the acknowledged reply rather than guessing it from the input", async () => {
  const { api, browser } = fixture();
  await browser.loadAppSettings();
  const accepted = { ...original, maxActiveJobs: 8 };
  api.saveAppSettings = vi.fn(async () => accepted);
  await browser.saveAppSettings({ ...original, maxActiveJobs: 4 });
  expect(browser.appSettings).toEqual(accepted);
  expect(browser.appSettingsStatus).toBe("current");
  expect(browser.appSettingsAttempt).toEqual({ value: 4, state: "acknowledged" });
});

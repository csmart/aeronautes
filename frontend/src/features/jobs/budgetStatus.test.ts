import { afterEach, expect, it, vi } from "vitest";
import { flushSync, mount, unmount } from "svelte";
import type { AppSettingsDTO } from "../../lib/api";
import { BrowserState } from "../browser/browserState.svelte";
import { newFakeApi } from "../browser/fakeApi";
import JobsDrawer from "./JobsDrawer.svelte";

const original: AppSettingsDTO = {
  maxActiveJobs: 2, defaultProfile: "balanced", auditRetentionDays: 17, jobRetentionDays: 11, downloadObjectCeiling: 250_000,
  browserRetentionCeiling: 100_000, maxQueuedJobs: 16, maxConnsPerHost: 16,
  reclaimReplacedFragments: true,
};
let drawer: ReturnType<typeof mount> | null = null;
const releasePending: (() => void)[] = [];

afterEach(async () => {
  for (const release of releasePending.splice(0)) release();
  await Promise.resolve();
  if (drawer) await unmount(drawer);
  drawer = null;
  document.body.innerHTML = "";
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
  api.jobs = vi.fn(async () => []);
  api.appSettings = vi.fn(async () => ({ ...original }));
  api.saveAppSettings = vi.fn(async (settings) => ({ ...settings }));
  return { api, browser: new BrowserState(api) };
}

function select(): HTMLSelectElement | null {
  return [...document.querySelectorAll("label")].find((label) =>
    label.querySelector("span")?.textContent?.trim() === "Active-job budget")?.querySelector("select") ?? null;
}

function button(text: string): HTMLButtonElement {
  const found = [...document.querySelectorAll<HTMLButtonElement>("button")]
    .find((node) => node.textContent?.trim() === text);
  expect(found, `BUDGET-BUTTON: ${text}`).toBeDefined();
  return found!;
}

function feedback(): string {
  return document.querySelector(".budget-status")?.textContent ?? "";
}

async function show(browser: BrowserState) {
  drawer = mount(JobsDrawer, { target: document.body, props: { browser } });
  flushSync();
  document.querySelector<HTMLButtonElement>(".bar")!.click();
  flushSync();
  await vi.waitFor(() => expect(browser.recoveryStatus).not.toBeNull());
}

function choose(value: string) {
  const control = select();
  expect(control).not.toBeNull();
  control!.value = value;
  control!.dispatchEvent(new Event("change", { bubbles: true }));
}

it("preserves the whole document and choices while awaiting acknowledgement", async () => {
  const { api, browser } = fixture();
  const reply = deferred();
  api.saveAppSettings = vi.fn(() => reply.promise);
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  choose("4");
  flushSync();
  expect(select()?.disabled).toBe(true);
  expect(browser.appSettings).toEqual(original);
  expect(api.saveAppSettings).toHaveBeenCalledExactlyOnceWith({ ...original, maxActiveJobs: 4 });
  reply.resolve({ ...original, maxActiveJobs: 4 });
  await vi.waitFor(() => expect(select()?.disabled).toBe(false));
  expect(select()?.value).toBe("4");
  expect(browser.appSettings).toEqual({ ...original, maxActiveJobs: 4 });
  expect([...select()!.options].map((option) => option.value)).toEqual(["1", "2", "4", "8"]);
});

it("shows a cold pending read then unavailable feedback and a read-only retry", async () => {
  const { api, browser } = fixture();
  const reply = deferred();
  api.appSettings = vi.fn().mockReturnValueOnce(reply.promise).mockResolvedValueOnce({ ...original });
  await show(browser);
  expect(feedback(), "BUDGET-LOADING").toContain("Loading settings");
  expect(api.saveAppSettings).not.toHaveBeenCalled();
  reply.reject(new Error("private cold Get failure"));
  await vi.waitFor(() => expect(browser.appSettingsOperation).toBeNull());
  flushSync();
  expect(feedback()).toContain("Settings are unavailable");
  expect(document.body.textContent).not.toContain("private cold Get");
  expect(select()?.disabled ?? true).toBe(true);
  button("Refresh settings").click();
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  expect(api.appSettings).toHaveBeenCalledTimes(2);
  expect(api.saveAppSettings).not.toHaveBeenCalled();
  expect(select()?.disabled).toBe(false);
});

it("shows cold failed settings as unavailable rather than an absent control", async () => {
  const { api, browser } = fixture();
  api.appSettings = vi.fn(async () => { throw new Error("private read failure"); });
  await show(browser);
  expect(feedback(), "BUDGET-GET-FAILURE").toContain("Settings are unavailable");
  expect(button("Refresh settings").disabled).toBe(false);
  expect(api.saveAppSettings).not.toHaveBeenCalled();
});

it("qualifies a cached failed read and blocks writes until a successful refresh", async () => {
  const { api, browser } = fixture();
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  api.appSettings = vi.fn().mockRejectedValueOnce(new Error("private refresh failure"))
    .mockResolvedValueOnce({ ...original, maxActiveJobs: 8 });
  await browser.loadAppSettings();
  flushSync();
  expect(select()?.value).toBe("2");
  expect(select()?.disabled, "BUDGET-STALE-EDIT").toBe(true);
  expect(feedback()).toContain("stale");
  expect(feedback()).toContain("Last observed budget: 2");
  button("Refresh settings").click();
  await vi.waitFor(() => expect(select()?.value).toBe("8"));
  expect(select()?.disabled).toBe(false);
  expect(api.saveAppSettings).not.toHaveBeenCalled();
});

it.each([false, true])("retains an uncertain save and observes recovery without repeating it (applied %s)", async (applied) => {
  const { api, browser } = fixture();
  let provider = { ...original };
  let settled = false;
  const reply = deferred();
  api.appSettings = vi.fn(async () => ({ ...provider }));
  api.saveAppSettings = vi.fn(async (settings) => {
    if (applied) provider = { ...settings };
    try { return await reply.promise; } finally { settled = true; }
  });
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  choose("4");
  flushSync();
  reply.reject(new Error(applied ? "owned applied Set reply lost" : "owned Set refused before effect"));
  await vi.waitFor(() => expect(settled).toBe(true));
  flushSync();
  expect.soft(feedback(), "BUDGET-SET-UNCONFIRMED").toContain("Budget change to 4 is unconfirmed");
  expect.soft(select()?.disabled, "BUDGET-UNCERTAIN-EDIT").toBe(true);
  expect(browser.appSettings).toEqual(original);
  expect(feedback()).toContain("Last observed budget: 2");
  expect(document.body.textContent).not.toMatch(/owned (?:applied|Set)/);
  button("Refresh settings").click();
  await vi.waitFor(() => expect(browser.appSettingsOperation).toBeNull());
  flushSync();
  expect(select()?.value, "BUDGET-SAME-VALUE-READBACK").toBe(String(provider.maxActiveJobs));
  expect(select()?.disabled).toBe(false);
  expect(browser.appSettings).toEqual(provider);
  expect(feedback(), "BUDGET-READBACK-NOT-ACK").toContain("Budget change to 4 is unconfirmed");
  expect(feedback()).not.toContain("Budget change acknowledged");
  expect(api.appSettings).toHaveBeenCalledTimes(2);
  expect(api.saveAppSettings).toHaveBeenCalledTimes(1);
});

it("does not replace uncertainty with a failed read or an automatic remount read", async () => {
  const { api, browser } = fixture();
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  api.saveAppSettings = vi.fn(async () => { throw new Error("owned uncertain reply"); });
  choose("4");
  await vi.waitFor(() => expect(browser.appSettingsAttempt?.state).toBe("unconfirmed"));
  api.appSettings = vi.fn(async () => { throw new Error("private recovery Get failure"); });
  button("Refresh settings").click();
  await vi.waitFor(() => expect(browser.appSettingsOperation).toBeNull());
  flushSync();
  expect(feedback(), "BUDGET-RECOVERY-GET-FAILURE").toContain("Settings could not be refreshed");
  expect(feedback()).toContain("stale");
  await unmount(drawer!);
  drawer = null;
  await show(browser);
  expect(api.appSettings, "BUDGET-EXPLICIT-READ").toHaveBeenCalledTimes(1);
  expect(feedback()).toContain("Budget change to 4 is unconfirmed");
  expect(select()?.disabled).toBe(true);
  expect(browser.appSettings).toEqual(original);
  expect(api.saveAppSettings).toHaveBeenCalledTimes(1);
});

it("rejects a second same-turn change and restores the admitted choice", async () => {
  const { api, browser } = fixture();
  const reply = deferred();
  api.saveAppSettings = vi.fn(() => reply.promise);
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  choose("4");
  choose("8");
  flushSync();
  expect.soft(api.saveAppSettings, "BUDGET-DUPLICATE-DOM").toHaveBeenCalledTimes(1);
  expect(select()?.value, "BUDGET-ADMITTED-CHOICE").toBe("4");
  expect(feedback()).toContain("Saving budget 4");
  choose("8");
  flushSync();
  expect(select()?.value).toBe("4");
  expect(api.saveAppSettings).toHaveBeenCalledTimes(1);
  reply.resolve({ ...original, maxActiveJobs: 4 });
  await vi.waitFor(() => expect(select()?.disabled).toBe(false));
  expect(select()?.value).toBe("4");
  expect(feedback()).toContain("Budget change acknowledged");
});

it("keeps one settings owner through tabs, collapse, polling and remount", async () => {
  const { api, browser } = fixture();
  const reply = deferred();
  api.saveAppSettings = vi.fn(() => reply.promise);
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  choose("4");
  flushSync();
  button("Recent changes").click();
  flushSync();
  button("Jobs").click();
  document.querySelector<HTMLButtonElement>(".bar")!.click();
  flushSync();
  document.querySelector<HTMLButtonElement>(".bar")!.click();
  await browser.loadJobs();
  await unmount(drawer!);
  drawer = null;
  await show(browser);
  expect.soft(api.appSettings, "BUDGET-REMOUNT-GET-OWNER").toHaveBeenCalledTimes(1);
  expect(select()?.disabled, "BUDGET-REMOUNT-SET-OWNER").toBe(true);
  expect(feedback()).toContain("Saving budget 4");
  reply.resolve({ ...original, maxActiveJobs: 4 });
  await vi.waitFor(() => expect(select()?.disabled).toBe(false));
  expect(select()?.value).toBe("4");
  expect(feedback()).toContain("Budget change acknowledged");
  expect(api.saveAppSettings).toHaveBeenCalledTimes(1);
});

it("blocks writes and duplicate refresh clicks while a cached Get is pending", async () => {
  const { api, browser } = fixture();
  await show(browser);
  await vi.waitFor(() => expect(select()?.value).toBe("2"));
  const reply = deferred();
  api.appSettings = vi.fn(() => reply.promise);
  button("Refresh settings").click();
  button("Refresh settings").click();
  choose("8");
  flushSync();
  expect(api.appSettings, "BUDGET-DUPLICATE-GET-DOM").toHaveBeenCalledTimes(1);
  expect(api.saveAppSettings, "BUDGET-PENDING-GET-DOM").not.toHaveBeenCalled();
  expect(select()?.disabled).toBe(true);
  reply.resolve({ ...original, maxActiveJobs: 4 });
  await vi.waitFor(() => expect(select()?.disabled).toBe(false));
  expect(select()?.value).toBe("4");
});

// ADR 0081 decision 5's option, as the operator reaches it: the control
// states which way a replacement will go, saves the other way, and shows
// the last observed value until the save is acknowledged.
function reclaimBox(): HTMLInputElement | null {
  return document.querySelector<HTMLInputElement>("input.reclaim-fragments");
}

function reclaimFeedback(): string {
  return [...document.querySelectorAll(".ceiling-status")]
    .map((node) => node.textContent ?? "").join(" ");
}

it("offers the replaced-segment option and says which way it is set", async () => {
  const { api, browser } = fixture();
  await show(browser);
  await vi.waitFor(() => expect(reclaimBox()).not.toBeNull());
  expect(reclaimBox()!.checked, "RECLAIM-DEFAULT").toBe(true);
  expect(reclaimFeedback()).toContain("removed once the replacement verifies");

  // Turning it off saves false and nothing else.
  reclaimBox()!.checked = false;
  reclaimBox()!.dispatchEvent(new Event("change", { bubbles: true }));
  await vi.waitFor(() => expect(api.saveAppSettings).toHaveBeenCalledTimes(1));
  expect(api.saveAppSettings).toHaveBeenCalledWith({ ...original, reclaimReplacedFragments: false });
  await vi.waitFor(() => expect(reclaimBox()!.checked).toBe(false));
  expect(reclaimFeedback()).toContain("are kept");
});

it("keeps showing the last observed setting until a save is acknowledged", async () => {
  const { api, browser } = fixture();
  await show(browser);
  await vi.waitFor(() => expect(reclaimBox()).not.toBeNull());
  const reply = deferred();
  api.saveAppSettings = vi.fn(() => reply.promise);
  reclaimBox()!.checked = false;
  reclaimBox()!.dispatchEvent(new Event("change", { bubbles: true }));
  flushSync();
  // The click is not the state: until the backend answers, the control
  // still shows what was read.
  expect(reclaimBox()!.checked, "RECLAIM-PENDING").toBe(true);
  reply.resolve({ ...original, reclaimReplacedFragments: false });
  await vi.waitFor(() => expect(reclaimBox()!.checked).toBe(false));
});
